package claims

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	ArtifactKindBackpressure = "claims_backpressure"
	backpressureActorID      = "sys:claims_backpressure"
)

type BackpressureEventKind string

const (
	BackpressureNearCapacity BackpressureEventKind = "near_capacity"
	BackpressureOverflow     BackpressureEventKind = "overflow"
)

type BackpressureEvent struct {
	Kind          BackpressureEventKind `json:"kind"`
	QueueName     string                `json:"queue_name"`
	ParticipantID string                `json:"participant_id,omitempty"`
	SessionID     string                `json:"session_id,omitempty"`
	InboxClass    string                `json:"inbox_class,omitempty"`
	Capacity      int                   `json:"capacity,omitempty"`
	Depth         int                   `json:"depth,omitempty"`
	Dropped       uint64                `json:"dropped,omitempty"`
	ObservedAt    time.Time             `json:"observed_at"`
}

type BackpressureSnapshot struct {
	Events          []BackpressureEvent `json:"events,omitempty"`
	OverflowByClass map[string]uint64   `json:"overflow_by_class,omitempty"`
	TotalOverflow   uint64              `json:"total_overflow,omitempty"`
	Truncated       uint64              `json:"truncated,omitempty"`
}

type BackpressureReporter struct {
	mu              sync.Mutex
	limit           int
	events          []BackpressureEvent
	truncated       uint64
	overflowByClass [numInboxClasses]uint64
}

func NewBackpressureReporter(cfg ClaimsOperationsConfig) *BackpressureReporter {
	ops := NormalizeClaimsOperationsConfig(cfg)
	return &BackpressureReporter{limit: ops.Backpressure.SummaryLimit}
}

func (r *BackpressureReporter) Record(event BackpressureEvent) {
	if r == nil {
		return
	}
	event.QueueName = strings.TrimSpace(event.QueueName)
	event.ParticipantID = strings.TrimSpace(event.ParticipantID)
	event.SessionID = strings.TrimSpace(event.SessionID)
	event.InboxClass = strings.TrimSpace(event.InboxClass)
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if class, ok := parseInboxClass(event.InboxClass); ok && event.Kind == BackpressureOverflow {
		r.overflowByClass[class] += event.Dropped
	}
	if len(r.events) < r.limit {
		r.events = append(r.events, event)
		return
	}
	copy(r.events, r.events[1:])
	r.events[len(r.events)-1] = event
	r.truncated++
}

func (r *BackpressureReporter) Snapshot() BackpressureSnapshot {
	if r == nil {
		return BackpressureSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := BackpressureSnapshot{
		Events:          append([]BackpressureEvent(nil), r.events...),
		OverflowByClass: make(map[string]uint64, numInboxClasses),
		Truncated:       r.truncated,
	}
	for idx, count := range r.overflowByClass {
		if count == 0 {
			continue
		}
		class := InboxClass(idx).String()
		out.OverflowByClass[class] = count
		out.TotalOverflow += count
	}
	return out
}

func ReportInboxBackpressure(ctx context.Context, board *ClaimsBoard, inbox *ClaimsInbox, reporter *BackpressureReporter) (BackpressureSnapshot, error) {
	if inbox == nil {
		return BackpressureSnapshot{}, nil
	}
	event := BackpressureEvent{
		Kind:          BackpressureOverflow,
		QueueName:     "claims_inbox",
		ParticipantID: inbox.AgentID(),
		SessionID:     inbox.SessionID(),
		InboxClass:    InboxClassObservation.String(),
		Capacity:      inbox.queueCap,
		Dropped:       inbox.OverflowCount(),
		ObservedAt:    time.Now().UTC(),
	}
	if event.Dropped == 0 {
		return reporter.Snapshot(), nil
	}
	reporter.Record(event)
	if board == nil {
		return reporter.Snapshot(), nil
	}
	_, err := RecordBusTransportEvidence(ctx, board, "overflow", inbox.SessionID(), map[string]any{
		"queue_name":     event.QueueName,
		"participant_id": event.ParticipantID,
		"inbox_class":    event.InboxClass,
		"dropped":        event.Dropped,
		"capacity":       event.Capacity,
	})
	return reporter.Snapshot(), err
}

func parseInboxClass(value string) (InboxClass, bool) {
	for idx := range numInboxClasses {
		class := InboxClass(idx)
		if class.String() == strings.TrimSpace(value) {
			return class, true
		}
	}
	return InboxClassObservation, false
}

func (event BackpressureEvent) String() string {
	return fmt.Sprintf("%s queue=%s participant=%s session=%s class=%s dropped=%d", event.Kind, event.QueueName, event.ParticipantID, event.SessionID, event.InboxClass, event.Dropped)
}
