package shared

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/activity"
)

// TestAutoPublishHelpers_EmitExpectedKinds is the Phase 6 emission
// audit (docs/PIPELINE_SKILL_REFACTOR.md). It verifies the new
// Phase 0 helpers emit the right ActionKind. Handler-level emission
// coverage (the 13 Phase 4 handlers) lives in each agent's test
// package; this test pins the contract of the shared helpers.
func TestAutoPublishHelpers_EmitExpectedKinds(t *testing.T) {
	collector := activity.NewTestCollector()
	prev := activity.SetDefaultSink(collector)
	t.Cleanup(func() { activity.SetDefaultSink(prev) })

	ctx := context.Background()

	cases := []struct {
		name string
		emit func()
		want activity.ActionKind
	}{
		{
			name: "AutoPublishClaimAcquired",
			emit: func() {
				AutoPublishClaimAcquired(ctx, AutoPublishClaimInput{
					SessionID:      "s",
					ActorAgentID:   "engineer-1",
					ActorAgentType: "engineer",
					ScopeKind:      "file",
					ScopeKey:       "src/foo.go",
					Mode:           "exclusive",
					LeaseSeconds:   600,
				})
			},
			want: activity.ActionClaimAcquired,
		},
		{
			name: "AutoPublishClaimReleased",
			emit: func() {
				AutoPublishClaimReleased(ctx, AutoPublishClaimInput{
					SessionID:      "s",
					ActorAgentID:   "engineer-1",
					ActorAgentType: "engineer",
					ClaimID:        "claim-1",
					ScopeKey:       "src/foo.go",
				}, activity.ActivityID("claim-1"))
			},
			want: activity.ActionClaimReleased,
		},
		{
			name: "AutoPublishArtifactPublished",
			emit: func() {
				AutoPublishArtifactPublished(ctx, AutoPublishArtifactInput{
					SessionID:       "s",
					AuthorAgentID:   "tester-1",
					AuthorAgentType: "tester-pipeline",
					Kind:            "risk_map",
					Summary:         "3 risks",
				})
			},
			want: activity.ActionArtifactPublished,
		},
		{
			name: "AutoPublishReviewRequested",
			emit: func() {
				AutoPublishReviewRequested(ctx, AutoPublishReviewInput{
					SessionID:         "s",
					ActorAgentID:      "designer-1",
					ActorAgentType:    "designer",
					ArtifactID:        "art-1",
					ReviewerAgentType: "inspector-pipeline",
					Summary:           "please review",
				})
			},
			want: activity.ActionReviewRequested,
		},
		{
			name: "AutoPublishReviewCompleted",
			emit: func() {
				AutoPublishReviewCompleted(ctx, AutoPublishReviewInput{
					SessionID:      "s",
					ActorAgentID:   "inspector-1",
					ActorAgentType: "inspector-pipeline",
					ReviewID:       "rev-1",
					Status:         "accepted",
				}, activity.ActivityID("rev-req-1"))
			},
			want: activity.ActionReviewCompleted,
		},
		{
			name: "AutoPublishValidationStarted",
			emit: func() {
				AutoPublishValidationStarted(ctx, AutoPublishValidationInput{
					SessionID:      "s",
					ActorAgentID:   "inspector-1",
					ActorAgentType: "inspector-pipeline",
					EpochID:        "epoch-1",
					Scope:          "task-1",
				})
			},
			want: activity.ActionValidationStarted,
		},
		{
			name: "AutoPublishValidationAccepted",
			emit: func() {
				AutoPublishValidationAccepted(ctx, AutoPublishValidationInput{
					SessionID:      "s",
					ActorAgentID:   "inspector-1",
					ActorAgentType: "inspector-pipeline",
					EpochID:        "epoch-1",
				}, activity.ActivityID("started-1"))
			},
			want: activity.ActionValidationAccepted,
		},
		{
			name: "AutoPublishValidationRejected",
			emit: func() {
				AutoPublishValidationRejected(ctx, AutoPublishValidationInput{
					SessionID:      "s",
					ActorAgentID:   "inspector-1",
					ActorAgentType: "inspector-pipeline",
					EpochID:        "epoch-1",
					FailureCount:   2,
				}, activity.ActivityID("started-1"))
			},
			want: activity.ActionValidationRejected,
		},
		{
			name: "AutoPublishAdvisory",
			emit: func() {
				AutoPublishAdvisory(ctx, AutoPublishAdvisoryInput{
					SessionID:       "s",
					AuthorAgentID:   "engineer-1",
					AuthorAgentType: "engineer",
					Domain:          "self_audit",
					Value:           "pass",
					Summary:         "looks good",
				})
			},
			want: activity.ActionAdvisoryEmitted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			collector.Reset()
			tc.emit()
			rows := collector.Snapshot()
			if len(rows) != 1 {
				t.Fatalf("%s: expected 1 emission, got %d", tc.name, len(rows))
			}
			if rows[0].Action != tc.want {
				t.Fatalf("%s: expected ActionKind %q, got %q", tc.name, tc.want, rows[0].Action)
			}
		})
	}
}

// TestAutoPublishHelpers_NoOpOnMissingRequiredFields pins the silent
// no-op contract: missing Domain / Kind is a caller bug but the helper
// must not panic or mis-emit. Protects against accidental catalog
// pollution when a handler forgets to fill in a field.
func TestAutoPublishHelpers_NoOpOnMissingRequiredFields(t *testing.T) {
	collector := activity.NewTestCollector()
	prev := activity.SetDefaultSink(collector)
	t.Cleanup(func() { activity.SetDefaultSink(prev) })

	ctx := context.Background()

	AutoPublishArtifactPublished(ctx, AutoPublishArtifactInput{SessionID: "s"}) // no Kind
	AutoPublishAdvisory(ctx, AutoPublishAdvisoryInput{SessionID: "s"})          // no Domain
	AutoPublishDecision(ctx, AutoPublishInput{SessionID: "s"})                  // no Domain/Value

	if got := len(collector.Snapshot()); got != 0 {
		t.Fatalf("expected zero emissions when required fields missing, got %d", got)
	}
}
