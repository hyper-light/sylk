package errors

import (
	"fmt"
	"strings"
	"time"
)

// RollbackLayer represents the layer being rolled back.
type RollbackLayer int

const (
	// LayerFileStaging represents staged file changes.
	LayerFileStaging RollbackLayer = iota + 1
	// LayerAgentState represents agent context/state.
	LayerAgentState
	// LayerGitLocal represents local unpushed git commits.
	LayerGitLocal
	// LayerGitPushed represents pushed git commits (requires user action).
	LayerGitPushed
	// LayerExternalState represents external service state.
	LayerExternalState
)

var layerNames = map[RollbackLayer]string{
	LayerFileStaging:   "file_staging",
	LayerAgentState:    "agent_state",
	LayerGitLocal:      "git_local",
	LayerGitPushed:     "git_pushed",
	LayerExternalState: "external_state",
}

// String returns the string representation of the layer.
func (l RollbackLayer) String() string {
	if name, ok := layerNames[l]; ok {
		return name
	}
	return "unknown"
}

// LayerResult contains the result of rolling back a single layer.
type LayerResult struct {
	Layer     RollbackLayer
	Success   bool
	Message   string
	ItemCount int // files discarded, commits removed, etc.
}

// ExternalCall records an external API call that cannot be undone.
type ExternalCall struct {
	Timestamp time.Time
	Endpoint  string
	Method    string
	Status    string
}

// RollbackReceipt contains the complete record of a rollback operation.
type RollbackReceipt struct {
	PipelineID           string
	Timestamp            time.Time
	LayerResults         []*LayerResult
	StagedFilesCount     int
	AgentContextReset    bool
	LocalCommitsRemoved  int
	PushedCommitsPending bool // requires user action
	ExternalCalls        []*ExternalCall
	ArchivalistRecorded  bool
	Warnings             []string
}

// NewRollbackReceipt creates a new RollbackReceipt for the given pipeline.
func NewRollbackReceipt(pipelineID string) *RollbackReceipt {
	return &RollbackReceipt{
		PipelineID:    pipelineID,
		Timestamp:     time.Now(),
		LayerResults:  make([]*LayerResult, 0),
		ExternalCalls: make([]*ExternalCall, 0),
		Warnings:      make([]string, 0),
	}
}

// AddLayerResult adds a layer result to the receipt.
func (r *RollbackReceipt) AddLayerResult(result *LayerResult) {
	r.LayerResults = append(r.LayerResults, result)
}

// AddWarning adds a warning message to the receipt.
func (r *RollbackReceipt) AddWarning(warning string) {
	r.Warnings = append(r.Warnings, warning)
}

// AddExternalCall records an external call that cannot be undone.
func (r *RollbackReceipt) AddExternalCall(call *ExternalCall) {
	r.ExternalCalls = append(r.ExternalCalls, call)
}

// WasSuccessful returns true if all layer rollbacks succeeded.
func (r *RollbackReceipt) WasSuccessful() bool {
	for _, result := range r.LayerResults {
		if !result.Success {
			return false
		}
	}
	return true
}

// RequiresUserAction returns true if user intervention is needed.
func (r *RollbackReceipt) RequiresUserAction() bool {
	return r.PushedCommitsPending
}

// Summary returns a human-readable summary of the rollback.
func (r *RollbackReceipt) Summary() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Rollback Receipt [%s]\n", r.PipelineID))
	sb.WriteString(fmt.Sprintf("Timestamp: %s\n", r.Timestamp.Format(time.RFC3339)))

	r.appendLayerSummary(&sb)
	r.appendStatsSummary(&sb)
	r.appendWarningsSummary(&sb)

	return sb.String()
}

// appendLayerSummary appends layer results to the summary.
func (r *RollbackReceipt) appendLayerSummary(sb *strings.Builder) {
	if len(r.LayerResults) == 0 {
		return
	}

	sb.WriteString("\nLayers:\n")
	for _, result := range r.LayerResults {
		status := "SUCCESS"
		if !result.Success {
			status = "FAILED"
		}
		sb.WriteString(fmt.Sprintf("  - %s: %s (%d items) - %s\n",
			result.Layer, status, result.ItemCount, result.Message))
	}
}

// appendStatsSummary appends statistics to the summary.
func (r *RollbackReceipt) appendStatsSummary(sb *strings.Builder) {
	sb.WriteString("\nStatistics:\n")
	sb.WriteString(fmt.Sprintf("  Staged files discarded: %d\n", r.StagedFilesCount))
	sb.WriteString(fmt.Sprintf("  Agent context reset: %t\n", r.AgentContextReset))
	sb.WriteString(fmt.Sprintf("  Local commits removed: %d\n", r.LocalCommitsRemoved))
	sb.WriteString(fmt.Sprintf("  Pushed commits pending: %t\n", r.PushedCommitsPending))
	sb.WriteString(fmt.Sprintf("  External calls logged: %d\n", len(r.ExternalCalls)))
	sb.WriteString(fmt.Sprintf("  Archivalist recorded: %t\n", r.ArchivalistRecorded))
}

// appendWarningsSummary appends warnings to the summary.
func (r *RollbackReceipt) appendWarningsSummary(sb *strings.Builder) {
	if len(r.Warnings) == 0 {
		return
	}

	sb.WriteString("\nWarnings:\n")
	for _, warning := range r.Warnings {
		sb.WriteString(fmt.Sprintf("  - %s\n", warning))
	}
}
