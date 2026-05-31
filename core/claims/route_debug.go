package claims

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	routeDebugLogger     *slog.Logger
	routeDebugLoggerOnce sync.Once

	routeFlowDebugLogger     *slog.Logger
	routeFlowDebugLoggerOnce sync.Once
)

// RouteDebugLogPath returns the temporary diagnostic log file for
// claim/delta routing. This is intentionally outside the WAL path so
// short-lived routing investigations do not contaminate durable logs.
func RouteDebugLogPath() string {
	if dir := strings.TrimSpace(os.TempDir()); dir != "" {
		return filepath.Join(dir, "sylk_claims_routing_debug.log")
	}
	return "sylk_claims_routing_debug.log"
}

// RouteFlowDebugLogPath returns the focused temporary trace for the
// Guide → claims board → delta bus → agent inbox handoff. It is kept
// separate from RouteDebugLog because the general claims log is very
// broad and quickly becomes too large to diagnose a single route.
func RouteFlowDebugLogPath() string {
	if dir := strings.TrimSpace(os.TempDir()); dir != "" {
		return filepath.Join(dir, "sylk_claims_route_flow_debug.log")
	}
	return "sylk_claims_route_flow_debug.log"
}

// RouteDebugLog returns a file-backed logger for temporary routing
// diagnostics. If the file cannot be opened, it degrades to io.Discard
// instead of perturbing the runtime path being diagnosed.
func RouteDebugLog() *slog.Logger {
	routeDebugLoggerOnce.Do(func() {
		path := RouteDebugLogPath()
		if dir := filepath.Dir(path); dir != "." {
			_ = os.MkdirAll(dir, 0755)
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			routeDebugLogger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
			return
		}
		routeDebugLogger = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
		routeDebugLogger.Info("claims_route_debug_log_opened", "path", path)
	})
	return routeDebugLogger
}

// RouteFlowDebugLog returns a focused file-backed logger for route
// handoff diagnostics. This logger is intentionally independent from
// RouteDebugLog so a live reproduction can be inspected without
// wading through unrelated bootstrap, validation, and artifact traffic.
func RouteFlowDebugLog() *slog.Logger {
	routeFlowDebugLoggerOnce.Do(func() {
		path := RouteFlowDebugLogPath()
		if dir := filepath.Dir(path); dir != "." {
			_ = os.MkdirAll(dir, 0755)
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			routeFlowDebugLogger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
			return
		}
		routeFlowDebugLogger = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
		routeFlowDebugLogger.Info("claims_route_flow_debug_log_opened", "path", path)
	})
	return routeFlowDebugLogger
}

// ShouldTraceRouteFlowDelta reports whether a delta is relevant to
// user-prompt routing and agent wakeup. It deliberately excludes the
// high-volume artifact/validation stream so the focused flow log stays
// useful during live reproductions.
func ShouldTraceRouteFlowDelta(d Delta) bool {
	if d == nil {
		return false
	}
	switch DeltaAction(d.DeltaKind()) {
	case DeltaActionClaimGenerated,
		DeltaActionClaimGenerationFailed,
		DeltaActionClaimPosted,
		DeltaActionClaimPostFailed,
		DeltaActionClaimReceived,
		DeltaActionClaimReceiptFailed,
		DeltaActionClaimProgressed,
		DeltaActionClaimProgressFailed,
		DeltaActionClaimTestamentGenerated,
		DeltaActionClaimTestamentGenerationFailed,
		DeltaActionClaimTestamentAcknowledged,
		DeltaActionClaimTestamentAcknowledgementFailed,
		DeltaActionClaimSatisfied,
		DeltaActionClaimValidationIncomplete,
		DeltaActionClaimValidationFailed,
		DeltaActionClaimValidationErrored,
		DeltaActionTestamentPosted:
		return true
	default:
		return false
	}
}

// TraceRouteFlowDelta emits a focused route-flow event for the subset
// of lifecycle deltas that can route, wake, acknowledge, or complete
// user-prompt work. It is intentionally no-op for high-volume display
// and validation noise.
func TraceRouteFlowDelta(event string, d Delta, args ...any) {
	if !ShouldTraceRouteFlowDelta(d) {
		return
	}
	RouteFlowDebugLog().Info(event, append(args, DeltaDebugArgs(d)...)...)
}

// DeltaDebugArgs returns stable slog key/value pairs for a delta. It is
// shared by publishers and subscribers so the same delta can be traced
// across board emission, bus delivery, and inbox matching.
func DeltaDebugArgs(d Delta) []any {
	if d == nil {
		return []any{"delta_nil", true}
	}
	args := []any{
		"delta_kind", d.DeltaKind(),
		"delta_key", d.DeltaKey(),
		"delta_sequence", d.DeltaSequence(),
	}
	switch delta := d.(type) {
	case CanonicalDelta:
		return append(args, canonicalDeltaDebugArgs(delta)...)
	case *CanonicalDelta:
		if delta == nil {
			return append(args, "canonical_nil", true)
		}
		return append(args, canonicalDeltaDebugArgs(*delta)...)
	default:
		return args
	}
}

func canonicalDeltaDebugArgs(delta CanonicalDelta) []any {
	args := []any{
		"delta_action", string(delta.Action),
		"session_id", delta.SessionID,
		"board_id", delta.BoardID,
		"claim_id", delta.ClaimID(),
		"testament_id", delta.TestamentID(),
		"validation_id", delta.ValidationID(),
		"claim_action", string(delta.ClaimActionType()),
		"actor", delta.Actor.RouteKey(),
	}
	if delta.Delivery != nil {
		args = append(args,
			"delivery_relationship", delta.Delivery.Relationship,
			"delivery_to", agentRefRouteKeys(delta.Delivery.To),
		)
	}
	return args
}

func agentRefRouteKeys(refs []AgentRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if key := ref.RouteKey(); key != "" {
			out = append(out, key)
		}
	}
	return out
}
