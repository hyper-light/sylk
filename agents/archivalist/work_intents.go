package archivalist

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	WorkIntentStatusActive    = "active"
	WorkIntentStatusCompleted = "completed"
	WorkIntentStatusFailed    = "failed"
)

// WorkIntent tracks cross-cutting work announcements that should surface in
// handoff briefings and conflict checks.
type WorkIntent struct {
	ID            string     `json:"id"`
	Type          string     `json:"type"`
	Description   string     `json:"description"`
	AffectedPaths []string   `json:"affected_paths,omitempty"`
	AffectedAPIs  []string   `json:"affected_apis,omitempty"`
	Priority      string     `json:"priority,omitempty"`
	Status        string     `json:"status"`
	SourceAgentID string     `json:"source_agent_id,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	Success       *bool      `json:"success,omitempty"`
	FilesChanged  []string   `json:"files_changed,omitempty"`
}

func cloneWorkIntent(intent *WorkIntent) *WorkIntent {
	if intent == nil {
		return nil
	}
	cloned := *intent
	if len(intent.AffectedPaths) > 0 {
		cloned.AffectedPaths = append([]string(nil), intent.AffectedPaths...)
	}
	if len(intent.AffectedAPIs) > 0 {
		cloned.AffectedAPIs = append([]string(nil), intent.AffectedAPIs...)
	}
	if len(intent.FilesChanged) > 0 {
		cloned.FilesChanged = append([]string(nil), intent.FilesChanged...)
	}
	if intent.CompletedAt != nil {
		completedAt := *intent.CompletedAt
		cloned.CompletedAt = &completedAt
	}
	if intent.Success != nil {
		success := *intent.Success
		cloned.Success = &success
	}
	return &cloned
}

func normalizeIntentPriority(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "low", "medium", "high", "critical":
		return strings.ToLower(strings.TrimSpace(priority))
	default:
		return "medium"
	}
}

func normalizeWorkIntentType(intentType string) string {
	trimmed := strings.ToLower(strings.TrimSpace(intentType))
	if trimmed == "" {
		return "general"
	}
	return trimmed
}

func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func (a *Archivalist) ActiveWorkIntents() []*WorkIntent {
	a.workIntentsMu.RLock()
	defer a.workIntentsMu.RUnlock()

	active := make([]*WorkIntent, 0, len(a.workIntents))
	for _, intent := range a.workIntents {
		if intent == nil || intent.Status != WorkIntentStatusActive {
			continue
		}
		active = append(active, cloneWorkIntent(intent))
	}
	return active
}

func (a *Archivalist) declareWorkIntent(params DeclareIntentInput, sourceAgentID string) map[string]any {
	intent := &WorkIntent{
		ID:            "intent_" + uuid.NewString()[:8],
		Type:          normalizeWorkIntentType(params.Type),
		Description:   strings.TrimSpace(params.Description),
		AffectedPaths: normalizeStringSlice(params.AffectedPaths),
		AffectedAPIs:  normalizeStringSlice(params.AffectedAPIs),
		Priority:      normalizeIntentPriority(params.Priority),
		Status:        WorkIntentStatusActive,
		SourceAgentID: strings.TrimSpace(sourceAgentID),
		CreatedAt:     time.Now(),
	}

	existing := a.findEquivalentWorkIntent(intent)
	if existing != nil {
		return map[string]any{
			"status": "already_declared",
			"intent": cloneWorkIntent(existing),
		}
	}

	conflicts := a.findConflictingWorkIntents(intent, "")
	if len(conflicts) > 0 {
		conflicting := conflicts[0]
		a.recordWorkIntentConflict(intent, conflicting)
		return map[string]any{
			"status":             "intent_conflict",
			"message":            "Overlapping active work intent detected.",
			"conflicting_intent": cloneWorkIntent(conflicting),
			"intent":             cloneWorkIntent(intent),
		}
	}

	a.workIntentsMu.Lock()
	a.workIntents = append(a.workIntents, intent)
	a.workIntentsMu.Unlock()

	return map[string]any{
		"status": "declared",
		"intent": cloneWorkIntent(intent),
	}
}

func (a *Archivalist) completeWorkIntent(params CompleteIntentInput) map[string]any {
	intentID := strings.TrimSpace(params.IntentID)
	if intentID == "" {
		return map[string]any{
			"status":  "error",
			"message": "intent_id is required",
		}
	}

	a.workIntentsMu.Lock()
	defer a.workIntentsMu.Unlock()

	for _, intent := range a.workIntents {
		if intent == nil || intent.ID != intentID {
			continue
		}
		now := time.Now()
		intent.CompletedAt = &now
		intent.Success = &params.Success
		intent.FilesChanged = normalizeStringSlice(params.FilesChanged)
		if params.Success {
			intent.Status = WorkIntentStatusCompleted
		} else {
			intent.Status = WorkIntentStatusFailed
		}

		return map[string]any{
			"status":    "completed",
			"intent_id": intent.ID,
			"intent":    cloneWorkIntent(intent),
			"success":   params.Success,
		}
	}

	return map[string]any{
		"status":    "not_found",
		"intent_id": intentID,
	}
}

func (a *Archivalist) findEquivalentWorkIntent(candidate *WorkIntent) *WorkIntent {
	if candidate == nil {
		return nil
	}

	a.workIntentsMu.RLock()
	defer a.workIntentsMu.RUnlock()

	for _, existing := range a.workIntents {
		if existing == nil || existing.Status != WorkIntentStatusActive {
			continue
		}
		if existing.Type != candidate.Type || existing.Description != candidate.Description {
			continue
		}
		if !stringSetsEquivalent(existing.AffectedPaths, candidate.AffectedPaths) {
			continue
		}
		if !stringSetsEquivalent(existing.AffectedAPIs, candidate.AffectedAPIs) {
			continue
		}
		return existing
	}

	return nil
}

func (a *Archivalist) findConflictingWorkIntents(candidate *WorkIntent, excludeID string) []*WorkIntent {
	if candidate == nil {
		return nil
	}

	a.workIntentsMu.RLock()
	defer a.workIntentsMu.RUnlock()

	conflicts := make([]*WorkIntent, 0)
	for _, existing := range a.workIntents {
		if existing == nil || existing.Status != WorkIntentStatusActive || existing.ID == excludeID {
			continue
		}
		if !workIntentTargetsOverlap(existing, candidate) {
			continue
		}
		if existing.Description == candidate.Description && existing.Type == candidate.Type {
			continue
		}
		conflicts = append(conflicts, existing)
	}

	return conflicts
}

func (a *Archivalist) conflictingWorkIntentsForPaths(paths []string) []*WorkIntent {
	candidate := &WorkIntent{AffectedPaths: normalizeStringSlice(paths)}
	if len(candidate.AffectedPaths) == 0 {
		return nil
	}
	return a.findConflictingWorkIntents(candidate, "")
}

func (a *Archivalist) recordWorkIntentConflict(incoming, existing *WorkIntent) {
	if a.conflictHistory == nil || incoming == nil || existing == nil {
		return
	}
	a.conflictHistory.Record(&ConflictRecord{
		ID:           uuid.NewString(),
		DetectedAt:   time.Now(),
		Type:         ConflictTypeIntent,
		Scope:        ScopeIntents,
		Key:          strings.Join(incoming.AffectedPaths, ","),
		AgentID:      incoming.SourceAgentID,
		BaseVersion:  a.registry.GetVersion(),
		AutoResolved: false,
		Result: &ConflictResult{
			Type:          ConflictTypeIntent,
			Strategy:      ResolutionHuman,
			Resolved:      false,
			Message:       "overlapping active work intent requires human coordination",
			ExistingValue: cloneWorkIntent(existing),
			IncomingValue: cloneWorkIntent(incoming),
		},
	})
}

func workIntentTargetsOverlap(a, b *WorkIntent) bool {
	if a == nil || b == nil {
		return false
	}
	if pathsOverlap(a.AffectedPaths, b.AffectedPaths) {
		return true
	}
	return stringSetsOverlap(a.AffectedAPIs, b.AffectedAPIs)
}

func pathsOverlap(left, right []string) bool {
	for _, l := range left {
		for _, r := range right {
			if pathPatternsOverlap(l, r) {
				return true
			}
		}
	}
	return false
}

func pathPatternsOverlap(left, right string) bool {
	l := strings.TrimSpace(left)
	r := strings.TrimSpace(right)
	if l == "" || r == "" {
		return false
	}
	if l == r {
		return true
	}
	if strings.HasPrefix(l, r) || strings.HasPrefix(r, l) {
		return true
	}
	if matched, err := filepath.Match(l, r); err == nil && matched {
		return true
	}
	if matched, err := filepath.Match(r, l); err == nil && matched {
		return true
	}

	lTrimmed := strings.Trim(l, "*")
	rTrimmed := strings.Trim(r, "*")
	return (lTrimmed != "" && strings.Contains(r, lTrimmed)) || (rTrimmed != "" && strings.Contains(l, rTrimmed))
}

func stringSetsOverlap(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(left))
	for _, item := range left {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized == "" {
			continue
		}
		seen[normalized] = struct{}{}
	}
	for _, item := range right {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			return true
		}
	}
	return false
}

func stringSetsEquivalent(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	if len(left) == 0 {
		return true
	}
	seen := make(map[string]int, len(left))
	for _, item := range left {
		seen[strings.ToLower(strings.TrimSpace(item))]++
	}
	for _, item := range right {
		key := strings.ToLower(strings.TrimSpace(item))
		count, ok := seen[key]
		if !ok || count == 0 {
			return false
		}
		seen[key]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}
