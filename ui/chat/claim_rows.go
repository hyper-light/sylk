package chat

import (
	"strings"
	"sync"
	"time"
)

// ClaimRowStatus is the cycle-row status the chat panel renders. It
// mirrors core/claims.ClaimStatus but lives in the chat package so the
// renderer doesn't take a transitive dependency on the claim board.
type ClaimRowStatus string

const (
	ClaimRowStatusPending    ClaimRowStatus = "pending"
	ClaimRowStatusInProgress ClaimRowStatus = "in_progress"
	ClaimRowStatusTestified  ClaimRowStatus = "testified"
	ClaimRowStatusAccepted   ClaimRowStatus = "accepted"
	ClaimRowStatusRejected   ClaimRowStatus = "rejected"
)

// ArtifactRowStatus is the per-artifact (tool/consult/challenge/
// guardian-check) row state. "in_flight" until a completion arrives;
// then one of success/failure/timeout/cancelled.
type ArtifactRowStatus string

const (
	ArtifactRowStatusInFlight  ArtifactRowStatus = "in_flight"
	ArtifactRowStatusSuccess   ArtifactRowStatus = "success"
	ArtifactRowStatusFailure   ArtifactRowStatus = "failure"
	ArtifactRowStatusTimeout   ArtifactRowStatus = "timeout"
	ArtifactRowStatusCancelled ArtifactRowStatus = "cancelled"
)

// ClaimRow models a top-level cycle row in the chat tree. The chat
// keeps one ClaimRow per CycleID. ClaimContextMsg updates Context in
// place; the row's Status flips on terminal claim transitions.
type ClaimRow struct {
	CycleID         string
	ClaimID         string // root claim of the cycle
	OwnerAgentID    string
	OwnerAgentType  string
	TargetAgentID   string
	SessionID       string
	Title           string
	Status          ClaimRowStatus
	Context         string // narrative status text from ClaimContextMsg
	ContextSequence int64
	StartedAt       time.Time
	EndedAt         time.Time
	// Artifacts holds the ArtifactIDs in the order they were started
	// directly under the cycle row (i.e. ParentRowID == "" or ==
	// CycleID). Nested artifacts attach to their parent ArtifactRow.
	Artifacts []string
}

// ArtifactRow models a single artifact-backed tool row. Peer exchanges
// are modeled by their claim rows; ParentRowID points at those
// claim-backed rows when an artifact belongs under a peer exchange.
type ArtifactRow struct {
	ArtifactID  string
	ParentRowID string
	CycleID     string
	ClaimID     string
	Kind        string // "tool_started" | "llm_started" | diagnostic artifact kind
	Reference   string // tool name / artifact reference
	ArgsSummary string
	AgentID     string
	AgentType   string
	TargetAgent string
	Status      ArtifactRowStatus
	StartedAt   time.Time
	EndedAt     time.Time
	Duration    time.Duration
	Output      string
	ErrorMsg    string
	Metadata    map[string]any
	// Children is the ordered list of child ArtifactIDs that nested
	// under this artifact. Claim-backed peer rows own inter-agent
	// nesting; artifact children are only for artifact-to-artifact
	// relationships.
	Children []string
}

// TestamentRow models an in-flight or sealed testament row. Keyed by
// AccumulatorID before flush, then rebinds onto TestamentID after the
// testament is submitted. Both IDs map to the same row via the index.
type TestamentRow struct {
	AccumulatorID   string
	TestamentID     string
	ClaimID         string
	CycleID         string
	ParentRowID     string
	AgentID         string
	Context         string // developing-conclusion narrative (acc.SetContext)
	ContextSequence int64
	Summary         string // sealed summary set on flush
	Sealed          bool
	StartedAt       time.Time
	SealedAt        time.Time
}

// claimRowIndex is the chat panel's claims-native row store. Per
// docs/CLAIMS_UI.md §3.3-3.4 the indices are explicitly:
//
//   - claimRows       CycleID/ClaimID → ClaimRow      (top-level cycle row)
//   - artifactRows    ArtifactID      → ArtifactRow   (tool/consult/challenge/guardian-check)
//   - accumulatorRows AccumulatorID   → TestamentRow  (in-flight testament anchor)
//   - testamentRows   TestamentID     → TestamentRow  (sealed testament anchor; rebinds in-flight row)
//
// Splitting accumulatorRows from testamentRows mirrors the rebind
// semantics from the spec: a TestamentContextDelta with only
// AccumulatorID populated creates the row in accumulatorRows; a later
// delta with TestamentID set rebinds that same row into testamentRows
// without creating a duplicate.
type claimRowIndex struct {
	mu sync.RWMutex

	claimRows       map[string]*ClaimRow
	artifactRows    map[string]*ArtifactRow
	accumulatorRows map[string]*TestamentRow
	testamentRows   map[string]*TestamentRow
}

func newClaimRowIndex() *claimRowIndex {
	return &claimRowIndex{
		claimRows:       make(map[string]*ClaimRow),
		artifactRows:    make(map[string]*ArtifactRow),
		accumulatorRows: make(map[string]*TestamentRow),
		testamentRows:   make(map[string]*TestamentRow),
	}
}

// Reset clears all index state. Called on session switch.
func (idx *claimRowIndex) Reset() {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.claimRows = make(map[string]*ClaimRow)
	idx.artifactRows = make(map[string]*ArtifactRow)
	idx.accumulatorRows = make(map[string]*TestamentRow)
	idx.testamentRows = make(map[string]*TestamentRow)
}

// ─── ClaimRow ──────────────────────────────────────────────────────

func (idx *claimRowIndex) getClaimRow(cycleID string) *ClaimRow {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.claimRows[strings.TrimSpace(cycleID)]
}

func (idx *claimRowIndex) ensureClaimRow(cycleID string) *ClaimRow {
	cycleID = strings.TrimSpace(cycleID)
	if cycleID == "" {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if r, ok := idx.claimRows[cycleID]; ok {
		return r
	}
	r := &ClaimRow{
		CycleID:   cycleID,
		Status:    ClaimRowStatusPending,
		StartedAt: time.Now(),
	}
	idx.claimRows[cycleID] = r
	return r
}

// openCycleRow opens (or refreshes) a cycle row from a
// ClaimsAgentStatusMsg{Active=true} event. ownerAgentID is the
// resolved cycle owner — i.e. the agent doing the work — never the
// issuer. Idempotent: re-opening a cycle just refreshes attribution.
func (idx *claimRowIndex) openCycleRow(cycleID, ownerAgentID, sessionID, title, actionType string) *ClaimRow {
	cycleID = strings.TrimSpace(cycleID)
	if cycleID == "" {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	r, ok := idx.claimRows[cycleID]
	if !ok {
		r = &ClaimRow{CycleID: cycleID, Status: ClaimRowStatusInProgress, StartedAt: time.Now()}
		idx.claimRows[cycleID] = r
	}
	if ownerAgentID != "" {
		r.OwnerAgentID = ownerAgentID
		r.OwnerAgentType = ownerAgentID
	}
	if sessionID != "" {
		r.SessionID = sessionID
	}
	if title != "" {
		r.Title = title
	}
	if r.Status == ClaimRowStatusPending {
		r.Status = ClaimRowStatusInProgress
	}
	return r
}

// closeCycleRow flips a cycle row to its terminal status. outcome is
// the bridge's TerminalOutcome string ("success" / "failure" / "").
// Empty outcome maps to a generic terminal close that doesn't
// override an explicit verdict — accepted/rejected pre-set by a
// status delta wins.
func (idx *claimRowIndex) closeCycleRow(cycleID, outcome string) *ClaimRow {
	cycleID = strings.TrimSpace(cycleID)
	if cycleID == "" {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	r, ok := idx.claimRows[cycleID]
	if !ok {
		return nil
	}
	switch outcome {
	case "success":
		r.Status = ClaimRowStatusAccepted
	case "failure":
		r.Status = ClaimRowStatusRejected
	}
	r.EndedAt = time.Now()
	return r
}

// updateClaimContext applies a ClaimContextMsg to the cycle row,
// guarding against out-of-order delivery via ContextSequence. Returns
// the updated row (or nil when nothing changed).
func (idx *claimRowIndex) updateClaimContext(cycleID, ctxValue string, sequence int64) *ClaimRow {
	cycleID = strings.TrimSpace(cycleID)
	if cycleID == "" {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	r, ok := idx.claimRows[cycleID]
	if !ok {
		// Lazy-create — context updates can arrive before the
		// claim_created handler fires when the bridge auto-adopts.
		r = &ClaimRow{CycleID: cycleID, Status: ClaimRowStatusInProgress, StartedAt: time.Now()}
		idx.claimRows[cycleID] = r
	}
	if sequence > 0 && sequence <= r.ContextSequence {
		return nil
	}
	r.Context = ctxValue
	if sequence > 0 {
		r.ContextSequence = sequence
	}
	return r
}

// ─── ArtifactRow ───────────────────────────────────────────────────

func (idx *claimRowIndex) getArtifactRow(artifactID string) *ArtifactRow {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.artifactRows[strings.TrimSpace(artifactID)]
}

// addArtifactRow inserts a new artifact row and links it to its
// parent — either another ArtifactRow (cross-claim nesting via
// ParentRowID) or the top-level ClaimRow (when ParentRowID is empty,
// the cycle's root row owns the child).
func (idx *claimRowIndex) addArtifactRow(row *ArtifactRow) *ArtifactRow {
	if row == nil {
		return nil
	}
	row.ArtifactID = strings.TrimSpace(row.ArtifactID)
	if row.ArtifactID == "" {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if existing, ok := idx.artifactRows[row.ArtifactID]; ok {
		// Idempotent: a duplicate started artifact (e.g. replay) is a
		// no-op. We keep the original row so children that already
		// linked don't get orphaned.
		return existing
	}
	idx.artifactRows[row.ArtifactID] = row
	parent := strings.TrimSpace(row.ParentRowID)
	if parent != "" {
		if pr, ok := idx.artifactRows[parent]; ok {
			pr.Children = append(pr.Children, row.ArtifactID)
			return row
		}
		// Parent hasn't arrived yet — leave the link recorded on the
		// row; renderers walk row.ParentRowID directly so the absent
		// parent's Children slice is not load-bearing for nesting.
		return row
	}
	if cycle := strings.TrimSpace(row.CycleID); cycle != "" {
		cr, ok := idx.claimRows[cycle]
		if !ok {
			cr = &ClaimRow{CycleID: cycle, Status: ClaimRowStatusInProgress, StartedAt: row.StartedAt}
			idx.claimRows[cycle] = cr
		}
		cr.Artifacts = append(cr.Artifacts, row.ArtifactID)
	}
	return row
}

// completeArtifactRow flips a started row to its terminal state.
// startArtifactID identifies the original *_started row (as carried
// on ClaimArtifactCompletedMsg.StartArtifactID).
func (idx *claimRowIndex) completeArtifactRow(
	startArtifactID string,
	status ArtifactRowStatus,
	endedAt time.Time,
	duration time.Duration,
	output, errMsg string,
	metadata map[string]any,
) *ArtifactRow {
	startArtifactID = strings.TrimSpace(startArtifactID)
	if startArtifactID == "" {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	r, ok := idx.artifactRows[startArtifactID]
	if !ok {
		return nil
	}
	r.Status = status
	r.EndedAt = endedAt
	r.Duration = duration
	if output != "" {
		r.Output = output
	}
	if errMsg != "" {
		r.ErrorMsg = errMsg
	}
	if metadata != nil {
		if r.Metadata == nil {
			r.Metadata = metadata
		} else {
			for k, v := range metadata {
				r.Metadata[k] = v
			}
		}
	}
	return r
}

// ─── TestamentRow ──────────────────────────────────────────────────

// getAccumulatorRow looks up the in-flight testament row by
// AccumulatorID (the synthetic anchor an accumulator stamps onto its
// emissions before flush).
func (idx *claimRowIndex) getAccumulatorRow(accumulatorID string) *TestamentRow {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.accumulatorRows[strings.TrimSpace(accumulatorID)]
}

// getTestamentRow looks up the sealed testament row by TestamentID
// (the durable board ID stamped at flush time).
func (idx *claimRowIndex) getTestamentRow(testamentID string) *TestamentRow {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.testamentRows[strings.TrimSpace(testamentID)]
}

// getTestamentRowByAnyID resolves a testament row regardless of which
// anchor the caller has — accumulator first (in-flight), testament
// second (sealed). Single lookup convenience for query APIs.
func (idx *claimRowIndex) getTestamentRowByAnyID(id string) *TestamentRow {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if r, ok := idx.accumulatorRows[id]; ok {
		return r
	}
	return idx.testamentRows[id]
}

// updateTestamentContext applies a TestamentContextMsg's narrative.
// Resolves via accumulatorRows first (in-flight anchor), falls back
// to testamentRows (sealed anchor). Lazy-creates if neither is found
// so the chat row appears as soon as the agent's first
// acc.SetContext fires. When a delta arrives carrying both
// AccumulatorID and TestamentID, registers the row under both maps
// so subsequent lookups by either ID find the same row.
func (idx *claimRowIndex) updateTestamentContext(
	accumulatorID, testamentID, claimID, cycleID, parentRowID, agentID, contextValue string,
	sequence int64,
) *TestamentRow {
	accumulatorID = strings.TrimSpace(accumulatorID)
	testamentID = strings.TrimSpace(testamentID)
	if accumulatorID == "" && testamentID == "" {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	row := idx.resolveTestamentRowLocked(accumulatorID, testamentID, claimID, cycleID, parentRowID, agentID)
	if sequence > 0 && sequence <= row.ContextSequence {
		return nil
	}
	row.Context = contextValue
	if sequence > 0 {
		row.ContextSequence = sequence
	}
	return row
}

// resolveTestamentRowLocked finds-or-creates a TestamentRow given
// available IDs, registering it in both accumulatorRows and
// testamentRows as IDs become known. Caller holds idx.mu.
//
// Lookup precedence: accumulatorID (in-flight) → testamentID
// (sealed). If neither lookup hits, a new row is created.
func (idx *claimRowIndex) resolveTestamentRowLocked(
	accumulatorID, testamentID, claimID, cycleID, parentRowID, agentID string,
) *TestamentRow {
	row := idx.lookupTestamentLocked(accumulatorID, testamentID)
	if row == nil {
		row = &TestamentRow{
			AccumulatorID: accumulatorID,
			TestamentID:   testamentID,
			ClaimID:       strings.TrimSpace(claimID),
			CycleID:       strings.TrimSpace(cycleID),
			ParentRowID:   strings.TrimSpace(parentRowID),
			AgentID:       strings.TrimSpace(agentID),
			StartedAt:     time.Now(),
		}
		idx.registerTestamentLocked(row, accumulatorID, testamentID)
		return row
	}
	idx.fillTestamentMetadataLocked(row, accumulatorID, testamentID, claimID, cycleID, parentRowID, agentID)
	return row
}

func (idx *claimRowIndex) lookupTestamentLocked(accumulatorID, testamentID string) *TestamentRow {
	if accumulatorID != "" {
		if r, ok := idx.accumulatorRows[accumulatorID]; ok {
			return r
		}
	}
	if testamentID != "" {
		if r, ok := idx.testamentRows[testamentID]; ok {
			return r
		}
	}
	return nil
}

func (idx *claimRowIndex) registerTestamentLocked(row *TestamentRow, accumulatorID, testamentID string) {
	if accumulatorID != "" {
		idx.accumulatorRows[accumulatorID] = row
	}
	if testamentID != "" {
		idx.testamentRows[testamentID] = row
	}
}

func (idx *claimRowIndex) fillTestamentMetadataLocked(
	row *TestamentRow,
	accumulatorID, testamentID, claimID, cycleID, parentRowID, agentID string,
) {
	if accumulatorID != "" && row.AccumulatorID == "" {
		row.AccumulatorID = accumulatorID
		idx.accumulatorRows[accumulatorID] = row
	}
	if testamentID != "" && row.TestamentID == "" {
		row.TestamentID = testamentID
		idx.testamentRows[testamentID] = row
	}
	if claimID != "" && row.ClaimID == "" {
		row.ClaimID = strings.TrimSpace(claimID)
	}
	if cycleID != "" && row.CycleID == "" {
		row.CycleID = strings.TrimSpace(cycleID)
	}
	if parentRowID != "" && row.ParentRowID == "" {
		row.ParentRowID = strings.TrimSpace(parentRowID)
	}
	if agentID != "" && row.AgentID == "" {
		row.AgentID = strings.TrimSpace(agentID)
	}
}

// sealTestament marks a testament row as sealed when the testament is
// submitted. Used by the testament_submitted handler to set Summary +
// Sealed=true. Lazy-creates the row if no in-flight accumulator anchor
// landed first.
func (idx *claimRowIndex) sealTestament(
	accumulatorID, testamentID, claimID, cycleID, agentID, summary string,
	sealedAt time.Time,
) *TestamentRow {
	accumulatorID = strings.TrimSpace(accumulatorID)
	testamentID = strings.TrimSpace(testamentID)
	if testamentID == "" && accumulatorID == "" {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	row := idx.lookupTestamentLocked(accumulatorID, testamentID)
	if row == nil {
		row = &TestamentRow{
			AccumulatorID: accumulatorID,
			TestamentID:   testamentID,
			ClaimID:       strings.TrimSpace(claimID),
			CycleID:       strings.TrimSpace(cycleID),
			AgentID:       strings.TrimSpace(agentID),
			StartedAt:     sealedAt,
		}
		idx.registerTestamentLocked(row, accumulatorID, testamentID)
	} else {
		idx.fillTestamentMetadataLocked(row, accumulatorID, testamentID, claimID, cycleID, "", agentID)
	}
	row.Summary = summary
	row.Sealed = true
	row.SealedAt = sealedAt
	return row
}
