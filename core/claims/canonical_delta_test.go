package claims

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
)

func TestCanonicalDelta_RoundTripEachAction(t *testing.T) {
	for _, action := range KnownDeltaActions() {
		d := testCanonicalDelta(action)
		data, err := MarshalDelta(d)
		if err != nil {
			t.Fatalf("%s marshal: %v", action, err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("%s raw decode: %v", action, err)
		}
		if _, ok := raw["kind"]; ok {
			t.Fatalf("%s encoded legacy kind field", action)
		}
		decoded, err := UnmarshalDelta(data)
		if err != nil {
			t.Fatalf("%s unmarshal: %v", action, err)
		}
		got, ok := decoded.(CanonicalDelta)
		if !ok {
			t.Fatalf("%s decoded %T, want CanonicalDelta", action, decoded)
		}
		if got.Action != action {
			t.Fatalf("%s action round-trip = %s", action, got.Action)
		}
		if got.DeltaKey() != d.DeltaKey() {
			t.Fatalf("%s key changed: %q != %q", action, got.DeltaKey(), d.DeltaKey())
		}
	}
}

func TestDeltaAction_PartitionedByCompletionSemantics(t *testing.T) {
	cases := map[DeltaAction]bool{
		DeltaActionClaimGenerated:             false,
		DeltaActionClaimPosted:                false,
		DeltaActionClaimProgressed:            false,
		DeltaActionTestamentGenerated:         false,
		DeltaActionTestamentPosted:            true,
		DeltaActionValidationEvaluated:        true,
		DeltaActionClaimTestamentAcknowledged: true,
		DeltaActionClaimSatisfied:             true,
		DeltaActionClaimValidationFailed:      true,
	}
	for _, action := range KnownDeltaActions() {
		if got := KnownDeltaAction(action); !got {
			t.Fatalf("KnownDeltaAction(%q) = false", action)
		}
	}
	for action, want := range cases {
		if got := DeltaActionMayCompleteExpectedWork(action); got != want {
			t.Fatalf("DeltaActionMayCompleteExpectedWork(%q) = %v, want %v", action, got, want)
		}
	}
}

func TestDeltaAction_LifecycleCoverageAndActionability(t *testing.T) {
	for _, status := range KnownClaimLifecycleStatuses() {
		action, ok := ClaimLifecycleDeltaAction(status)
		if !ok {
			t.Fatalf("claim lifecycle status %q has no canonical delta action", status)
		}
		if got, ok := DeltaActionClaimLifecycleStatus(action); !ok || got != status {
			t.Fatalf("claim action %q maps back to (%q, %v), want %q", action, got, ok, status)
		}
	}
	for _, status := range KnownTestamentLifecycleStatuses() {
		action, ok := TestamentLifecycleDeltaAction(status)
		if !ok {
			t.Fatalf("testament lifecycle status %q has no canonical delta action", status)
		}
		if got, ok := DeltaActionTestamentLifecycleStatus(action); !ok || got != status {
			t.Fatalf("testament action %q maps back to (%q, %v), want %q", action, got, ok, status)
		}
	}
	for _, status := range KnownArtifactStatuses() {
		action, ok := ArtifactLifecycleDeltaAction(status)
		if !ok {
			t.Fatalf("artifact lifecycle status %q has no canonical delta action", status)
		}
		if got, ok := DeltaActionArtifactLifecycleStatus(action); !ok || got != status {
			t.Fatalf("artifact action %q maps back to (%q, %v), want %q", action, got, ok, status)
		}
	}
	for _, status := range KnownValidationLifecycleStatuses() {
		action, ok := ValidationLifecycleDeltaAction(status)
		if !ok {
			t.Fatalf("validation lifecycle status %q has no canonical delta action", status)
		}
		if got, ok := DeltaActionValidationLifecycleStatus(action); !ok || got != status {
			t.Fatalf("validation action %q maps back to (%q, %v), want %q", action, got, ok, status)
		}
	}
	if DeltaActionStartsClaimWork(DeltaActionClaimGenerated) {
		t.Fatal("claim.generated must not start claim work")
	}
	if DeltaActionStartsClaimWork(DeltaActionTestamentGenerated) {
		t.Fatal("testament.generated must not start claim work")
	}
	if !DeltaActionStartsClaimWork(DeltaActionClaimPosted) {
		t.Fatal("claim.posted must be the only claim work-start action")
	}
	if !DeltaActionIsGenerated(DeltaActionClaimGenerated) || !DeltaActionIsGenerated(DeltaActionTestamentGenerated) {
		t.Fatal("generated lifecycle actions were not classified as generated")
	}
}

func TestCanonicalDelta_StrictValidation(t *testing.T) {
	d := testCanonicalDelta(DeltaActionClaimPosted)
	if err := ValidateCanonicalDeltaStrict(d); err != nil {
		t.Fatalf("valid delta rejected: %v", err)
	}
	d.Action = DeltaAction("future.action")
	if err := ValidateCanonicalDeltaStrict(d); err == nil {
		t.Fatal("strict validation accepted unknown action")
	}
	if err := ValidateCanonicalDeltaTolerant(d); err != nil {
		t.Fatalf("tolerant validation rejected unknown action: %v", err)
	}
	d.SessionID = ""
	if err := ValidateCanonicalDeltaTolerant(d); err == nil {
		t.Fatal("missing session accepted")
	}
}

func TestCanonicalDelta_StrictValidationRequiresDeliveryForReceiverFacts(t *testing.T) {
	for _, action := range KnownDeltaActions() {
		d := testCanonicalDelta(action)
		d.Delivery = nil
		err := ValidateCanonicalDeltaStrict(d)
		if DeltaActionRequiresDelivery(action) {
			if err == nil {
				t.Fatalf("%s accepted without receiver delivery", action)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s rejected without delivery: %v", action, err)
		}
	}
}

func TestCanonicalDelta_TolerantUnmarshalAllowsUnknownAction(t *testing.T) {
	d := testCanonicalDelta(DeltaActionClaimPosted)
	d.Action = DeltaAction("future.action")
	d.Key = BuildCanonicalDeltaKeyForSequence(d.Action, d.SessionID, d.BoardID, d.Sequence, d.Refs, d.Delivery)
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnmarshalDelta(data); err == nil {
		t.Fatal("strict unmarshal accepted unknown canonical action")
	}
	decoded, err := UnmarshalDeltaTolerant(data)
	if err != nil {
		t.Fatalf("tolerant unmarshal rejected unknown canonical action: %v", err)
	}
	got := decoded.(CanonicalDelta)
	if got.Action != DeltaAction("future.action") {
		t.Fatalf("action = %q", got.Action)
	}
}

func TestCanonicalDeltaKey_DeterministicAndDeliverySensitive(t *testing.T) {
	refs := []DeltaRef{{Role: "claim", Type: RelatedTypeClaim, ID: "c1"}}
	toA := &DeltaDelivery{To: []AgentRef{DegradedAgentRef("librarian", "test")}, Relationship: RelationshipSubject}
	toB := &DeltaDelivery{To: []AgentRef{DegradedAgentRef("architect", "test")}, Relationship: RelationshipSubject}
	keyA1 := BuildCanonicalDeltaKey(DeltaActionClaimReceived, "s", "b", refs, toA)
	keyA2 := BuildCanonicalDeltaKey(DeltaActionClaimReceived, "s", "b", refs, toA)
	keyB := BuildCanonicalDeltaKey(DeltaActionClaimReceived, "s", "b", refs, toB)
	if keyA1 != keyA2 {
		t.Fatalf("key is not deterministic: %q != %q", keyA1, keyA2)
	}
	if keyA1 == keyB {
		t.Fatal("different recipients produced identical keys")
	}
}

func TestCanonicalDeltaKey_SequenceSensitiveAndReplayStable(t *testing.T) {
	refs := []DeltaRef{{Role: "claim", Type: RelatedTypeClaim, ID: "c1"}}
	d1 := NewCanonicalDelta(DeltaActionClaimPosted, "s", "b", 7, time.Unix(1, 0), DegradedAgentRef("architect", "test"), refs, &DeltaDelivery{To: []AgentRef{DegradedAgentRef("librarian", "test")}, Relationship: RelationshipSubject}, nil)
	replay := NewCanonicalDelta(DeltaActionClaimPosted, "s", "b", 7, time.Unix(2, 0), DegradedAgentRef("architect", "test"), refs, &DeltaDelivery{To: []AgentRef{DegradedAgentRef("librarian", "test")}, Relationship: RelationshipSubject}, nil)
	next := NewCanonicalDelta(DeltaActionClaimPosted, "s", "b", 8, time.Unix(3, 0), DegradedAgentRef("architect", "test"), refs, &DeltaDelivery{To: []AgentRef{DegradedAgentRef("librarian", "test")}, Relationship: RelationshipSubject}, nil)
	if d1.DeltaKey() != replay.DeltaKey() {
		t.Fatalf("replayed committed fact key changed: %q != %q", d1.DeltaKey(), replay.DeltaKey())
	}
	if d1.DeltaKey() == next.DeltaKey() {
		t.Fatalf("different committed sequence reused key %q", d1.DeltaKey())
	}
	if !strings.Contains(d1.DeltaKey(), "seq:7") {
		t.Fatalf("delta key %q does not include committed sequence", d1.DeltaKey())
	}
}

func TestAgentRefFromIdentityAndDegradedRef(t *testing.T) {
	id := identity.RebuildForReplay(identity.ReplayAgentIdentity{
		UID:        "uid-1",
		Namespace:  "sess",
		Pod:        identity.PodRef{ID: "knowledge", Type: identity.PodTypeSingleton},
		Name:       "librarian",
		Kind:       identity.AgentTypeLibrarian,
		Category:   identity.CategoryKnowledge,
		Model:      identity.ModelID("claude"),
		Generation: 2,
		Labels:     identity.Labels{"scope": "knowledge"},
	})
	task := identity.RebuildTaskForReplay(identity.ReplayTaskRef{
		UID:       "task-1",
		Namespace: "sess",
		DisplayID: "default",
		Pipeline:  &identity.PipelineRef{ID: "pipe-1"},
	})
	ref := AgentRefFromIdentity(id, task)
	if err := ref.Validate(); err != nil {
		t.Fatalf("canonical ref invalid: %v", err)
	}
	if ref.UID != "uid-1" || ref.Type != "librarian" || ref.Category != string(ParticipantCategoryAgent) || ref.Task.PipelineID != "pipe-1" {
		t.Fatalf("unexpected ref: %+v", ref)
	}
	degraded := DegradedAgentRef("librarian", "legacy")
	if !degraded.Unresolved {
		t.Fatal("degraded ref should be unresolved")
	}
	if err := degraded.Validate(); err != nil {
		t.Fatalf("degraded ref invalid: %v", err)
	}
	if err := (AgentRef{Type: "librarian"}).Validate(); err == nil {
		t.Fatal("missing uid without unresolved=true accepted")
	}
}

func TestCanonicalDeltaStrictValidationAcceptsServiceParticipantRef(t *testing.T) {
	participant, err := NewServiceParticipantRegistration("tool_runtime", map[string]string{"session": "sess"}, 4, 1, time.Second, []ActionType{ActionTypeTask})
	if err != nil {
		t.Fatalf("NewServiceParticipantRegistration: %v", err)
	}
	delta := NewCanonicalDelta(
		DeltaActionClaimPosted,
		"sess",
		"board",
		1,
		time.Unix(1, 0),
		AgentRef{UID: "issuer-uid", Type: "architect", Category: string(ParticipantCategoryAgent), Generation: 1},
		[]DeltaRef{{Role: "claim", Type: RelatedTypeClaim, ID: "claim-1"}},
		&DeltaDelivery{To: []AgentRef{participant.AgentRef()}, Relationship: RelationshipSubject},
		map[string]any{"claim": map[string]any{"id": "claim-1", "action": string(ActionTypeTask)}},
	)
	if err := ValidateCanonicalDeltaStrict(delta); err != nil {
		t.Fatalf("strict service delta rejected: %v", err)
	}
	if got := delta.Delivery.To[0].Category; got != string(ParticipantCategoryService) {
		t.Fatalf("delivery category = %q, want service", got)
	}
}

func TestCanonicalAgentRefTopicUsesTypeRouteForDegradedRefsEvenWithUID(t *testing.T) {
	ref := AgentRef{UID: "spoofed-uid", Type: "librarian", Unresolved: true, ResolutionReason: "legacy relation"}.Normalized()
	topic := CanonicalAgentRefTopic("session", ref, DeltaActionClaimPosted)
	if strings.Contains(topic, "."+TopicSegmentAgent+".") {
		t.Fatalf("degraded ref used canonical uid topic: %s", topic)
	}
	if !strings.Contains(topic, "."+TopicSegmentAgentType+".librarian.") {
		t.Fatalf("degraded ref topic = %s, want agent_type route", topic)
	}
}

func TestAgentRefResolverMismatchDegradesRef(t *testing.T) {
	resolver := AgentRefResolverFunc(func(context.Context, string, string) (AgentRef, bool) {
		return AgentRef{
			UID:        "uid-architect",
			Namespace:  "sess",
			Pod:        "global",
			Name:       "architect",
			Type:       "architect",
			Generation: 1,
			Model:      "claude",
		}, true
	})
	amp := NewBoardAmplifier("sess", "task", "board").WithAgentRefResolver(resolver)
	ref := amp.resolveAgentRef(context.Background(), "librarian", "test")
	if !ref.Unresolved {
		t.Fatalf("mismatched resolver ref should degrade: %+v", ref)
	}
	if ref.RouteKey() != "librarian" {
		t.Fatalf("degraded route key = %q", ref.RouteKey())
	}
}

func TestCanonicalTestamentPostedContextAndArtifactHeaders(t *testing.T) {
	amp := NewBoardAmplifier("sess", "task", "board")
	longContext := strings.Repeat("x", canonicalTestamentContextMax+64)
	testament := &Testament{
		ID:         "testament-1",
		AgentID:    "librarian",
		SessionID:  "sess",
		Sequence:   12,
		Summary:    longContext,
		Confidence: "committed",
		Artifacts: []*Artifact{{
			ID:        "artifact-response",
			Kind:      ArtifactKindResponseText,
			Reference: longContext,
			Presentation: &Presentation{
				Audiences: []PresentationAudience{PresentationAudienceUser},
				Surfaces:  []PresentationSurface{PresentationSurfaceChat},
				Format:    PresentationFormatMarkdown,
				Placement: PresentationPlacementAfterResponse,
				Title:     "Answer",
			},
		}},
	}
	claim := &Claim{
		ID:         "claim-1",
		ActionType: ActionTypeConsultation,
		Status:     ClaimStatusTestified,
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "librarian", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
	}
	delta := amp.buildCanonicalTestamentLifecycle(context.Background(), testament, claim, DeltaActionTestamentPosted, TestamentLifecyclePosted, testament.AgentID, time.Now())
	testaments, ok := delta.Context["testaments"].([]map[string]any)
	if !ok || len(testaments) != 1 {
		t.Fatalf("testaments context = %#v", delta.Context["testaments"])
	}
	entry := testaments[0]
	if entry["context_truncated"] != true {
		t.Fatalf("context_truncated = %#v", entry["context_truncated"])
	}
	if entry["context_artifact_id"] != "artifact-response" {
		t.Fatalf("context_artifact_id = %#v", entry["context_artifact_id"])
	}
	if got := entry["context"].(string); len(got) <= canonicalTestamentContextMax || len(got) > canonicalTestamentContextMax+3 {
		t.Fatalf("context length = %d", len(got))
	}
	headers, ok := entry["artifacts"].([]map[string]any)
	if !ok || len(headers) != 1 {
		t.Fatalf("artifact headers = %#v", entry["artifacts"])
	}
	presentation, ok := headers[0]["presentation"].(map[string]any)
	if !ok {
		t.Fatalf("presentation header missing: %#v", headers[0])
	}
	if presentation["format"] != string(PresentationFormatMarkdown) ||
		presentation["placement"] != string(PresentationPlacementAfterResponse) ||
		presentation["title"] != "Answer" {
		t.Fatalf("presentation header = %#v", presentation)
	}
}

func testCanonicalDelta(action DeltaAction) CanonicalDelta {
	delivery := (*DeltaDelivery)(nil)
	if DeltaActionRequiresDelivery(action) {
		delivery = &DeltaDelivery{
			To:           []AgentRef{DegradedAgentRef("librarian", "test")},
			Relationship: RelationshipSubject,
		}
	}
	return NewCanonicalDelta(
		action,
		"sess",
		"board",
		42,
		time.Unix(100, 0).UTC(),
		DegradedAgentRef("architect", "test"),
		[]DeltaRef{
			{Role: "action", Type: RelatedTypeAction, ID: "a1"},
			{Role: "claim", Type: RelatedTypeClaim, ID: "c1"},
			{Role: "testament", Type: RelatedTypeTestament, ID: "t1"},
			{Role: "artifact", Type: RelatedTypeArtifact, ID: "art1"},
			{Role: "validation", Type: RelatedTypeValidation, ID: "v1"},
		},
		delivery,
		map[string]any{
			"claim": map[string]any{
				"id":     "c1",
				"action": string(ActionTypeConsultation),
				"status": string(ClaimStatusPending),
			},
		},
	)
}
