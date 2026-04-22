package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	shared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/versioning"
)

// AuditRejectionBridge subscribes to AuditMergeResultTopic and, on a
// Rejected verdict from a global audit replica (inspector or tester),
// translates the result into a RemediationRequest and hands it to the
// architect's shared processRemediationRequest path. This closes the
// §3.8 rejection-→-remediation loop without routing through the
// orchestrator or the control-plane forwarding layer.
//
// The bridge is owned by the architect (it holds the
// processRemediationRequest receiver) and started/stopped in lockstep
// with the architect's Start/Stop lifecycle.
//
// Idempotency: the bridge tracks per-(session, merged_version)
// dispatch state. The coordinator emits one result per replica per
// merge. For AND-accept semantics the coordinator's finalizeDecision
// only publishes after finalization; in the first-rejection-wins path
// a second rejection for the same merge would be redundant.
// Deduplication here is a safety net for repeat deliveries from
// lossy transports — the bridge skips any (session, merged_version)
// it has already submitted.
type AuditRejectionBridge struct {
	bus       guide.EventBus
	logger    *slog.Logger
	submit    auditRejectionSubmitter
	sessionID string
	sub       guide.Subscription

	mu         sync.Mutex
	dispatched map[auditRejectionKey]struct{}
}

// auditRejectionSubmitter is the shared.RemediationRequest-consuming
// interface the bridge delegates to. Defined as a small interface so
// the architect's processRemediationRequest method can be bound
// directly and unit tests can supply a recorder double.
type auditRejectionSubmitter interface {
	processRemediationRequest(ctx context.Context, req *shared.RemediationRequest) (*shared.RemediationResult, error)
}

type auditRejectionKey struct {
	sessionID     string
	mergedVersion versioning.SemanticVersion
}

// AuditRejectionBridgeConfig configures the bridge.
type AuditRejectionBridgeConfig struct {
	// Bus is the event bus carrying AuditMergeResultTopic publications.
	// Required.
	Bus guide.EventBus
	// SessionID constrains the bridge to a single session; results for
	// other sessions are ignored. Required when the bus is shared
	// across sessions; empty accepts any session (single-session
	// configurations).
	SessionID string
	// Submitter receives translated RemediationRequests. Required —
	// nil disables the bridge.
	Submitter auditRejectionSubmitter
	// Logger is the structured logger. Optional.
	Logger *slog.Logger
}

// NewAuditRejectionBridge constructs an unstarted bridge. Call Start
// to subscribe.
func NewAuditRejectionBridge(cfg AuditRejectionBridgeConfig) *AuditRejectionBridge {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AuditRejectionBridge{
		bus:        cfg.Bus,
		submit:     cfg.Submitter,
		sessionID:  strings.TrimSpace(cfg.SessionID),
		logger:     logger,
		dispatched: make(map[auditRejectionKey]struct{}),
	}
}

// Start subscribes to AuditMergeResultTopic. Idempotent.
func (b *AuditRejectionBridge) Start(ctx context.Context) error {
	if b == nil {
		return nil
	}
	if b.bus == nil || b.submit == nil {
		return fmt.Errorf("audit rejection bridge: bus and submitter required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sub != nil {
		return nil
	}
	sub, err := b.bus.SubscribeAsync(shared.AuditMergeResultTopic, func(msg *guide.Message) error {
		b.onResult(ctx, msg)
		return nil
	})
	if err != nil {
		return fmt.Errorf("audit rejection bridge: subscribe: %w", err)
	}
	b.sub = sub
	return nil
}

// Stop unsubscribes and releases resources. Idempotent.
func (b *AuditRejectionBridge) Stop() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sub == nil {
		return
	}
	_ = b.sub.Unsubscribe()
	b.sub = nil
}

// onResult decodes an AuditMergeResult from the bus message and, if
// the verdict is Rejected and the session matches, dispatches a
// RemediationRequest.
func (b *AuditRejectionBridge) onResult(ctx context.Context, msg *guide.Message) {
	if msg == nil {
		return
	}
	raw, ok := msg.Payload.([]byte)
	if !ok {
		b.logger.Warn("audit rejection bridge: payload not []byte",
			"payload_type", fmt.Sprintf("%T", msg.Payload),
		)
		return
	}
	var result shared.AuditMergeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		b.logger.Warn("audit rejection bridge: unmarshal failed",
			"error", err.Error(),
		)
		return
	}
	if result.Decision != versioning.ReplicaDecisionRejected {
		return
	}
	if b.sessionID != "" && result.SessionID != b.sessionID {
		return
	}

	key := auditRejectionKey{
		sessionID:     result.SessionID,
		mergedVersion: result.MergedVersion,
	}
	b.mu.Lock()
	if _, seen := b.dispatched[key]; seen {
		b.mu.Unlock()
		return
	}
	b.dispatched[key] = struct{}{}
	b.mu.Unlock()

	req := b.buildRemediationRequest(&result)
	b.logger.Info("audit rejection bridge: dispatching remediation",
		"session_id", req.SessionID,
		"case_id", req.CaseID,
		"remediates_version", req.RemediatesVersion.String(),
		"rejected_replica_id", req.RejectedReplicaID,
		"concerns", len(req.Findings),
	)
	if _, err := b.submit.processRemediationRequest(ctx, req); err != nil {
		b.logger.Warn("audit rejection bridge: remediation failed",
			"session_id", req.SessionID,
			"remediates_version", req.RemediatesVersion.String(),
			"error", err.Error(),
		)
	}
}

// buildRemediationRequest shapes a RemediationRequest from the
// audit-replica verdict. The CaseID is deterministic (session +
// merged_version) so repeat deliveries produce the same identifier
// for downstream case-record deduplication.
func (b *AuditRejectionBridge) buildRemediationRequest(result *shared.AuditMergeResult) *shared.RemediationRequest {
	findings := make([]shared.ValidationFinding, 0, len(result.Concerns))
	for idx, concern := range result.Concerns {
		concern = strings.TrimSpace(concern)
		if concern == "" {
			continue
		}
		findings = append(findings, shared.ValidationFinding{
			ID:       fmt.Sprintf("%s#%d", result.ReplicaID, idx),
			Kind:     auditRejectionFindingKind(result.ReplicaID),
			Severity: shared.ValidationSeverityHigh,
			Summary:  concern,
		})
	}
	return &shared.RemediationRequest{
		CaseID:             fmt.Sprintf("audit-rejection:%s:%s", result.SessionID, result.MergedVersion.String()),
		SessionID:          result.SessionID,
		Summary:            result.Summary,
		Findings:           findings,
		CreatedAt:          time.Now().UTC(),
		RemediatesVersion:  result.MergedVersion,
		RejectedReplicaID:  result.ReplicaID,
	}
}

// auditRejectionFindingKind maps the replica ID prefix to a finding
// kind so the UI + downstream tooling can distinguish inspector-led
// vs tester-led rejections in the findings list.
func auditRejectionFindingKind(replicaID string) string {
	replicaID = strings.TrimSpace(replicaID)
	switch {
	case strings.HasPrefix(replicaID, shared.AgentTypeInspectorGlobal):
		return "audit.inspector"
	case strings.HasPrefix(replicaID, shared.AgentTypeTesterGlobal):
		return "audit.tester"
	default:
		return "audit"
	}
}
