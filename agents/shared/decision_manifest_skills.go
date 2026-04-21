package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/manifest"
	"github.com/adalundhe/sylk/core/skills"
)

// DecisionManifestSkillConfig wires the LLM-facing declare_decision
// skill. Phase 3 refactor: the handler now emits an
// ActionDecisionDeclared fabric activity via AutoPublishDecision
// directly — the orchestrator's manifest fabric subscriber ingests it
// and persists the canonical SQL row. The DecisionManifestClient
// field remains for callers that still need the bus-RPC path (e.g.
// tester decision-manifest gate helpers), but the skill no longer
// touches it.
type DecisionManifestSkillConfig struct {
	Client DecisionManifestClient

	// Phase 3 fabric-native wiring — required so AutoPublishDecision
	// carries the right actor identity onto the emitted activity.
	// All four getters are optional (fall back to empty string when
	// nil), but pipeline agents should wire all four.
	SessionID        func() string
	AuthorAgentID    func() string
	AuthorAgentType  func() string
	AuthorPipelineID func() string
}

// DecisionManifestSkills returns the cross-pipeline manifest skills:
// query_decisions and declare_decision. Both are LLM-facing tools that
// route through the orchestrator-owned manifest store.
//
// Agents call query_decisions BEFORE making a typed decision (e.g.
// choosing a test framework) to see whether peer pipelines have already
// committed in the same domain/scope. They call declare_decision when
// they've made a choice — the manifest detects scope-overlapping
// conflicts against existing decisions and returns adopt/align/challenge
// guidance.
func DecisionManifestSkills(cfg DecisionManifestSkillConfig) []*skills.Skill {
	// Phase 1 refactor (docs/PIPELINE_SKILL_REFACTOR.md):
	// query_decisions is subsumed by query_peer_activity with
	// kinds=["decision_declared"]. declare_decision stays for now;
	// Phase 3 rewrites its body to route through the Activity Fabric
	// via AutoPublishDecision.
	return []*skills.Skill{
		declareDecisionSkill(cfg),
	}
}

// queryDecisionsSkill is retained for reference during the refactor
// but no longer registered. Remove once Phase 3 verification completes.
//
//nolint:unused // retained during refactor for code archaeology
func queryDecisionsSkill(cfg DecisionManifestSkillConfig) *skills.Skill {
	return skills.NewSkill("query_decisions").
		Description("Look up cross-pipeline decisions a peer (or this pipeline earlier) has declared in a typed domain. Use BEFORE picking a test framework, build backend, lint config, or any other choice that parallel pipelines must coordinate on. Returns the resolved winner plus all matching decisions.").
		Domain("manifest").
		Keywords("decision", "manifest", "framework", "tooling", "coordination", "cross-pipeline").
		Priority(95).
		Usage("Call this as the first step in any task where the choice you're about to make would conflict if a peer made a different choice. Common examples: which test framework to use, which build backend, which linter / formatter, which migration tool, which fixture strategy. Pass the domain name (e.g. \"test_framework\") and the scope coordinate that locates your context. The system returns the winning decision per the domain's resolution policy plus all matches sorted by specificity.").
		Requirement("Always query before declaring or before authoring code that depends on a typed decision. The cost is one indexed read; the benefit is avoiding incompatible parallel commitments.").
		Satisfies("Returns the current cross-pipeline decision state for a domain at the queried scope.").
		StringParam("domain", knownDomainNamesEnumDescription("Decision domain to query."), true).
		ObjectParam("scope", "Scope coordinate locating your context. Built-in dimensions: \"path\" (filesystem, prefix-match) and \"language\" (exact match). Domain-recommended dimensions appear in the system prompt. Empty scope = ambient query (matches every decision in the domain).", map[string]*skills.Property{
			"path":     {Type: "string", Description: "Filesystem path identifying the work location (file or directory). Prefix-matched against decision paths."},
			"language": {Type: "string", Description: "Source language: python | go | rust | lua | cpp | ts | sql | bash | …"},
			"surface":  {Type: "string", Description: "Test surface where applicable: unit | integration | e2e | property"},
		}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Domain string         `json:"domain"`
				Scope  manifest.Scope `json:"scope"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			result, err := cfg.Client.Query(ctx, manifest.QueryDecisionsInput{
				Domain: strings.TrimSpace(params.Domain),
				Scope:  normalizeManifestScope(params.Scope),
			})
			if err != nil {
				return nil, err
			}
			return result, nil
		}).
		Build()
}

func declareDecisionSkill(cfg DecisionManifestSkillConfig) *skills.Skill {
	return skills.NewSkill("declare_decision").
		Description("Record a typed decision (e.g. \"use pytest for Python tests in services/api\") in the cross-pipeline manifest so peers can see and coordinate. The manifest detects scope-overlapping conflicts and returns adopt/align/challenge guidance when a peer has declared an incompatible value.").
		Domain("manifest").
		Keywords("decision", "manifest", "framework", "tooling", "declare", "commit").
		Priority(95).
		Usage("Call this AFTER querying decisions and AFTER you've decided on a value. The declaration is your commitment that downstream peers will see. Confidence levels: \"tentative\" = planning, no code written yet; \"committed\" = work has started; \"consensus\" = peer-ratified or architect-charter (do not declare consensus yourself unless ratifying a peer challenge). The system promotes existing decisions toward consensus when independent peers corroborate the same value.").
		Requirement("Always query_decisions for the domain at your scope before declaring. If the result includes a non-nil winner with a value compatible with your intent, ADOPT it (use that value, do not declare again). Only declare when no winner exists or you genuinely intend a different value.").
		Satisfies("Persists your typed decision and surfaces any cross-pipeline conflict for adopt/align/challenge resolution.").
		StringParam("domain", knownDomainNamesEnumDescription("Decision domain (e.g. \"test_framework\")."), true).
		ObjectParam("scope", "Scope coordinate locating your decision. Built-in dimensions: \"path\" (filesystem, prefix-match) and \"language\" (exact match). See domain-recommended dimensions in the system prompt. Empty scope = ambient (applies to all queries in the domain).", map[string]*skills.Property{
			"path":     {Type: "string", Description: "Filesystem path the decision applies to (file or directory). Prefix-matched."},
			"language": {Type: "string", Description: "Source language the decision applies to."},
			"surface":  {Type: "string", Description: "Test surface where applicable."},
		}, false).
		StringParam("value", "The decided value (e.g. \"pytest\", \"setuptools\", \"ruff\"). Domain-specific.", true).
		ArrayParam("evidence", "Files, prior decisions, or rationale supporting this choice.", "string", false).
		EnumParam("confidence", "Confidence tier. \"tentative\" = planning; \"committed\" = work started; \"consensus\" = peer-ratified.", []string{"tentative", "committed", "consensus"}, false).
		StringParam("supersedes", "ID of a prior decision this replaces (after challenge resolution).", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Domain     string               `json:"domain"`
				Scope      manifest.Scope       `json:"scope"`
				Value      string               `json:"value"`
				Evidence   []string             `json:"evidence"`
				Confidence manifest.Confidence  `json:"confidence"`
				Supersedes string               `json:"supersedes"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			scope := normalizeManifestScope(params.Scope)
			// Phase 3 refactor: emit fabric-native. The orchestrator's
			// manifest fabric subscriber tails ActionDecisionDeclared
			// and writes the canonical SQL row. No orchestrator bus
			// RPC on the skill path.
			coords := map[string]string{}
			for k, v := range scope {
				coords[string(k)] = v
			}
			if s := strings.TrimSpace(params.Supersedes); s != "" {
				coords["supersedes"] = s
			}
			AutoPublishDecision(ctx, AutoPublishInput{
				SessionID:        safeCallString(cfg.SessionID),
				AuthorAgentID:    safeCallString(cfg.AuthorAgentID),
				AuthorAgentType:  safeCallString(cfg.AuthorAgentType),
				AuthorPipelineID: safeCallString(cfg.AuthorPipelineID),
				TriggerSkill:     "declare_decision",
				Domain:           strings.TrimSpace(params.Domain),
				Value:            strings.TrimSpace(params.Value),
				Scope:            scope[manifest.DimensionPath],
				Confidence:       string(params.Confidence),
				Coordinates:      coords,
				Evidence:         params.Evidence,
			})
			return map[string]any{
				"accepted": true,
				"domain":   strings.TrimSpace(params.Domain),
				"value":    strings.TrimSpace(params.Value),
				"scope":    scope,
			}, nil
		}).
		Build()
}

// normalizeManifestScope strips empty values and trims whitespace so
// an LLM passing {"path": "", "language": "python"} doesn't accidentally
// over-narrow the scope (an empty path string would never path-match).
func normalizeManifestScope(s manifest.Scope) manifest.Scope {
	if len(s) == 0 {
		return manifest.Scope{}
	}
	out := make(manifest.Scope, len(s))
	for k, v := range s {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// knownDomainNamesEnumDescription appends the currently registered domain
// names to a base description so the LLM tool definition lists them
// explicitly. Domains added after skill construction won't appear in the
// description but are still accepted at the handler layer (open vocabulary).
func knownDomainNamesEnumDescription(base string) string {
	domains := manifest.RegisteredDomains()
	if len(domains) == 0 {
		return base + " (no domains currently registered; declarations may use any domain name)"
	}
	names := make([]string, 0, len(domains))
	for _, d := range domains {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	return fmt.Sprintf("%s Currently registered domains: %s.", base, strings.Join(names, ", "))
}
