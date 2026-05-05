package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/providers"
)

// SetScope injects the goroutine scope for async claims dispatch.
func (a *Archivalist) SetScope(scope *concurrency.GoroutineScope) {
	a.scope = scope
}

// archivalistBoard resolves the claims board for the archivalist's session.
func (a *Archivalist) archivalistBoard() *claims.ClaimsBoard {
	return claims.DefaultSessionBoardRegistry().Lookup(a.defaultSessionID)
}

// archivalistScope adapts *concurrency.GoroutineScope to claims.ScopeProvider.
func (a *Archivalist) archivalistScope() claims.ScopeProvider {
	if a.scope == nil {
		return nil
	}
	return &archivalistScopeAdapter{scope: a.scope}
}

type archivalistScopeAdapter struct {
	scope *concurrency.GoroutineScope
}

func (ad *archivalistScopeAdapter) Go(desc string, timeout time.Duration, fn func(context.Context) error) error {
	return ad.scope.Go(desc, timeout, concurrency.WorkFunc(fn))
}

// archivalistSubmitTestament submits a testament async via scope. Best-effort.
func (a *Archivalist) archivalistSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := a.archivalistBoard()
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "archivalist", Type: claims.ActionTypeTestament}
	if a.scope != nil {
		if err := a.scope.Go("archivalist_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("archivalist_submit_testament_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("archivalist testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("archivalist_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("archivalist testament: " + err.Error())
	}
}

// archivalistTestament builds a testament issued by the archivalist.
func (a *Archivalist) archivalistTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID: "archivalist", SessionID: a.defaultSessionID,
		Summary: summary, Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "archivalist", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// archivalistArtifact builds a single artifact.
func (a *Archivalist) archivalistArtifact(kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID: "archivalist", SessionID: a.defaultSessionID,
		Kind: kind, Reference: reference,
	}
}

// archivalistJSONArtifact builds a JSON-serialized artifact.
func (a *Archivalist) archivalistJSONArtifact(kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return a.archivalistArtifact(kind, string(ref))
}

// archivalistPostClaim posts a claim async via scope. Best-effort.
// Registers a just-in-time response Expectation on the archivalist's
// inbox after a successful PostAction (CLAIMS.md §5).
func (a *Archivalist) archivalistPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := a.archivalistBoard()
	if board == nil {
		return
	}
	posted := []claims.Claim{claim}
	if a.scope != nil {
		if err := a.scope.Go("archivalist_post_claim", 5*time.Second, func(gctx context.Context) error {
			if err := board.PostAction(gctx, action, posted); err != nil {
				return err
			}
			claims.RegisterPostActionExpectations(a.claimsInbox, action, posted)
			return nil
		}); err != nil {
			slog.Error("archivalist_post_claim_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("archivalist post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, posted); err != nil {
		slog.Error("archivalist_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("archivalist post claim: " + err.Error())
		return
	}
	claims.RegisterPostActionExpectations(a.claimsInbox, action, posted)
}

// archivalistConsultClaim builds a consultation claim against a peer.
func archivalistConsultClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeConsultation,
		Relations: []claims.Relation{
			{Related: "archivalist", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// archivalistValidation builds a pending validation.
func archivalistValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type: vtype, Required: required,
		Description: description, QualityBar: qualityBar,
		Status: claims.ValidationStatusPending,
	}
}

func (a *Archivalist) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}

	// RoleArchivist fast path: every claim.*.testified delta in the
	// session arrives here as a TestamentDelta-bearing entry. These
	// are NOT directed at the archivalist as a subject — they're
	// observed via standing subscription so the archivalist can
	// persist testaments produced by other agents. Running the full
	// LLM tool loop on each one would be both expensive and
	// unnecessary: persistence is mechanical, not deliberative. The
	// fast path runs the curation predicate, persists what passes,
	// and returns without touching the provider.
	if a.shouldHandleViaArchivePersistFastPath(entry) {
		return a.persistTestamentEntry(ctx, entry)
	}

	p := a.getProvider()
	if p == nil {
		return fmt.Errorf("archivalist: LLM provider not configured")
	}

	acc := shared.NewClaimsEntryAccumulator("archivalist", a.defaultSessionID, entry)
	acc.WithBoard(a.archivalistBoard())
	defer acc.Flush(ctx, a.archivalistBoard(), a.archivalistScope())
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	if entry.Node.Claim != nil {
		shared.RecordAgentState(ctx, a.archivalistBoard(), entry.Node.Claim.ID,
			"Acknowledging request", shared.AgentStateReasoning, nil)
	}

	userMessage := shared.ComposeClaimsEntryPrompt(entry)
	board := a.archivalistBoard()
	userMessage = claims.PrependBoardPreamble(userMessage, board, "archivalist")
	a.prepareSkillsForInput(userMessage)

	systemPrompt := a.config.SystemPrompt
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        a.buildToolDefinitions(),
		Model:        a.CurrentModel(),
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyConversationRuntimeProfile(req)

	ledger := a.steering.Create(entry.Delta.DeltaKey(), a.id, a.defaultSessionID, nil, nil)
	defer a.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	result, err := shared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return a.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		if shared.IsConsultYielded(err) {
			slog.Info("archivalist_claims_entry_yielded", "delta_key", entry.Delta.DeltaKey())
			return nil
		}
		slog.Error("archivalist_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// truncateArchivalist truncates a string for claim titles.
func truncateArchivalist(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}

// shouldHandleViaArchivePersistFastPath reports whether a claims
// entry should be handled by the persistence fast path instead of
// the LLM tool loop.
//
// Yes when:
//   - The delta is a TestamentDelta (claim.*.testified) — i.e. some
//     other agent's testament arrived via RoleArchivist standing
//     subscription, not a directed action against the archivalist.
//   - The testament's issuer is not the archivalist itself — we
//     don't want to recursively persist our own commit testaments.
//
// No when:
//   - The delta is a directed claim/action where the archivalist is
//     subject (a user or peer asking the archivalist to do work) —
//     those need the full skill surface to dispatch correctly.
//   - The entry is malformed (no testament resolved on the node).
func (a *Archivalist) shouldHandleViaArchivePersistFastPath(entry *claims.GraphEntryPoint) bool {
	if entry == nil {
		return false
	}
	if _, ok := entry.Delta.(claims.TestamentDelta); !ok {
		if _, okPtr := entry.Delta.(*claims.TestamentDelta); !okPtr {
			return false
		}
	}
	if entry.Node.Testament == nil {
		return false
	}
	issuer := claims.IssuerAgentID(entry.Node.Testament.Relations)
	if strings.EqualFold(strings.TrimSpace(issuer), "archivalist") {
		return false
	}
	return true
}

// persistTestamentEntry runs the fast-path persistence routine for
// a peer-issued testament. Curation is intentionally minimal at this
// layer: the archivalist owns its own retention policy, but the
// canonical record is the claims board itself — Store entries are a
// queryable index, not the source of truth. We persist every passing
// testament, letting downstream queries narrow as needed.
//
// Curation predicate (apply in this order):
//
//  1. Drop testaments without a Summary (nothing meaningful to index).
//  2. Drop testaments with no Artifacts (the response_text artifact
//     is the headline payload — its absence usually means the
//     accumulator flushed an error path with notes only, which
//     downstream consumers can recover from the board directly).
//  3. Otherwise, persist with category Timeline (default for
//     observational records) and source = the testament's issuer.
//
// Persistence emits an archivalist-issued testament confirming the
// archive write, so the board carries a closed loop: peer testament
// → archivalist persistence testament → board record. No
// fire-and-forget RPC into the archivalist, no invented "store
// health check result" dispatcher: every persistence is observable
// and auditable through the same mechanism every other agent action
// goes through.
func (a *Archivalist) persistTestamentEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	t := entry.Node.Testament
	if strings.TrimSpace(t.Summary) == "" {
		return nil
	}
	if len(t.Artifacts) == 0 {
		return nil
	}
	issuer := claims.IssuerAgentID(t.Relations)
	if issuer == "" {
		issuer = "unknown"
	}

	storeEntry := &Entry{
		Category:  CategoryTimeline,
		Title:     truncateArchivalist(t.Summary, 200),
		Content:   buildTestamentArchiveContent(t),
		Source:    SourceModel(issuer),
		SessionID: a.defaultSessionID,
		Metadata: map[string]any{
			"testament_id":   t.ID,
			"confidence":     t.Confidence,
			"artifact_kinds": testamentArtifactKinds(t),
		},
	}
	if claimID := claims.ClaimIDFromRelations(t.Relations); claimID != "" {
		storeEntry.RelatedIDs = []string{claimID}
	}

	id, err := a.store.InsertEntryInSession(a.defaultSessionID, storeEntry)
	if err != nil {
		slog.Error("archivalist_persist_testament_failed",
			"error", err.Error(),
			"testament_id", t.ID,
			"issuer", issuer,
		)
		return err
	}

	a.archivalistSubmitTestament(ctx, a.archivalistTestament(
		"Archived testament from "+issuer,
		"committed",
		[]*claims.Artifact{
			a.archivalistArtifact("archive_entry_id", id),
			a.archivalistArtifact("source_testament_id", t.ID),
			a.archivalistArtifact("issuer", issuer),
		},
	))
	return nil
}

// buildTestamentArchiveContent renders a testament + its artifacts
// into a single content blob suitable for full-text indexing in the
// archive. Headline is the Summary; body is each artifact rendered
// as `[kind] reference` so the search surface covers both response
// text and structured evidence.
func buildTestamentArchiveContent(t *claims.Testament) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(t.Summary))
	for _, art := range t.Artifacts {
		if art == nil {
			continue
		}
		ref := strings.TrimSpace(art.Reference)
		if ref == "" {
			continue
		}
		b.WriteString("\n\n[")
		b.WriteString(art.Kind)
		b.WriteString("] ")
		b.WriteString(ref)
	}
	return b.String()
}

// testamentArtifactKinds returns the distinct artifact kinds on a
// testament — useful as a Metadata facet for archive queries that
// want to find e.g. "all archived testaments with response_text" or
// "with error artifacts".
func testamentArtifactKinds(t *claims.Testament) []string {
	if len(t.Artifacts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(t.Artifacts))
	out := make([]string, 0, len(t.Artifacts))
	for _, art := range t.Artifacts {
		if art == nil {
			continue
		}
		kind := strings.TrimSpace(art.Kind)
		if kind == "" {
			continue
		}
		if _, dup := seen[kind]; dup {
			continue
		}
		seen[kind] = struct{}{}
		out = append(out, kind)
	}
	return out
}
