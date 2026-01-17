package librarian

import (
	"context"

	"github.com/adalundhe/sylk/core/security"
)

type IndexHookData struct {
	FilePath string
	Content  []byte
	Metadata map[string]interface{}
}

type LibrarianPreIndexHookHandler struct {
	sanitizer   *security.SecretSanitizer
	auditLogger *security.AuditLogger
}

func NewLibrarianPreIndexHook(sanitizer *security.SecretSanitizer, logger *security.AuditLogger) *LibrarianPreIndexHookHandler {
	return &LibrarianPreIndexHookHandler{
		sanitizer:   sanitizer,
		auditLogger: logger,
	}
}

func (h *LibrarianPreIndexHookHandler) Name() string {
	return "secret_sanitization_index"
}

func (h *LibrarianPreIndexHookHandler) Type() security.HookType {
	return security.HookTypePreIndex
}

func (h *LibrarianPreIndexHookHandler) Priority() security.HookPriority {
	return security.HookPriorityFirst
}

func (h *LibrarianPreIndexHookHandler) Handle(ctx context.Context, data *IndexHookData) (*IndexHookData, error) {
	if data == nil {
		return data, nil
	}

	if data.Metadata == nil {
		data.Metadata = make(map[string]interface{})
	}

	sanitized, result := h.sanitizer.SanitizeForIndex(data.FilePath, data.Content)
	data.Content = sanitized

	h.updateMetadata(data, result)

	if result.IsStubbed || result.RedactionCount > 0 {
		h.logSanitization(data.FilePath, result)
	}

	return data, nil
}

func (h *LibrarianPreIndexHookHandler) updateMetadata(data *IndexHookData, result *security.SanitizeResult) {
	data.Metadata["sanitized"] = result.Sanitized
	data.Metadata["redaction_count"] = result.RedactionCount
	data.Metadata["skipped"] = result.IsStubbed

	if len(result.MatchedPatterns) > 0 {
		data.Metadata["matched_patterns"] = result.MatchedPatterns
	}
}

func (h *LibrarianPreIndexHookHandler) logSanitization(filePath string, result *security.SanitizeResult) {
	if h.auditLogger == nil {
		return
	}

	eventType := "index_redaction"
	if result.IsStubbed {
		eventType = "index_skipped"
	}

	entry := security.NewAuditEntry(security.AuditCategorySecret, eventType, "sanitize")
	entry.Target = filePath
	entry.Details = map[string]interface{}{
		"stubbed":          result.IsStubbed,
		"redaction_count":  result.RedactionCount,
		"matched_patterns": result.MatchedPatterns,
	}
	_ = h.auditLogger.Log(entry)
}
