package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/mock"
)

func TestPhase13GovernedProposalClaimBackedIdempotentAndRejectsToNegativeEvidence(t *testing.T) {
	sink := newMockForestProposalSink(t)
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true, ClaimProposalSink: sink})
	defer forest.Close()
	defer db.Close()

	input := ForestGovernedChange{
		Kind:                   ForestProposalKindPolicyPromotion,
		SubjectType:            "forest_policy",
		SubjectID:              "candidate-governed",
		Summary:                "Promote validated forest policy candidate candidate-governed",
		EvidenceRefs:           []string{"validation:policy-governed"},
		RollbackPath:           PolicyRollbackUsePreviousActive,
		GuardianReviewRequired: true,
	}
	sink.On("ProposeClaim", mock.Anything, mock.MatchedBy(func(proposal ForestClaimProposal) bool {
		return proposal.ID == governedProposalID(input.Kind, input.SubjectType, input.SubjectID, input.EvidenceRefs) &&
			proposal.Dimension == input.Kind &&
			proposal.GuardianReviewRequired &&
			len(proposal.EvidenceRefs) == len(input.EvidenceRefs)
	})).Return(nil).Once()

	first, err := forest.ProposeGovernedForestChange(context.Background(), input)
	if err != nil {
		t.Fatalf("propose governed change: %v", err)
	}
	second, err := forest.ProposeGovernedForestChange(context.Background(), input)
	if err != nil {
		t.Fatalf("propose governed duplicate: %v", err)
	}
	if first.ClaimID != second.ClaimID {
		t.Fatalf("duplicate claim id = %s want %s", second.ClaimID, first.ClaimID)
	}
	assertGovernanceProposal(t, db, first.ProposalID, ForestProposalStatusProposed, PolicyRollbackUsePreviousActive)

	if err := forest.RejectGovernanceProposal(context.Background(), first.ProposalID, "validation regression"); err != nil {
		t.Fatalf("reject proposal: %v", err)
	}
	assertGovernanceProposal(t, db, first.ProposalID, ForestProposalStatusRejected, PolicyRollbackUsePreviousActive)
	assertGovernanceQuarantine(t, db, input.SubjectType, input.SubjectID)
	assertNegativeMemeForSubject(t, db, input.SubjectID)
}

func TestPhase13GovernedProposalClaimEmissionRetriesAfterSinkFailure(t *testing.T) {
	sink := newMockForestProposalSink(t)
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true, ClaimProposalSink: sink})
	defer forest.Close()
	defer db.Close()

	input := ForestGovernedChange{
		Kind:                   ForestProposalKindRemediation,
		SubjectType:            "forest_outbreak",
		SubjectID:              "outbreak-retry",
		Summary:                "Retry remediation proposal after transient sink failure",
		EvidenceRefs:           []string{"validation:outbreak-retry"},
		RollbackPath:           "close_retry_remediation_without_activation",
		GuardianReviewRequired: true,
	}
	sink.On("ProposeClaim", mock.Anything, mock.AnythingOfType("forest.ForestClaimProposal")).
		Return(errors.New("sink temporarily unavailable")).
		Once()

	_, err := forest.ProposeGovernedForestChange(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "sink temporarily unavailable") {
		t.Fatalf("first proposal error = %v", err)
	}
	proposalID := governedProposalID(input.Kind, input.SubjectType, input.SubjectID, input.EvidenceRefs)
	var emittedAt int64
	var emitError string
	if err := db.QueryRow(`SELECT claim_emitted_at, claim_emit_error FROM forest_governance_proposals WHERE proposal_id = ?`, proposalID).Scan(&emittedAt, &emitError); err != nil {
		t.Fatalf("query failed proposal emission state: %v", err)
	}
	if emittedAt != 0 || !strings.Contains(emitError, "sink temporarily unavailable") {
		t.Fatalf("emission state after failure = emitted %d err %q", emittedAt, emitError)
	}

	sink.On("ProposeClaim", mock.Anything, mock.MatchedBy(func(proposal ForestClaimProposal) bool {
		return proposal.ID == proposalID && proposal.Dimension == input.Kind
	})).Return(nil).Once()
	retried, err := forest.ProposeGovernedForestChange(context.Background(), input)
	if err != nil {
		t.Fatalf("retry proposal: %v", err)
	}
	if retried.ClaimEmittedAt.IsZero() || retried.ClaimEmitError != "" {
		t.Fatalf("retried emission state = %#v", retried)
	}
}

func TestPhase13OutbreakRemediationCreatesGovernanceProposalAndClaim(t *testing.T) {
	sink := newMockForestProposalSink(t)
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true, ClaimProposalSink: sink})
	defer forest.Close()
	defer db.Close()

	refs := make([]string, 0, nodeProjectionRetryLimit())
	for idx := range nodeProjectionRetryLimit() {
		refs = append(refs, fmt.Sprintf("validation:outbreak:%d", idx))
	}
	candidate := outbreakCandidate{
		ClusterID:           "cluster-outbreak-governed",
		Dimension:           CorruptionContradiction,
		Corruption:          1,
		Immunity:            0,
		EvidenceCount:       len(refs),
		EvidenceRefs:        refs,
		CounterEvidenceRefs: []string{"claim:counter-outbreak"},
		SourceSeq:           int64(nodeProjectionRetryLimit()),
		BridgeSevere:        true,
	}
	expectedClaim := claimProposalForOutbreak(candidate)
	sink.On("ProposeClaim", mock.Anything, mock.MatchedBy(func(proposal ForestClaimProposal) bool {
		return proposal.ID == expectedClaim.ID &&
			proposal.Dimension == expectedClaim.Dimension &&
			proposal.SourceOutbreakID == expectedClaim.SourceOutbreakID &&
			proposal.GuardianReviewRequired
	})).Return(nil).Once()

	result, err := forest.upsertOutbreak(context.Background(), candidate)
	if err != nil {
		t.Fatalf("upsert outbreak: %v", err)
	}
	if result.ProposalsEmitted != 1 || result.OutbreaksUpdated != 1 {
		t.Fatalf("outbreak result = %+v", result)
	}
	proposalID := governedProposalID(ForestProposalKindRemediation, "forest_outbreak", expectedClaim.SourceOutbreakID, expectedClaim.EvidenceRefs)
	var kind, rollback string
	var emittedAt int64
	if err := db.QueryRow(`
		SELECT proposal_kind, rollback_path, claim_emitted_at
		FROM forest_governance_proposals
		WHERE proposal_id = ?
	`, proposalID).Scan(&kind, &rollback, &emittedAt); err != nil {
		t.Fatalf("query outbreak governance proposal: %v", err)
	}
	if kind != ForestProposalKindRemediation || rollback == "" || emittedAt == 0 {
		t.Fatalf("outbreak governance proposal = kind %q rollback %q emitted %d", kind, rollback, emittedAt)
	}
}

func TestPhase13GovernedProposalRequiresValidationEvidence(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()

	_, err := forest.ProposeGovernedForestChange(context.Background(), ForestGovernedChange{
		Kind:         ForestProposalKindPruning,
		SubjectType:  "forest_node",
		SubjectID:    "node-without-validation",
		Summary:      "Prune unvalidated node",
		RollbackPath: ForestProposalRollbackManualReview,
	})
	if err == nil || !strings.Contains(err.Error(), "validation evidence") {
		t.Fatalf("proposal without validation evidence err = %v", err)
	}
}

func TestPhase13SimultaneousProposalAttemptsDeduplicate(t *testing.T) {
	sink := newMockForestProposalSink(t)
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true, ClaimProposalSink: sink})
	defer forest.Close()
	defer db.Close()

	input := ForestGovernedChange{
		Kind:                   ForestProposalKindRemediation,
		SubjectType:            "forest_outbreak",
		SubjectID:              "outbreak-race",
		Summary:                "Remediate contradiction outbreak outbreak-race",
		EvidenceRefs:           []string{"validation:outbreak-race"},
		RollbackPath:           "close_remediation_proposal_without_activation",
		GuardianReviewRequired: true,
	}
	sink.On("ProposeClaim", mock.Anything, mock.AnythingOfType("forest.ForestClaimProposal")).Return(nil).Once()

	errs := make(chan error, nodeProjectionRetryLimit())
	var wg sync.WaitGroup
	for range nodeProjectionRetryLimit() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := forest.ProposeGovernedForestChange(context.Background(), input)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent proposal: %v", err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_governance_proposals WHERE proposal_kind = ? AND subject_id = ?`, input.Kind, input.SubjectID).Scan(&count); err != nil {
		t.Fatalf("count governance proposals: %v", err)
	}
	if count != 1 {
		t.Fatalf("governance proposals = %d, want 1", count)
	}
}

func TestPhase13PermissionBoundaryDetectsExpansionAndRequiresExactApproval(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()

	if err := forest.RequireAuthorityBoundary(context.Background(), "subject:none", ForestAuthorityDiff{}, ForestAuthorityApproval{}); err != nil {
		t.Fatalf("no permission change rejected: %v", err)
	}
	assertSkillPermissionRejected(t, forest, db, []string{"tool:shell"}, "approval claim")
	assertSkillPermissionRejected(t, forest, db, []string{"fs:/private"}, "approval claim")
	assertSkillPermissionRejected(t, forest, db, []string{"bypass guardian"}, "bypass")
	assertSkillPermissionRejected(t, forest, db, []string{"maybe more access"}, "ambiguous")

	claimID := "skill-permission-approved"
	appendAcceptedClaimDelta(t, forest, claimID)
	input := SkillCandidateInput{
		Name:                      "Network Evidence Reader Approved",
		RoleScope:                 "guardian",
		Trigger:                   "validated external evidence replay requested",
		SourceMemeIDs:             []string{"meme:external-evidence"},
		SourceValidationRefs:      []string{"validation:external-evidence"},
		RequestedPermissions:      []string{"network"},
		ExplicitPermissionClaimID: claimID,
		PermissionApprovalActor:   "guardian",
		PromotionRationale:        "Read externally referenced evidence only when a validated replay request requires it.",
		PermissionApprovalExpires: time.Now().UTC().Add(time.Hour),
	}
	predicted, _ := buildSkillCandidate(input)
	input.PermissionApprovalScope = []string{SkillCandidatePermissionScope(predicted.CandidateID)}
	candidate, err := forest.ProposeGeneratedSkillCandidate(context.Background(), input)
	if err != nil {
		t.Fatalf("permission approved skill: %v", err)
	}
	assertSkillCandidateStatus(t, db, candidate.CandidateID, SkillCandidateStatusProposed)
	assertAuthorityApprovalRecorded(t, db, claimID, "guardian", "network")
}

func TestPhase14RuntimeQueueAndWorkerGates(t *testing.T) {
	runtime := newRuntime(context.Background(), nil)
	if err := runtime.RegisterQueue("fixture-queue", nodeProjectionRetryLimit()); err != nil {
		t.Fatalf("register queue: %v", err)
	}
	runtime.RecordQueueOverflow("fixture-queue", "overflow fixture")
	snapshot := runtime.Snapshot()
	if len(snapshot.Queues) != 1 || snapshot.Queues[0].Overflows != 1 {
		t.Fatalf("queue snapshot = %+v", snapshot.Queues)
	}
	started := make(chan struct{})
	if err := runtime.StartWorker("fixture-worker", nodeProjectionRetryLimit(), func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	<-started
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Close(closeCtx); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	if got := runtime.Snapshot(); !got.Closed || got.Workers[0].Status != RuntimeWorkerStopped {
		t.Fatalf("runtime snapshot after close = %+v", got)
	}
}

func TestPhase14LedgerReplayToForestPacketIsDeterministicAndIdempotent(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()
	claimID := "claim-phase14-deterministic"
	insertPhase789Node(t, db, phase789NodeSpec{
		ID:      claimID,
		Session: "phase14",
		Kind:    ForestNodeClaim,
		Grade:   EvidenceGradeValidated,
		Seq:     int64(nodeProjectionRetryLimit()),
	})
	delta := claims.NewCanonicalDelta(
		claims.DeltaActionClaimSatisfied,
		"phase14",
		"phase14-board",
		uint64(nodeProjectionRetryLimit()),
		time.Now().UTC(),
		claims.DegradedAgentRef("guardian", "phase14 deterministic claim"),
		[]claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: claimID}},
		nil,
		map[string]any{"claim": map[string]any{"id": claimID}},
	)
	if _, err := forest.AppendCanonicalDelta(context.Background(), delta); err != nil {
		t.Fatalf("append deterministic delta: %v", err)
	}
	first, err := forest.Retrieve(context.Background(), Query{SessionID: "phase14", Query: claimID, Limit: nodeProjectionRetryLimit()})
	if err != nil {
		t.Fatalf("first retrieve: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("expected first deterministic ForestPacket")
	}
	if _, err := forest.AppendCanonicalDelta(context.Background(), delta); err != nil {
		t.Fatalf("append deterministic delta duplicate: %v", err)
	}
	second, err := forest.Retrieve(context.Background(), Query{SessionID: "phase14", Query: claimID, Limit: nodeProjectionRetryLimit()})
	if err != nil {
		t.Fatalf("second retrieve: %v", err)
	}
	if packetSignature(first) != packetSignature(second) {
		t.Fatalf("packet replay changed: %q != %q", packetSignature(first), packetSignature(second))
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_ledger WHERE subject_id = ?`, claimID).Scan(&count); err != nil {
		t.Fatalf("count deterministic claim ledger rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("ledger rows for idempotent claim = %d, want 1", count)
	}
}

func TestPhase14MockeryConfigNamesForestInterfaces(t *testing.T) {
	cfg := readFile(t, filepath.Join(repositoryRoot(t), ".mockery.yaml"))
	for _, name := range []string{
		"NeighborIndex",
		"TextSearcher",
		"ForestProposalSink",
		"SkillArtifactWriter",
		"PolicyOutcomeSource",
		"AgentTurnProvider",
		"GuardianApprover",
	} {
		if !strings.Contains(cfg, name+":") {
			t.Fatalf(".mockery.yaml missing forest interface %s", name)
		}
	}
}

func TestPhase15PartialMigrationFailsClosed(t *testing.T) {
	db := newPartialMigrationDB(t)
	defer db.Close()
	cfg := Config{DB: db, DisableBackgroundWorkers: true, SynchronousProjection: true}
	forest, err := New(cfg)
	if err == nil {
		forest.Close()
		t.Fatal("partial migration state opened successfully")
	}
	if !strings.Contains(err.Error(), "partial forest migration") && !strings.Contains(err.Error(), "incomplete forest migration") {
		t.Fatalf("partial migration error = %v", err)
	}
}

func TestPhase15ForestPacketRetrievalDoesNotWriteLegacyTables(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()
	insertPhase789Node(t, db, phase789NodeSpec{
		ID:      "phase15-node",
		Session: "phase15-session",
		Kind:    ForestNodeClaim,
		Grade:   EvidenceGradeValidated,
		Seq:     int64(nodeProjectionRetryLimit()),
	})
	assertTableAbsent(t, db, "forest_branches")
	packets, err := forest.Retrieve(context.Background(), Query{SessionID: "phase15-session", Query: "phase15", Limit: nodeProjectionRetryLimit()})
	if err != nil {
		t.Fatalf("retrieve forest packets: %v", err)
	}
	if len(packets) == 0 {
		t.Fatal("expected forest packet retrieval")
	}
	assertTableAbsent(t, db, "forest_branches")
}

func TestPhase15DocsAndSkillProposalCiteActiveArchitecture(t *testing.T) {
	root := repositoryRoot(t)
	memoryDoc := readFile(t, filepath.Join(root, "docs", "MEMORY_FOREST.md"))
	fabricDoc := readFile(t, filepath.Join(root, "docs", "FOREST_FABRIC_INTEGRATION.md"))
	if !strings.Contains(memoryDoc, "docs/EMERGENT_FOREST.md") || !strings.Contains(memoryDoc, "ForestPacket") {
		t.Fatalf("MEMORY_FOREST.md does not point at active ForestPacket architecture")
	}
	if strings.Contains(fabricDoc, "ClaimsHarvester") || strings.Contains(fabricDoc, "claims-harvester") {
		t.Fatalf("FOREST_FABRIC_INTEGRATION.md still names claims-harvester semantics")
	}

	candidate, _ := buildSkillCandidate(SkillCandidateInput{
		Name:                 "Doc Citing Skill",
		RoleScope:            "guardian",
		Trigger:              "validated doc citation evidence appears",
		SourceMemeIDs:        []string{"meme:doc-citation"},
		SourceValidationRefs: []string{"validation:doc-citation"},
		PromotionRationale:   "Use active memory forest docs when proposing generated skill behavior.",
	})
	if !strings.Contains(skillCandidateSafetyCase(candidate), "docs/EMERGENT_FOREST.md") {
		t.Fatalf("generated skill safety case does not cite active emergent forest docs")
	}
}

func assertSkillPermissionRejected(t testing.TB, forest *MemoryForest, db *sql.DB, permissions []string, want string) {
	t.Helper()
	candidate, err := forest.ProposeGeneratedSkillCandidate(context.Background(), SkillCandidateInput{
		Name:                 "Unsafe " + strings.Join(permissions, " "),
		RoleScope:            "architect",
		Trigger:              "validated unsafe permission proposal appears",
		SourceMemeIDs:        []string{"meme:unsafe-permission"},
		SourceValidationRefs: []string{"validation:unsafe-permission"},
		RequestedPermissions: permissions,
		PromotionRationale:   "Proposal should fail closed when authority metadata is unsafe.",
	})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("permission %v error = %v, want %q", permissions, err, want)
	}
	assertSkillCandidateStatus(t, db, candidate.CandidateID, SkillCandidateStatusRejected)
	assertGovernanceQuarantine(t, db, SkillCandidateArtifactType, candidate.CandidateID)
}

func assertGovernanceProposal(t testing.TB, db *sql.DB, proposalID, status, rollback string) {
	t.Helper()
	var gotStatus, gotRollback string
	err := db.QueryRow(`SELECT status, rollback_path FROM forest_governance_proposals WHERE proposal_id = ?`, proposalID).Scan(&gotStatus, &gotRollback)
	if err != nil {
		t.Fatalf("query governance proposal: %v", err)
	}
	if gotStatus != status || gotRollback != rollback {
		t.Fatalf("governance proposal status/rollback = %s/%s want %s/%s", gotStatus, gotRollback, status, rollback)
	}
}

func assertGovernanceQuarantine(t testing.TB, db *sql.DB, subjectType, subjectID string) {
	t.Helper()
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM forest_governance_quarantine WHERE subject_type = ? AND subject_id = ?`, subjectType, subjectID).Scan(&count)
	if err != nil {
		t.Fatalf("count governance quarantine: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected governance quarantine for %s/%s", subjectType, subjectID)
	}
}

func assertNegativeMemeForSubject(t testing.TB, db *sql.DB, subjectID string) {
	t.Helper()
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM forest_memes WHERE polarity = ? AND summary LIKE ?`, MemePolarityNegative, "%"+subjectID+"%").Scan(&count)
	if err != nil {
		t.Fatalf("count negative memes: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected negative meme for %s", subjectID)
	}
}

func assertAuthorityApprovalRecorded(t testing.TB, db *sql.DB, claimID, actor, permission string) {
	t.Helper()
	var gotActor, diff string
	err := db.QueryRow(`SELECT actor, permission_diff FROM forest_governance_approvals WHERE claim_id = ?`, claimID).Scan(&gotActor, &diff)
	if err != nil {
		t.Fatalf("query authority approval: %v", err)
	}
	if gotActor != actor || !strings.Contains(diff, permission) {
		t.Fatalf("authority approval = actor %s diff %s", gotActor, diff)
	}
}

func newPartialMigrationDB(t testing.TB) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", stableID("partial-migration", t.Name()))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE nodes (id TEXT PRIMARY KEY, domain INTEGER NOT NULL, node_type INTEGER NOT NULL, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create nodes: %v", err)
	}
	if err := ensureForestRolloutSchema(db); err != nil {
		t.Fatalf("ensure rollout schema: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM forest_migration_slices WHERE slice_order > 1`); err != nil {
		t.Fatalf("corrupt migration slices: %v", err)
	}
	return db
}

func countRows(t testing.TB, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func assertTableAbsent(t testing.TB, db *sql.DB, table string) {
	t.Helper()
	exists, err := tableExists(db, table)
	if err != nil {
		t.Fatalf("inspect %s: %v", table, err)
	}
	if exists {
		t.Fatalf("%s exists in active phase 15 schema", table)
	}
}

func repositoryRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}

func readFile(t testing.TB, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func packetSignature(packets []*ForestPacket) string {
	ids := make([]string, 0, len(packets))
	for _, packet := range packets {
		ids = append(ids, packet.Node.ID)
	}
	return strings.Join(ids, "|")
}
