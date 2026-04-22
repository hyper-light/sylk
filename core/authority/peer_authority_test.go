package authority

import (
	"sort"
	"strings"
	"testing"
)

// TestPeerAuthorityMatrix pins the full per-agent authority matrix
// for consult_peer and challenge_peer. Any drift in the profile
// definitions fails this test — a reviewer changing a role's
// permitted targets must update this fixture too. Keeps the matrix
// documented in code next to the check that enforces it.
//
// See docs/COMMS_MATRIX.md for the narrative view of the same data.
func TestPeerAuthorityMatrix(t *testing.T) {
	type rolePermissions struct {
		consult          []string
		challenge        []string
		crossPipeline    bool
		rationaleConsult string
		rationaleChall   string
	}
	// This is the single source of truth for the test. Synchronize
	// with core/authority/profile.go and docs/COMMS_MATRIX.md.
	matrix := map[string]rolePermissions{
		"academic": {
			rationaleConsult: "reactive — knowledge agent responds, does not initiate",
			rationaleChall:   "knowledge agents do not challenge",
		},
		"archivalist": {
			rationaleConsult: "reactive — knowledge agent responds, does not initiate",
			rationaleChall:   "knowledge agents do not challenge",
		},
		"librarian": {
			rationaleConsult: "reactive — knowledge agent responds, does not initiate",
			rationaleChall:   "knowledge agents do not challenge",
		},
		"architect": {
			consult:          []string{"academic", "archivalist", "librarian", "orchestrator"},
			rationaleChall:   "uses global_review protocol for challenges",
		},
		"inspector": {
			consult:        []string{"academic", "architect", "archivalist", "librarian", "orchestrator", "tester-global"},
			challenge:      []string{"architect", "orchestrator", "tester-global"},
			crossPipeline:  false,
		},
		"inspector-pipeline": {
			consult:       []string{"academic", "archivalist", "designer", "engineer", "librarian", "tester-pipeline"},
			challenge:     []string{"designer", "engineer", "tester-pipeline"},
			crossPipeline: true,
		},
		"engineer": {
			consult:       []string{"academic", "archivalist", "designer", "inspector-pipeline", "librarian", "tester-pipeline"},
			challenge:     []string{"designer", "inspector-pipeline", "tester-pipeline"},
			crossPipeline: true,
		},
		"designer": {
			consult:       []string{"academic", "archivalist", "engineer", "inspector-pipeline", "librarian", "tester-pipeline"},
			challenge:     []string{"engineer", "inspector-pipeline", "tester-pipeline"},
			crossPipeline: true,
		},
		"tester-pipeline": {
			consult:       []string{"academic", "archivalist", "designer", "engineer", "inspector-pipeline", "librarian"},
			challenge:     []string{"designer", "engineer", "inspector-pipeline"},
			crossPipeline: true,
		},
		"tester-global": {
			consult:   []string{"academic", "architect", "archivalist", "inspector", "librarian", "orchestrator"},
			challenge: []string{"architect", "inspector", "orchestrator"},
		},
		"tester": {
			consult:   []string{"academic", "architect", "archivalist", "inspector", "librarian", "orchestrator"},
			challenge: []string{"architect", "inspector", "orchestrator"},
		},
		"orchestrator": {
			consult:        []string{"academic", "architect", "archivalist", "librarian"},
			rationaleChall: "uses DAG/control-plane channels, not challenge_peer",
		},
		"guide": {
			rationaleConsult: "router, not a peer",
			rationaleChall:   "router, not a peer",
		},
		"guardian": {
			rationaleConsult: "policy enforcer, not a peer initiator",
			rationaleChall:   "policy enforcer, not a peer initiator",
		},
		"global-editor": {
			rationaleConsult: "operates in merge scope; no peer consults in current design",
			rationaleChall:   "operates in merge scope; no peer challenges in current design",
		},
	}

	for role, want := range matrix {
		t.Run(role+"/consult", func(t *testing.T) {
			got := PermittedConsultTargets(role)
			wantSorted := append([]string(nil), want.consult...)
			sort.Strings(wantSorted)
			if !stringSlicesEqual(got, wantSorted) {
				t.Errorf("%s consult targets mismatch\n  want: %v\n   got: %v\n  rationale: %s",
					role, wantSorted, got, want.rationaleConsult)
			}
			// Self-target must NEVER be in the list.
			for _, target := range got {
				if target == role {
					t.Errorf("%s consult targets contain self — self-exclusion broken", role)
				}
			}
		})
		t.Run(role+"/challenge", func(t *testing.T) {
			got := PermittedChallengeTargets(role)
			wantSorted := append([]string(nil), want.challenge...)
			sort.Strings(wantSorted)
			if !stringSlicesEqual(got, wantSorted) {
				t.Errorf("%s challenge targets mismatch\n  want: %v\n   got: %v\n  rationale: %s",
					role, wantSorted, got, want.rationaleChall)
			}
			for _, target := range got {
				if target == role {
					t.Errorf("%s challenge targets contain self — self-exclusion broken", role)
				}
			}
		})
	}
}

// TestCanConsult_ReproducesObservedBugs pins the exact scenarios the
// defense is designed to prevent. Each sub-test maps to a specific
// screenshot or reported incident.
func TestCanConsult_ReproducesObservedBugs(t *testing.T) {
	cases := []struct {
		name    string
		caller  string
		target  string
		allowed bool
		why     string
	}{
		{
			name:    "archivalist_challenging_tester-pipeline",
			caller:  "archivalist",
			target:  "tester-pipeline",
			allowed: false,
			why:     "screenshot: archivalist formally challenged tester-pipeline. Knowledge agents are reactive.",
		},
		{
			name:    "global-inspector_self_target",
			caller:  "inspector",
			target:  "inspector",
			allowed: false,
			why:     "screenshot: global inspector consulted itself via target_agent_type=inspector.",
		},
		{
			name:    "global-inspector_reaching_pipeline-tester",
			caller:  "inspector",
			target:  "tester-pipeline",
			allowed: false,
			why:     "global inspector must not reach into per-task pipeline agents.",
		},
		{
			name:    "global-inspector_reaching_pipeline-inspector",
			caller:  "inspector",
			target:  "inspector-pipeline",
			allowed: false,
			why:     "global inspector coordinates at global scope, not inside pipelines.",
		},
		{
			name:    "librarian_initiating_consult",
			caller:  "librarian",
			target:  "academic",
			allowed: false,
			why:     "knowledge agents are reactive even to each other.",
		},
		{
			name:    "engineer_consulting_librarian_ok",
			caller:  "engineer",
			target:  "librarian",
			allowed: true,
			why:     "legitimate knowledge-agent consultation.",
		},
		{
			name:    "engineer_consulting_designer_ok",
			caller:  "engineer",
			target:  "designer",
			allowed: true,
			why:     "legitimate co-tenant consult in pipeline pod.",
		},
		{
			name:    "tester-pipeline_consulting_engineer_ok",
			caller:  "tester-pipeline",
			target:  "engineer",
			allowed: true,
			why:     "legitimate pipeline-peer consult.",
		},
		{
			name:    "inspector-pipeline_consulting_tester-pipeline_ok",
			caller:  "inspector-pipeline",
			target:  "tester-pipeline",
			allowed: true,
			why:     "pipeline inspector requests tester validation.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CanConsult(tc.caller, tc.target)
			if got != tc.allowed {
				t.Errorf("CanConsult(%q, %q) = %v, want %v; %s",
					tc.caller, tc.target, got, tc.allowed, tc.why)
			}
		})
	}
}

// TestCanChallenge_ReproducesObservedBugs mirrors the consult matrix
// for challenges. Challenge permissions are strictly tighter —
// every case here must ALSO have been denied (or explicitly allowed)
// in the challenge matrix.
func TestCanChallenge_ReproducesObservedBugs(t *testing.T) {
	cases := []struct {
		name    string
		caller  string
		target  string
		allowed bool
		why     string
	}{
		{name: "archivalist_cannot_challenge_tester-pipeline", caller: "archivalist", target: "tester-pipeline", allowed: false, why: "screenshot scenario — reactive role."},
		{name: "inspector_cannot_challenge_self", caller: "inspector", target: "inspector", allowed: false, why: "self-target always denied."},
		{name: "inspector_cannot_challenge_tester-pipeline", caller: "inspector", target: "tester-pipeline", allowed: false, why: "global scope cannot challenge per-task agents."},
		{name: "inspector-pipeline_can_challenge_engineer", caller: "inspector-pipeline", target: "engineer", allowed: true, why: "legitimate pipeline-scope challenge."},
		{name: "engineer_can_challenge_designer", caller: "engineer", target: "designer", allowed: true, why: "pipeline peer disagreement."},
		{name: "orchestrator_cannot_challenge_anything", caller: "orchestrator", target: "architect", allowed: false, why: "uses control-plane channels instead."},
		{name: "librarian_cannot_challenge", caller: "librarian", target: "academic", allowed: false, why: "knowledge agents never challenge."},
		{name: "architect_cannot_challenge_via_peer_channel", caller: "architect", target: "tester-global", allowed: false, why: "architect uses global_review, not challenge_peer."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CanChallenge(tc.caller, tc.target)
			if got != tc.allowed {
				t.Errorf("CanChallenge(%q, %q) = %v, want %v; %s",
					tc.caller, tc.target, got, tc.allowed, tc.why)
			}
		})
	}
}

// TestPermittedTargets_SelfExclusionInvariant is a stronger version
// of the same property: regardless of how the static lists are
// defined (someone might accidentally add the agent's own type),
// the accessor filters it out.
func TestPermittedTargets_SelfExclusionInvariant(t *testing.T) {
	for role := range profiles {
		if contains(PermittedConsultTargets(role), role) {
			t.Errorf("PermittedConsultTargets(%q) contains %q — self-exclusion failed", role, role)
		}
		if contains(PermittedChallengeTargets(role), role) {
			t.Errorf("PermittedChallengeTargets(%q) contains %q — self-exclusion failed", role, role)
		}
	}
}

// TestChallengeTargetsAreSubsetOfConsult enforces the design
// invariant that challenge lists are tighter than consult lists —
// you can only challenge peers you can also consult. If this fails
// for a role, the profile has drifted.
func TestChallengeTargetsAreSubsetOfConsult(t *testing.T) {
	for role := range profiles {
		consult := PermittedConsultTargets(role)
		consultSet := make(map[string]struct{}, len(consult))
		for _, c := range consult {
			consultSet[c] = struct{}{}
		}
		for _, chall := range PermittedChallengeTargets(role) {
			if _, ok := consultSet[chall]; !ok {
				t.Errorf("role %q has challenge target %q that is not in its consult targets — challenge must be subset of consult", role, chall)
			}
		}
	}
}

func TestCanConsult_UnknownAgentTypes(t *testing.T) {
	if CanConsult("", "librarian") {
		t.Error("empty caller should be denied")
	}
	if CanConsult("engineer", "") {
		t.Error("empty target should be denied")
	}
	if CanConsult("unknown-role", "librarian") {
		t.Error("unknown caller should be denied (no profile => no permissions)")
	}
}

// helpers.

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
}

func contains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
