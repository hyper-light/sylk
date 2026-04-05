package bridge

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func formatStructuredPayload(agentID string, payload any) (string, bool) {
	payload = decodeStructuredJSONPayload(payload)
	if text, ok := formatResponseTextPayload(payload); ok {
		return text, true
	}
	if text, ok := formatInspectorStagePayload(payload); ok {
		return text, true
	}
	if text, ok := formatTesterStagePayload(payload); ok {
		return text, true
	}
	if text, ok := formatConversationResult(payload); ok {
		return text, true
	}
	if text, ok := formatArchitectPlanSummary(payload); ok {
		return text, true
	}
	if text, ok := formatArchitecturePayload(payload); ok {
		return text, true
	}
	if text, ok := formatCapabilitiesPayload(payload); ok {
		return text, true
	}
	if text, ok := formatAnswerPayload(payload); ok {
		return text, true
	}
	if strings.EqualFold(strings.TrimSpace(agentID), "architect") {
		if text, ok := formatGenericMapSummary(payload); ok {
			return text, true
		}
	}
	return "", false
}

type responseTexter interface {
	ResponseText() string
}

func formatResponseTextPayload(payload any) (string, bool) {
	rt, ok := payload.(responseTexter)
	if !ok || rt == nil {
		return "", false
	}
	return strings.TrimSpace(rt.ResponseText()), true
}

// formatConversationResult extracts the Response field from a ConversationResult
// payload. Handles both PascalCase keys (architect.ConversationResult, no json
// tags) and lowercase keys (orchestrator.ConversationResult, has json tags).
func formatConversationResult(payload any) (string, bool) {
	values, ok := toMap(payload)
	if !ok {
		return "", false
	}
	response := conversationResultResponse(values)
	if response == "" {
		return "", false
	}
	if !conversationResultHasIntent(values) {
		return "", false
	}
	return response, true
}

// conversationResultResponse extracts the response text from a
// ConversationResult map, checking both PascalCase and lowercase keys.
func conversationResultResponse(values map[string]any) string {
	if text := strings.TrimSpace(stringFromKey(values, "Response")); text != "" {
		return text
	}
	return strings.TrimSpace(stringFromKey(values, "response"))
}

// conversationResultHasIntent checks for the Intent field in either case.
func conversationResultHasIntent(values map[string]any) bool {
	if _, ok := values["Intent"]; ok {
		return true
	}
	_, ok := values["intent"]
	return ok
}

func unwrapResponseEnvelope(payload any) any {
	envelope, ok := toMap(payload)
	if !ok || !looksLikeResponseEnvelope(envelope) {
		return payload
	}
	return envelope["Data"]
}

func normalizeStructuredPayload(payload any) any {
	payload = decodeStructuredJSONPayload(payload)
	payload = unwrapResponseEnvelope(payload)
	payload = decodeStructuredJSONPayload(payload)
	return payload
}

func extractTurnEnvelopeResult(payload any) (any, bool) {
	values, ok := toMap(payload)
	if !ok {
		return payload, false
	}
	if result, ok := values["result"]; ok {
		if _, hasAction := values["action"]; hasAction || values["processed"] != nil {
			return decodeStructuredJSONPayload(result), true
		}
	}
	if result, ok := values["Result"]; ok {
		if _, hasAction := values["Action"]; hasAction || values["Processed"] != nil {
			return decodeStructuredJSONPayload(result), true
		}
	}
	if _, hasAction := values["action"]; hasAction {
		return nil, true
	}
	if _, hasAction := values["Action"]; hasAction {
		return nil, true
	}
	if values["processed"] != nil || values["Processed"] != nil {
		return nil, true
	}
	return payload, false
}

func decodeStructuredJSONPayload(payload any) any {
	switch typed := payload.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if len(trimmed) == 0 {
			return payload
		}
		if trimmed[0] != '{' && trimmed[0] != '[' {
			return payload
		}
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return payload
		}
		return decoded
	case []byte:
		trimmed := strings.TrimSpace(string(typed))
		if len(trimmed) == 0 {
			return payload
		}
		if trimmed[0] != '{' && trimmed[0] != '[' {
			return payload
		}
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return payload
		}
		return decoded
	default:
		return payload
	}
}

func looksLikeResponseEnvelope(values map[string]any) bool {
	if values == nil {
		return false
	}
	_, hasData := values["Data"]
	_, hasSuccess := values["Success"]
	_, hasID := values["ID"]
	return hasData && hasSuccess && hasID
}

func formatArchitectPlanSummary(payload any) (string, bool) {
	planMap, ok := toMap(payload)
	if !ok || !looksLikeArchitectPlan(planMap) {
		return "", false
	}
	if text := architectExplicitUserResponse(planMap); text != "" {
		return text, true
	}
	if architectNeedsClarification(planMap) {
		return architectClarificationPrompt(planMap), true
	}
	return architectConversationalPlan(planMap), true
}

func architectExplicitUserResponse(plan map[string]any) string {
	for _, key := range []string{"UserResponse", "user_response", "ResponseText", "response_text"} {
		if text := strings.TrimSpace(stringFromKey(plan, key)); text != "" {
			return text
		}
	}
	return ""
}

func looksLikeArchitectPlan(values map[string]any) bool {
	if values == nil {
		return false
	}
	if _, ok := values["ClarificationQuestions"]; ok {
		return true
	}
	if _, ok := values["Requirements"]; ok {
		return true
	}
	if _, ok := values["Tasks"]; ok {
		return true
	}
	_, hasStatus := values["Status"]
	_, hasWorkflow := values["Workflow"]
	return hasStatus && hasWorkflow
}

func architectNeedsClarification(plan map[string]any) bool {
	if status, ok := plan["Status"].(string); ok && status == "clarifying" {
		return true
	}
	return len(architectClarificationQuestions(plan)) > 0
}

func architectClarificationPrompt(plan map[string]any) string {
	lines := make([]string, 0, 8)
	scope := architectPlanScope(plan)
	if narrative := architectRecommendationNarrative(plan); narrative != "" {
		lines = append(lines, narrative)
	}
	recommendations := architectRecommendations(plan)
	if len(recommendations) > 0 {
		lines = append(lines, "Recommended starting direction:")
		for _, item := range recommendations {
			lines = append(lines, "- "+item)
		}
	}
	tradeoffs := architectTradeoffs(plan)
	if len(tradeoffs) > 0 {
		lines = append(lines, "Key tradeoffs to decide:")
		for _, item := range tradeoffs {
			lines = append(lines, "- "+item)
		}
	}
	assumptions := architectAssumptions(plan)
	if len(assumptions) > 0 {
		lines = append(lines, "Assumptions:")
		for _, assumption := range assumptions {
			lines = append(lines, "- "+assumption)
		}
	}
	questions := architectClarificationQuestions(plan)
	if len(questions) > 0 {
		lines = append(lines, architectClarificationLead(scope, len(questions)))
		for idx, question := range questions {
			lines = append(lines, fmt.Sprintf("%d. %s", idx+1, question))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, fmt.Sprintf("I can finalize the %s plan once we align on one or two decisions.", scope))
	}
	return strings.Join(lines, "\n")
}

func architectClarificationLead(scope string, count int) string {
	if count <= 1 {
		return fmt.Sprintf("One decision will let me finalize the %s plan cleanly:", scope)
	}
	return fmt.Sprintf("To finalize the %s plan, I need decisions on:", scope)
}

func architectConversationalPlan(plan map[string]any) string {
	scope := architectPlanScope(plan)
	firstTask := architectFirstTaskName(plan)
	if firstTask == "" {
		return fmt.Sprintf("I drafted a plan for %s. Which constraint or priority should I optimize first?", scope)
	}
	lines := []string{
		fmt.Sprintf("I drafted a concrete plan for %s.", scope),
		fmt.Sprintf("Best starting slice: %s.", firstTask),
	}
	if assumptions := architectAssumptions(plan); len(assumptions) > 0 {
		lines = append(lines, "Current assumptions:")
		for _, assumption := range assumptions {
			lines = append(lines, "- "+assumption)
		}
	}
	lines = append(lines, "Should I refine this further, or package it for execution?")
	return strings.Join(lines, "\n")
}

func architectPlanScope(plan map[string]any) string {
	requirements := mapFromKey(plan, "Requirements")
	scope := strings.TrimSpace(stringFromKey(requirements, "Scope"))
	if scope != "" {
		return scope
	}
	query := strings.TrimSpace(stringFromKey(plan, "Query"))
	if query != "" {
		return query
	}
	return "implementation"
}

func architectFirstTaskName(plan map[string]any) string {
	tasks := sliceFromKey(plan, "Tasks")
	if len(tasks) == 0 {
		return ""
	}
	task, ok := toMap(tasks[0])
	if !ok {
		return ""
	}
	return stringFromKey(task, "Name")
}

func architectClarificationQuestions(plan map[string]any) []string {
	questions := stringSliceFromKey(plan, "ClarificationQuestions")
	if len(questions) > 0 {
		return limitAndUniqueQuestions(questions)
	}
	requirements := mapFromKey(plan, "Requirements")
	return limitAndUniqueQuestions(stringSliceFromMap(requirements, "Metadata", "clarification_questions"))
}

func architectAssumptions(plan map[string]any) []string {
	return limitAndUniqueQuestions(stringSliceFromKey(plan, "Assumptions"))
}

func architectRecommendations(plan map[string]any) []string {
	requirements := mapFromKey(plan, "Requirements")
	return limitAndUniqueQuestions(stringSliceFromMap(requirements, "Metadata", "provisional_recommendations"))
}

func architectTradeoffs(plan map[string]any) []string {
	requirements := mapFromKey(plan, "Requirements")
	return limitAndUniqueQuestions(stringSliceFromMap(requirements, "Metadata", "tradeoffs"))
}

func architectRecommendationNarrative(plan map[string]any) string {
	requirements := mapFromKey(plan, "Requirements")
	metadata := mapFromKey(requirements, "Metadata")
	return strings.TrimSpace(stringFromKey(metadata, "recommendation_narrative"))
}

func limitAndUniqueQuestions(values []string) []string {
	trimmed := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		text := strings.TrimSpace(value)
		if text == "" {
			continue
		}
		key := strings.ToLower(text)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		trimmed = append(trimmed, text)
	}
	return trimmed
}

func architectStatusText(raw any) string {
	switch value := raw.(type) {
	case float64:
		return architectStatusFromInt(int(value))
	case int:
		return architectStatusFromInt(value)
	case int64:
		return architectStatusFromInt(int(value))
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return ""
		}
		if parsed, err := strconv.Atoi(trimmed); err == nil {
			return architectStatusFromInt(parsed)
		}
		return strings.ToLower(trimmed)
	default:
		return ""
	}
}

func architectStatusFromInt(code int) string {
	statuses := []string{
		"pending",
		"analyzing",
		"consulting",
		"designing",
		"generating",
		"orchestrating",
		"ready",
		"executing",
		"completed",
		"failed",
	}
	if code < 0 || code >= len(statuses) {
		return "unknown"
	}
	return statuses[code]
}

// formatArchitecturePayload detects a bare SolutionArchitecture (has
// Components + Patterns) and returns its Description as human-readable text.
// Safety net: with normal routing this type should not reach the bridge
// unformatted, but this prevents raw JSON if it ever does.
func formatArchitecturePayload(payload any) (string, bool) {
	values, ok := toMap(payload)
	if !ok {
		return "", false
	}
	_, hasComponents := values["Components"]
	_, hasPatterns := values["Patterns"]
	if !hasComponents || !hasPatterns {
		return "", false
	}
	desc := strings.TrimSpace(stringFromKey(values, "Description"))
	if desc == "" {
		return "", false
	}
	name := strings.TrimSpace(stringFromKey(values, "Name"))
	if name != "" {
		return fmt.Sprintf("Architecture: %s — %s", name, desc), true
	}
	return desc, true
}

func formatCapabilitiesPayload(payload any) (string, bool) {
	values, ok := toMap(payload)
	if !ok {
		return "", false
	}
	intents := sliceFromKey(values, "supported_intents")
	if len(intents) == 0 {
		return "", false
	}
	agent := stringFromKey(values, "agent")
	if agent == "" {
		agent = "agent"
	}
	intentLabels := make([]string, 0, len(intents))
	for _, intent := range intents {
		text := strings.TrimSpace(fmt.Sprint(intent))
		if text != "" {
			intentLabels = append(intentLabels, text)
		}
	}
	if len(intentLabels) == 0 {
		return "", false
	}
	return fmt.Sprintf(
		"%s supports: %s.",
		titleFirst(agent),
		strings.Join(intentLabels, ", "),
	), true
}

func formatAnswerPayload(payload any) (string, bool) {
	values, ok := toMap(payload)
	if !ok {
		return "", false
	}
	for _, key := range []string{"answer", "content"} {
		answer := strings.TrimSpace(stringFromKey(values, key))
		if answer != "" {
			return answer, true
		}
	}
	return "", false
}

func formatTesterStagePayload(payload any) (string, bool) {
	values, ok := toMap(payload)
	if !ok || !looksLikeTesterStageResult(values) {
		return "", false
	}
	lines := make([]string, 0, 3)
	created := stringSliceFromKey(values, "CreatedFiles")
	if len(created) == 0 {
		created = stringSliceFromKey(values, "created_files")
	}
	if len(created) > 0 {
		lines = append(lines, fmt.Sprintf("Created test artifacts: %s.", strings.Join(created, ", ")))
	}
	suite := mapFromKey(values, "SuiteResult")
	if suite == nil {
		suite = mapFromKey(values, "suite_result")
	}
	if suite != nil {
		total := intFromKey(suite, "total_tests")
		if total == 0 {
			total = intFromKey(suite, "TotalTests")
		}
		passed := firstIntValue(suite, "passed", "Passed")
		failed := firstIntValue(suite, "failed", "Failed")
		skipped := firstIntValue(suite, "skipped", "Skipped")
		errors := firstIntValue(suite, "errors", "Errors")
		if total == 0 {
			total = passed + failed + skipped + errors
		}
		if total > 0 {
			lines = append(lines, fmt.Sprintf(
				"Latest test run: %d passed, %d failed, %d skipped, %d errors out of %d.",
				passed, failed, skipped, errors, total,
			))
		} else if name := strings.TrimSpace(firstStringValue(suite, "name", "Name")); name != "" {
			lines = append(lines, fmt.Sprintf("Executed test suite %q.", name))
		}
	}
	plan := mapFromKey(values, "Plan")
	if plan == nil {
		plan = mapFromKey(values, "plan")
	}
	if len(lines) == 0 && plan != nil {
		if rationale := strings.TrimSpace(firstStringValue(plan, "rationale", "Rationale")); rationale != "" {
			lines = append(lines, fmt.Sprintf("Prepared a test plan: %s.", rationale))
		} else {
			lines = append(lines, "Prepared a concrete test plan.")
		}
	}
	if len(lines) == 0 {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

func formatInspectorStagePayload(payload any) (string, bool) {
	values, ok := toMap(payload)
	if !ok || !looksLikeInspectorStageResult(values) {
		return "", false
	}

	lines := make([]string, 0, 2)
	criteria := mapFromKey(values, "Criteria")
	if criteria == nil {
		criteria = mapFromKey(values, "criteria")
	}
	if criteria != nil {
		successCriteria := sliceFromKey(criteria, "success_criteria")
		if len(successCriteria) == 0 {
			successCriteria = sliceFromKey(criteria, "SuccessCriteria")
		}
		qualityGates := sliceFromKey(criteria, "quality_gates")
		if len(qualityGates) == 0 {
			qualityGates = sliceFromKey(criteria, "QualityGates")
		}
		constraints := sliceFromKey(criteria, "constraints")
		if len(constraints) == 0 {
			constraints = sliceFromKey(criteria, "Constraints")
		}
		lines = append(lines, fmt.Sprintf(
			"Defined criteria contract: %d success criteria, %d quality gates, %d constraints.",
			len(successCriteria),
			len(qualityGates),
			len(constraints),
		))
	}

	result := mapFromKey(values, "Result")
	if result == nil {
		result = mapFromKey(values, "result")
	}
	if result != nil {
		status := "failed"
		if boolFromKey(result, "passed") || boolFromKey(result, "Passed") {
			status = "passed"
		}
		criteriaMet := sliceFromKey(result, "criteria_met")
		if len(criteriaMet) == 0 {
			criteriaMet = sliceFromKey(result, "CriteriaMet")
		}
		criteriaFailed := sliceFromKey(result, "criteria_failed")
		if len(criteriaFailed) == 0 {
			criteriaFailed = sliceFromKey(result, "CriteriaFailed")
		}
		issues := sliceFromKey(result, "issues")
		if len(issues) == 0 {
			issues = sliceFromKey(result, "Issues")
		}
		loopCount := firstIntValue(result, "loop_count", "LoopCount")
		lines = append(lines, fmt.Sprintf(
			"Latest validation %s: %d criteria met, %d failed, %d issues, %d feedback loops.",
			status,
			len(criteriaMet),
			len(criteriaFailed),
			len(issues),
			loopCount,
		))
	}

	if len(lines) == 0 {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

func looksLikeTesterStageResult(values map[string]any) bool {
	if values == nil {
		return false
	}
	_, hasPlan := values["Plan"]
	_, hasPlanJSON := values["plan"]
	_, hasCreated := values["CreatedFiles"]
	_, hasCreatedJSON := values["created_files"]
	_, hasSuite := values["SuiteResult"]
	_, hasSuiteJSON := values["suite_result"]
	return hasPlan || hasPlanJSON || hasCreated || hasCreatedJSON || hasSuite || hasSuiteJSON
}

func looksLikeInspectorStageResult(values map[string]any) bool {
	if values == nil {
		return false
	}
	_, hasCriteria := values["Criteria"]
	_, hasCriteriaJSON := values["criteria"]
	_, hasResult := values["Result"]
	_, hasResultJSON := values["result"]
	return hasCriteria || hasCriteriaJSON || hasResult || hasResultJSON
}

func formatGenericMapSummary(payload any) (string, bool) {
	values, ok := toMap(payload)
	if !ok || len(values) == 0 {
		return "", false
	}
	if len(values) > 6 {
		return "", false
	}
	lines := make([]string, 0, len(values))
	for key, value := range values {
		scalar, ok := scalarString(value)
		if !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", key, scalar))
	}
	if len(lines) == 0 {
		return "", false
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), true
}

func firstIntValue(values map[string]any, keys ...string) int {
	for _, key := range keys {
		if value := intFromKey(values, key); value != 0 {
			return value
		}
	}
	return 0
}

func firstStringValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringFromKey(values, key)); value != "" {
			return value
		}
	}
	return ""
}

func scalarString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return fmt.Sprintf("%t", typed), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case int:
		return strconv.Itoa(typed), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	default:
		return "", false
	}
}

func toMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, false
	case map[string]any:
		return typed, true
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func valueFromKey(values map[string]any, key string) any {
	if values == nil {
		return nil
	}
	return values[key]
}

func mapFromKey(values map[string]any, key string) map[string]any {
	nested, ok := toMap(valueFromKey(values, key))
	if !ok {
		return nil
	}
	return nested
}

func sliceFromKey(values map[string]any, key string) []any {
	value := valueFromKey(values, key)
	switch typed := value.(type) {
	case []any:
		return typed
	case nil:
		return nil
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		var result []any
		if err := json.Unmarshal(encoded, &result); err != nil {
			return nil
		}
		return result
	}
}

func stringFromKey(values map[string]any, key string) string {
	text := strings.TrimSpace(fmt.Sprint(valueFromKey(values, key)))
	if text == "<nil>" {
		return ""
	}
	return text
}

func intFromKey(values map[string]any, key string) int {
	value := valueFromKey(values, key)
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

func boolFromKey(values map[string]any, key string) bool {
	value := valueFromKey(values, key)
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func stringSliceFromKey(values map[string]any, key string) []string {
	value := valueFromKey(values, key)
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text == "" || text == "<nil>" {
				continue
			}
			result = append(result, text)
		}
		return result
	default:
		return nil
	}
}

func stringSliceFromMap(values map[string]any, parent string, key string) []string {
	parentMap := mapFromKey(values, parent)
	if len(parentMap) == 0 {
		return nil
	}
	return stringSliceFromKey(parentMap, key)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func titleFirst(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return strings.ToUpper(trimmed[:1]) + trimmed[1:]
}
