package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ArtifactEvidenceCheck describes content requirements validators can apply
// to an artifact found through ordinary board queries. Presentation metadata
// is intentionally not a trust signal.
type ArtifactEvidenceCheck struct {
	Kind             string
	RequireReference bool
	RequiredMetadata map[string]any
}

// ArtifactEvidenceResult is the deterministic outcome of an artifact
// evidence check.
type ArtifactEvidenceResult struct {
	Passed   bool      `json:"passed"`
	Reasons  []string  `json:"reasons,omitempty"`
	Artifact *Artifact `json:"artifact,omitempty"`
}

// ValidateArtifactEvidence checks artifact content and metadata without
// considering whether the artifact is presentable to a user.
func ValidateArtifactEvidence(artifact *Artifact, check ArtifactEvidenceCheck) ArtifactEvidenceResult {
	result := ArtifactEvidenceResult{Artifact: CloneArtifact(artifact)}
	if artifact == nil {
		result.Reasons = append(result.Reasons, "artifact is missing")
		return result
	}
	if want := strings.TrimSpace(check.Kind); want != "" && strings.TrimSpace(artifact.Kind) != want {
		result.Reasons = append(result.Reasons, fmt.Sprintf("artifact kind %q does not match required kind %q", artifact.Kind, want))
	}
	if check.RequireReference && strings.TrimSpace(artifact.Reference) == "" {
		result.Reasons = append(result.Reasons, "artifact reference is empty")
	}
	for key, want := range check.RequiredMetadata {
		if strings.TrimSpace(key) == "" {
			continue
		}
		got, ok := artifact.Metadata[key]
		if !ok {
			result.Reasons = append(result.Reasons, fmt.Sprintf("metadata %q is missing", key))
			continue
		}
		if !metadataValueEqual(got, want) {
			result.Reasons = append(result.Reasons, fmt.Sprintf("metadata %q = %v, want %v", key, got, want))
		}
	}
	result.Passed = len(result.Reasons) == 0
	return result
}

// PlanMarkdownEvidenceCheck describes validator-facing requirements for the
// canonical human-reviewable plan markdown artifact.
type PlanMarkdownEvidenceCheck struct {
	PlanID            string
	Epoch             int64
	RequireChat       bool
	RequireApproval   bool
	ExpectedTaskCount int
	RequiredTaskText  []string
}

// PlanMarkdownEvidenceResult reports whether a plan_markdown artifact found
// on the board satisfies content, metadata, and presentation-surface checks.
type PlanMarkdownEvidenceResult struct {
	Passed      bool      `json:"passed"`
	Reasons     []string  `json:"reasons,omitempty"`
	Artifact    *Artifact `json:"artifact,omitempty"`
	TestamentID string    `json:"testament_id,omitempty"`
}

// ValidatePlanMarkdownEvidence finds the latest matching plan_markdown
// artifact via the board projection and validates it as normal evidence.
func ValidatePlanMarkdownEvidence(board *ClaimsBoard, check PlanMarkdownEvidenceCheck) PlanMarkdownEvidenceResult {
	var result PlanMarkdownEvidenceResult
	if board == nil {
		result.Reasons = append(result.Reasons, "claims board is missing")
		return result
	}
	artifact, testamentID := latestPlanMarkdownArtifact(board.Projection(), check.PlanID)
	if artifact == nil {
		if strings.TrimSpace(check.PlanID) != "" {
			result.Reasons = append(result.Reasons, fmt.Sprintf("plan_markdown artifact for plan %q not found", strings.TrimSpace(check.PlanID)))
		} else {
			result.Reasons = append(result.Reasons, "plan_markdown artifact not found")
		}
		return result
	}
	result.Artifact = CloneArtifact(artifact)
	result.TestamentID = testamentID

	required := map[string]any{}
	if planID := strings.TrimSpace(check.PlanID); planID != "" {
		required["plan_id"] = planID
	}
	if check.Epoch > 0 {
		required["epoch"] = check.Epoch
	}
	if check.ExpectedTaskCount > 0 {
		required["task_count"] = check.ExpectedTaskCount
	}
	artifactResult := ValidateArtifactEvidence(artifact, ArtifactEvidenceCheck{
		Kind:             ArtifactKindPlanMarkdown,
		RequireReference: true,
		RequiredMetadata: required,
	})
	result.Reasons = append(result.Reasons, artifactResult.Reasons...)

	if !looksLikePlanMarkdown(artifact.Reference) {
		result.Reasons = append(result.Reasons, "artifact reference does not contain a plan markdown heading")
	}
	for _, requiredText := range check.RequiredTaskText {
		requiredText = strings.TrimSpace(requiredText)
		if requiredText == "" {
			continue
		}
		if !strings.Contains(artifact.Reference, requiredText) {
			result.Reasons = append(result.Reasons, fmt.Sprintf("artifact reference does not contain required task text %q", requiredText))
		}
	}

	presentation := NormalizePresentation(artifact.Presentation)
	if check.RequireChat && !PresentationMatches(presentation, string(PresentationAudienceUser), string(PresentationSurfaceChat)) {
		result.Reasons = append(result.Reasons, "presentation does not target user/chat")
	}
	if check.RequireApproval && !PresentationMatches(presentation, string(PresentationAudienceUser), string(PresentationSurfaceApproval)) {
		result.Reasons = append(result.Reasons, "presentation does not target user/approval")
	}
	result.Passed = len(result.Reasons) == 0
	return result
}

func latestPlanMarkdownArtifact(proj *ClaimsBoardProjection, planID string) (*Artifact, string) {
	if proj == nil {
		return nil, ""
	}
	planID = strings.TrimSpace(planID)
	type candidate struct {
		artifact    *Artifact
		testamentID string
		sequence    uint64
	}
	var candidates []candidate
	for i := range proj.Testaments {
		t := &proj.Testaments[i]
		for _, artifact := range t.Artifacts {
			if artifact == nil || strings.TrimSpace(artifact.Kind) != ArtifactKindPlanMarkdown {
				continue
			}
			if planID != "" && strings.TrimSpace(metadataString(artifact.Metadata, "plan_id")) != planID {
				continue
			}
			candidates = append(candidates, candidate{
				artifact:    artifact,
				testamentID: t.ID,
				sequence:    artifact.Sequence,
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].sequence != candidates[j].sequence {
			return candidates[i].sequence > candidates[j].sequence
		}
		return strings.TrimSpace(candidates[i].artifact.ID) > strings.TrimSpace(candidates[j].artifact.ID)
	})
	if len(candidates) == 0 {
		return nil, ""
	}
	return candidates[0].artifact, candidates[0].testamentID
}

func looksLikePlanMarkdown(reference string) bool {
	for _, line := range strings.Split(reference, "\n") {
		line = strings.TrimSpace(line)
		if strings.EqualFold(line, "# Plan") ||
			strings.EqualFold(line, "## Plan") ||
			strings.EqualFold(line, "### Plan") {
			return true
		}
	}
	return false
}

func metadataValueEqual(got, want any) bool {
	switch wantTyped := want.(type) {
	case string:
		return strings.TrimSpace(metadataValueString(got)) == strings.TrimSpace(wantTyped)
	case int:
		gotInt, ok := metadataValueInt64(got)
		return ok && gotInt == int64(wantTyped)
	case int64:
		gotInt, ok := metadataValueInt64(got)
		return ok && gotInt == wantTyped
	case uint64:
		gotInt, ok := metadataValueInt64(got)
		return ok && gotInt >= 0 && uint64(gotInt) == wantTyped
	case float64:
		gotInt, ok := metadataValueInt64(got)
		if ok {
			return float64(gotInt) == wantTyped
		}
		gotFloat, ok := got.(float64)
		return ok && gotFloat == wantTyped
	default:
		return fmt.Sprint(got) == fmt.Sprint(want)
	}
}

func metadataValueString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case fmt.Stringer:
		return typed.String()
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

func metadataValueInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int8:
		return int64(typed), true
	case int16:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		return int64(typed), true
	case uint8:
		return int64(typed), true
	case uint16:
		return int64(typed), true
	case uint32:
		return int64(typed), true
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(typed), true
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return n, true
		}
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// DefaultPlanMarkdownPresentation returns the canonical presentation contract
// for a human-reviewable plan artifact.
func DefaultPlanMarkdownPresentation(planID string) *Presentation {
	replaceKey := ""
	if planID = strings.TrimSpace(planID); planID != "" {
		replaceKey = "plan:" + planID + ":review"
	}
	return &Presentation{
		Audiences:  []PresentationAudience{PresentationAudienceUser},
		Surfaces:   []PresentationSurface{PresentationSurfaceChat, PresentationSurfaceApproval},
		Format:     PresentationFormatMarkdown,
		Title:      "Plan",
		Placement:  PresentationPlacementBeforeResponse,
		ReplaceKey: replaceKey,
	}
}

// LegacyPlanMarkdownFromHandoffArtifact extracts transient or migratable
// plan markdown from a pre-presentation plan_handoff_payload artifact.
func LegacyPlanMarkdownFromHandoffArtifact(artifact *Artifact) (string, map[string]any, bool) {
	if artifact == nil || strings.TrimSpace(artifact.Kind) != ArtifactKindPlanHandoffPayload {
		return "", nil, false
	}
	metadata := cloneAnyMap(artifact.Metadata)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	for _, key := range []string{"plan_markdown", "markdown", "plan_text"} {
		if markdown := strings.TrimSpace(metadataString(artifact.Metadata, key)); markdown != "" {
			return markdown, metadata, true
		}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(artifact.Reference), &payload); err != nil {
		if text := strings.TrimSpace(artifact.Reference); looksLikePlanMarkdown(text) {
			return text, metadata, true
		}
		return "", metadata, false
	}
	for _, key := range []string{"plan_markdown", "markdown", "plan_text"} {
		if markdown := strings.TrimSpace(stringFromMap(payload, key)); markdown != "" {
			copyPlanMetadata(metadata, payload)
			return markdown, metadata, true
		}
	}
	if plan, ok := payload["plan"].(map[string]any); ok {
		for _, key := range []string{"plan_markdown", "markdown", "plan_text"} {
			if markdown := strings.TrimSpace(stringFromMap(plan, key)); markdown != "" {
				copyPlanMetadata(metadata, plan)
				return markdown, metadata, true
			}
		}
	}
	return "", metadata, false
}

func stringFromMap(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	switch typed := values[key].(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func copyPlanMetadata(dst map[string]any, src map[string]any) {
	if dst == nil {
		return
	}
	for _, key := range []string{"plan_id", "epoch", "task_count", "content_hash"} {
		if _, exists := dst[key]; exists {
			continue
		}
		if v, ok := src[key]; ok {
			dst[key] = v
		}
	}
}

// LegacyPlanMarkdownBackfill records one durable artifact written by the
// optional legacy plan presentation migration.
type LegacyPlanMarkdownBackfill struct {
	TestamentID      string `json:"testament_id"`
	SourceArtifactID string `json:"source_artifact_id"`
	PlanArtifactID   string `json:"plan_artifact_id"`
	PlanID           string `json:"plan_id,omitempty"`
}

// BackfillLegacyPlanMarkdownArtifacts writes durable plan_markdown artifacts
// for legacy plan handoff testaments that do not already contain one. Normal
// runtime replay uses transient synthetic rows; this helper is the explicit
// migration path for callers that want durable artifacts.
func BackfillLegacyPlanMarkdownArtifacts(ctx context.Context, board *ClaimsBoard, agentID string) ([]LegacyPlanMarkdownBackfill, error) {
	if board == nil {
		return nil, fmt.Errorf("claims board is nil")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = "claims-migration"
	}
	var testaments []Testament
	var records []LegacyPlanMarkdownBackfill
	proj := board.Projection()
	for i := range proj.Testaments {
		t := proj.Testaments[i]
		if testamentHasArtifactKind(&t, ArtifactKindPlanMarkdown) {
			continue
		}
		for _, artifact := range t.Artifacts {
			markdown, metadata, ok := LegacyPlanMarkdownFromHandoffArtifact(artifact)
			if !ok {
				continue
			}
			planID := strings.TrimSpace(metadataString(metadata, "plan_id"))
			testament := Testament{
				AgentID: firstNonEmpty(agentID, strings.TrimSpace(t.AgentID)),
				Summary: "Backfilled legacy plan presentation artifact.",
				Relations: []Relation{
					{Related: t.ID, RelatedType: RelatedTypeTestament, Relationship: RelationshipDerivedFrom},
				},
				Artifacts: []*Artifact{{
					Kind:         ArtifactKindPlanMarkdown,
					Reference:    markdown,
					Metadata:     metadata,
					Presentation: DefaultPlanMarkdownPresentation(planID),
					Relations: []Relation{
						{Related: artifact.ID, RelatedType: RelatedTypeArtifact, Relationship: RelationshipDerivedFrom},
					},
				}},
			}
			if claimID := ClaimIDFromRelations(t.Relations); claimID != "" {
				testament.Relations = append(testament.Relations, Relation{
					Related:      claimID,
					RelatedType:  RelatedTypeClaim,
					Relationship: RelationshipClaim,
				})
			}
			testaments = append(testaments, testament)
			records = append(records, LegacyPlanMarkdownBackfill{
				TestamentID:      t.ID,
				SourceArtifactID: artifact.ID,
				PlanID:           planID,
			})
			break
		}
	}
	if len(testaments) == 0 {
		return nil, nil
	}
	if err := board.SubmitTestaments(ctx, Action{AgentID: agentID, Type: ActionTypeTestament}, testaments); err != nil {
		return nil, err
	}
	for i := range records {
		if i < len(testaments) && len(testaments[i].Artifacts) > 0 && testaments[i].Artifacts[0] != nil {
			records[i].PlanArtifactID = testaments[i].Artifacts[0].ID
		}
	}
	return records, nil
}

func testamentHasArtifactKind(t *Testament, kind string) bool {
	if t == nil {
		return false
	}
	kind = strings.TrimSpace(kind)
	for _, artifact := range t.Artifacts {
		if artifact != nil && strings.TrimSpace(artifact.Kind) == kind {
			return true
		}
	}
	return false
}
