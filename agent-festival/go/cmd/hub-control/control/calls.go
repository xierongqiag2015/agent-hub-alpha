package control

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// DefaultPermitTTL bounds how long a dispatch permit is valid. The Agent Bus
// Gateway does not verify permits yet, so this window is also how long an
// executor that has just lost its lease could still send (disclosed residual
// risk; see docs/control-rights.md).
const DefaultPermitTTL = 15 * time.Second

// Call states of a remote request b.
const (
	CallBound      = "bound"      // identity and payload fixed, nothing sent
	CallDispatched = "dispatched" // send committed; no acknowledgement recorded
	CallAccepted   = "accepted"   // remote side acknowledged; remote_ref known
	CallUnknown    = "unknown"    // send may or may not have arrived
	CallSucceeded  = "succeeded"
	CallFailed     = "failed_confirmed"
)

type CallView struct {
	B          string `json:"b"`
	O          string `json:"o"`
	Name       string `json:"name"`
	Target     string `json:"target"`
	Kappa      string `json:"kappa"`
	State      string `json:"state"`
	RemoteRef  string `json:"remote_ref,omitempty"`
	Result     string `json:"result_artifact,omitempty"`
	Sends      int    `json:"sends"`
	Epoch      int    `json:"epoch"`
	UpdatedAt  string `json:"updated_at"`
	NextAction string `json:"next_action,omitempty"` // query | resend_same_key | manual
	Next       string `json:"next,omitempty"`        // the same, for people
	LateEvents int    `json:"late_events,omitempty"`
}

// CallID is the identity of one remote request inside a node execution. A
// retry of o reuses b; it never mints a new request for the same intent.
func CallID(o, name string) string { return o + "!" + name }

type BindInput struct {
	Task    string          `json:"task"`
	Token   string          `json:"token"`
	O       string          `json:"o"`
	A       int             `json:"a"`
	Name    string          `json:"name"`   // stable per node, e.g. "extract-indicators"
	Target  string          `json:"target"` // AgentCard id; must be allowed for the node's capability
	Payload json.RawMessage `json:"payload"`
}

// BindCall fixes b and its payload digest before anything is sent. Binding
// again with the same payload returns the existing record; a different
// payload for the same b is refused, so a retry cannot drift.
func (s *Store) BindCall(p Principal, in BindInput) (CallView, error) {
	var out CallView
	err := s.tx("bind_call", p.ID, func(x *Tx) error {
		t, err := x.fence(p, in.Task, in.Token)
		if err != nil {
			return err
		}
		var capability string
		if err := x.row(`SELECT capability FROM node_exec WHERE o=? AND task=?`, in.O, in.Task).Scan(&capability); err != nil {
			return refuse(CodeNotFound, "%s was never claimed", in.O)
		}
		var state string
		var epoch int
		if err := x.row(`SELECT state, epoch FROM attempts WHERE o=? AND a=?`, in.O, in.A).Scan(&state, &epoch); err != nil || state != "running" || epoch != t.Epoch {
			return refuse(CodeFenced, "attempt %d of %s is not the running attempt", in.A, in.O)
		}
		pol, err := x.policy(t.Policy)
		if err != nil {
			return err
		}
		allowed := false
		for _, tg := range pol.Capabilities[capability].Targets {
			allowed = allowed || tg == in.Target
		}
		if !allowed {
			return refuse(CodeForbidden, "target %q is not bound to capability %s under policy %s", in.Target, capability, t.Policy)
		}
		if in.Name == "" {
			return refuse(CodeInvalid, "call name is required")
		}
		var payload any
		if err := json.Unmarshal(in.Payload, &payload); err != nil {
			return refuse(CodeInvalid, "payload is not JSON: %v", err)
		}
		k, err := kappa("remote-call", map[string]any{"target": in.Target, "payload": payload})
		if err != nil {
			return err
		}
		b := CallID(in.O, in.Name)
		if c, err := x.call(b); err != nil {
			return err
		} else if c != nil {
			if c.Kappa != k || c.Target != in.Target {
				return refuse(CodeConflict, "call %s is already bound to a different payload; the original request stays", b)
			}
			out = *c
			return nil
		}
		if err := x.exec(`INSERT INTO calls(b,o,name,target,kappa,payload,state,epoch,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			b, in.O, in.Name, in.Target, k, string(in.Payload), CallBound, t.Epoch, x.ts()); err != nil {
			return err
		}
		c, err := x.call(b)
		if err != nil {
			return err
		}
		out = *c
		return x.event("call_bound", in.Task, in.O, b, map[string]any{"target": in.Target, "kappa": k})
	})
	return out, err
}

type CallRef struct {
	Task  string `json:"task"`
	Token string `json:"token"`
	B     string `json:"b"`
}

type Permit struct {
	B      string    `json:"b"`
	Kappa  string    `json:"kappa"`
	Epoch  int       `json:"epoch"`
	Send   int       `json:"send"`
	Expiry time.Time `json:"expires_at"`
	Token  string    `json:"permit"`
}

func (s *Store) permitTTL() time.Duration {
	if s.PermitTTL > 0 {
		return s.PermitTTL
	}
	return DefaultPermitTTL
}

// Dispatch is asked immediately before sending b. The first send is allowed
// from bound. A second send happens only if the capability contract declares
// same-key resend safe; otherwise an unknown outcome must be queried or handed
// to a person, never resent.
func (s *Store) Dispatch(p Principal, in CallRef) (Permit, error) {
	var out Permit
	err := s.tx("dispatch", p.ID, func(x *Tx) error {
		t, err := x.fence(p, in.Task, in.Token)
		if err != nil {
			return err
		}
		if t.Paused {
			return refuse(CodePaused, "task %s is paused: no new remote work is dispatched", t.ID)
		}
		c, err := x.call(in.B)
		if err != nil {
			return err
		}
		if c == nil || !strings.HasPrefix(c.O, t.ID+"/") {
			return refuse(CodeNotFound, "call %s", in.B)
		}
		var running int
		if err := x.row(`SELECT COUNT(*) FROM attempts WHERE o=? AND state='running' AND epoch=?`, c.O, t.Epoch).Scan(&running); err != nil {
			return err
		}
		if running == 0 {
			return refuse(CodeFenced, "no running attempt of %s in epoch %d", c.O, t.Epoch)
		}
		capability, pol, err := x.callCapability(c.O, t.Policy)
		if err != nil {
			return err
		}
		switch c.State {
		case CallBound:
		case CallDispatched, CallUnknown:
			if pol.Capabilities[capability].Resend != "same_key" {
				return refuse(CodeBlocked, "call %s may already have reached %s; its contract does not allow a resend — query it or hand it to a person", c.B, c.Target)
			}
		case CallAccepted:
			return refuse(CodeBlocked, "call %s was accepted as %s; query that remote task instead of resending", c.B, c.RemoteRef)
		default:
			return refuse(CodeConflict, "call %s is %s; history is read-only and is never re-sent", c.B, c.State)
		}
		sends := c.Sends + 1
		if err := x.exec(`UPDATE calls SET state=?, sends=?, epoch=?, updated_at=? WHERE b=?`, CallDispatched, sends, t.Epoch, x.ts(), c.B); err != nil {
			return err
		}
		exp := x.now.Add(s.permitTTL())
		body, _ := json.Marshal(map[string]any{"b": c.B, "kappa": c.Kappa, "epoch": t.Epoch, "send": sends, "exp": exp.Unix()})
		payload := base64.RawURLEncoding.EncodeToString(body)
		m := hmac.New(sha256.New, s.key)
		m.Write([]byte("permit\x00" + payload))
		out = Permit{B: c.B, Kappa: c.Kappa, Epoch: t.Epoch, Send: sends, Expiry: exp,
			Token: payload + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))}
		return x.event("call_dispatched", t.ID, c.O, c.B, map[string]any{"send": sends, "epoch": t.Epoch, "previous_state": c.State})
	})
	return out, err
}

// VerifyPermit lets an egress proxy check a permit against the exact payload
// it is about to send (P04 groundwork; the demo Gateway does not call it).
func (s *Store) VerifyPermit(token string, target string, payload json.RawMessage) (Permit, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Permit{}, refuse(CodeForbidden, "malformed permit")
	}
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte("permit\x00" + parts[0]))
	if !hmac.Equal([]byte(base64.RawURLEncoding.EncodeToString(m.Sum(nil))), []byte(parts[1])) {
		return Permit{}, refuse(CodeForbidden, "permit signature invalid")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var c struct {
		B     string `json:"b"`
		Kappa string `json:"kappa"`
		Epoch int    `json:"epoch"`
		Send  int    `json:"send"`
		Exp   int64  `json:"exp"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return Permit{}, refuse(CodeForbidden, "malformed permit")
	}
	if s.Now().Unix() > c.Exp {
		return Permit{}, refuse(CodeForbidden, "permit expired")
	}
	var body any
	if err := json.Unmarshal(payload, &body); err != nil {
		return Permit{}, refuse(CodeForbidden, "payload is not JSON")
	}
	k, err := kappa("remote-call", map[string]any{"target": target, "payload": body})
	if err != nil {
		return Permit{}, err
	}
	if k != c.Kappa {
		return Permit{}, refuse(CodeForbidden, "payload differs from the one the permit was issued for")
	}
	var out Permit
	err = s.read(func(x *Tx) error {
		cv, err := x.call(c.B)
		if err != nil || cv == nil {
			return refuse(CodeForbidden, "unknown call")
		}
		var epoch int
		if err := x.row(`SELECT t.lease_epoch FROM tasks t JOIN node_exec e ON e.task=t.id WHERE e.o=?`, cv.O).Scan(&epoch); err != nil {
			return err
		}
		if epoch != c.Epoch || cv.Sends != c.Send || cv.State != CallDispatched {
			return refuse(CodeForbidden, "permit is stale (epoch %d/%d, send %d/%d, state %s)", c.Epoch, epoch, c.Send, cv.Sends, cv.State)
		}
		out = Permit{B: c.B, Kappa: c.Kappa, Epoch: c.Epoch, Send: c.Send, Expiry: time.Unix(c.Exp, 0)}
		return nil
	})
	return out, err
}

type ObserveInput struct {
	Task      string          `json:"task"`
	Token     string          `json:"token"`
	B         string          `json:"b"`
	Kind      string          `json:"kind"` // accepted | succeeded | failed | timeout
	RemoteRef string          `json:"remote_ref,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Detail    string          `json:"detail,omitempty"`
}

type ObserveResult struct {
	Call    CallView `json:"call"`
	Applied bool     `json:"applied"`
	Note    string   `json:"note,omitempty"`
}

// Observe records what the remote side reported. Every observation is kept;
// only one that moves the call forward is applied. A late or duplicate reply
// never moves a finished call, and a reply delivered by an executor that has
// lost its lease is stored for reconciliation but not applied (A06).
func (s *Store) Observe(p Principal, in ObserveInput) (ObserveResult, error) {
	var out ObserveResult
	err := s.tx("observe", p.ID, func(x *Tx) error {
		if _, err := x.current(p, KindExecutor, ""); err != nil {
			return err
		}
		t, ferr := x.fence(p, in.Task, in.Token)
		if ferr != nil && CodeOf(ferr) != CodeFenced {
			return ferr
		}
		c, err := x.call(in.B)
		if err != nil {
			return err
		}
		if c == nil || !strings.HasPrefix(c.O, in.Task+"/") {
			return refuse(CodeNotFound, "call %s", in.B)
		}
		switch in.Kind {
		case "accepted", "succeeded", "failed", "timeout":
		default:
			return refuse(CodeInvalid, "observation kind must be accepted, succeeded, failed or timeout")
		}
		next, note := "", ""
		switch {
		case ferr != nil:
			note = "reported by an executor that lost its lease; kept for reconciliation, not applied"
		case c.State == CallSucceeded || c.State == CallFailed:
			note = "call already finished; late or duplicate reply recorded only"
		case c.State == CallBound:
			note = "call was never dispatched"
		case in.Kind == "accepted":
			if c.RemoteRef != "" && in.RemoteRef != c.RemoteRef {
				note = "remote reference differs from the one already recorded; needs a person"
			} else if c.State == CallAccepted {
				note = "duplicate acknowledgement"
			} else {
				next = CallAccepted
			}
		case in.Kind == "succeeded":
			next = CallSucceeded
		case in.Kind == "failed":
			next = CallFailed
		case in.Kind == "timeout":
			if c.State == CallDispatched {
				next = CallUnknown
			} else {
				note = "timeout while " + c.State + "; state kept"
			}
		}
		var seq int
		if err := x.row(`SELECT COALESCE(MAX(seq),0)+1 FROM call_observations WHERE b=?`, c.B).Scan(&seq); err != nil {
			return err
		}
		detail := in.Detail
		if len(in.Result) > 0 {
			detail = string(in.Result)
		}
		epoch := t.Epoch
		if err := x.exec(`INSERT INTO call_observations(b,seq,kind,remote_ref,detail,epoch,applied,note,at) VALUES(?,?,?,?,?,?,?,?,?)`,
			c.B, seq, in.Kind, in.RemoteRef, detail, epoch, boolInt(next != ""), note, x.ts()); err != nil {
			return err
		}
		if next != "" {
			ref := c.RemoteRef
			if in.RemoteRef != "" {
				ref = in.RemoteRef
			}
			var resultArt *string
			if next == CallSucceeded && len(in.Result) > 0 {
				d, canon, err := DigestJSON(in.Result)
				if err != nil {
					return err
				}
				if err := x.exec(`INSERT INTO artifacts(digest,media,body,created_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, d, "application/json", canon, x.ts()); err != nil {
					return err
				}
				resultArt = &d
			}
			if err := x.exec(`UPDATE calls SET state=?, remote_ref=NULLIF(?,''), result_artifact=COALESCE(?,result_artifact), updated_at=? WHERE b=?`,
				next, ref, resultArt, x.ts(), c.B); err != nil {
				return err
			}
		}
		if c, err = x.call(in.B); err != nil {
			return err
		}
		out = ObserveResult{Call: *c, Applied: next != "", Note: note}
		return x.event("call_observed", in.Task, c.O, c.B, map[string]any{"kind": in.Kind, "applied": next != "", "state": c.State, "note": note})
	})
	return out, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (x *Tx) call(b string) (*CallView, error) {
	var c CallView
	var ref, res *string
	err := x.row(`SELECT b,o,name,target,kappa,state,remote_ref,result_artifact,sends,epoch,updated_at FROM calls WHERE b=?`, b).
		Scan(&c.B, &c.O, &c.Name, &c.Target, &c.Kappa, &c.State, &ref, &res, &c.Sends, &c.Epoch, &c.UpdatedAt)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if ref != nil {
		c.RemoteRef = *ref
	}
	if res != nil {
		c.Result = *res
	}
	if err := x.row(`SELECT COUNT(*) FROM call_observations WHERE b=? AND applied=0`, b).Scan(&c.LateEvents); err != nil {
		return nil, err
	}
	return &c, nil
}

func (x *Tx) callCapability(o, policyVersion string) (string, Policy, error) {
	var capability string
	if err := x.row(`SELECT capability FROM node_exec WHERE o=?`, o).Scan(&capability); err != nil {
		return "", Policy{}, err
	}
	pol, err := x.policy(policyVersion)
	return capability, pol, err
}

func (x *Tx) markUnknownOnTakeover(task string, epoch int) error {
	rows, err := x.tx.Query(`SELECT b FROM calls WHERE state=? AND o IN (SELECT o FROM node_exec WHERE task=?)`, CallDispatched, task)
	if err != nil {
		return err
	}
	var bs []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			rows.Close()
			return err
		}
		bs = append(bs, b)
	}
	rows.Close()
	for _, b := range bs {
		if err := x.exec(`UPDATE calls SET state=?, updated_at=? WHERE b=?`, CallUnknown, x.ts(), b); err != nil {
			return err
		}
		if err := x.event("call_unknown", task, "", b, map[string]any{"reason": "lease taken over before the send was acknowledged", "epoch": epoch}); err != nil {
			return err
		}
	}
	return nil
}

// unresolvedCalls lists calls a recovering executor must settle, each with
// the only action its contract permits.
func (x *Tx) unresolvedCalls(task string) ([]CallView, error) {
	rows, err := x.tx.Query(`SELECT b FROM calls WHERE state IN ('dispatched','accepted','unknown') AND o IN (SELECT o FROM node_exec WHERE task=?) ORDER BY b`, task)
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
	t, err := x.task(task)
	if err != nil {
		return nil, err
	}
	var out []CallView
	for _, b := range bs {
		c, err := x.call(b)
		if err != nil {
			return nil, err
		}
		capability, pol, err := x.callCapability(c.O, t.Policy)
		if err != nil {
			return nil, err
		}
		cp := pol.Capabilities[capability]
		switch {
		case c.RemoteRef != "" && cp.Queryable:
			c.NextAction, c.Next = "query", "按远端任务号 "+c.RemoteRef+" 查询结果"
		case cp.Resend == "same_key":
			c.NextAction, c.Next = "resend_same_key", "契约声明同键幂等：可用同一调用身份和载荷重发"
		default:
			c.NextAction, c.Next = "manual", "人工核对：结果未知，且契约既不支持查询也不支持重发"
		}
		out = append(out, *c)
	}
	return out, nil
}
