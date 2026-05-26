package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	claimsPresentationArtifactsSeen       = "claims_presentation_artifacts_seen"
	claimsPresentationMessagesEmitted     = "claims_presentation_messages_emitted"
	claimsPresentationMessagesDropped     = "claims_presentation_messages_dropped"
	claimsPresentationReplacements        = "claims_presentation_replacements"
	claimsPresentationInvalid             = "claims_presentation_invalid"
	claimsPresentationDereferenceFailures = "claims_presentation_dereference_failures"

	presentationMetricUnknown = "unknown"
	presentationMaxContent    = 64 * 1024
)

type presentationMetricKey struct {
	Name    string
	Surface string
	Format  string
	Reason  string
}

// PresentationMetric is a point-in-time bridge counter sample for claims
// presentation observability.
type PresentationMetric struct {
	Name    string
	Surface string
	Format  string
	Reason  string
	Count   int64
}

type presentationDiagnosticRecord struct {
	SessionID            string
	ClaimID              string
	AgentID              string
	SourceArtifactID     string
	SourceArtifactKind   string
	DiagnosticArtifactID string
	Surface              string
	Format               string
	Reason               string
	Reference            string
	CreatedAt            time.Time
}

// PresentationMetricsSnapshot returns the bridge's per-session presentation
// counters. Labels are intentionally low-cardinality: surface, format, reason.
func (b *ClaimsBridge) PresentationMetricsSnapshot() []PresentationMetric {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]PresentationMetric, 0, len(b.presentationMetrics))
	for key, count := range b.presentationMetrics {
		out = append(out, PresentationMetric{
			Name:    key.Name,
			Surface: key.Surface,
			Format:  key.Format,
			Reason:  key.Reason,
			Count:   count,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].Surface != out[j].Surface {
			return out[i].Surface < out[j].Surface
		}
		if out[i].Format != out[j].Format {
			return out[i].Format < out[j].Format
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

func (b *ClaimsBridge) observePresentationMetricLocked(name, surface, format, reason string) {
	if b == nil {
		return
	}
	if b.presentationMetrics == nil {
		b.presentationMetrics = make(map[presentationMetricKey]int64)
	}
	key := presentationMetricKey{
		Name:    strings.TrimSpace(name),
		Surface: firstNonBlank(strings.TrimSpace(surface), presentationMetricUnknown),
		Format:  firstNonBlank(strings.TrimSpace(format), presentationMetricUnknown),
		Reason:  strings.TrimSpace(reason),
	}
	if key.Name == "" {
		return
	}
	b.presentationMetrics[key]++
}

func (b *ClaimsBridge) recordPresentationDropLocked(sourceType, sourceID string, presentation *claims.Presentation, reason string) {
	surface, format := presentationMetricLabels(presentation)
	reason = firstNonBlank(strings.TrimSpace(reason), "unspecified")
	b.observePresentationMetricLocked(claimsPresentationMessagesDropped, surface, format, reason)
	b.debug("presentation_drop",
		"source_type", strings.TrimSpace(sourceType),
		"source_id", strings.TrimSpace(sourceID),
		"surface", surface,
		"format", format,
		"reason", reason,
	)
}

func (b *ClaimsBridge) recordPresentationInvalidLocked(sourceType, sourceID string, presentation *claims.Presentation, reason, detail string) {
	surface, format := presentationMetricLabels(presentation)
	reason = firstNonBlank(strings.TrimSpace(reason), "invalid_presentation")
	b.observePresentationMetricLocked(claimsPresentationInvalid, surface, format, reason)
	b.observePresentationMetricLocked(claimsPresentationMessagesDropped, surface, format, reason)
	b.debug("presentation_invalid",
		"source_type", strings.TrimSpace(sourceType),
		"source_id", strings.TrimSpace(sourceID),
		"surface", surface,
		"format", format,
		"reason", reason,
		"detail", trimLogValue(detail, 240),
	)
}

func presentationMetricLabels(p *claims.Presentation) (string, string) {
	cp := claims.NormalizePresentation(p)
	if cp == nil {
		return presentationMetricUnknown, presentationMetricUnknown
	}
	surface := presentationMetricUnknown
	if claims.PresentationMatches(cp, string(claims.PresentationAudienceUser), string(claims.PresentationSurfaceChat)) {
		surface = string(claims.PresentationSurfaceChat)
	} else if len(cp.Surfaces) > 0 {
		surface = strings.TrimSpace(string(cp.Surfaces[0]))
	}
	format := strings.TrimSpace(string(cp.Format))
	if format == "" {
		format = presentationMetricUnknown
	}
	return surface, format
}

func validateBridgeRenderablePresentation(p *claims.Presentation) error {
	cp := claims.NormalizePresentation(p)
	if cp == nil {
		return fmt.Errorf("presentation is empty")
	}
	if err := claims.ValidatePresentation(cp); err != nil {
		return err
	}
	switch presentationFormat(cp) {
	case string(claims.PresentationFormatMarkdown),
		string(claims.PresentationFormatText),
		string(claims.PresentationFormatJSON),
		string(claims.PresentationFormatDiff),
		string(claims.PresentationFormatTable):
		return nil
	default:
		return fmt.Errorf("unsupported presentation format %q", presentationFormat(cp))
	}
}

func (b *ClaimsBridge) presentationDiagnosticForArtifactLocked(sessionID, claimID string, art *claims.Artifact, reason, detail string) (*presentationDiagnosticRecord, *msg.ClaimPresentationMsg) {
	if b == nil || art == nil {
		return nil, nil
	}
	sourceID := strings.TrimSpace(art.ID)
	if sourceID == "" {
		return nil, nil
	}
	key := sourceID + "|" + strings.TrimSpace(reason)
	if b.presentationDiagnostics == nil {
		b.presentationDiagnostics = make(map[string]struct{})
	}
	if _, exists := b.presentationDiagnostics[key]; exists {
		return nil, nil
	}
	b.presentationDiagnostics[key] = struct{}{}
	surface, format := presentationMetricLabels(art.Presentation)
	diagnosticID := "presentation-diagnostic-" + sanitizePresentationID(sourceID) + "-" + sanitizePresentationID(reason)
	reference := fmt.Sprintf("Could not render %s artifact %s: %s", firstNonBlank(strings.TrimSpace(art.Kind), "presentation"), sourceID, firstNonBlank(trimLogValue(detail, 240), reason))
	reference = safeClaimPresentationContent(diagnosticID, reference, string(claims.PresentationFormatText))
	meta := b.metaForClaimLocked(claimID)
	cycleID := firstNonBlank(meta.CycleID, claimID)
	record := &presentationDiagnosticRecord{
		SessionID:            sessionID,
		ClaimID:              claimID,
		AgentID:              firstNonBlank(strings.TrimSpace(art.AgentID), claimContextActor(meta, ""), claimsBridgeAgentID),
		SourceArtifactID:     sourceID,
		SourceArtifactKind:   strings.TrimSpace(art.Kind),
		DiagnosticArtifactID: diagnosticID,
		Surface:              surface,
		Format:               format,
		Reason:               strings.TrimSpace(reason),
		Reference:            reference,
		CreatedAt:            nonZeroTime(art.Created),
	}
	if cycleID == "" {
		return record, nil
	}
	fallback := &msg.ClaimPresentationMsg{
		SessionID:   sessionID,
		CycleID:     cycleID,
		ClaimID:     claimID,
		SourceType:  "artifact",
		SourceID:    diagnosticID,
		TestamentID: strings.TrimSpace(art.TestamentID),
		AgentID:     record.AgentID,
		Title:       "Presentation diagnostic",
		Content:     reference,
		Format:      string(claims.PresentationFormatText),
		Placement:   string(claims.PresentationPlacementInline),
		Metadata: map[string]any{
			"diagnostic":         true,
			"source_artifact_id": sourceID,
			"surface":            surface,
			"format":             format,
			"reason":             strings.TrimSpace(reason),
		},
		CreatedAt: record.CreatedAt,
		Sequence:  art.Sequence,
	}
	b.observePresentationMetricLocked(claimsPresentationMessagesEmitted, surface, string(claims.PresentationFormatText), "fallback")
	return record, fallback
}

func (b *ClaimsBridge) recordPresentationDiagnostics(records []presentationDiagnosticRecord) {
	if b == nil || len(records) == 0 {
		return
	}
	board, _ := b.currentBoard()
	if board == nil {
		return
	}
	for _, record := range records {
		if strings.TrimSpace(record.DiagnosticArtifactID) == "" || strings.TrimSpace(record.SourceArtifactID) == "" {
			continue
		}
		relation := claims.Relation{
			Related:      record.SourceArtifactID,
			RelatedType:  claims.RelatedTypeArtifact,
			Relationship: claims.RelationshipDerivedFrom,
		}
		metadata := map[string]any{
			"diagnostic_type":      "presentation_failure",
			"source_artifact_id":   record.SourceArtifactID,
			"source_artifact_kind": record.SourceArtifactKind,
			"surface":              record.Surface,
			"format":               record.Format,
			"reason":               record.Reason,
		}
		if record.ClaimID != "" {
			metadata["claim_id"] = record.ClaimID
		}
		testament := claims.Testament{
			AgentID:    claimsBridgeAgentID,
			Summary:    record.Reference,
			Confidence: "committed",
			Relations:  []claims.Relation{relation},
			Artifacts: []*claims.Artifact{{
				ID:        record.DiagnosticArtifactID,
				AgentID:   claimsBridgeAgentID,
				Kind:      claims.ArtifactKindErrorDiagnostic,
				Reference: record.Reference,
				Metadata:  metadata,
				Relations: []claims.Relation{relation},
			}},
		}
		if err := board.SubmitTestaments(context.Background(), claims.Action{AgentID: claimsBridgeAgentID, Type: claims.ActionTypeTestament}, []claims.Testament{testament}); err != nil {
			b.debug("presentation_diagnostic_record_failed",
				"source_artifact_id", record.SourceArtifactID,
				"diagnostic_artifact_id", record.DiagnosticArtifactID,
				"reason", record.Reason,
				"error", trimLogValue(err.Error(), 240),
			)
		}
	}
}

func safeClaimPresentationContent(sourceID, content, format string) string {
	content = stripUnsafePresentationControls(content)
	switch strings.ToLower(strings.TrimSpace(format)) {
	case string(claims.PresentationFormatJSON):
		content = safePresentationJSON(content)
	case string(claims.PresentationFormatDiff):
		if looksLikeBinaryPresentation(content) {
			content = "Binary diff omitted for presentation source " + strings.TrimSpace(sourceID) + "."
		} else {
			content = redactPresentationSecretsText(content)
		}
	case string(claims.PresentationFormatMarkdown), string(claims.PresentationFormatTable):
		content = sanitizePresentationMarkdown(redactPresentationSecretsText(content))
	default:
		content = redactPresentationSecretsText(content)
	}
	return boundPresentationContent(content)
}

func safePresentationJSON(content string) string {
	var decoded any
	if err := json.Unmarshal([]byte(content), &decoded); err == nil {
		redacted := redactPresentationJSON(decoded)
		if pretty, err := json.MarshalIndent(redacted, "", "  "); err == nil {
			return string(pretty)
		}
	}
	return redactPresentationSecretsText(content)
}

func redactPresentationJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			if isSecretPresentationKey(k) {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = redactPresentationJSON(v)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = redactPresentationJSON(v)
		}
		return out
	default:
		return value
	}
}

func isSecretPresentationKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	for _, marker := range []string{"token", "api_key", "password", "secret"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

var presentationSecretPattern = regexp.MustCompile(`(?i)\b(token|api[_-]?key|password|secret)(\s*[:=]\s*)(?:"[^"\n]*"|'[^'\n]*'|[^\s,;&]+)`)

func redactPresentationSecretsText(content string) string {
	return presentationSecretPattern.ReplaceAllString(content, `$1$2[REDACTED]`)
}

func sanitizePresentationMarkdown(content string) string {
	content = strings.ReplaceAll(content, "<", "&lt;")
	content = strings.ReplaceAll(content, ">", "&gt;")
	return content
}

func stripUnsafePresentationControls(content string) string {
	var b strings.Builder
	b.Grow(len(content))
	for _, r := range content {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(r)
		case r == 0x1b:
			continue
		case r < 0x20:
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func looksLikeBinaryPresentation(content string) bool {
	if strings.ContainsRune(content, '\x00') {
		return true
	}
	lower := strings.ToLower(content)
	return strings.Contains(lower, "git binary patch") ||
		strings.Contains(lower, "binary files ") ||
		strings.Contains(lower, "cannot display: file marked as a binary type")
}

func boundPresentationContent(content string) string {
	if len(content) <= presentationMaxContent {
		return content
	}
	truncated := content[:presentationMaxContent]
	for !utf8.ValidString(truncated) && len(truncated) > 0 {
		truncated = truncated[:len(truncated)-1]
	}
	return strings.TrimRight(truncated, "\n\r\t ") + "\n\n[Presentation content truncated]"
}

func sanitizePresentationID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "unknown"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

func trimLogValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
