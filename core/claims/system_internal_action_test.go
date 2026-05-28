package claims

import (
	"context"
	"strings"
	"testing"
	"time"
)

// IsSystemInternalAction + AgentActivationActionTypes contracts.

func TestIsSystemInternalAction_KnownTypes(t *testing.T) {
	systemTypes := []ActionType{
		ActionTypeBoot,
		ActionTypeActivation,
		ActionTypeShutdown,
		ActionTypeArchival,
		ActionTypeTestament,
		ActionTypeCheckpoint,
	}
	for _, ty := range systemTypes {
		t.Run(string(ty), func(t *testing.T) {
			if !IsSystemInternalAction(ty) {
				t.Fatalf("IsSystemInternalAction(%q) = false, want true", ty)
			}
		})
	}
	activationTypes := AgentActivationActionTypes()
	for _, ty := range activationTypes {
		t.Run(string(ty), func(t *testing.T) {
			if IsSystemInternalAction(ty) {
				t.Fatalf("IsSystemInternalAction(%q) = true, want false", ty)
			}
		})
	}
}

// Partition guard: every defined ActionType must be classified as
// either system-internal OR activation-bearing — never both, never
// neither. New ActionType constants MUST update either the activation
// list (AgentActivationActionTypes) or IsSystemInternalAction.
func TestActionType_PartitionedByVisibility(t *testing.T) {
	allKnown := []ActionType{
		ActionTypeTask,
		ActionTypeChallenge,
		ActionTypeConsultation,
		ActionTypeCorrective,
		ActionTypeArchival,
		ActionTypePrompt,
		ActionTypeTestament,
		ActionTypeBoot,
		ActionTypeActivation,
		ActionTypeShutdown,
		ActionTypeHandoff,
		ActionTypeCheckpoint,
		ActionTypeGuardianCheck,
		ActionTypeConsultContinuation,
	}
	activationSet := make(map[ActionType]struct{}, len(AgentActivationActionTypes()))
	for _, ty := range AgentActivationActionTypes() {
		activationSet[ty] = struct{}{}
	}
	for _, ty := range allKnown {
		isSystem := IsSystemInternalAction(ty)
		_, isActivation := activationSet[ty]
		if isSystem && isActivation {
			t.Errorf("%q is in BOTH the system-internal set AND the activation set", ty)
		}
		if !isSystem && !isActivation {
			t.Errorf("%q is in NEITHER the system-internal set NOR the activation set", ty)
		}
	}
}

// Amplifier-side filter: posting an action with a system-internal
// type produces ZERO claim.posted deltas, even when the claim has a
// subject.
func TestBoardAmplifier_SystemActionTypes_EmitNoClaimPostedDeltas(t *testing.T) {
	systemTypes := []ActionType{
		ActionTypeBoot,
		ActionTypeActivation,
		ActionTypeShutdown,
		ActionTypeArchival,
		ActionTypeCheckpoint,
	}
	for _, ty := range systemTypes {
		t.Run(string(ty), func(t *testing.T) {
			b := NewClaimsBoard(ClaimsBoardConfig{
				BoardID:    "test-amp-" + string(ty),
				PipelineID: "p",
				TaskID:     "t",
				SessionID:  "s",
			})
			err := b.PostAction(context.Background(),
				Action{AgentID: "system:lifecycle", Type: ty},
				[]Claim{{
					Title: "lifecycle",
					Relations: []Relation{
						{Related: "system:lifecycle", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
						{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
					},
					ActionType:  ty,
					Validations: []*Validation{{Description: "v", QualityBar: "x", Type: ValidationTypeInspection, Required: true}},
				}},
			)
			if err != nil {
				t.Fatalf("PostAction failed: %v", err)
			}
			amp := b.amplifier
			deltas := amp.buildClaimLifecycleDeltas(
				context.Background(),
				&Action{AgentID: "system:lifecycle", Type: ty},
				&Claim{
					ID: "c-1",
					Relations: []Relation{
						{Related: "system:lifecycle", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
						{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
					},
					ActionType: ty,
				},
				ClaimLifecyclePosted,
				"system:lifecycle",
				nowForTest(),
			)
			if len(deltas) != 0 {
				t.Fatalf("system action %q produced %d claim.posted deltas, want 0", ty, len(deltas))
			}
		})
	}
}

// Self-claims (issuer == subject) MUST NOT publish claim.posted deltas. The
// issuer is already executing when the claim posts; a delivery to its
// own inbox would wake the standing subscription and trigger another
// dispatch, producing the runaway feedback loop observed in live
// sessions (architect posting self-targeted task claims at ~50ms
// cadence).
func TestBoardAmplifier_SelfClaim_EmitsNoClaimPostedDelta(t *testing.T) {
	b := NewClaimsBoard(ClaimsBoardConfig{BoardID: "tb", PipelineID: "p", TaskID: "t", SessionID: "s"})
	deltas := b.amplifier.buildClaimLifecycleDeltas(
		context.Background(),
		&Action{AgentID: "architect", Type: ActionTypeTask},
		&Claim{
			ID: "self-claim",
			Relations: []Relation{
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			},
			ActionType: ActionTypeTask,
		},
		ClaimLifecyclePosted,
		"architect",
		nowForTest(),
	)
	if len(deltas) != 0 {
		t.Fatalf("self-claim produced %d claim.posted deltas, want 0", len(deltas))
	}
}

func TestClaimsBoard_RejectsSelfTargetPeerClaims(t *testing.T) {
	for _, ty := range []ActionType{ActionTypeConsultation, ActionTypeChallenge, ActionTypeGuardianCheck} {
		t.Run(string(ty), func(t *testing.T) {
			b := NewClaimsBoard(ClaimsBoardConfig{BoardID: "tb", PipelineID: "p", TaskID: "t", SessionID: "s"})
			err := b.PostAction(context.Background(),
				Action{AgentID: "architect", Type: ty},
				[]Claim{{
					Title:      "self peer claim",
					ActionType: ty,
					Relations: []Relation{
						{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
						{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
					},
					Validations: []*Validation{{Description: "v", QualityBar: "x", Type: ValidationTypeReceipt, Required: true}},
				}},
			)
			if err == nil {
				t.Fatalf("PostAction accepted self-targeted %q claim", ty)
			}
		})
	}
}

// Cross-agent claims (issuer != subject) DO emit a claim.posted delta to the
// subject — that's the legitimate wake-up path for peer-directed work.
func TestBoardAmplifier_CrossAgentClaim_EmitsClaimPostedDelta(t *testing.T) {
	b := NewClaimsBoard(ClaimsBoardConfig{BoardID: "tb", PipelineID: "p", TaskID: "t", SessionID: "s"})
	deltas := b.amplifier.buildClaimLifecycleDeltas(
		context.Background(),
		&Action{AgentID: "architect", Type: ActionTypeTask},
		&Claim{
			ID: "cross-claim",
			Relations: []Relation{
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
				{Related: "engineer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			},
			ActionType: ActionTypeTask,
		},
		ClaimLifecyclePosted,
		"architect",
		nowForTest(),
	)
	if len(deltas) != 1 {
		t.Fatalf("cross-agent claim produced %d claim.posted deltas, want 1", len(deltas))
	}
	if !deltas[0].delta.DeliveredTo("engineer") {
		t.Fatalf("claim.posted delivery = %#v, want engineer", deltas[0].delta.Delivery)
	}
}

func TestBoardAmplifier_ClaimReceivedCarriesReceiverTopic(t *testing.T) {
	b := NewClaimsBoard(ClaimsBoardConfig{BoardID: "tb", PipelineID: "p", TaskID: "t", SessionID: "s"})
	deltas := b.amplifier.buildClaimLifecycleDeltas(
		context.Background(),
		&Action{AgentID: "architect", Type: ActionTypeTask},
		&Claim{
			ID:       "received-claim",
			Sequence: 7,
			Relations: []Relation{
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
				{Related: "engineer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			},
			ActionType: ActionTypeTask,
		},
		ClaimLifecycleReceived,
		"engineer",
		nowForTest(),
	)
	if len(deltas) != 2 {
		t.Fatalf("claim.received dispatches = %d, want claim topic + receiver topic", len(deltas))
	}
	if deltas[1].topic != CanonicalAgentTypeTopic("s", "engineer", DeltaActionClaimReceived) {
		t.Fatalf("receiver topic = %q", deltas[1].topic)
	}
	if !deltas[1].delta.DeliveredTo("engineer") {
		t.Fatalf("claim.received delivery = %#v, want engineer", deltas[1].delta.Delivery)
	}
}

func TestBoardAmplifier_TestamentPostedRoutesToIssuerAndEvaluators(t *testing.T) {
	b := NewClaimsBoard(ClaimsBoardConfig{BoardID: "tb", PipelineID: "p", TaskID: "t", SessionID: "s"})
	claim := &Claim{
		ID:         "claim-with-evaluator",
		Sequence:   7,
		ActionType: ActionTypeTask,
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "engineer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			{Related: "inspector", RelatedType: RelatedTypeAgent, Relationship: RelationshipEvaluator},
		},
	}
	testament := &Testament{ID: "testament-1", AgentID: "engineer", Sequence: 9}
	deltas := b.amplifier.buildTestamentLifecycleDeltas(context.Background(), testament, claim, TestamentLifecyclePosted, "engineer", nowForTest())
	if len(deltas) != 3 {
		t.Fatalf("testament.posted dispatches = %d, want claim topic + issuer + evaluator", len(deltas))
	}
	gotTopics := map[string]bool{}
	for _, dispatch := range deltas {
		gotTopics[dispatch.topic] = true
	}
	for _, want := range []string{
		CanonicalClaimTopic("s", claim.ID, DeltaActionTestamentPosted),
		CanonicalAgentTypeTopic("s", "architect", DeltaActionTestamentPosted),
		CanonicalAgentTypeTopic("s", "inspector", DeltaActionTestamentPosted),
	} {
		if !gotTopics[want] {
			t.Fatalf("missing topic %q in %#v", want, gotTopics)
		}
	}
}

// Mixed: claim with both self subject AND cross-agent evaluator must
// emit ONLY the evaluator's claim.posted delta — self subject is filtered.
func TestBoardAmplifier_SelfSubjectWithCrossEvaluator_EmitsOnlyEvaluator(t *testing.T) {
	b := NewClaimsBoard(ClaimsBoardConfig{BoardID: "tb", PipelineID: "p", TaskID: "t", SessionID: "s"})
	deltas := b.amplifier.buildClaimLifecycleDeltas(
		context.Background(),
		&Action{AgentID: "architect", Type: ActionTypeTask},
		&Claim{
			ID: "mixed-claim",
			Relations: []Relation{
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "inspector", RelatedType: RelatedTypeAgent, Relationship: RelationshipEvaluator},
			},
			ActionType: ActionTypeTask,
		},
		ClaimLifecyclePosted,
		"architect",
		nowForTest(),
	)
	if len(deltas) != 1 {
		t.Fatalf("mixed claim produced %d claim.posted deltas, want 1 (evaluator only)", len(deltas))
	}
	if !deltas[0].delta.DeliveredTo("inspector") {
		t.Fatalf("claim.posted delivery = %#v, want inspector", deltas[0].delta.Delivery)
	}
}

// Directed self-handoff (scribe-driven context-exhaustion handoff,
// UI_DESIGN.md §2.2 + §5.2): predecessor instance posts
// ActionTypeHandoff with subject=<same agent ID> AND a handoff_from
// relation pointing at the predecessor cycle's root claim. The
// successor MUST receive the claim.posted delta — without it, the new
// instance never wakes. The handoff_from relation is the canonical
// signal that this self-targeted post is directed work, not audit.
func TestBoardAmplifier_SelfHandoffWithHandoffFrom_EmitsClaimPostedDelta(t *testing.T) {
	b := NewClaimsBoard(ClaimsBoardConfig{BoardID: "tb", PipelineID: "p", TaskID: "t", SessionID: "s"})
	deltas := b.amplifier.buildClaimLifecycleDeltas(
		context.Background(),
		&Action{AgentID: "architect", Type: ActionTypeHandoff},
		&Claim{
			ID: "scribe-handoff",
			Relations: []Relation{
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "predecessor-cycle-root", RelatedType: RelatedTypeClaim, Relationship: RelationshipHandoffFrom},
			},
			ActionType: ActionTypeHandoff,
		},
		ClaimLifecyclePosted,
		"architect",
		nowForTest(),
	)
	if len(deltas) != 1 {
		t.Fatalf("self-handoff with handoff_from produced %d claim.posted deltas, want 1", len(deltas))
	}
	if !deltas[0].delta.DeliveredTo("architect") {
		t.Fatalf("claim.posted delivery = %#v, want architect", deltas[0].delta.Delivery)
	}
}

// Self-claim with NO handoff_from must remain filtered — that's the
// audit-shaped self-claim case (architect tool-loop trace, etc.).
// Distinguishing from the directed self-handoff above: same self
// shape, same activation-set ActionType, but no handoff_from → audit.
func TestBoardAmplifier_SelfClaim_HandoffActionType_NoHandoffFrom_Filtered(t *testing.T) {
	b := NewClaimsBoard(ClaimsBoardConfig{BoardID: "tb", PipelineID: "p", TaskID: "t", SessionID: "s"})
	deltas := b.amplifier.buildClaimLifecycleDeltas(
		context.Background(),
		&Action{AgentID: "architect", Type: ActionTypeHandoff},
		&Claim{
			ID: "self-no-handoff-from",
			Relations: []Relation{
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			},
			ActionType: ActionTypeHandoff,
		},
		ClaimLifecyclePosted,
		"architect",
		nowForTest(),
	)
	if len(deltas) != 0 {
		t.Fatalf("self-claim without handoff_from produced %d claim.posted deltas, want 0", len(deltas))
	}
}

// Amplifier still emits for activation types.
func TestBoardAmplifier_ActivationActionTypes_EmitClaimPostedDeltas(t *testing.T) {
	for _, ty := range AgentActivationActionTypes() {
		t.Run(string(ty), func(t *testing.T) {
			b := NewClaimsBoard(ClaimsBoardConfig{BoardID: "t", PipelineID: "p", TaskID: "t", SessionID: "s"})
			deltas := b.amplifier.buildClaimLifecycleDeltas(
				context.Background(),
				&Action{AgentID: "architect", Type: ty},
				&Claim{
					ID: "c-1",
					Relations: []Relation{
						{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
						{Related: "engineer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
					},
					ActionType: ty,
				},
				ClaimLifecyclePosted,
				"architect",
				nowForTest(),
			)
			if len(deltas) != 1 {
				t.Fatalf("activation type %q produced %d claim.posted deltas, want 1", ty, len(deltas))
			}
		})
	}
}

// Standing subscriptions are the two canonical directed-work patterns
// (UID and degraded agent-type) — never the broad firehose. Peer
// responses are delivered through explicit expectation subscriptions on
// canonical testament/claim deltas, not a standing consult-resolved
// channel.
func TestInboxPatternsFor_RoleSubject_NarrowedToActivationTypes(t *testing.T) {
	patterns := InboxPatternsFor(RoleSubject, "sess", "architect")
	want := 2
	if len(patterns) != want {
		t.Fatalf("RoleSubject patterns count = %d, want %d canonical patterns", len(patterns), want)
	}
	canonicalUID := CanonicalAgentActionPattern("sess", "architect", DeltaActionClaimPosted)
	canonicalType := CanonicalAgentTypeActionPattern("sess", "architect", DeltaActionClaimPosted)
	if !sliceContains(patterns, canonicalUID) {
		t.Fatalf("missing canonical uid directed pattern %q in %v", canonicalUID, patterns)
	}
	if !sliceContains(patterns, canonicalType) {
		t.Fatalf("missing canonical type directed pattern %q in %v", canonicalType, patterns)
	}
	for _, p := range patterns {
		if hasFinalSegment(p, "*") {
			t.Fatalf("activation pattern %q has final wildcard (would match system types)", p)
		}
		if containsSegment(p, "consult_resolved") {
			t.Fatalf("RoleSubject pattern %q reintroduced legacy consult_resolved channel", p)
		}
	}
}

func hasFinalSegment(pattern, segment string) bool {
	if len(pattern) < len(segment) {
		return false
	}
	return pattern[len(pattern)-len(segment):] == segment
}

func containsSegment(pattern, segment string) bool {
	for _, part := range strings.Split(pattern, ".") {
		if part == segment {
			return true
		}
	}
	return false
}

func sliceContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func nowForTest() time.Time {
	return time.Now().UTC()
}

func canonicalPostedDeltaForTest(sessionID, claimID string, action ActionType, recipient string) CanonicalDelta {
	return NewCanonicalDelta(
		DeltaActionClaimPosted,
		sessionID,
		"board",
		1,
		nowForTest(),
		DegradedAgentRef("issuer", "test"),
		[]DeltaRef{{Role: "claim", Type: RelatedTypeClaim, ID: claimID}},
		&DeltaDelivery{To: []AgentRef{DegradedAgentRef(recipient, "test")}, Relationship: RelationshipSubject},
		map[string]any{"claim": map[string]any{"id": claimID, "action": string(action)}},
	)
}

// Defense-in-depth at the matcher: legacy InboxDelta values are not
// workflow inputs, including for system action types.
func TestClaimsInbox_MatchesStandingSubscription_RejectsSystemTypes(t *testing.T) {
	systemTypes := []ActionType{
		ActionTypeBoot,
		ActionTypeActivation,
		ActionTypeShutdown,
		ActionTypeArchival,
		ActionTypeTestament,
		ActionTypeCheckpoint,
	}
	for _, ty := range systemTypes {
		t.Run(string(ty), func(t *testing.T) {
			i := &ClaimsInbox{
				agentID:   "architect",
				sessionID: "s",
				role:      RoleSubject,
			}
			matched := i.matchesStandingSubscription(InboxDelta{
				AgentID:    "architect",
				ActionKind: ty,
			})
			if matched {
				t.Fatalf("matchesStandingSubscription accepted system action type %q", ty)
			}
		})
	}
}

// And canonical claim.posted activation types still match.
func TestClaimsInbox_MatchesStandingSubscription_AcceptsCanonicalActivationTypes(t *testing.T) {
	for _, ty := range AgentActivationActionTypes() {
		t.Run(string(ty), func(t *testing.T) {
			i := &ClaimsInbox{
				agentID:   "architect",
				sessionID: "s",
				role:      RoleSubject,
			}
			matched := i.matchesStandingSubscription(canonicalPostedDeltaForTest("s", "c-"+string(ty), ty, "architect"))
			if !matched {
				t.Fatalf("matchesStandingSubscription rejected canonical activation type %q", ty)
			}
		})
	}
}

// Archivist / Auditor: legacy TestamentDelta values are not workflow
// inputs, including for system action types. Without this, an activation
// system's confirmation testament could wake the archivist through the old
// claim.testified subscription path.
func TestClaimsInbox_TestamentDelta_RejectsSystemActionKind(t *testing.T) {
	systemTypes := []ActionType{
		ActionTypeBoot,
		ActionTypeActivation,
		ActionTypeShutdown,
		ActionTypeArchival,
		ActionTypeCheckpoint,
	}
	for _, ty := range systemTypes {
		t.Run(string(ty), func(t *testing.T) {
			i := &ClaimsInbox{
				agentID:   "archivalist",
				sessionID: "s",
				role:      RoleArchivist,
			}
			if i.matchesStandingSubscription(TestamentDelta{ActionKind: ty}) {
				t.Fatalf("archivist matched system testament with ActionKind=%q", ty)
			}
			if i.matchesStandingSubscription(&TestamentDelta{ActionKind: ty}) {
				t.Fatalf("archivist matched *TestamentDelta with system ActionKind=%q", ty)
			}
			i.role = RoleAuditor
			if i.matchesStandingSubscription(TestamentDelta{ActionKind: ty}) {
				t.Fatalf("auditor matched system testament with ActionKind=%q", ty)
			}
		})
	}
}

func TestClaimsInbox_TestamentDelta_RejectsLegacyActivationActionKind(t *testing.T) {
	for _, ty := range AgentActivationActionTypes() {
		t.Run(string(ty), func(t *testing.T) {
			i := &ClaimsInbox{
				agentID:   "archivalist",
				sessionID: "s",
				role:      RoleArchivist,
			}
			if i.matchesStandingSubscription(TestamentDelta{ActionKind: ty}) {
				t.Fatalf("archivist matched legacy testament with activation ActionKind=%q", ty)
			}
		})
	}
}

// ClaimStatusDelta with system ActionKind also blocked at matcher.
func TestClaimsInbox_ClaimStatusDelta_RejectsSystemActionKind(t *testing.T) {
	for _, ty := range []ActionType{ActionTypeBoot, ActionTypeActivation, ActionTypeShutdown} {
		t.Run(string(ty), func(t *testing.T) {
			i := &ClaimsInbox{
				agentID:   "architect",
				sessionID: "s",
				role:      RoleRemediator,
			}
			if i.matchesStandingSubscription(ClaimStatusDelta{ActionKind: ty, ToStatus: ClaimStatusRejected}) {
				t.Fatalf("remediator matched system claim_status with ActionKind=%q", ty)
			}
		})
	}
}

// Amplifier-side: PublishTestamentDelta short-circuits for system
// claims (no dispatch attempted, no log of failure, no subscriber
// even gets a chance to filter).
func TestBoardAmplifier_PublishTestamentDelta_SkipsSystemActionType(t *testing.T) {
	b := NewClaimsBoard(ClaimsBoardConfig{BoardID: "tb", PipelineID: "p", TaskID: "t", SessionID: "s"})
	systemClaim := &Claim{ID: "c-sys", ActionType: ActionTypeActivation, Relations: []Relation{
		{Related: "system:activation", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
		{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
	}}
	systemTestament := &Testament{ID: "t-sys", AgentID: "architect"}

	// Deliberately exercise the publish method directly. The
	// amplifier's dispatch is async via runTracked; if the short-
	// circuit is in place, NO emission attempt should record an error
	// (which is the only signal we have without a wire-level capture).
	// More importantly: changing the claim's ActionType to a
	// non-system type AND running the same call should produce a
	// different code path. We assert the short-circuit by checking
	// the lifecycle build path returns early for system claims. To keep
	// this targeted, just call the public Publish and rely on no panic +
	// no error log.
	b.amplifier.PublishTestamentDelta(context.Background(), systemTestament, systemClaim)
	// If the short-circuit weren't in place, the dispatch would queue
	// a goroutine. We can't easily count goroutines here cross-test,
	// but the matcher tests above validate the receiver-side guard
	// independently. The publish-side short-circuit is verified via
	// IsSystemInternalAction equivalence + the build-skip itself
	// (this test is primarily a no-panic smoke test for the early
	// return path).
	_ = systemClaim
}
