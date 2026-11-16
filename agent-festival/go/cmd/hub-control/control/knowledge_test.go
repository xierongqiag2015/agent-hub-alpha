package control

import (
	"strings"
	"testing"
	"time"
)

// F-02C (#14 review): new-task knowledge consumption.
//   Tnew ≠ Told; ReuseCandidate = Kθ(input, version, source, freshness,
//   permission, applicability); adopting it still passes this task's checks
//   and approval; revoked knowledge stops later consumption without
//   rewriting what already happened.

func (e *env) curator() Principal {
	e.t.Helper()
	must(e.t, e.s.PutPrincipal("test", Principal{ID: "kate", Kind: KindUser, Roles: []string{"curate"}}, "secret-kate"))
	_, p, err := e.s.Login("kate", "secret-kate")
	must(e.t, err)
	return p
}

func (e *env) adoptedOn(cmd string, m Material) Adoption {
	e.t.Helper()
	pr, err := e.s.Propose(e.planner, ProposeInput{Goal: "g-" + cmd, Material: []Material{m}, Graph: demoGraph()})
	must(e.t, err)
	if !pr.Mountable {
		e.t.Fatalf("not mountable: %v", pr.Problems)
	}
	a, err := e.s.Adopt(e.alice, AdoptInput{CommandID: cmd, Task: pr.Task, Rev: pr.Rev, Digest: pr.Digest})
	must(e.t, err)
	return a
}

func claimOn(r *run, node string, m Material) ClaimInput {
	return ClaimInput{Task: r.Task, Token: r.Token, Node: node, Sources: []Material{m}, Material: raw(map[string]any{"snapshot": m, "instructions": "role:" + node})}
}

func TestK_HitRecordsSourceAndSavedCalls(t *testing.T) {
	e := newEnv(t)
	a1 := e.adopted("c1")
	r1 := e.leaseAs(e.ex1, a1.Task)
	origin := e.runNode(r1, "extract", 0, map[string]any{"indicators": 8}, "agent:material")
	if n := e.node(a1.Task, "extract"); n.Lookup == nil || n.Lookup.Outcome != LookupMiss || !strings.Contains(n.Lookup.Reason, "没有其他任务") {
		t.Fatalf("first consumer lookup = %+v", n.Lookup)
	}

	a2 := e.adopted("c2")
	r2 := e.leaseAs(e.ex2, a2.Task)
	got := e.runNode(r2, "extract", 0, map[string]any{"indicators": 8}, "agent:material")
	if got.ReusedFrom != origin.O {
		t.Fatalf("reused_from = %q", got.ReusedFrom)
	}
	n := e.node(a2.Task, "extract")
	if n.Lookup.Outcome != LookupHit || n.Lookup.SourceO != origin.O || n.Saved != 1 || n.Origin != origin.O || len(n.Calls) != 0 {
		t.Fatalf("hit node = %+v lookup=%+v", n, n.Lookup)
	}
	if len(n.Sources) != 1 || n.Sources[0] != SourceRef(snapshot) {
		t.Fatalf("sources = %v", n.Sources)
	}
	v := e.view(a2.Task)
	if v.Knowledge.Hits != 1 || v.Knowledge.SavedCalls != 1 {
		t.Fatalf("summary = %+v", v.Knowledge)
	}
	// the analysis capability is not declared reusable: lookup says so
	e.runNode(r2, "operating", 0, map[string]any{"s": 1}, "agent:analysis")
	if l := e.node(a2.Task, "operating").Lookup; l.Outcome != LookupNotApplicable {
		t.Fatalf("operating lookup = %+v", l)
	}
	kv, err := e.s.Knowledge()
	must(t, err)
	if kv.Hits != 1 || len(kv.Items) != 1 || kv.Items[0].ReusedBy != 1 || kv.Items[0].Saved != 1 {
		t.Fatalf("inventory = %+v", kv)
	}
}

func TestK_SourcesMustBeApprovedAndDeclared(t *testing.T) {
	e := newEnv(t)
	other := Material{ID: "other-report", SHA256: strings.Repeat("cd", 32)}
	must(t, e.s.RegisterMaterial(e.admin, other))
	a := e.adopted("c1")
	r := e.leaseAs(e.ex1, a.Task)
	// registered, but not part of what this task was approved to use
	_, err := e.s.Claim(e.ex1, claimOn(r, "extract", other))
	wantCode(t, err, CodeForbidden)
	// a reusable capability must say what it consumes
	_, err = e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "extract", Material: nodeInput("extract", nil)})
	wantCode(t, err, CodeInvalid)
	if _, err := e.s.Claim(e.ex1, claimOn(r, "extract", snapshot)); err != nil {
		t.Fatalf("approved source refused: %v", err)
	}
}

func TestK_MissReasonsAreSpecific(t *testing.T) {
	e := newEnv(t)
	a1 := e.adopted("c1")
	e.runNode(e.leaseAs(e.ex1, a1.Task), "extract", 0, map[string]any{"v": 1}, "agent:material")

	// a corrected snapshot: same document, different version
	v2 := Material{ID: snapshot.ID, SHA256: strings.Repeat("ab", 32)}
	must(t, e.s.RegisterMaterial(e.admin, v2))
	a2 := e.adoptedOn("c2", v2)
	r2 := e.leaseAs(e.ex1, a2.Task)
	c, err := e.s.Claim(e.ex1, claimOn(r2, "extract", v2))
	must(t, err)
	if c.ReuseCandidate != nil || c.Lookup == nil || c.Lookup.Outcome != LookupMiss || !strings.Contains(c.Lookup.Reason, "版本不同") || !strings.Contains(e.node(a2.Task, "extract").Lookup.Reason, "版本不同") {
		t.Fatalf("v2 lookup = %+v", e.node(a2.Task, "extract").Lookup)
	}

	// same source, different instructions: not applicable
	a3 := e.adopted("c3")
	r3 := e.leaseAs(e.ex1, a3.Task)
	in := claimOn(r3, "extract", snapshot)
	in.Material = raw(map[string]any{"snapshot": snapshot, "instructions": "only the first 3 indicators"})
	_, err = e.s.Claim(e.ex1, in)
	must(t, err)
	if r := e.node(a3.Task, "extract").Lookup.Reason; !strings.Contains(r, "指令") {
		t.Fatalf("instructions lookup = %q", r)
	}

	// freshness: past the policy's reuse window
	e.advance(31 * 24 * time.Hour)
	a4 := e.adopted("c4")
	r4 := e.leaseAs(e.ex1, a4.Task)
	_, err = e.s.Claim(e.ex1, ClaimInput{Task: r4.Task, Token: r4.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	must(t, err)
	if r := e.node(a4.Task, "extract").Lookup; r.Outcome != LookupMiss || !strings.Contains(r.Reason, "保鲜期") {
		t.Fatalf("stale lookup = %+v", r)
	}
}

func TestK_RevokedBetweenLookupAndCommit(t *testing.T) {
	e := newEnv(t)
	kate := e.curator()
	a1 := e.adopted("c1")
	origin := e.runNode(e.leaseAs(e.ex1, a1.Task), "extract", 0, map[string]any{"v": 1}, "agent:material")
	a2 := e.adopted("c2")
	r2 := e.leaseAs(e.ex2, a2.Task)
	c, err := e.s.Claim(e.ex2, ClaimInput{Task: r2.Task, Token: r2.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	must(t, err)
	if c.ReuseCandidate == nil {
		t.Fatal("expected a candidate")
	}
	_, err = e.s.Revoke(kate, RevokeInput{CommandID: "rv1", Ref: ResultRef(origin.O), Reason: "抽取口径错误"})
	must(t, err)
	_, err = e.s.Commit(e.ex2, CommitInput{Task: r2.Task, Token: r2.Token, O: c.O, A: c.A, Outcome: OutcomeCompleted, Body: c.ReuseCandidate.Body, ReusedFrom: origin.O})
	wantCode(t, err, CodeConflict)
	// later lookups say why, and the origin's own record is untouched
	a3 := e.adopted("c3")
	r3 := e.leaseAs(e.ex1, a3.Task)
	c3, err := e.s.Claim(e.ex1, ClaimInput{Task: r3.Task, Token: r3.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	must(t, err)
	if c3.ReuseCandidate != nil || !strings.Contains(e.node(a3.Task, "extract").Lookup.Reason, "已撤回") {
		t.Fatalf("lookup after revocation = %+v", e.node(a3.Task, "extract").Lookup)
	}
	if n := e.node(a1.Task, "extract"); n.Result == nil || n.Result.Artifact != origin.Artifact || n.Revoked == "" {
		t.Fatalf("origin after revocation: %+v", n)
	}
}

func TestK_RevokedSourceBlocksDeliveryButNotHistory(t *testing.T) {
	e := newEnv(t)
	kate := e.curator()
	// one task already delivered on the snapshot, one checked and waiting
	a1 := e.adopted("c1")
	rep1 := e.runAll(e.leaseAs(e.ex1, a1.Task))
	e.passChecks(rep1.Artifact)
	_, err := e.s.Accept(e.alice, AcceptInput{CommandID: "acc1", Task: a1.Task, Artifact: rep1.Artifact})
	must(t, err)
	a2 := e.adopted("c2")
	r2 := e.leaseAs(e.ex2, a2.Task)
	rep2 := e.runNode(r2, "extract", 0, map[string]any{"indicators": 8}, "agent:material") // reused from a1
	_ = rep2
	e.runNode(r2, "operating", 0, map[string]any{"section": "operating"}, "agent:analysis")
	e.runNode(r2, "asset_quality", 0, map[string]any{"section": "asset_quality"}, "agent:analysis")
	rep := e.runNode(r2, "write", 0, report(1.88), "agent:writer")
	e.passChecks(rep.Artifact)
	if g := e.view(a2.Task).Gate; g.Status != GateReady {
		t.Fatalf("before revocation: %s", g.Status)
	}

	// only a curator may revoke, with a reason; the command is deduplicated
	_, err = e.s.Revoke(e.alice, RevokeInput{CommandID: "x", Ref: SourceRef(snapshot), Reason: "错页"})
	wantCode(t, err, CodeForbidden)
	_, err = e.s.Revoke(kate, RevokeInput{CommandID: "rv1", Ref: SourceRef(snapshot), Reason: ""})
	wantCode(t, err, CodeInvalid)
	rv, err := e.s.Revoke(kate, RevokeInput{CommandID: "rv1", Ref: SourceRef(snapshot), Reason: "快照缺第 16 页"})
	must(t, err)
	again, err := e.s.Revoke(kate, RevokeInput{CommandID: "rv1", Ref: SourceRef(snapshot), Reason: "快照缺第 16 页"})
	must(t, err)
	if !again.Replayed || again.At != rv.At {
		t.Fatal("revocation command not deduplicated")
	}

	// the waiting task can no longer be delivered...
	g := e.view(a2.Task).Gate
	if g.Status != GateStaleBasis || strings.Join(g.AllowedActions, ",") != "revise_in_new_task,open_case" {
		t.Fatalf("gate after revocation: %+v", g)
	}
	_, err = e.s.Accept(e.alice, AcceptInput{CommandID: "acc2", Task: a2.Task, Artifact: rep.Artifact})
	wantCode(t, err, CodeBlocked)
	// ...the delivered one stays delivered, with the warning shown
	v1 := e.view(a1.Task)
	if v1.Status != "delivered" || v1.Gate.Status != GateDelivered || len(v1.Knowledge.BasisRevoked) == 0 {
		t.Fatalf("delivered task after revocation: %s %s %v", v1.Status, v1.Gate.Status, v1.Knowledge.BasisRevoked)
	}
	// results are not rewritten
	if n := e.node(a1.Task, "extract"); n.Result == nil || n.State != OutcomeCompleted {
		t.Fatalf("history changed: %+v", n)
	}
	// and the source cannot start anything new
	pr, err := e.s.Propose(e.planner, ProposeInput{Goal: "g", Material: []Material{snapshot}, Graph: demoGraph()})
	must(t, err)
	if pr.Mountable || !strings.Contains(strings.Join(pr.Problems, "|"), "已撤回") {
		t.Fatalf("revoked material still mountable: %v", pr.Problems)
	}
}
