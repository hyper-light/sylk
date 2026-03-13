package shared

import "strings"

type TaskExecutionLedger struct {
	TaskID           string
	WorkerType       string
	Seeded           bool
	PendingReviews   []TaskExecutionReview
	RelevantArtifacts []TaskExecutionArtifact
}

type TaskExecutionReview struct {
	ID            string
	ArtifactID    string
	Summary       string
	RequesterType string
	ReviewerType  string
	Status        string
}

type TaskExecutionArtifact struct {
	ID           string
	Kind         string
	Summary      string
	ProducerType string
	ScopeKey     string
}

func buildTaskExecutionLedger(task *PipelineTaskInput, workerType string) *TaskExecutionLedger {
	if task == nil {
		return nil
	}
	packet := decodeMap(task.Context, "coordination_packet")
	ledger := &TaskExecutionLedger{
		TaskID:     strings.TrimSpace(task.TaskID),
		WorkerType: strings.TrimSpace(workerType),
		Seeded:     len(packet) > 0,
	}
	if len(packet) == 0 {
		return ledger
	}
	ledger.PendingReviews = decodeTaskExecutionReviews(packet["pending_reviews"])
	ledger.RelevantArtifacts = decodeTaskExecutionArtifacts(packet["relevant_artifacts"])
	return ledger
}

func decodeTaskExecutionReviews(value any) []TaskExecutionReview {
	entries, ok := value.([]any)
	if !ok {
		return nil
	}
	reviews := make([]TaskExecutionReview, 0, len(entries))
	for _, entry := range entries {
		review, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if parsed, ok := taskExecutionReviewFromMap(review); ok {
			reviews = append(reviews, parsed)
		}
	}
	return reviews
}

func decodeTaskExecutionArtifacts(value any) []TaskExecutionArtifact {
	entries, ok := value.([]any)
	if !ok {
		return nil
	}
	artifacts := make([]TaskExecutionArtifact, 0, len(entries))
	for _, entry := range entries {
		artifact, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if parsed, ok := taskExecutionArtifactFromMap(artifact); ok {
			artifacts = append(artifacts, parsed)
		}
	}
	return artifacts
}

func taskExecutionReviewFromMap(review map[string]any) (TaskExecutionReview, bool) {
	parsed := TaskExecutionReview{
		ID:            strings.TrimSpace(stringValue(review["id"])),
		ArtifactID:    strings.TrimSpace(stringValue(review["artifact_id"])),
		Summary:       strings.TrimSpace(stringValue(review["summary"])),
		RequesterType: strings.TrimSpace(stringValue(review["requester_type"])),
		ReviewerType:  strings.TrimSpace(stringValue(review["reviewer_type"])),
		Status:        strings.TrimSpace(stringValue(review["status"])),
	}
	return parsed, parsed.ID != "" || parsed.ArtifactID != "" || parsed.Summary != ""
}

func taskExecutionArtifactFromMap(artifact map[string]any) (TaskExecutionArtifact, bool) {
	parsed := TaskExecutionArtifact{
		ID:           strings.TrimSpace(stringValue(artifact["id"])),
		Kind:         strings.TrimSpace(stringValue(artifact["kind"])),
		Summary:      strings.TrimSpace(stringValue(artifact["summary"])),
		ProducerType: strings.TrimSpace(stringValue(artifact["producer_type"])),
		ScopeKey:     strings.TrimSpace(stringValue(artifact["scope_key"])),
	}
	return parsed, parsed.ID != "" || parsed.Kind != "" || parsed.Summary != ""
}
