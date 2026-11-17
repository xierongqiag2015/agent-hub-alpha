package main

import (
	"sync"
	"testing"
	"time"
)

// Combination of #31 (AR-04: a run keeps the model it was adopted under) and
// #32 (AR-05: capability discovery outside the lock, revalidated under it).
// Each branch passes alone; this checks the semantics still hold together:
// the input digest that discovery snapshots and revalidates is computed on
// the run's bound model, across a config change, a lease takeover during
// discovery and concurrent BeginCalls.
func TestComboAR04AR05_BoundModelAcrossConfigChangeTakeoverAndConcurrentBegin(t *testing.T) {
	e := newAR04(t, true)
	s := e.open()
	_, r := e.proposeAndApprove(s)
	mustLease(t, s)

	// restart with another configured model; the adopted run keeps model-a
	e.mdl = "model-b"
	s = e.open()
	s.mu.Lock()
	bound, token := s.st.Runs[r.ID].Binding.Model, s.st.Runs[r.ID].Token
	s.mu.Unlock()
	if bound != "model-a" {
		t.Fatalf("run bound to %q after restart with model-b, want model-a", bound)
	}
	in := CallCommand{RunID: r.ID, Token: token, Step: "intake", Input: "case C006", Goal: "intake"}

	// 1. lease taken over while the old executor waits on discovery: no intent
	g := withSlowRDC(t, s)
	begin := beginAsync(s, in)
	<-g.entered
	s.mu.Lock()
	s.st.Runs[r.ID].LeaseUntil = time.Now().Add(-time.Second)
	s.mu.Unlock()
	job, err := s.Lease("new-worker")
	g.release()
	if berr := <-begin; berr == nil {
		t.Fatal("fenced executor's BeginCall succeeded after a takeover during discovery")
	}
	if err != nil || job == nil || job.RunID != r.ID {
		t.Fatalf("takeover lease: job=%v err=%v", job, err)
	}
	s.mu.Lock()
	stale := s.st.Runs[r.ID].Steps["intake"].Call
	s.mu.Unlock()
	if stale != nil {
		t.Fatalf("intent written by the fenced executor: %+v", stale)
	}

	// 2. two concurrent BeginCalls from the new owner: one intent, digest on the bound model
	in.Token = job.Token
	g2 := withSlowRDC(t, s)
	var mu sync.Mutex
	var calls []*RemoteCall
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.BeginCall(in)
			if err != nil {
				t.Errorf("BeginCall: %v", err)
				return
			}
			mu.Lock()
			calls = append(calls, out["call"].(*RemoteCall))
			mu.Unlock()
		}()
	}
	<-g2.entered
	select {
	case <-g2.entered:
	case <-time.After(300 * time.Millisecond):
	}
	g2.release()
	wg.Wait()
	if len(calls) != 2 || calls[0].RequestID != calls[1].RequestID {
		t.Fatalf("concurrent BeginCall produced %d calls / different request ids", len(calls))
	}
	call := calls[0]
	onBound, _ := canonicalHash(map[string]any{"input": in.Input, "goal": in.Goal, "binding": call.Binding, "model": "model-a"})
	onConfig, _ := canonicalHash(map[string]any{"input": in.Input, "goal": in.Goal, "binding": call.Binding, "model": "model-b"})
	if call.InputDigest != onBound || call.InputDigest == onConfig {
		t.Fatalf("intent digest is not on the bound model-a (configured model-b)")
	}

	// 3. another config change and restart: the in-flight call is recognised, not a conflict
	e.mdl = "model-c"
	s = e.open()
	s.mu.Lock()
	in.Token = s.st.Runs[r.ID].Token
	s.mu.Unlock()
	again, err := s.BeginCall(in)
	if err != nil {
		t.Fatalf("in-flight call refused after a second config change: %v", err)
	}
	if got := again["call"].(*RemoteCall); got.RequestID != call.RequestID || got.InputDigest != call.InputDigest {
		t.Fatalf("call identity changed across restarts: %s/%s -> %s/%s", call.RequestID, call.InputDigest, got.RequestID, got.InputDigest)
	}
	if again["action"] == "send" {
		t.Fatal("restart turned an existing intent into a new send")
	}
}
