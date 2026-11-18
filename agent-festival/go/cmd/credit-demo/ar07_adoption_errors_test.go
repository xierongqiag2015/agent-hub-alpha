package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// AR-07 review (#43): a judgment whose content passed every check can still
// fail to be adopted. Those failures must not come back as POLICY_REJECTED,
// or the judge revises its answer and calls the model again.

func judgmentReady(t *testing.T) (*Service, *httptest.Server, *Run, *Job) {
	t.Helper()
	e := newAR04(t, true)
	s := e.open()
	mux := http.NewServeMux()
	s.routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	_, r := e.proposeAndApprove(s)
	job, err := s.LeaseAs("worker-a", "model-a")
	if err != nil || job == nil {
		t.Fatalf("lease: %v", err)
	}
	if err := s.StepPut(r.ID, job.Token, "related", "verified evidence"); err != nil {
		t.Fatal(err)
	}
	return s, srv, r, job
}

func postJudgment(t *testing.T, srv *httptest.Server, r *Run, job *Job, raw string) ar07Reply {
	t.Helper()
	return ar07Post(t, srv, "/exec/judgment", map[string]any{"run_id": r.ID, "token": job.Token, "step": "judge", "raw": raw})
}

const validJudgment = `{"level":"grant-50000","evidence_steps":["related"],"rationale":"test"}`

func TestAR07_AdoptionLedgerWriteFailureIsStorageNotPolicy(t *testing.T) {
	s, srv, r, job := judgmentReady(t)
	if _, err := s.machine(r.ID); err != nil { // the gate is open; its file then cannot be written
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.cfg.DataDir, "runs", r.ID, "adopt.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	expectCode(t, "valid judgment whose adoption cannot be recorded", postJudgment(t, srv, r, job, validJudgment), 503, "STORAGE_UNAVAILABLE")
	s.mu.Lock()
	decision := s.st.Runs[r.ID].Decision
	s.mu.Unlock()
	if decision != nil {
		t.Fatalf("a decision that was not recorded must not be applied: %v", decision)
	}
}

func TestAR07_AdoptionLedgerThatCannotOpenIsStorage(t *testing.T) {
	s, srv, r, job := judgmentReady(t)
	if err := os.MkdirAll(filepath.Join(s.cfg.DataDir, "runs", r.ID, "adopt.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	expectCode(t, "judgment when the adoption ledger cannot be opened", postJudgment(t, srv, r, job, validJudgment), 503, "STORAGE_UNAVAILABLE")
}

func TestAR07_ConflictingSecondJudgmentIsInputConflict(t *testing.T) {
	_, srv, r, job := judgmentReady(t)
	if got := postJudgment(t, srv, r, job, validJudgment); got.status != 200 {
		t.Fatalf("first judgment: HTTP %d %s", got.status, got.Error)
	}
	expectCode(t, "same run, different decision", postJudgment(t, srv, r, job, `{"level":"deny","evidence_steps":["related"],"rationale":"changed"}`),
		409, "INPUT_CONFLICT")
	if got := postJudgment(t, srv, r, job, validJudgment); got.status != 200 {
		t.Fatalf("resubmitting the adopted decision is a reuse: HTTP %d %s", got.status, got.Error)
	}
	// content the spec refuses is still a policy rejection
	expectCode(t, "level outside the spec", postJudgment(t, srv, r, job, `{"level":"grant-1","evidence_steps":["related"],"rationale":"x"}`),
		422, "POLICY_REJECTED")
}
