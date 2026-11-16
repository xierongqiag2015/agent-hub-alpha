// Package control is the W04 control and formal-result module of the #16
// annual-report demo: command dedup, graph adoption, node execution identity,
// remote-call identity, artifact checks, delivery and audit.
//
// Every state transition runs in one SQLite transaction together with its
// audit event, so a transition is either fully recorded or not at all. The
// uniqueness rules the design relies on are table constraints, not code
// paths: one command per (scope, issuer, command_id), one formal result per
// node execution o, one remote-call record per (o, name), one delivery per
// task.
//
// Scope: single instance (one process owns the database file). Identities are
// demo accounts signed by this service, never request headers; see
// docs/control-rights.md for what this does and does not guarantee.
package control

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS meta(k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS principals(
  id TEXT PRIMARY KEY, kind TEXT NOT NULL, roles TEXT NOT NULL, assurance TEXT NOT NULL,
  salt TEXT NOT NULL, secret_hash TEXT NOT NULL, disabled INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS policies(
  version TEXT PRIMARY KEY, doc TEXT NOT NULL, digest TEXT NOT NULL, published_by TEXT NOT NULL,
  published_at TEXT NOT NULL, active INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS materials(
  id TEXT NOT NULL, sha256 TEXT NOT NULL, title TEXT NOT NULL, registered_by TEXT NOT NULL,
  registered_at TEXT NOT NULL, PRIMARY KEY(id, sha256));
CREATE TABLE IF NOT EXISTS commands(
  scope TEXT NOT NULL, issuer TEXT NOT NULL, command_id TEXT NOT NULL, kind TEXT NOT NULL,
  kappa TEXT NOT NULL, result TEXT NOT NULL, at TEXT NOT NULL,
  PRIMARY KEY(scope, issuer, command_id));
CREATE TABLE IF NOT EXISTS tasks(
  id TEXT PRIMARY KEY, scope TEXT NOT NULL, goal TEXT NOT NULL, material TEXT NOT NULL,
  proposed_by TEXT NOT NULL, status TEXT NOT NULL, paused INTEGER NOT NULL DEFAULT 0,
  adopted_rev INTEGER, adopted_digest TEXT, policy_version TEXT, approved_by TEXT, approval_assurance TEXT,
  lease_epoch INTEGER NOT NULL DEFAULT 0, lease_token_hash TEXT, lease_owner TEXT, lease_until INTEGER,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS graphs(
  task TEXT NOT NULL, rev INTEGER NOT NULL, digest TEXT NOT NULL, doc TEXT NOT NULL,
  proposed_by TEXT NOT NULL, problems TEXT NOT NULL, status TEXT NOT NULL, at TEXT NOT NULL,
  PRIMARY KEY(task, rev));
CREATE TABLE IF NOT EXISTS node_exec(
  o TEXT PRIMARY KEY, task TEXT NOT NULL, rev INTEGER NOT NULL, node TEXT NOT NULL, iter INTEGER NOT NULL,
  capability TEXT NOT NULL, kappa_in TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, reused INTEGER NOT NULL DEFAULT 0,
  UNIQUE(task, rev, node, iter));
CREATE TABLE IF NOT EXISTS attempts(
  o TEXT NOT NULL, a INTEGER NOT NULL, epoch INTEGER NOT NULL, owner TEXT NOT NULL, state TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL, ended_at TEXT,
  PRIMARY KEY(o, a));
CREATE TABLE IF NOT EXISTS results(
  o TEXT PRIMARY KEY, outcome TEXT NOT NULL, artifact TEXT NOT NULL, kappa_in TEXT NOT NULL,
  epoch INTEGER NOT NULL, a INTEGER NOT NULL, basis TEXT NOT NULL, reused_from TEXT,
  committed_by TEXT NOT NULL, committed_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS artifacts(
  digest TEXT PRIMARY KEY, media TEXT NOT NULL, body BLOB NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS calls(
  b TEXT PRIMARY KEY, o TEXT NOT NULL, name TEXT NOT NULL, target TEXT NOT NULL, kappa TEXT NOT NULL,
  payload TEXT NOT NULL, state TEXT NOT NULL, remote_ref TEXT, result_artifact TEXT,
  sends INTEGER NOT NULL DEFAULT 0, epoch INTEGER NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(o, name));
CREATE TABLE IF NOT EXISTS call_observations(
  b TEXT NOT NULL, seq INTEGER NOT NULL, kind TEXT NOT NULL, remote_ref TEXT, detail TEXT NOT NULL,
  epoch INTEGER NOT NULL, applied INTEGER NOT NULL, note TEXT NOT NULL, at TEXT NOT NULL,
  PRIMARY KEY(b, seq));
CREATE TABLE IF NOT EXISTS progress(
  o TEXT NOT NULL, a INTEGER NOT NULL, seq INTEGER NOT NULL, kind TEXT NOT NULL, detail TEXT NOT NULL,
  at TEXT NOT NULL, PRIMARY KEY(o, a, seq));
CREATE TABLE IF NOT EXISTS checks(
  id INTEGER PRIMARY KEY AUTOINCREMENT, artifact TEXT NOT NULL, checker TEXT NOT NULL,
  checker_version TEXT NOT NULL, rule_version TEXT NOT NULL, verdict TEXT NOT NULL, findings TEXT NOT NULL,
  recorded_by TEXT NOT NULL, at TEXT NOT NULL,
  UNIQUE(artifact, checker, checker_version, rule_version));
CREATE TABLE IF NOT EXISTS reviews(
  check_id INTEGER PRIMARY KEY, verdict TEXT NOT NULL, note TEXT NOT NULL, reviewer TEXT NOT NULL,
  assurance TEXT NOT NULL, at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS deliveries(
  task TEXT PRIMARY KEY, artifact TEXT NOT NULL, checks TEXT NOT NULL, policy_version TEXT NOT NULL,
  approved_by TEXT NOT NULL, assurance TEXT NOT NULL, at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS cases(
  id TEXT PRIMARY KEY, task TEXT NOT NULL, ref TEXT NOT NULL, text TEXT NOT NULL, opened_by TEXT NOT NULL,
  policy_version TEXT NOT NULL, status TEXT NOT NULL, at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS events(
  seq INTEGER PRIMARY KEY AUTOINCREMENT, at TEXT NOT NULL, actor TEXT NOT NULL, kind TEXT NOT NULL,
  task TEXT NOT NULL DEFAULT '', o TEXT NOT NULL DEFAULT '', b TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS counters(name TEXT PRIMARY KEY, n INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS node_sources(o TEXT NOT NULL, source TEXT NOT NULL, PRIMARY KEY(o, source));
CREATE TABLE IF NOT EXISTS knowledge_revocations(
  ref TEXT PRIMARY KEY, reason TEXT NOT NULL, revoked_by TEXT NOT NULL, at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS knowledge_lookups(
  o TEXT NOT NULL, a INTEGER NOT NULL, outcome TEXT NOT NULL, reason TEXT NOT NULL, source_o TEXT,
  at TEXT NOT NULL, PRIMARY KEY(o, a));
`

// Store owns the control database.
type Store struct {
	db  *sql.DB
	key []byte
	mu  sync.Mutex // serialises transactions in this process

	// Now is the clock used for leases and timestamps (tests replace it).
	Now func() time.Time
	// LeaseTTL overrides DefaultLeaseTTL; PermitTTL overrides DefaultPermitTTL.
	LeaseTTL, PermitTTL time.Duration
	// FailCommit, when set, runs right before a transaction commits; a non-nil
	// error rolls the whole transition back, as a failed disk write would.
	FailCommit func(op string) error
}

// Error is a refusal with a stable code the HTTP layer maps to a status.
type Error struct {
	Code string `json:"code"`
	Msg  string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

const (
	CodeInvalid    = "invalid"
	CodeForbidden  = "forbidden"
	CodeNotFound   = "not_found"
	CodeConflict   = "conflict"
	CodeFenced     = "fenced"
	CodeNotReady   = "not_ready"
	CodePaused     = "paused"
	CodeBlocked    = "blocked"
	CodeStorage    = "storage"
	CodeUnauthed   = "unauthenticated"
	CodeInProgress = "in_progress"
)

func refuse(code, format string, a ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// CodeOf returns the refusal code of err, or "" for unexpected errors.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Open opens (creating if needed) the control database at path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	s := &Store{db: db, Now: time.Now}
	var k string
	switch err := db.QueryRow(`SELECT v FROM meta WHERE k='session_key'`).Scan(&k); {
	case errors.Is(err, sql.ErrNoRows):
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			db.Close()
			return nil, err
		}
		k = hex.EncodeToString(b)
		if _, err := db.Exec(`INSERT INTO meta(k,v) VALUES('session_key',?)`, k); err != nil {
			db.Close()
			return nil, err
		}
	case err != nil:
		db.Close()
		return nil, err
	}
	s.key, _ = hex.DecodeString(k)
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Ping reports whether the database answers (used by /healthz).
func (s *Store) Ping() error {
	var n int
	return s.db.QueryRow(`SELECT 1`).Scan(&n)
}

// Tx is one open transition.
type Tx struct {
	tx    *sql.Tx
	s     *Store
	actor string
	now   time.Time
}

func (s *Store) tx(op, actor string, fn func(*Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.db.Begin()
	if err != nil {
		return refuse(CodeStorage, "begin: %v", err)
	}
	x := &Tx{tx: t, s: s, actor: actor, now: s.Now().UTC()}
	if err := fn(x); err != nil {
		t.Rollback()
		return err
	}
	if s.FailCommit != nil {
		if err := s.FailCommit(op); err != nil {
			t.Rollback()
			return refuse(CodeStorage, "%s not recorded: %v", op, err)
		}
	}
	if err := t.Commit(); err != nil {
		return refuse(CodeStorage, "%s not recorded: %v", op, err)
	}
	return nil
}

func (s *Store) read(fn func(*Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.db.Begin()
	if err != nil {
		return refuse(CodeStorage, "begin: %v", err)
	}
	defer t.Rollback()
	return fn(&Tx{tx: t, s: s, now: s.Now().UTC()})
}

func (x *Tx) exec(q string, args ...any) error {
	if _, err := x.tx.Exec(q, args...); err != nil {
		return fmt.Errorf("%s: %w", strings.Fields(q)[0], err)
	}
	return nil
}

func (x *Tx) row(q string, args ...any) *sql.Row { return x.tx.QueryRow(q, args...) }

func (x *Tx) ts() string { return x.now.Format(time.RFC3339Nano) }

// event appends one audit record in the same transaction as the transition.
func (x *Tx) event(kind, task, o, b string, detail any) error {
	return x.exec(`INSERT INTO events(at,actor,kind,task,o,b,detail) VALUES(?,?,?,?,?,?,?)`,
		x.ts(), x.actor, kind, task, o, b, mustJSON(detail))
}

func (x *Tx) next(name string) (int, error) {
	if err := x.exec(`INSERT INTO counters(name,n) VALUES(?,1) ON CONFLICT(name) DO UPDATE SET n=n+1`, name); err != nil {
		return 0, err
	}
	var n int
	err := x.row(`SELECT n FROM counters WHERE name=?`, name).Scan(&n)
	return n, err
}

func noRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// IntegrityCheck runs SQLite's full consistency check (used after crash tests
// and by /healthz?deep=1).
func (s *Store) IntegrityCheck() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res string
	if err := s.db.QueryRow(`PRAGMA integrity_check`).Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("integrity_check: %s", res)
	}
	return nil
}
