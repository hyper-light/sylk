package claims

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// SessionInboxRegistry is a process-wide registry mapping
// (sessionID, agentID) tuples to the live ClaimsInbox for that agent
// in that session. Publishers consult it to read peer admission
// budgets (Inbox.ConsultBudget) before issuing a consult, so the
// calling agent's loop can see saturation as a structured event
// rather than discovering it via a downstream "tool call failed" tail.
//
// Thread-safe. Bounded by (active sessions × distinct agent kinds);
// typically a few dozen entries.
type SessionInboxRegistry struct {
	mu      sync.RWMutex
	inboxes map[string]*ClaimsInbox
}

var defaultInboxRegistry = &SessionInboxRegistry{
	inboxes: make(map[string]*ClaimsInbox),
}

var ErrSessionInboxAlreadyRegistered = fmt.Errorf("claims: session inbox already registered for this session and agent")

// DefaultSessionInboxRegistry returns the process-wide registry.
func DefaultSessionInboxRegistry() *SessionInboxRegistry { return defaultInboxRegistry }

// inboxKey returns the canonical map key for a (sessionID, agentID)
// tuple. Both must be non-empty; trimmed for tolerance.
func inboxKey(sessionID, agentID string) string {
	return strings.TrimSpace(sessionID) + "|" + strings.TrimSpace(agentID)
}

// Register adds an inbox to the registry. Last-write-wins by design:
// the same (session, agent) tuple is overwritten when an agent is
// re-wired (e.g. credential refresh). Empty IDs or nil inbox are
// programming errors and panic.
func (r *SessionInboxRegistry) Register(sessionID, agentID string, inbox *ClaimsInbox) {
	if strings.TrimSpace(sessionID) == "" {
		panic("claims.SessionInboxRegistry.Register: empty session ID")
	}
	if strings.TrimSpace(agentID) == "" {
		panic("claims.SessionInboxRegistry.Register: empty agent ID")
	}
	if inbox == nil {
		panic("claims.SessionInboxRegistry.Register: nil inbox")
	}
	r.mu.Lock()
	r.inboxes[inboxKey(sessionID, agentID)] = inbox
	r.mu.Unlock()
}

func (r *SessionInboxRegistry) RegisterWithEvidence(ctx context.Context, sessionID, agentID string, inbox *ClaimsInbox, evidence SessionRegistryEvidenceOptions) error {
	if r.Lookup(sessionID, agentID) != nil && strings.TrimSpace(evidence.Reason) == "" {
		return ErrSessionInboxAlreadyRegistered
	}
	r.Register(sessionID, agentID, inbox)
	return recordInboxRegistryEvidence(ctx, sessionID, agentID, "attach", evidence)
}

func (r *SessionInboxRegistry) ReplaceForReasonWithEvidence(ctx context.Context, sessionID, agentID string, inbox *ClaimsInbox, reason string, evidence SessionRegistryEvidenceOptions) error {
	if strings.TrimSpace(reason) == "" {
		panic("claims.SessionInboxRegistry.ReplaceForReasonWithEvidence: empty reason")
	}
	evidence.Reason = firstNonEmpty(evidence.Reason, reason)
	r.Register(sessionID, agentID, inbox)
	return recordInboxRegistryEvidence(ctx, sessionID, agentID, "recover", evidence)
}

// Lookup returns the inbox for (sessionID, agentID), or nil. Empty
// IDs return nil.
func (r *SessionInboxRegistry) Lookup(sessionID, agentID string) *ClaimsInbox {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(agentID) == "" {
		return nil
	}
	r.mu.RLock()
	inbox := r.inboxes[inboxKey(sessionID, agentID)]
	r.mu.RUnlock()
	return inbox
}

// Remove unregisters the inbox for (sessionID, agentID). Safe to call
// during inbox shutdown.
func (r *SessionInboxRegistry) Remove(sessionID, agentID string) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(agentID) == "" {
		return
	}
	r.mu.Lock()
	delete(r.inboxes, inboxKey(sessionID, agentID))
	r.mu.Unlock()
}

func (r *SessionInboxRegistry) RemoveWithEvidence(ctx context.Context, sessionID, agentID string, evidence SessionRegistryEvidenceOptions) error {
	existing := r.Lookup(sessionID, agentID)
	r.Remove(sessionID, agentID)
	if existing == nil && evidence.Board == nil {
		return nil
	}
	return recordInboxRegistryEvidence(ctx, sessionID, agentID, "detach", evidence)
}

func recordInboxRegistryEvidence(ctx context.Context, sessionID, agentID, operation string, evidence SessionRegistryEvidenceOptions) error {
	metadata := mergeMetadata(evidence.Metadata, map[string]any{"agent_id": agentID, "reason": evidence.Reason})
	_, err := RecordFabricSubscriptionEvidence(ctx, evidence.Board, operation, sessionID, metadata)
	return err
}
