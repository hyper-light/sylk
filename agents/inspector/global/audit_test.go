package global

import (
	"testing"

	"github.com/adalundhe/sylk/agents/inspector/shared"
)

func TestApplyDeterministicAuditPrepass_FailedNodeAndSharedFile(t *testing.T) {
	gi := &GlobalInspector{}
	req := &shared.LayerAuditRequest{
		DAGID:    "dag-1",
		LayerIdx: 0,
		NodeDiffs: map[string]*shared.NodeDiffSnapshot{
			"node-a": {
				NodeID: "node-a",
				ModifiedFiles: []shared.AuditFileDiff{
					{Path: "internal/service.go"},
				},
			},
			"node-b": {
				NodeID: "node-b",
				ModifiedFiles: []shared.AuditFileDiff{
					{Path: "internal/service.go"},
				},
			},
		},
		NodeResults: map[string]*shared.NodeResultInfo{
			"node-a": {NodeID: "node-a", Success: true},
			"node-b": {NodeID: "node-b", Success: false, Error: "compile failed"},
		},
	}
	result := &shared.AuditResult{}

	gi.applyDeterministicAuditPrepass(req, result)

	if result.PlanAdherence.Score >= 1.0 {
		t.Fatalf("expected degraded adherence score, got %v", result.PlanAdherence.Score)
	}
	if len(result.PlanAdherence.TasksMissing) == 0 {
		t.Fatal("expected missing tasks to be recorded")
	}
	if result.HighCount == 0 {
		t.Fatal("expected high-severity findings")
	}
	if len(result.CrossFileIssues) == 0 {
		t.Fatal("expected cross-file issue for shared file modification")
	}
	if len(result.Recommendations) == 0 {
		t.Fatal("expected recommendations to be generated")
	}
}
