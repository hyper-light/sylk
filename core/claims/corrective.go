package claims

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// correctiveRateLimiter prevents flooding the board with corrective
// claims from the same (agent, tool) pair. Max 1 per 10-second window.
var correctiveRateLimiter = &correctiveRL{
	last: make(map[string]time.Time),
}

type correctiveRL struct {
	mu   sync.Mutex
	last map[string]time.Time
}

const correctiveWindow = 10 * time.Second

func (r *correctiveRL) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if t, ok := r.last[key]; ok && now.Sub(t) < correctiveWindow {
		return false
	}
	r.last[key] = now
	// Evict stale entries to prevent unbounded growth.
	if len(r.last) > 1000 {
		for k, t := range r.last {
			if now.Sub(t) > correctiveWindow {
				delete(r.last, k)
			}
		}
	}
	return true
}

// NewCorrectiveClaim builds a corrective claim directed at an agent.
// The system is the issuer; the agent is the subject expected to
// address the condition.
func NewCorrectiveClaim(agentID, title, description string, scope []ClaimScopeEntry) Claim {
	return Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  ActionTypeCorrective,
		Relations: []Relation{
			{Related: "system", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: strings.TrimSpace(agentID), RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
	}
}

// PostCorrectiveClaim posts a corrective claim to the board. Nil-safe
// — returns nil immediately when the board is unavailable. Rate-limited
// to max 1 per (agent, scope-key) per 10-second window to prevent
// flooding.
func PostCorrectiveClaim(board *ClaimsBoard, agentID, title, description string, scope []ClaimScopeEntry) error {
	if board == nil || strings.TrimSpace(agentID) == "" {
		return nil
	}
	key := strings.TrimSpace(agentID)
	if len(scope) > 0 {
		key += ":" + scope[0].Key
	}
	if !correctiveRateLimiter.allow(key) {
		return nil
	}
	action := Action{AgentID: "system", Type: ActionTypeCorrective}
	claim := NewCorrectiveClaim(agentID, title, description, scope)
	if err := board.PostAction(context.Background(), action, []Claim{claim}); err != nil {
		slog.Error("corrective_claim_post_failed",
			"agent_id", agentID, "title", title, "error", err.Error())
		board.RecordNotificationError("corrective claim: " + err.Error())
		return err
	}
	return nil
}
