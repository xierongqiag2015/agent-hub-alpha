package control

import (
	"strings"
	"testing"
	"time"
)

// Each test names the #16 acceptance item (A01–A15) or the #20 matrix row it
// covers, at the scope of this module: a single control instance on one
// database file, driven the way the Java Graph host and the checkers drive it.

func TestA01_HappyPathDeliversCheckedReport(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("cmd-adopt-1")
	if a.Policy != "ar-1" || a.Assurance != AssuranceDemo {
		t.Fatalf("adoption = %+v", a)
	}
	r := e.leaseAs(e.ex1, a.Task)
	rep := e.runAll(r)
	must(t, e.s.Release(e.ex1, r.ref()))

	v := e.view(a.Task)
	if v.Gate.Status != GateUncovered {
		t.Fatalf("before checks gate = %s", v.Gate.Status)
	}
	e.passChecks(rep.Artifact)
	if g := e.view(a.Task).Gate; g.Status != GateReady || g.Artifact != rep.Artifact {
		t.Fatalf("gate = %+v", g)
	}
	d, err := e.s.Accept(e.alice, AcceptInput{CommandID: "cmd-accept-1", Task: a.Task, Artifact: rep.Artifact})
	must(t, err)
	if d.Artifact != rep.Artifact || len(d.Checks) != 2 || d.Assurance != AssuranceDemo {
		t.Fatalf("delivery = %+v", d)
	}
	v = e.view(a.Task)
	if v.Status != "delivered" || v.Gate.Status != GateDelivered {
		t.Fatalf("view after delivery: %s / %s", v.Status, v.Gate.Status)
	}
	for _, n := range v.Nodes {
		if n.State != OutcomeCompleted || len(n.Attempts) != 1 || len(n.Calls) != 1 || n.Calls[0].State != CallSucceeded {
			t.Fatalf("node %s: %+v", n.Node, n)
		}
	}
	// the executor is closed out once the task is delivered
	_, err = e.s.Lease(e.ex1, LeaseInput{Task: a.Task})
	wantCode(t, err, CodeConflict)
}

func TestA02_InvalidGraphRejectedBeforeMount(t *testing.T) {
	e := newEnv(t)
	bad := Graph{Report: "write", Nodes: []Node{
		{ID: "extract", Capability: "material"},
		{ID: "scrape", Capability: "web_crawler"},                                            // unregistered capability
		{ID: "loop", Capability: "analysis", DependsOn: []string{"write"}, MaxIterations: 9}, // unbounded loop
		{ID: "write", Capability: "writer", DependsOn: []string{"loop", "ghost"}},            // cycle + unknown dep
	}}
	pr, err := e.s.Propose(e.planner, ProposeInput{Goal: "x", Material: []Material{snapshot}, Graph: bad})
	must(t, err)
	if pr.Mountable || len(pr.Problems) < 4 {
		t.Fatalf("problems = %v", pr.Problems)
	}
	for _, want := range []string{"未登记的能力", "循环上界 9", "不存在的节点 ghost", "依赖环"} {
		if !strings.Contains(strings.Join(pr.Problems, "|"), want) {
			t.Fatalf("missing %q in %v", want, pr.Problems)
		}
	}
	_, err = e.s.Adopt(e.alice, AdoptInput{CommandID: "c1", Task: pr.Task, Rev: pr.Rev, Digest: pr.Digest})
	wantCode(t, err, CodeBlocked)
	if l, err := e.s.Lease(e.ex1, LeaseInput{}); err != nil || l != nil {
		t.Fatalf("a rejected plan must not be leasable: %v %v", l, err)
	}
	// an unregistered material snapshot is also refused
	pr2, err := e.s.Propose(e.planner, ProposeInput{Goal: "x", Material: []Material{{ID: snapshot.ID, SHA256: strings.Repeat("0", 64)}}, Graph: demoGraph()})
	must(t, err)
	if pr2.Mountable {
		t.Fatal("unregistered snapshot accepted")
	}
}

func TestA03_SameCommandSameContentReturnsOriginal(t *testing.T) {
	e := newEnv(t)
	pr, err := e.s.Propose(e.planner, ProposeInput{Goal: "g", Material: []Material{snapshot}, Graph: demoGraph()})
	must(t, err)
	in := AdoptInput{CommandID: "click-7f3a", Task: pr.Task, Rev: pr.Rev, Digest: pr.Digest}
	a1, err := e.s.Adopt(e.alice, in)
	must(t, err)
	a2, err := e.s.Adopt(e.alice, in)
	must(t, err)
	if !a2.Replayed || a1.Task != a2.Task || a1.Digest != a2.Digest {
		t.Fatalf("retry = %+v, first = %+v", a2, a1)
	}
	if n := e.countEvents(pr.Task, "graph_adopted"); n != 1 {
		t.Fatalf("graph_adopted events = %d, want 1", n)
	}
	// a second, different click on the same plan does not start a second run
	_, err = e.s.Adopt(e.alice, AdoptInput{CommandID: "click-other", Task: pr.Task, Rev: pr.Rev, Digest: pr.Digest})
	wantCode(t, err, CodeConflict)
	// the same command id from another person is a different command
	_, err = e.s.Adopt(e.bob, in)
	wantCode(t, err, CodeForbidden)
}

func TestA04_SameIdentityChangedInputIsConflict(t *testing.T) {
	e := newEnv(t)
	pr1, _ := e.s.Propose(e.planner, ProposeInput{Goal: "g1", Material: []Material{snapshot}, Graph: demoGraph()})
	pr2, _ := e.s.Propose(e.planner, ProposeInput{Goal: "g2", Material: []Material{snapshot}, Graph: demoGraph()})
	_, err := e.s.Adopt(e.alice, AdoptInput{CommandID: "click-1", Task: pr1.Task, Rev: pr1.Rev, Digest: pr1.Digest})
	must(t, err)
	_, err = e.s.Adopt(e.alice, AdoptInput{CommandID: "click-1", Task: pr2.Task, Rev: pr2.Rev, Digest: pr2.Digest})
	wantCode(t, err, CodeConflict)
	if v := e.view(pr2.Task); v.Status != "draft" {
		t.Fatalf("second task changed to %s", v.Status)
	}
	if v := e.view(pr1.Task); v.Status != "adopted" {
		t.Fatalf("original changed to %s", v.Status)
	}

	// node level: o is bound to the input material of its first claim
	r := e.leaseAs(e.ex1, pr1.Task)
	c, err := e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	must(t, err)
	_, err = e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", map[string]any{"page": 99})})
	wantCode(t, err, CodeConflict)

	// call level: b is bound to its payload
	call, err := e.s.BindCall(e.ex1, BindInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Name: "ask", Target: "agent:material", Payload: raw(map[string]any{"q": 1})})
	must(t, err)
	_, err = e.s.BindCall(e.ex1, BindInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Name: "ask", Target: "agent:material", Payload: raw(map[string]any{"q": 2})})
	wantCode(t, err, CodeConflict)
	same, err := e.s.BindCall(e.ex1, BindInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Name: "ask", Target: "agent:material", Payload: raw(map[string]any{"q": 1})})
	must(t, err)
	if same.B != call.B || same.Kappa != call.Kappa {
		t.Fatal("rebinding the same payload must return the same call")
	}

	// result level: one formal result per o
	_, err = e.s.Dispatch(e.ex1, CallRef{Task: r.Task, Token: r.Token, B: call.B})
	must(t, err)
	_, err = e.s.Observe(e.ex1, ObserveInput{Task: r.Task, Token: r.Token, B: call.B, Kind: "succeeded", Result: raw(map[string]any{"v": 1})})
	must(t, err)
	_, err = e.s.Commit(e.ex1, CommitInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Outcome: OutcomeCompleted, Body: raw(map[string]any{"v": 1})})
	must(t, err)
	_, err = e.s.Commit(e.ex1, CommitInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Outcome: OutcomeCompleted, Body: raw(map[string]any{"v": 2})})
	wantCode(t, err, CodeConflict)
}

func TestA05_RestartReusesCompletedNodesWithZeroNewAttempts(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	r1 := e.leaseAs(e.ex1, a.Task)
	ext := e.runNode(r1, "extract", 0, map[string]any{"indicators": 8}, "agent:material")
	op := e.runNode(r1, "operating", 0, map[string]any{"section": "operating"}, "agent:analysis")
	// ex1 is mid-way through asset_quality when the process is killed
	c, err := e.s.Claim(e.ex1, ClaimInput{Task: r1.Task, Token: r1.Token, Node: "asset_quality", Sources: []Material{snapshot}, Material: nodeInput("asset_quality", nil)})
	must(t, err)

	e.reopen() // kill -9 + restart of the control process
	e.advance(DefaultLeaseTTL + time.Second)
	r2 := e.leaseAs(e.ex2, a.Task)
	if !r2.TakeOver || r2.Epoch != 2 {
		t.Fatalf("lease = %+v", r2.Lease)
	}
	for node, want := range map[string]ResultView{"extract": ext, "operating": op} {
		cl, err := e.s.Claim(e.ex2, ClaimInput{Task: r2.Task, Token: r2.Token, Node: node, Sources: []Material{snapshot}, Material: nodeInput(node, nil)})
		must(t, err)
		if cl.Action != "reuse" || cl.Result.Artifact != want.Artifact || cl.A != 0 {
			t.Fatalf("%s after restart: %+v", node, cl)
		}
		if n := e.node(a.Task, node); len(n.Attempts) != 1 || n.Reused != 1 {
			t.Fatalf("%s attempts=%d reused=%d, want 1/1", node, len(n.Attempts), n.Reused)
		}
	}
	// the interrupted node keeps o; the lost attempt stays on record and a new attempt starts
	cl, err := e.s.Claim(e.ex2, ClaimInput{Task: r2.Task, Token: r2.Token, Node: "asset_quality", Sources: []Material{snapshot}, Material: nodeInput("asset_quality", nil)})
	must(t, err)
	if cl.O != c.O || cl.A != 2 || cl.Action != "run" {
		t.Fatalf("resumed claim = %+v (first was %+v)", cl, c)
	}
	if n := e.node(a.Task, "asset_quality"); n.Attempts[0].State != "lost" {
		t.Fatalf("old attempt = %+v", n.Attempts[0])
	}
	// the old executor wakes up: it can no longer write anything
	_, err = e.s.Commit(e.ex1, CommitInput{Task: r1.Task, Token: r1.Token, O: c.O, A: c.A, Outcome: OutcomeCompleted, Body: raw(map[string]any{"stale": true})})
	wantCode(t, err, CodeFenced)
	_, err = e.s.Claim(e.ex1, ClaimInput{Task: r1.Task, Token: r1.Token, Node: "write", Sources: []Material{snapshot}, Material: nodeInput("write", nil)})
	wantCode(t, err, CodeFenced)
	// ...but repeating a commit that already happened is acknowledged, not an error
	dup, err := e.s.Commit(e.ex1, CommitInput{Task: r1.Task, Token: r1.Token, O: ext.O, A: ext.A, Outcome: OutcomeCompleted, Body: raw(map[string]any{"indicators": 8})})
	must(t, err)
	if !dup.Duplicate {
		t.Fatal("expected duplicate acknowledgement")
	}
}

func TestA06_LostReplyIsQueriedNotResent(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	r1 := e.leaseAs(e.ex1, a.Task)
	c, _ := e.s.Claim(e.ex1, ClaimInput{Task: r1.Task, Token: r1.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	call, _ := e.s.BindCall(e.ex1, BindInput{Task: r1.Task, Token: r1.Token, O: c.O, A: c.A, Name: "ask", Target: "agent:material", Payload: raw(map[string]any{"q": 1})})
	_, err := e.s.Dispatch(e.ex1, CallRef{Task: r1.Task, Token: r1.Token, B: call.B})
	must(t, err)
	// connection drops before any acknowledgement
	ob, err := e.s.Observe(e.ex1, ObserveInput{Task: r1.Task, Token: r1.Token, B: call.B, Kind: "timeout"})
	must(t, err)
	if ob.Call.State != CallUnknown {
		t.Fatalf("state = %s", ob.Call.State)
	}
	_, err = e.s.Dispatch(e.ex1, CallRef{Task: r1.Task, Token: r1.Token, B: call.B})
	wantCode(t, err, CodeBlocked) // contract: resend never
	_, err = e.s.Commit(e.ex1, CommitInput{Task: r1.Task, Token: r1.Token, O: c.O, A: c.A, Outcome: OutcomeCompleted, Body: raw(map[string]any{"v": 1})})
	wantCode(t, err, CodeBlocked) // cannot hide an open call behind a completed node

	// a second call that was acknowledged, then the executor dies
	call2, _ := e.s.BindCall(e.ex1, BindInput{Task: r1.Task, Token: r1.Token, O: c.O, A: c.A, Name: "ask2", Target: "agent:material", Payload: raw(map[string]any{"q": 2})})
	_, _ = e.s.Dispatch(e.ex1, CallRef{Task: r1.Task, Token: r1.Token, B: call2.B})
	_, _ = e.s.Observe(e.ex1, ObserveInput{Task: r1.Task, Token: r1.Token, B: call2.B, Kind: "accepted", RemoteRef: "a2a-77"})
	e.advance(DefaultLeaseTTL + time.Second)
	r2 := e.leaseAs(e.ex2, a.Task)
	next := map[string]string{}
	for _, u := range r2.Unresolved {
		next[u.Name] = u.NextAction + " " + u.Next
	}
	if !strings.HasPrefix(next["ask"], "manual") || !strings.HasPrefix(next["ask2"], "query") || !strings.Contains(next["ask2"], "a2a-77") {
		t.Fatalf("unresolved = %+v", r2.Unresolved)
	}
	// the new owner works under o with a new attempt; still no resend
	c2, err := e.s.Claim(e.ex2, ClaimInput{Task: r2.Task, Token: r2.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	must(t, err)
	if c2.O != c.O || c2.A != 2 {
		t.Fatalf("claim after takeover = %+v", c2)
	}
	_, err = e.s.Dispatch(e.ex2, CallRef{Task: r2.Task, Token: r2.Token, B: call2.B})
	wantCode(t, err, CodeBlocked) // accepted: query, do not resend
	// the old executor's late reply is kept but not applied
	late, err := e.s.Observe(e.ex1, ObserveInput{Task: r1.Task, Token: r1.Token, B: call2.B, Kind: "succeeded", Result: raw(map[string]any{"from": "old"})})
	must(t, err)
	if late.Applied || late.Call.State != CallAccepted {
		t.Fatalf("stale observation applied: %+v", late)
	}
	// the new owner queries a2a-77 and records the answer; reconciliation settles "ask"
	for _, b := range []string{call2.B, call.B} {
		ob, err := e.s.Observe(e.ex2, ObserveInput{Task: r2.Task, Token: r2.Token, B: b, Kind: "succeeded", Result: raw(map[string]any{"v": 1})})
		must(t, err)
		if !ob.Applied || ob.Call.State != CallSucceeded || ob.Call.Sends != 1 {
			t.Fatalf("query result: %+v", ob)
		}
	}
	res, err := e.s.Commit(e.ex2, CommitInput{Task: r2.Task, Token: r2.Token, O: c2.O, A: c2.A, Outcome: OutcomeCompleted, Body: raw(map[string]any{"v": 1})})
	must(t, err)
	// a reply arriving after the node's result is committed changes nothing
	dup, err := e.s.Observe(e.ex2, ObserveInput{Task: r2.Task, Token: r2.Token, B: call2.B, Kind: "succeeded", Result: raw(map[string]any{"v": "late"})})
	must(t, err)
	if dup.Applied {
		t.Fatal("late reply applied")
	}
	n := e.node(a.Task, "extract")
	if n.Result.Artifact != res.Result.Artifact || n.Calls[1].LateEvents != 2 {
		t.Fatalf("node after late replies: result=%s calls=%+v", n.Result.Artifact, n.Calls)
	}
}
