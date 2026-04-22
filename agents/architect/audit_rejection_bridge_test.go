package architect

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	shared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/versioning"
)

// recordingSubmitter captures the RemediationRequests the bridge
// dispatches. Stands in for the architect's processRemediationRequest
// method so the bridge's translation logic can be tested in isolation.
type recordingSubmitter struct {
	mu   sync.Mutex
	reqs []*shared.RemediationRequest
}

func (r *recordingSubmitter) processRemediationRequest(_ context.Context, req *shared.RemediationRequest) (*shared.RemediationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
	return &shared.RemediationResult{CaseID: req.CaseID}, nil
}

func (r *recordingSubmitter) calls() []*shared.RemediationRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*shared.RemediationRequest, len(r.reqs))
	copy(out, r.reqs)
	return out
}

// TestAuditRejectionBridge_TranslatesRejectionToRemediation verifies
// the core translation: a Rejected AuditMergeResult on the topic
// produces a RemediationRequest with RemediatesVersion populated to
// the rejected MergedVersion and the replica's concerns rendered as
// findings.
func TestAuditRejectionBridge_TranslatesRejectionToRemediation(t *testing.T) {
	bus := guide.NewChannelBus(guide.ChannelBusConfig{})
	defer bus.Close()

	rec := &recordingSubmitter{}
	bridge := NewAuditRejectionBridge(AuditRejectionBridgeConfig{
		Bus:       bus,
		Submitter: rec,
	})
	if err := bridge.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer bridge.Stop()

	result := &shared.AuditMergeResult{
		SessionID:     "sess-1",
		ReplicaID:     shared.AgentTypeInspectorGlobal + "#replica-sess-1:0.5",
		MergedVersion: versioning.SemanticVersion{Minor: 5},
		Decision:      versioning.ReplicaDecisionRejected,
		Summary:       "architecture violation",
		Concerns:      []string{"cross-package cycle", "missing test coverage"},
		DecidedAt:     time.Now().UTC(),
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	msg := guide.NewBridgeMessage(result.ReplicaID, "observers", body)
	if err := bus.Publish(shared.AuditMergeResultTopic, msg); err != nil {
		t.Fatal(err)
	}

	// Give the async subscriber a moment to process.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.calls()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	calls := rec.calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	req := calls[0]
	if req.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", req.SessionID)
	}
	if req.RemediatesVersion != result.MergedVersion {
		t.Errorf("RemediatesVersion = %v, want %v", req.RemediatesVersion, result.MergedVersion)
	}
	if req.RejectedReplicaID != result.ReplicaID {
		t.Errorf("RejectedReplicaID = %q", req.RejectedReplicaID)
	}
	if req.Summary != result.Summary {
		t.Errorf("Summary = %q", req.Summary)
	}
	if len(req.Findings) != 2 {
		t.Errorf("Findings = %d, want 2", len(req.Findings))
	}
	for _, f := range req.Findings {
		if f.Kind != "audit.inspector" {
			t.Errorf("Finding kind = %q, want audit.inspector", f.Kind)
		}
		if f.Severity != shared.ValidationSeverityHigh {
			t.Errorf("Finding severity = %q", f.Severity)
		}
	}
}

// TestAuditRejectionBridge_IgnoresAcceptedVerdict verifies the bridge
// only forwards Rejected verdicts — Accepted verdicts are
// pass-through (the coordinator handles accept routing directly).
func TestAuditRejectionBridge_IgnoresAcceptedVerdict(t *testing.T) {
	bus := guide.NewChannelBus(guide.ChannelBusConfig{})
	defer bus.Close()

	rec := &recordingSubmitter{}
	bridge := NewAuditRejectionBridge(AuditRejectionBridgeConfig{
		Bus:       bus,
		Submitter: rec,
	})
	if err := bridge.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer bridge.Stop()

	result := &shared.AuditMergeResult{
		SessionID:     "sess-1",
		ReplicaID:     shared.AgentTypeInspectorGlobal + "#replica-sess-1:0.5",
		MergedVersion: versioning.SemanticVersion{Minor: 5},
		Decision:      versioning.ReplicaDecisionAccepted,
		Summary:       "looks fine",
		DecidedAt:     time.Now().UTC(),
	}
	body, _ := json.Marshal(result)
	msg := guide.NewBridgeMessage(result.ReplicaID, "observers", body)
	_ = bus.Publish(shared.AuditMergeResultTopic, msg)

	time.Sleep(200 * time.Millisecond)
	if calls := rec.calls(); len(calls) != 0 {
		t.Errorf("bridge forwarded accept: %d calls", len(calls))
	}
}

// TestAuditRejectionBridge_Deduplicates verifies that a redelivered
// identical rejection does not produce two remediation requests.
func TestAuditRejectionBridge_Deduplicates(t *testing.T) {
	bus := guide.NewChannelBus(guide.ChannelBusConfig{})
	defer bus.Close()

	rec := &recordingSubmitter{}
	bridge := NewAuditRejectionBridge(AuditRejectionBridgeConfig{
		Bus:       bus,
		Submitter: rec,
	})
	if err := bridge.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()

	result := &shared.AuditMergeResult{
		SessionID:     "sess-dupe",
		ReplicaID:     shared.AgentTypeTesterGlobal + "#replica-sess-dupe:0.3",
		MergedVersion: versioning.SemanticVersion{Minor: 3},
		Decision:      versioning.ReplicaDecisionRejected,
		Summary:       "tests failed",
		Concerns:      []string{"assertion x"},
		DecidedAt:     time.Now().UTC(),
	}
	body, _ := json.Marshal(result)
	for i := 0; i < 3; i++ {
		_ = bus.Publish(shared.AuditMergeResultTopic, guide.NewBridgeMessage(result.ReplicaID, "observers", body))
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.calls()) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)

	if calls := rec.calls(); len(calls) != 1 {
		t.Errorf("duplicate publishes produced %d remediations, want 1", len(calls))
	}
}
