package guardian

import (
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestGuardianFastPathDirectSkill_AllowsReviewGate(t *testing.T) {
	fwd := &guide.ForwardedRequest{
		Metadata: map[string]any{"direct_skill": "review_gate"},
	}
	if !guardianFastPathDirectSkill(fwd) {
		t.Fatal("review_gate must take the serializer-bypass fast path")
	}
}

func TestGuardianFastPathDirectSkill_AllowsPlanApprovalGate(t *testing.T) {
	// plan_approval_gate blocks for many minutes on the user's
	// click; if it ran under the request serializer, every other
	// Guardian forwarded request would queue behind it. The skill
	// is per-request-state-only (no shared state mutation), so
	// concurrent execution is safe.
	fwd := &guide.ForwardedRequest{
		Metadata: map[string]any{"direct_skill": "plan_approval_gate"},
	}
	if !guardianFastPathDirectSkill(fwd) {
		t.Fatal("plan_approval_gate must take the serializer-bypass fast path so the dialog wait doesn't queue other Guardian work")
	}
}

func TestGuardianFastPathDirectSkill_RejectsNonAllowlisted(t *testing.T) {
	cases := []string{
		"command_approval_gate",
		"content_scan",
		"git_safety",
		"",
	}
	for _, skill := range cases {
		t.Run(skill, func(t *testing.T) {
			fwd := &guide.ForwardedRequest{
				Metadata: map[string]any{"direct_skill": skill},
			}
			if guardianFastPathDirectSkill(fwd) {
				t.Fatalf("non-allowlisted skill %q must NOT take the fast path", skill)
			}
		})
	}
}

func TestGuardianFastPathDirectSkill_RejectsConversationLoop(t *testing.T) {
	// No direct_skill metadata → conversation loop request, which
	// MUST go through the serializer (it mutates Guardian state via
	// the LLM tool loop, steering ledger, etc.).
	fwd := &guide.ForwardedRequest{
		Metadata: map[string]any{},
	}
	if guardianFastPathDirectSkill(fwd) {
		t.Fatal("conversation-loop requests must NOT take the fast path")
	}
}

func TestGuardianFastPathDirectSkill_NilSafe(t *testing.T) {
	if guardianFastPathDirectSkill(nil) {
		t.Fatal("nil ForwardedRequest must NOT take the fast path")
	}
}
