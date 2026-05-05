package claims

import (
	"encoding/json"
	"testing"
	"time"
)

func TestClaimStatusIsTerminal(t *testing.T) {
	terminal := []ClaimStatus{ClaimStatusAccepted, ClaimStatusRejected, ClaimStatusSuperseded}
	nonTerminal := []ClaimStatus{ClaimStatusPending, ClaimStatusInProgress, ClaimStatusTestified}

	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("expected %q to be terminal", s)
		}
	}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("expected %q to not be terminal", s)
		}
	}
}

func TestClaimStatusIsActive(t *testing.T) {
	active := []ClaimStatus{ClaimStatusPending, ClaimStatusInProgress}
	inactive := []ClaimStatus{ClaimStatusTestified, ClaimStatusAccepted, ClaimStatusRejected, ClaimStatusSuperseded}

	for _, s := range active {
		if !s.IsActive() {
			t.Errorf("expected %q to be active", s)
		}
	}
	for _, s := range inactive {
		if s.IsActive() {
			t.Errorf("expected %q to not be active", s)
		}
	}
}

func TestActionStatusIsTerminal(t *testing.T) {
	if !ActionStatusComplete.IsTerminal() {
		t.Error("complete should be terminal")
	}
	if !ActionStatusFailed.IsTerminal() {
		t.Error("failed should be terminal")
	}
	if ActionStatusPending.IsTerminal() {
		t.Error("pending should not be terminal")
	}
}

func TestValidationStatusIsTerminal(t *testing.T) {
	terminal := []ValidationStatus{ValidationStatusPassed, ValidationStatusFailed, ValidationStatusSkipped}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("expected %q to be terminal", s)
		}
	}
	if ValidationStatusPending.IsTerminal() {
		t.Error("pending should not be terminal")
	}
}

func TestBoardPhaseIsTerminal(t *testing.T) {
	if !BoardPhaseComplete.IsTerminal() {
		t.Error("complete should be terminal")
	}
	if BoardPhaseImplementation.IsTerminal() {
		t.Error("implementation should not be terminal")
	}
	if BoardPhaseValidation.IsTerminal() {
		t.Error("validation should not be terminal")
	}
}

func TestScopeOverlaps(t *testing.T) {
	a := []ClaimScopeEntry{{Kind: "file", Key: "services/auth/jwk.go"}}
	b := []ClaimScopeEntry{{Kind: "file", Key: "services/auth/jwk.go"}}
	c := []ClaimScopeEntry{{Kind: "file", Key: "services/api/routes.go"}}

	if !ScopeOverlaps(a, b) {
		t.Error("identical entries should overlap")
	}
	if ScopeOverlaps(a, c) {
		t.Error("disjoint entries should not overlap")
	}
	if ScopeOverlaps(nil, b) {
		t.Error("nil first arg should not overlap")
	}
	if ScopeOverlaps(a, nil) {
		t.Error("nil second arg should not overlap")
	}
}

func TestScopeOverlapsPartial(t *testing.T) {
	a := []ClaimScopeEntry{
		{Kind: "file", Key: "a.go"},
		{Kind: "file", Key: "b.go"},
	}
	b := []ClaimScopeEntry{
		{Kind: "file", Key: "b.go"},
		{Kind: "file", Key: "c.go"},
	}
	if !ScopeOverlaps(a, b) {
		t.Error("partially overlapping entries should overlap")
	}
}

func TestClaimJSONRoundTrip(t *testing.T) {
	c := Claim{
		ID:        "claim-1",
		AgentID:   "inspector-a",
		SessionID: "ses-1",
		TaskID:    "task-1",
		Sequence:  42,
		Relations: []Relation{
			{Related: "inspector-a", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "engineer-b", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			{Related: "action-1", RelatedType: RelatedTypeAction, Relationship: RelationshipClaimAction},
		},
		Created:     time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC),
		Accessed:    time.Date(2026, 4, 22, 0, 1, 0, 0, time.UTC),
		Title:       "Implement HS256 JWK deserialization",
		Description: "Implement the DeserializeHS256JWK() function",
		Scope:       []ClaimScopeEntry{{Kind: "file", Key: "services/auth/jwk.go"}},
		ActionType:  ActionTypeTask,
		Status:      ClaimStatusPending,
		Priority:    75,
		Tags:        []string{"security", "blocking"},
		Iteration:   0,
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Claim
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != c.ID {
		t.Errorf("ID mismatch: %q vs %q", decoded.ID, c.ID)
	}
	if decoded.Title != c.Title {
		t.Errorf("Title mismatch: %q vs %q", decoded.Title, c.Title)
	}
	if len(decoded.Relations) != 3 {
		t.Errorf("Relations count: %d, want 3", len(decoded.Relations))
	}
	if decoded.Relations[0].Relationship != RelationshipIssuer {
		t.Errorf("first relation should be issuer, got %q", decoded.Relations[0].Relationship)
	}
	if decoded.Status != ClaimStatusPending {
		t.Errorf("Status: %q, want pending", decoded.Status)
	}
}

func TestTestamentJSONRoundTrip(t *testing.T) {
	te := Testament{
		ID:        "testament-1",
		AgentID:   "engineer-b",
		SessionID: "ses-1",
		TaskID:    "task-1",
		Sequence:  43,
		Relations: []Relation{
			{Related: "engineer-b", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "inspector-a", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			{Related: "claim-1", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		},
		Created:    time.Date(2026, 4, 22, 0, 2, 0, 0, time.UTC),
		Accessed:   time.Date(2026, 4, 22, 0, 2, 0, 0, time.UTC),
		Summary:    "Implemented JWK validation using HS256 in DeserializeHS256JWK()",
		Confidence: "committed",
		Duration:   4*time.Minute + 32*time.Second,
	}

	data, err := json.Marshal(te)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Testament
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Summary != te.Summary {
		t.Errorf("Summary mismatch")
	}
	if decoded.Confidence != "committed" {
		t.Errorf("Confidence: %q, want committed", decoded.Confidence)
	}
}

func TestArtifactImmutableFields(t *testing.T) {
	a := Artifact{
		ID:          "art-1",
		AgentID:     "engineer-b",
		Kind:        "code_reference",
		Reference:   "services/auth/jwk.go:47-89",
		ContentHash: "sha256:abc123",
		Size:        1247,
		Ephemeral:   false,
	}

	data, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Artifact
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Kind != "code_reference" {
		t.Errorf("Kind: %q", decoded.Kind)
	}
	if decoded.ContentHash != "sha256:abc123" {
		t.Errorf("ContentHash: %q", decoded.ContentHash)
	}
	if decoded.Size != 1247 {
		t.Errorf("Size: %d", decoded.Size)
	}
}

func TestValidationPassed(t *testing.T) {
	v := Validation{Status: ValidationStatusPassed}
	if !v.Passed() {
		t.Error("passed validation should report Passed()")
	}

	v.Status = ValidationStatusFailed
	if v.Passed() {
		t.Error("failed validation should not report Passed()")
	}

	v.Status = ValidationStatusPending
	if v.Passed() {
		t.Error("pending validation should not report Passed()")
	}
}

func TestRelationHelpers(t *testing.T) {
	relations := []Relation{
		{Related: "inspector-a", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
		{Related: "engineer-b", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		{Related: "action-1", RelatedType: RelatedTypeAction, Relationship: RelationshipClaimAction},
		{Related: "claim-2", RelatedType: RelatedTypeClaim, Relationship: RelationshipDependsOn},
		{Related: "claim-3", RelatedType: RelatedTypeClaim, Relationship: RelationshipDependsOn},
	}

	if IssuerAgentID(relations) != "inspector-a" {
		t.Error("IssuerAgentID should return inspector-a")
	}
	if SubjectAgentID(relations) != "engineer-b" {
		t.Error("SubjectAgentID should return engineer-b")
	}
	if ClaimActionID(relations) != "action-1" {
		t.Error("ClaimActionID should return action-1")
	}

	deps := FindRelationsByType(relations, RelationshipDependsOn)
	if len(deps) != 2 {
		t.Errorf("should find 2 depends_on relations, got %d", len(deps))
	}

	if !HasRelation(relations, RelationshipIssuer, "inspector-a") {
		t.Error("should find issuer=inspector-a")
	}
	if HasRelation(relations, RelationshipIssuer, "nobody") {
		t.Error("should not find issuer=nobody")
	}

	if FindRelation(relations, "nonexistent") != nil {
		t.Error("nonexistent relationship should return nil")
	}
}

func TestIssuerAgentIDEmpty(t *testing.T) {
	if IssuerAgentID(nil) != "" {
		t.Error("nil relations should return empty")
	}
	if IssuerAgentID([]Relation{}) != "" {
		t.Error("empty relations should return empty")
	}
}

func TestHandoffFromClaimID_Empty(t *testing.T) {
	if HandoffFromClaimID(nil) != "" {
		t.Error("nil relations should return empty")
	}
	if HandoffFromClaimID([]Relation{}) != "" {
		t.Error("empty relations should return empty")
	}
	if HandoffFromClaimID([]Relation{
		{Related: "claim-x", RelatedType: RelatedTypeClaim, Relationship: RelationshipCausedBy},
	}) != "" {
		t.Error("relations without handoff_from should return empty")
	}
}

func TestHandoffFromClaimID_Single(t *testing.T) {
	got := HandoffFromClaimID([]Relation{
		{Related: "predecessor-cycle-root", RelatedType: RelatedTypeClaim, Relationship: RelationshipHandoffFrom},
	})
	if got != "predecessor-cycle-root" {
		t.Fatalf("HandoffFromClaimID = %q, want predecessor-cycle-root", got)
	}
}

func TestHandoffFromClaimID_MultipleReturnsFirst(t *testing.T) {
	got := HandoffFromClaimID([]Relation{
		{Related: "first", RelatedType: RelatedTypeClaim, Relationship: RelationshipHandoffFrom},
		{Related: "second", RelatedType: RelatedTypeClaim, Relationship: RelationshipHandoffFrom},
	})
	if got != "first" {
		t.Fatalf("HandoffFromClaimID = %q, want first", got)
	}
}

func TestCompletesArtifactID_Empty(t *testing.T) {
	if CompletesArtifactID(nil) != "" {
		t.Error("nil relations should return empty")
	}
	if CompletesArtifactID([]Relation{}) != "" {
		t.Error("empty relations should return empty")
	}
	if CompletesArtifactID([]Relation{
		{Related: "art-x", RelatedType: RelatedTypeArtifact, Relationship: RelationshipCausedBy},
	}) != "" {
		t.Error("relations without completes should return empty")
	}
}

func TestCompletesArtifactID_Single(t *testing.T) {
	got := CompletesArtifactID([]Relation{
		{Related: "started-art-1", RelatedType: RelatedTypeArtifact, Relationship: RelationshipCompletes},
	})
	if got != "started-art-1" {
		t.Fatalf("CompletesArtifactID = %q, want started-art-1", got)
	}
}

func TestCompletesArtifactID_MultipleReturnsFirst(t *testing.T) {
	got := CompletesArtifactID([]Relation{
		{Related: "first-started", RelatedType: RelatedTypeArtifact, Relationship: RelationshipCompletes},
		{Related: "second-started", RelatedType: RelatedTypeArtifact, Relationship: RelationshipCompletes},
	})
	if got != "first-started" {
		t.Fatalf("CompletesArtifactID = %q, want first-started", got)
	}
}

func TestActionTypeHandoff_StringMatches(t *testing.T) {
	if string(ActionTypeHandoff) != "handoff" {
		t.Fatalf("ActionTypeHandoff = %q, want handoff", string(ActionTypeHandoff))
	}
}

// TestActionType_AllConstantsKnown asserts that the enumerated set of
// ActionType constants is the full set considered authoritative by the
// system. New ActionType constants must be added to this slice (and
// then any switch over ActionType audited for handling).
func TestActionType_AllConstantsKnown(t *testing.T) {
	known := []ActionType{
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
	}
	seen := make(map[ActionType]struct{}, len(known))
	for _, k := range known {
		if _, dup := seen[k]; dup {
			t.Fatalf("duplicate ActionType in known set: %q", k)
		}
		if string(k) == "" {
			t.Fatalf("ActionType has empty string value")
		}
		seen[k] = struct{}{}
	}
}
