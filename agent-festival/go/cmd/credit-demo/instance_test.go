package main

import (
	"errors"
	"testing"
)

// The instance that actually executes a remote call is reported by the
// runtime and accepted only if the call's discovery listed it as eligible
// (W05 item: "actual execution instance" was not collected before).

func agentAuth(in CallCommand, c *RemoteCall, instance string) CallCommand {
	return CallCommand{RunID: in.RunID, Step: in.Step, Token: c.CapabilityToken, ServiceID: c.Binding.AgentID,
		ServiceVersion: c.Binding.Version, ContextID: c.ContextID, InstanceID: instance}
}

func TestInstance_EligibleInstanceIsRecorded(t *testing.T) {
	s, in, _ := busFixture(t)
	c := beginFixture(t, s, in)
	if _, err := s.AgentContext(agentAuth(in, c, "fixture-1")); err != nil {
		t.Fatalf("eligible instance refused: %v", err)
	}
	s.mu.Lock()
	step := s.st.Runs[in.RunID].Steps[in.Step]
	eb := step.Call.ExecutedBy
	view := remoteView(step)
	s.mu.Unlock()
	if eb == nil || eb.InstanceID != "fixture-1" || eb.ServiceVersion != c.Binding.Version || eb.At == "" {
		t.Fatalf("executed_by: %+v", eb)
	}
	if view["executed_by"] == nil {
		t.Fatal("the graph projection does not show the executing instance")
	}
}

func TestInstance_UndiscoveredOrUnreportedInstanceIsRefusedWithoutTakingTheCall(t *testing.T) {
	s, in, _ := busFixture(t)
	c := beginFixture(t, s, in)
	for _, id := range []string{"rogue-1", ""} {
		if _, err := s.AgentContext(agentAuth(in, c, id)); !errors.Is(err, errInstance) {
			t.Fatalf("instance %q accepted: %v", id, err)
		}
		if status, code := errorCode(errInstance); status != 409 || code != "INSTANCE_NOT_ELIGIBLE" {
			t.Fatalf("typed code: %d %s", status, code)
		}
	}
	s.mu.Lock()
	started := s.st.Runs[in.RunID].Steps[in.Step].Call.AgentStartedAt
	s.mu.Unlock()
	if started != "" {
		t.Fatal("a refused instance marked the call as started")
	}
	if _, err := s.AgentContext(agentAuth(in, c, "fixture-1")); err != nil {
		t.Fatalf("the eligible instance could not take the call after a refusal: %v", err)
	}
}

func TestInstance_ModelEventFromAnotherInstanceIsRefused(t *testing.T) {
	s, in, _ := busFixture(t)
	c := beginFixture(t, s, in)
	if _, err := s.AgentContext(agentAuth(in, c, "fixture-1")); err != nil {
		t.Fatal(err)
	}
	other := agentAuth(in, c, "rogue-1")
	other.EventID, other.Model = "ev-1", "fixture-model"
	if err := s.ModelCall(other); !errors.Is(err, errInstance) {
		t.Fatalf("model event from another instance accepted: %v", err)
	}
	same := agentAuth(in, c, "fixture-1")
	same.EventID, same.Model = "ev-1", "fixture-model"
	if err := s.ModelCall(same); err != nil {
		t.Fatalf("model event from the executing instance: %v", err)
	}
}
