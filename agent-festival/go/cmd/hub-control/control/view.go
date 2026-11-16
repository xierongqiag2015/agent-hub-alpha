package control

import (
	"encoding/json"
)

// TaskView is the authoritative projection the user interface renders. It is
// computed from the records, so a refreshed or reconnected page shows the
// same state (A12) and never more than what was committed.
type TaskView struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Paused      bool       `json:"paused"`
	Goal        string     `json:"goal"`
	Material    []Material `json:"material"`
	Rev         int        `json:"adopted_rev,omitempty"`
	Digest      string     `json:"adopted_digest,omitempty"`
	Policy      string     `json:"policy_version,omitempty"`
	ApprovedBy  string     `json:"approved_by,omitempty"`
	Assurance   string     `json:"approval_assurance,omitempty"`
	Epoch       int        `json:"lease_epoch"`
	LeaseOwner  string     `json:"lease_owner,omitempty"`
	Graph       Graph      `json:"graph"`
	Candidates  []GraphRev `json:"candidates"`
	Nodes       []NodeView `json:"nodes"`
	Gate        *Gate      `json:"report_gate,omitempty"`
	Cases       []CaseView `json:"cases,omitempty"`
	Knowledge   TaskKnow   `json:"knowledge"`
	LastEvent   int        `json:"last_event_seq"`
	IdentityTag string     `json:"identity_note"`
}

// TaskKnow summarises the task's knowledge consumption.
type TaskKnow struct {
	Hits          int      `json:"hits"`
	Misses        int      `json:"misses"`
	NotApplicable int      `json:"not_applicable"`
	SavedCalls    int      `json:"saved_calls"`
	BasisRevoked  []string `json:"basis_revoked,omitempty"` // shown even after delivery; history is not rewritten
}

type GraphRev struct {
	Rev      int      `json:"rev"`
	Digest   string   `json:"digest"`
	Status   string   `json:"status"`
	Problems []string `json:"problems,omitempty"`
	By       string   `json:"proposed_by"`
	Graph    Graph    `json:"graph"` // exactly what an approver confirms with Digest
}

type NodeView struct {
	O        string        `json:"o"`
	Node     string        `json:"node"`
	Iter     int           `json:"iter"`
	State    string        `json:"state"` // running | completed | business_rejected | failed_attempts | lost
	Attempts []AttemptView `json:"attempts"`
	Reused   int           `json:"reused"`
	Result   *ResultView   `json:"result,omitempty"`
	Calls    []CallView    `json:"calls,omitempty"`
	Activity string        `json:"activity,omitempty"`
	Sources  []string      `json:"sources,omitempty"`
	Lookup   *LookupView   `json:"lookup,omitempty"`
	Saved    int           `json:"saved_calls,omitempty"`
	Origin   string        `json:"origin,omitempty"`
	Revoked  string        `json:"basis_revoked,omitempty"`
}

type AttemptView struct {
	A      int    `json:"a"`
	Epoch  int    `json:"epoch"`
	Owner  string `json:"owner"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// View returns the projection of one task. Users and agents may read it.
func (s *Store) View(p Principal, id string) (*TaskView, error) {
	var v *TaskView
	err := s.read(func(x *Tx) error {
		t, err := x.task(id)
		if err != nil {
			return err
		}
		v = &TaskView{ID: t.ID, Status: t.Status, Paused: t.Paused, Goal: t.Goal, Material: t.Material, Rev: t.Rev, Digest: t.Digest,
			Policy: t.Policy, ApprovedBy: t.Approver, Assurance: t.Assure, Epoch: t.Epoch, Graph: t.Graph,
			IdentityTag: "演示身份（demo-account）：由本服务签发的演示账号，不代表企业身份认证已接入"}
		var owner *string
		if err := x.row(`SELECT lease_owner FROM tasks WHERE id=?`, id).Scan(&owner); err != nil {
			return err
		}
		if owner != nil {
			v.LeaseOwner = *owner
		}
		rows, err := x.tx.Query(`SELECT rev,digest,status,problems,proposed_by,doc FROM graphs WHERE task=? ORDER BY rev`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var g GraphRev
			var probs, doc string
			if err := rows.Scan(&g.Rev, &g.Digest, &g.Status, &probs, &g.By, &doc); err != nil {
				rows.Close()
				return err
			}
			_ = json.Unmarshal([]byte(probs), &g.Problems)
			var pd planDoc
			_ = json.Unmarshal([]byte(doc), &pd)
			g.Graph = pd.Graph
			v.Candidates = append(v.Candidates, g)
		}
		rows.Close()
		var os []string
		rows, err = x.tx.Query(`SELECT o FROM node_exec WHERE task=? ORDER BY node, iter`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var o string
			if err := rows.Scan(&o); err != nil {
				rows.Close()
				return err
			}
			os = append(os, o)
		}
		rows.Close()
		for _, o := range os {
			nv, err := x.nodeView(o)
			if err != nil {
				return err
			}
			v.Nodes = append(v.Nodes, *nv)
		}
		if t.Status != "draft" {
			g, err := x.gate(t)
			if err != nil {
				return err
			}
			v.Gate = &g
			if v.Knowledge.BasisRevoked, err = x.taskBasisRevoked(t); err != nil {
				return err
			}
		}
		for _, n := range v.Nodes {
			if n.Lookup != nil {
				switch n.Lookup.Outcome {
				case LookupHit:
					v.Knowledge.Hits++
				case LookupMiss:
					v.Knowledge.Misses++
				default:
					v.Knowledge.NotApplicable++
				}
			}
			v.Knowledge.SavedCalls += n.Saved
		}
		rows, err = x.tx.Query(`SELECT id,task,ref,text,opened_by,policy_version,status,at FROM cases WHERE task=? ORDER BY at`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c CaseView
			if err := rows.Scan(&c.ID, &c.Task, &c.Ref, &c.Text, &c.OpenedBy, &c.Policy, &c.Status, &c.At); err != nil {
				rows.Close()
				return err
			}
			v.Cases = append(v.Cases, c)
		}
		rows.Close()
		return x.row(`SELECT COALESCE(MAX(seq),0) FROM events`).Scan(&v.LastEvent)
	})
	return v, err
}

func (x *Tx) nodeView(o string) (*NodeView, error) {
	n := &NodeView{O: o}
	if err := x.row(`SELECT node, iter, reused FROM node_exec WHERE o=?`, o).Scan(&n.Node, &n.Iter, &n.Reused); err != nil {
		return nil, err
	}
	rows, err := x.tx.Query(`SELECT a,epoch,owner,state,detail FROM attempts WHERE o=? ORDER BY a`, o)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a AttemptView
		if err := rows.Scan(&a.A, &a.Epoch, &a.Owner, &a.State, &a.Detail); err != nil {
			rows.Close()
			return nil, err
		}
		n.Attempts = append(n.Attempts, a)
	}
	rows.Close()
	r, err := x.result(o, false)
	if err != nil {
		return nil, err
	}
	n.Result = r
	switch {
	case r != nil:
		n.State = r.Outcome
	case len(n.Attempts) > 0 && n.Attempts[len(n.Attempts)-1].State == "running":
		n.State = "running"
	case len(n.Attempts) > 0:
		n.State = n.Attempts[len(n.Attempts)-1].State
	default:
		n.State = "claimed"
	}
	rows, err = x.tx.Query(`SELECT b FROM calls WHERE o=? ORDER BY b`, o)
	if err != nil {
		return nil, err
	}
	var bs []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			rows.Close()
			return nil, err
		}
		bs = append(bs, b)
	}
	rows.Close()
	for _, b := range bs {
		c, err := x.call(b)
		if err != nil {
			return nil, err
		}
		n.Calls = append(n.Calls, *c)
	}
	var detail *string
	_ = x.row(`SELECT p.detail FROM progress p WHERE p.o=? ORDER BY p.a DESC, p.seq DESC LIMIT 1`, o).Scan(&detail)
	if detail != nil {
		n.Activity = *detail
	}
	rows, err = x.tx.Query(`SELECT source FROM node_sources WHERE o=? ORDER BY source`, o)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, err
		}
		n.Sources = append(n.Sources, s)
	}
	rows.Close()
	var lk LookupView
	var src *string
	switch err := x.row(`SELECT outcome, reason, source_o, at FROM knowledge_lookups WHERE o=? ORDER BY a DESC LIMIT 1`, o).Scan(&lk.Outcome, &lk.Reason, &src, &lk.At); {
	case err == nil:
		if src != nil {
			lk.SourceO = *src
		}
		n.Lookup = &lk
	case !noRows(err):
		return nil, err
	}
	if r != nil && r.ReusedFrom != "" {
		if n.Saved, n.Origin, err = x.savedCalls(o); err != nil {
			return nil, err
		}
	}
	if r != nil {
		if n.Revoked, err = x.basisRevoked(o); err != nil {
			return nil, err
		}
	}
	return n, nil
}

type Event struct {
	Seq    int             `json:"seq"`
	At     string          `json:"at"`
	Actor  string          `json:"actor"`
	Kind   string          `json:"kind"`
	Task   string          `json:"task,omitempty"`
	O      string          `json:"o,omitempty"`
	B      string          `json:"b,omitempty"`
	Detail json.RawMessage `json:"detail"`
}

// Events returns the audit trail after seq, optionally for one task.
func (s *Store) Events(task string, after, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var out []Event
	err := s.read(func(x *Tx) error {
		q := `SELECT seq,at,actor,kind,task,o,b,detail FROM events WHERE seq>?`
		args := []any{after}
		if task != "" {
			q += ` AND task=?`
			args = append(args, task)
		}
		rows, err := x.tx.Query(q+` ORDER BY seq LIMIT ?`, append(args, limit)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Event
			var d string
			if err := rows.Scan(&e.Seq, &e.At, &e.Actor, &e.Kind, &e.Task, &e.O, &e.B, &d); err != nil {
				return err
			}
			e.Detail = json.RawMessage(d)
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// Artifact returns stored canonical bytes by digest.
func (s *Store) Artifact(digest string) ([]byte, error) {
	var body []byte
	err := s.read(func(x *Tx) error {
		err := x.row(`SELECT body FROM artifacts WHERE digest=?`, digest).Scan(&body)
		if noRows(err) {
			return refuse(CodeNotFound, "artifact %s", digest)
		}
		return err
	})
	return body, err
}

// Unresolved lists the task's calls without a confirmed outcome and what may
// be done about each.
func (s *Store) Unresolved(task string) ([]CallView, error) {
	var out []CallView
	err := s.read(func(x *Tx) error {
		var err error
		out, err = x.unresolvedCalls(task)
		return err
	})
	return out, err
}

type TaskSummary struct {
	ID         string `json:"id"`
	Goal       string `json:"goal"`
	Status     string `json:"status"`
	Paused     bool   `json:"paused"`
	Policy     string `json:"policy_version,omitempty"`
	ApprovedBy string `json:"approved_by,omitempty"`
	Epoch      int    `json:"lease_epoch"`
	Gate       string `json:"report_gate,omitempty"`
	Unresolved int    `json:"unresolved_calls"`
	UpdatedAt  string `json:"updated_at"`
}

// Tasks lists the most recently updated tasks for the console.
func (s *Store) Tasks(limit int) ([]TaskSummary, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []TaskSummary{}
	err := s.read(func(x *Tx) error {
		rows, err := x.tx.Query(`SELECT id, updated_at FROM tasks ORDER BY updated_at DESC, id DESC LIMIT ?`, limit)
		if err != nil {
			return err
		}
		type ref struct{ id, at string }
		var refs []ref
		for rows.Next() {
			var r ref
			if err := rows.Scan(&r.id, &r.at); err != nil {
				rows.Close()
				return err
			}
			refs = append(refs, r)
		}
		rows.Close()
		for _, r := range refs {
			t, err := x.task(r.id)
			if err != nil {
				return err
			}
			sum := TaskSummary{ID: t.ID, Goal: t.Goal, Status: t.Status, Paused: t.Paused, Policy: t.Policy, ApprovedBy: t.Approver, Epoch: t.Epoch, UpdatedAt: r.at}
			if t.Status != "draft" {
				g, err := x.gate(t)
				if err != nil {
					return err
				}
				sum.Gate = g.Status
			}
			if err := x.row(`SELECT COUNT(*) FROM calls WHERE state IN ('dispatched','accepted','unknown') AND o IN (SELECT o FROM node_exec WHERE task=?)`, t.ID).Scan(&sum.Unresolved); err != nil {
				return err
			}
			out = append(out, sum)
		}
		return nil
	})
	return out, err
}
