package activity

import (
	"sync"
	"time"
)

// Forest-consult → outcome linkage.
//
// When an agent calls a `*_forest_consult` skill, the handler emits
// an ActionForestConsultEmitted activity and records its ID in the
// process-wide tracker below keyed on (sessionID, agentID). For a
// short window after the consult, subsequent activity emissions
// from the same (sessionID, agentID) can auto-populate their Caused
// pointer to link back to the consult — this gives the Memory
// Forest a join between "branches consulted" and "outcomes
// produced" without requiring every chokepoint to thread a context
// key through.
//
// The design tradeoffs:
//
//   - Process-wide state (not context-carried) because tool-loop
//     boundaries recreate ctx between LLM turns. The consult needs
//     to survive those boundaries to link to the subsequent
//     implementation emissions.
//   - Best-effort (not strict) because the tracker uses a bounded
//     TTL. A consult whose outcome lands after the window expires
//     simply doesn't get linked — the fabric still records the
//     consult, so manual analysis can recover the chain. We'd
//     rather miss some links than hold state indefinitely.
//   - Last-write-wins per (sessionID, agentID) because agents that
//     consult twice in quick succession are almost always refining
//     the same question; linking outcomes to the most recent
//     consult is the more useful signal.
//
// Window: 60 seconds. Longer than any realistic consult → outcome
// sequence, short enough that stale entries don't cross-link
// unrelated work.

const defaultConsultLinkTTL = 60 * time.Second

// consultLinkEntry records a pending consult → outcome link.
type consultLinkEntry struct {
	ActivityID ActivityID
	CapturedAt time.Time
}

// consultLinkKey identifies a (sessionID, agentID) pair. Keyed by
// both because an agent can participate in multiple sessions, and a
// session can have multiple agents issuing consults concurrently.
type consultLinkKey struct {
	SessionID string
	AgentID   string
}

var (
	consultLinkMu sync.RWMutex
	consultLinks  = map[consultLinkKey]consultLinkEntry{}
	consultLinkTTL = defaultConsultLinkTTL
)

// RecordForestConsult registers the most recent forest consult for
// (sessionID, agentID). Called from the `*_forest_consult` skill
// handler right after the ActionForestConsultEmitted activity is
// appended. If sessionID or agentID is empty, the consult is not
// trackable and the call is a no-op.
func RecordForestConsult(sessionID, agentID string, consultActivityID ActivityID) {
	if sessionID == "" || agentID == "" || consultActivityID == "" {
		return
	}
	key := consultLinkKey{SessionID: sessionID, AgentID: agentID}
	consultLinkMu.Lock()
	consultLinks[key] = consultLinkEntry{
		ActivityID: consultActivityID,
		CapturedAt: time.Now(),
	}
	consultLinkMu.Unlock()
}

// RecentForestConsult returns the most recent forest consult ID
// captured for (sessionID, agentID) within the TTL window, and a
// boolean indicating presence. Callers should check presence
// rather than treating an empty ID as "none" because the map stores
// only non-empty IDs.
func RecentForestConsult(sessionID, agentID string) (ActivityID, bool) {
	if sessionID == "" || agentID == "" {
		return "", false
	}
	key := consultLinkKey{SessionID: sessionID, AgentID: agentID}
	consultLinkMu.RLock()
	entry, ok := consultLinks[key]
	ttl := consultLinkTTL
	consultLinkMu.RUnlock()
	if !ok {
		return "", false
	}
	if time.Since(entry.CapturedAt) > ttl {
		// Lazy eviction on stale read. The entry might still be
		// served by a racing reader before eviction completes;
		// that's acceptable — readers after eviction see no entry.
		consultLinkMu.Lock()
		if cur, ok := consultLinks[key]; ok && cur.CapturedAt == entry.CapturedAt {
			delete(consultLinks, key)
		}
		consultLinkMu.Unlock()
		return "", false
	}
	return entry.ActivityID, true
}

// EnrichCausationFromConsult populates a.Caused with the most
// recent forest consult for (a.SessionID, a.Actor.AgentID) if a
// consult is in the tracker AND a.Caused isn't already set by a
// stronger upstream causation. Callers invoke this before passing
// an activity to Append when they want the consult → outcome
// linkage applied.
//
// The caller must not have already set a.Caused — if they have, we
// leave their causation alone (upstream span context is always
// stronger precedent than the consult tracker).
func EnrichCausationFromConsult(a *AgentActivity) {
	if a == nil {
		return
	}
	if a.Caused != nil {
		return
	}
	id, ok := RecentForestConsult(string(a.SessionID), a.Actor.AgentID)
	if !ok {
		return
	}
	caused := id
	a.Caused = &caused
}

// ClearForestConsultLinksForTesting drops the tracker. Test
// helpers call this between cases so stale entries don't cross-
// link unrelated cases.
func ClearForestConsultLinksForTesting() {
	consultLinkMu.Lock()
	consultLinks = map[consultLinkKey]consultLinkEntry{}
	consultLinkTTL = defaultConsultLinkTTL
	consultLinkMu.Unlock()
}

// SetForestConsultLinkTTLForTesting tunes the TTL for test cases
// that want to exercise eviction without waiting the full minute.
// Always paired with ClearForestConsultLinksForTesting in Cleanup.
func SetForestConsultLinkTTLForTesting(ttl time.Duration) {
	consultLinkMu.Lock()
	consultLinkTTL = ttl
	consultLinkMu.Unlock()
}
