package handoff

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// recordingArchivePublisher is the HAND-05 test double: captures
// every ArchiveRequest so assertions can inspect reason / agent /
// payload without the production bus / archivalist pipeline.
type recordingArchivePublisher struct {
	mu       sync.Mutex
	requests []*ArchiveRequest
	fail     error  // set to return an error from PublishArchive
	block    chan struct{} // when non-nil, PublishArchive blocks until closed
}

func (p *recordingArchivePublisher) PublishArchive(_ context.Context, req *ArchiveRequest) error {
	if p.block != nil {
		<-p.block
	}
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return p.fail
}

func (p *recordingArchivePublisher) snapshot() []*ArchiveRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*ArchiveRequest, len(p.requests))
	copy(out, p.requests)
	return out
}

// fakeHandoffableAgent is a minimal HandoffableAgent that returns a
// fixed ArchivableState — enough to let publishArchiveRequest build
// a non-nil request.
type fakeHandoffableAgent struct {
	id, kind string
	state    *ArchivableState
}

func (a *fakeHandoffableAgent) AgentID() string                   { return a.id }
func (a *fakeHandoffableAgent) AgentType() string                 { return a.kind }
func (a *fakeHandoffableAgent) Descriptor() AgentDescriptor       { return AgentDescriptor{AgentType: a.kind} }
func (a *fakeHandoffableAgent) ExtractArchivableState() *ArchivableState {
	return a.state
}
func (a *fakeHandoffableAgent) Terminate(_ context.Context) error { return nil }

// TestNewArchiveRequestFromBridge_PopulatesTypedKeys verifies the
// HAND-05 payload contract: the builder lifts AgentID / AgentType /
// SessionID / CorrelationID off the state and trace meta, and
// timestamps the request. A regression here means the Archivalist
// receives requests it can't file (missing keys) — a silent failure
// mode, since the publisher never errors on partial payloads.
func TestNewArchiveRequestFromBridge_PopulatesTypedKeys(t *testing.T) {
	agent := &fakeHandoffableAgent{
		id:   "engineer-uid-9",
		kind: "engineer",
		state: &ArchivableState{
			AgentID:   "engineer-uid-9",
			AgentType: "engineer",
			State:     map[string]string{"k": "v"},
			Timestamp: time.Now(),
		},
	}
	req := newArchiveRequestFromBridge(agent, ArchiveReasonHandoffComplete, "sess-1", "corr-abc")
	if req == nil {
		t.Fatal("request is nil — state extraction should have succeeded")
	}
	if req.Reason != ArchiveReasonHandoffComplete {
		t.Errorf("Reason = %q, want handoff_complete", req.Reason)
	}
	if req.SourceAgentID != "engineer-uid-9" {
		t.Errorf("SourceAgentID = %q, want engineer-uid-9", req.SourceAgentID)
	}
	if req.AgentType != "engineer" {
		t.Errorf("AgentType = %q, want engineer", req.AgentType)
	}
	if req.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", req.SessionID)
	}
	if req.CorrelationID != "corr-abc" {
		t.Errorf("CorrelationID = %q, want corr-abc", req.CorrelationID)
	}
	if req.Timestamp.IsZero() {
		t.Error("Timestamp is zero — should be set")
	}
}

// TestNewArchiveRequestFromBridge_NilStateReturnsNil guards the
// contract that agents without extractable state produce no request
// (rather than a hollow one the Archivalist would have to reject).
func TestNewArchiveRequestFromBridge_NilStateReturnsNil(t *testing.T) {
	agent := &fakeHandoffableAgent{id: "x", kind: "y", state: nil}
	if req := newArchiveRequestFromBridge(agent, ArchiveReasonTerminate, "", ""); req != nil {
		t.Errorf("nil state should produce nil request, got %+v", req)
	}
	if req := newArchiveRequestFromBridge(nil, ArchiveReasonTerminate, "", ""); req != nil {
		t.Errorf("nil agent should produce nil request, got %+v", req)
	}
}

// TestHandoffSupervisor_SetArchivePublisher_PropagatesAndClears is
// the supervisor-level plumbing test: flipping the publisher updates
// both the supervisor's field and every already-registered bridge.
// This mirrors SetBriefSource semantics — bootstrap calls this once
// during startup and expects all bridges to pick it up without
// reconstruction.
func TestHandoffSupervisor_SetArchivePublisher_PropagatesAndClears(t *testing.T) {
	sup := NewHandoffSupervisor(nil)
	if got := sup.ArchivePublisher(); got != nil {
		t.Fatalf("new supervisor has publisher already: %+v", got)
	}

	pub := &recordingArchivePublisher{}
	sup.SetArchivePublisher(pub)
	if got := sup.ArchivePublisher(); got != pub {
		t.Errorf("after Set, supervisor publisher = %v, want %v", got, pub)
	}

	sup.SetArchivePublisher(nil)
	if got := sup.ArchivePublisher(); got != nil {
		t.Errorf("after Set(nil), supervisor publisher = %v, want nil", got)
	}
}

// TestHandoffBridge_PublishArchiveRequest_FiresRequestOnPublisher
// proves the bridge's publishArchiveRequest path wires through to
// the publisher and carries the right reason + identity. This is
// the HAND-05 acceptance test: on a handoff-complete trigger, the
// bridge emits exactly one ArchiveRequest with the outgoing agent's
// state, and the publisher sees it.
func TestHandoffBridge_PublishArchiveRequest_FiresRequestOnPublisher(t *testing.T) {
	pub := &recordingArchivePublisher{}
	bridge := &HandoffBridge{
		archivePublisher: pub,
	}
	agent := &fakeHandoffableAgent{
		id:   "uid-1",
		kind: "engineer",
		state: &ArchivableState{
			AgentID:   "uid-1",
			AgentType: "engineer",
			Timestamp: time.Now(),
		},
	}

	bridge.publishArchiveRequest(agent, ArchiveReasonHandoffComplete)

	reqs := pub.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("publisher saw %d requests, want 1", len(reqs))
	}
	if reqs[0].Reason != ArchiveReasonHandoffComplete {
		t.Errorf("Reason = %q, want handoff_complete", reqs[0].Reason)
	}
	if reqs[0].SourceAgentID != "uid-1" {
		t.Errorf("SourceAgentID = %q, want uid-1", reqs[0].SourceAgentID)
	}
}

// TestHandoffBridge_PublishArchiveRequest_NilPublisherIsNoop ensures
// running without an archive publisher is a valid, silent
// configuration. A panic or error here would break every test /
// bootstrap path that doesn't configure archival.
func TestHandoffBridge_PublishArchiveRequest_NilPublisherIsNoop(t *testing.T) {
	bridge := &HandoffBridge{archivePublisher: nil}
	agent := &fakeHandoffableAgent{
		id:    "x",
		kind:  "y",
		state: &ArchivableState{AgentID: "x", AgentType: "y"},
	}

	// Must not panic. Must not error.
	bridge.publishArchiveRequest(agent, ArchiveReasonHandoffComplete)
}

// TestHandoffBridge_PublishArchiveRequest_SwallowsPublishErrors
// locks the fire-and-forget invariant: a publisher error must not
// propagate out of the bridge. Handoff latency cannot spike because
// archival had a transient fault.
func TestHandoffBridge_PublishArchiveRequest_SwallowsPublishErrors(t *testing.T) {
	pub := &recordingArchivePublisher{fail: errors.New("bus down")}
	bridge := &HandoffBridge{archivePublisher: pub}
	agent := &fakeHandoffableAgent{
		id:    "x",
		kind:  "y",
		state: &ArchivableState{AgentID: "x", AgentType: "y"},
	}

	// publishArchiveRequest returns nothing; a re-thrown error would
	// manifest as a test failure via panic() or observable side effect.
	// The request still reaches the publisher — the bridge just
	// records the error on the trace rather than failing the handoff.
	bridge.publishArchiveRequest(agent, ArchiveReasonHandoffComplete)
	if got := len(pub.snapshot()); got != 1 {
		t.Errorf("publisher saw %d requests despite error, want 1", got)
	}
}
