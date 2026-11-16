package control

import (
	"encoding/json"
	"fmt"
)

// DefaultScope is the single business scope of the first-week demo. Commands
// are unique per (scope, issuer, command_id).
const DefaultScope = "annual-report-demo"

// command runs fn at most once per (scope, issuer, command_id) and stores its
// result. A repeat with the same content returns the stored result without
// running fn again (A03); a repeat with different content is refused and the
// original record is left untouched (A04). A refusal from fn rolls back with
// the transaction, so a corrected retry under the same id is still possible.
func (x *Tx) command(p Principal, id, kind string, params any, out any, fn func() (any, error)) (replayed bool, err error) {
	if id == "" || len(id) > 128 {
		return false, refuse(CodeInvalid, "command_id is required (generated once per user action by the trusted entry)")
	}
	k, err := kappa("command:"+kind, params)
	if err != nil {
		return false, err
	}
	var oldKind, oldKappa, stored string
	err = x.row(`SELECT kind,kappa,result FROM commands WHERE scope=? AND issuer=? AND command_id=?`,
		DefaultScope, p.ID, id).Scan(&oldKind, &oldKappa, &stored)
	switch {
	case err == nil:
		if oldKind != kind || oldKappa != k {
			return false, refuse(CodeConflict, "command %s was already used for different content; the original stays in force", id)
		}
		return true, json.Unmarshal([]byte(stored), out)
	case !noRows(err):
		return false, err
	}
	res, err := fn()
	if err != nil {
		return false, err
	}
	raw := mustJSON(res)
	if err := x.exec(`INSERT INTO commands(scope,issuer,command_id,kind,kappa,result,at) VALUES(?,?,?,?,?,?,?)`,
		DefaultScope, p.ID, id, kind, k, raw, x.ts()); err != nil {
		return false, err
	}
	if err := x.event("command", "", "", "", map[string]any{"kind": kind, "command_id": id, "kappa": k}); err != nil {
		return false, err
	}
	return false, json.Unmarshal([]byte(raw), out)
}

// ProposeInput is a candidate plan from the planner (or a user).
type ProposeInput struct {
	TaskID   string     `json:"task_id,omitempty"` // revise an existing draft; empty creates a task
	Goal     string     `json:"goal"`
	Material []Material `json:"material"`
	Graph    Graph      `json:"graph"`
}

type Proposal struct {
	Task      string   `json:"task"`
	Rev       int      `json:"rev"`
	Digest    string   `json:"digest"` // what an approver must confirm: goal + material + graph
	Problems  []string `json:"problems,omitempty"`
	Mountable bool     `json:"mountable"`
}

type planDoc struct {
	Goal     string     `json:"goal"`
	Material []Material `json:"material"`
	Graph    Graph      `json:"graph"`
}

// Propose records a candidate graph. Planners may propose; only a user with
// the approve role can turn a candidate into the task's adopted graph.
func (s *Store) Propose(p Principal, in ProposeInput) (Proposal, error) {
	doc := planDoc{Goal: in.Goal, Material: in.Material, Graph: in.Graph}
	digest, err := kappa("plan", doc)
	if err != nil {
		return Proposal{}, err
	}
	var out Proposal
	err = s.tx("propose", p.ID, func(x *Tx) error {
		cur, err := x.principal(p.ID)
		if err != nil {
			return err
		}
		if cur.Disabled || cur.Kind != KindAgent && cur.Kind != KindUser || !cur.Has("propose") {
			return refuse(CodeForbidden, "%q cannot propose plans", p.ID)
		}
		pol, err := x.activePolicy()
		if err != nil {
			return err
		}
		problems := CheckGraph(in.Graph, pol)
		probs, err := x.materialProblems(in.Material)
		if err != nil {
			return err
		}
		problems = append(problems, probs...)
		task := in.TaskID
		if task == "" {
			n, err := x.next("task")
			if err != nil {
				return err
			}
			task = fmt.Sprintf("T%d", n)
			if err := x.exec(`INSERT INTO tasks(id,scope,goal,material,proposed_by,status,created_at,updated_at) VALUES(?,?,?,?,?,'draft',?,?)`,
				task, DefaultScope, in.Goal, mustJSON(in.Material), p.ID, x.ts(), x.ts()); err != nil {
				return err
			}
		} else {
			var status string
			if err := x.row(`SELECT status FROM tasks WHERE id=?`, task).Scan(&status); err != nil {
				return refuse(CodeNotFound, "task %s", task)
			}
			if status != "draft" {
				return refuse(CodeConflict, "task %s is %s: its adopted graph is frozen; changing goal or topology needs a new task", task, status)
			}
			if err := x.exec(`UPDATE tasks SET goal=?, material=?, updated_at=? WHERE id=?`, in.Goal, mustJSON(in.Material), x.ts(), task); err != nil {
				return err
			}
		}
		var rev int
		if err := x.row(`SELECT COALESCE(MAX(rev),0)+1 FROM graphs WHERE task=?`, task).Scan(&rev); err != nil {
			return err
		}
		status := "candidate"
		if len(problems) > 0 {
			status = "rejected"
		}
		if err := x.exec(`INSERT INTO graphs(task,rev,digest,doc,proposed_by,problems,status,at) VALUES(?,?,?,?,?,?,?,?)`,
			task, rev, digest, mustJSON(doc), p.ID, mustJSON(problems), status, x.ts()); err != nil {
			return err
		}
		out = Proposal{Task: task, Rev: rev, Digest: digest, Problems: problems, Mountable: len(problems) == 0}
		return x.event("graph_proposed", task, "", "", out)
	})
	return out, err
}

func (x *Tx) materialProblems(ms []Material) ([]string, error) {
	if len(ms) == 0 {
		return []string{"计划没有指定材料快照"}, nil
	}
	var out []string
	for _, m := range ms {
		var n int
		if err := x.row(`SELECT COUNT(*) FROM materials WHERE id=? AND sha256=?`, m.ID, m.SHA256).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			out = append(out, fmt.Sprintf("材料 %s@%.12s 不是已登记的快照", m.ID, m.SHA256))
		} else if why, err := x.revoked(SourceRef(m)); err != nil {
			return nil, err
		} else if why != "" {
			out = append(out, fmt.Sprintf("材料 %s@%.12s 已撤回：%s", m.ID, m.SHA256, why))
		}
	}
	return out, nil
}

type AdoptInput struct {
	CommandID string `json:"command_id"`
	Task      string `json:"task"`
	Rev       int    `json:"rev"`
	Digest    string `json:"digest"`
}

type Adoption struct {
	Task       string `json:"task"`
	Rev        int    `json:"rev"`
	Digest     string `json:"digest"`
	Policy     string `json:"policy_version"`
	ApprovedBy string `json:"approved_by"`
	Assurance  string `json:"assurance"`
	Replayed   bool   `json:"replayed,omitempty"`
}

// Adopt makes a checked candidate the task's one adopted graph. The approver
// confirms the digest they were shown; the graph is re-checked against the
// policy active now, and that policy version is bound to the task.
func (s *Store) Adopt(p Principal, in AdoptInput) (Adoption, error) {
	if err := need(p, KindUser, "approve"); err != nil {
		return Adoption{}, err
	}
	var out Adoption
	err := s.tx("adopt", p.ID, func(x *Tx) error {
		cur, err := x.current(p, KindUser, "approve")
		if err != nil {
			return err
		}
		p = cur
		replayed, err := x.command(p, in.CommandID, "adopt", in, &out, func() (any, error) {
			var status string
			var adoptedRev *int
			var adoptedDigest *string
			if err := x.row(`SELECT status, adopted_rev, adopted_digest FROM tasks WHERE id=?`, in.Task).Scan(&status, &adoptedRev, &adoptedDigest); err != nil {
				return nil, refuse(CodeNotFound, "task %s", in.Task)
			}
			if status != "draft" {
				if adoptedRev != nil && *adoptedRev == in.Rev && adoptedDigest != nil && *adoptedDigest == in.Digest {
					return nil, refuse(CodeConflict, "task %s is already adopted at rev %d by another action; no second run is started", in.Task, in.Rev)
				}
				return nil, refuse(CodeConflict, "task %s already has an adopted graph; it cannot be replaced, create a new task", in.Task)
			}
			var digest, docRaw, gstatus string
			if err := x.row(`SELECT digest, doc, status FROM graphs WHERE task=? AND rev=?`, in.Task, in.Rev).Scan(&digest, &docRaw, &gstatus); err != nil {
				return nil, refuse(CodeNotFound, "task %s has no rev %d", in.Task, in.Rev)
			}
			if digest != in.Digest {
				return nil, refuse(CodeConflict, "the plan changed after it was shown; review rev %d again", in.Rev)
			}
			var doc planDoc
			if err := json.Unmarshal([]byte(docRaw), &doc); err != nil {
				return nil, err
			}
			pol, err := x.activePolicy()
			if err != nil {
				return nil, err
			}
			problems := CheckGraph(doc.Graph, pol)
			mp, err := x.materialProblems(doc.Material)
			if err != nil {
				return nil, err
			}
			if problems = append(problems, mp...); len(problems) > 0 {
				return nil, refuse(CodeBlocked, "plan cannot be mounted under policy %s: %v", pol.Version, problems)
			}
			if err := x.exec(`UPDATE tasks SET status='adopted', adopted_rev=?, adopted_digest=?, policy_version=?, approved_by=?, approval_assurance=?, updated_at=? WHERE id=?`,
				in.Rev, digest, pol.Version, p.ID, p.Assurance, x.ts(), in.Task); err != nil {
				return nil, err
			}
			if err := x.exec(`UPDATE graphs SET status = CASE WHEN rev=? THEN 'adopted' WHEN status='candidate' THEN 'superseded' ELSE status END WHERE task=?`, in.Rev, in.Task); err != nil {
				return nil, err
			}
			a := Adoption{Task: in.Task, Rev: in.Rev, Digest: digest, Policy: pol.Version, ApprovedBy: p.ID, Assurance: p.Assurance}
			return a, x.event("graph_adopted", in.Task, "", "", a)
		})
		out.Replayed = replayed
		return err
	})
	return out, err
}

type TaskCommand struct {
	CommandID string `json:"command_id"`
	Task      string `json:"task"`
}

type CommandAck struct {
	Task     string `json:"task"`
	Action   string `json:"action"`
	Replayed bool   `json:"replayed,omitempty"`
}

// Pause stops new node attempts and new remote dispatches. Calls already in
// flight are left running and shown as such: pausing is not cancellation.
func (s *Store) Pause(p Principal, in TaskCommand) (CommandAck, error) {
	return s.setPaused(p, in, true)
}

func (s *Store) Resume(p Principal, in TaskCommand) (CommandAck, error) {
	return s.setPaused(p, in, false)
}

func (s *Store) setPaused(p Principal, in TaskCommand, paused bool) (CommandAck, error) {
	kind := map[bool]string{true: "pause", false: "resume"}[paused]
	var out CommandAck
	err := s.tx(kind, p.ID, func(x *Tx) error {
		cur, err := x.current(p, KindUser, "operate")
		if err != nil {
			return err
		}
		p = cur
		replayed, err := x.command(p, in.CommandID, kind, in, &out, func() (any, error) {
			var status string
			if err := x.row(`SELECT status FROM tasks WHERE id=?`, in.Task).Scan(&status); err != nil {
				return nil, refuse(CodeNotFound, "task %s", in.Task)
			}
			if status != "adopted" {
				return nil, refuse(CodeConflict, "task %s is %s; only an adopted, undelivered task can be paused or resumed", in.Task, status)
			}
			if err := x.exec(`UPDATE tasks SET paused=?, updated_at=? WHERE id=?`, map[bool]int{true: 1, false: 0}[paused], x.ts(), in.Task); err != nil {
				return nil, err
			}
			ack := CommandAck{Task: in.Task, Action: kind}
			return ack, x.event("task_"+kind+"d", in.Task, "", "", ack)
		})
		out.Replayed = replayed
		return err
	})
	return out, err
}
