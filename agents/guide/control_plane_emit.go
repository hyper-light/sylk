package guide

import (
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// ControlPlaneEmission is the atomic unit for internal-coordination
// traffic. A single emission publishes both:
//
//  1. The guide.RouteRequest that carries the coordination payload
//     (e.g. a command-approval-hold begin, a plan-handoff status
//     lookup). The route bears `control_plane_kind` on its metadata;
//     the UI stream bridge reads that flag to suppress the errant
//     top-level agent entry the stream would otherwise produce.
//
//  2. A SourceSystem ActivityEvent with EventTypeSystemCoordination
//     that describes what just happened in plain English. The chat
//     model renders it as a system row alongside agent turns, giving
//     the user visible feedback ("DAG 05b288e6 paused · awaiting
//     approval") without promoting the orchestrator's transient
//     routing waypoint to a fake conversational turn.
//
// The two emissions are bundled so senders can't accidentally emit one
// without the other. That coupling is what upholds the invariant:
//
//	Every control-plane route has a paired system_coordination
//	activity event that tells the user what's happening.
//
// Callers that need the RouteRequest back (e.g. to correlate a sync
// reply) retrieve it from the returned ControlPlaneEmission.
type ControlPlaneEmission struct {
	Kind          string                // e.g. "command_approval_hold"
	Summary       string                // Human-readable one-liner for the system row.
	SessionID     string                // Empty routes are rejected; must be set.
	CorrelationID string                // Optional; lets the UI collapse begin/resolve pairs.
	Related       map[string]any        // Optional: dag_id, pipeline_id, task_id, plan_id — surfaced on the event's Data map.
	Route         *RouteRequest         // The underlying route to publish. Metadata is stamped automatically.
	EventOverride *events.ActivityEvent // Optional: fully-formed event overrides the auto-generated one.
}

// EmitControlPlaneCoordination publishes the route and the paired
// system_coordination activity event atomically. Both are emitted on
// success; on any emission error the caller gets the error and may
// assume nothing was published (the route is attempted first; if it
// fails the activity event is not published).
//
// This is the ONLY sanctioned entry point for control-plane routes.
// Call sites that previously hand-constructed a RouteRequest with
// Metadata["control_plane_kind"] = ... are required to funnel through
// this helper so the paired system row is guaranteed. An AST/grep test
// enforces the invariant at lint time.
//
// Arguments:
//
//   - bus: the guide bus that accepts request messages.
//   - pub: the activity publisher that dispatches the system event.
//     May be nil — in that case the route is still published and
//     the system event is silently dropped (useful for test harnesses
//     that don't wire a publisher).
//   - emission: the coordinated route + event bundle.
func EmitControlPlaneCoordination(
	bus busPublisher,
	pub events.ActivityPublisher,
	emission ControlPlaneEmission,
) error {
	if bus == nil {
		return fmt.Errorf("control plane emit: bus is required")
	}
	kind := strings.TrimSpace(emission.Kind)
	if kind == "" {
		return fmt.Errorf("control plane emit: Kind is required")
	}
	if emission.Route == nil {
		return fmt.Errorf("control plane emit: Route is required")
	}
	if emission.Route.Metadata == nil {
		emission.Route.Metadata = map[string]any{}
	}
	// Stamp the control-plane marker. If a caller pre-set it we still
	// overwrite with `kind` to keep a single source of truth.
	emission.Route.Metadata["control_plane_kind"] = kind
	if emission.Route.Timestamp.IsZero() {
		emission.Route.Timestamp = time.Now()
	}

	if err := bus.Publish(TopicGuideRequests, NewRequestMessage("", emission.Route)); err != nil {
		return fmt.Errorf("control plane emit %q: publish route: %w", kind, err)
	}

	if pub == nil {
		return nil
	}
	evt := emission.EventOverride
	if evt == nil {
		summary := strings.TrimSpace(emission.Summary)
		if summary == "" {
			summary = fmt.Sprintf("system coordination: %s", kind)
		}
		evt = events.NewActivityEvent(
			events.EventTypeSystemCoordination,
			strings.TrimSpace(emission.SessionID),
			summary,
		)
		evt.Visibility = events.VisibilityUser
	}
	if evt.Data == nil {
		evt.Data = map[string]any{}
	}
	evt.Data["control_plane_kind"] = kind
	if cid := strings.TrimSpace(emission.CorrelationID); cid != "" {
		evt.Data["correlation_id"] = cid
	}
	for k, v := range emission.Related {
		if strings.TrimSpace(k) == "" {
			continue
		}
		evt.Data[k] = v
	}
	if err := pub.PublishActivity(evt); err != nil {
		// Route already published — report but don't rollback. The
		// coordination happens regardless of whether the UI row landed.
		return fmt.Errorf("control plane emit %q: publish activity event (route already published): %w", kind, err)
	}
	return nil
}

// busPublisher is the minimal bus interface EmitControlPlaneCoordination
// needs. Kept local (rather than reusing a package-wide Bus interface)
// so tests can inject a no-op fake without pulling the full guide bus
// surface.
type busPublisher interface {
	Publish(topic string, msg *Message) error
}
