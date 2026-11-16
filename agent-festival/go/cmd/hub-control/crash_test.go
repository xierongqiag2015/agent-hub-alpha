package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openjiuwen/agent-festival/go/cmd/hub-control/control"
)

// These tests run the real service as a child process and kill it with
// SIGKILL / TerminateProcess (no shutdown hook runs), then restart it on the
// same database file. The child is this test binary re-executed with
// HUB_CONTROL_CHILD=1, so no separate build step is needed.

func TestMain(m *testing.M) {
	if os.Getenv("HUB_CONTROL_CHILD") == "1" {
		os.Args = append([]string{os.Args[0]}, splitArgs(os.Getenv("HUB_CONTROL_ARGS"))...)
		main()
		return
	}
	os.Exit(m.Run())
}

func splitArgs(s string) []string {
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		panic(err)
	}
	return out
}

type child struct {
	t    *testing.T
	cmd  *exec.Cmd
	base string
}

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func startChild(t *testing.T, dir, addr string) *child {
	t.Helper()
	args, _ := json.Marshal([]string{"-addr", addr, "-db", filepath.Join(dir, "hub.db"), "-bootstrap", filepath.Join(dir, "boot.json"), "-lease", "2s"})
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "HUB_CONTROL_CHILD=1", "HUB_CONTROL_ARGS="+string(args))
	for _, id := range []string{"alice", "planner", "ex1", "ex2", "calc", "cite"} {
		cmd.Env = append(cmd.Env, "SECRET_"+id+"=secret-"+id)
	}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &child{t: t, cmd: cmd, base: "http://" + addr}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(c.base + "/healthz?deep=1"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return c
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	cmd.Process.Kill()
	t.Fatal("child did not become healthy")
	return nil
}

// kill9 terminates the child without letting it run any cleanup.
func (c *child) kill9() {
	c.cmd.Process.Kill()
	c.cmd.Wait()
}

func (c *child) call(token, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rd *bytes.Reader
	method := "GET"
	if body != nil {
		b, _ := json.Marshal(body)
		rd, method = bytes.NewReader(b), "POST"
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, map[string]any{"transport_error": err.Error()}
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (c *child) ok(token, path string, body any) map[string]any {
	c.t.Helper()
	st, out := c.call(token, path, body)
	if st != 200 {
		c.t.Fatalf("%s: %d %v", path, st, out)
	}
	return out
}

func (c *child) login(id string) string {
	return c.ok("", "/v1/login", map[string]any{"id": id, "secret": "secret-" + id})["token"].(string)
}

func writeBoot(t *testing.T, dir string) {
	boot := map[string]any{
		"principals": []map[string]any{
			{"id": "alice", "kind": "user", "roles": []string{"approve", "accept", "operate", "review"}, "secret_env": "SECRET_alice"},
			{"id": "planner", "kind": "agent", "roles": []string{"propose"}, "secret_env": "SECRET_planner"},
			{"id": "ex1", "kind": "executor", "secret_env": "SECRET_ex1"},
			{"id": "ex2", "kind": "executor", "secret_env": "SECRET_ex2"},
			{"id": "calc", "kind": "checker", "roles": []string{"check:calc"}, "secret_env": "SECRET_calc"},
			{"id": "cite", "kind": "checker", "roles": []string{"check:citation"}, "secret_env": "SECRET_cite"},
		},
		"materials": []control.Material{{ID: "cmb-2025-annual-report-a", SHA256: "52c49dcdc00d4f5436adabc6561044d7a539e9bb0a8ebbdb692a1da6237459da"}},
		"policy": control.Policy{
			Version: "ar-1",
			Capabilities: map[string]control.Capability{
				"material": {Targets: []string{"agent:material"}, Queryable: true, Reusable: true},
				"analysis": {Targets: []string{"agent:analysis"}, Queryable: true},
				"writer":   {Targets: []string{"agent:writer"}, Queryable: true},
			},
			MaxNodes: 8, MaxLoopIterations: 2,
			RequiredChecks: []control.RequiredCheck{{Checker: "calc", RuleVersion: "r1"}, {Checker: "citation", RuleVersion: "r1"}},
		},
	}
	b, _ := json.MarshalIndent(boot, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "boot.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

var snap = map[string]any{"id": "cmb-2025-annual-report-a", "sha256": "52c49dcdc00d4f5436adabc6561044d7a539e9bb0a8ebbdb692a1da6237459da"}

var graph = map[string]any{"report": "write", "nodes": []map[string]any{
	{"id": "extract", "capability": "material"},
	{"id": "operating", "capability": "analysis", "depends_on": []string{"extract"}},
	{"id": "asset_quality", "capability": "analysis", "depends_on": []string{"extract"}},
	{"id": "write", "capability": "writer", "depends_on": []string{"operating", "asset_quality"}},
}}

func input(node string) map[string]any {
	return map[string]any{"snapshot": snap, "instructions": "role:" + node}
}

// runNode drives one node the way the Java host would: claim, bind, dispatch,
// observe, commit.
func runNode(c *child, ex, task, token, node string, body any) map[string]any {
	cl := c.ok(ex, "/v1/exec/claim", map[string]any{"task": task, "token": token, "node": node, "material": input(node), "sources": []any{snap}})
	if cl["action"] == "reuse" {
		return cl["result"].(map[string]any)
	}
	o, a := cl["o"], cl["a"]
	call := c.ok(ex, "/v1/exec/calls/bind", map[string]any{"task": task, "token": token, "o": o, "a": a, "name": "ask", "target": "agent:" + map[string]string{"extract": "material", "operating": "analysis", "asset_quality": "analysis", "write": "writer"}[node], "payload": map[string]any{"node": node}})
	c.ok(ex, "/v1/exec/calls/dispatch", map[string]any{"task": task, "token": token, "b": call["b"]})
	c.ok(ex, "/v1/exec/calls/observe", map[string]any{"task": task, "token": token, "b": call["b"], "kind": "succeeded", "result": body})
	return c.ok(ex, "/v1/exec/commit", map[string]any{"task": task, "token": token, "o": o, "a": a, "outcome": "completed", "body": body})["result"].(map[string]any)
}

func nodeState(v map[string]any, node string) map[string]any {
	for _, n := range v["nodes"].([]any) {
		if m := n.(map[string]any); m["node"] == node {
			return m
		}
	}
	return nil
}

func TestKill9MidRunThenRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	dir := t.TempDir()
	writeBoot(t, dir)
	addr := freeAddr(t)
	c := startChild(t, dir, addr)
	alice, planner, ex1 := c.login("alice"), c.login("planner"), c.login("ex1")

	pr := c.ok(planner, "/v1/plans", map[string]any{"goal": "招商银行 2025 年报简报", "material": []any{snap}, "graph": graph})
	task := pr["task"].(string)
	c.ok(alice, "/v1/commands/adopt", map[string]any{"command_id": "click-1", "task": task, "rev": pr["rev"], "digest": pr["digest"]})
	l1 := c.ok(ex1, "/v1/exec/lease", map[string]any{"task": task})
	tok1 := l1["token"].(string)
	ext := runNode(c, ex1, task, tok1, "extract", map[string]any{"indicators": 8})
	cl := c.ok(ex1, "/v1/exec/claim", map[string]any{"task": task, "token": tok1, "node": "operating", "material": input("operating"), "sources": []any{snap}})
	call := c.ok(ex1, "/v1/exec/calls/bind", map[string]any{"task": task, "token": tok1, "o": cl["o"], "a": cl["a"], "name": "ask", "target": "agent:analysis", "payload": map[string]any{"node": "operating"}})
	c.ok(ex1, "/v1/exec/calls/dispatch", map[string]any{"task": task, "token": tok1, "b": call["b"]})

	c.kill9() // the control process dies with a send committed and no reply recorded
	c = startChild(t, dir, addr)
	// sessions survive: the signing key lives in the database, not in memory
	v := c.ok(alice, "/v1/tasks/"+task, nil)
	if e := nodeState(v, "extract"); e["state"] != "completed" || len(e["attempts"].([]any)) != 1 {
		t.Fatalf("extract after restart: %v", e)
	}
	time.Sleep(2200 * time.Millisecond) // executor ex1 is gone too; its 2 s lease runs out
	ex2 := c.login("ex2")
	l2 := c.ok(ex2, "/v1/exec/lease", map[string]any{"task": task})
	tok2 := l2["token"].(string)
	if l2["epoch"].(float64) != 2 || len(l2["unresolved"].([]any)) != 1 {
		t.Fatalf("takeover lease: %v", l2)
	}
	if u := l2["unresolved"].([]any)[0].(map[string]any); u["state"] != "unknown" {
		t.Fatalf("in-flight call after crash: %v", u)
	}
	re := c.ok(ex2, "/v1/exec/claim", map[string]any{"task": task, "token": tok2, "node": "extract", "material": input("extract"), "sources": []any{snap}})
	if re["action"] != "reuse" || re["result"].(map[string]any)["artifact"] != ext["artifact"] {
		t.Fatalf("extract not reused: %v", re)
	}
	if st, out := c.call(ex2, "/v1/exec/calls/dispatch", map[string]any{"task": task, "token": tok2, "b": call["b"]}); st != 409 {
		t.Fatalf("unknown call resent after crash: %d %v", st, out)
	}
	if st, _ := c.call(ex1, "/v1/exec/commit", map[string]any{"task": task, "token": tok1, "o": cl["o"], "a": cl["a"], "outcome": "completed", "body": map[string]any{"stale": true}}); st != 409 {
		t.Fatalf("old executor committed after takeover: %d", st)
	}
	v = c.ok(alice, "/v1/tasks/"+task, nil)
	if e := nodeState(v, "extract"); e["reused"].(float64) != 1 || len(e["attempts"].([]any)) != 1 {
		t.Fatalf("extract reuse accounting: %v", e)
	}
	c.kill9()
}

// TestKill9DuringWriteBurst kills the service while many transitions are in
// flight and checks that every acknowledged write survived, nothing partial
// is visible, and the database passes integrity_check.
func TestKill9DuringWriteBurst(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	dir := t.TempDir()
	writeBoot(t, dir)
	addr := freeAddr(t)
	c := startChild(t, dir, addr)
	alice, planner, ex1 := c.login("alice"), c.login("planner"), c.login("ex1")
	pr := c.ok(planner, "/v1/plans", map[string]any{"goal": "g", "material": []any{snap}, "graph": graph})
	task := pr["task"].(string)
	c.ok(alice, "/v1/commands/adopt", map[string]any{"command_id": "click-1", "task": task, "rev": pr["rev"], "digest": pr["digest"]})
	tok := c.ok(ex1, "/v1/exec/lease", map[string]any{"task": task})["token"].(string)
	cl := c.ok(ex1, "/v1/exec/claim", map[string]any{"task": task, "token": tok, "node": "extract", "material": input("extract"), "sources": []any{snap}})

	var acked sync.Map
	var stop atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; !stop.Load(); i++ {
				seq := w*100000 + i
				st, _ := c.call(ex1, "/v1/exec/progress", map[string]any{"task": task, "token": tok, "o": cl["o"], "a": cl["a"], "seq": seq, "kind": "activity", "detail": fmt.Sprint(seq)})
				if st == 200 {
					acked.Store(seq, true)
				} else if st == 0 {
					return
				}
			}
		}(w)
	}
	time.Sleep(400 * time.Millisecond)
	c.kill9()
	stop.Store(true)
	wg.Wait()

	s, err := control.Open(filepath.Join(dir, "hub.db"))
	if err != nil {
		t.Fatalf("reopen after kill: %v", err)
	}
	if err := s.IntegrityCheck(); err != nil {
		t.Fatal(err)
	}
	ev, err := s.Events(task, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	n := 0
	acked.Range(func(_, _ any) bool { n++; return true })
	if n == 0 {
		t.Fatal("no write was acknowledged before the kill")
	}
	c = startChild(t, dir, addr)
	defer c.kill9()
	// every acknowledged progress write must be readable after restart
	missing := 0
	acked.Range(func(k, _ any) bool {
		st, out := c.call(ex1, "/v1/exec/progress", map[string]any{"task": task, "token": tok, "o": cl["o"], "a": cl["a"], "seq": k, "kind": "activity", "detail": fmt.Sprint(k)})
		if st != 200 || out["applied"] != false {
			missing++
		}
		return true
	})
	if missing > 0 {
		t.Fatalf("%d of %d acknowledged writes lost or not idempotent", missing, n)
	}
	t.Logf("acknowledged writes before kill: %d, all present after restart; %d audit events intact", n, len(ev))
}

// TestBootstrapExampleStarts runs the shipped example configuration.
func TestBootstrapExampleStarts(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	dir := t.TempDir()
	b, err := os.ReadFile(filepath.Join("deploy", "bootstrap.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "boot.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"DEMO_APPROVER", "DEMO_VIEWER", "DEMO_CURATOR", "HUB_ADMIN", "PLANNER", "GRAPH_HOST", "CHECKER_CALC", "CHECKER_CITATION"} {
		t.Setenv("HUB_SECRET_"+k, "example-"+strings.ToLower(k))
	}
	c := startChild(t, dir, freeAddr(t))
	defer c.kill9()
	tok := c.login2("planner", "example-planner")
	pr := c.ok(tok, "/v1/plans", map[string]any{"goal": "g", "material": []any{snap}, "graph": graph})
	if pr["mountable"] != true {
		t.Fatalf("example policy rejects the demo graph: %v", pr)
	}
	if st, _ := c.call("", "/v1/commands/adopt", map[string]any{"command_id": "x"}); st != 401 {
		t.Fatalf("unauthenticated adopt: %d", st)
	}
}

func (c *child) login2(id, secret string) string {
	return c.ok("", "/v1/login", map[string]any{"id": id, "secret": secret})["token"].(string)
}

// TestSeedProducesEveryScenario runs -seed against a real child process and
// checks that each simulated task ends in the state the console should show.
func TestSeedProducesEveryScenario(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	dir := t.TempDir()
	b, err := os.ReadFile(filepath.Join("deploy", "bootstrap.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "boot.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"DEMO_APPROVER", "DEMO_VIEWER", "DEMO_CURATOR", "HUB_ADMIN", "PLANNER", "GRAPH_HOST", "CHECKER_CALC", "CHECKER_CITATION"} {
		t.Setenv("HUB_SECRET_"+k, "example-"+strings.ToLower(k))
	}
	c := startChild(t, dir, freeAddr(t))
	defer c.kill9()
	runSeed(c.base, false, 2*time.Second)
	runSeed(c.base, false, 2*time.Second) // second run is a no-op

	tok := c.login2("demo-viewer", "example-demo_viewer")
	tasks := c.ok(tok, "/v1/tasks", nil)["tasks"].([]any)
	if len(tasks) != 10 {
		t.Fatalf("tasks = %d, want 10", len(tasks))
	}
	got := map[string]bool{}
	for _, x := range tasks {
		m := x.(map[string]any)
		key := fmt.Sprint(m["status"], "/", m["report_gate"], "/", m["unresolved_calls"])
		got[key] = true
	}
	for _, want := range []string{"delivered/delivered/0", "adopted/ready/0", "adopted/needs_human/0", "adopted/rejected/0", "adopted/not_ready/1", "draft/<nil>/0", "adopted/stale_basis/0"} {
		if !got[want] {
			t.Fatalf("missing scenario %s in %v", want, got)
		}
	}
	resp, err := http.Get(c.base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(buf.String(), "hub-control 控制台") || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("console page: %d %q", resp.StatusCode, resp.Header)
	}
}
