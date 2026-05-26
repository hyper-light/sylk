package chat

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// projectClaimArtifactToHistory renders a claim artifact onto the
// cycle's ChatEntry as a ToolCallRecord. Routing rules
// (UI_DESIGN.md §3.4):
//
//   - tool_started → flat ToolCallRecord on the cycle entry.
//   - consult_started / challenge_started / guardian_check_started →
//     ToolCallRecord with InterAgent populated (the chat tree's
//     "child agent" block).
//   - any artifact carrying ParentRowID → nested under the parent
//     InterAgent's Children entry for the responding agent. A
//     consult dispatching to the librarian followed by the
//     librarian's tool call lands the librarian's tool inside the
//     consult's child block, not flat under the cycle.
//
// Idempotent: a duplicate artifact ID re-finds the existing record
// (anywhere in the tree) instead of creating a sibling.
func (m *Model) projectClaimArtifactToHistory(art *ArtifactRow) {
	if m == nil || art == nil {
		return
	}
	cycleID := strings.TrimSpace(art.CycleID)
	if cycleID == "" {
		return
	}

	idx := m.historyIndexForCorrelation(cycleID)
	if idx < 0 {
		entry := &ChatEntry{
			ID:            uuid.NewString(),
			Timestamp:     art.StartedAt,
			CorrelationID: cycleID,
			Source:        SourceAgent,
			AgentID:       art.AgentID,
			AgentType:     art.AgentType,
			Streaming:     true,
			Height:        -1,
		}
		m.history.Push(entry)
		idx = m.historyIndexForCorrelation(cycleID)
	}
	if idx < 0 {
		return
	}

	m.history.UpdateAt(idx, func(e *ChatEntry) {
		if e.AgentID == "" && art.AgentID != "" {
			e.AgentID = art.AgentID
		}
		if e.AgentType == "" && art.AgentType != "" {
			e.AgentType = art.AgentType
		}
		if entryHasClaimArtifact(e, art.ArtifactID) {
			return
		}
		if attachClaimArtifactNested(e, art) {
			invalidateChatEntryRender(e)
			return
		}
		e.ToolCalls = append(e.ToolCalls, buildClaimToolCallRecord(art))
		invalidateChatEntryRender(e)
	})
	m.syncPendingInterAgentEntry(idx)
	m.viewDirty = true
}

// completeClaimArtifactInHistory finds the existing ToolCallRecord
// matching art.ArtifactID anywhere in the cycle's tree (top-level
// or nested under an InterAgent child) and flips it to its
// terminal state. Recurses through InterAgent.Children so a
// consult-nested librarian tool's completion lands on the actual
// nested record rather than searching only the flat top-level
// list.
func (m *Model) completeClaimArtifactInHistory(art *ArtifactRow) {
	if m == nil || art == nil {
		return
	}
	cycleID := strings.TrimSpace(art.CycleID)
	if cycleID == "" {
		return
	}
	idx := m.historyIndexForCorrelation(cycleID)
	if idx < 0 {
		return
	}
	m.history.UpdateAt(idx, func(e *ChatEntry) {
		if completeClaimArtifactInToolCalls(e.ToolCalls, art) {
			invalidateChatEntryRender(e)
		}
	})
	m.syncPendingInterAgentEntry(idx)
	m.viewDirty = true
}

// applyChildClaimResponseTextToHistory records a child claim's final
// response on the nested inter-agent child activity that represents
// that consultation/challenge branch. It deliberately does not write
// the content onto the cycle's ChatEntry: only the cycle-root claim's
// response_text is the user-visible answer.
func (m *Model) applyChildClaimResponseTextToHistory(cycleID, parentRowID, claimID, agentID, content string) bool {
	if m == nil {
		return false
	}
	cycleID = strings.TrimSpace(cycleID)
	claimID = strings.TrimSpace(claimID)
	content = strings.TrimSpace(content)
	if cycleID == "" || claimID == "" || content == "" {
		return false
	}
	idx := m.historyIndexForCorrelation(cycleID)
	if idx < 0 {
		return false
	}
	changed := false
	m.history.UpdateAt(idx, func(e *ChatEntry) {
		if applyChildClaimResponseTextInToolCalls(e.ToolCalls, parentRowID, claimID, agentID, content) {
			invalidateChatEntryRender(e)
			changed = true
		}
	})
	if changed {
		m.syncPendingInterAgentEntry(idx)
	}
	return changed
}

func (m *Model) applyTestamentContextToHistory(row *TestamentRow) {
	if m == nil || row == nil {
		return
	}
	contextValue := sanitizeThinkingMessage(row.Context)
	if contextValue == "" {
		return
	}
	cycleID := strings.TrimSpace(row.CycleID)
	claimID := strings.TrimSpace(row.ClaimID)
	if cycleID == "" {
		return
	}
	if strings.TrimSpace(row.ParentRowID) != "" {
		if m.applyChildClaimThinkingStatusToHistory(cycleID, row.ParentRowID, claimID, row.AgentID, contextValue, row.Sealed || strings.TrimSpace(row.TestamentID) != "") {
			m.viewDirty = true
			return
		}
	}
	m.applyClaimContextToHistory(cycleID, contextValue)
}

func (m *Model) applyChildClaimThinkingStatusToHistory(cycleID, parentRowID, claimID, agentID, status string, terminal bool) bool {
	cycleID = strings.TrimSpace(cycleID)
	claimID = strings.TrimSpace(claimID)
	status = sanitizeThinkingMessage(status)
	if cycleID == "" || claimID == "" || status == "" {
		return false
	}
	idx := m.historyIndexForCorrelation(cycleID)
	if idx < 0 {
		return false
	}
	changed := false
	now := time.Now()
	m.history.UpdateAt(idx, func(e *ChatEntry) {
		if applyChildClaimThinkingStatusInToolCalls(e.ToolCalls, parentRowID, claimID, agentID, status, terminal, now) {
			invalidateChatEntryRender(e)
			changed = true
		}
	})
	if changed {
		m.syncPendingInterAgentEntry(idx)
	}
	return changed
}

func applyChildClaimResponseTextInToolCalls(calls []ToolCallRecord, parentRowID, claimID, agentID, content string) bool {
	parentRowID = strings.TrimSpace(parentRowID)
	if parentRowID != "" {
		if applyChildClaimResponseTextInParentToolCall(calls, parentRowID, claimID, agentID, content) {
			return true
		}
		if !hasSingleInterAgentTarget(calls, agentID) {
			return false
		}
	}
	for i := range calls {
		if calls[i].InterAgent == nil {
			continue
		}
		if applyChildClaimResponseTextInInterAgent(calls[i].InterAgent, claimID, agentID, content) {
			return true
		}
	}
	return false
}

func applyChildClaimThinkingStatusInToolCalls(calls []ToolCallRecord, parentRowID, claimID, agentID, status string, terminal bool, now time.Time) bool {
	parentRowID = strings.TrimSpace(parentRowID)
	if parentRowID != "" {
		if applyChildClaimThinkingStatusInParentToolCall(calls, parentRowID, claimID, agentID, status, terminal, now) {
			return true
		}
		if !hasSingleInterAgentTarget(calls, agentID) {
			return false
		}
	}
	for i := range calls {
		if calls[i].InterAgent == nil {
			continue
		}
		if applyChildClaimThinkingStatusInInterAgent(calls[i].InterAgent, claimID, agentID, status, terminal, now) {
			return true
		}
	}
	return false
}

func applyChildClaimResponseTextInParentToolCall(calls []ToolCallRecord, parentRowID, claimID, agentID, content string) bool {
	parentRowID = strings.TrimSpace(parentRowID)
	if parentRowID == "" {
		return false
	}
	for i := range calls {
		tc := &calls[i]
		if strings.TrimSpace(tc.ToolCallKey) == parentRowID && tc.InterAgent != nil {
			return applyChildClaimResponseTextInInterAgent(tc.InterAgent, claimID, agentID, content)
		}
		if tc.InterAgent == nil {
			continue
		}
		for c := range tc.InterAgent.Children {
			if applyChildClaimResponseTextInParentToolCall(tc.InterAgent.Children[c].ToolCalls, parentRowID, claimID, agentID, content) {
				return true
			}
		}
	}
	return false
}

func applyChildClaimThinkingStatusInParentToolCall(calls []ToolCallRecord, parentRowID, claimID, agentID, status string, terminal bool, now time.Time) bool {
	parentRowID = strings.TrimSpace(parentRowID)
	if parentRowID == "" {
		return false
	}
	for i := range calls {
		tc := &calls[i]
		if strings.TrimSpace(tc.ToolCallKey) == parentRowID && tc.InterAgent != nil {
			return applyChildClaimThinkingStatusInInterAgent(tc.InterAgent, claimID, agentID, status, terminal, now)
		}
		if tc.InterAgent == nil {
			continue
		}
		for c := range tc.InterAgent.Children {
			if applyChildClaimThinkingStatusInParentToolCall(tc.InterAgent.Children[c].ToolCalls, parentRowID, claimID, agentID, status, terminal, now) {
				return true
			}
		}
	}
	return false
}

func hasSingleInterAgentTarget(calls []ToolCallRecord, agentID string) bool {
	return countInterAgentTargets(calls, strings.TrimSpace(agentID)) == 1
}

func countInterAgentTargets(calls []ToolCallRecord, agentID string) int {
	count := 0
	for i := range calls {
		if calls[i].InterAgent != nil {
			if interAgentRowTargetsAgent(calls[i].InterAgent, agentID) {
				count++
			}
			for c := range calls[i].InterAgent.Children {
				count += countInterAgentTargets(calls[i].InterAgent.Children[c].ToolCalls, agentID)
			}
		}
	}
	return count
}

func applyChildClaimResponseTextInInterAgent(row *InterAgentTool, claimID, agentID, content string) bool {
	if row == nil {
		return false
	}
	doneAt := time.Now()
	for i := range row.Children {
		child := &row.Children[i]
		if childMatchesClaimResponse(child, claimID, agentID) {
			completeInterAgentChild(child, content, doneAt)
			return true
		}
		if applyChildClaimResponseTextInToolCalls(child.ToolCalls, "", claimID, agentID, content) {
			return true
		}
	}
	if idx, ok := ensureInterAgentChildForClaim(row, claimID, agentID, time.Now()); ok {
		child := &row.Children[idx]
		completeInterAgentChild(child, content, doneAt)
		return true
	}
	return false
}

func applyChildClaimThinkingStatusInInterAgent(row *InterAgentTool, claimID, agentID, status string, terminal bool, now time.Time) bool {
	if row == nil {
		return false
	}
	for i := range row.Children {
		child := &row.Children[i]
		if childMatchesClaimResponse(child, claimID, agentID) {
			if terminal {
				completeInterAgentChild(child, status, now)
				return true
			}
			if child.ThinkingStartedAt.IsZero() {
				child.ThinkingStartedAt = now
			}
			child.ThinkingStatus = status
			return true
		}
		if applyChildClaimThinkingStatusInToolCalls(child.ToolCalls, "", claimID, agentID, status, terminal, now) {
			return true
		}
	}
	if idx, ok := ensureInterAgentChildForClaim(row, claimID, agentID, now); ok {
		child := &row.Children[idx]
		if terminal {
			completeInterAgentChild(child, status, now)
			return true
		}
		child.ThinkingStatus = status
		return true
	}
	return false
}

func completeInterAgentChild(child *InterAgentChildActivity, summary string, doneAt time.Time) {
	if child == nil {
		return
	}
	if doneAt.IsZero() {
		doneAt = time.Now()
	}
	child.ResultSummary = normalizeInlineText(summary)
	child.Completed = true
	child.Failed = false
	child.ThinkingText = ""
	child.ThinkingStatus = ""
	finalizeToolCallsSyntheticRecursive(child.ToolCalls, doneAt, true, "")
}

func finalizeToolCallsSyntheticRecursive(calls []ToolCallRecord, doneAt time.Time, success bool, errorMsg string) {
	for i := range calls {
		finalizeToolCallRecordSynthetic(&calls[i], doneAt, success, errorMsg)
		if calls[i].InterAgent == nil {
			continue
		}
		finalizeInterAgentChildrenSynthetic(calls[i].InterAgent, doneAt, success, errorMsg)
	}
}

func finalizeInterAgentChildrenSynthetic(row *InterAgentTool, doneAt time.Time, success bool, errorMsg string) {
	if row == nil {
		return
	}
	for c := range row.Children {
		child := &row.Children[c]
		if success {
			if !child.Completed {
				child.Completed = true
			}
		} else if !child.Completed {
			child.Failed = true
		}
		if summary := strings.TrimSpace(errorMsg); summary != "" && strings.TrimSpace(child.ResultSummary) == "" {
			child.ResultSummary = summary
		}
		child.ThinkingText = ""
		child.ThinkingStatus = ""
		child.ThinkingColor = ""
		finalizeToolCallsSyntheticRecursive(child.ToolCalls, doneAt, success, errorMsg)
	}
}

func ensureInterAgentChildForClaim(row *InterAgentTool, claimID, agentID string, now time.Time) (int, bool) {
	if row == nil {
		return -1, false
	}
	claimID = strings.TrimSpace(claimID)
	agentID = strings.TrimSpace(agentID)
	if claimID == "" && agentID == "" {
		return -1, false
	}
	if agentID != "" && !interAgentRowTargetsAgent(row, agentID) {
		return -1, false
	}
	key := firstNonEmptyString(claimID, agentID)
	for i := range row.Children {
		if strings.TrimSpace(row.Children[i].CorrelationID) == key {
			return i, true
		}
	}
	row.Children = append(row.Children, InterAgentChildActivity{
		CorrelationID:     key,
		AgentID:           agentID,
		AgentType:         agentID,
		ThinkingStartedAt: now,
	})
	return len(row.Children) - 1, true
}

func interAgentRowTargetsAgent(row *InterAgentTool, agentID string) bool {
	if row == nil {
		return false
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return true
	}
	for _, candidate := range row.AgentTypes {
		if strings.TrimSpace(candidate) == agentID {
			return true
		}
	}
	return false
}

func childMatchesClaimResponse(child *InterAgentChildActivity, claimID, agentID string) bool {
	if child == nil {
		return false
	}
	claimID = strings.TrimSpace(claimID)
	agentID = strings.TrimSpace(agentID)
	if claimID != "" && strings.TrimSpace(child.CorrelationID) == claimID {
		return true
	}
	if agentID == "" {
		return false
	}
	return strings.TrimSpace(child.AgentID) == agentID || strings.TrimSpace(child.AgentType) == agentID
}

// applyClaimContextToHistory writes the cycle's narrative status text
// into the projected ChatEntry's ThinkingStatus field so the renderer
// shows it under the agent's row while work is in flight.
func (m *Model) applyClaimContextToHistory(cycleID, contextValue string) {
	if m == nil || strings.TrimSpace(cycleID) == "" {
		return
	}
	idx := m.historyIndexForCorrelation(strings.TrimSpace(cycleID))
	if idx < 0 {
		return
	}
	changed := false
	m.history.UpdateAt(idx, func(e *ChatEntry) {
		if strings.EqualFold(strings.TrimSpace(e.ThinkingStatus), "complete") && !claimContextTerminal("", contextValue) {
			return
		}
		if e.ThinkingStatus == contextValue {
			return
		}
		e.ThinkingStatus = contextValue
		invalidateChatEntryRender(e)
		changed = true
	})
	if changed {
		m.viewDirty = true
	}
}

// finalizeClaimEntry stops the streaming spinner on a cycle's entry
// when the cycle reaches a terminal status. Called from the cycle-
// status path (claim_status_changed → ClaimsAgentStatusMsg{Active=
// false}). Idempotent.
func (m *Model) finalizeClaimEntry(cycleID string, endedAt time.Time) {
	if m == nil || strings.TrimSpace(cycleID) == "" {
		return
	}
	idx := m.historyIndexForCorrelation(strings.TrimSpace(cycleID))
	if idx < 0 {
		return
	}
	m.history.UpdateAt(idx, func(e *ChatEntry) {
		m.finalizeClaimEntryFields(e, endedAt)
		invalidateChatEntryRender(e)
	})
	m.viewDirty = true
}

func (m *Model) finalizeClaimEntryFields(e *ChatEntry, endedAt time.Time) {
	if e == nil {
		return
	}
	if endedAt.IsZero() {
		endedAt = time.Now()
	}
	finalizeToolCallsSynthetic(e.ToolCalls, endedAt, true, "")
	e.Streaming = false
	elapsed := endedAt.Sub(e.Timestamp)
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > 0 {
		e.ThinkingElapsed = elapsed
	}
	e.ThinkingText = fmt.Sprintf("%s  %s", thinkingSummaryGlyph, formatToolDuration(elapsed))
	e.ThinkingStatus = "Complete"
	e.ThinkingColor = ""
}

// claimArtifactDisplayName picks the user-facing label for an
// artifact row. The renderer treats this as the "tool name" in the
// inline tool-call row.
func claimArtifactDisplayName(art *ArtifactRow) string {
	if art == nil {
		return ""
	}
	ref := strings.TrimSpace(art.Reference)
	if ref != "" {
		return ref
	}
	if ref := claimArtifactMetadataText(art.Metadata, "tool_name", "target", "name"); ref != "" {
		return ref
	}
	return claimArtifactDisplayNameFallback(art.Kind)
}

func claimArtifactDisplayNameFallback(kind string) string {
	switch kind {
	case "consult_started":
		return "consult_peer"
	case "challenge_started":
		return "challenge_peer"
	case "guardian_check_started":
		return "guardian_check"
	case "tool_started":
		return "tool"
	}
	return kind
}

// interAgentKindForClaimArtifact maps a claim artifact kind to the
// chat panel's InterAgentToolKind. Empty return means the artifact
// is a plain tool call, not an inter-agent invocation.
func interAgentKindForClaimArtifact(kind string) InterAgentToolKind {
	switch kind {
	case "consult_started":
		return InterAgentToolConsult
	case "challenge_started":
		return InterAgentToolChallenge
	case "guardian_check_started":
		return InterAgentToolApproval
	}
	return ""
}

// buildClaimToolCallRecord constructs a ToolCallRecord from an
// ArtifactRow. Peer-interaction kinds get an InterAgent payload
// pre-populated with the invocation target — that's what the
// renderer keys on to draw the child-agent block.
func buildClaimToolCallRecord(art *ArtifactRow) ToolCallRecord {
	rec := ToolCallRecord{
		ToolCallKey: art.ArtifactID,
		ToolName:    claimArtifactDisplayName(art),
		ArgsSummary: art.ArgsSummary,
		FullArgs:    claimArtifactFullArgs(art.Metadata),
		StartedAt:   art.StartedAt,
	}
	kind := interAgentKindForClaimArtifact(art.Kind)
	if kind == "" {
		return rec
	}
	target := strings.TrimSpace(art.TargetAgent)
	if target == "" {
		target = strings.TrimSpace(art.Reference)
	}
	rec.InterAgent = &InterAgentTool{
		Kind:       kind,
		AgentTypes: normalizeAgentTypes([]string{target}),
		Summary:    art.ArgsSummary,
		Status:     InterAgentToolPending,
	}
	return rec
}

// attachClaimArtifactNested walks the cycle entry's tree looking
// for a ToolCallRecord whose key matches the new artifact's
// ParentRowID. When found, the new artifact lands as a child
// activity tool call under that record's InterAgent. Returns false
// if no parent matches; the caller falls back to flat append so the
// row never disappears.
func attachClaimArtifactNested(e *ChatEntry, art *ArtifactRow) bool {
	pid := strings.TrimSpace(art.ParentRowID)
	if pid == "" {
		return false
	}
	return attachClaimArtifactToToolCalls(e.ToolCalls, art, pid)
}

// attachClaimArtifactToToolCalls iterates a slice of ToolCallRecords
// looking for the parent. Direct match → attach to its InterAgent;
// otherwise recurse through any nested InterAgent children. The
// callers all share the same `[]ToolCallRecord` shape — the one
// helper covers both top-level and nested traversal.
func attachClaimArtifactToToolCalls(calls []ToolCallRecord, art *ArtifactRow, parentArtifactID string) bool {
	for i := range calls {
		tc := &calls[i]
		if tc.ToolCallKey == parentArtifactID && tc.InterAgent != nil {
			attachClaimArtifactToInterAgent(tc.InterAgent, art)
			return true
		}
		if tc.InterAgent != nil && attachClaimArtifactWithinInterAgent(tc.InterAgent, art, parentArtifactID) {
			return true
		}
	}
	return false
}

// attachClaimArtifactWithinInterAgent walks the children of an
// inter-agent invocation looking for a deeper parent (a consult
// nested inside another consult, etc.).
func attachClaimArtifactWithinInterAgent(row *InterAgentTool, art *ArtifactRow, parentArtifactID string) bool {
	for c := range row.Children {
		if attachClaimArtifactToToolCalls(row.Children[c].ToolCalls, art, parentArtifactID) {
			return true
		}
	}
	return false
}

// attachClaimArtifactToInterAgent appends the artifact under the
// inter-agent's child activity block. A new child entry is created
// the first time a given responding agent emits an artifact under
// this invocation; subsequent artifacts append to that same child's
// ToolCalls so the librarian's three tools render as three rows
// inside one librarian block, not three separate librarian blocks.
func attachClaimArtifactToInterAgent(row *InterAgentTool, art *ArtifactRow) {
	idx := findOrCreateInterAgentChild(row, art)
	row.Children[idx].ToolCalls = append(row.Children[idx].ToolCalls, buildClaimToolCallRecord(art))
}

func findOrCreateInterAgentChild(row *InterAgentTool, art *ArtifactRow) int {
	key := childActivityKey(art)
	for i := range row.Children {
		if row.Children[i].CorrelationID == key {
			return i
		}
	}
	row.Children = append(row.Children, InterAgentChildActivity{
		CorrelationID:     key,
		AgentID:           art.AgentID,
		AgentType:         art.AgentType,
		ThinkingStartedAt: art.StartedAt,
	})
	return len(row.Children) - 1
}

// childActivityKey identifies a single responding-agent block
// within an inter-agent invocation. Each child claim is a distinct
// agent excursion, so the claim ID is the natural separator;
// agent ID is a fallback for the rare case where the artifact
// arrives without a claim ID. The key is opaque to the renderer —
// it only matters for find-or-create idempotency.
func childActivityKey(art *ArtifactRow) string {
	if id := strings.TrimSpace(art.ClaimID); id != "" {
		return id
	}
	return strings.TrimSpace(art.AgentID)
}

// entryHasClaimArtifact reports whether artID is already attached
// anywhere in the entry's tree (top-level or nested under any
// InterAgent.Children). Idempotency guard against redelivery.
func entryHasClaimArtifact(e *ChatEntry, artID string) bool {
	artID = strings.TrimSpace(artID)
	if artID == "" {
		return false
	}
	return toolCallsContainArtifact(e.ToolCalls, artID)
}

func toolCallsContainArtifact(calls []ToolCallRecord, artID string) bool {
	for i := range calls {
		if strings.TrimSpace(calls[i].ToolCallKey) == artID {
			return true
		}
		if calls[i].InterAgent != nil && interAgentContainsArtifact(calls[i].InterAgent, artID) {
			return true
		}
	}
	return false
}

func interAgentContainsArtifact(row *InterAgentTool, artID string) bool {
	for c := range row.Children {
		if toolCallsContainArtifact(row.Children[c].ToolCalls, artID) {
			return true
		}
	}
	return false
}

// completeClaimArtifactInToolCalls finds the artifact anywhere in
// the slice (top-level or nested via InterAgent.Children) and flips
// it to its terminal state. Returns true if the artifact was
// located and updated. Recursion is bounded by the depth of the
// inter-agent tree (single-digit in practice).
func completeClaimArtifactInToolCalls(calls []ToolCallRecord, art *ArtifactRow) bool {
	for i := range calls {
		tc := &calls[i]
		if strings.TrimSpace(tc.ToolCallKey) == art.ArtifactID {
			applyClaimArtifactCompletion(tc, art)
			return true
		}
		if tc.InterAgent != nil && completeWithinInterAgent(tc.InterAgent, art) {
			return true
		}
	}
	return false
}

func completeWithinInterAgent(row *InterAgentTool, art *ArtifactRow) bool {
	for c := range row.Children {
		if completeClaimArtifactInToolCalls(row.Children[c].ToolCalls, art) {
			return true
		}
	}
	return false
}

// applyClaimArtifactCompletion writes terminal state onto a
// matched ToolCallRecord. For inter-agent invocations the InterAgent
// Status flips alongside Completed so the renderer drops the
// pending-spinner visual.
func applyClaimArtifactCompletion(tc *ToolCallRecord, art *ArtifactRow) {
	tc.Duration = art.Duration
	tc.Success = art.Status == ArtifactRowStatusSuccess
	tc.Completed = true
	tc.SyntheticCompletion = false
	if art.Output != "" {
		tc.Output = art.Output
	}
	if art.ErrorMsg != "" {
		tc.ErrorMsg = art.ErrorMsg
		tc.Expanded = true
	}
	if tc.InterAgent != nil {
		tc.InterAgent.Status = interAgentStatusFromArtifact(art.Status)
	}
}

func interAgentStatusFromArtifact(status ArtifactRowStatus) InterAgentToolStatus {
	switch status {
	case ArtifactRowStatusSuccess:
		return InterAgentToolDone
	case ArtifactRowStatusFailure, ArtifactRowStatusTimeout, ArtifactRowStatusCancelled:
		return InterAgentToolFailed
	}
	return InterAgentToolPending
}
