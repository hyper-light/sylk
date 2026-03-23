package chat

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/adalundhe/sylk/ui/msg"
)

// planStatusIcon maps a task status string to a Unicode icon.
func planStatusIcon(status string) string {
	switch status {
	case "pending":
		return "○"
	case "queued", "running":
		return "◉"
	case "completed":
		return "✓"
	case "failed":
		return "✕"
	case "blocked":
		return "◌"
	case "skipped":
		return "─"
	default:
		return "○"
	}
}

// estimatePlanSize returns a rough byte estimate for the rendered markdown.
// Avoids repeated Builder reallocations for typical plan sizes.
func estimatePlanSize(taskCount int) int {
	// Header (~96) + per-task (~720) + footer (~48).
	return 144 + taskCount*720
}

// formatPlanMarkdown renders a PlanUpdateMsg as a markdown string suitable
// for the chat panel's existing CommonMark renderer.
func formatPlanMarkdown(update msg.PlanUpdateMsg) string {
	var b strings.Builder
	b.Grow(estimatePlanSize(len(update.Tasks)))

	b.WriteString("## Plan\n\n**Status:** ")
	b.WriteString(strings.TrimSpace(update.Status))
	b.WriteString("\n\n")

	if len(update.Tasks) == 0 {
		return b.String()
	}

	taskByID := make(map[string]msg.PlanTaskSnapshot, len(update.Tasks))
	for i := range update.Tasks {
		taskByID[update.Tasks[i].ID] = update.Tasks[i]
	}

	if len(update.ExecutionLayers) > 1 {
		formatPlanLayered(&b, update.ExecutionLayers, taskByID, update.Tasks)
	} else {
		formatPlanFlat(&b, update.Tasks)
	}

	layers := len(update.ExecutionLayers)
	tasks := len(update.Tasks)
	b.WriteString("\n### Execution\n\n")
	if layers > 1 {
		b.WriteString(strconv.Itoa(layers))
		b.WriteString(" layers, ")
		b.WriteString(strconv.Itoa(tasks))
		b.WriteString(" tasks\n")
	} else {
		b.WriteString(strconv.Itoa(tasks))
		b.WriteString(" tasks\n")
	}

	return b.String()
}

// formatPlanLayered renders tasks grouped by execution layer.
func formatPlanLayered(b *strings.Builder, layers [][]string, taskByID map[string]msg.PlanTaskSnapshot, allTasks []msg.PlanTaskSnapshot) {
	b.WriteString("### Tasks\n\n")
	taskNum := 1
	for layerIdx, layer := range layers {
		if layerIdx > 0 {
			b.WriteString("\n")
		}
		b.WriteString("#### Layer ")
		b.WriteString(strconv.Itoa(layerIdx + 1))
		b.WriteString("\n\n")
		for _, id := range layer {
			task, ok := taskByID[id]
			if !ok {
				continue
			}
			formatPlanTask(b, task, taskNum)
			taskNum++
		}
	}

	totalIDs := 0
	for _, layer := range layers {
		totalIDs += len(layer)
	}
	if totalIDs < len(allTasks) {
		layerSet := make(map[string]struct{}, totalIDs)
		for _, layer := range layers {
			for _, id := range layer {
				layerSet[id] = struct{}{}
			}
		}
		if taskNum > 1 {
			b.WriteString("\n")
		}
		b.WriteString("#### Unlayered\n\n")
		for _, task := range allTasks {
			if _, inLayer := layerSet[task.ID]; inLayer {
				continue
			}
			formatPlanTask(b, task, taskNum)
			taskNum++
		}
	}
}

// formatPlanFlat renders tasks as a numbered list.
func formatPlanFlat(b *strings.Builder, tasks []msg.PlanTaskSnapshot) {
	b.WriteString("### Tasks\n\n")
	for i := range tasks {
		formatPlanTask(b, tasks[i], i+1)
	}
}

// formatPlanTask renders a single task entry.
func formatPlanTask(b *strings.Builder, task msg.PlanTaskSnapshot, num int) {
	status := strings.TrimSpace(task.Status)
	b.WriteString(strconv.Itoa(num))
	b.WriteString(". **")
	b.WriteString(firstNonEmptyPlanText(strings.TrimSpace(task.Name), strings.TrimSpace(task.ID), "Untitled task"))
	b.WriteString("**")
	if agent := strings.TrimSpace(task.AgentType); agent != "" {
		b.WriteString(" `")
		b.WriteString(agent)
		b.WriteString("`")
	}
	b.WriteByte(' ')
	b.WriteString(planStatusIcon(status))
	if status != "" {
		b.WriteByte(' ')
		b.WriteString(status)
	}
	b.WriteByte('\n')

	indent := taskIndent(num)

	if desc := normalizePlanMarkdownBlock(task.Description); desc != "" {
		b.WriteByte('\n')
		writeIndentedMarkdownBlock(b, indent, desc)
		b.WriteByte('\n')
	}

	if len(task.Dependencies) > 0 {
		b.WriteByte('\n')
		writeIndentedMarkdownBlock(b, indent, "**Depends On:** "+inlineCodeList(task.Dependencies))
		b.WriteByte('\n')
	}

	if len(task.AcceptanceCriteria) > 0 {
		b.WriteByte('\n')
		writeIndentedMarkdownBlock(b, indent, "**Acceptance Criteria**")
		b.WriteByte('\n')
		renderAcceptanceCriteriaMarkdown(b, indent, task.AcceptanceCriteria)
	}

	if len(task.AffectedFiles) > 0 {
		b.WriteByte('\n')
		writeIndentedMarkdownBlock(b, indent, "**Affected Files**")
		b.WriteByte('\n')
		for _, file := range task.AffectedFiles {
			line := "- `" + strings.TrimSpace(file.Path) + "`"
			if op := strings.TrimSpace(file.Operation); op != "" {
				line += " (" + op + ")"
			}
			if reason := strings.TrimSpace(file.Reason); reason != "" {
				line += ": " + reason
			}
			writeIndentedMarkdownBlock(b, indent, line)
		}
		b.WriteByte('\n')
	}

	if guide := normalizePlanMarkdownBlock(task.ImplementationGuide); guide != "" {
		b.WriteByte('\n')
		writeIndentedMarkdownBlock(b, indent, "**Implementation Guide**")
		b.WriteByte('\n')
		b.WriteByte('\n')
		writeIndentedMarkdownBlock(b, indent, guide)
		b.WriteByte('\n')
	}

	if len(task.Examples) > 0 {
		b.WriteByte('\n')
		writeIndentedMarkdownBlock(b, indent, "**Examples**")
		b.WriteByte('\n')
		renderTaskExamplesMarkdown(b, indent, task.Examples)
	}

	if len(task.Guidelines) > 0 {
		b.WriteByte('\n')
		writeIndentedMarkdownBlock(b, indent, "**Guidelines**")
		b.WriteByte('\n')
		for _, guideline := range task.Guidelines {
			if strings.TrimSpace(guideline) == "" {
				continue
			}
			writeIndentedMarkdownBlock(b, indent, "- "+strings.TrimSpace(guideline))
		}
		b.WriteByte('\n')
	}

	if note := strings.TrimSpace(task.StatusMessage); note != "" {
		b.WriteByte('\n')
		writeIndentedMarkdownBlock(b, indent, "> "+note)
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
}

func renderAcceptanceCriteriaMarkdown(b *strings.Builder, indent string, criteria []msg.PlanAcceptanceCriterion) {
	priorities := []string{"must", "should", "could"}
	grouped := map[string][]msg.PlanAcceptanceCriterion{}
	for _, criterion := range criteria {
		priority := normalizePlanPriority(criterion.Priority)
		grouped[priority] = append(grouped[priority], criterion)
	}
	for _, priority := range priorities {
		group := grouped[priority]
		if len(group) == 0 {
			continue
		}
		writeIndentedMarkdownBlock(b, indent, "**"+priorityHeading(priority)+"**")
		for _, criterion := range group {
			line := "- [" + priority + "] Given " + strings.TrimSpace(criterion.Given) +
				" / When " + strings.TrimSpace(criterion.When) +
				" / Then " + strings.TrimSpace(criterion.Then)
			writeIndentedMarkdownBlock(b, indent, line)
		}
		b.WriteByte('\n')
	}
}

func renderTaskExamplesMarkdown(b *strings.Builder, indent string, examples []msg.PlanTaskExample) {
	for i, example := range examples {
		renderTaskExampleMarkdown(b, indent, example)
		if i < len(examples)-1 {
			b.WriteByte('\n')
		}
	}
}

func renderTaskExampleMarkdown(b *strings.Builder, indent string, example msg.PlanTaskExample) {
	example = normalizePlanTaskExample(example)
	if example.Label != "" {
		writeIndentedMarkdownBlock(b, indent, "*"+example.Label+"*")
	}
	if example.Code != "" {
		fence := "```"
		if example.Language != "" {
			fence += example.Language
		}
		writeIndentedMarkdownBlock(b, indent, fence)
		writeIndentedMarkdownBlock(b, indent, example.Code)
		writeIndentedMarkdownBlock(b, indent, "```")
	}
	if explanation := normalizePlanMarkdownBlock(example.Explanation); explanation != "" {
		writeIndentedMarkdownBlock(b, indent, explanation)
	}
}

func taskIndent(num int) string {
	return strings.Repeat(" ", len(strconv.Itoa(num))+2)
}

func writeIndentedMarkdownBlock(b *strings.Builder, indent, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			b.WriteByte('\n')
			continue
		}
		b.WriteString(indent)
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func inlineCodeList(values []string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		items = append(items, "`"+trimmed+"`")
	}
	return strings.Join(items, ", ")
}

func normalizePlanPriority(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "must", "should", "could":
		return strings.ToLower(strings.TrimSpace(priority))
	default:
		return "must"
	}
}

func priorityHeading(priority string) string {
	switch priority {
	case "must":
		return "Must"
	case "should":
		return "Should"
	case "could":
		return "Could"
	default:
		return "Criteria"
	}
}

func normalizePlanMarkdownBlock(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	normalized := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if strings.HasPrefix(strings.TrimSpace(trimmed), "```") {
			inFence = !inFence
			normalized = append(normalized, strings.TrimSpace(trimmed))
			continue
		}
		if inFence {
			normalized = append(normalized, trimmed)
			continue
		}
		normalized = append(normalized, rewritePlanStepLine(trimmed))
	}
	return strings.TrimSpace(strings.Join(normalized, "\n"))
}

func rewritePlanStepLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if number, rest, ok := parseStepPrefix(trimmed); ok {
		return strconv.Itoa(number) + ". " + rest
	}
	if number, rest, ok := parseParenNumberedPrefix(trimmed); ok {
		return strconv.Itoa(number) + ". " + rest
	}
	return trimmed
}

func parseStepPrefix(line string) (int, string, bool) {
	lower := strings.ToLower(line)
	if !strings.HasPrefix(lower, "step ") {
		return 0, "", false
	}
	i := len("step ")
	start := i
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == start || i >= len(line) || line[i] != ':' {
		return 0, "", false
	}
	number, err := strconv.Atoi(line[start:i])
	if err != nil {
		return 0, "", false
	}
	rest := strings.TrimSpace(line[i+1:])
	if rest == "" {
		return 0, "", false
	}
	return number, rest, true
}

func parseParenNumberedPrefix(line string) (int, string, bool) {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(line) || line[i] != ')' {
		return 0, "", false
	}
	number, err := strconv.Atoi(line[:i])
	if err != nil {
		return 0, "", false
	}
	rest := strings.TrimSpace(line[i+1:])
	if rest == "" {
		return 0, "", false
	}
	return number, rest, true
}

func normalizePlanTaskExample(example msg.PlanTaskExample) msg.PlanTaskExample {
	code, detectedLanguage := trimPlanExampleFence(example.Code)
	example.Label = strings.TrimSpace(example.Label)
	example.Language = normalizePlanExampleLanguage(firstNonEmptyPlanText(example.Language, detectedLanguage, ""))
	example.Code = strings.TrimSpace(code)
	example.Explanation = strings.TrimSpace(example.Explanation)
	if example.Language == "" {
		example.Language = inferPlanExampleLanguage(example.Code)
	}
	return example
}

func trimPlanExampleFence(code string) (string, string) {
	trimmed := strings.TrimSpace(code)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed, ""
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return strings.TrimPrefix(trimmed, "```"), ""
	}
	first := strings.TrimSpace(lines[0])
	last := strings.TrimSpace(lines[len(lines)-1])
	if last != "```" {
		return trimmed, ""
	}
	language := strings.TrimSpace(strings.TrimPrefix(first, "```"))
	body := strings.Join(lines[1:len(lines)-1], "\n")
	return strings.Trim(body, "\n"), language
}

func normalizePlanExampleLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range language {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("+-_#", r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func inferPlanExampleLanguage(code string) string {
	for _, line := range strings.Split(code, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "flowchart ") ||
			strings.HasPrefix(lower, "graph ") ||
			strings.HasPrefix(lower, "sequencediagram") ||
			strings.HasPrefix(lower, "statediagram") {
			return "mermaid"
		}
		break
	}
	return ""
}

func firstNonEmptyPlanText(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
