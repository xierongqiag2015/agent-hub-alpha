package control

import (
	"encoding/json"
	"fmt"
	"strings"
)

// current re-reads the acting principal inside the transaction, so a role
// removed or an account disabled after login stops the action now.
func (x *Tx) current(p Principal, kind, role string) (Principal, error) {
	cur, err := x.principal(p.ID)
	if err != nil {
		return cur, err
	}
	if cur.Disabled {
		return cur, refuse(CodeForbidden, "account %q is disabled", p.ID)
	}
	return cur, need(cur, kind, role)
}

// Check verdicts.
const (
	VerdictPass  = "pass"
	VerdictFail  = "fail"
	VerdictHuman = "human" // the checker cannot decide; a person must
	VerdictError = "error" // the checker itself failed; not a judgement on the report
)

type CheckInput struct {
	Artifact       string          `json:"artifact"`
	Checker        string          `json:"checker"`
	CheckerVersion string          `json:"checker_version"`
	RuleVersion    string          `json:"rule_version"`
	Verdict        string          `json:"verdict"`
	Findings       json.RawMessage `json:"findings,omitempty"`
}

type CheckView struct {
	ID             int             `json:"id"`
	Artifact       string          `json:"artifact"`
	Checker        string          `json:"checker"`
	CheckerVersion string          `json:"checker_version"`
	RuleVersion    string          `json:"rule_version"`
	Verdict        string          `json:"verdict"`
	Findings       json.RawMessage `json:"findings,omitempty"`
	RecordedBy     string          `json:"recorded_by"`
	At             string          `json:"at"`
	Review         *ReviewView     `json:"review,omitempty"`
}

type ReviewView struct {
	Verdict   string `json:"verdict"`
	Note      string `json:"note"`
	Reviewer  string `json:"reviewer"`
	Assurance string `json:"assurance"`
	At        string `json:"at"`
}

// RecordCheck stores a checker's verdict on one exact artifact digest. A
// different verdict for the same (artifact, checker, versions) is refused: the
// checker is expected to be deterministic, and a flip needs a new version.
func (s *Store) RecordCheck(p Principal, in CheckInput) (CheckView, error) {
	var out CheckView
	err := s.tx("record_check", p.ID, func(x *Tx) error {
		if _, err := x.current(p, KindChecker, "check:"+in.Checker); err != nil {
			return err
		}
		switch in.Verdict {
		case VerdictPass, VerdictFail, VerdictHuman, VerdictError:
		default:
			return refuse(CodeInvalid, "verdict must be pass, fail, human or error")
		}
		if in.CheckerVersion == "" || in.RuleVersion == "" {
			return refuse(CodeInvalid, "checker_version and rule_version are required")
		}
		var n int
		if err := x.row(`SELECT COUNT(*) FROM artifacts WHERE digest=?`, in.Artifact).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return refuse(CodeNotFound, "artifact %s", in.Artifact)
		}
		findings := "null"
		if len(in.Findings) > 0 {
			findings = string(in.Findings)
		}
		var id int
		var verdict, oldF string
		err := x.row(`SELECT id, verdict, findings FROM checks WHERE artifact=? AND checker=? AND checker_version=? AND rule_version=?`,
			in.Artifact, in.Checker, in.CheckerVersion, in.RuleVersion).Scan(&id, &verdict, &oldF)
		switch {
		case err == nil:
			if verdict != in.Verdict {
				return refuse(CodeConflict, "check %d already recorded %s for this artifact and versions", id, verdict)
			}
		case noRows(err):
			if err := x.exec(`INSERT INTO checks(artifact,checker,checker_version,rule_version,verdict,findings,recorded_by,at) VALUES(?,?,?,?,?,?,?,?)`,
				in.Artifact, in.Checker, in.CheckerVersion, in.RuleVersion, in.Verdict, findings, p.ID, x.ts()); err != nil {
				return err
			}
			if err := x.row(`SELECT last_insert_rowid()`).Scan(&id); err != nil {
				return err
			}
			if err := x.event("check_recorded", "", "", "", map[string]any{"id": id, "artifact": in.Artifact, "checker": in.Checker, "verdict": in.Verdict}); err != nil {
				return err
			}
		default:
			return err
		}
		c, err := x.check(id)
		if err != nil {
			return err
		}
		out = *c
		return nil
	})
	return out, err
}

func (x *Tx) check(id int) (*CheckView, error) {
	var c CheckView
	var findings string
	err := x.row(`SELECT id,artifact,checker,checker_version,rule_version,verdict,findings,recorded_by,at FROM checks WHERE id=?`, id).
		Scan(&c.ID, &c.Artifact, &c.Checker, &c.CheckerVersion, &c.RuleVersion, &c.Verdict, &findings, &c.RecordedBy, &c.At)
	if noRows(err) {
		return nil, refuse(CodeNotFound, "check %d", id)
	}
	if err != nil {
		return nil, err
	}
	c.Findings = json.RawMessage(findings)
	var r ReviewView
	switch err := x.row(`SELECT verdict,note,reviewer,assurance,at FROM reviews WHERE check_id=?`, id).Scan(&r.Verdict, &r.Note, &r.Reviewer, &r.Assurance, &r.At); {
	case err == nil:
		c.Review = &r
	case !noRows(err):
		return nil, err
	}
	return &c, nil
}

type ReviewInput struct {
	CommandID string `json:"command_id"`
	CheckID   int    `json:"check_id"`
	Verdict   string `json:"verdict"` // pass | fail
	Note      string `json:"note"`
}

// Review is a person's decision on a check the checker handed over (human).
func (s *Store) Review(p Principal, in ReviewInput) (CheckView, error) {
	var out CheckView
	err := s.tx("review", p.ID, func(x *Tx) error {
		cur, err := x.current(p, KindUser, "review")
		if err != nil {
			return err
		}
		_, err = x.command(cur, in.CommandID, "review", in, &out, func() (any, error) {
			if in.Verdict != VerdictPass && in.Verdict != VerdictFail {
				return nil, refuse(CodeInvalid, "review verdict must be pass or fail")
			}
			c, err := x.check(in.CheckID)
			if err != nil {
				return nil, err
			}
			if c.Verdict != VerdictHuman {
				return nil, refuse(CodeConflict, "check %d is %s, not waiting for a person", c.ID, c.Verdict)
			}
			if c.Review != nil {
				return nil, refuse(CodeConflict, "check %d was already reviewed (%s by %s)", c.ID, c.Review.Verdict, c.Review.Reviewer)
			}
			if strings.TrimSpace(in.Note) == "" {
				return nil, refuse(CodeInvalid, "a review needs a note saying what was checked")
			}
			if err := x.exec(`INSERT INTO reviews(check_id,verdict,note,reviewer,assurance,at) VALUES(?,?,?,?,?,?)`,
				c.ID, in.Verdict, in.Note, cur.ID, cur.Assurance, x.ts()); err != nil {
				return nil, err
			}
			c, err = x.check(in.CheckID)
			if err != nil {
				return nil, err
			}
			return c, x.event("check_reviewed", "", "", "", map[string]any{"check": c.ID, "verdict": in.Verdict})
		})
		return err
	})
	return out, err
}

// Gate statuses of a task's report.
const (
	GateNotReady      = "not_ready"      // the report node has no formal result yet
	GateNotConfigured = "not_configured" // policy names no required checks: no automatic delivery
	GateUncovered     = "uncovered"      // a required check has not run on this exact digest
	GateCheckError    = "check_error"    // a checker failed to run; rerun it
	GateNeedsHuman    = "needs_human"
	GateRejected      = "rejected"
	GateReady         = "ready"
	GateDelivered     = "delivered"
	GateStaleBasis    = "stale_basis" // knowledge the report rests on was revoked
)

type Gate struct {
	Status         string      `json:"status"`
	Artifact       string      `json:"artifact,omitempty"`
	O              string      `json:"o,omitempty"`
	Reasons        []string    `json:"reasons,omitempty"`
	Checks         []CheckView `json:"checks,omitempty"`
	AllowedActions []string    `json:"allowed_actions"`
}

var gateActions = map[string][]string{
	GateNotReady:      {},
	GateNotConfigured: {"manual_handling"},
	GateUncovered:     {"wait_for_check"},
	GateCheckError:    {"rerun_check"},
	GateNeedsHuman:    {"review"},
	GateRejected:      {"revise_in_new_task", "open_case"},
	GateReady:         {"accept"},
	GateDelivered:     {"open_case"},
	GateStaleBasis:    {"revise_in_new_task", "open_case"},
}

// gate decides what may happen to the task's report now. Checks count only
// if they were recorded on exactly the committed report digest under the
// rule versions the task's policy requires.
func (x *Tx) gate(t taskRow) (g Gate, err error) {
	defer func() { g.AllowedActions = gateActions[g.Status] }()
	var delivered string
	if err := x.row(`SELECT artifact FROM deliveries WHERE task=?`, t.ID).Scan(&delivered); err == nil {
		g.Status, g.Artifact = GateDelivered, delivered
		return g, nil
	} else if !noRows(err) {
		return g, err
	}
	var o string
	err = x.row(`SELECT r.o FROM results r JOIN node_exec e ON e.o=r.o WHERE e.task=? AND e.rev=? AND e.node=? ORDER BY e.iter DESC LIMIT 1`,
		t.ID, t.Rev, t.Graph.Report).Scan(&o)
	if noRows(err) {
		g.Status = GateNotReady
		return g, nil
	}
	if err != nil {
		return g, err
	}
	r, err := x.result(o, false)
	if err != nil {
		return g, err
	}
	g.O, g.Artifact = o, r.Artifact
	if r.Outcome == OutcomeBusinessRejected {
		g.Status, g.Reasons = GateRejected, []string{"报告节点记录的是业务拒绝"}
		return g, nil
	}
	if stale, err := x.taskBasisRevoked(t); err != nil {
		return g, err
	} else if len(stale) > 0 {
		g.Status, g.Reasons = GateStaleBasis, append([]string{"报告依据的知识已撤回，禁止交付；已有记录保持不变"}, stale...)
		return g, nil
	}
	pol, err := x.policy(t.Policy)
	if err != nil {
		return g, err
	}
	if len(pol.RequiredChecks) == 0 {
		g.Status, g.Reasons = GateNotConfigured, []string{fmt.Sprintf("策略 %s 没有配置必需检查，禁止自动交付", t.Policy)}
		return g, nil
	}
	worst := GateReady
	rank := map[string]int{GateReady: 0, GateUncovered: 1, GateNeedsHuman: 2, GateCheckError: 3, GateRejected: 4}
	for _, rc := range pol.RequiredChecks {
		var id int
		err := x.row(`SELECT id FROM checks WHERE artifact=? AND checker=? AND rule_version=? ORDER BY id DESC LIMIT 1`, r.Artifact, rc.Checker, rc.RuleVersion).Scan(&id)
		st := ""
		if noRows(err) {
			st = GateUncovered
			g.Reasons = append(g.Reasons, fmt.Sprintf("检查器 %s（规则 %s）还没有检查这个报告摘要", rc.Checker, rc.RuleVersion))
		} else if err != nil {
			return g, err
		} else {
			c, err := x.check(id)
			if err != nil {
				return g, err
			}
			g.Checks = append(g.Checks, *c)
			switch {
			case c.Verdict == VerdictPass:
				st = GateReady
			case c.Verdict == VerdictFail:
				st = GateRejected
				g.Reasons = append(g.Reasons, fmt.Sprintf("检查器 %s 判定不通过（检查 %d）", rc.Checker, c.ID))
			case c.Verdict == VerdictError:
				st = GateCheckError
				g.Reasons = append(g.Reasons, fmt.Sprintf("检查器 %s 运行出错（检查 %d），需要重跑", rc.Checker, c.ID))
			case c.Review == nil:
				st = GateNeedsHuman
				g.Reasons = append(g.Reasons, fmt.Sprintf("检查器 %s 把检查 %d 转给人工复核", rc.Checker, c.ID))
			case c.Review.Verdict == VerdictPass:
				st = GateReady
			default:
				st = GateRejected
				g.Reasons = append(g.Reasons, fmt.Sprintf("复核人 %s 判定检查 %d 不通过", c.Review.Reviewer, c.ID))
			}
		}
		if rank[st] > rank[worst] {
			worst = st
		}
	}
	g.Status = worst
	return g, nil
}

type AcceptInput struct {
	CommandID string `json:"command_id"`
	Task      string `json:"task"`
	Artifact  string `json:"artifact"` // the digest the person looked at
}

type Delivery struct {
	Task       string   `json:"task"`
	Artifact   string   `json:"artifact"`
	Checks     []int    `json:"checks"`
	Policy     string   `json:"policy_version"`
	ApprovedBy string   `json:"approved_by"`
	Assurance  string   `json:"assurance"`
	Replayed   bool     `json:"replayed,omitempty"`
	Gate       []string `json:"-"`
}

// Accept delivers the report: only the committed report digest, only when
// every required check passed on that digest, only by a person who holds the
// accept role at this moment. Once delivered the task is closed to executors.
func (s *Store) Accept(p Principal, in AcceptInput) (Delivery, error) {
	var out Delivery
	err := s.tx("accept", p.ID, func(x *Tx) error {
		cur, err := x.current(p, KindUser, "accept")
		if err != nil {
			return err
		}
		replayed, err := x.command(cur, in.CommandID, "accept", in, &out, func() (any, error) {
			t, err := x.task(in.Task)
			if err != nil {
				return nil, err
			}
			if t.Status == "delivered" {
				return nil, refuse(CodeConflict, "task %s was already delivered by another action", t.ID)
			}
			if t.Status != "adopted" {
				return nil, refuse(CodeConflict, "task %s is %s", t.ID, t.Status)
			}
			g, err := x.gate(t)
			if err != nil {
				return nil, err
			}
			if g.Artifact != "" && in.Artifact != g.Artifact {
				return nil, refuse(CodeConflict, "the report you reviewed (%.24s…) is not the committed report (%.24s…); review the current one", in.Artifact, g.Artifact)
			}
			if g.Status != GateReady {
				return nil, refuse(CodeBlocked, "report cannot be delivered: %s %v", g.Status, g.Reasons)
			}
			var ids []int
			for _, c := range g.Checks {
				ids = append(ids, c.ID)
			}
			d := Delivery{Task: t.ID, Artifact: g.Artifact, Checks: ids, Policy: t.Policy, ApprovedBy: cur.ID, Assurance: cur.Assurance}
			if err := x.exec(`INSERT INTO deliveries(task,artifact,checks,policy_version,approved_by,assurance,at) VALUES(?,?,?,?,?,?,?)`,
				t.ID, d.Artifact, mustJSON(ids), d.Policy, d.ApprovedBy, d.Assurance, x.ts()); err != nil {
				return nil, err
			}
			if err := x.exec(`UPDATE tasks SET status='delivered', lease_token_hash=NULL, lease_owner=NULL, lease_until=NULL, updated_at=? WHERE id=?`, x.ts(), t.ID); err != nil {
				return nil, err
			}
			return d, x.event("report_delivered", t.ID, g.O, "", d)
		})
		out.Replayed = replayed
		return err
	})
	return out, err
}

type CaseInput struct {
	CommandID string `json:"command_id"`
	Task      string `json:"task"`
	Ref       string `json:"ref"` // artifact digest, node execution o or call b in this task
	Text      string `json:"text"`
}

type CaseView struct {
	ID       string `json:"id"`
	Task     string `json:"task"`
	Ref      string `json:"ref"`
	Text     string `json:"text"`
	OpenedBy string `json:"opened_by"`
	Policy   string `json:"policy_version"`
	Status   string `json:"status"`
	At       string `json:"at"`
	Replayed bool   `json:"replayed,omitempty"`
}

// OpenCase records feedback against concrete evidence of a task. It changes
// nothing in the task; a fix arrives as a new policy or material version that
// later tasks bind (A14).
func (s *Store) OpenCase(p Principal, in CaseInput) (CaseView, error) {
	var out CaseView
	err := s.tx("open_case", p.ID, func(x *Tx) error {
		cur, err := x.current(p, KindUser, "")
		if err != nil {
			return err
		}
		replayed, err := x.command(cur, in.CommandID, "open_case", in, &out, func() (any, error) {
			t, err := x.task(in.Task)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.Text) == "" {
				return nil, refuse(CodeInvalid, "case text is required")
			}
			var n int
			if err := x.row(`SELECT (SELECT COUNT(*) FROM results r JOIN node_exec e ON e.o=r.o WHERE e.task=? AND (r.artifact=? OR r.o=?))
				+ (SELECT COUNT(*) FROM calls c JOIN node_exec e ON e.o=c.o WHERE e.task=? AND c.b=?)`, t.ID, in.Ref, in.Ref, t.ID, in.Ref).Scan(&n); err != nil {
				return nil, err
			}
			if n == 0 {
				return nil, refuse(CodeNotFound, "%q is not an artifact, node execution or call of task %s", in.Ref, t.ID)
			}
			k, err := x.next("case")
			if err != nil {
				return nil, err
			}
			c := CaseView{ID: fmt.Sprintf("C%d", k), Task: t.ID, Ref: in.Ref, Text: in.Text, OpenedBy: cur.ID, Policy: t.Policy, Status: "open", At: x.ts()}
			if err := x.exec(`INSERT INTO cases(id,task,ref,text,opened_by,policy_version,status,at) VALUES(?,?,?,?,?,?,?,?)`,
				c.ID, c.Task, c.Ref, c.Text, c.OpenedBy, c.Policy, c.Status, c.At); err != nil {
				return nil, err
			}
			return c, x.event("case_opened", t.ID, "", "", c)
		})
		out.Replayed = replayed
		return err
	})
	return out, err
}
