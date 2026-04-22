package shared

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/versioning"
)

// SessionStateResyncTopic is the observability topic that fires when
// a SessionVFS has opened (or reopened) and its state is ready for
// observers to read. The TUI listens so it can rebuild its audit /
// commit-queue panels from the session's current state — avoiding a
// stale display after a restart (docs/PARALLEL_GLOBAL_VFS.md §11,
// stage 5 operational layer).
//
// The event carries a state *summary* only; detail queries go through
// the session directly (SessionVFS.MergeDescriptors, CommitQueue,
// ReplicaLifecycleLog accessors). This keeps the event payload
// bounded and lets observers pull the resolution they need.
const SessionStateResyncTopic = "session.state.resync"

// SessionStateResyncNotification is the payload on
// SessionStateResyncTopic.
type SessionStateResyncNotification struct {
	SessionID         string                     `json:"session_id"`
	GreenVersion      versioning.SemanticVersion `json:"green_version"`
	CommitQueueDepth  int                        `json:"commit_queue_depth"`
	BlockedCount      int                        `json:"blocked_count"`
	AuditingCount     int                        `json:"auditing_count"`
	InFlightReplicas  int                        `json:"in_flight_replicas"`
	MergeDescriptors  int                        `json:"merge_descriptors"`
	SessionOpenAt     time.Time                  `json:"session_open_at"`
	ElapsedOpenSecs   float64                    `json:"elapsed_open_seconds"`
}

// PublishSessionStateResync emits the resync notification on bus.
// Called by the session-lifecycle wiring (orchestrator or test
// harness) immediately after NewSessionVFS returns successfully. Nil
// bus / nil session are no-ops so the function is safe to call from
// deployment paths that may not have wired observability yet.
func PublishSessionStateResync(bus guide.EventBus, sess *versioning.SessionVFS) error {
	if bus == nil || sess == nil {
		return nil
	}
	notif := buildSessionStateResyncNotification(sess)
	body, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("session state resync marshal: %w", err)
	}
	msg := guide.NewBridgeMessage(notif.SessionID, "observers", body)
	return bus.Publish(SessionStateResyncTopic, msg)
}

func buildSessionStateResyncNotification(sess *versioning.SessionVFS) *SessionStateResyncNotification {
	now := time.Now().UTC()
	notif := &SessionStateResyncNotification{
		SessionID:        string(sess.SessionID()),
		GreenVersion:     sess.CurrentVersion(),
		CommitQueueDepth: sess.CommitQueueDepth(),
		MergeDescriptors: len(sess.MergeDescriptors()),
		SessionOpenAt:    now,
	}
	if queue := sess.CommitQueue(); queue != nil {
		notif.BlockedCount = queue.DepthBlocked()
		notif.AuditingCount = queue.DepthAuditing()
	}
	if lifecycle := sess.ReplicaLifecycleLog(); lifecycle != nil {
		notif.InFlightReplicas = len(lifecycle.InFlight())
	}
	if clock := sess.Clock(); clock != nil {
		notif.ElapsedOpenSecs = clock.Elapsed(now).Seconds()
	}
	return notif
}
