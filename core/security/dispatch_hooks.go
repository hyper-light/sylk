package security

import (
	"context"
)

type DispatchHookData struct {
	SourceAgent string
	TargetAgent string
	Message     string
	SessionID   string
}

type InterAgentSecretHookHandler struct {
	sanitizer   *SecretSanitizer
	auditLogger *AuditLogger
}

func NewInterAgentSecretHook(sanitizer *SecretSanitizer, logger *AuditLogger) *InterAgentSecretHookHandler {
	return &InterAgentSecretHookHandler{
		sanitizer:   sanitizer,
		auditLogger: logger,
	}
}

func (h *InterAgentSecretHookHandler) Name() string {
	return "secret_sanitization_transit"
}

func (h *InterAgentSecretHookHandler) Type() HookType {
	return HookTypePreDispatch
}

func (h *InterAgentSecretHookHandler) Priority() HookPriority {
	return HookPriorityLast
}

func (h *InterAgentSecretHookHandler) Handle(ctx context.Context, data *DispatchHookData) (*DispatchHookData, error) {
	if data == nil || data.Message == "" {
		return data, nil
	}

	sanitized, count := h.sanitizer.SanitizeToolOutput(data.Message)
	if count > 0 {
		data.Message = sanitized
		h.logRedaction(count, data)
	}

	return data, nil
}

func (h *InterAgentSecretHookHandler) logRedaction(count int, data *DispatchHookData) {
	if h.auditLogger == nil {
		return
	}

	entry := NewAuditEntry(AuditCategorySecret, "secret_redacted_transit", "redact")
	entry.SessionID = data.SessionID
	entry.Details = map[string]interface{}{
		"source_agent":    data.SourceAgent,
		"target_agent":    data.TargetAgent,
		"redaction_count": count,
	}
	_ = h.auditLogger.Log(entry)
}
