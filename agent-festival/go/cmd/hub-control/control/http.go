package control

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

//go:embed web/console.html
var consoleHTML []byte

// Handler exposes the store over HTTP. Identity comes only from the
// Authorization: Bearer session token issued by POST /v1/login; headers such
// as X-User-ID are ignored.
func (s *Store) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(consoleHTML)
	})
	mux.HandleFunc("GET /v1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"service":  "hub-control (#16 W04: control and formal results)",
			"identity": "demo accounts issued by this service; not enterprise authentication",
			"auth":     "POST /v1/login {id, secret} -> token; then Authorization: Bearer <token>",
			"read":     []string{"GET /v1/me", "GET /v1/tasks", "GET /v1/tasks/{id}", "GET /v1/tasks/{id}/unresolved", "GET /v1/events?task=&after=", "GET /v1/artifacts/{digest}", "GET /v1/knowledge"},
			"write":    []string{"POST /v1/plans", "POST /v1/commands/{adopt,pause,resume,review,accept,case,revoke}", "POST /v1/exec/{lease,heartbeat,release,claim,commit,fail,progress}", "POST /v1/exec/calls/{bind,dispatch,observe}", "POST /v1/checks"},
			"health":   "GET /healthz (?deep=1 runs integrity_check)",
		})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		err := s.Ping()
		if err == nil && r.URL.Query().Get("deep") == "1" {
			err = s.IntegrityCheck()
		}
		if err != nil {
			writeErr(w, refuse(CodeStorage, "database unavailable: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /v1/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID     string `json:"id"`
			Secret string `json:"secret"`
		}
		if !decode(w, r, &in) {
			return
		}
		tok, p, err := s.Login(in.ID, in.Secret)
		reply(w, map[string]any{"token": tok, "principal": p}, err)
	})
	mux.HandleFunc("GET /v1/me", s.authed(func(p Principal, w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, p)
	}))

	// management plane
	mux.HandleFunc("POST /v1/admin/policies", s.authed(func(p Principal, w http.ResponseWriter, r *http.Request) {
		var in Policy
		if decode(w, r, &in) {
			d, err := s.PublishPolicy(p, in)
			reply(w, map[string]any{"version": in.Version, "digest": d}, err)
		}
	}))
	mux.HandleFunc("POST /v1/admin/materials", s.authed(func(p Principal, w http.ResponseWriter, r *http.Request) {
		var in Material
		if decode(w, r, &in) {
			reply(w, in, s.RegisterMaterial(p, in))
		}
	}))

	// user plane: planner proposals and user commands
	mux.HandleFunc("POST /v1/plans", s.authed(func(p Principal, w http.ResponseWriter, r *http.Request) {
		var in ProposeInput
		if decode(w, r, &in) {
			out, err := s.Propose(p, in)
			reply(w, out, err)
		}
	}))
	cmd := func(path string, fn func(Principal, json.RawMessage) (any, error)) {
		mux.HandleFunc("POST "+path, s.authed(func(p Principal, w http.ResponseWriter, r *http.Request) {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
			if err != nil {
				writeErr(w, refuse(CodeInvalid, "read body: %v", err))
				return
			}
			out, err := fn(p, raw)
			reply(w, out, err)
		}))
	}
	cmd("/v1/commands/adopt", func(p Principal, raw json.RawMessage) (any, error) {
		var in AdoptInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.Adopt(p, in)
	})
	cmd("/v1/commands/pause", func(p Principal, raw json.RawMessage) (any, error) {
		var in TaskCommand
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.Pause(p, in)
	})
	cmd("/v1/commands/resume", func(p Principal, raw json.RawMessage) (any, error) {
		var in TaskCommand
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.Resume(p, in)
	})
	cmd("/v1/commands/review", func(p Principal, raw json.RawMessage) (any, error) {
		var in ReviewInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.Review(p, in)
	})
	cmd("/v1/commands/accept", func(p Principal, raw json.RawMessage) (any, error) {
		var in AcceptInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.Accept(p, in)
	})
	cmd("/v1/commands/revoke", func(p Principal, raw json.RawMessage) (any, error) {
		var in RevokeInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.Revoke(p, in)
	})
	mux.HandleFunc("GET /v1/knowledge", s.authed(func(p Principal, w http.ResponseWriter, r *http.Request) {
		v, err := s.Knowledge()
		reply(w, v, err)
	}))
	cmd("/v1/commands/case", func(p Principal, raw json.RawMessage) (any, error) {
		var in CaseInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.OpenCase(p, in)
	})

	// runtime plane: the Java Graph host
	cmd("/v1/exec/lease", func(p Principal, raw json.RawMessage) (any, error) {
		var in LeaseInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		l, err := s.Lease(p, in)
		if err == nil && l == nil {
			return map[string]any{"lease": nil}, nil
		}
		return l, err
	})
	cmd("/v1/exec/heartbeat", func(p Principal, raw json.RawMessage) (any, error) {
		var in LeaseRef
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		until, err := s.Heartbeat(p, in)
		return map[string]any{"lease_until": until}, err
	})
	cmd("/v1/exec/release", func(p Principal, raw json.RawMessage) (any, error) {
		var in LeaseRef
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return map[string]any{"released": true}, s.Release(p, in)
	})
	cmd("/v1/exec/claim", func(p Principal, raw json.RawMessage) (any, error) {
		var in ClaimInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.Claim(p, in)
	})
	cmd("/v1/exec/commit", func(p Principal, raw json.RawMessage) (any, error) {
		var in CommitInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.Commit(p, in)
	})
	cmd("/v1/exec/fail", func(p Principal, raw json.RawMessage) (any, error) {
		var in FailInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return map[string]any{"recorded": true}, s.Fail(p, in)
	})
	cmd("/v1/exec/progress", func(p Principal, raw json.RawMessage) (any, error) {
		var in ProgressInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		applied, err := s.Progress(p, in)
		return map[string]any{"applied": applied}, err
	})
	cmd("/v1/exec/calls/bind", func(p Principal, raw json.RawMessage) (any, error) {
		var in BindInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.BindCall(p, in)
	})
	cmd("/v1/exec/calls/dispatch", func(p Principal, raw json.RawMessage) (any, error) {
		var in CallRef
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.Dispatch(p, in)
	})
	cmd("/v1/exec/calls/observe", func(p Principal, raw json.RawMessage) (any, error) {
		var in ObserveInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.Observe(p, in)
	})
	mux.HandleFunc("POST /v1/permits/verify", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Permit  string          `json:"permit"`
			Target  string          `json:"target"`
			Payload json.RawMessage `json:"payload"`
		}
		if decode(w, r, &in) {
			out, err := s.VerifyPermit(in.Permit, in.Target, in.Payload)
			reply(w, out, err)
		}
	})

	// checkers (W06)
	cmd("/v1/checks", func(p Principal, raw json.RawMessage) (any, error) {
		var in CheckInput
		if err := strictJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.RecordCheck(p, in)
	})

	// projections (read-only; history reads never trigger anything)
	mux.HandleFunc("GET /v1/tasks", s.authed(func(p Principal, w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		ts, err := s.Tasks(limit)
		reply(w, map[string]any{"tasks": ts}, err)
	}))
	mux.HandleFunc("GET /v1/tasks/{id}", s.authed(func(p Principal, w http.ResponseWriter, r *http.Request) {
		v, err := s.View(p, r.PathValue("id"))
		reply(w, v, err)
	}))
	mux.HandleFunc("GET /v1/tasks/{id}/unresolved", s.authed(func(p Principal, w http.ResponseWriter, r *http.Request) {
		v, err := s.Unresolved(r.PathValue("id"))
		reply(w, map[string]any{"calls": v}, err)
	}))
	mux.HandleFunc("GET /v1/events", s.authed(func(p Principal, w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		ev, err := s.Events(r.URL.Query().Get("task"), after, limit)
		reply(w, map[string]any{"events": ev}, err)
	}))
	mux.HandleFunc("GET /v1/artifacts/{digest}", s.authed(func(p Principal, w http.ResponseWriter, r *http.Request) {
		body, err := s.Artifact(r.PathValue("digest"))
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	return mux
}

func (s *Store) authed(h func(Principal, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeErr(w, refuse(CodeUnauthed, "Authorization: Bearer <session token> required"))
			return
		}
		p, err := s.Authenticate(tok)
		if err != nil {
			writeErr(w, err)
			return
		}
		h(p, w, r)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err == nil {
		err = strictJSON(raw, v)
	}
	if err != nil {
		writeErr(w, err)
		return false
	}
	return true
}

func strictJSON(raw []byte, v any) error {
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = []byte("{}")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return refuse(CodeInvalid, "request body: %v", err)
	}
	return nil
}

var statusOf = map[string]int{
	CodeInvalid: http.StatusBadRequest, CodeUnauthed: http.StatusUnauthorized, CodeForbidden: http.StatusForbidden,
	CodeNotFound: http.StatusNotFound, CodeConflict: http.StatusConflict, CodeFenced: http.StatusConflict,
	CodeNotReady: http.StatusConflict, CodePaused: http.StatusConflict, CodeBlocked: http.StatusConflict,
	CodeInProgress: http.StatusConflict, CodeStorage: http.StatusServiceUnavailable,
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func writeErr(w http.ResponseWriter, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: "internal", Msg: err.Error()}
	}
	st, ok := statusOf[e.Code]
	if !ok {
		st = http.StatusInternalServerError
	}
	writeJSON(w, st, map[string]any{"error": e})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}
