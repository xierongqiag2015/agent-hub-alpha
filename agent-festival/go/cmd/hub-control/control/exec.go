package control

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// DefaultLeaseTTL is how long an executor owns a task without a heartbeat.
const DefaultLeaseTTL = 30 * time.Second

// Lease is the executor's ownership of one adopted task. Every write the
// executor makes carries the token; a takeover issues a new token and epoch,
// and the old token is refused everywhere from then on.
type Lease struct {
	Task       string     `json:"task"`
	Token      string     `json:"token"`
	Epoch      int        `json:"epoch"`
	LeaseUntil time.Time  `json:"lease_until"`
	Rev        int        `json:"rev"`
	Digest     string     `json:"digest"`
	Policy     string     `json:"policy_version"`
	Goal       string     `json:"goal"`
	Material   []Material `json:"material"`
	Graph      Graph      `json:"graph"`
	TakeOver   bool       `json:"take_over,omitempty"`
	// Unresolved lists remote calls whose outcome the previous owner did not
	// record. They are never resent automatically; see CallView.Next.
	Unresolved []CallView `json:"unresolved,omitempty"`
}

type LeaseInput struct {
	Task string `json:"task,omitempty"` // empty: any runnable task
}

func tokenHash(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func (s *Store) leaseTTL() time.Duration {
	if s.LeaseTTL > 0 {
		return s.LeaseTTL
	}
	return DefaultLeaseTTL
}

// Lease hands an adopted, undelivered task to an executor, taking it over if
// the previous lease expired.
func (s *Store) Lease(p Principal, in LeaseInput) (*Lease, error) {
	var out *Lease
	err := s.tx("lease", p.ID, func(x *Tx) error {
		if _, err := x.current(p, KindExecutor, ""); err != nil {
			return err
		}
		q := `SELECT id FROM tasks WHERE status='adopted' AND (lease_until IS NULL OR lease_until < ?)`
		args := []any{x.now.UnixMilli()}
		if in.Task != "" {
			q += ` AND id=?`
			args = append(args, in.Task)
		}
		var id string
		if err := x.row(q+` ORDER BY created_at, id LIMIT 1`, args...).Scan(&id); err != nil {
			if noRows(err) && in.Task != "" {
				var status string
				var until *int64
				if x.row(`SELECT status, lease_until FROM tasks WHERE id=?`, in.Task).Scan(&status, &until) != nil {
					return refuse(CodeNotFound, "task %s", in.Task)
				}
				if status != "adopted" {
					return refuse(CodeConflict, "task %s is %s and cannot be leased", in.Task, status)
				}
				return refuse(CodeInProgress, "task %s is leased by another executor", in.Task)
			}
			if noRows(err) {
				return nil
			}
			return err
		}
		var epoch int
		var prevOwner *string
		if err := x.row(`SELECT lease_epoch, lease_owner FROM tasks WHERE id=?`, id).Scan(&epoch, &prevOwner); err != nil {
			return err
		}
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		token := hex.EncodeToString(b)
		epoch++
		until := x.now.Add(s.leaseTTL())
		if err := x.exec(`UPDATE tasks SET lease_epoch=?, lease_token_hash=?, lease_owner=?, lease_until=?, updated_at=? WHERE id=?`,
			epoch, tokenHash(token), p.ID, until.UnixMilli(), x.ts(), id); err != nil {
			return err
		}
		takeOver := epoch > 1
		if takeOver {
			// The old owner may still be alive. Its running attempts are lost
			// (it can no longer commit them), and any call it had committed to
			// send without a reply becomes unknown: it may or may not have
			// reached the remote side.
			if err := x.exec(`UPDATE attempts SET state='lost', ended_at=?, detail='lease taken over' WHERE state='running' AND epoch<? AND o IN (SELECT o FROM node_exec WHERE task=?)`,
				x.ts(), epoch, id); err != nil {
				return err
			}
			if err := x.markUnknownOnTakeover(id, epoch); err != nil {
				return err
			}
		}
		t, err := x.task(id)
		if err != nil {
			return err
		}
		out = &Lease{Task: id, Token: token, Epoch: epoch, LeaseUntil: until, Rev: t.Rev, Digest: t.Digest, Policy: t.Policy,
			Goal: t.Goal, Material: t.Material, Graph: t.Graph, TakeOver: takeOver}
		if out.Unresolved, err = x.unresolvedCalls(id); err != nil {
			return err
		}
		return x.event("lease_granted", id, "", "", map[string]any{"epoch": epoch, "owner": p.ID, "take_over": takeOver, "previous_owner": prevOwner})
	})
	return out, err
}

type taskRow struct {
	ID       string
	Status   string
	Paused   bool
	Rev      int
	Digest   string
	Policy   string
	Goal     string
	Material []Material
	Graph    Graph
	Epoch    int
	Approver string
	Assure   string
	tokenH   string
}

func (x *Tx) task(id string) (taskRow, error) {
	var t taskRow
	var paused int
	var rev *int
	var digest, policy, approver, assure, tokenH *string
	var material string
	err := x.row(`SELECT id,status,paused,adopted_rev,adopted_digest,policy_version,goal,material,lease_epoch,approved_by,approval_assurance,lease_token_hash FROM tasks WHERE id=?`, id).
		Scan(&t.ID, &t.Status, &paused, &rev, &digest, &policy, &t.Goal, &material, &t.Epoch, &approver, &assure, &tokenH)
	if noRows(err) {
		return t, refuse(CodeNotFound, "task %s", id)
	}
	if err != nil {
		return t, err
	}
	t.Paused = paused == 1
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	t.Digest, t.Policy, t.Approver, t.Assure, t.tokenH = deref(digest), deref(policy), deref(approver), deref(assure), deref(tokenH)
	if err := json.Unmarshal([]byte(material), &t.Material); err != nil {
		return t, err
	}
	if rev != nil {
		t.Rev = *rev
		var doc string
		if err := x.row(`SELECT doc FROM graphs WHERE task=? AND rev=?`, id, t.Rev).Scan(&doc); err != nil {
			return t, err
		}
		var pd planDoc
		if err := json.Unmarshal([]byte(doc), &pd); err != nil {
			return t, err
		}
		t.Graph = pd.Graph
	}
	return t, nil
}

// fence admits a write only from the executor holding the task's current
// lease token. An executor that lost its lease is refused, whatever it sends.
func (x *Tx) fence(p Principal, task, token string) (taskRow, error) {
	if _, err := x.current(p, KindExecutor, ""); err != nil {
		return taskRow{}, err
	}
	t, err := x.task(task)
	if err != nil {
		return t, err
	}
	if token == "" || t.tokenH == "" || tokenHash(token) != t.tokenH {
		return t, refuse(CodeFenced, "this executor no longer holds task %s (current epoch %d)", task, t.Epoch)
	}
	if t.Status != "adopted" {
		return t, refuse(CodeConflict, "task %s is %s", task, t.Status)
	}
	return t, nil
}

type LeaseRef struct {
	Task  string `json:"task"`
	Token string `json:"token"`
}

func (s *Store) Heartbeat(p Principal, in LeaseRef) (time.Time, error) {
	var until time.Time
	err := s.tx("heartbeat", p.ID, func(x *Tx) error {
		if _, err := x.fence(p, in.Task, in.Token); err != nil {
			return err
		}
		until = x.now.Add(s.leaseTTL())
		return x.exec(`UPDATE tasks SET lease_until=? WHERE id=?`, until.UnixMilli(), in.Task)
	})
	return until, err
}

// Release gives the lease back (end of a run, or the executor stopping).
func (s *Store) Release(p Principal, in LeaseRef) error {
	return s.tx("release", p.ID, func(x *Tx) error {
		t, err := x.fence(p, in.Task, in.Token)
		if err != nil {
			return err
		}
		if err := x.exec(`UPDATE tasks SET lease_token_hash=NULL, lease_owner=NULL, lease_until=NULL WHERE id=?`, in.Task); err != nil {
			return err
		}
		return x.event("lease_released", in.Task, "", "", map[string]any{"epoch": t.Epoch})
	})
}

// ExecID is the logical node execution identity o = (task, adopted graph
// revision, node, loop iteration). Retries and recovery keep o; each try is an
// attempt a under it.
func ExecID(task string, rev int, node string, iter int) string {
	return fmt.Sprintf("%s/g%d/%s#%d", task, rev, node, iter)
}

type ClaimInput struct {
	Task  string `json:"task"`
	Token string `json:"token"`
	Node  string `json:"node"`
	Iter  int    `json:"iter"`
	// Material is the node's actual input: snapshot references, instructions,
	// upstream artifact digests. κ_in is computed here from it.
	Material json.RawMessage `json:"material"`
	// Sources are the knowledge sources the node consumes. They must be part
	// of the material the task was approved with, registered and not revoked;
	// they are part of κ_in and are what a later revocation is traced through.
	Sources []Material `json:"sources,omitempty"`
}

type ResultView struct {
	O           string          `json:"o"`
	Outcome     string          `json:"outcome"`
	Artifact    string          `json:"artifact"`
	KappaIn     string          `json:"kappa_in"`
	Epoch       int             `json:"epoch"`
	A           int             `json:"a"`
	Basis       []string        `json:"basis,omitempty"`
	ReusedFrom  string          `json:"reused_from,omitempty"`
	CommittedAt string          `json:"committed_at"`
	Body        json.RawMessage `json:"body,omitempty"`
}

type ClaimResult struct {
	O       string `json:"o"`
	Action  string `json:"action"` // run | reuse | in_progress
	A       int    `json:"a,omitempty"`
	Epoch   int    `json:"epoch"`
	KappaIn string `json:"kappa_in"`
	// Result is the formal result already recorded for o (action=reuse): the
	// node must return it without running its model or tools again.
	Result *ResultView `json:"result,omitempty"`
	// ReuseCandidate is another task's committed result for identical input
	// material. Adopting it still goes through Commit for this o, and this
	// task's own checks and approval still apply.
	ReuseCandidate *ResultView `json:"reuse_candidate,omitempty"`
	// Lookup says whether another task's result could be consumed and why
	// (not); it is recorded with the attempt and shown in the task view.
	Lookup *LookupView `json:"lookup,omitempty"`
}

const (
	OutcomeCompleted        = "completed"
	OutcomeBusinessRejected = "business_rejected"
)

// Claim is the entry every business node passes before doing any work.
func (s *Store) Claim(p Principal, in ClaimInput) (ClaimResult, error) {
	var out ClaimResult
	err := s.tx("claim", p.ID, func(x *Tx) error {
		t, err := x.fence(p, in.Task, in.Token)
		if err != nil {
			return err
		}
		n, ok := t.Graph.node(in.Node)
		if !ok {
			return refuse(CodeForbidden, "node %q is not in the adopted graph of %s", in.Node, in.Task)
		}
		maxIter := n.MaxIterations
		if maxIter == 0 {
			maxIter = 1
		}
		if in.Iter < 0 || in.Iter >= maxIter {
			return refuse(CodeForbidden, "node %s iteration %d is outside its adopted bound %d", in.Node, in.Iter, maxIter)
		}
		pol, err := x.policy(t.Policy)
		if err != nil {
			return err
		}
		var material any
		if len(in.Material) > 0 {
			if err := json.Unmarshal(in.Material, &material); err != nil {
				return refuse(CodeInvalid, "material is not JSON: %v", err)
			}
		}
		var refs []string
		for _, m := range in.Sources {
			refs = append(refs, SourceRef(m))
		}
		sort.Strings(refs)
		kin, err := kappa("node-input", map[string]any{"capability": n.Capability, "capability_def": pol.Capabilities[n.Capability], "material": material, "sources": refs})
		if err != nil {
			return err
		}
		o := ExecID(t.ID, t.Rev, n.ID, in.Iter)
		out = ClaimResult{O: o, Epoch: t.Epoch, KappaIn: kin}

		if r, err := x.result(o, true); err != nil {
			return err
		} else if r != nil {
			if r.KappaIn != kin {
				return refuse(CodeConflict, "%s already has a formal result for different input material; it is not recomputed or replaced", o)
			}
			out.Action, out.Result = "reuse", r
			if err := x.exec(`UPDATE node_exec SET reused=reused+1 WHERE o=?`, o); err != nil {
				return err
			}
			return x.event("result_reused", t.ID, o, "", map[string]any{"artifact": r.Artifact, "epoch": t.Epoch})
		}
		if t.Paused {
			return refuse(CodePaused, "task %s is paused: no new node attempts start", t.ID)
		}
		// reading an existing result above is history; consuming sources for a
		// new attempt is checked against what the task may use now
		if _, err := x.checkSources(t, in.Sources, pol.Capabilities[n.Capability].Reusable); err != nil {
			return err
		}
		for _, d := range n.DependsOn {
			var cnt int
			if err := x.row(`SELECT COUNT(*) FROM results r JOIN node_exec e ON e.o=r.o WHERE e.task=? AND e.rev=? AND e.node=?`, t.ID, t.Rev, d).Scan(&cnt); err != nil {
				return err
			}
			if cnt == 0 {
				return refuse(CodeNotReady, "node %s depends on %s, which has no formal result yet", n.ID, d)
			}
		}
		var oldK string
		switch err := x.row(`SELECT kappa_in FROM node_exec WHERE o=?`, o).Scan(&oldK); {
		case noRows(err):
			if err := x.exec(`INSERT INTO node_exec(o,task,rev,node,iter,capability,kappa_in) VALUES(?,?,?,?,?,?,?)`,
				o, t.ID, t.Rev, n.ID, in.Iter, n.Capability, kin); err != nil {
				return err
			}
			for _, r := range refs {
				if err := x.exec(`INSERT INTO node_sources(o,source) VALUES(?,?)`, o, r); err != nil {
					return err
				}
			}
		case err != nil:
			return err
		case oldK != kin:
			return refuse(CodeConflict, "%s was first claimed with different input material; the binding is not changed by a retry", o)
		}
		var runningA int
		switch err := x.row(`SELECT a FROM attempts WHERE o=? AND state='running' AND epoch=?`, o, t.Epoch).Scan(&runningA); {
		case err == nil:
			out.Action, out.A = "in_progress", runningA
			return nil
		case !noRows(err):
			return err
		}
		if err := x.exec(`UPDATE node_exec SET attempts=attempts+1 WHERE o=?`, o); err != nil {
			return err
		}
		if err := x.row(`SELECT attempts FROM node_exec WHERE o=?`, o).Scan(&out.A); err != nil {
			return err
		}
		if err := x.exec(`INSERT INTO attempts(o,a,epoch,owner,state,started_at) VALUES(?,?,?,?,'running',?)`, o, out.A, t.Epoch, p.ID, x.ts()); err != nil {
			return err
		}
		out.Action = "run"
		if out.ReuseCandidate, err = x.lookup(t, o, out.A, n.Capability, kin, refs, pol); err != nil {
			return err
		}
		var lk LookupView
		var src *string
		if err := x.row(`SELECT outcome, reason, source_o, at FROM knowledge_lookups WHERE o=? AND a=?`, o, out.A).Scan(&lk.Outcome, &lk.Reason, &src, &lk.At); err != nil {
			return err
		}
		if src != nil {
			lk.SourceO = *src
		}
		out.Lookup = &lk
		return x.event("attempt_started", t.ID, o, "", map[string]any{"a": out.A, "epoch": t.Epoch, "kappa_in": kin, "reuse_candidate": out.ReuseCandidate != nil})
	})
	return out, err
}

func (x *Tx) result(o string, withBody bool) (*ResultView, error) {
	var r ResultView
	var basis string
	var reused *string
	err := x.row(`SELECT o,outcome,artifact,kappa_in,epoch,a,basis,reused_from,committed_at FROM results WHERE o=?`, o).
		Scan(&r.O, &r.Outcome, &r.Artifact, &r.KappaIn, &r.Epoch, &r.A, &basis, &reused, &r.CommittedAt)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(basis), &r.Basis)
	if reused != nil {
		r.ReusedFrom = *reused
	}
	if withBody {
		var body []byte
		if err := x.row(`SELECT body FROM artifacts WHERE digest=?`, r.Artifact).Scan(&body); err != nil {
			return nil, err
		}
		r.Body = body
	}
	return &r, nil
}

type CommitInput struct {
	Task    string          `json:"task"`
	Token   string          `json:"token"`
	O       string          `json:"o"`
	A       int             `json:"a"`
	Outcome string          `json:"outcome"`
	Body    json.RawMessage `json:"body"` // the artifact (JSON); its digest is computed here
	// Basis is required for business_rejected: the check ids ("check:N") or
	// judgment artifact digests the rejection rests on. A technical error is
	// reported with Fail, never as a business rejection.
	Basis      []string `json:"basis,omitempty"`
	ReusedFrom string   `json:"reused_from,omitempty"`
}

type CommitResult struct {
	Result    ResultView `json:"result"`
	Duplicate bool       `json:"duplicate,omitempty"`
}

// Commit records the one formal result of o. A repeat of the same result is
// acknowledged even from a fenced executor (it already happened); a different
// result for the same o is refused, so a late writer can never replace it.
func (s *Store) Commit(p Principal, in CommitInput) (CommitResult, error) {
	var out CommitResult
	err := s.tx("commit", p.ID, func(x *Tx) error {
		if _, err := x.current(p, KindExecutor, ""); err != nil {
			return err
		}
		if in.Outcome != OutcomeCompleted && in.Outcome != OutcomeBusinessRejected {
			return refuse(CodeInvalid, "outcome must be completed or business_rejected; report technical failures with fail")
		}
		digest, canon, err := DigestJSON(in.Body)
		if err != nil {
			return err
		}
		if r, err := x.result(in.O, false); err != nil {
			return err
		} else if r != nil {
			if r.Artifact == digest && r.Outcome == in.Outcome {
				out = CommitResult{Result: *r, Duplicate: true}
				return x.event("commit_duplicate", in.Task, in.O, "", map[string]any{"a": in.A, "artifact": digest})
			}
			return refuse(CodeConflict, "%s already has a formal result (%s); a different result is not accepted", in.O, r.Artifact)
		}
		t, err := x.fence(p, in.Task, in.Token)
		if err != nil {
			return err
		}
		var kin, capability string
		if err := x.row(`SELECT kappa_in, capability FROM node_exec WHERE o=? AND task=?`, in.O, in.Task).Scan(&kin, &capability); err != nil {
			return refuse(CodeNotFound, "%s was never claimed", in.O)
		}
		var state string
		var epoch int
		if err := x.row(`SELECT state, epoch FROM attempts WHERE o=? AND a=?`, in.O, in.A).Scan(&state, &epoch); err != nil {
			return refuse(CodeNotFound, "%s has no attempt %d", in.O, in.A)
		}
		if state != "running" || epoch != t.Epoch {
			return refuse(CodeFenced, "attempt %d of %s is %s (epoch %d, current %d)", in.A, in.O, state, epoch, t.Epoch)
		}
		var open int
		if err := x.row(`SELECT COUNT(*) FROM calls WHERE o=? AND state IN ('dispatched','accepted','unknown')`, in.O).Scan(&open); err != nil {
			return err
		}
		if open > 0 {
			return refuse(CodeBlocked, "%s has %d remote call(s) without a confirmed outcome; resolve them before committing", in.O, open)
		}
		if in.Outcome == OutcomeBusinessRejected {
			if err := x.checkBasis(in.Basis); err != nil {
				return err
			}
		}
		var reused *string
		if in.ReusedFrom != "" {
			pol, err := x.policy(t.Policy)
			if err != nil {
				return err
			}
			if !pol.Capabilities[capability].Reusable {
				return refuse(CodeForbidden, "capability %s does not allow cross-task reuse under policy %s", capability, t.Policy)
			}
			src, err := x.result(in.ReusedFrom, false)
			if err != nil {
				return err
			}
			var srcK, srcTask string
			if src != nil {
				_ = x.row(`SELECT kappa_in, task FROM node_exec WHERE o=?`, in.ReusedFrom).Scan(&srcK, &srcTask)
			}
			if src == nil || src.Outcome != OutcomeCompleted || srcK != kin || srcTask == in.Task || src.Artifact != digest {
				return refuse(CodeConflict, "%s is not a reusable result for this input material", in.ReusedFrom)
			}
			// the knowledge may have been revoked between lookup and commit
			if why, err := x.basisRevoked(in.ReusedFrom); err != nil {
				return err
			} else if why != "" {
				return refuse(CodeConflict, "%s can no longer be reused: %s", in.ReusedFrom, why)
			}
			if pol.ReuseMaxAgeHrs > 0 {
				at, _ := time.Parse(time.RFC3339Nano, src.CommittedAt)
				if x.now.Sub(at) > time.Duration(pol.ReuseMaxAgeHrs)*time.Hour {
					return refuse(CodeConflict, "%s is older than the policy's reuse window", in.ReusedFrom)
				}
			}
			reused = &in.ReusedFrom
		}
		if err := x.exec(`INSERT INTO artifacts(digest,media,body,created_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, digest, "application/json", canon, x.ts()); err != nil {
			return err
		}
		if err := x.exec(`INSERT INTO results(o,outcome,artifact,kappa_in,epoch,a,basis,reused_from,committed_by,committed_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			in.O, in.Outcome, digest, kin, t.Epoch, in.A, mustJSON(in.Basis), reused, p.ID, x.ts()); err != nil {
			return err
		}
		if err := x.exec(`UPDATE attempts SET state='committed', ended_at=? WHERE o=? AND a=?`, x.ts(), in.O, in.A); err != nil {
			return err
		}
		r, err := x.result(in.O, false)
		if err != nil {
			return err
		}
		out = CommitResult{Result: *r}
		return x.event("result_committed", in.Task, in.O, "", map[string]any{"a": in.A, "outcome": in.Outcome, "artifact": digest, "reused_from": in.ReusedFrom})
	})
	return out, err
}

func (x *Tx) checkBasis(basis []string) error {
	if len(basis) == 0 {
		return refuse(CodeInvalid, "a business rejection needs a basis (failed check or judgment artifact); use fail for technical errors")
	}
	for _, b := range basis {
		var n int
		var id int
		if _, err := fmt.Sscanf(b, "check:%d", &id); err == nil {
			if err := x.row(`SELECT COUNT(*) FROM checks WHERE id=? AND verdict='fail'`, id).Scan(&n); err != nil {
				return err
			}
		} else if err := x.row(`SELECT COUNT(*) FROM artifacts WHERE digest=?`, b).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return refuse(CodeInvalid, "basis %q is not a recorded failed check or artifact", b)
		}
	}
	return nil
}

type FailInput struct {
	Task   string `json:"task"`
	Token  string `json:"token"`
	O      string `json:"o"`
	A      int    `json:"a"`
	Detail string `json:"detail"`
}

// Fail ends an attempt with a technical failure (timeout, download error,
// crash in a tool). It is not a business result: o stays open for a new
// attempt and nothing is reused from it.
func (s *Store) Fail(p Principal, in FailInput) error {
	return s.tx("fail", p.ID, func(x *Tx) error {
		t, err := x.fence(p, in.Task, in.Token)
		if err != nil {
			return err
		}
		var state string
		var epoch int
		if err := x.row(`SELECT state, epoch FROM attempts WHERE o=? AND a=?`, in.O, in.A).Scan(&state, &epoch); err != nil {
			return refuse(CodeNotFound, "%s has no attempt %d", in.O, in.A)
		}
		if state != "running" || epoch != t.Epoch {
			return refuse(CodeFenced, "attempt %d of %s is %s", in.A, in.O, state)
		}
		if err := x.exec(`UPDATE attempts SET state='technical_failure', detail=?, ended_at=? WHERE o=? AND a=?`, in.Detail, x.ts(), in.O, in.A); err != nil {
			return err
		}
		return x.event("attempt_failed", in.Task, in.O, "", map[string]any{"a": in.A, "detail": in.Detail})
	})
}

type ProgressInput struct {
	Task   string `json:"task"`
	Token  string `json:"token"`
	O      string `json:"o"`
	A      int    `json:"a"`
	Seq    int    `json:"seq"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Progress records what a node is doing for the live view. Each (o, a, seq)
// is stored once; a duplicate or late event is ignored, so the view never
// moves backwards or double-counts (A12).
func (s *Store) Progress(p Principal, in ProgressInput) (bool, error) {
	applied := false
	err := s.tx("progress", p.ID, func(x *Tx) error {
		if _, err := x.fence(p, in.Task, in.Token); err != nil {
			return err
		}
		var n int
		if err := x.row(`SELECT COUNT(*) FROM attempts WHERE o=? AND a=?`, in.O, in.A).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return refuse(CodeNotFound, "%s has no attempt %d", in.O, in.A)
		}
		res, err := x.tx.Exec(`INSERT INTO progress(o,a,seq,kind,detail,at) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, in.O, in.A, in.Seq, in.Kind, in.Detail, x.ts())
		if err != nil {
			return err
		}
		k, _ := res.RowsAffected()
		applied = k == 1
		return nil
	})
	return applied, err
}
