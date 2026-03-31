package shared

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestBusyRouteResponseMessagePreservesRetryAfter(t *testing.T) {
	snapshot := RequestReplicaPoolSnapshot{
		Active:              3,
		MaxActive:           6,
		Queued:              5,
		MaxQueued:           12,
		EstimatedRetryAfter: 9 * time.Second,
	}
	resp := &guide.RouteResponse{
		CorrelationID:       "corr_busy",
		Success:             false,
		RespondingAgentID:   "librarian",
		RespondingAgentName: "Librarian",
		Error:               "busy",
		Data:                BusyRouteResponseData("librarian", snapshot, "busy"),
	}
	msg := guide.NewResponseMessage("msg_busy", resp)

	busyErr, ok := BusyRouteResponseMessage(msg, "librarian")
	if !ok {
		t.Fatal("busy response was not detected")
	}
	if busyErr == nil {
		t.Fatal("busy error is nil")
	}
	if busyErr.RetryAfter != 9*time.Second {
		t.Fatalf("retry after = %s, want 9s", busyErr.RetryAfter)
	}
	if busyErr.Snapshot.EstimatedRetryAfter != 9*time.Second {
		t.Fatalf("snapshot retry after = %s, want 9s", busyErr.Snapshot.EstimatedRetryAfter)
	}
}
