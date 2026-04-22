package shared

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/skills"
)

// TestCrossPipelineSkills_OmittedForReactiveRoles verifies knowledge
// agents (librarian, archivalist, academic) and the guardian/guide/
// global-editor do NOT see consult_peer or challenge_peer in their
// tool catalog at all. The skill isn't registered when the role's
// authority profile has empty permitted targets — the LLM cannot
// call what it cannot see.
func TestCrossPipelineSkills_OmittedForReactiveRoles(t *testing.T) {
	reactiveRoles := []string{"librarian", "archivalist", "academic", "guide", "guardian", "global-editor"}
	for _, role := range reactiveRoles {
		t.Run(role, func(t *testing.T) {
			got := CrossPipelineSkills(CrossPipelineSkillConfig{
				AgentID:   func() string { return role + "-1" },
				AgentType: func() string { return role },
			})
			for _, s := range got {
				if s.Name == "consult_peer" {
					t.Errorf("role %q must not receive consult_peer skill (reactive role)", role)
				}
				if s.Name == "challenge_peer" {
					t.Errorf("role %q must not receive challenge_peer skill (reactive role)", role)
				}
			}
		})
	}
}

// TestCrossPipelineSkills_RegisteredForActiveRoles verifies that
// agents with peer-authority (engineer, inspector, tester-*, etc.)
// get the skills registered.
func TestCrossPipelineSkills_RegisteredForActiveRoles(t *testing.T) {
	cases := map[string]struct {
		expectConsult   bool
		expectChallenge bool
	}{
		"engineer":           {expectConsult: true, expectChallenge: true},
		"designer":           {expectConsult: true, expectChallenge: true},
		"inspector-pipeline": {expectConsult: true, expectChallenge: true},
		"tester-pipeline":    {expectConsult: true, expectChallenge: true},
		"inspector":          {expectConsult: true, expectChallenge: true},
		"tester-global":      {expectConsult: true, expectChallenge: true},
		"architect":          {expectConsult: true, expectChallenge: false},
		"orchestrator":       {expectConsult: true, expectChallenge: false},
	}
	for role, want := range cases {
		t.Run(role, func(t *testing.T) {
			got := CrossPipelineSkills(CrossPipelineSkillConfig{
				AgentID:   func() string { return role + "-1" },
				AgentType: func() string { return role },
			})
			hasConsult, hasChallenge := false, false
			for _, s := range got {
				switch s.Name {
				case "consult_peer":
					hasConsult = true
				case "challenge_peer":
					hasChallenge = true
				}
			}
			if hasConsult != want.expectConsult {
				t.Errorf("role %q consult_peer registered=%v, want %v", role, hasConsult, want.expectConsult)
			}
			if hasChallenge != want.expectChallenge {
				t.Errorf("role %q challenge_peer registered=%v, want %v", role, hasChallenge, want.expectChallenge)
			}
		})
	}
}

// TestConsultPeer_SchemaEnumReflectsProfile verifies the
// target_agent_type param's enum matches the caller's permitted
// targets. Schema + runtime both enforce the same invariant.
func TestConsultPeer_SchemaEnumReflectsProfile(t *testing.T) {
	got := CrossPipelineSkills(CrossPipelineSkillConfig{
		AgentID:   func() string { return "eng-1" },
		AgentType: func() string { return "engineer" },
	})
	consult := findByName(got, "consult_peer")
	if consult == nil {
		t.Fatal("consult_peer not registered for engineer")
	}
	enum := enumFor(consult, "target_agent_type")
	for _, want := range []string{"librarian", "archivalist", "academic", "designer", "tester-pipeline", "inspector-pipeline"} {
		if !containsStr(enum, want) {
			t.Errorf("engineer's target_agent_type enum missing %q; enum=%v", want, enum)
		}
	}
	for _, bad := range []string{"engineer", "orchestrator", "inspector", "tester-global"} {
		if containsStr(enum, bad) {
			t.Errorf("engineer's target_agent_type enum unexpectedly contains %q; enum=%v", bad, enum)
		}
	}
}

// TestConsultPeerHandler_RejectsUnauthorizedTarget verifies the
// runtime guard: even if the schema enum is bypassed, the handler
// checks authority.CanConsult and returns a structured error.
func TestConsultPeerHandler_RejectsUnauthorizedTarget(t *testing.T) {
	got := CrossPipelineSkills(CrossPipelineSkillConfig{
		AgentID:   func() string { return "isp-1" },
		AgentType: func() string { return "inspector" },
	})
	consult := findByName(got, "consult_peer")
	if consult == nil {
		t.Fatal("consult_peer not registered")
	}
	_, err := consult.Handler(context.Background(), json.RawMessage(`{
		"target_agent_type": "tester-pipeline",
		"query": "probe"
	}`))
	if err == nil {
		t.Fatal("expected authority error for inspector→tester-pipeline consult")
	}
	if !strings.Contains(err.Error(), "not permitted to consult") {
		t.Errorf("error should name the authority violation; got %q", err)
	}
	if !strings.Contains(err.Error(), "Permitted targets for") {
		t.Errorf("error should enumerate permitted targets; got %q", err)
	}
}

// TestChallengePeerHandler_RejectsUnauthorizedTarget verifies the
// challenge_peer runtime guard.
func TestChallengePeerHandler_RejectsUnauthorizedTarget(t *testing.T) {
	got := CrossPipelineSkills(CrossPipelineSkillConfig{
		AgentID:   func() string { return "isp-1" },
		AgentType: func() string { return "inspector" },
	})
	challenge := findByName(got, "challenge_peer")
	if challenge == nil {
		t.Fatal("challenge_peer not registered for inspector")
	}
	_, err := challenge.Handler(context.Background(), json.RawMessage(`{
		"target_activity_id": "act-fake-1",
		"evidence": "some evidence",
		"target_agent_type": "tester-pipeline"
	}`))
	if err == nil {
		t.Fatal("expected authority error for inspector→tester-pipeline challenge")
	}
	if !strings.Contains(err.Error(), "not permitted to challenge") {
		t.Errorf("error should name the authority violation; got %q", err)
	}
}

// TestConsultPeerHandler_CrossPipelineGate verifies the
// cross-pipeline gate.
func TestConsultPeerHandler_CrossPipelineGate(t *testing.T) {
	got := CrossPipelineSkills(CrossPipelineSkillConfig{
		AgentID:    func() string { return "isp-1" },
		AgentType:  func() string { return "inspector" },
		PipelineID: func() string { return "pipeline-A" },
	})
	consult := findByName(got, "consult_peer")
	if consult == nil {
		t.Fatal("consult_peer not registered")
	}
	_, err := consult.Handler(context.Background(), json.RawMessage(`{
		"target_agent_type": "librarian",
		"target_pipeline_id": "pipeline-B",
		"query": "cross-pipeline probe"
	}`))
	if err == nil {
		t.Fatal("expected cross-pipeline gate error")
	}
	if !strings.Contains(err.Error(), "not permitted to cross-pipeline consult") {
		t.Errorf("error should name the cross-pipeline violation; got %q", err)
	}
}

// helpers

func findByName(list []*skills.Skill, name string) *skills.Skill {
	for _, s := range list {
		if s != nil && s.Name == name {
			return s
		}
	}
	return nil
}

func enumFor(s *skills.Skill, param string) []string {
	if s == nil || s.InputSchema == nil {
		return nil
	}
	prop, ok := s.InputSchema.Properties[param]
	if !ok || prop == nil {
		return nil
	}
	return append([]string(nil), prop.Enum...)
}

func containsStr(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
