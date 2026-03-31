package shared

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
)

const (
	busyResponseStatusKey            = "status"
	busyResponseRetryableKey         = "retryable"
	busyResponseBusyKey              = "busy"
	busyResponseTargetKey            = "target"
	busyResponseMessageKey           = "message"
	busyResponseActiveReplicasKey    = "active_replicas"
	busyResponseMaxReplicasKey       = "max_replicas"
	busyResponseQueuedRequestsKey    = "queued_requests"
	busyResponseMaxQueuedRequestsKey = "max_queued_requests"
	busyResponseRetryAfterMsKey      = "retry_after_ms"
)

func BusyRouteResponseData(target string, snapshot RequestReplicaPoolSnapshot, message string) map[string]any {
	return map[string]any{
		busyResponseStatusKey:            "busy",
		busyResponseBusyKey:              true,
		busyResponseRetryableKey:         true,
		busyResponseTargetKey:            strings.TrimSpace(target),
		busyResponseMessageKey:           strings.TrimSpace(message),
		busyResponseActiveReplicasKey:    snapshot.Active,
		busyResponseMaxReplicasKey:       snapshot.MaxActive,
		busyResponseQueuedRequestsKey:    snapshot.Queued,
		busyResponseMaxQueuedRequestsKey: snapshot.MaxQueued,
		busyResponseRetryAfterMsKey:      snapshot.EstimatedRetryAfter.Milliseconds(),
	}
}

func BusyRouteResponseMessage(msg *guide.Message, fallbackTarget string) (*AgentBusyError, bool) {
	if msg == nil {
		return nil, false
	}
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		return nil, false
	}
	data := CoerceMap(resp.Data)
	if len(data) == 0 {
		return nil, false
	}
	if !extractBusyFlag(data) {
		return nil, false
	}
	target := trimmedAnyString(data[busyResponseTargetKey])
	if target == "" {
		target = strings.TrimSpace(resp.RespondingAgentID)
	}
	if target == "" {
		target = strings.TrimSpace(fallbackTarget)
	}
	message := firstNonEmptyInline(
		trimmedAnyString(data[busyResponseMessageKey]),
		strings.TrimSpace(resp.Error),
		AgentBusyDisplayMessage(target, busySnapshotFromData(data)),
	)
	return &AgentBusyError{
		Target:     target,
		Snapshot:   busySnapshotFromData(data),
		RetryAfter: durationMillisecondsFromAny(data[busyResponseRetryAfterMsKey]),
		Message:    message,
	}, true
}

func ConsultationBusyDelegatedError(target string, err error) error {
	target = strings.TrimSpace(target)
	payload := map[string]any{
		"status":    "busy",
		"busy":      true,
		"target":    target,
		"retryable": true,
		"pause":     true,
	}
	if busyErr, ok := consultationBusyError(err); ok {
		payload[busyResponseActiveReplicasKey] = busyErr.Snapshot.Active
		payload[busyResponseMaxReplicasKey] = busyErr.Snapshot.MaxActive
		payload[busyResponseQueuedRequestsKey] = busyErr.Snapshot.Queued
		payload[busyResponseMaxQueuedRequestsKey] = busyErr.Snapshot.MaxQueued
		if busyErr.RetryAfter > 0 {
			payload[busyResponseRetryAfterMsKey] = busyErr.RetryAfter.Milliseconds()
		}
	}
	message := fmt.Sprintf("%s is busy handling other consultations.", AgentDisplayName(strings.ToLower(target)))
	if busyErr, ok := consultationBusyError(err); ok && busyErr.RetryAfter > 0 {
		message = fmt.Sprintf("%s Estimated retry window: %s.", message, busyErr.RetryAfter.Round(time.Second))
	}
	message += " I’ve paused instead of failing. Do you want me to keep waiting and retry?"
	return skills.NewDelegatedError(payload, message)
}

func consultationBusyError(err error) (*AgentBusyError, bool) {
	if err == nil {
		return nil, false
	}
	var busyErr *AgentBusyError
	if !errors.As(err, &busyErr) {
		return nil, false
	}
	return busyErr, true
}

func busySnapshotFromData(data map[string]any) RequestReplicaPoolSnapshot {
	return RequestReplicaPoolSnapshot{
		Active:              intFromAny(data[busyResponseActiveReplicasKey]),
		MaxActive:           intFromAny(data[busyResponseMaxReplicasKey]),
		Queued:              intFromAny(data[busyResponseQueuedRequestsKey]),
		MaxQueued:           intFromAny(data[busyResponseMaxQueuedRequestsKey]),
		EstimatedRetryAfter: durationMillisecondsFromAny(data[busyResponseRetryAfterMsKey]),
	}
}

func extractBusyFlag(data map[string]any) bool {
	if len(data) == 0 {
		return false
	}
	if boolFromAny(data[busyResponseBusyKey]) && boolFromAny(data[busyResponseRetryableKey]) {
		return true
	}
	return strings.EqualFold(trimmedAnyString(data[busyResponseStatusKey]), "busy")
}

func boolFromAny(value any) bool {
	typed, ok := value.(bool)
	return ok && typed
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func trimmedAnyString(value any) string {
	typed, _ := value.(string)
	return strings.TrimSpace(typed)
}

func durationMillisecondsFromAny(value any) time.Duration {
	switch typed := value.(type) {
	case int:
		return time.Duration(typed) * time.Millisecond
	case int32:
		return time.Duration(typed) * time.Millisecond
	case int64:
		return time.Duration(typed) * time.Millisecond
	case float64:
		return time.Duration(int64(typed)) * time.Millisecond
	default:
		return 0
	}
}
