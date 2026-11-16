package control

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// Principal kinds. A kind limits what a principal can ever do; roles narrow it
// further and are read from the database on every request, so a revocation
// takes effect on the next call, not when a token expires.
const (
	KindUser     = "user"     // people: approve graphs, accept reports, review, pause
	KindAgent    = "agent"    // planner models: propose candidate graphs only
	KindExecutor = "executor" // Java Graph host: leases, claims, commits, calls
	KindChecker  = "checker"  // W06 checkers: record check results
	KindAdmin    = "admin"    // publish policies, register materials, manage principals
)

// AssuranceDemo marks identities proven only by a demo account secret held by
// this service. Every approval and delivery records it, and views show it, so
// nothing downstream can mistake it for enterprise authentication.
const AssuranceDemo = "demo-account"

const sessionTTL = 8 * time.Hour

type Principal struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Roles     []string `json:"roles"`
	Assurance string   `json:"assurance"`
	Disabled  bool     `json:"disabled,omitempty"`
}

func (p Principal) Has(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

func hashSecret(salt, secret string) string {
	sum := sha256.Sum256([]byte(salt + "\x00" + secret))
	return hex.EncodeToString(sum[:])
}

// PutPrincipal creates or updates a principal (bootstrap and admin use). It
// sets kind, roles and secret but never re-enables a disabled account, so a
// restart that re-applies the bootstrap file cannot undo a revocation.
func (s *Store) PutPrincipal(actor string, p Principal, secret string) error {
	switch p.Kind {
	case KindUser, KindAgent, KindExecutor, KindChecker, KindAdmin:
	default:
		return refuse(CodeInvalid, "unknown principal kind %q", p.Kind)
	}
	if p.ID == "" || len(secret) < 8 {
		return refuse(CodeInvalid, "principal needs an id and a secret of at least 8 characters")
	}
	if p.Assurance == "" {
		p.Assurance = AssuranceDemo
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	sh := hex.EncodeToString(salt)
	return s.tx("put_principal", actor, func(x *Tx) error {
		if err := x.exec(`INSERT INTO principals(id,kind,roles,assurance,salt,secret_hash,disabled) VALUES(?,?,?,?,?,?,0)
			ON CONFLICT(id) DO UPDATE SET kind=excluded.kind, roles=excluded.roles, assurance=excluded.assurance,
			salt=excluded.salt, secret_hash=excluded.secret_hash`,
			p.ID, p.Kind, strings.Join(p.Roles, ","), p.Assurance, sh, hashSecret(sh, secret)); err != nil {
			return err
		}
		return x.event("principal_put", "", "", "", map[string]any{"id": p.ID, "kind": p.Kind, "roles": p.Roles})
	})
}

// SetRoles replaces a principal's roles; an empty list revokes everything.
func (s *Store) SetRoles(actor, id string, roles []string) error {
	return s.tx("set_roles", actor, func(x *Tx) error {
		if err := x.exec(`UPDATE principals SET roles=? WHERE id=?`, strings.Join(roles, ","), id); err != nil {
			return err
		}
		return x.event("roles_set", "", "", "", map[string]any{"id": id, "roles": roles})
	})
}

// Disable revokes a principal: its existing sessions stop working at once.
func (s *Store) Disable(actor, id string) error {
	return s.tx("disable_principal", actor, func(x *Tx) error {
		if err := x.exec(`UPDATE principals SET disabled=1 WHERE id=?`, id); err != nil {
			return err
		}
		return x.event("principal_disabled", "", "", "", map[string]any{"id": id})
	})
}

func (x *Tx) principal(id string) (Principal, error) {
	var p Principal
	var roles string
	var disabled int
	err := x.row(`SELECT id,kind,roles,assurance,disabled FROM principals WHERE id=?`, id).
		Scan(&p.ID, &p.Kind, &roles, &p.Assurance, &disabled)
	if noRows(err) {
		return p, refuse(CodeUnauthed, "unknown principal")
	}
	if err != nil {
		return p, err
	}
	if roles != "" {
		p.Roles = strings.Split(roles, ",")
	}
	p.Disabled = disabled == 1
	return p, nil
}

type claims struct {
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
}

func (s *Store) sign(payload []byte) string {
	m := hmac.New(sha256.New, s.key)
	m.Write(payload)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Login exchanges an account secret for a session token signed by this
// service. The token names the subject only; kind and roles are looked up on
// each request.
func (s *Store) Login(id, secret string) (string, Principal, error) {
	var p Principal
	err := s.read(func(x *Tx) error {
		var salt, sh string
		if err := x.row(`SELECT salt, secret_hash FROM principals WHERE id=?`, id).Scan(&salt, &sh); err != nil {
			return refuse(CodeUnauthed, "wrong account or secret")
		}
		if subtle.ConstantTimeCompare([]byte(hashSecret(salt, secret)), []byte(sh)) != 1 {
			return refuse(CodeUnauthed, "wrong account or secret")
		}
		var err error
		p, err = x.principal(id)
		if err == nil && p.Disabled {
			err = refuse(CodeUnauthed, "account disabled")
		}
		return err
	})
	if err != nil {
		return "", Principal{}, err
	}
	body, _ := json.Marshal(claims{Sub: id, Exp: s.Now().Add(sessionTTL).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(body)
	return "v1." + payload + "." + s.sign([]byte(payload)), p, nil
}

// Authenticate verifies a session token and returns the principal's current
// record. Request headers such as X-User-ID play no part.
func (s *Store) Authenticate(token string) (Principal, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return Principal{}, refuse(CodeUnauthed, "missing or malformed session token")
	}
	if subtle.ConstantTimeCompare([]byte(s.sign([]byte(parts[1]))), []byte(parts[2])) != 1 {
		return Principal{}, refuse(CodeUnauthed, "session token signature invalid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Principal{}, refuse(CodeUnauthed, "malformed session token")
	}
	var c claims
	if err := json.Unmarshal(raw, &c); err != nil || c.Sub == "" {
		return Principal{}, refuse(CodeUnauthed, "malformed session token")
	}
	if s.Now().Unix() > c.Exp {
		return Principal{}, refuse(CodeUnauthed, "session expired")
	}
	var p Principal
	err = s.read(func(x *Tx) error {
		var err error
		p, err = x.principal(c.Sub)
		return err
	})
	if err != nil {
		return Principal{}, err
	}
	if p.Disabled {
		return Principal{}, refuse(CodeUnauthed, "account disabled")
	}
	return p, nil
}

func need(p Principal, kind, role string) error {
	if p.Kind != kind {
		return refuse(CodeForbidden, "%s %q cannot do this (needs a %s principal)", p.Kind, p.ID, kind)
	}
	if role != "" && !p.Has(role) {
		return refuse(CodeForbidden, "%q lacks role %q", p.ID, role)
	}
	return nil
}
