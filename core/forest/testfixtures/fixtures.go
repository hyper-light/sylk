package testfixtures

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/forest"
)

var fixtureEpoch = time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC)

type Clock struct {
	mu  sync.Mutex
	now time.Time
}

func NewClock() *Clock {
	return &Clock{now: fixtureEpoch}
}

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Advance(step time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(step)
	return c.now
}

type IDs struct {
	mu      sync.Mutex
	prefix  string
	counter int
}

func NewIDs(prefix string) *IDs {
	return &IDs{prefix: prefix}
}

func (ids *IDs) Next(parts ...string) string {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.counter++
	return ids.prefix + ":" + stableID(append([]string{fmt.Sprint(ids.counter)}, parts...)...)
}

func ClaimSatisfiedDelta(clock *Clock, claimID string) claims.CanonicalDelta {
	now := clock.Now()
	return claims.NewCanonicalDelta(
		claims.DeltaActionClaimSatisfied,
		"fixture-session",
		"fixture-board",
		uint64(now.UnixNano()),
		now,
		claims.DegradedAgentRef("guardian", "fixture accepted claim"),
		[]claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: claimID}},
		nil,
		map[string]any{"claim": map[string]any{"id": claimID}},
	)
}

func GovernedPolicyPromotion(candidateID string) forest.ForestGovernedChange {
	return forest.ForestGovernedChange{
		Kind:                   forest.ForestProposalKindPolicyPromotion,
		SubjectType:            "forest_policy",
		SubjectID:              candidateID,
		Summary:                "Promote validated forest policy candidate " + candidateID,
		EvidenceRefs:           []string{"validation:fixture-policy"},
		RollbackPath:           forest.PolicyRollbackUsePreviousActive,
		GuardianReviewRequired: true,
	}
}

func SkillCandidateInput(name string) forest.SkillCandidateInput {
	return forest.SkillCandidateInput{
		Name:                 name,
		RoleScope:            "guardian",
		Trigger:              "validated remediation evidence appears",
		SourceMemeIDs:        []string{"meme:fixture-remediation"},
		SourceValidationRefs: []string{"validation:fixture-skill"},
		PromotionRationale:   "Produce an inert generated skill proposal for repeated validated remediation evidence.",
	}
}

func AuthorityBackedSkillInput(name, claimID, candidateID string) forest.SkillCandidateInput {
	input := SkillCandidateInput(name)
	input.RequestedPermissions = []string{"network"}
	input.ExplicitPermissionClaimID = claimID
	input.PermissionApprovalActor = "guardian"
	input.PermissionApprovalScope = []string{forest.SkillCandidatePermissionScope(candidateID)}
	input.PermissionApprovalExpires = fixtureEpoch.AddDate(0, 0, 1)
	return input
}

func Node(id string, kind forest.ForestNodeKind) forest.ForestNode {
	return forest.ForestNode{
		ID:            id,
		Kind:          kind,
		SourceKind:    "fixture",
		SourceKey:     "fixture:" + id,
		SourceSeq:     1,
		Subject:       forest.ForestSubjectRef{Type: string(kind), ID: id},
		SessionID:     "fixture-session",
		Title:         "Fixture " + id,
		Summary:       "Fixture node " + id,
		EvidenceGrade: forest.EvidenceGradeValidated,
		Confidence:    1,
		Salience:      1,
		PolicyVersion: "fixture-policy",
		FirstSeenAt:   fixtureEpoch,
		LastSeenAt:    fixtureEpoch,
		Metadata:      map[string]any{"fixture": true},
	}
}

func Artifact(claimID string) forest.ArtifactEvidenceRecord {
	return forest.ArtifactEvidenceRecord{
		ArtifactID:       "artifact:" + stableID(claimID),
		ClaimID:          claimID,
		ArtifactName:     "Fixture artifact",
		ArtifactKind:     "fixture",
		DataType:         "text",
		ContentRef:       "fixture://" + claimID,
		Status:           "accepted",
		ValidationStatus: "validated",
		LastSequence:     1,
		FirstSeenAt:      fixtureEpoch,
		LastSeenAt:       fixtureEpoch,
	}
}

func Validation(claimID, artifactID string) forest.ValidationEvidenceRecord {
	return forest.ValidationEvidenceRecord{
		ValidationID:         "validation:" + stableID(claimID, artifactID),
		ClaimID:              claimID,
		TargetArtifactID:     artifactID,
		EvaluatorParticipant: "guardian",
		ValidationType:       "fixture",
		Status:               "passed",
		Required:             true,
		LastSequence:         1,
		FirstSeenAt:          fixtureEpoch,
		LastSeenAt:           fixtureEpoch,
	}
}

func PolicyOutcomes(candidateID string, count int, score float64) []forest.PolicyOutcome {
	outcomes := make([]forest.PolicyOutcome, 0, count)
	for idx := range count {
		outcomes = append(outcomes, forest.PolicyOutcome{
			CandidateID:        candidateID,
			PolicyID:           "fixture-policy",
			Arm:                forest.PolicyOutcomeArmChallenger,
			Correctness:        score,
			Safety:             score,
			Cost:               score,
			Ecology:            score,
			ValidationPassRate: score,
			ArtifactQuality:    score,
			SampleCount:        1,
			EvidenceRefs:       []string{fmt.Sprintf("validation:fixture-policy:%d", idx)},
			ObservedAt:         fixtureEpoch.Add(time.Duration(idx) * time.Second),
		})
	}
	return outcomes
}

func CloneNode(node forest.ForestNode) forest.ForestNode {
	node.Metadata = cloneMap(node.Metadata)
	return node
}

func stableID(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func cloneMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
