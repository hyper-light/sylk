package claims

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type ClaimsMetricKind string

const (
	ClaimsMetricCounter   ClaimsMetricKind = "counter"
	ClaimsMetricGauge     ClaimsMetricKind = "gauge"
	ClaimsMetricHistogram ClaimsMetricKind = "histogram"
)

type ClaimsMetricDefinition struct {
	Name   string            `json:"name"`
	Kind   ClaimsMetricKind  `json:"kind"`
	Labels []string          `json:"labels,omitempty"`
	MapsTo string            `json:"maps_to,omitempty"`
	Meta   map[string]string `json:"meta,omitempty"`
}

type ClaimsMetric struct {
	Name       string            `json:"name"`
	Kind       ClaimsMetricKind  `json:"kind"`
	Value      float64           `json:"value"`
	Labels     map[string]string `json:"labels,omitempty"`
	ObservedAt time.Time         `json:"observed_at"`
}

//go:generate mockery --name=ClaimsMetricsSink --output=./mocks --outpkg=mocks
type ClaimsMetricsSink interface {
	ObserveClaimsMetric(ctx context.Context, metric ClaimsMetric) error
}

type NoopClaimsMetricsSink struct{}

func (NoopClaimsMetricsSink) ObserveClaimsMetric(context.Context, ClaimsMetric) error { return nil }

type InMemoryClaimsMetricsSink struct {
	mu      sync.Mutex
	limit   int
	records []ClaimsMetric
	dropped uint64
}

type ClaimsMetricsSinkSnapshot struct {
	Records []ClaimsMetric `json:"records,omitempty"`
	Dropped uint64         `json:"dropped,omitempty"`
}

const defaultInMemoryMetricLimit = 4096

func NewInMemoryClaimsMetricsSink(limit int) *InMemoryClaimsMetricsSink {
	if limit <= 0 {
		limit = defaultInMemoryMetricLimit
	}
	return &InMemoryClaimsMetricsSink{limit: limit}
}

func (s *InMemoryClaimsMetricsSink) ObserveClaimsMetric(_ context.Context, metric ClaimsMetric) error {
	if s == nil {
		return nil
	}
	metric = normalizeClaimsMetric(metric)
	if metric.Name == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.records) >= s.limit {
		copy(s.records, s.records[1:])
		s.records[len(s.records)-1] = metric
		s.dropped++
		return nil
	}
	s.records = append(s.records, metric)
	return nil
}

func (s *InMemoryClaimsMetricsSink) Snapshot() ClaimsMetricsSinkSnapshot {
	if s == nil {
		return ClaimsMetricsSinkSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return ClaimsMetricsSinkSnapshot{
		Records: append([]ClaimsMetric(nil), s.records...),
		Dropped: s.dropped,
	}
}

func ClaimsMetricCatalog() []ClaimsMetricDefinition {
	out := append([]ClaimsMetricDefinition(nil), claimsMetricCatalog...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func ValidateClaimsMetricCatalog(catalog []ClaimsMetricDefinition) error {
	seen := make(map[string]struct{}, len(catalog))
	for _, def := range catalog {
		name := strings.TrimSpace(def.Name)
		if name == "" || !def.Kind.valid() {
			return fmt.Errorf("invalid claims metric definition: %#v", def)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate claims metric definition %q", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func (k ClaimsMetricKind) valid() bool {
	return k == ClaimsMetricCounter || k == ClaimsMetricGauge || k == ClaimsMetricHistogram
}

func recordClaimsCounter(ctx context.Context, sink ClaimsMetricsSink, name string, labels map[string]string) {
	observeClaimsMetric(ctx, sink, ClaimsMetric{Name: name, Kind: ClaimsMetricCounter, Value: 1, Labels: labels})
}

func recordClaimsGauge(ctx context.Context, sink ClaimsMetricsSink, name string, value float64, labels map[string]string) {
	observeClaimsMetric(ctx, sink, ClaimsMetric{Name: name, Kind: ClaimsMetricGauge, Value: value, Labels: labels})
}

func recordClaimsHistogram(ctx context.Context, sink ClaimsMetricsSink, name string, value float64, labels map[string]string) {
	observeClaimsMetric(ctx, sink, ClaimsMetric{Name: name, Kind: ClaimsMetricHistogram, Value: value, Labels: labels})
}

func RecordClaimsBootPhaseMetric(ctx context.Context, sink ClaimsMetricsSink, phase, outcome string, duration time.Duration) {
	recordClaimsCounter(ctx, sink, "claims_boot_phase_completed_total", metricLabels("phase", phase, "outcome", outcome))
	recordClaimsHistogram(ctx, sink, "claims_boot_phase_duration_seconds", duration.Seconds(), metricLabels("phase", phase))
}

func observeClaimsMetric(ctx context.Context, sink ClaimsMetricsSink, metric ClaimsMetric) {
	sink = normalizeClaimsMetricsSink(sink)
	if ctx == nil {
		ctx = context.Background()
	}
	metric = normalizeClaimsMetric(metric)
	if metric.Name == "" {
		return
	}
	_ = sink.ObserveClaimsMetric(ctx, metric)
}

func normalizeClaimsMetricsSink(sink ClaimsMetricsSink) ClaimsMetricsSink {
	if sink == nil {
		return NoopClaimsMetricsSink{}
	}
	return sink
}

func normalizeClaimsMetric(metric ClaimsMetric) ClaimsMetric {
	metric.Name = strings.TrimSpace(metric.Name)
	if !metric.Kind.valid() {
		metric.Kind = claimsMetricKindForName(metric.Name)
	}
	if metric.Value == 0 && metric.Kind == ClaimsMetricCounter {
		metric.Value = 1
	}
	if metric.ObservedAt.IsZero() {
		metric.ObservedAt = time.Now().UTC()
	} else {
		metric.ObservedAt = metric.ObservedAt.UTC()
	}
	metric.Labels = boundedMetricLabels(metric.Labels)
	return metric
}

func claimsMetricKindForName(name string) ClaimsMetricKind {
	for _, def := range claimsMetricCatalog {
		if def.Name == name {
			return def.Kind
		}
	}
	return ClaimsMetricCounter
}

func boundedMetricLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		key = boundedMetricLabel(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		out[key] = boundedMetricLabelValue(value)
	}
	return out
}

func boundedMetricLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.ToLower(raw)
	raw = strings.NewReplacer(" ", "_", "-", "_", ".", "_", "/", "_", ":", "_").Replace(raw)
	return boundedString(raw, 64)
}

func boundedMetricLabelValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "unknown"
	}
	raw = strings.ToLower(raw)
	raw = strings.NewReplacer(" ", "_", "/", "_", "\n", "_", "\t", "_").Replace(raw)
	return boundedString(raw, 96)
}

func boundedString(raw string, limit int) string {
	if limit <= 0 || len(raw) <= limit {
		return raw
	}
	return raw[:limit]
}

func metricLabels(values ...string) map[string]string {
	out := make(map[string]string, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		key := boundedMetricLabel(values[i])
		if key == "" {
			continue
		}
		out[key] = boundedMetricLabelValue(values[i+1])
	}
	return out
}

func deltaMetricAction(delta Delta) string {
	if delta == nil {
		return "unknown"
	}
	return delta.DeltaKind()
}

func deltaMetricParticipantCategory(delta Delta) string {
	switch d := delta.(type) {
	case CanonicalDelta:
		return canonicalDeltaMetricParticipantCategory(d)
	case *CanonicalDelta:
		if d != nil {
			return canonicalDeltaMetricParticipantCategory(*d)
		}
	}
	return "unknown"
}

func canonicalDeltaMetricParticipantCategory(delta CanonicalDelta) string {
	if delta.Delivery != nil && len(delta.Delivery.To) > 0 {
		if category := strings.TrimSpace(delta.Delivery.To[0].Category); category != "" {
			return category
		}
	}
	if category := strings.TrimSpace(delta.Actor.Category); category != "" {
		return category
	}
	return "unknown"
}

func errMetricReason(err error) string {
	if err == nil {
		return "none"
	}
	unwrapped := errors.Unwrap(err)
	if unwrapped != nil {
		err = unwrapped
	}
	return boundedMetricLabelValue(err.Error())
}

var claimsMetricCatalog = []ClaimsMetricDefinition{
	{Name: "claims_board_transitions_total", Kind: ClaimsMetricCounter, Labels: []string{"entity_type", "action"}},
	{Name: "claims_board_transition_duration_seconds", Kind: ClaimsMetricHistogram, Labels: []string{"entity_type", "action"}},
	{Name: "claims_board_wal_write_total", Kind: ClaimsMetricCounter, Labels: []string{"result"}},
	{Name: "claims_board_wal_write_duration_seconds", Kind: ClaimsMetricHistogram},
	{Name: "claims_board_wal_replay_entries_total", Kind: ClaimsMetricCounter},
	{Name: "claims_board_wal_replay_duration_seconds", Kind: ClaimsMetricHistogram},
	{Name: "claims_delta_emitted_total", Kind: ClaimsMetricCounter, Labels: []string{"action", "participant_category"}},
	{Name: "claims_delta_emission_duration_seconds", Kind: ClaimsMetricHistogram},
	{Name: "claims_delta_publish_failure_total", Kind: ClaimsMetricCounter, Labels: []string{"reason"}},
	{Name: "claims_delta_subscriber_overflow_total", Kind: ClaimsMetricCounter, Labels: []string{"topic"}},
	{Name: "claims_claim_generated_total", Kind: ClaimsMetricCounter, Labels: []string{"action_type", "issuer_category"}},
	{Name: "claims_claim_posted_total", Kind: ClaimsMetricCounter, Labels: []string{"action_type", "target_category"}},
	{Name: "claims_claim_received_total", Kind: ClaimsMetricCounter, Labels: []string{"target_category"}},
	{Name: "claims_claim_satisfied_total", Kind: ClaimsMetricCounter, Labels: []string{"action_type"}},
	{Name: "claims_claim_validation_failed_total", Kind: ClaimsMetricCounter, Labels: []string{"action_type", "error_category"}},
	{Name: "claims_claim_validation_incomplete_total", Kind: ClaimsMetricCounter, Labels: []string{"action_type"}},
	{Name: "claims_claim_validation_errored_total", Kind: ClaimsMetricCounter, Labels: []string{"action_type", "error_category"}},
	{Name: "claims_claim_duration_seconds", Kind: ClaimsMetricHistogram, Labels: []string{"action_type", "outcome"}},
	{Name: "claims_artifact_generated_total", Kind: ClaimsMetricCounter, Labels: []string{"participant_category", "artifact_kind"}},
	{Name: "claims_artifact_received_total", Kind: ClaimsMetricCounter, Labels: []string{"claimant_category"}},
	{Name: "claims_artifact_attached_total", Kind: ClaimsMetricCounter, Labels: []string{"participant_category"}},
	{Name: "claims_artifact_validated_total", Kind: ClaimsMetricCounter, Labels: []string{"claimant_category"}},
	{Name: "claims_artifact_validation_failed_total", Kind: ClaimsMetricCounter, Labels: []string{"claimant_category", "error_category"}},
	{Name: "claims_artifact_receipt_failed_total", Kind: ClaimsMetricCounter, Labels: []string{"claimant_category", "error_category"}},
	{Name: "claims_validation_dispatched_total", Kind: ClaimsMetricCounter, Labels: []string{"validator_id"}},
	{Name: "claims_validation_validated_total", Kind: ClaimsMetricCounter, Labels: []string{"validator_id"}},
	{Name: "claims_validation_failed_total", Kind: ClaimsMetricCounter, Labels: []string{"validator_id", "required", "error_category"}},
	{Name: "claims_validation_errored_total", Kind: ClaimsMetricCounter, Labels: []string{"validator_id", "required", "error_category"}},
	{Name: "claims_validation_quality_bar_started_total", Kind: ClaimsMetricCounter, Labels: []string{"validator_id"}},
	{Name: "claims_validation_quality_bar_validated_total", Kind: ClaimsMetricCounter, Labels: []string{"validator_id"}},
	{Name: "claims_validation_quality_bar_failed_total", Kind: ClaimsMetricCounter, Labels: []string{"validator_id", "required"}},
	{Name: "claims_validation_handler_duration_seconds", Kind: ClaimsMetricHistogram, Labels: []string{"validator_id", "determinism"}},
	{Name: "claims_validation_handler_timeout_total", Kind: ClaimsMetricCounter, Labels: []string{"validator_id"}},
	{Name: "claims_validation_handler_panic_total", Kind: ClaimsMetricCounter, Labels: []string{"validator_id"}},
	{Name: "claims_dispatcher_queue_depth", Kind: ClaimsMetricGauge, Labels: []string{"queue"}},
	{Name: "claims_dispatcher_queue_near_capacity_total", Kind: ClaimsMetricCounter, Labels: []string{"queue"}},
	{Name: "claims_dispatcher_queue_overflow_total", Kind: ClaimsMetricCounter, Labels: []string{"queue"}},
	{Name: "claims_dispatcher_handler_invocations_total", Kind: ClaimsMetricCounter, Labels: []string{"participant_type", "outcome"}},
	{Name: "claims_continuation_registered_total", Kind: ClaimsMetricCounter, Labels: []string{"handler_type"}},
	{Name: "claims_continuation_resumed_total", Kind: ClaimsMetricCounter, Labels: []string{"handler_type", "outcome"}},
	{Name: "claims_continuation_pending_count", Kind: ClaimsMetricGauge, Labels: []string{"handler_type"}},
	{Name: "claims_continuation_timeout_total", Kind: ClaimsMetricCounter, Labels: []string{"handler_type"}},
	{Name: "claims_cancellation_initiated_total", Kind: ClaimsMetricCounter, Labels: []string{"source"}},
	{Name: "claims_cancellation_propagated_claims_total", Kind: ClaimsMetricCounter, Labels: []string{"source"}},
	{Name: "claims_cancellation_completion_duration_seconds", Kind: ClaimsMetricHistogram, Labels: []string{"source"}},
	{Name: "claims_cancellation_stuck_total", Kind: ClaimsMetricCounter, Labels: []string{"source"}},
	{Name: "claims_recovery_initiated_total", Kind: ClaimsMetricCounter},
	{Name: "claims_recovery_completion_duration_seconds", Kind: ClaimsMetricHistogram},
	{Name: "claims_recovery_orphan_validations_total", Kind: ClaimsMetricCounter},
	{Name: "claims_recovery_orphan_continuations_total", Kind: ClaimsMetricCounter},
	{Name: "claims_recovery_failed_total", Kind: ClaimsMetricCounter, Labels: []string{"reason"}},
	{Name: "claims_boot_phase_completed_total", Kind: ClaimsMetricCounter, Labels: []string{"phase", "outcome"}},
	{Name: "claims_boot_phase_duration_seconds", Kind: ClaimsMetricHistogram, Labels: []string{"phase"}},
	{Name: "claims_boot_phase_duration_seconds_exceeded", Kind: ClaimsMetricCounter, Labels: []string{"phase"}},
	{Name: "claims_session_count", Kind: ClaimsMetricGauge},
	{Name: "claims_active_claim_count", Kind: ClaimsMetricGauge, Labels: []string{"participant_category"}},
	{Name: "claims_pending_continuation_count", Kind: ClaimsMetricGauge},
	{Name: "claims_in_flight_handler_count", Kind: ClaimsMetricGauge, Labels: []string{"participant_type"}},
	{Name: "claims_goroutine_count", Kind: ClaimsMetricGauge, Labels: []string{"scope_type"}},
}
