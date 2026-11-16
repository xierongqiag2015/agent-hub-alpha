package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openjiuwen/agent-festival/go/cmd/hub-control/control"
)

// TestKnowledgeSamples records real HTTP exchanges of the knowledge-consumption
// path (claim lookup hit / version miss / revocation) for the D1 handoff. It
// only runs when K_SAMPLES_DIR is set. Tokens are replaced by placeholders.
func TestKnowledgeSamples(t *testing.T) {
	dir := os.Getenv("K_SAMPLES_DIR")
	if dir == "" {
		t.Skip("set K_SAMPLES_DIR to regenerate the knowledge-consumption samples")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := control.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, p := range []control.Principal{
		{ID: "approver", Kind: control.KindUser, Roles: []string{"approve", "accept", "operate", "review"}},
		{ID: "curator", Kind: control.KindUser, Roles: []string{"curate"}},
		{ID: "planner", Kind: control.KindAgent, Roles: []string{"propose"}},
		{ID: "graph-host", Kind: control.KindExecutor},
		{ID: "admin", Kind: control.KindAdmin},
	} {
		if err := s.PutPrincipal("samples", p, "secret-"+p.ID); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	var log []map[string]any
	tokens := map[string]string{}
	call := func(step, who, method, path string, body any) map[string]any {
		t.Helper()
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rd)
		if who != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[who])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		if step != "" {
			log = append(log, map[string]any{"step": step, "as": who, "request": map[string]any{"method": method, "path": path, "body": body},
				"response": map[string]any{"status": resp.StatusCode, "body": out}})
		}
		return out
	}
	flush := func(name string) {
		t.Helper()
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		enc.Encode(log)
		txt := buf.String()
		for who, tok := range tokens {
			txt = strings.ReplaceAll(txt, tok, "<session token of "+who+">")
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(txt), 0o644); err != nil {
			t.Fatal(err)
		}
		log = nil
	}
	for _, id := range []string{"approver", "curator", "planner", "graph-host", "admin"} {
		tokens[id] = call("", "", "POST", "/v1/login", map[string]any{"id": id, "secret": "secret-" + id})["token"].(string)
	}
	v1 := map[string]any{"id": "cmb-2025-annual-report-a", "sha256": "52c49dcdc00d4f5436adabc6561044d7a539e9bb0a8ebbdb692a1da6237459da"}
	v2 := map[string]any{"id": "cmb-2025-annual-report-a", "sha256": strings.Repeat("ab", 32), "title": "corrected snapshot (sample)"}
	call("", "admin", "POST", "/v1/admin/policies", control.Policy{Version: "ar-demo-1", MaxNodes: 8, MaxLoopIterations: 2,
		Capabilities: map[string]control.Capability{
			"material": {Targets: []string{"rdc:annual-report/material"}, Queryable: true, Reusable: true},
			"analysis": {Targets: []string{"rdc:annual-report/analysis"}, Queryable: true},
			"writer":   {Targets: []string{"rdc:annual-report/writer"}, Queryable: true},
		},
		RequiredChecks: []control.RequiredCheck{{Checker: "calc", RuleVersion: "w01-r1"}}, ReuseMaxAgeHrs: 720})
	call("", "admin", "POST", "/v1/admin/materials", v1)
	call("", "admin", "POST", "/v1/admin/materials", v2)
	graph := map[string]any{"report": "extract", "nodes": []any{map[string]any{"id": "extract", "capability": "material"}}}
	start := func(snap map[string]any, cmd string) (string, string) {
		p := call("", "planner", "POST", "/v1/plans", map[string]any{"goal": "extract 8 indicators", "material": []any{snap}, "graph": graph})
		task := p["task"].(string)
		call("", "approver", "POST", "/v1/commands/adopt", map[string]any{"command_id": cmd, "task": task, "rev": p["rev"], "digest": p["digest"]})
		l := call("", "graph-host", "POST", "/v1/exec/lease", map[string]any{"task": task})
		return task, l["token"].(string)
	}
	input := func(snap map[string]any) map[string]any {
		return map[string]any{"snapshot": snap, "instructions": "extract the 8 W01 indicators", "template": "w01-report-v1"}
	}
	runFresh := func(task, lease string, cl map[string]any) {
		o, a := cl["o"], cl["a"]
		b := call("", "graph-host", "POST", "/v1/exec/calls/bind", map[string]any{"task": task, "token": lease, "o": o, "a": a, "name": "ask", "target": "rdc:annual-report/material", "payload": map[string]any{"snapshot": "v"}})
		call("", "graph-host", "POST", "/v1/exec/calls/dispatch", map[string]any{"task": task, "token": lease, "b": b["b"]})
		call("", "graph-host", "POST", "/v1/exec/calls/observe", map[string]any{"task": task, "token": lease, "b": b["b"], "kind": "succeeded", "result": map[string]any{"indicators": 8}})
		call("step: commit the computed result", "graph-host", "POST", "/v1/exec/commit", map[string]any{"task": task, "token": lease, "o": o, "a": a, "outcome": "completed", "body": map[string]any{"indicators": 8}})
	}

	// k-01: first consumer of snapshot v1 -> miss, computes, commits
	a, la := start(v1, "c-a")
	tokens["lease-a"] = la
	cl := call("step: claim the extract node (first consumer of v1)", "graph-host", "POST", "/v1/exec/claim", map[string]any{"task": a, "token": la, "node": "extract", "material": input(v1), "sources": []any{v1}})
	runFresh(a, la, cl)
	origin := cl["o"].(string)
	flush("k-01-first-consumer-miss.json")

	// k-02: new task, same snapshot and instructions -> hit, commits reused_from
	b, lb := start(v1, "c-b")
	tokens["lease-b"] = lb
	cl = call("step: claim the extract node in a new task (lookup hit)", "graph-host", "POST", "/v1/exec/claim", map[string]any{"task": b, "token": lb, "node": "extract", "material": input(v1), "sources": []any{v1}})
	rc := cl["reuse_candidate"].(map[string]any)
	call("step: commit the candidate as this task's own result", "graph-host", "POST", "/v1/exec/commit", map[string]any{"task": b, "token": lb, "o": cl["o"], "a": cl["a"], "outcome": "completed", "body": rc["body"], "reused_from": rc["o"]})
	view := call("", "approver", "GET", "/v1/tasks/"+b, nil)
	for _, n := range view["nodes"].([]any) {
		if m := n.(map[string]any); m["node"] == "extract" {
			log = append(log, map[string]any{"step": "projection: the node in GET /v1/tasks/" + b, "node": map[string]any{
				"o": m["o"], "sources": m["sources"], "lookup": m["lookup"], "saved_calls": m["saved_calls"], "origin": m["origin"], "result": m["result"]}})
		}
	}
	flush("k-02-cross-task-hit.json")

	// k-03: corrected snapshot v2 -> miss with a version reason
	c, lc := start(v2, "c-c")
	tokens["lease-c"] = lc
	call("step: claim with the corrected snapshot v2 (version miss)", "graph-host", "POST", "/v1/exec/claim", map[string]any{"task": c, "token": lc, "node": "extract", "material": input(v2), "sources": []any{v2}})
	view = call("", "approver", "GET", "/v1/tasks/"+c, nil)
	for _, n := range view["nodes"].([]any) {
		if m := n.(map[string]any); m["node"] == "extract" {
			log = append(log, map[string]any{"step": "projection: lookup reason", "lookup": m["lookup"]})
		}
	}
	flush("k-03-version-miss.json")

	// k-04: candidate found, then revoked before commit; later lookups say why
	d, ld := start(v1, "c-d")
	tokens["lease-d"] = ld
	cl = call("step: claim (a candidate is offered)", "graph-host", "POST", "/v1/exec/claim", map[string]any{"task": d, "token": ld, "node": "extract", "material": input(v1), "sources": []any{v1}})
	rc = cl["reuse_candidate"].(map[string]any)
	call("step: curator revokes the original result", "curator", "POST", "/v1/commands/revoke", map[string]any{"command_id": "rv-1", "ref": "result:" + origin, "reason": "extraction used the wrong basis (sample)"})
	call("step: commit the candidate after revocation (refused)", "graph-host", "POST", "/v1/exec/commit", map[string]any{"task": d, "token": ld, "o": cl["o"], "a": cl["a"], "outcome": "completed", "body": rc["body"], "reused_from": rc["o"]})
	e, le := start(v1, "c-e")
	tokens["lease-e"] = le
	call("step: a later claim (miss, reason names the revocation)", "graph-host", "POST", "/v1/exec/claim", map[string]any{"task": e, "token": le, "node": "extract", "material": input(v1), "sources": []any{v1}})
	view = call("", "approver", "GET", "/v1/tasks/"+e, nil)
	for _, n := range view["nodes"].([]any) {
		if m := n.(map[string]any); m["node"] == "extract" {
			log = append(log, map[string]any{"step": "projection: lookup reason", "lookup": m["lookup"]})
		}
	}
	flush("k-04-revoked-before-commit.json")
}
