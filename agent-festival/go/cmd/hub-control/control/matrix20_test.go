package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// #20 matrix: identity and authority come from this service's records, not
// from what the caller says about itself.

func TestI20_IdentityIsNotSelfReported(t *testing.T) {
	e := newEnv(t)
	pr, _ := e.s.Propose(e.planner, ProposeInput{Goal: "g", Material: []Material{snapshot}, Graph: demoGraph()})
	srv := httptest.NewServer(e.s.Handler())
	defer srv.Close()
	post := func(path, token string, hdr map[string]string, body any) (int, map[string]any) {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(string(raw(body))))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		must(t, err)
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	adopt := AdoptInput{CommandID: "c1", Task: pr.Task, Rev: pr.Rev, Digest: pr.Digest}
	if st, _ := post("/v1/commands/adopt", "", map[string]string{"X-User-ID": "alice"}, adopt); st != http.StatusUnauthorized {
		t.Fatalf("header identity accepted: %d", st)
	}
	bobTok, _, _ := e.s.Login("bob", "secret-bob")
	if st, _ := post("/v1/commands/adopt", bobTok, map[string]string{"X-User-ID": "alice"}, adopt); st != http.StatusForbidden {
		t.Fatalf("bob acting as alice via header: %d", st)
	}
	planTok, _, _ := e.s.Login("planner", "secret-planner")
	if st, _ := post("/v1/commands/adopt", planTok, nil, adopt); st != http.StatusForbidden {
		t.Fatalf("planner model approved its own plan: %d", st)
	}
	aliceTok, _, _ := e.s.Login("alice", "secret-alice")
	forged := aliceTok[:len(aliceTok)-4] + "AAAA"
	if st, _ := post("/v1/commands/adopt", forged, nil, adopt); st != http.StatusUnauthorized {
		t.Fatalf("forged token accepted: %d", st)
	}
	st, out := post("/v1/commands/adopt", aliceTok, nil, adopt)
	if st != http.StatusOK || out["approved_by"] != "alice" || out["assurance"] != AssuranceDemo {
		t.Fatalf("alice adopt: %d %v", st, out)
	}
	// unknown fields are refused rather than silently ignored
	if st, _ := post("/v1/commands/adopt", aliceTok, nil, map[string]any{"command_id": "c2", "approved_by": "ceo"}); st != http.StatusBadRequest {
		t.Fatalf("unknown field accepted: %d", st)
	}
}

func TestI20_AuthorityIsCheckedNowNotAtLogin(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	r := e.leaseAs(e.ex1, a.Task)
	rep := e.runAll(r)
	e.passChecks(rep.Artifact)
	aliceStale := e.alice // logged in before the change

	must(t, e.s.SetRoles("admin", "alice", []string{"propose", "approve"})) // accept role withdrawn
	_, err := e.s.Accept(aliceStale, AcceptInput{CommandID: "acc", Task: a.Task, Artifact: rep.Artifact})
	wantCode(t, err, CodeForbidden)

	must(t, e.s.Disable("admin", "ex1"))
	tok, _, err := e.s.Login("ex2", "secret-ex2")
	must(t, err)
	_ = tok
	_, err = e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "write", Iter: 1})
	if err == nil {
		t.Fatal("disabled executor still writes")
	}
	exTok, _, err := e.s.Login("ex1", "secret-ex1")
	wantCode(t, err, CodeUnauthed)
	_ = exTok
	// re-applying the bootstrap (a restart) must not re-enable the account
	must(t, e.s.PutPrincipal("bootstrap", Principal{ID: "ex1", Kind: KindExecutor}, "secret-ex1"))
	_, _, err = e.s.Login("ex1", "secret-ex1")
	wantCode(t, err, CodeUnauthed)
	// a checker cannot record verdicts for a checker it is not
	_, err = e.s.RecordCheck(e.calc, CheckInput{Artifact: rep.Artifact, Checker: "citation", CheckerVersion: "9", RuleVersion: "r1", Verdict: VerdictPass})
	wantCode(t, err, CodeForbidden)
}

func TestI20_VerdictsMapToDistinctAllowedActions(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		calc, cite string
		review     string
		want       string
		actions    []string
	}{
		{VerdictPass, VerdictPass, "", GateReady, []string{"accept"}},
		{VerdictPass, VerdictFail, "", GateRejected, []string{"revise_in_new_task", "open_case"}},
		{VerdictPass, VerdictHuman, "", GateNeedsHuman, []string{"review"}},
		{VerdictPass, VerdictHuman, VerdictPass, GateReady, []string{"accept"}},
		{VerdictPass, VerdictHuman, VerdictFail, GateRejected, []string{"revise_in_new_task", "open_case"}},
		{VerdictError, VerdictPass, "", GateCheckError, []string{"rerun_check"}},
		{VerdictPass, "", "", GateUncovered, []string{"wait_for_check"}},
	}
	for i, c := range cases {
		a := e.adopted("c" + string(rune('a'+i)))
		r := e.leaseAs(e.ex1, a.Task)
		e.runNode(r, "extract", 0, map[string]any{"i": i}, "agent:material")
		e.runNode(r, "operating", 0, map[string]any{"i": i}, "agent:analysis")
		e.runNode(r, "asset_quality", 0, map[string]any{"i": i}, "agent:analysis")
		rep := e.runNode(r, "write", 0, map[string]any{"report": i}, "agent:writer")
		_, err := e.s.RecordCheck(e.calc, CheckInput{Artifact: rep.Artifact, Checker: "calc", CheckerVersion: "1", RuleVersion: "r1", Verdict: c.calc})
		must(t, err)
		if c.cite != "" {
			ck, err := e.s.RecordCheck(e.ci, CheckInput{Artifact: rep.Artifact, Checker: "citation", CheckerVersion: "1", RuleVersion: "r1", Verdict: c.cite})
			must(t, err)
			if c.review != "" {
				_, err = e.s.Review(e.alice, ReviewInput{CommandID: "rv" + a.Task, CheckID: ck.ID, Verdict: c.review, Note: "第 15 页逐字核对"})
				must(t, err)
			}
		}
		g := e.view(a.Task).Gate
		if g.Status != c.want || strings.Join(g.AllowedActions, ",") != strings.Join(c.actions, ",") {
			t.Fatalf("case %d: gate %s %v, want %s %v (%v)", i, g.Status, g.AllowedActions, c.want, c.actions, g.Reasons)
		}
		_, err = e.s.Accept(e.alice, AcceptInput{CommandID: "acc" + a.Task, Task: a.Task, Artifact: rep.Artifact})
		if (c.want == GateReady) != (err == nil) {
			t.Fatalf("case %d: accept err = %v", i, err)
		}
	}
	// a checker flipping its verdict for the same versions is refused
	_, err := e.s.RecordCheck(e.calc, CheckInput{Artifact: e.view("T1").Gate.Artifact, Checker: "calc", CheckerVersion: "1", RuleVersion: "r1", Verdict: VerdictFail})
	wantCode(t, err, CodeConflict)
}

func TestI20_NoRulesIsNotPermission(t *testing.T) {
	e := newEnv(t)
	p := demoPolicy("ar-norules")
	p.RequiredChecks = nil
	_, err := e.s.PublishPolicy(e.admin, p)
	must(t, err)
	a := e.adopted("c1")
	r := e.leaseAs(e.ex1, a.Task)
	rep := e.runAll(r)
	g := e.view(a.Task).Gate
	if g.Status != GateNotConfigured || strings.Join(g.AllowedActions, ",") != "manual_handling" {
		t.Fatalf("gate = %+v", g)
	}
	_, err = e.s.Accept(e.alice, AcceptInput{CommandID: "acc", Task: a.Task, Artifact: rep.Artifact})
	wantCode(t, err, CodeBlocked)
}

func TestI20_TechnicalFailureIsNotABusinessRejection(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	r := e.leaseAs(e.ex1, a.Task)
	c, _ := e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	_, err := e.s.Commit(e.ex1, CommitInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Outcome: OutcomeBusinessRejected, Body: raw(map[string]any{"reason": "download 503"})})
	wantCode(t, err, CodeInvalid)
	must(t, e.s.Fail(e.ex1, FailInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Detail: "chinamoney 503"}))
	c2, err := e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	must(t, err)
	if c2.Action != "run" || c2.O != c.O || c2.A != 2 {
		t.Fatalf("retry after technical failure = %+v", c2)
	}
	n := e.node(a.Task, "extract")
	if n.Result != nil || n.Attempts[0].State != "technical_failure" {
		t.Fatalf("node = %+v", n)
	}
}

func TestI20_HistoryReadsNeverTriggerEffects(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	r := e.leaseAs(e.ex1, a.Task)
	ext := e.runNode(r, "extract", 0, map[string]any{"v": 1}, "agent:material")
	before := e.view(a.Task).LastEvent
	for i := 0; i < 3; i++ {
		e.view(a.Task)
		if _, err := e.s.Events(a.Task, 0, 100); err != nil {
			t.Fatal(err)
		}
	}
	if e.view(a.Task).LastEvent != before {
		t.Fatal("reads wrote events")
	}
	b := CallID(ext.O, "ask")
	_, err := e.s.Dispatch(e.ex1, CallRef{Task: r.Task, Token: r.Token, B: b})
	if CodeOf(err) != CodeConflict && CodeOf(err) != CodeFenced {
		t.Fatalf("finished call re-sent: %v", err)
	}
	if n := e.node(a.Task, "extract"); n.Calls[0].Sends != 1 {
		t.Fatalf("sends = %d", n.Calls[0].Sends)
	}
}

func TestI20_PermitBindsPayloadEpochAndSend(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	r := e.leaseAs(e.ex1, a.Task)
	c, _ := e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
	payload := raw(map[string]any{"q": "营业收入"})
	call, _ := e.s.BindCall(e.ex1, BindInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Name: "ask", Target: "agent:material", Payload: payload})
	pm, err := e.s.Dispatch(e.ex1, CallRef{Task: r.Task, Token: r.Token, B: call.B})
	must(t, err)
	if _, err := e.s.VerifyPermit(pm.Token, "agent:material", payload); err != nil {
		t.Fatalf("valid permit refused: %v", err)
	}
	_, err = e.s.VerifyPermit(pm.Token, "agent:material", raw(map[string]any{"q": "净利润"}))
	wantCode(t, err, CodeForbidden)
	_, err = e.s.VerifyPermit(pm.Token, "agent:other", payload)
	wantCode(t, err, CodeForbidden)
	// after a takeover the old permit is stale even before it expires
	e.advance(DefaultLeaseTTL + time.Second)
	e.s.PermitTTL = time.Hour
	_ = e.leaseAs(e.ex2, a.Task)
	_, err = e.s.VerifyPermit(pm.Token, "agent:material", payload)
	wantCode(t, err, CodeForbidden)
	// targets outside the capability are refused at bind time
	_, err = e.s.BindCall(e.ex1, BindInput{Task: r.Task, Token: r.Token, O: c.O, A: c.A, Name: "x", Target: "https://evil.example", Payload: payload})
	if err == nil {
		t.Fatal("unbound target accepted")
	}
}

func TestAdoptedGraphIsFrozen(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	_, err := e.s.Propose(e.planner, ProposeInput{TaskID: a.Task, Goal: "g", Material: []Material{snapshot}, Graph: demoGraph()})
	wantCode(t, err, CodeConflict)
	r := e.leaseAs(e.ex1, a.Task)
	_, err = e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "web_search"})
	wantCode(t, err, CodeForbidden)
	_, err = e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "write", Iter: 2})
	wantCode(t, err, CodeForbidden)
	_, err = e.s.Claim(e.ex1, ClaimInput{Task: r.Task, Token: r.Token, Node: "operating", Sources: []Material{snapshot}, Material: nodeInput("operating", nil)})
	wantCode(t, err, CodeNotReady)
	// a revised plan on a draft gets a new revision; approving the old digest is refused
	pr1, _ := e.s.Propose(e.planner, ProposeInput{Goal: "g", Material: []Material{snapshot}, Graph: demoGraph()})
	g2 := demoGraph()
	g2.Nodes = g2.Nodes[:1]
	g2.Report = "extract"
	pr2, _ := e.s.Propose(e.planner, ProposeInput{TaskID: pr1.Task, Goal: "g", Material: []Material{snapshot}, Graph: g2})
	_, err = e.s.Adopt(e.alice, AdoptInput{CommandID: "c2", Task: pr1.Task, Rev: pr2.Rev, Digest: pr1.Digest})
	wantCode(t, err, CodeConflict)
}

func TestConcurrentClaimsRunOnce(t *testing.T) {
	e := newEnv(t)
	a := e.adopted("c1")
	// two executors race for the lease
	var wg sync.WaitGroup
	leases := make([]*Lease, 2)
	for i, p := range []Principal{e.ex1, e.ex2} {
		wg.Add(1)
		go func(i int, p Principal) {
			defer wg.Done()
			l, err := e.s.Lease(p, LeaseInput{})
			if err == nil {
				leases[i] = l
			}
		}(i, p)
	}
	wg.Wait()
	if (leases[0] == nil) == (leases[1] == nil) {
		t.Fatalf("exactly one executor must win the lease: %v %v", leases[0], leases[1])
	}
	l, p := leases[0], e.ex1
	if l == nil {
		l, p = leases[1], e.ex2
	}
	// parallel branches of the same executor claiming the same node
	actions := make([]string, 8)
	for i := range actions {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := e.s.Claim(p, ClaimInput{Task: a.Task, Token: l.Token, Node: "extract", Sources: []Material{snapshot}, Material: nodeInput("extract", nil)})
			if err != nil {
				actions[i] = err.Error()
				return
			}
			actions[i] = c.Action
		}(i)
	}
	wg.Wait()
	runs := 0
	for _, x := range actions {
		switch x {
		case "run":
			runs++
		case "in_progress":
		default:
			t.Fatalf("unexpected claim outcome %q", x)
		}
	}
	if runs != 1 {
		t.Fatalf("runs = %d, want 1 (%v)", runs, actions)
	}
}
