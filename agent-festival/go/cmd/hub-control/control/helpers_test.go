package control

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// Test fixtures for the annual-report demo: the W01 snapshot, a policy with
// three remote roles and two required checkers, and a four-node graph.

const snapSHA = "52c49dcdc00d4f5436adabc6561044d7a539e9bb0a8ebbdb692a1da6237459da"

var snapshot = Material{ID: "cmb-2025-annual-report-a", SHA256: snapSHA, Title: "招商银行 2025 年度报告（A 股）"}

func demoPolicy(version string) Policy {
	return Policy{
		Version: version,
		Capabilities: map[string]Capability{
			"material": {Targets: []string{"agent:material"}, Queryable: true, Reusable: true},
			"analysis": {Targets: []string{"agent:analysis"}, Queryable: true},
			"writer":   {Targets: []string{"agent:writer"}, Queryable: true},
		},
		MaxNodes:          8,
		MaxLoopIterations: 3,
		RequiredChecks:    []RequiredCheck{{Checker: "calc", RuleVersion: "r1"}, {Checker: "citation", RuleVersion: "r1"}},
		ReuseMaxAgeHrs:    24 * 30,
	}
}

func demoGraph() Graph {
	return Graph{Report: "write", Nodes: []Node{
		{ID: "extract", Capability: "material"},
		{ID: "operating", Capability: "analysis", DependsOn: []string{"extract"}},
		{ID: "asset_quality", Capability: "analysis", DependsOn: []string{"extract"}},
		{ID: "write", Capability: "writer", DependsOn: []string{"operating", "asset_quality"}, MaxIterations: 2},
	}}
}

type env struct {
	t                                              *testing.T
	s                                              *Store
	path                                           string
	now                                            time.Time
	admin, planner, alice, bob, ex1, ex2, calc, ci Principal
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, path: filepath.Join(t.TempDir(), "hub.db"), now: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)}
	e.open()
	for _, p := range []Principal{
		{ID: "admin", Kind: KindAdmin},
		{ID: "planner", Kind: KindAgent, Roles: []string{"propose"}},
		{ID: "alice", Kind: KindUser, Roles: []string{"propose", "approve", "accept", "operate", "review"}},
		{ID: "bob", Kind: KindUser},
		{ID: "ex1", Kind: KindExecutor},
		{ID: "ex2", Kind: KindExecutor},
		{ID: "calc", Kind: KindChecker, Roles: []string{"check:calc"}},
		{ID: "cite", Kind: KindChecker, Roles: []string{"check:citation"}},
	} {
		must(t, e.s.PutPrincipal("test", p, "secret-"+p.ID))
	}
	e.reload()
	_, err := e.s.PublishPolicy(e.admin, demoPolicy("ar-1"))
	must(t, err)
	must(t, e.s.RegisterMaterial(e.admin, snapshot))
	return e
}

func (e *env) open() {
	s, err := Open(e.path)
	must(e.t, err)
	s.Now = func() time.Time { return e.now }
	e.s = s
	e.t.Cleanup(func() { s.Close() })
}

// reopen simulates a process restart on the same database file.
func (e *env) reopen() {
	e.s.Close()
	e.open()
	e.reload()
}

func (e *env) reload() {
	get := func(id string) Principal {
		_, p, err := e.s.Login(id, "secret-"+id)
		must(e.t, err)
		return p
	}
	e.admin, e.planner, e.alice, e.bob = get("admin"), get("planner"), get("alice"), get("bob")
	e.ex1, e.ex2, e.calc, e.ci = get("ex1"), get("ex2"), get("calc"), get("cite")
}

func (e *env) advance(d time.Duration) { e.now = e.now.Add(d) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if CodeOf(err) != code {
		t.Fatalf("want %s refusal, got %v", code, err)
	}
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// adopted proposes the demo graph as the planner and adopts it as alice.
func (e *env) adopted(cmd string) Adoption {
	e.t.Helper()
	pr, err := e.s.Propose(e.planner, ProposeInput{Goal: "招商银行 2025 年报经营与资产质量简报", Material: []Material{snapshot}, Graph: demoGraph()})
	must(e.t, err)
	if !pr.Mountable {
		e.t.Fatalf("demo graph not mountable: %v", pr.Problems)
	}
	a, err := e.s.Adopt(e.alice, AdoptInput{CommandID: cmd, Task: pr.Task, Rev: pr.Rev, Digest: pr.Digest})
	must(e.t, err)
	return a
}

// run is a lease together with the executor that holds it.
type run struct {
	*Lease
	p Principal
}

func (e *env) leaseAs(p Principal, task string) *run {
	e.t.Helper()
	l, err := e.s.Lease(p, LeaseInput{Task: task})
	must(e.t, err)
	if l == nil {
		e.t.Fatalf("no lease for %s", task)
	}
	return &run{Lease: l, p: p}
}

func (r *run) ref() LeaseRef { return LeaseRef{Task: r.Task, Token: r.Token} }

func nodeInput(node string, extra map[string]any) json.RawMessage {
	m := map[string]any{"snapshot": snapshot, "instructions": "role:" + node}
	for k, v := range extra {
		m[k] = v
	}
	return raw(m)
}

// runNode claims a node and commits body as its result, with one remote call.
func (e *env) runNode(l *run, node string, iter int, body any, target string) ResultView {
	e.t.Helper()
	c, err := e.s.Claim(l.p, ClaimInput{Task: l.Task, Token: l.Token, Node: node, Iter: iter, Sources: []Material{snapshot}, Material: nodeInput(node, nil)})
	must(e.t, err)
	if c.Action == "reuse" {
		return *c.Result
	}
	if c.Action != "run" {
		e.t.Fatalf("claim %s: action %s", node, c.Action)
	}
	if c.ReuseCandidate != nil {
		r, err := e.s.Commit(l.p, CommitInput{Task: l.Task, Token: l.Token, O: c.O, A: c.A, Outcome: OutcomeCompleted, Body: c.ReuseCandidate.Body, ReusedFrom: c.ReuseCandidate.O})
		must(e.t, err)
		return r.Result
	}
	p := l.p
	call, err := e.s.BindCall(p, BindInput{Task: l.Task, Token: l.Token, O: c.O, A: c.A, Name: "ask", Target: target, Payload: raw(map[string]any{"node": node})})
	must(e.t, err)
	_, err = e.s.Dispatch(p, CallRef{Task: l.Task, Token: l.Token, B: call.B})
	must(e.t, err)
	_, err = e.s.Observe(p, ObserveInput{Task: l.Task, Token: l.Token, B: call.B, Kind: "accepted", RemoteRef: "a2a-task-" + node})
	must(e.t, err)
	_, err = e.s.Observe(p, ObserveInput{Task: l.Task, Token: l.Token, B: call.B, Kind: "succeeded", Result: raw(body)})
	must(e.t, err)
	r, err := e.s.Commit(p, CommitInput{Task: l.Task, Token: l.Token, O: c.O, A: c.A, Outcome: OutcomeCompleted, Body: raw(body)})
	must(e.t, err)
	return r.Result
}

func report(nim float64) map[string]any {
	return map[string]any{
		"snapshot": snapshot.ID,
		"indicators": []any{map[string]any{"id": "net_interest_margin", "values": map[string]any{"2025": nim, "2024": 1.98},
			"unit": "%", "change": map[string]any{"kind": "pp", "value": nim - 1.98},
			"citation": map[string]any{"pdf_page": 16, "printed_page": 15, "quote": "净利息收益率"}}},
	}
}

// runAll executes the whole graph and returns the report result.
func (e *env) runAll(l *run) ResultView {
	e.runNode(l, "extract", 0, map[string]any{"indicators": 8}, "agent:material")
	e.runNode(l, "operating", 0, map[string]any{"section": "operating"}, "agent:analysis")
	e.runNode(l, "asset_quality", 0, map[string]any{"section": "asset_quality"}, "agent:analysis")
	return e.runNode(l, "write", 0, report(1.87), "agent:writer")
}

func (e *env) passChecks(artifact string) {
	e.t.Helper()
	_, err := e.s.RecordCheck(e.calc, CheckInput{Artifact: artifact, Checker: "calc", CheckerVersion: "1.0", RuleVersion: "r1", Verdict: VerdictPass})
	must(e.t, err)
	_, err = e.s.RecordCheck(e.ci, CheckInput{Artifact: artifact, Checker: "citation", CheckerVersion: "1.0", RuleVersion: "r1", Verdict: VerdictPass})
	must(e.t, err)
}

func (e *env) view(task string) *TaskView {
	e.t.Helper()
	v, err := e.s.View(e.alice, task)
	must(e.t, err)
	return v
}

func (e *env) node(task, node string) NodeView {
	e.t.Helper()
	for _, n := range e.view(task).Nodes {
		if n.Node == node {
			return n
		}
	}
	e.t.Fatalf("node %s not in view", node)
	return NodeView{}
}

func (e *env) countEvents(task, kind string) int {
	e.t.Helper()
	ev, err := e.s.Events(task, 0, 1000)
	must(e.t, err)
	n := 0
	for _, x := range ev {
		if x.Kind == kind {
			n++
		}
	}
	return n
}
