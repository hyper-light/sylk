package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	contextskills "github.com/adalundhe/sylk/core/context/skills"
	"github.com/adalundhe/sylk/core/forest"
	coreskills "github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
)

// MemoryForestService captures the forest methods agents use directly.
//
// Phase 9: agent-facing memory is ForestPacket and ForestCursor native.
// Branch projections are not part of the agent contract.
type MemoryForestService interface {
	ResolveIntent(ctx context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error)
	RetrieveForest(ctx context.Context, query forest.Query) ([]*forest.ForestPacket, error)
	CreateForestCursor(ctx context.Context, input forest.ForestCursorInput) (*forest.ForestCursor, error)
	ProposeForestClaim(ctx context.Context, proposal forest.ForestClaimProposal) error
	RecordOutcome(ctx context.Context, record forest.OutcomeRecord) error
}

type ForestOutcomeClassification struct {
	Status  forest.OutcomeStatus
	Summary string
	Enabled bool
}

type ForestOutcomeClassifier func(context.Context, any, error) ForestOutcomeClassification

const (
	forestTrackerMaxEntries = 256
	forestTrackerMaxAge     = 15 * time.Minute
)

// MemoryForestTracker keeps the most recent node IDs surfaced through forest
// skills so later terminal steps can feed outcomes back into the forest.
type MemoryForestTracker struct {
	mu      sync.RWMutex
	entries map[string]trackedForestNodes
}

type trackedForestNodes struct {
	NodeIDs   []string
	CursorID  string
	UpdatedAt time.Time
}

func NewMemoryForestTracker() *MemoryForestTracker {
	return &MemoryForestTracker{
		entries: make(map[string]trackedForestNodes),
	}
}

// RegisterMemoryForestSkills installs forest-backed skills for the given role
// and wraps them so surfaced node IDs are tracked for later outcome writes.
func RegisterMemoryForestSkills(
	registry *coreskills.Registry,
	agentType string,
	forestSvc MemoryForestService,
	tracker *MemoryForestTracker,
) error {
	if registry == nil || forestSvc == nil {
		return nil
	}
	if err := contextskills.RegisterForestSkillsForAgent(registry, agentType, &contextskills.RetrievalDependencies{
		Forest: forestSvc,
	}); err != nil {
		return err
	}
	if tracker == nil {
		return nil
	}
	for _, name := range append(contextskills.ForestSkillNamesForAgent(agentType), contextskills.ForestMutatingSkillNames()...) {
		skill := registry.Get(name)
		if skill == nil || skill.Handler == nil {
			continue
		}
		original := skill.Handler
		skill.Handler = func(ctx context.Context, input json.RawMessage) (any, error) {
			result, err := original(ctx, input)
			if err == nil {
				tracker.ObserveResult(ctx, result, "")
			}
			return result, err
		}
	}
	return nil
}

func AppendMemoryForestVisibleSkillNames(base []string, agentType string) []string {
	return appendUnique(base, contextskills.ForestSkillNamesForAgent(agentType)...)
}

func AppendMemoryForestMutatingSkillNames(base []string) []string {
	return appendUnique(base, contextskills.ForestMutatingSkillNames()...)
}

func FilterRegisteredSkillNames(registry *coreskills.Registry, names []string) []string {
	if registry == nil || len(names) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || registry.Get(trimmed) == nil {
			continue
		}
		filtered = append(filtered, trimmed)
	}
	return filtered
}

func AppendMemoryForestToolPolicies(
	base []toolruntime.ToolPolicy,
	registry *coreskills.Registry,
	agentType string,
) []toolruntime.ToolPolicy {
	policies := append([]toolruntime.ToolPolicy(nil), base...)
	seen := make(map[string]struct{}, len(base))
	for _, policy := range base {
		name := strings.TrimSpace(policy.Name)
		if name == "" {
			continue
		}
		seen[name] = struct{}{}
	}

	for _, name := range contextskills.ForestSkillNamesForAgent(agentType) {
		if registry != nil && registry.Get(name) == nil {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		policies = append(policies, toolruntime.NewToolPolicy(
			name,
			toolruntime.EffectReadOnly,
			toolruntime.DomainMemory,
			toolruntime.ExecutionModeLocal,
			toolruntime.WithVisibleByDefault(),
		))
	}

	for _, name := range contextskills.ForestMutatingSkillNames() {
		if registry != nil && registry.Get(name) == nil {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		policies = append(policies, toolruntime.NewToolPolicy(
			name,
			toolruntime.EffectMutating,
			toolruntime.DomainMemory,
			toolruntime.ExecutionModeLocalWorker,
		))
	}

	return policies
}

func AttachForestOutcomeRecorder(
	registry *coreskills.Registry,
	skillName string,
	tracker *MemoryForestTracker,
	forestSvc MemoryForestService,
	agentID func() string,
	agentType string,
	sessionID func() string,
	classify ForestOutcomeClassifier,
) error {
	if registry == nil || tracker == nil || forestSvc == nil || classify == nil {
		return nil
	}
	skill := registry.Get(strings.TrimSpace(skillName))
	if skill == nil || skill.Handler == nil {
		return nil
	}
	original := skill.Handler
	skill.Handler = func(ctx context.Context, input json.RawMessage) (any, error) {
		result, err := original(ctx, input)
		decision := classify(ctx, result, err)
		if decision.Enabled {
			var agent string
			if agentID != nil {
				agent = strings.TrimSpace(agentID())
			}
			var session string
			if sessionID != nil {
				session = strings.TrimSpace(sessionID())
			}
			_ = tracker.RecordOutcome(
				ctx,
				forestSvc,
				agent,
				strings.TrimSpace(agentType),
				session,
				decision.Status,
				strings.TrimSpace(decision.Summary),
			)
		}
		return result, err
	}
	return nil
}

func OutcomeOnSuccess(summary string) ForestOutcomeClassifier {
	summary = strings.TrimSpace(summary)
	return func(_ context.Context, _ any, err error) ForestOutcomeClassification {
		if err != nil {
			return ForestOutcomeClassification{}
		}
		return ForestOutcomeClassification{
			Status:  forest.OutcomeStatusSucceeded,
			Summary: summary,
			Enabled: true,
		}
	}
}

func OutcomeAlways(status forest.OutcomeStatus, summary string) ForestOutcomeClassifier {
	summary = strings.TrimSpace(summary)
	return func(_ context.Context, _ any, err error) ForestOutcomeClassification {
		if err != nil {
			return ForestOutcomeClassification{}
		}
		return ForestOutcomeClassification{
			Status:  status,
			Summary: summary,
			Enabled: true,
		}
	}
}

func OutcomeFromGlobalReviewValidation(
	successSummary string,
	failureSummary string,
	mixedSummary string,
) ForestOutcomeClassifier {
	successSummary = strings.TrimSpace(successSummary)
	failureSummary = strings.TrimSpace(failureSummary)
	mixedSummary = strings.TrimSpace(mixedSummary)
	return func(_ context.Context, result any, err error) ForestOutcomeClassification {
		if err != nil {
			return ForestOutcomeClassification{}
		}
		payload, ok := result.(map[string]any)
		if !ok {
			return ForestOutcomeClassification{}
		}
		status := strings.TrimSpace(forestStringFromAny(payload["status"]))
		switch status {
		case "passed":
			return ForestOutcomeClassification{
				Status:  forest.OutcomeStatusSucceeded,
				Summary: successSummary,
				Enabled: true,
			}
		case "failed":
			return ForestOutcomeClassification{
				Status:  forest.OutcomeStatusFailed,
				Summary: failureSummary,
				Enabled: true,
			}
		case "blocked", "unclear", "partial":
			return ForestOutcomeClassification{
				Status:  forest.OutcomeStatusMixed,
				Summary: mixedSummary,
				Enabled: true,
			}
		default:
			return ForestOutcomeClassification{}
		}
	}
}

func OutcomeFromSuiteCounts(successSummary string, failureSummary string) ForestOutcomeClassifier {
	successSummary = strings.TrimSpace(successSummary)
	failureSummary = strings.TrimSpace(failureSummary)
	return func(_ context.Context, result any, err error) ForestOutcomeClassification {
		if err != nil {
			return ForestOutcomeClassification{}
		}
		payload, ok := result.(map[string]any)
		if !ok {
			return ForestOutcomeClassification{}
		}
		failed := forestIntFromAny(payload["failed"])
		passed := forestIntFromAny(payload["passed"])
		if failed > 0 {
			return ForestOutcomeClassification{
				Status:  forest.OutcomeStatusFailed,
				Summary: failureSummary,
				Enabled: true,
			}
		}
		if passed > 0 {
			return ForestOutcomeClassification{
				Status:  forest.OutcomeStatusSucceeded,
				Summary: successSummary,
				Enabled: true,
			}
		}
		return ForestOutcomeClassification{
			Status:  forest.OutcomeStatusMixed,
			Summary: firstNonEmptyForestString(successSummary, failureSummary),
			Enabled: true,
		}
	}
}

func appendUnique(base []string, names ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(names))
	result := make([]string, 0, len(base)+len(names))
	for _, name := range base {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func forestStringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

func forestIntFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint:
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func firstNonEmptyForestString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (t *MemoryForestTracker) ObserveResult(ctx context.Context, result any, sessionFallback string) {
	if t == nil {
		return
	}
	nodeIDs, cursorID := extractTrackedForestNodeIDs(result)
	if len(nodeIDs) == 0 {
		return
	}
	key := forestTrackerKey(ctx, sessionFallback)
	if key == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries[key] = trackedForestNodes{
		NodeIDs:   nodeIDs,
		CursorID:  cursorID,
		UpdatedAt: time.Now(),
	}
	t.pruneLocked(time.Now())
}

func (t *MemoryForestTracker) Snapshot(ctx context.Context, sessionFallback string) ([]string, string) {
	if t == nil {
		return nil, ""
	}
	key := forestTrackerKey(ctx, sessionFallback)
	t.mu.RLock()
	defer t.mu.RUnlock()
	if entry, ok := t.entries[key]; ok {
		if forestTrackerEntryExpired(entry, time.Now()) {
			return nil, ""
		}
		return append([]string(nil), entry.NodeIDs...), entry.CursorID
	}
	var (
		bestTime time.Time
		bestIDs  []string
		cursorID string
	)
	sessionID := forestTrackerSessionID(ctx, sessionFallback)
	for entryKey, entry := range t.entries {
		if forestTrackerEntryExpired(entry, time.Now()) {
			continue
		}
		if sessionID != "" && !strings.Contains(entryKey, "|"+sessionID+"|") {
			continue
		}
		if entry.UpdatedAt.After(bestTime) {
			bestTime = entry.UpdatedAt
			bestIDs = entry.NodeIDs
			cursorID = entry.CursorID
		}
	}
	return append([]string(nil), bestIDs...), cursorID
}

func (t *MemoryForestTracker) RecordOutcome(
	ctx context.Context,
	forestSvc MemoryForestService,
	agentID string,
	agentType string,
	sessionFallback string,
	status forest.OutcomeStatus,
	summary string,
) error {
	if t == nil || forestSvc == nil {
		return nil
	}
	nodeIDs, cursorID := t.Snapshot(ctx, sessionFallback)
	if len(nodeIDs) == 0 {
		return nil
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = fmt.Sprintf("%s outcome recorded by %s", status, strings.TrimSpace(agentType))
	}
	sessionID := forestTrackerSessionID(ctx, sessionFallback)
	for _, nodeID := range nodeIDs {
		if err := forestSvc.RecordOutcome(ctx, forest.OutcomeRecord{
			NodeID:     nodeID,
			CursorID:   cursorID,
			SessionID:  sessionID,
			AgentID:    strings.TrimSpace(agentID),
			AgentType:  strings.TrimSpace(agentType),
			Status:     status,
			Summary:    summary,
			Confidence: 0.8,
			Salience:   0.7,
		}); err != nil {
			return err
		}
	}
	t.forget(ctx, sessionFallback)
	return nil
}

func (t *MemoryForestTracker) forget(ctx context.Context, sessionFallback string) {
	if t == nil {
		return
	}
	key := forestTrackerKey(ctx, sessionFallback)
	if key == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}

func (t *MemoryForestTracker) pruneLocked(now time.Time) {
	if t == nil || len(t.entries) == 0 {
		return
	}
	for key, entry := range t.entries {
		if forestTrackerEntryExpired(entry, now) {
			delete(t.entries, key)
		}
	}
	if len(t.entries) <= forestTrackerMaxEntries {
		return
	}
	type agedKey struct {
		key       string
		updatedAt time.Time
	}
	oldest := make([]agedKey, 0, len(t.entries))
	for key, entry := range t.entries {
		oldest = append(oldest, agedKey{key: key, updatedAt: entry.UpdatedAt})
	}
	sort.Slice(oldest, func(i, j int) bool {
		return oldest[i].updatedAt.Before(oldest[j].updatedAt)
	})
	for _, item := range oldest[:len(oldest)-forestTrackerMaxEntries] {
		delete(t.entries, item.key)
	}
}

func forestTrackerEntryExpired(entry trackedForestNodes, now time.Time) bool {
	if entry.UpdatedAt.IsZero() {
		return false
	}
	return now.Sub(entry.UpdatedAt) > forestTrackerMaxAge
}

func forestTrackerKey(ctx context.Context, sessionFallback string) string {
	sessionID := forestTrackerSessionID(ctx, sessionFallback)
	logMeta := LogMetaFromContext(ctx)
	correlationID := strings.TrimSpace(logMeta.CorrID)
	agentID := strings.TrimSpace(logMeta.AgentID)
	switch {
	case correlationID != "":
		return "corr|" + sessionID + "|" + correlationID
	case agentID != "":
		return "agent|" + sessionID + "|" + agentID
	case sessionID != "":
		return "session|" + sessionID
	default:
		return ""
	}
}

func forestTrackerSessionID(ctx context.Context, sessionFallback string) string {
	if sessionID := strings.TrimSpace(versioning.SessionIDFromContext(ctx)); sessionID != "" {
		return sessionID
	}
	if sessionID := strings.TrimSpace(LogMetaFromContext(ctx).SessionID); sessionID != "" {
		return sessionID
	}
	return strings.TrimSpace(sessionFallback)
}

func extractTrackedForestNodeIDs(result any) ([]string, string) {
	switch value := result.(type) {
	case *contextskills.ForestRoleOutput:
		return nodeIDsFromForestRoleOutput(value)
	case contextskills.ForestRoleOutput:
		return nodeIDsFromForestRoleOutput(&value)
	case *contextskills.ForestRecallOutput:
		return dedupeNodeIDs(nodeIDsFromForestPackets(value.Packets)), cursorIDFromForestCursor(value.Cursor)
	case contextskills.ForestRecallOutput:
		return dedupeNodeIDs(nodeIDsFromForestPackets(value.Packets)), cursorIDFromForestCursor(value.Cursor)
	case *forest.IntentResolution:
		return dedupeNodeIDs(nodeIDsFromIntentResolution(value)), ""
	case forest.IntentResolution:
		return dedupeNodeIDs(nodeIDsFromIntentResolution(&value)), ""
	default:
		return nil, ""
	}
}

func nodeIDsFromForestRoleOutput(output *contextskills.ForestRoleOutput) ([]string, string) {
	if output == nil {
		return nil, ""
	}
	ids := nodeIDsFromForestPackets(output.Packets)
	ids = append(ids, nodeIDsFromIntentResolution(output.Intent)...)
	return dedupeNodeIDs(ids), cursorIDFromForestCursor(output.Cursor)
}

func nodeIDsFromIntentResolution(resolution *forest.IntentResolution) []string {
	if resolution == nil {
		return nil
	}
	var ids []string
	ids = append(ids, nodeIDsFromForestPackets(resolution.Constraints)...)
	ids = append(ids, nodeIDsFromForestPackets(resolution.Preferences)...)
	ids = append(ids, nodeIDsFromForestPackets(resolution.IntentNodes)...)
	ids = append(ids, nodeIDsFromForestPackets(resolution.OutcomeHints)...)
	return dedupeNodeIDs(ids)
}

func nodeIDsFromForestPackets(packets []*forest.ForestPacket) []string {
	ids := make([]string, 0, len(packets))
	for _, packet := range packets {
		if packet == nil {
			continue
		}
		ids = append(ids, packet.Node.ID)
	}
	return dedupeNodeIDs(ids)
}

func cursorIDFromForestCursor(cursor *forest.ForestCursor) string {
	if cursor == nil {
		return ""
	}
	return strings.TrimSpace(cursor.ID)
}

func dedupeNodeIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}
