package control

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestA09_ChangedReportNeedsNewCheckAndApproval(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	r := e.leaseAs(e.ex1, a.Task)
	rep := e.runAll(r)
	e.passChecks(rep.Artifact)

	// someone edits the report after it was checked (NIM 1.87 -> 1.97)
	edited, _, _ := Digest(report(1.97))
	_, err := e.s.Accept(e.alice, AcceptInput{CommandID: "acc-1", Task: a.Task, Artifact: edited})
	wantCode(t, err, CodeConflict)
	// and cannot slip it in as the node's result either
	_, err = e.s.Commit(e.ex1, CommitInput{Task: r.Task, Token: r.Token, O: rep.O, A: rep.A, Outcome: OutcomeCompleted, Body: raw(report(1.97))})
	wantCode(t, err, CodeConflict)

	// a revision in the adopted loop bound (write#1) is a new artifact: old checks do not carry over
	c, err := e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "write", Iter: 1, Sources: []Material{snapshot}, Material: nodeInput("write", map[string]any{"revision": 1})})
	must(t, err)
	call, _ := e.s.BindCall(e.ex1, BindInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Name: "ask", Target: "agent:writer", Payload: raw(map[string]any{"rev": 1})})
	_, _ = e.s.Dispatch(e.ex1, CallRef{Task: r.Task, Token: r.Token, B: call.B})
	_, _ = e.s.Observe(e.ex1, ObserveInput{Task: r.Task, Token: r.Token, B: call.B, Kind: "succeeded"})
	rev, err := e.s.Commit(e.ex1, CommitInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Outcome: OutcomeCompleted, Body: raw(report(1.97))})
	must(t, err)
	g := e.view(a.Task).Gate
	if g.Artifact != rev.Result.Artifact || g.Status != GateUncovered {
		t.Fatalf("gate after revision = %+v", g)
	}
	_, err = e.s.Accept(e.alice, AcceptInput{CommandID: "acc-2", Task: a.Task, Artifact: rep.Artifact})
	wantCode(t, err, CodeConflict) // the checked old digest is no longer the report
	e.passChecks(rev.Result.Artifact)
	_, err = e.s.Accept(e.alice, AcceptInput{CommandID: "acc-3", Task: a.Task, Artifact: rev.Result.Artifact})
	must(t, err)
}

func TestA10_CrossTaskReuseIsBoundToMaterialVersion(t *testing.T) {
	e := newEnv(t)
	a1 := e.adopted("c1")
	r1 := e.leaseAs(e.ex1, a1.Task)
	ext := e.runNode(r1, "extract", 0, map[string]any{"indicators": 8}, "agent:material")

	a2 := e.adopted("c2")
	r2 := e.leaseAs(e.ex2, a2.Task)
	c, err := e.s.Claim(e.ex2, ClaimInput{Task: r2.Task, Token: r2.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	must(t, err)
	if c.Action != "run" || c.ReuseCandidate == nil || c.ReuseCandidate.O != ext.O {
		t.Fatalf("claim in new task = %+v", c)
	}
	res, err := e.s.Commit(e.ex2, CommitInput{Task: r2.Task, Token: r2.Token, O: c.O, A: c.A, Outcome: OutcomeCompleted,
		Body: c.ReuseCandidate.Body, ReusedFrom: ext.O})
	must(t, err)
	if res.Result.ReusedFrom != ext.O || res.Result.O == ext.O || len(e.node(a2.Task, "extract").Calls) != 0 {
		t.Fatalf("reuse result = %+v", res.Result)
	}
	// analysis is not declared reusable: no candidate even for identical input
	e.runNode(r1, "operating", 0, map[string]any{"section": "operating"}, "agent:analysis")
	c3, err := e.s.Claim(e.ex2, ClaimInput{Task: r2.Task, Token: r2.Token, Node: "operating", Sources: []Material{snapshot}, Material: nodeInput("operating", nil)})
	must(t, err)
	if c3.ReuseCandidate != nil {
		t.Fatal("non-reusable capability offered a candidate")
	}
	_, err = e.s.Commit(e.ex2, CommitInput{Task: r2.Task, Token: r2.Token, O: c3.O, A: c3.A, Outcome: OutcomeCompleted, Body: raw(map[string]any{"section": "operating"}), ReusedFrom: ExecID(a1.Task, a1.Rev, "operating", 0)})
	wantCode(t, err, CodeForbidden)

	// a corrected snapshot is a new material version: nothing from the old one is reused
	v2 := Material{ID: snapshot.ID, SHA256: strings.Repeat("ab", 32), Title: "corrected"}
	must(t, e.s.RegisterMaterial(e.admin, v2))
	pr, _ := e.s.Propose(e.planner, ProposeInput{Goal: "g", Material: []Material{v2}, Graph: demoGraph()})
	a3, err := e.s.Adopt(e.alice, AdoptInput{CommandID: "c3", Task: pr.Task, Rev: pr.Rev, Digest: pr.Digest})
	must(t, err)
	r3 := e.leaseAs(e.ex1, a3.Task)
	c4, err := e.s.Claim(e.ex1, ClaimInput{Task: r3.Task, Token: r3.Token, Node: "extract", Sources: []Material{v2}, Material: raw(map[string]any{"snapshot": v2, "instructions": "role:extract"})})
	must(t, err)
	if c4.ReuseCandidate != nil {
		t.Fatal("result for the old snapshot offered for the new one")
	}
	// reuse never forges a source: a different body than the candidate is refused
	_, err = e.s.Commit(e.ex1, CommitInput{Task: r3.Task, Token: r3.Token, O: c4.O, A: c4.A, Outcome: OutcomeCompleted, Body: raw(map[string]any{"indicators": 8}), ReusedFrom: ext.O})
	wantCode(t, err, CodeConflict)
}

func TestA11_PauseStopsNewWorkButNotInFlightCalls(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	r := e.leaseAs(e.ex1, a.Task)
	c, _ := e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	call, _ := e.s.BindCall(e.ex1, BindInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Name: "ask", Target: "agent:material", Payload: raw(map[string]any{"q": 1})})
	_, err := e.s.Dispatch(e.ex1, CallRef{Task: r.Task, Token: r.Token, B: call.B})
	must(t, err)

	_, err = e.s.Pause(e.alice, TaskCommand{CommandID: "p1", Task: a.Task})
	must(t, err)
	v := e.view(a.Task)
	if !v.Paused || v.Nodes[0].Calls[0].State != CallDispatched {
		t.Fatalf("paused view: paused=%v call=%s", v.Paused, v.Nodes[0].Calls[0].State)
	}
	call2, _ := e.s.BindCall(e.ex1, BindInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Name: "ask2", Target: "agent:material", Payload: raw(map[string]any{"q": 2})})
	_, err = e.s.Dispatch(e.ex1, CallRef{Task: r.Task, Token: r.Token, B: call2.B})
	wantCode(t, err, CodePaused)
	// the call in flight still lands
	ob, err := e.s.Observe(e.ex1, ObserveInput{Task: r.Task, Token: r.Token, B: call.B, Kind: "succeeded", Result: raw(map[string]any{"v": 1})})
	must(t, err)
	if !ob.Applied {
		t.Fatal("in-flight reply dropped while paused")
	}
	_, err = e.s.Commit(e.ex1, CommitInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Outcome: OutcomeCompleted, Body: raw(map[string]any{"v": 1})})
	must(t, err)
	_, err = e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "operating", Sources: []Material{snapshot}, Material: nodeInput("operating", nil)})
	wantCode(t, err, CodePaused)

	_, err = e.s.Resume(e.alice, TaskCommand{CommandID: "p2", Task: a.Task})
	must(t, err)
	cl, err := e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "operating", Sources: []Material{snapshot}, Material: nodeInput("operating", nil)})
	must(t, err)
	if cl.O != ExecID(a.Task, a.Rev, "operating", 0) || cl.Epoch != r.Epoch {
		t.Fatalf("identity changed across pause: %+v", cl)
	}
}

func TestA12_DuplicateAndLateProgressNeverRegress(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	r := e.leaseAs(e.ex1, a.Task)
	c, _ := e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	send := func(seq int, detail string) bool {
		ok, err := e.s.Progress(e.ex1, ProgressInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Seq: seq, Kind: "activity", Detail: detail})
		must(t, err)
		return ok
	}
	if !send(1, "读取年报第 14 页") || !send(2, "核对营业收入") || send(2, "核对营业收入") || send(1, "读取年报第 14 页") {
		t.Fatal("duplicate progress applied")
	}
	if got := e.node(a.Task, "extract").Activity; got != "核对营业收入" {
		t.Fatalf("activity = %q", got)
	}
	e.reopen() // a reconnecting page reads the same state from disk
	if got := e.node(a.Task, "extract").Activity; got != "核对营业收入" {
		t.Fatalf("activity after reopen = %q", got)
	}
}

func TestA13_FailedWriteLeavesNothingVisible(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	r := e.leaseAs(e.ex1, a.Task)
	c, _ := e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	before := e.view(a.Task).LastEvent

	e.s.FailCommit = func(op string) error {
		if op == "commit" {
			return errors.New("disk full")
		}
		return nil
	}
	_, err := e.s.Commit(e.ex1, CommitInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Outcome: OutcomeCompleted, Body: raw(map[string]any{"v": 1})})
	wantCode(t, err, CodeStorage)
	n := e.node(a.Task, "extract")
	if n.Result != nil || n.State != "running" || e.view(a.Task).LastEvent != before {
		t.Fatalf("failed commit left traces: %+v", n)
	}
	e.s.FailCommit = nil
	_, err = e.s.Commit(e.ex1, CommitInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Outcome: OutcomeCompleted, Body: raw(map[string]any{"v": 1})})
	must(t, err)

	// the same holds for delivery
	rep := e.runAll(r)
	e.passChecks(rep.Artifact)
	e.s.FailCommit = func(op string) error {
		if op == "accept" {
			return errors.New("fsync failed")
		}
		return nil
	}
	_, err = e.s.Accept(e.alice, AcceptInput{CommandID: "acc", Task: a.Task, Artifact: rep.Artifact})
	wantCode(t, err, CodeStorage)
	if v := e.view(a.Task); v.Status != "adopted" || v.Gate.Status != GateReady {
		t.Fatalf("after failed accept: %s %s", v.Status, v.Gate.Status)
	}
	e.s.FailCommit = nil
	d, err := e.s.Accept(e.alice, AcceptInput{CommandID: "acc", Task: a.Task, Artifact: rep.Artifact})
	must(t, err)
	if d.Replayed {
		t.Fatal("a rolled-back command must not count as executed")
	}
}

func TestA14_CaseLinksOldEvidenceAndNewPolicyAppliesToNewTasks(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	r := e.leaseAs(e.ex1, a.Task)
	rep := e.runAll(r)
	e.passChecks(rep.Artifact)
	_, err := e.s.Accept(e.alice, AcceptInput{CommandID: "acc", Task: a.Task, Artifact: rep.Artifact})
	must(t, err)

	cs, err := e.s.OpenCase(e.alice, CaseInput{CommandID: "case-1", Task: a.Task, Ref: rep.Artifact, Text: "净利息收益率引用页码应为印刷页 15"})
	must(t, err)
	_, err = e.s.OpenCase(e.alice, CaseInput{CommandID: "case-2", Task: a.Task, Ref: "nonexistent", Text: "x"})
	wantCode(t, err, CodeNotFound)

	p2 := demoPolicy("ar-2")
	p2.RequiredChecks = append(p2.RequiredChecks, RequiredCheck{Checker: "citation", RuleVersion: "r2"})
	_, err = e.s.PublishPolicy(e.admin, p2)
	must(t, err)
	a2 := e.adopted("c2")
	if a2.Policy != "ar-2" {
		t.Fatalf("new task bound %s", a2.Policy)
	}
	old := e.view(a.Task)
	if old.Policy != "ar-1" || old.Status != "delivered" || old.Cases[0].ID != cs.ID || old.Cases[0].Policy != "ar-1" {
		t.Fatalf("old task changed: %+v", old)
	}
	// republishing a version with different content is refused
	p1changed := demoPolicy("ar-1")
	p1changed.MaxNodes = 99
	_, err = e.s.PublishPolicy(e.admin, p1changed)
	wantCode(t, err, CodeConflict)
}

// AR-04 (#27): a policy change plus a restart must not change what an adopted
// task resolves; the new version applies to new tasks only.
func TestAR04_AdoptedBindingSurvivesPolicyChangeAndRestart(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	r := e.leaseAs(e.ex1, a.Task)
	ext := e.runNode(r, "extract", 0, map[string]any{"indicators": 8}, "agent:material")

	// publish ar-2: the material capability changes (new target, not reusable)
	p2 := demoPolicy("ar-2")
	p2.Capabilities["material"] = Capability{Targets: []string{"agent:material-v2"}}
	_, err := e.s.PublishPolicy(e.admin, p2)
	must(t, err)
	e.reopen()
	e.advance(DefaultLeaseTTL + time.Second)
	r2 := e.leaseAs(e.ex2, a.Task)
	if r2.Policy != "ar-1" {
		t.Fatalf("resumed task resolved policy %s, want its adopted ar-1", r2.Policy)
	}
	// the completed node is reused under the original binding (same κ_in)
	c, err := e.s.Claim(e.ex2, ClaimInput{Task: r2.Task, Token: r2.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	must(t, err)
	if c.Action != "reuse" || c.Result.Artifact != ext.Artifact {
		t.Fatalf("after policy change + restart: %+v", c)
	}
	// a remaining node still binds calls against ar-1's targets, not ar-2's
	e.runNode(r2, "operating", 0, map[string]any{"s": 1}, "agent:analysis")
	// a new task binds ar-2
	a2 := e.adopted("c2")
	if a2.Policy != "ar-2" {
		t.Fatalf("new task bound %s", a2.Policy)
	}
	r3 := e.leaseAs(e.ex1, a2.Task)
	c3, err := e.s.Claim(e.ex1, ClaimInput{Task: r3.Task, Token: r3.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	must(t, err)
	_, err = e.s.BindCall(e.ex1, BindInput{Task: r3.Task, Token: r3.Token, O: c3.O, A: c3.A, Name: "ask", Target: "agent:material", Payload: raw(map[string]any{"q": 1})})
	wantCode(t, err, CodeForbidden) // ar-2 no longer allows the old target
	if l := e.node(a2.Task, "extract").Lookup; l.Outcome != LookupNotApplicable {
		t.Fatalf("ar-2 made material non-reusable, lookup = %+v", l)
	}
}
