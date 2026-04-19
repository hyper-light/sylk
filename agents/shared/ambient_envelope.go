package shared

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/activity/lenses"
)

// AmbientEnvelopeConfig describes the per-agent context the
// AppendAmbientContext helper needs to compute and render the
// fabric awareness envelope. Agents construct one of these once
// (ideally lazy via getter functions) and pass it into every tool-
// result wrap.
type AmbientEnvelopeConfig struct {
	SessionID  func() string
	AgentID    func() string
	AgentType  func() string
	PipelineID func() string

	// ScopeHint is consulted at envelope time to pick a workspace
	// scope for the ambient query. Typically the agent's last
	// touched file or task scope; nil-safe defaults to empty (which
	// returns the broadest envelope filtered only by the agent's
	// own inbound queue).
	ScopeHint func() string
}

// AppendAmbientContext renders a bounded fabric ambient_context
// envelope for the agent and appends it to the tool result string
// the LLM is about to receive. When the envelope is empty (no peer
// activity, no inbound disputes, no advisories — typical for short
// idle periods), the helper is a no-op so we don't spam empty tags.
//
// This is the centerpiece of the "every tool result carries ambient
// awareness" promise made in the agent prompts. Without it, the
// awareness model is dead — agents are told context will appear and
// it never does, so they default to older direct skills.
//
// The envelope is bounded by the lens's per-target/per-initiator
// caps; a typical envelope is < 1KB. When fabric isn't yet wired
// (DefaultSource returns nil), this returns the result unchanged.
//
// To prevent runaway cost: the envelope is computed at most once
// every minEnvelopeInterval per (agentID, batchKey) tuple. Most
// LLM turns include several tool calls; without rate-limiting,
// ambient computation would dominate.
func AppendAmbientContext(ctx context.Context, cfg AmbientEnvelopeConfig, result string) string {
	if cfg.SessionID == nil || cfg.AgentID == nil || cfg.AgentType == nil {
		return result
	}
	src := activity.DefaultSource()
	if src == nil {
		return result
	}
	agentID := strings.TrimSpace(cfg.AgentID())
	if agentID == "" {
		return result
	}
	if !ambientRateLimit(agentID) {
		return result
	}

	scope := ""
	if cfg.ScopeHint != nil {
		scope = strings.TrimSpace(cfg.ScopeHint())
	}

	envelope, err := lenses.AmbientFor(ctx, src, lenses.AmbientQuery{
		SessionID: activity.SessionID(strings.TrimSpace(cfg.SessionID())),
		AgentID:   agentID,
		AgentType: strings.TrimSpace(cfg.AgentType()),
		Scope:     scope,
	})
	if err != nil {
		return result
	}
	rendered := envelope.Render()
	if rendered == "" {
		return result
	}
	if strings.TrimSpace(result) == "" {
		return rendered
	}
	return result + "\n\n" + rendered
}

// ─── Rate limiting ────────────────────────────────────────────────────

// minEnvelopeInterval bounds how often the ambient envelope is
// computed per agent. Without this guard, an agent making N tool
// calls per turn would re-compute the envelope N times.
//
// 5 seconds is short enough that the envelope feels "live" but long
// enough that bursty tool loops don't dominate cost. Tunable per-
// agent later if needed.
const minEnvelopeInterval = 5 * time.Second

var ambientLastComputed atomic.Pointer[ambientRateMap]

type ambientRateMap struct {
	last map[string]time.Time
}

// ambientRateLimit returns true when this agent should compute a
// fresh ambient envelope. False means the prior envelope was
// computed too recently and we should skip (returning the result
// unchanged).
//
// Implementation: a process-wide map keyed by agentID. Atomic
// pointer swap means the map is replaced wholesale (rare) rather
// than locked per access (common path). The locking is implicit in
// the per-agent timestamp comparison.
func ambientRateLimit(agentID string) bool {
	now := time.Now()
	for {
		current := ambientLastComputed.Load()
		var nextMap *ambientRateMap
		if current == nil {
			nextMap = &ambientRateMap{
				last: map[string]time.Time{agentID: now},
			}
		} else {
			if last, ok := current.last[agentID]; ok && now.Sub(last) < minEnvelopeInterval {
				return false
			}
			nextMap = &ambientRateMap{
				last: cloneRateMap(current.last),
			}
			nextMap.last[agentID] = now
		}
		if ambientLastComputed.CompareAndSwap(current, nextMap) {
			return true
		}
	}
}

func cloneRateMap(m map[string]time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ResetAmbientRateLimitForTesting drops the rate-limit state. Test
// helpers call this between cases so the per-agent throttle doesn't
// leak between tests in the same package.
func ResetAmbientRateLimitForTesting() {
	ambientLastComputed.Store(nil)
}
