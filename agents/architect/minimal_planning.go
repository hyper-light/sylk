package architect

import (
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/dag"
)

const (
	metadataConsultationEvidence = "consultation_evidence"
	metadataRecommendedPath      = "recommended_path"
	metadataMinimalPath          = "minimal"
)

func attachRequirementsEvidenceDigest(requirements *Requirements, digest []map[string]any) {
	if requirements == nil || len(digest) == 0 {
		return
	}
	if requirements.Metadata == nil {
		requirements.Metadata = map[string]any{}
	}
	requirements.Metadata[metadataConsultationEvidence] = digest
	if _, ok := requirements.Metadata[metadataRecommendedPath]; !ok && inferredMinimalRequest(requirements) {
		requirements.Metadata[metadataRecommendedPath] = metadataMinimalPath
	}
}

func evidenceDigestFromRequirements(requirements *Requirements) []map[string]any {
	if requirements == nil || requirements.Metadata == nil {
		return nil
	}
	items, ok := requirements.Metadata[metadataConsultationEvidence].([]map[string]any)
	if ok {
		return items
	}
	return nil
}

func attachArchitectureEvidenceDigest(architecture *SolutionArchitecture, digest []map[string]any) {
	if architecture == nil || len(digest) == 0 {
		return
	}
	if architecture.Metadata == nil {
		architecture.Metadata = map[string]any{}
	}
	architecture.Metadata[metadataConsultationEvidence] = digest
}

func evidenceDigestFromArchitecture(architecture *SolutionArchitecture) []map[string]any {
	if architecture == nil || architecture.Metadata == nil {
		return nil
	}
	items, ok := architecture.Metadata[metadataConsultationEvidence].([]map[string]any)
	if ok {
		return items
	}
	return nil
}

func isMinimalPlanningPath(requirements *Requirements) bool {
	if requirements == nil {
		return false
	}
	if requirements.Metadata != nil {
		if strings.EqualFold(strings.TrimSpace(stringFromAny(requirements.Metadata[metadataRecommendedPath])), metadataMinimalPath) {
			return true
		}
	}
	return inferredMinimalRequest(requirements)
}

func inferredMinimalRequest(requirements *Requirements) bool {
	if requirements == nil {
		return false
	}
	if len(requirements.Goals) > 1 || len(requirements.Dependencies) > 0 {
		return false
	}
	text := strings.ToLower(strings.Join([]string{
		requirements.Query,
		strings.Join(requirements.Goals, " "),
		strings.Join(requirements.Constraints, " "),
	}, " "))
	for _, blocker := range []string{
		"database", "auth", "authentication", "migration", "distributed",
		"frontend", "react", "api", "server", "service", "deploy",
		"performance", "security", "multi", "multiple", "parallel",
	} {
		if strings.Contains(text, blocker) {
			return false
		}
	}
	for _, signal := range []string{"toy", "hello world", "minimal", "simple", "one-file", "single file", "cli"} {
		if strings.Contains(text, signal) {
			return true
		}
	}
	return false
}

func minimalArchitectureFromRequirements(requirements *Requirements) *SolutionArchitecture {
	query := ""
	if requirements != nil {
		query = requirements.Query
	}
	if strings.TrimSpace(query) == "" {
		query = "Implement the requested minimal change."
	}
	filePath := inferMinimalFilePath(query)
	return &SolutionArchitecture{
		Name:        "Minimal implementation",
		Description: query,
		Components: []ComponentSpec{{
			Name:        "minimal_change",
			Type:        "backend",
			Description: query,
			FilePath:    filePath,
			Metadata: map[string]any{
				metadataRecommendedPath: metadataMinimalPath,
			},
		}},
		Patterns: []string{"single focused implementation task"},
		Layers: []ArchitectureLayer{{
			Name:       "implementation",
			Components: []string{"minimal_change"},
			Order:      1,
		}},
		Metadata: map[string]any{
			metadataRecommendedPath: metadataMinimalPath,
		},
	}
}

func isMinimalArchitecture(architecture *SolutionArchitecture) bool {
	if architecture == nil || architecture.Metadata == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(stringFromAny(architecture.Metadata[metadataRecommendedPath])), metadataMinimalPath)
}

func minimalTasksFromArchitecture(architecture *SolutionArchitecture) []*AtomicTask {
	target := "main.go"
	description := "Implement the requested minimal change."
	if architecture != nil {
		description = firstNonEmptyString(architecture.Description, description)
		if len(architecture.Components) > 0 {
			target = firstNonEmptyString(architecture.Components[0].FilePath, target)
		}
	}
	task := &AtomicTask{
		ID:              "task_1",
		Slug:            "minimal-implementation",
		Name:            "Implement minimal change",
		Description:     description,
		AgentType:       "engineer",
		SuccessCriteria: []string{"Requested behavior is implemented", "Relevant tests or smoke checks pass"},
		Dependencies:    []string{},
		EstimatedTokens: 1200,
		Complexity:      ComplexityLow,
		Status:          TaskStatusPending,
		AcceptanceCriteria: []AcceptanceCriterion{
			{Given: "the repository is clean", When: "the minimal implementation is complete", Then: "the requested behavior is available", Priority: "must"},
			{Given: "the implementation is complete", When: "the relevant check is run", Then: "the check passes without errors", Priority: "must"},
		},
		Guidelines: []string{"Keep the change focused to the requested behavior", "Avoid introducing unnecessary framework or dependency changes"},
		ImplementationGuide: "Implement the requested behavior in the narrowest appropriate file. Add or update a focused test or smoke check when the repository structure supports it.",
		AffectedFiles:       []TaskFileTarget{{Path: target, Operation: "create", Reason: "primary file for the minimal requested behavior"}},
		TestRequirements:    []string{"Run the narrowest relevant test or CLI smoke check"},
		Workspace: TaskWorkspaceSpec{
			BaseVersion: "session_head",
			ReadSet:     []string{target},
			WriteSet:    []string{target},
			TestSurface: []string{target},
		},
		WorkerPackets: []WorkerPacket{{
			AgentType:           "engineer",
			Role:                "primary",
			Objective:           "Implement the minimal requested behavior",
			Responsibilities:    []string{"Make the focused code change", "Verify the behavior with the narrowest available check"},
			AcceptanceCriteria:  []AcceptanceCriterion{{Given: "the repository is clean", When: "the task is complete", Then: "the requested behavior works", Priority: "must"}},
			ImplementationGuide: "Work only in the scoped files unless discovery shows an adjacent test or entrypoint is required.",
			AffectedFiles:       []TaskFileTarget{{Path: target, Operation: "create", Reason: "primary implementation surface"}},
			ReadSet:             []string{target},
			WriteSet:            []string{target},
			Guidelines:          []string{"Keep scope minimal"},
			TestRequirements:    []string{"Run the narrowest available check"},
		}},
		ExecutionContracts: []AgentExecutionContract{
			{AgentType: "inspector-pipeline", Intents: []string{"synthesize_contract", "inspect_scope", "record_pending_validation", "publish_handoff_contract"}, Deliverables: []string{"criteria_contract", "scope_inspection", "pending_validation_state", "handoff_contract"}},
			{AgentType: "tester-pipeline", Intents: []string{"plan_tests", "author_tests"}, Deliverables: []string{"test_plan", "test_artifact"}},
			{AgentType: "engineer", Intents: []string{"produce_requested_change"}, Deliverables: []string{"requested_change"}},
		},
		CollaborationMode: dag.CollaborationSequential,
		CreatedAt:         time.Now(),
	}
	return []*AtomicTask{task}
}

func inferMinimalFilePath(query string) string {
	lower := strings.ToLower(query)
	switch {
	case strings.Contains(lower, "python") || strings.Contains(lower, ".py"):
		if strings.Contains(lower, "cli") {
			return "cli.py"
		}
		return "main.py"
	case strings.Contains(lower, "typescript") || strings.Contains(lower, ".ts"):
		return "src/index.ts"
	case strings.Contains(lower, "javascript") || strings.Contains(lower, ".js"):
		return "src/index.js"
	default:
		return "main.go"
	}
}
