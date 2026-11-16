// Command ctl is the spike control plane: it owns node-execution identity (o),
// formal results, business requests and attempts, and serves a fault-injectable
// bank API stub on a second port. Every invariant is a unique key or a
// conditional update inside one SQLite transaction.
package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var db *sql.DB

const schema = `
CREATE TABLE IF NOT EXISTS node_exec(task TEXT, node TEXT, iter TEXT, o TEXT UNIQUE, owner TEXT, owner_gen INTEGER, lease_until INTEGER, PRIMARY KEY(task,node,iter));
CREATE TABLE IF NOT EXISTS results(o TEXT PRIMARY KEY, owner_gen INTEGER, h TEXT, result TEXT, k_id TEXT, committed_at TEXT);
CREATE TABLE IF NOT EXISTS knowledge(id TEXT PRIMARY KEY, node TEXT, version TEXT, valid INTEGER);
CREATE TABLE IF NOT EXISTS business_requests(b TEXT PRIMARY KEY, o TEXT UNIQUE, h TEXT, payload TEXT, state TEXT, provider_ref TEXT);
CREATE TABLE IF NOT EXISTS attempts(b TEXT, a INTEGER, owner_gen INTEGER, state TEXT, detail TEXT, at TEXT, PRIMARY KEY(b,a));
CREATE TABLE IF NOT EXISTS counters(name TEXT PRIMARY KEY, n INTEGER);
CREATE TABLE IF NOT EXISTS events(seq INTEGER PRIMARY KEY AUTOINCREMENT, at TEXT, actor TEXT, kind TEXT, task TEXT, o TEXT, detail TEXT);
`

func now() time.Time { return time.Now() }

func ts() string { return now().Format("15:04:05.000") }

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:8])
}

func event(tx *sql.Tx, actor, kind, task, o, detail string) error {
	_, err := tx.Exec(`INSERT INTO events(at,actor,kind,task,o,detail) VALUES(?,?,?,?,?,?)`, ts(), actor, kind, task, o, detail)
	return err
}

type req map[string]any

func (r req) s(k string) string {
	if v, ok := r[k]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func (r req) i(k string) int64 {
	if v, ok := r[k].(float64); ok {
		return int64(v)
	}
	return 0
}

// handle wraps a handler body in one transaction; the body's map is the reply.
func handle(fn func(tx *sql.Tx, in req) (map[string]any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in := req{}
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		}
		for k, v := range r.URL.Query() {
			in[k] = v[0]
		}
		tx, err := db.Begin()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		out, err := fn(tx, in)
		if err != nil {
			tx.Rollback()
			http.Error(w, err.Error(), 500)
			return
		}
		if err := tx.Commit(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}
}

// claim resolves (task,node,iter) to o. It returns the formal result when one
// exists, "busy" while another owner holds a live lease, otherwise it grants
// ownership and bumps owner_gen (the fencing token).
func claim(tx *sql.Tx, in req) (map[string]any, error) {
	task, node, iter, owner := in.s("task"), in.s("node"), in.s("iter"), in.s("owner")
	lease := in.i("lease_ms")
	o := fmt.Sprintf("%s/%s/%s", task, node, iter)
	var res sql.NullString
	tx.QueryRow(`SELECT result FROM results WHERE o=?`, o).Scan(&res)
	if res.Valid {
		return map[string]any{"status": "result", "o": o, "result": json.RawMessage(res.String)}, event(tx, owner, "claim:reuse", task, o, "formal result exists; inner not executed")
	}
	var curOwner string
	var gen, until int64
	err := tx.QueryRow(`SELECT owner, owner_gen, lease_until FROM node_exec WHERE task=? AND node=? AND iter=?`, task, node, iter).Scan(&curOwner, &gen, &until)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		gen = 1
		_, err = tx.Exec(`INSERT INTO node_exec VALUES(?,?,?,?,?,?,?)`, task, node, iter, o, owner, gen, now().UnixMilli()+lease)
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": "owned", "o": o, "owner_gen": gen}, event(tx, owner, "claim:owned", task, o, fmt.Sprintf("gen=%d", gen))
	case err != nil:
		return nil, err
	}
	if curOwner != owner && until > now().UnixMilli() {
		return map[string]any{"status": "busy", "o": o, "owner": curOwner, "owner_gen": gen}, event(tx, owner, "claim:busy", task, o, fmt.Sprintf("held by %s gen=%d", curOwner, gen))
	}
	if curOwner != owner {
		gen++
	}
	if _, err := tx.Exec(`UPDATE node_exec SET owner=?, owner_gen=?, lease_until=? WHERE o=?`, owner, gen, now().UnixMilli()+lease, o); err != nil {
		return nil, err
	}
	kind := "claim:owned"
	if curOwner != owner {
		kind = "claim:takeover"
	}
	return map[string]any{"status": "owned", "o": o, "owner_gen": gen}, event(tx, owner, kind, task, o, fmt.Sprintf("gen=%d (previous owner %s)", gen, curOwner))
}

// commit writes the single formal result for o. Same h returns the existing
// result; a stale owner_gen is fenced; a referenced knowledge item must still
// be valid inside the same transaction.
func commit(tx *sql.Tx, in req) (map[string]any, error) {
	o, owner, gen := in.s("o"), in.s("owner"), in.i("owner_gen")
	raw, _ := json.Marshal(in["result"])
	h := hashOf(string(raw))
	task := ""
	tx.QueryRow(`SELECT task FROM node_exec WHERE o=?`, o).Scan(&task)
	var existing, existingH string
	if err := tx.QueryRow(`SELECT result, h FROM results WHERE o=?`, o).Scan(&existing, &existingH); err == nil {
		return map[string]any{"status": "duplicate", "result": json.RawMessage(existing)}, event(tx, owner, "commit:duplicate", task, o, "already committed; caller adopts existing result")
	}
	var curGen int64
	if err := tx.QueryRow(`SELECT owner_gen FROM node_exec WHERE o=?`, o).Scan(&curGen); err != nil {
		return nil, err
	}
	if curGen != gen {
		return map[string]any{"status": "stale_owner", "current_gen": curGen}, event(tx, owner, "commit:fenced", task, o, fmt.Sprintf("gen=%d < current %d", gen, curGen))
	}
	kid := in.s("k_id")
	if kid != "" {
		var valid int
		tx.QueryRow(`SELECT valid FROM knowledge WHERE id=?`, kid).Scan(&valid)
		if valid != 1 {
			return map[string]any{"status": "k_invalid"}, event(tx, owner, "commit:k_invalid", task, o, kid+" is no longer valid at commit time")
		}
	}
	if _, err := tx.Exec(`INSERT INTO results VALUES(?,?,?,?,?,?)`, o, gen, h, string(raw), kid, ts()); err != nil {
		return nil, err
	}
	return map[string]any{"status": "committed", "h": h}, event(tx, owner, "commit:ok", task, o, fmt.Sprintf("gen=%d h=%s k=%s", gen, h, kid))
}

func bind(tx *sql.Tx, in req) (map[string]any, error) {
	o := in.s("o")
	raw, _ := json.Marshal(in["payload"])
	h := hashOf(string(raw))
	b := "B:" + o
	var curH, state string
	err := tx.QueryRow(`SELECT h, state FROM business_requests WHERE b=?`, b).Scan(&curH, &state)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.Exec(`INSERT INTO business_requests VALUES(?,?,?,?,?,?)`, b, o, h, string(raw), "BOUND", ""); err != nil {
			return nil, err
		}
		return map[string]any{"b": b, "state": "BOUND", "h": h}, event(tx, in.s("owner"), "b:bind", "", o, b+" "+h)
	}
	if curH != h {
		return map[string]any{"b": b, "state": "CONFLICT"}, event(tx, in.s("owner"), "b:conflict", "", o, "same B, different payload")
	}
	return map[string]any{"b": b, "state": state, "h": h}, nil
}

// dispatch is the local control boundary: only a BOUND request held by the
// current owner generation gets a send permit; anything already committed
// goes to recovery instead of a blind resend.
func dispatch(tx *sql.Tx, in req) (map[string]any, error) {
	b, owner, gen := in.s("b"), in.s("owner"), in.i("owner_gen")
	var o, state, ref string
	if err := tx.QueryRow(`SELECT o, state, provider_ref FROM business_requests WHERE b=?`, b).Scan(&o, &state, &ref); err != nil {
		return nil, err
	}
	var curGen int64
	tx.QueryRow(`SELECT owner_gen FROM node_exec WHERE o=?`, o).Scan(&curGen)
	if curGen != gen {
		return map[string]any{"decision": "fenced"}, event(tx, owner, "dispatch:fenced", "", o, fmt.Sprintf("gen=%d < current %d", gen, curGen))
	}
	switch state {
	case "BOUND":
		if _, err := tx.Exec(`UPDATE business_requests SET state='DISPATCH_COMMITTED' WHERE b=?`, b); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO attempts VALUES(?,?,?,?,?,?)`, b, 1, gen, "PERMITTED", "", ts()); err != nil {
			return nil, err
		}
		return map[string]any{"decision": "send", "a": 1, "idem_key": b}, event(tx, owner, "dispatch:permit", "", o, b+" attempt=1")
	case "SUCCEEDED", "DECLINED":
		return map[string]any{"decision": "done", "state": state, "ref": ref}, event(tx, owner, "dispatch:done", "", o, state)
	default:
		return map[string]any{"decision": "recover", "state": state}, event(tx, owner, "dispatch:recover", "", o, "state="+state+"; no blind resend")
	}
}

var terminal = map[string]bool{"SUCCEEDED": true, "DECLINED": true}

func observe(tx *sql.Tx, in req) (map[string]any, error) {
	b, outcome := in.s("b"), in.s("outcome")
	var o, state string
	if err := tx.QueryRow(`SELECT o, state FROM business_requests WHERE b=?`, b).Scan(&o, &state); err != nil {
		return nil, err
	}
	next := map[string]string{"accepted": "PENDING", "succeeded": "SUCCEEDED", "declined": "DECLINED", "timeout": "UNRESOLVED", "error": "UNRESOLVED"}[outcome]
	if terminal[state] && next != state {
		return map[string]any{"state": state}, event(tx, in.s("actor"), "observe:ignored", "", o, outcome+" after terminal "+state)
	}
	if state == "PENDING" && next == "UNRESOLVED" {
		next = "PENDING"
	}
	if _, err := tx.Exec(`UPDATE business_requests SET state=?, provider_ref=COALESCE(NULLIF(?,''),provider_ref) WHERE b=?`, next, in.s("ref"), b); err != nil {
		return nil, err
	}
	if a := in.i("a"); a > 0 {
		tx.Exec(`UPDATE attempts SET state=?, detail=? WHERE b=? AND a=?`, outcome, in.s("ref"), b, a)
	}
	return map[string]any{"state": next}, event(tx, in.s("actor"), "observe:"+outcome, "", o, b+" -> "+next+" "+in.s("ref"))
}

// recoverB resolves a dispatched request by the provider's declared
// capability (query, then idempotent resend under the same key).
func recoverB(tx *sql.Tx, in req) (map[string]any, error) {
	b := in.s("b")
	var o, state, ref string
	if err := tx.QueryRow(`SELECT o, state, provider_ref FROM business_requests WHERE b=?`, b).Scan(&o, &state, &ref); err != nil {
		return nil, err
	}
	if terminal[state] || state == "PENDING" {
		return map[string]any{"decision": "done", "state": state, "ref": ref}, nil
	}
	found, qref, qstatus := bankQuery(b)
	if found {
		next := map[string]string{"accepted": "SUCCEEDED", "pending": "PENDING"}[qstatus]
		tx.Exec(`UPDATE business_requests SET state=?, provider_ref=? WHERE b=?`, next, qref, b)
		return map[string]any{"decision": "done", "state": next, "ref": qref}, event(tx, in.s("actor"), "recover:query-found", "", o, b+" provider has it ("+qref+"); no resend")
	}
	var a int64
	tx.QueryRow(`SELECT COALESCE(MAX(a),0) FROM attempts WHERE b=?`, b).Scan(&a)
	a++
	var gen int64
	tx.QueryRow(`SELECT owner_gen FROM node_exec WHERE o=?`, o).Scan(&gen)
	tx.Exec(`INSERT INTO attempts VALUES(?,?,?,?,?,?)`, b, a, gen, "PERMITTED", "resend under same key", ts())
	return map[string]any{"decision": "send", "a": a, "idem_key": b}, event(tx, in.s("actor"), "recover:resend-same-key", "", o, fmt.Sprintf("%s not at provider; capability=idempotent; attempt=%d", b, a))
}

func getB(tx *sql.Tx, in req) (map[string]any, error) {
	var state, ref string
	err := tx.QueryRow(`SELECT state, provider_ref FROM business_requests WHERE b=?`, in.s("b")).Scan(&state, &ref)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"state": "NONE"}, nil
	}
	return map[string]any{"state": state, "ref": ref}, err
}

func count(tx *sql.Tx, in req) (map[string]any, error) {
	_, err := tx.Exec(`INSERT INTO counters VALUES(?,1) ON CONFLICT(name) DO UPDATE SET n=n+1`, in.s("name"))
	if err == nil {
		err = event(tx, in.s("actor"), "count", in.s("task"), in.s("o"), in.s("name"))
	}
	return map[string]any{}, err
}

func kPublish(tx *sql.Tx, in req) (map[string]any, error) {
	_, err := tx.Exec(`INSERT OR REPLACE INTO knowledge VALUES(?,?,?,1)`, in.s("id"), in.s("node"), in.s("version"))
	return map[string]any{}, err
}

func kInvalidate(tx *sql.Tx, in req) (map[string]any, error) {
	_, err := tx.Exec(`UPDATE knowledge SET valid=0 WHERE id=?`, in.s("id"))
	if err == nil {
		err = event(tx, in.s("actor"), "k:invalidate", "", "", in.s("id"))
	}
	return map[string]any{}, err
}

func kFind(tx *sql.Tx, in req) (map[string]any, error) {
	var id, version string
	err := tx.QueryRow(`SELECT id, version FROM knowledge WHERE node=? AND valid=1 ORDER BY id`, in.s("node")).Scan(&id, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"found": false}, nil
	}
	return map[string]any{"found": true, "id": id, "version": version}, err
}

func dump(tx *sql.Tx, _ req) (map[string]any, error) {
	out := map[string]any{}
	for _, t := range []string{"node_exec", "results", "knowledge", "business_requests", "attempts", "counters", "events"} {
		rows, err := tx.Query("SELECT * FROM " + t)
		if err != nil {
			return nil, err
		}
		cols, _ := rows.Columns()
		var list []map[string]any
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			rows.Scan(ptrs...)
			m := map[string]any{}
			for i, c := range cols {
				if b, ok := vals[i].([]byte); ok {
					m[c] = string(b)
				} else {
					m[c] = vals[i]
				}
			}
			list = append(list, m)
		}
		rows.Close()
		out[t] = list
	}
	bankMu.Lock()
	out["bank"] = map[string]any{"calls": bankCalls, "effects": bankEffects}
	bankMu.Unlock()
	return out, nil
}

func reset(tx *sql.Tx, _ req) (map[string]any, error) {
	for _, t := range []string{"node_exec", "results", "knowledge", "business_requests", "attempts", "counters", "events"} {
		if _, err := tx.Exec("DELETE FROM " + t); err != nil {
			return nil, err
		}
	}
	tx.Exec(`DELETE FROM sqlite_sequence WHERE name='events'`)
	bankMu.Lock()
	bankCalls, bankEffects, bankSeq = map[string]int{}, map[string]map[string]any{}, 0
	bankMu.Unlock()
	return map[string]any{}, nil
}

// ---- bank API stub: idempotent by key, queryable, optional delay/pending ----

var (
	bankMu      sync.Mutex
	bankCalls   = map[string]int{}
	bankEffects = map[string]map[string]any{}
	bankSeq     int
)

func bankApply(w http.ResponseWriter, r *http.Request) {
	var in req
	json.NewDecoder(r.Body).Decode(&in)
	key := in.s("key")
	bankMu.Lock()
	bankCalls[key]++
	bankMu.Unlock()
	time.Sleep(time.Duration(in.i("delay_ms")) * time.Millisecond)
	bankMu.Lock()
	eff, ok := bankEffects[key]
	if !ok {
		bankSeq++
		status := "accepted"
		if in.s("mode") == "pending" {
			status = "pending"
		}
		eff = map[string]any{"ref": fmt.Sprintf("BANK-%04d", bankSeq), "amount": in.s("amount"), "status": status, "at": ts()}
		bankEffects[key] = eff
	}
	bankMu.Unlock()
	json.NewEncoder(w).Encode(eff)
}

func bankQueryHandler(w http.ResponseWriter, r *http.Request) {
	bankMu.Lock()
	eff, ok := bankEffects[r.URL.Query().Get("key")]
	bankMu.Unlock()
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{"found": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"found": true, "ref": eff["ref"], "status": eff["status"]})
}

var bankURL string

// bankQuery calls the provider's query API over HTTP, as a real control
// plane would; the stub merely shares this binary.
func bankQuery(key string) (bool, string, string) {
	resp, err := http.Get(bankURL + "/query?key=" + url.QueryEscape(key))
	if err != nil {
		return false, "", ""
	}
	defer resp.Body.Close()
	var out req
	json.NewDecoder(resp.Body).Decode(&out)
	if out["found"] != true {
		return false, "", ""
	}
	return true, out.s("ref"), out.s("status")
}

func main() {
	dbPath := flag.String("db", "ctl.db", "sqlite path")
	ctlAddr := flag.String("ctl", "127.0.0.1:18080", "control listen address")
	bankAddr := flag.String("bank", "127.0.0.1:18081", "bank stub listen address")
	flag.Parse()
	var err error
	db, err = sql.Open("sqlite", *dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	for path, fn := range map[string]func(*sql.Tx, req) (map[string]any, error){
		"/claim": claim, "/commit": commit, "/b/bind": bind, "/b/dispatch": dispatch,
		"/b/observe": observe, "/b/recover": recoverB, "/b/get": getB, "/count": count,
		"/k/publish": kPublish, "/k/invalidate": kInvalidate, "/k/find": kFind,
		"/state": dump, "/reset": reset,
	} {
		mux.HandleFunc(path, handle(fn))
	}
	bank := http.NewServeMux()
	bank.HandleFunc("/apply", bankApply)
	bank.HandleFunc("/query", bankQueryHandler)
	bankURL = "http://" + *bankAddr
	go func() { log.Fatal(http.ListenAndServe(*bankAddr, bank)) }()
	fmt.Fprintf(os.Stderr, "ctl on %s, bank on %s, db %s\n", *ctlAddr, *bankAddr, *dbPath)
	log.Fatal(http.ListenAndServe(*ctlAddr, mux))
}
