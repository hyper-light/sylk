package architect

import (
	"testing"
	"time"
)

func TestConsultationTargetsSatisfied_AllowsAgenticEmptyConsultationSet(t *testing.T) {
	plan := &DesignPlan{
		Consultations: map[string]*ConsultationEvidence{},
	}
	if !consultationTargetsSatisfied(plan, DefaultConsultationMaxAge) {
		t.Fatal("expected empty consultation set to be acceptable under agentic consultation flow")
	}
}

func TestValidateDeclarationConsultations_OnlyValidatesAttachedEvidence(t *testing.T) {
	decl := &PreDelegationDeclaration{
		ConsultationChecks: map[string]*ConsultationEvidence{},
	}
	if err := validateDeclarationConsultations(decl, DefaultConsultationMaxAge); err != nil {
		t.Fatalf("validateDeclarationConsultations returned error for empty consultation set: %v", err)
	}
}

func TestValidateDeclarationConsultations_RejectsFailedOrStaleAttachedEvidence(t *testing.T) {
	stale := &PreDelegationDeclaration{
		ConsultationChecks: map[string]*ConsultationEvidence{
			"academic": {
				Target:     "academic",
				Success:    true,
				ReceivedAt: time.Now().Add(-2 * DefaultConsultationMaxAge),
			},
		},
	}
	if err := validateDeclarationConsultations(stale, DefaultConsultationMaxAge); err == nil {
		t.Fatal("expected stale attached evidence to fail validation")
	}

	failed := &PreDelegationDeclaration{
		ConsultationChecks: map[string]*ConsultationEvidence{
			"archivalist": {
				Target:     "archivalist",
				Success:    false,
				Error:      "consultation failed",
				ReceivedAt: time.Now(),
			},
		},
	}
	if err := validateDeclarationConsultations(failed, DefaultConsultationMaxAge); err == nil {
		t.Fatal("expected failed attached evidence to fail validation")
	}
}

func TestDefaultDiscussionConsultationTargets_IncludesKnowledgeTriad(t *testing.T) {
	got := defaultDiscussionConsultationTargets()
	want := []string{"librarian", "archivalist", "academic"}
	if len(got) != len(want) {
		t.Fatalf("len(defaultDiscussionConsultationTargets()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("defaultDiscussionConsultationTargets()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
