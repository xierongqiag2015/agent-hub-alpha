package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestD1W05Samples regenerates the W05 handoff samples in
// agent-festival/docs/d1-handoff/w05 from the real code paths (busFixture,
// BeginCall, UpdateCall, AgentContext/ModelCall, CommitCall, graph). It only
// runs when D1_SAMPLES_DIR is set; scoped credentials are redacted.
func TestD1W05Samples(t *testing.T) {
	dir := os.Getenv("D1_SAMPLES_DIR")
	if dir == "" {
		t.Skip("set D1_SAMPLES_DIR to regenerate the D1 handoff samples")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dump := func(name string, v any) {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	redact := func(c *RemoteCall) *RemoteCall {
		c = copyCall(c)
		if c.CapabilityToken != "" {
			c.CapabilityToken = "<redacted: node-scoped credential, never projected>"
		}
		return c
	}

	// 1. normal path: bound card -> send intent -> accepted -> model use -> committed result
	s, in, _ := busFixture(t)
	p := s.st.Plans["P1"][0]
	p.Steps = []Step{{ID: "report", Member: "report"}}
	dump("01-capability-binding.json", map[string]any{
		"node":         "report",
		"expert":       "report-writer",
		"rdc_agent_id": busAgents["report-writer"],
		"binding":      p.BusBindings["report"],
		"note":         "card_digest/contract_digest are fixed when the plan is proposed and re-checked against RDC before every send",
	})
	c := beginFixture(t, s, in)
	dump("02-send-intent.json", map[string]any{"begin_call": in, "call": redact(c),
		"note": "the intent (request/message/context ids, input digest, discovery snapshot) is persisted before anything is sent"})
	if err := s.UpdateCall(receiptCommand(in, c)); err != nil {
		t.Fatal(err)
	}
	accepted := s.st.Runs["R1"].Steps["report"].Call
	dump("03-accepted.json", map[string]any{"update_call": receiptCommand(in, c), "call": redact(accepted), "gateway_view": gatewayView(accepted)})
	auth := CallCommand{RunID: in.RunID, Step: in.Step, Token: c.CapabilityToken, ServiceID: c.Binding.AgentID, ServiceVersion: c.Binding.Version, ContextID: c.ContextID, EventID: "model-1"}
	if _, err := s.AgentContext(auth); err != nil {
		t.Fatal(err)
	}
	if err := s.ModelCall(auth); err != nil {
		t.Fatal(err)
	}
	commit := callIdentity(in, c)
	commit.Output = "structured report artifact (fixture)"
	if _, err := s.CommitCall(commit); err != nil {
		t.Fatal(err)
	}
	done := s.st.Runs["R1"].Steps["report"]
	commit.Token = "<executor lease token>"
	dump("04-committed-result.json", map[string]any{"commit_call": commit, "call": redact(done.Call), "step_status": done.Status, "gateway_view": gatewayView(done.Call)})
	g, err := s.graph(p, false)
	if err != nil {
		t.Fatal(err)
	}
	dump("05-graph-projection.json", g)

	// 2. reply lost: the remote task is known, the outcome is not -> query, never resend
	s2, in2, _ := busFixture(t)
	c2 := beginFixture(t, s2, in2)
	u := receiptCommand(in2, c2)
	if err := s2.UpdateCall(u); err != nil {
		t.Fatal(err)
	}
	u.Sequence, u.Status = 2, "unknown"
	u.Gateway = &GatewayExchange{RPCID: "query-1", Method: "GetTask", StartedAt: "2026-11-11T01:00:02Z", FinishedAt: "2026-11-11T01:00:03Z", HTTPStatus: 200, Outcome: "protocol_error", ErrorCode: "TASK_NOT_FOUND"}
	if err := s2.UpdateCall(u); err != nil {
		t.Fatal(err)
	}
	retry, err := s2.BeginCall(in2)
	if err != nil {
		t.Fatal(err)
	}
	unknown := s2.st.Runs["R1"].Steps["report"].Call
	dump("06-unknown-then-retry.json", map[string]any{"retry_begin_call_action": retry["action"], "call": redact(unknown), "gateway_view": gatewayView(unknown),
		"note": "a retry of the same node returns the original call with action=query; no second SendMessage is issued"})
}
