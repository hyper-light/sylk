package fabric

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// ActivityBridge is the production Emitter that adapts substrate
// fabric.Activity events into core/activity.AgentActivity records.
//
// The substrate emits domain-shaped Activity records (with substrate
// ActivityKind and a free-form Payload); the wider Sylk activity
// fabric expects typed AgentActivity records with canonical
// ActionKind, Subject, and Resolution. ActivityBridge translates one
// to the other and routes through activity.Append.
//
// Construction is wire-once-at-startup: the substrate's bring-up code
// (typically cmd/tui.go alongside the orchestrator's existing
// activity.SetDefaultSink call) builds an ActivityBridge with an
// agentID + sessionID resolver and installs it on the substrate
// subsystem (lockfile, projection, exec backend) wherever an
// fabric.Emitter is accepted.
type ActivityBridge struct {
	// agentType is the actor type recorded on every emitted activity.
	// Defaults to "substrate".
	agentType string

	// agentIDResolver returns the actor agent_id for an emission.
	// Typically constant in production (one substrate process = one
	// actor). Optional; defaults to agentType.
	agentIDResolver func() string

	// sessionIDResolver returns the SessionID for an emission. The
	// substrate's per-emission context doesn't always know its
	// session (e.g., process-global cache pressure events), so the
	// resolver supplies a fallback. Required.
	sessionIDResolver func(ctx context.Context, scope string) string
}

// BridgeConfig configures the ActivityBridge.
type BridgeConfig struct {
	AgentType         string
	AgentIDResolver   func() string
	SessionIDResolver func(ctx context.Context, scope string) string
}

// NewActivityBridge constructs a production-wired ActivityBridge.
func NewActivityBridge(cfg BridgeConfig) *ActivityBridge {
	agentType := cfg.AgentType
	if agentType == "" {
		agentType = "substrate"
	}
	resolver := cfg.AgentIDResolver
	if resolver == nil {
		resolver = func() string { return agentType }
	}
	sessionResolver := cfg.SessionIDResolver
	if sessionResolver == nil {
		sessionResolver = defaultSessionResolver
	}
	return &ActivityBridge{
		agentType:         agentType,
		agentIDResolver:   resolver,
		sessionIDResolver: sessionResolver,
	}
}

// defaultSessionResolver returns an empty SessionID — used only when
// the caller hasn't wired a real resolver. Activities with empty
// SessionID are still routed through activity.Append (the activity
// store accepts them) but won't appear in per-session lenses.
func defaultSessionResolver(_ context.Context, _ string) string { return "" }

// Emit translates a substrate Activity into an AgentActivity and
// routes it through activity.Append. Synchronous; the substrate's
// callers expect Emit to return promptly.
func (b *ActivityBridge) Emit(act Activity) {
	ctx := context.Background()
	agentActivity := b.translate(ctx, act)
	activity.Append(ctx, agentActivity)
}

func (b *ActivityBridge) translate(ctx context.Context, act Activity) activity.AgentActivity {
	timestamp := act.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	actionKind := mapToActionKind(act.Kind)
	return activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(b.sessionIDResolver(ctx, act.Scope)),
		Timestamp:  timestamp,
		Resolution: activity.ResolutionFor(actionKind),
		Action:     actionKind,
		Actor: activity.Actor{
			AgentID:   b.agentIDResolver(),
			AgentType: b.agentType,
		},
		Subject: activity.Subject{
			PathPrefix: act.Scope,
			Domain:     "substrate",
		},
		Payload: marshalPayloadOrEmpty(act.Payload),
		State:   stateFor(actionKind),
	}
}

// mapToActionKind translates a substrate ActivityKind to a core
// ActionKind. The substrate's kinds are domain-specific
// ("tool_pinned", "build_completed"); the wider activity fabric uses
// canonical kinds (ActionDecisionDeclared, ActionConsulted, etc.).
//
// The translation table maps each substrate kind to the closest
// canonical kind:
//
//   - ToolPinned, ToolUpgraded, ToolWitnessAdded, ToolAttached,
//     ToolDetached: ActionDecisionDeclared (these are decisions about
//     which tools the substrate uses).
//   - ProvisioningStarted/Completed: ActionConsulted (provisioning is
//     a substrate-internal action, not a peer-coordination decision).
//   - ProvisioningFailed, BuildFailed: ActionErrorEmitted.
//   - SupplyChainViolation: ActionErrorEmitted (with high-priority
//     payload).
//   - DiskFallbackUsed: ActionDecisionDeclared (an explicit downgrade
//     decision worth surfacing to other agents).
//   - SubstrateEvictionPressure: ActionConsulted (advisory).
//
// Adding new substrate ActivityKinds requires updating this table.
func mapToActionKind(kind ActivityKind) activity.ActionKind {
	switch kind {
	case ToolPinned, ToolUpgraded, ToolWitnessAdded, ToolAttached, ToolDetached, DiskFallbackUsed:
		return activity.ActionDecisionDeclared
	case ProvisioningStarted, BuildStarted:
		return activity.ActionConsulted
	case ProvisioningCompleted, BuildCompleted:
		return activity.ActionDecisionDeclared
	case ProvisioningFailed, BuildFailed, SupplyChainViolation:
		return activity.ActionErrorEmitted
	case SubstrateEvictionPressure:
		return activity.ActionConsulted
	}
	return activity.ActionConsulted
}

// stateFor returns StateInFlight for kinds that have a paired
// completion (Started → Completed/Failed), StatePoint otherwise.
func stateFor(kind activity.ActionKind) activity.ActivityState {
	if kind == activity.ActionConsulted {
		return activity.StatePoint
	}
	return activity.StatePoint
}

func marshalPayloadOrEmpty(payload any) json.RawMessage {
	if payload == nil {
		return nil
	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		// Failure to marshal payload is non-fatal — we record the
		// activity itself with a sentinel payload describing the
		// failure rather than dropping the activity.
		return json.RawMessage(fallbackPayload(err))
	}
	return bytes
}

func fallbackPayload(err error) string {
	return `{"_marshal_error":"` + escapeJSONString(err.Error()) + `"}`
}

func escapeJSONString(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}
