package control

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Knowledge consumption (the K path of #4, F-02C in the #14 review):
//
//	Tnew ≠ Told
//	ReuseCandidate = Kθ(input, version, source, freshness, permission, applicability)
//	AdoptForNewTask(ReuseCandidate) still passes this task's own checks and approval
//
// How each term is enforced here:
//   - input, version, applicability: the candidate's κ_in must equal this
//     node's κ_in, which covers the capability definition, the declared
//     sources (snapshot id + SHA-256) and the instructions/template. This is
//     exact equivalence, deliberately conservative; semantic applicability
//     belongs to the capability knowledge service (#6).
//   - source: every node declares the sources it consumes; a reused result
//     records reused_from, and lookups are kept with their outcome.
//   - freshness: the policy's reuse window, plus revocation of a source or of
//     a result, which stops all later consumption without rewriting history.
//   - permission: a node may only declare sources that are in the material
//     list its task was approved with, and that are registered and not revoked.
//   - approval: never inherited; delivery is approved per task.

// SourceRef names a consumed knowledge source.
func SourceRef(m Material) string { return "material:" + m.ID + "@" + m.SHA256 }

// ResultRef names a committed result as a knowledge item.
func ResultRef(o string) string { return "result:" + o }

// Lookup outcomes.
const (
	LookupHit           = "hit"
	LookupMiss          = "miss"
	LookupNotApplicable = "not_applicable"
)

type LookupView struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
	SourceO string `json:"source_o,omitempty"`
	At      string `json:"at"`
}

// checkSources validates the sources a node declares and returns their refs.
func (x *Tx) checkSources(t taskRow, srcs []Material, reusable bool) ([]string, error) {
	if reusable && len(srcs) == 0 {
		return nil, refuse(CodeInvalid, "this capability's results can be reused across tasks, so the node must declare the knowledge sources it consumes")
	}
	approved := map[string]bool{}
	for _, m := range t.Material {
		approved[SourceRef(m)] = true
	}
	var refs []string
	for _, m := range srcs {
		ref := SourceRef(m)
		if !approved[ref] {
			return nil, refuse(CodeForbidden, "source %s@%.12s is not in the material task %s was approved with", m.ID, m.SHA256, t.ID)
		}
		if why, err := x.revoked(ref); err != nil {
			return nil, err
		} else if why != "" {
			return nil, refuse(CodeForbidden, "source %s@%.12s was revoked: %s", m.ID, m.SHA256, why)
		}
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs, nil
}

// revoked returns the revocation reason of ref, or "".
func (x *Tx) revoked(ref string) (string, error) {
	var why string
	err := x.row(`SELECT reason FROM knowledge_revocations WHERE ref=?`, ref).Scan(&why)
	if noRows(err) {
		return "", nil
	}
	return why, err
}

// origin follows reused_from to the result that was actually computed.
func (x *Tx) origin(o string) (string, error) {
	for i := 0; i < 16; i++ {
		var from *string
		if err := x.row(`SELECT reused_from FROM results WHERE o=?`, o).Scan(&from); err != nil {
			return o, err
		}
		if from == nil || *from == "" {
			return o, nil
		}
		o = *from
	}
	return o, nil
}

// basisRevoked reports why the result of o can no longer serve as knowledge:
// the result itself, a result it was reused from, or one of their sources
// was revoked. "" means it is still usable.
func (x *Tx) basisRevoked(o string) (string, error) {
	for i := 0; i < 16 && o != ""; i++ {
		if why, err := x.revoked(ResultRef(o)); err != nil || why != "" {
			if why != "" {
				why = fmt.Sprintf("结果 %s 已撤回：%s", o, why)
			}
			return why, err
		}
		rows, err := x.tx.Query(`SELECT source FROM node_sources WHERE o=?`, o)
		if err != nil {
			return "", err
		}
		var refs []string
		for rows.Next() {
			var r string
			if err := rows.Scan(&r); err != nil {
				rows.Close()
				return "", err
			}
			refs = append(refs, r)
		}
		rows.Close()
		for _, r := range refs {
			if why, err := x.revoked(r); err != nil {
				return "", err
			} else if why != "" {
				return fmt.Sprintf("来源 %s 已撤回：%s", shortRef(r), why), nil
			}
		}
		var from *string
		if err := x.row(`SELECT reused_from FROM results WHERE o=?`, o).Scan(&from); err != nil && !noRows(err) {
			return "", err
		}
		if from == nil {
			return "", nil
		}
		o = *from
	}
	return "", nil
}

// lookup searches other tasks' results for one this node may consume, and
// records the outcome with a reason the user can read.
func (x *Tx) lookup(t taskRow, o string, a int, capability, kin string, refs []string, pol Policy) (*ResultView, error) {
	record := func(outcome, reason, src string) error {
		var s *string
		if src != "" {
			s = &src
		}
		if err := x.exec(`INSERT OR REPLACE INTO knowledge_lookups(o,a,outcome,reason,source_o,at) VALUES(?,?,?,?,?,?)`, o, a, outcome, reason, s, x.ts()); err != nil {
			return err
		}
		return x.event("knowledge_lookup", t.ID, o, "", map[string]any{"a": a, "outcome": outcome, "reason": reason, "source_o": src})
	}
	if !pol.Capabilities[capability].Reusable {
		return nil, record(LookupNotApplicable, fmt.Sprintf("能力 %s 未声明可跨任务复用，按首次计算处理", capability), "")
	}
	rows, err := x.tx.Query(`SELECT r.o, r.committed_at FROM results r JOIN node_exec e ON e.o=r.o
		WHERE e.kappa_in=? AND e.task<>? AND r.outcome='completed' ORDER BY r.committed_at, r.o`, kin, t.ID)
	if err != nil {
		return nil, err
	}
	type cand struct{ o, at string }
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.o, &c.at); err != nil {
			rows.Close()
			return nil, err
		}
		cands = append(cands, c)
	}
	rows.Close()
	var stale, revokedWhy string
	for _, c := range cands {
		if pol.ReuseMaxAgeHrs > 0 {
			at, _ := time.Parse(time.RFC3339Nano, c.at)
			if x.now.Sub(at) > time.Duration(pol.ReuseMaxAgeHrs)*time.Hour {
				stale = c.o
				continue
			}
		}
		why, err := x.basisRevoked(c.o)
		if err != nil {
			return nil, err
		}
		if why != "" {
			revokedWhy = why
			continue
		}
		r, err := x.result(c.o, true)
		if err != nil {
			return nil, err
		}
		return r, record(LookupHit, "输入、来源版本与能力定义完全一致，可复用", c.o)
	}
	switch {
	case revokedWhy != "":
		return nil, record(LookupMiss, "已有结果不可用："+revokedWhy, "")
	case stale != "":
		return nil, record(LookupMiss, fmt.Sprintf("已有结果 %s 超出保鲜期（%d 小时）", stale, pol.ReuseMaxAgeHrs), "")
	}
	// explain why nothing matched
	if len(refs) > 0 {
		var n int
		ids := make([]any, 0, len(refs))
		ph := make([]string, 0, len(refs))
		for _, r := range refs {
			id := strings.SplitN(strings.TrimPrefix(r, "material:"), "@", 2)[0]
			ids = append(ids, "material:"+id+"@%")
			ph = append(ph, "s.source LIKE ?")
		}
		q := `SELECT COUNT(DISTINCT r.o) FROM results r JOIN node_exec e ON e.o=r.o JOIN node_sources s ON s.o=r.o
			WHERE e.capability=? AND e.task<>? AND r.outcome='completed' AND (` + strings.Join(ph, " OR ") + `)`
		args := append([]any{capability, t.ID}, ids...)
		if err := x.row(q, args...).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			var same int
			eq := make([]any, 0, len(refs))
			ph2 := make([]string, 0, len(refs))
			for _, r := range refs {
				eq = append(eq, r)
				ph2 = append(ph2, "?")
			}
			q2 := `SELECT COUNT(DISTINCT r.o) FROM results r JOIN node_exec e ON e.o=r.o JOIN node_sources s ON s.o=r.o
				WHERE e.capability=? AND e.task<>? AND r.outcome='completed' AND s.source IN (` + strings.Join(ph2, ",") + `)`
			if err := x.row(q2, append([]any{capability, t.ID}, eq...)...).Scan(&same); err != nil {
				return nil, err
			}
			if same == 0 {
				return nil, record(LookupMiss, "已有结果基于同一材料的其他版本，版本不同不复用", "")
			}
			return nil, record(LookupMiss, "已有结果使用了相同来源，但指令、模板或能力定义不同，不适用", "")
		}
	}
	return nil, record(LookupMiss, "没有其他任务消费过这些来源", "")
}

// savedCalls is how many remote calls the computed origin of o made, i.e.
// what this reuse avoided.
func (x *Tx) savedCalls(o string) (int, string, error) {
	org, err := x.origin(o)
	if err != nil || org == o {
		return 0, org, err
	}
	var n int
	err = x.row(`SELECT COUNT(*) FROM calls WHERE o=?`, org).Scan(&n)
	return n, org, err
}

// shortRef renders material:<id>@<sha256> as id@<first 10 hex>….
func shortRef(r string) string {
	idsha := strings.SplitN(strings.TrimPrefix(r, "material:"), "@", 2)
	if len(idsha) == 2 && len(idsha[1]) > 10 {
		return idsha[0] + "@" + idsha[1][:10] + "…"
	}
	return strings.TrimPrefix(r, "material:")
}

// taskBasisRevoked lists revoked knowledge the task's results rest on, one
// line per cause with the nodes it affects.
func (x *Tx) taskBasisRevoked(t taskRow) ([]string, error) {
	rows, err := x.tx.Query(`SELECT r.o FROM results r JOIN node_exec e ON e.o=r.o WHERE e.task=? AND e.rev=? ORDER BY r.o`, t.ID, t.Rev)
	if err != nil {
		return nil, err
	}
	var os []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			rows.Close()
			return nil, err
		}
		os = append(os, o)
	}
	rows.Close()
	var causes []string
	nodes := map[string][]string{}
	for _, o := range os {
		why, err := x.basisRevoked(o)
		if err != nil {
			return nil, err
		}
		if why == "" {
			continue
		}
		if _, seen := nodes[why]; !seen {
			causes = append(causes, why)
		}
		node := o
		if i := strings.LastIndex(o, "/"); i >= 0 {
			node = o[i+1:]
		}
		nodes[why] = append(nodes[why], node)
	}
	var out []string
	for _, c := range causes {
		out = append(out, fmt.Sprintf("%s（涉及 %d 个节点：%s）", c, len(nodes[c]), strings.Join(nodes[c], "、")))
	}
	return out, nil
}

type RevokeInput struct {
	CommandID string `json:"command_id"`
	Ref       string `json:"ref"` // material:<id>@<sha256> or result:<o>
	Reason    string `json:"reason"`
}

type Revocation struct {
	Ref       string `json:"ref"`
	Reason    string `json:"reason"`
	RevokedBy string `json:"revoked_by"`
	At        string `json:"at"`
	Replayed  bool   `json:"replayed,omitempty"`
}

// Revoke withdraws a knowledge source or a result from further consumption.
// Nothing already recorded is changed: results stay, delivered reports stay
// delivered. Undelivered tasks whose results rest on it can no longer be
// delivered, and no new task or node can consume it.
func (s *Store) Revoke(p Principal, in RevokeInput) (Revocation, error) {
	var out Revocation
	err := s.tx("revoke", p.ID, func(x *Tx) error {
		cur, err := x.principal(p.ID)
		if err != nil {
			return err
		}
		if cur.Disabled || !(cur.Kind == KindAdmin || cur.Kind == KindUser && cur.Has("curate")) {
			return refuse(CodeForbidden, "%q cannot revoke knowledge (needs the curate role)", p.ID)
		}
		replayed, err := x.command(cur, in.CommandID, "revoke", in, &out, func() (any, error) {
			if strings.TrimSpace(in.Reason) == "" {
				return nil, refuse(CodeInvalid, "a revocation needs a reason")
			}
			var n int
			switch {
			case strings.HasPrefix(in.Ref, "material:"):
				idsha := strings.SplitN(strings.TrimPrefix(in.Ref, "material:"), "@", 2)
				if len(idsha) == 2 {
					if err := x.row(`SELECT COUNT(*) FROM materials WHERE id=? AND sha256=?`, idsha[0], idsha[1]).Scan(&n); err != nil {
						return nil, err
					}
				}
			case strings.HasPrefix(in.Ref, "result:"):
				if err := x.row(`SELECT COUNT(*) FROM results WHERE o=?`, strings.TrimPrefix(in.Ref, "result:")).Scan(&n); err != nil {
					return nil, err
				}
			}
			if n == 0 {
				return nil, refuse(CodeNotFound, "%q is neither a registered material nor a committed result", in.Ref)
			}
			if why, err := x.revoked(in.Ref); err != nil {
				return nil, err
			} else if why != "" {
				return nil, refuse(CodeConflict, "%s is already revoked: %s", in.Ref, why)
			}
			r := Revocation{Ref: in.Ref, Reason: in.Reason, RevokedBy: cur.ID, At: x.ts()}
			if err := x.exec(`INSERT INTO knowledge_revocations(ref,reason,revoked_by,at) VALUES(?,?,?,?)`, r.Ref, r.Reason, r.RevokedBy, r.At); err != nil {
				return nil, err
			}
			return r, x.event("knowledge_revoked", "", "", "", r)
		})
		out.Replayed = replayed
		return err
	})
	return out, err
}

// KnowledgeView is the inventory the console's knowledge page shows.
type KnowledgeView struct {
	Materials []MaterialView `json:"materials"`
	Items     []ItemView     `json:"items"`
	Hits      int            `json:"hits"`
	Misses    int            `json:"misses"`
	Saved     int            `json:"saved_calls"`
}

type MaterialView struct {
	Material
	Ref       string `json:"ref"`
	Revoked   string `json:"revoked,omitempty"`
	Consumers int    `json:"consumer_nodes"`
}

type ItemView struct {
	O          string `json:"o"`
	Task       string `json:"task"`
	Capability string `json:"capability"`
	Artifact   string `json:"artifact"`
	Sources    string `json:"sources"`
	ReusedBy   int    `json:"reused_by"`
	Saved      int    `json:"saved_calls"`
	Revoked    string `json:"revoked,omitempty"`
	At         string `json:"committed_at"`
}

// Knowledge lists registered sources and the computed results other tasks
// have consumed (or may consume), with revocation state.
func (s *Store) Knowledge() (*KnowledgeView, error) {
	v := &KnowledgeView{Materials: []MaterialView{}, Items: []ItemView{}}
	err := s.read(func(x *Tx) error {
		rows, err := x.tx.Query(`SELECT id, sha256, title FROM materials ORDER BY id, registered_at`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var m MaterialView
			if err := rows.Scan(&m.ID, &m.SHA256, &m.Title); err != nil {
				rows.Close()
				return err
			}
			m.Ref = SourceRef(m.Material)
			v.Materials = append(v.Materials, m)
		}
		rows.Close()
		for i := range v.Materials {
			m := &v.Materials[i]
			var err error
			if m.Revoked, err = x.revoked(m.Ref); err != nil {
				return err
			}
			if err := x.row(`SELECT COUNT(*) FROM node_sources WHERE source=?`, m.Ref).Scan(&m.Consumers); err != nil {
				return err
			}
		}
		rows, err = x.tx.Query(`SELECT r.o, e.task, e.capability, r.artifact, r.committed_at FROM results r JOIN node_exec e ON e.o=r.o
			WHERE (r.reused_from IS NULL OR r.reused_from='') AND EXISTS (SELECT 1 FROM node_sources s WHERE s.o=r.o) ORDER BY r.committed_at`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var it ItemView
			if err := rows.Scan(&it.O, &it.Task, &it.Capability, &it.Artifact, &it.At); err != nil {
				rows.Close()
				return err
			}
			v.Items = append(v.Items, it)
		}
		rows.Close()
		// only results of capabilities their task's policy declares reusable are knowledge
		kept := v.Items[:0]
		for _, it := range v.Items {
			t, err := x.task(it.Task)
			if err != nil {
				return err
			}
			pol, err := x.policy(t.Policy)
			if err != nil {
				return err
			}
			if pol.Capabilities[it.Capability].Reusable {
				kept = append(kept, it)
			}
		}
		v.Items = kept
		for i := range v.Items {
			it := &v.Items[i]
			var srcs string
			if err := x.row(`SELECT COALESCE(GROUP_CONCAT(REPLACE(source,'material:',''), ' '),'') FROM node_sources WHERE o=?`, it.O).Scan(&srcs); err != nil {
				return err
			}
			it.Sources = srcs
			if err := x.row(`SELECT COUNT(*) FROM results WHERE reused_from=?`, it.O).Scan(&it.ReusedBy); err != nil {
				return err
			}
			var calls int
			if err := x.row(`SELECT COUNT(*) FROM calls WHERE o=?`, it.O).Scan(&calls); err != nil {
				return err
			}
			it.Saved = calls * it.ReusedBy
			var err error
			if it.Revoked, err = x.basisRevoked(it.O); err != nil {
				return err
			}
		}
		if err := x.row(`SELECT COUNT(*) FROM knowledge_lookups WHERE outcome='hit'`).Scan(&v.Hits); err != nil {
			return err
		}
		if err := x.row(`SELECT COUNT(*) FROM knowledge_lookups WHERE outcome='miss'`).Scan(&v.Misses); err != nil {
			return err
		}
		for _, it := range v.Items {
			v.Saved += it.Saved
		}
		return nil
	})
	return v, err
}
