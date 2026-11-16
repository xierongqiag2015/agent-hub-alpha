package control

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Policy is the published management-plane configuration a task binds when it
// is adopted. A later policy applies to later tasks only.
type Policy struct {
	Version           string                `json:"version"`
	Capabilities      map[string]Capability `json:"capabilities"`
	MaxNodes          int                   `json:"max_nodes"`
	MaxLoopIterations int                   `json:"max_loop_iterations"`
	// RequiredChecks must all pass on the exact report digest before it can
	// be delivered. An empty list means "not configured": delivery is refused
	// rather than defaulting to open.
	RequiredChecks []RequiredCheck `json:"required_checks"`
	ReuseMaxAgeHrs int             `json:"reuse_max_age_hours"`
}

type Capability struct {
	// Targets are the only remote endpoints (AgentCard ids) a node of this
	// capability may call; the model cannot name a new one.
	Targets []string `json:"targets,omitempty"`
	// Resend is "never" (default) or "same_key": whether the remote API is
	// documented as idempotent for a resend of the same call b with the same κ.
	Resend string `json:"resend,omitempty"`
	// Queryable means the remote task can be looked up by its reference after
	// a lost reply, so an unknown outcome is resolved by query, not resend.
	Queryable bool `json:"queryable,omitempty"`
	// Reusable allows a new task to adopt another task's committed result for
	// identical input material (κ_in), subject to ReuseMaxAgeHrs.
	Reusable bool `json:"reusable,omitempty"`
}

type RequiredCheck struct {
	Checker     string `json:"checker"`
	RuleVersion string `json:"rule_version"`
}

// PublishPolicy stores a new policy version and makes it the active one.
func (s *Store) PublishPolicy(p Principal, pol Policy) (string, error) {
	if err := need(p, KindAdmin, ""); err != nil {
		return "", err
	}
	if pol.Version == "" || len(pol.Capabilities) == 0 {
		return "", refuse(CodeInvalid, "policy needs a version and at least one capability")
	}
	for id, c := range pol.Capabilities {
		if c.Resend != "" && c.Resend != "never" && c.Resend != "same_key" {
			return "", refuse(CodeInvalid, "capability %s: resend must be never or same_key", id)
		}
	}
	d, _, err := Digest(pol)
	if err != nil {
		return "", err
	}
	return d, s.tx("publish_policy", p.ID, func(x *Tx) error {
		var old string
		if err := x.row(`SELECT digest FROM policies WHERE version=?`, pol.Version).Scan(&old); err == nil {
			if old != d {
				return refuse(CodeConflict, "policy %s already published with different content; publish a new version", pol.Version)
			}
		} else if !noRows(err) {
			return err
		} else if err := x.exec(`INSERT INTO policies(version,doc,digest,published_by,published_at,active) VALUES(?,?,?,?,?,0)`,
			pol.Version, mustJSON(pol), d, p.ID, x.ts()); err != nil {
			return err
		}
		if err := x.exec(`UPDATE policies SET active = (version=?)`, pol.Version); err != nil {
			return err
		}
		return x.event("policy_published", "", "", "", map[string]any{"version": pol.Version, "digest": d})
	})
}

func (x *Tx) activePolicy() (Policy, error) {
	var doc string
	err := x.row(`SELECT doc FROM policies WHERE active=1`).Scan(&doc)
	if noRows(err) {
		return Policy{}, refuse(CodeBlocked, "no policy published: nothing can be adopted")
	}
	if err != nil {
		return Policy{}, err
	}
	var p Policy
	return p, json.Unmarshal([]byte(doc), &p)
}

func (x *Tx) policy(version string) (Policy, error) {
	var doc string
	if err := x.row(`SELECT doc FROM policies WHERE version=?`, version).Scan(&doc); err != nil {
		return Policy{}, fmt.Errorf("policy %s: %w", version, err)
	}
	var p Policy
	return p, json.Unmarshal([]byte(doc), &p)
}

// Material is a registered, digest-pinned source snapshot (W01 source.json).
type Material struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
	Title  string `json:"title,omitempty"`
}

// RegisterMaterial records a snapshot an admin has verified. Tasks may only
// reference registered (id, sha256) pairs; a new file version is a new pair.
func (s *Store) RegisterMaterial(p Principal, m Material) error {
	if err := need(p, KindAdmin, ""); err != nil {
		return err
	}
	if m.ID == "" || len(m.SHA256) != 64 {
		return refuse(CodeInvalid, "material needs an id and a hex SHA-256")
	}
	return s.tx("register_material", p.ID, func(x *Tx) error {
		if err := x.exec(`INSERT INTO materials(id,sha256,title,registered_by,registered_at) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`,
			m.ID, m.SHA256, m.Title, p.ID, x.ts()); err != nil {
			return err
		}
		return x.event("material_registered", "", "", "", m)
	})
}

// Graph is a candidate business graph: which capabilities run in which
// dependency order, and which node's result is the deliverable report.
type Graph struct {
	Nodes  []Node `json:"nodes"`
	Report string `json:"report"`
}

type Node struct {
	ID            string   `json:"id"`
	Capability    string   `json:"capability"`
	DependsOn     []string `json:"depends_on,omitempty"`
	MaxIterations int      `json:"max_iterations,omitempty"` // loop bound; 0 means a single run
}

func (g Graph) node(id string) (Node, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// CheckGraph lists every reason the graph cannot be mounted under pol (A02).
func CheckGraph(g Graph, pol Policy) []string {
	var out []string
	if len(g.Nodes) == 0 {
		return []string{"图中没有节点"}
	}
	if pol.MaxNodes > 0 && len(g.Nodes) > pol.MaxNodes {
		out = append(out, fmt.Sprintf("图有 %d 个节点，策略上限是 %d", len(g.Nodes), pol.MaxNodes))
	}
	ids := map[string]Node{}
	for _, n := range g.Nodes {
		if n.ID == "" {
			out = append(out, "有节点缺少 id")
			continue
		}
		if _, dup := ids[n.ID]; dup {
			out = append(out, fmt.Sprintf("节点 id 重复：%s", n.ID))
		}
		ids[n.ID] = n
		if _, ok := pol.Capabilities[n.Capability]; !ok {
			out = append(out, fmt.Sprintf("节点 %s 使用了未登记的能力 %q", n.ID, n.Capability))
		}
		if n.MaxIterations < 0 || (n.MaxIterations > 0 && (pol.MaxLoopIterations == 0 || n.MaxIterations > pol.MaxLoopIterations)) {
			out = append(out, fmt.Sprintf("节点 %s 的循环上界 %d 超出策略上限 %d", n.ID, n.MaxIterations, pol.MaxLoopIterations))
		}
	}
	for _, n := range g.Nodes {
		for _, d := range n.DependsOn {
			if _, ok := ids[d]; !ok {
				out = append(out, fmt.Sprintf("节点 %s 依赖不存在的节点 %s", n.ID, d))
			}
		}
	}
	if _, ok := ids[g.Report]; !ok {
		out = append(out, fmt.Sprintf("报告节点 %q 不在图中", g.Report))
	}
	// cycle check (Kahn)
	indeg := map[string]int{}
	for id := range ids {
		indeg[id] = 0
	}
	for _, n := range ids {
		for _, d := range n.DependsOn {
			if _, ok := ids[d]; ok {
				indeg[n.ID]++
			}
		}
	}
	var q []string
	for id, d := range indeg {
		if d == 0 {
			q = append(q, id)
		}
	}
	seen := 0
	for len(q) > 0 {
		id := q[0]
		q = q[1:]
		seen++
		for _, n := range ids {
			for _, d := range n.DependsOn {
				if d == id {
					if indeg[n.ID]--; indeg[n.ID] == 0 {
						q = append(q, n.ID)
					}
				}
			}
		}
	}
	if seen < len(ids) {
		out = append(out, "图中存在依赖环")
	}
	sort.Strings(out)
	return out
}
