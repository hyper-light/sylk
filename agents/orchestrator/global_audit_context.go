package orchestrator

import (
	"fmt"
	"os"
	"strings"

	"github.com/adalundhe/sylk/agents/architect"
)

const workflowPlanSnapshotKey = "plan_snapshot"

func buildGlobalInspectorPlanSnapshot(h *architect.PlanHandoff) string {
	if h == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Architect Execution Plan\n\n")
	fmt.Fprintf(&b, "- Plan ID: %s\n", strings.TrimSpace(h.PlanID))
	if revision := h.Revision; revision > 0 {
		fmt.Fprintf(&b, "- Revision: %d\n", revision)
	}
	if trigger := strings.TrimSpace(h.Trigger); trigger != "" {
		fmt.Fprintf(&b, "- Trigger: %s\n", trigger)
	}
	if planFile := strings.TrimSpace(h.PlanFile); planFile != "" {
		fmt.Fprintf(&b, "- Plan file: %s\n", planFile)
	}
	if goal := strings.TrimSpace(h.Query); goal != "" {
		b.WriteString("\n## User Goal\n")
		b.WriteString(goal)
		b.WriteString("\n")
	}
	if summary := summarizeArchitecture(h.Architecture); strings.TrimSpace(summary) != "" {
		b.WriteString("\n## Architecture Summary\n")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	if h.Requirements != nil {
		if len(h.Requirements.Goals) > 0 {
			b.WriteString("\n## Requirements Goals\n")
			appendBulletList(&b, h.Requirements.Goals)
		}
		if len(h.Requirements.Constraints) > 0 {
			b.WriteString("\n## Requirements Constraints\n")
			appendBulletList(&b, h.Requirements.Constraints)
		}
	}
	if len(h.Assumptions) > 0 {
		b.WriteString("\n## Assumptions\n")
		appendBulletList(&b, h.Assumptions)
	}
	if len(h.RiskSummary) > 0 {
		b.WriteString("\n## Plan Risks\n")
		appendBulletList(&b, h.RiskSummary)
	}
	if len(h.CriticalPath) > 0 {
		b.WriteString("\n## Critical Path\n")
		appendNumberedList(&b, h.CriticalPath)
	}
	if len(h.ExecutionLayers) > 0 {
		b.WriteString("\n## Execution Layers\n")
		for i, layer := range h.ExecutionLayers {
			fmt.Fprintf(&b, "%d. %s\n", i+1, strings.Join(layer, ", "))
		}
	}
	if len(h.Tasks) > 0 {
		b.WriteString("\n## Tasks\n")
		for _, task := range h.Tasks {
			if task == nil {
				continue
			}
			appendPlanTaskSnapshot(&b, task)
		}
	}
	return strings.TrimSpace(b.String())
}

func buildGlobalInspectorTaskCriteriaSnapshot(task *architect.HandoffTask) string {
	if task == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Task Criteria: %s\n\n", firstNonEmpty(strings.TrimSpace(task.Name), strings.TrimSpace(task.ID), "task"))
	fmt.Fprintf(&b, "- Task ID: %s\n", strings.TrimSpace(task.ID))
	if slug := strings.TrimSpace(task.Slug); slug != "" {
		fmt.Fprintf(&b, "- Slug: %s\n", slug)
	}
	if agentType := strings.TrimSpace(task.AgentType); agentType != "" {
		fmt.Fprintf(&b, "- Agent type: %s\n", agentType)
	}
	if priority := task.Priority; priority != 0 {
		fmt.Fprintf(&b, "- Priority: %d\n", priority)
	}
	if complexity := strings.TrimSpace(task.Complexity); complexity != "" {
		fmt.Fprintf(&b, "- Complexity: %s\n", complexity)
	}
	if description := strings.TrimSpace(task.Description); description != "" {
		b.WriteString("\n## Description\n")
		b.WriteString(description)
		b.WriteString("\n")
	}
	if len(task.Dependencies) > 0 {
		b.WriteString("\n## Dependencies\n")
		appendBulletList(&b, task.Dependencies)
	}
	if guide := strings.TrimSpace(task.ImplementationGuide); guide != "" {
		b.WriteString("\n## Implementation Guide\n")
		b.WriteString(guide)
		b.WriteString("\n")
	}
	if len(task.SuccessCriteria) > 0 {
		b.WriteString("\n## Success Criteria\n")
		appendBulletList(&b, task.SuccessCriteria)
	}
	if len(task.AcceptanceCriteria) > 0 {
		b.WriteString("\n## Acceptance Criteria\n")
		appendAcceptanceCriteria(&b, task.AcceptanceCriteria)
	}
	if len(task.Guidelines) > 0 {
		b.WriteString("\n## Guidelines\n")
		appendBulletList(&b, task.Guidelines)
	}
	if len(task.Examples) > 0 {
		b.WriteString("\n## Examples\n")
		appendExamples(&b, task.Examples)
	}
	if len(task.AffectedFiles) > 0 {
		b.WriteString("\n## Affected Files\n")
		appendAffectedFiles(&b, task.AffectedFiles)
	}
	if len(task.TestRequirements) > 0 {
		b.WriteString("\n## Test Requirements\n")
		appendBulletList(&b, task.TestRequirements)
	}
	if len(task.RiskFactors) > 0 {
		b.WriteString("\n## Risk Factors\n")
		appendBulletList(&b, task.RiskFactors)
	}
	if len(task.WorkerPackets) > 0 {
		b.WriteString("\n## Worker Packets\n")
		appendWorkerPackets(&b, task.WorkerPackets)
	}
	if len(task.ExecutionContracts) > 0 {
		b.WriteString("\n## Execution Contracts\n")
		appendExecutionContracts(&b, task.ExecutionContracts)
	}
	if len(task.AgentScopes) > 0 {
		b.WriteString("\n## Agent Scopes\n")
		appendAgentScopes(&b, task.AgentScopes)
	}
	return strings.TrimSpace(b.String())
}

func appendPlanTaskSnapshot(b *strings.Builder, task *architect.HandoffTask) {
	if b == nil || task == nil {
		return
	}
	fmt.Fprintf(b, "\n### %s (%s)\n", firstNonEmpty(strings.TrimSpace(task.Name), strings.TrimSpace(task.ID), "task"), strings.TrimSpace(task.ID))
	fmt.Fprintf(b, "- Agent type: %s\n", strings.TrimSpace(task.AgentType))
	if slug := strings.TrimSpace(task.Slug); slug != "" {
		fmt.Fprintf(b, "- Slug: %s\n", slug)
	}
	if complexity := strings.TrimSpace(task.Complexity); complexity != "" {
		fmt.Fprintf(b, "- Complexity: %s\n", complexity)
	}
	if task.Priority != 0 {
		fmt.Fprintf(b, "- Priority: %d\n", task.Priority)
	}
	if task.EstimatedTokens > 0 {
		fmt.Fprintf(b, "- Estimated tokens: %d\n", task.EstimatedTokens)
	}
	if len(task.Dependencies) > 0 {
		fmt.Fprintf(b, "- Dependencies: %s\n", strings.Join(task.Dependencies, ", "))
	}
	if len(task.CoAgents) > 0 {
		fmt.Fprintf(b, "- Co-agents: %s\n", strings.Join(task.CoAgents, ", "))
	}
	if mode := strings.TrimSpace(task.CollaborationMode); mode != "" {
		fmt.Fprintf(b, "- Collaboration mode: %s\n", mode)
	}
	if task.MaxReviewRounds > 0 {
		fmt.Fprintf(b, "- Max review rounds: %d\n", task.MaxReviewRounds)
	}
	if description := strings.TrimSpace(task.Description); description != "" {
		b.WriteString("\n#### Description\n")
		b.WriteString(description)
		b.WriteString("\n")
	}
	if guide := strings.TrimSpace(task.ImplementationGuide); guide != "" {
		b.WriteString("\n#### Implementation Guide\n")
		b.WriteString(guide)
		b.WriteString("\n")
	}
	if len(task.SuccessCriteria) > 0 {
		b.WriteString("\n#### Success Criteria\n")
		appendBulletList(b, task.SuccessCriteria)
	}
	if len(task.AcceptanceCriteria) > 0 {
		b.WriteString("\n#### Acceptance Criteria\n")
		appendAcceptanceCriteria(b, task.AcceptanceCriteria)
	}
	if len(task.Guidelines) > 0 {
		b.WriteString("\n#### Guidelines\n")
		appendBulletList(b, task.Guidelines)
	}
	if len(task.Examples) > 0 {
		b.WriteString("\n#### Examples\n")
		appendExamples(b, task.Examples)
	}
	if len(task.AffectedFiles) > 0 {
		b.WriteString("\n#### Affected Files\n")
		appendAffectedFiles(b, task.AffectedFiles)
	}
	if len(task.TestRequirements) > 0 {
		b.WriteString("\n#### Test Requirements\n")
		appendBulletList(b, task.TestRequirements)
	}
	if len(task.RiskFactors) > 0 {
		b.WriteString("\n#### Risk Factors\n")
		appendBulletList(b, task.RiskFactors)
	}
	if len(task.ExecutionContracts) > 0 {
		b.WriteString("\n#### Execution Contracts\n")
		appendExecutionContracts(b, task.ExecutionContracts)
	}
	if len(task.WorkerPackets) > 0 {
		b.WriteString("\n#### Worker Packets\n")
		appendWorkerPackets(b, task.WorkerPackets)
	}
	if len(task.AgentScopes) > 0 {
		b.WriteString("\n#### Agent Scopes\n")
		appendAgentScopes(b, task.AgentScopes)
	}
}

func appendBulletList(b *strings.Builder, values []string) {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			fmt.Fprintf(b, "- %s\n", trimmed)
		}
	}
}

func appendNumberedList(b *strings.Builder, values []string) {
	idx := 1
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			fmt.Fprintf(b, "%d. %s\n", idx, trimmed)
			idx++
		}
	}
}

func appendAcceptanceCriteria(b *strings.Builder, criteria []architect.AcceptanceCriterion) {
	idx := 1
	for _, criterion := range criteria {
		given := strings.TrimSpace(criterion.Given)
		when := strings.TrimSpace(criterion.When)
		then := strings.TrimSpace(criterion.Then)
		priority := strings.TrimSpace(criterion.Priority)
		if given == "" && when == "" && then == "" {
			continue
		}
		if priority != "" {
			fmt.Fprintf(b, "%d. [%s] Given %s, when %s, then %s\n", idx, priority, given, when, then)
		} else {
			fmt.Fprintf(b, "%d. Given %s, when %s, then %s\n", idx, given, when, then)
		}
		idx++
	}
}

func appendExamples(b *strings.Builder, examples []architect.TaskExample) {
	for _, example := range examples {
		if label := strings.TrimSpace(example.Label); label != "" {
			fmt.Fprintf(b, "##### %s\n", label)
		}
		if code := strings.TrimSpace(example.Code); code != "" {
			fence := "```"
			if language := strings.TrimSpace(example.Language); language != "" {
				fence += language
			}
			fmt.Fprintf(b, "%s\n%s\n```\n", fence, code)
		}
		if explanation := strings.TrimSpace(example.Explanation); explanation != "" {
			b.WriteString(explanation)
			b.WriteString("\n")
		}
	}
}

func appendAffectedFiles(b *strings.Builder, files []architect.TaskFileTarget) {
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		op := strings.TrimSpace(file.Operation)
		reason := strings.TrimSpace(file.Reason)
		switch {
		case op != "" && reason != "":
			fmt.Fprintf(b, "- %s [%s]: %s\n", path, op, reason)
		case op != "":
			fmt.Fprintf(b, "- %s [%s]\n", path, op)
		case reason != "":
			fmt.Fprintf(b, "- %s: %s\n", path, reason)
		default:
			fmt.Fprintf(b, "- %s\n", path)
		}
	}
}

func appendExecutionContracts(b *strings.Builder, contracts []architect.AgentExecutionContract) {
	for _, contract := range contracts {
		agentType := strings.TrimSpace(contract.AgentType)
		if agentType == "" {
			agentType = "agent"
		}
		fmt.Fprintf(b, "- %s\n", agentType)
		appendIndentedBullets(b, "intents", contract.Intents)
		appendIndentedBullets(b, "deliverables", contract.Deliverables)
	}
}

func appendWorkerPackets(b *strings.Builder, packets []architect.WorkerPacket) {
	for _, packet := range packets {
		agentType := strings.TrimSpace(packet.AgentType)
		if agentType == "" {
			agentType = "agent"
		}
		fmt.Fprintf(b, "- %s (%s)\n", agentType, strings.TrimSpace(packet.Role))
		if objective := strings.TrimSpace(packet.Objective); objective != "" {
			fmt.Fprintf(b, "  objective: %s\n", objective)
		}
		appendIndentedBullets(b, "responsibilities", packet.Responsibilities)
		if len(packet.AcceptanceCriteria) > 0 {
			fmt.Fprintf(b, "  acceptance criteria:\n")
			appendAcceptanceCriteriaWithIndent(b, packet.AcceptanceCriteria, "  ")
		}
		if guide := strings.TrimSpace(packet.ImplementationGuide); guide != "" {
			fmt.Fprintf(b, "  implementation guide: %s\n", guide)
		}
		appendIndentedAffectedFiles(b, packet.AffectedFiles)
		appendIndentedBullets(b, "guidelines", packet.Guidelines)
		appendIndentedBullets(b, "test requirements", packet.TestRequirements)
	}
}

func appendAgentScopes(b *strings.Builder, scopes []architect.AgentScope) {
	for _, scope := range scopes {
		agentType := strings.TrimSpace(scope.AgentType)
		if agentType == "" {
			agentType = "agent"
		}
		fmt.Fprintf(b, "- %s (%s)\n", agentType, strings.TrimSpace(scope.Role))
		if len(scope.AcceptanceCriteria) > 0 {
			fmt.Fprintf(b, "  acceptance criteria:\n")
			appendAcceptanceCriteriaWithIndent(b, scope.AcceptanceCriteria, "  ")
		}
		if guide := strings.TrimSpace(scope.ImplementationGuide); guide != "" {
			fmt.Fprintf(b, "  implementation guide: %s\n", guide)
		}
		appendIndentedAffectedFiles(b, scope.AffectedFiles)
		appendIndentedBullets(b, "guidelines", scope.Guidelines)
		appendIndentedBullets(b, "test requirements", scope.TestRequirements)
	}
}

func appendIndentedBullets(b *strings.Builder, label string, values []string) {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	if len(filtered) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s:\n", strings.TrimSpace(label))
	for _, value := range filtered {
		fmt.Fprintf(b, "  - %s\n", value)
	}
}

func appendAcceptanceCriteriaWithIndent(b *strings.Builder, criteria []architect.AcceptanceCriterion, indent string) {
	idx := 1
	for _, criterion := range criteria {
		given := strings.TrimSpace(criterion.Given)
		when := strings.TrimSpace(criterion.When)
		then := strings.TrimSpace(criterion.Then)
		priority := strings.TrimSpace(criterion.Priority)
		if given == "" && when == "" && then == "" {
			continue
		}
		if priority != "" {
			fmt.Fprintf(b, "%s%d. [%s] Given %s, when %s, then %s\n", indent, idx, priority, given, when, then)
		} else {
			fmt.Fprintf(b, "%s%d. Given %s, when %s, then %s\n", indent, idx, given, when, then)
		}
		idx++
	}
}

func appendIndentedAffectedFiles(b *strings.Builder, files []architect.TaskFileTarget) {
	filtered := make([]architect.TaskFileTarget, 0, len(files))
	for _, file := range files {
		if strings.TrimSpace(file.Path) != "" {
			filtered = append(filtered, file)
		}
	}
	if len(filtered) == 0 {
		return
	}
	b.WriteString("  affected files:\n")
	for _, file := range filtered {
		path := strings.TrimSpace(file.Path)
		op := strings.TrimSpace(file.Operation)
		reason := strings.TrimSpace(file.Reason)
		switch {
		case op != "" && reason != "":
			fmt.Fprintf(b, "  - %s [%s]: %s\n", path, op, reason)
		case op != "":
			fmt.Fprintf(b, "  - %s [%s]\n", path, op)
		case reason != "":
			fmt.Fprintf(b, "  - %s: %s\n", path, reason)
		default:
			fmt.Fprintf(b, "  - %s\n", path)
		}
	}
}

func (o *Orchestrator) globalInspectorPlanContext(task *TaskRecord) (string, string) {
	if o == nil || task == nil || o.state == nil {
		return "", ""
	}

	planFilePath := strings.TrimSpace(stringMapValue(task.Metadata, "plan_file_path"))
	planSnapshot := ""

	o.mu.RLock()
	if workflow := o.state.Workflows[strings.TrimSpace(task.WorkflowID)]; workflow != nil {
		planSnapshot = strings.TrimSpace(stringMapValue(workflow.Metadata, workflowPlanSnapshotKey))
		planFilePath = firstNonEmpty(planFilePath, strings.TrimSpace(stringMapValue(workflow.Metadata, "plan_file_path")))
	}
	o.mu.RUnlock()

	if planText := readPlanTextBestEffort(planFilePath); planText != "" {
		return planText, planFilePath
	}
	return planSnapshot, planFilePath
}

func readPlanTextBestEffort(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
