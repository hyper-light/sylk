package security

import (
	"context"
	"fmt"
	"regexp"
)

type HookType string

const (
	HookTypePrePrompt   HookType = "pre_prompt"
	HookTypePostPrompt  HookType = "post_prompt"
	HookTypePreTool     HookType = "pre_tool"
	HookTypePostTool    HookType = "post_tool"
	HookTypePreIndex    HookType = "pre_index"
	HookTypePreDispatch HookType = "pre_dispatch"
)

type HookPriority int

const (
	HookPriorityFirst  HookPriority = 0
	HookPriorityNormal HookPriority = 50
	HookPriorityLast   HookPriority = 100
)

type PromptHookData struct {
	Prompt    string
	SessionID string
	AgentID   string
}

type ToolCallHookData struct {
	ToolName   string
	Parameters map[string]interface{}
	Output     string
	SessionID  string
	AgentID    string
}

type SecretDetectedError struct {
	Message  string
	Findings []SecretFinding
}

func (e *SecretDetectedError) Error() string {
	return e.Message
}

type EnvVarInjectionError struct {
	Message   string
	ToolName  string
	ParamName string
}

func (e *EnvVarInjectionError) Error() string {
	return e.Message
}

type PrePromptSecretHookHandler struct {
	sanitizer   *SecretSanitizer
	auditLogger *AuditLogger
}

func NewPrePromptSecretHook(sanitizer *SecretSanitizer, logger *AuditLogger) *PrePromptSecretHookHandler {
	return &PrePromptSecretHookHandler{
		sanitizer:   sanitizer,
		auditLogger: logger,
	}
}

func (h *PrePromptSecretHookHandler) Name() string {
	return "secret_detection"
}

func (h *PrePromptSecretHookHandler) Type() HookType {
	return HookTypePrePrompt
}

func (h *PrePromptSecretHookHandler) Priority() HookPriority {
	return HookPriorityFirst
}

func (h *PrePromptSecretHookHandler) Handle(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
	if data == nil || data.Prompt == "" {
		return data, nil
	}

	detection := h.sanitizer.CheckUserPrompt(data.Prompt)
	if !detection.HasFindings() {
		return data, nil
	}

	h.logSecretDetection(detection, data)

	return nil, &SecretDetectedError{
		Message:  h.buildUserMessage(detection),
		Findings: detection.Findings,
	}
}

func (h *PrePromptSecretHookHandler) logSecretDetection(detection *SecretDetection, data *PromptHookData) {
	if h.auditLogger == nil {
		return
	}

	patternNames := make([]string, 0, len(detection.Findings))
	for _, f := range detection.Findings {
		patternNames = append(patternNames, f.PatternName)
	}

	entry := NewAuditEntry(AuditCategorySecret, "secret_detected_prompt", "reject")
	entry.Severity = AuditSeveritySecurity
	entry.SessionID = data.SessionID
	entry.AgentID = data.AgentID
	entry.Details = map[string]interface{}{
		"patterns_matched": patternNames,
		"finding_count":    len(detection.Findings),
	}
	_ = h.auditLogger.Log(entry)
}

func (h *PrePromptSecretHookHandler) buildUserMessage(detection *SecretDetection) string {
	count := len(detection.Findings)
	severity := detection.HighestSeverity()

	msg := fmt.Sprintf("Detected %d potential secret(s) in your prompt (severity: %s). ", count, severity)
	msg += "For security, prompts containing secrets are blocked. "
	msg += "Consider using environment variables instead: reference them as $VAR_NAME."
	return msg
}

type PostToolSecretHookHandler struct {
	sanitizer   *SecretSanitizer
	auditLogger *AuditLogger
}

func NewPostToolSecretHook(sanitizer *SecretSanitizer, logger *AuditLogger) *PostToolSecretHookHandler {
	return &PostToolSecretHookHandler{
		sanitizer:   sanitizer,
		auditLogger: logger,
	}
}

func (h *PostToolSecretHookHandler) Name() string {
	return "secret_redaction_output"
}

func (h *PostToolSecretHookHandler) Type() HookType {
	return HookTypePostTool
}

func (h *PostToolSecretHookHandler) Priority() HookPriority {
	return HookPriorityLast
}

func (h *PostToolSecretHookHandler) Handle(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, error) {
	if data == nil || data.Output == "" {
		return data, nil
	}

	sanitized, count := h.sanitizer.SanitizeToolOutput(data.Output)
	if count > 0 {
		data.Output = sanitized
		h.logRedaction(count, data)
	}

	return data, nil
}

func (h *PostToolSecretHookHandler) logRedaction(count int, data *ToolCallHookData) {
	if h.auditLogger == nil {
		return
	}

	entry := NewAuditEntry(AuditCategorySecret, "secret_redacted_output", "redact")
	entry.SessionID = data.SessionID
	entry.AgentID = data.AgentID
	entry.Target = data.ToolName
	entry.Details = map[string]interface{}{
		"redaction_count": count,
	}
	_ = h.auditLogger.Log(entry)
}

var envVarPattern = regexp.MustCompile(`\$\{?([A-Z_][A-Z0-9_]*)\}?`)

type PreToolEnvVarHookHandler struct {
	sanitizer   *SecretSanitizer
	auditLogger *AuditLogger
}

func NewPreToolEnvVarHook(sanitizer *SecretSanitizer, logger *AuditLogger) *PreToolEnvVarHookHandler {
	return &PreToolEnvVarHookHandler{
		sanitizer:   sanitizer,
		auditLogger: logger,
	}
}

func (h *PreToolEnvVarHookHandler) Name() string {
	return "env_var_isolation"
}

func (h *PreToolEnvVarHookHandler) Type() HookType {
	return HookTypePreTool
}

func (h *PreToolEnvVarHookHandler) Priority() HookPriority {
	return HookPriorityFirst
}

func (h *PreToolEnvVarHookHandler) Handle(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, error) {
	if data == nil || data.Parameters == nil {
		return data, nil
	}

	for name, value := range data.Parameters {
		if err := h.checkParameter(name, value, data); err != nil {
			return nil, err
		}
	}

	return data, nil
}

func (h *PreToolEnvVarHookHandler) checkParameter(name string, value interface{}, data *ToolCallHookData) error {
	strValue, ok := value.(string)
	if !ok {
		return nil
	}

	detection := h.sanitizer.CheckUserPrompt(strValue)
	if !detection.HasFindings() {
		return nil
	}

	h.logInjectionAttempt(name, data)

	return &EnvVarInjectionError{
		Message:   h.buildErrorMessage(name),
		ToolName:  data.ToolName,
		ParamName: name,
	}
}

func (h *PreToolEnvVarHookHandler) logInjectionAttempt(paramName string, data *ToolCallHookData) {
	if h.auditLogger == nil {
		return
	}

	entry := NewAuditEntry(AuditCategorySecret, "env_var_injection_attempt", "block")
	entry.Severity = AuditSeveritySecurity
	entry.SessionID = data.SessionID
	entry.AgentID = data.AgentID
	entry.Target = data.ToolName
	entry.Details = map[string]interface{}{
		"parameter_name": paramName,
	}
	_ = h.auditLogger.Log(entry)
}

func (h *PreToolEnvVarHookHandler) buildErrorMessage(paramName string) string {
	return fmt.Sprintf("Parameter %q contains a potential secret. Tools should read credentials from environment variables internally, not receive them as parameters.", paramName)
}
