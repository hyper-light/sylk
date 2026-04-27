package forest

import (
	"encoding/binary"
	"math"
	"sort"
	"strings"

	"github.com/viterin/vek/vek32"
)

const (
	featureQueryMatch = iota
	featureEvidence
	featureCanopy
	featureConfidence
	featureRecency
	featureWarmth
	featureUtility
	featureSalience
	featureConflictSafety
	featureScopeSafety
	featureBaseScore
	featureSupportDensity
	featureCounterDensity
	featureSuccessBalance
	featureFailurePressure
	featureAccessPressure
	featureRelayMass
	featureSessionAffinity
	featureAgentFamilyAffinity
	featureBranchDepth
	featureSubstratePotential
	featureFrontierScore
	featureInhibitionSafety
	featureScopeWorking
	featureScopeEpisodic
	featureScopeSemantic
	featureScopeContradiction
	featureScopeDormant
	featureFamilyIntent
	featureFamilyConstraint
	featureFamilyEvidence
	// Removed: featureFamilyDecision (Issue #11 Phase 3 — Decision
	// merged into Intent). Index slot retained as a reserved
	// placeholder so persisted base-score model weights don't
	// have to be retrained on a different feature length.
	featureReservedDecision
	featureFamilyOutcome
	// Removed: featureFamilyPreference (merged into Constraint via
	// severity field), featureFamilyCapability (merged into Intent),
	// featureFamilyOpportunity (merged into Intent),
	// featureFamilyConflict (replaced by AntiPattern + relations).
	// Reserved slots retained so the feature vector length is
	// stable across migrations.
	featureReservedPreference
	featureReservedCapability
	featureReservedOpportunity
	featureFamilyAntiPattern
	featureSourceGuardian
	featureSourceScribe
	featureSourceEngineer
	featureSourceDesigner
	featureSourceAcademic
	featureSourceLibrarian
	featureSourceArchivalist
	featureSourceOther
	// MEM-04: relay co-fire count as a lifetime-evidence signal. Distinct
	// from featureRelayMass (which uses decay-sensitive weight, capped at
	// 2.0) — cofire_count is unbounded monotonic history, so a branch
	// that has co-fired 50 times over months ranks higher than one that
	// spiked in the last hour, even if current weights match. Fed through
	// log1p normalization so a single blockbuster co-fire doesn't
	// dominate the vector; see loadRelayCofire for the math.
	featureRelayCofire
	featureCount
)

var forestFeatureNames = [...]string{
	"query_match",
	"evidence",
	"canopy",
	"confidence",
	"recency",
	"warmth",
	"utility",
	"salience",
	"conflict_safety",
	"scope_safety",
	"base_score",
	"support_density",
	"counter_density",
	"success_balance",
	"failure_pressure",
	"access_pressure",
	"relay_mass",
	"session_affinity",
	"agent_family_affinity",
	"branch_depth",
	"substrate_potential",
	"frontier_score",
	"inhibition_safety",
	"scope_working",
	"scope_episodic",
	"scope_semantic",
	"scope_contradiction",
	"scope_dormant",
	"family_intent",
	"family_constraint",
	"family_evidence",
	"reserved_decision",
	"family_outcome",
	"reserved_preference",
	"reserved_capability",
	"reserved_opportunity",
	"family_antipattern",
	"source_guardian",
	"source_scribe",
	"source_engineer",
	"source_designer",
	"source_academic",
	"source_librarian",
	"source_archivalist",
	"source_other",
	"relay_cofire",
}

func buildFeatureVector(
	query Query,
	branch *Branch,
	input scoreInput,
	support []PacketEvidence,
	counter []PacketEvidence,
	baseScore float64,
	relayMass float64,
	relayCofire float64,
	depth int,
	substrate substrateSignal,
) []float32 {
	// Populate the vector with raw (possibly out-of-range) values, then
	// apply a single SIMD-accelerated clamp pass at the end. vek32's
	// MaximumNumber_Inplace and MinimumNumber_Inplace use AVX2 on amd64
	// and NEON on arm64 (falling back to scalar elsewhere), so a single
	// end-of-function clamp beats scattering 23 scalar clamp01 calls
	// across the body — both in cycles and cache-line writes.
	vector := make([]float32, featureCount)
	vector[featureQueryMatch] = float32(input.QueryMatch)
	vector[featureEvidence] = float32(input.Evidence)
	vector[featureCanopy] = float32(input.Canopy)
	vector[featureConfidence] = float32(input.Confidence)
	vector[featureRecency] = float32(input.Recency)
	vector[featureWarmth] = float32(input.Warmth)
	vector[featureUtility] = float32(input.Utility)
	vector[featureSalience] = float32(input.Salience)
	vector[featureConflictSafety] = float32(input.ConflictSafety)
	vector[featureScopeSafety] = float32(input.ScopeSafety)
	vector[featureBaseScore] = float32(baseScore)
	vector[featureSupportDensity] = float32(float64(len(support)) / 4.0)
	vector[featureCounterDensity] = float32(float64(len(counter)) / 4.0)
	vector[featureSuccessBalance] = float32(successBalance(branch))
	vector[featureFailurePressure] = float32(failurePressure(branch))
	vector[featureAccessPressure] = float32(accessPressure(branch))
	vector[featureRelayMass] = float32(relayMass)
	vector[featureRelayCofire] = float32(relayCofire)
	vector[featureSessionAffinity] = float32(sessionAffinity(query.SessionID, branch.SessionID))
	vector[featureAgentFamilyAffinity] = float32(agentFamilyAffinity(query.AgentType, branch.Family))
	vector[featureBranchDepth] = float32(float64(depth) / 6.0)
	vector[featureSubstratePotential] = float32(substrate.Potential)
	vector[featureFrontierScore] = float32(substrate.Frontier)
	vector[featureInhibitionSafety] = float32(1 - substrate.Inhibition)

	switch branch.Scope {
	case ScopeWorking:
		vector[featureScopeWorking] = 1
	case ScopeEpisodic:
		vector[featureScopeEpisodic] = 1
	case ScopeSemantic:
		vector[featureScopeSemantic] = 1
	case ScopeContradiction:
		vector[featureScopeContradiction] = 1
	case ScopeDormant:
		vector[featureScopeDormant] = 1
	}

	switch branch.Family {
	case TreeFamilyIntent:
		vector[featureFamilyIntent] = 1
	case TreeFamilyConstraint:
		vector[featureFamilyConstraint] = 1
	case TreeFamilyEvidence:
		vector[featureFamilyEvidence] = 1
	case TreeFamilyOutcome:
		vector[featureFamilyOutcome] = 1
	case TreeFamilyAntiPattern:
		vector[featureFamilyAntiPattern] = 1
	}

	agentType := strings.ToLower(strings.TrimSpace(branch.AgentType))
	switch {
	case agentType == "guardian":
		vector[featureSourceGuardian] = 1
	case strings.HasPrefix(agentType, "scribe"):
		vector[featureSourceScribe] = 1
	case agentType == "engineer":
		vector[featureSourceEngineer] = 1
	case agentType == "designer":
		vector[featureSourceDesigner] = 1
	case agentType == "academic":
		vector[featureSourceAcademic] = 1
	case agentType == "librarian":
		vector[featureSourceLibrarian] = 1
	case agentType == "archivalist":
		vector[featureSourceArchivalist] = 1
	default:
		vector[featureSourceOther] = 1
	}

	// Single SIMD clamp pass over the whole vector. The flag-style slots
	// (scope / family / source) already hold values in {0, 1}; the
	// numeric-input slots may be outside [0, 1] (e.g. substrate signals
	// can overshoot under unusual inputs). Two vek32 inplace passes on a
	// 44-float vector fit in a single AVX2 register pair, so the whole
	// operation is a handful of SIMD instructions vs 23 scalar clamp01
	// calls that each wrapped two conditional branches.
	vek32.MaximumNumber_Inplace(vector, 0)
	vek32.MinimumNumber_Inplace(vector, 1)

	return vector
}

func successBalance(branch *Branch) float64 {
	if branch == nil {
		return 0
	}
	total := branch.SuccessCount + branch.FailureCount
	if total <= 0 {
		return clamp01((branch.Utility + branch.SuccessRate) / 2)
	}
	return clamp01(float64(branch.SuccessCount) / float64(total))
}

func failurePressure(branch *Branch) float64 {
	if branch == nil {
		return 0
	}
	total := branch.SuccessCount + branch.FailureCount + branch.CounterCount + 1
	return clamp01(float64(branch.FailureCount+branch.CounterCount) / float64(total))
}

func accessPressure(branch *Branch) float64 {
	if branch == nil {
		return 0
	}
	return clamp01(float64(branch.AccessCount) / 32.0)
}

func sessionAffinity(querySessionID, branchSessionID string) float64 {
	querySessionID = strings.TrimSpace(querySessionID)
	branchSessionID = strings.TrimSpace(branchSessionID)
	switch {
	case querySessionID == "" || branchSessionID == "":
		return 0.25
	case querySessionID == branchSessionID:
		return 1.0
	case branchSessionID == "global":
		return 0.4
	default:
		return 0.1
	}
}

func agentFamilyAffinity(agentType string, family TreeFamily) float64 {
	normalized := strings.ToLower(strings.TrimSpace(agentType))
	// Issue #11 Phase 3 — collapsed taxonomy. The five remaining
	// families (Intent, Constraint, Evidence, Outcome, AntiPattern)
	// cover the same operator-facing semantics as the prior nine via
	// the documented invariants (types.go) + ConstraintSeverity.
	switch {
	case normalized == "engineer":
		switch family {
		case TreeFamilyIntent, TreeFamilyEvidence, TreeFamilyOutcome:
			return 1.0
		case TreeFamilyConstraint, TreeFamilyAntiPattern:
			return 0.85
		default:
			return 0.55
		}
	case normalized == "designer":
		switch family {
		case TreeFamilyIntent, TreeFamilyConstraint, TreeFamilyEvidence, TreeFamilyOutcome:
			return 1.0
		default:
			return 0.5
		}
	case normalized == "guardian":
		switch family {
		case TreeFamilyConstraint, TreeFamilyAntiPattern, TreeFamilyOutcome:
			return 1.0
		case TreeFamilyIntent, TreeFamilyEvidence:
			return 0.8
		default:
			return 0.35
		}
	case strings.HasPrefix(normalized, "scribe"):
		switch family {
		case TreeFamilyEvidence, TreeFamilyIntent, TreeFamilyOutcome:
			return 1.0
		default:
			return 0.6
		}
	case normalized == "academic":
		switch family {
		case TreeFamilyEvidence, TreeFamilyAntiPattern, TreeFamilyConstraint:
			return 1.0
		default:
			return 0.55
		}
	case normalized == "librarian":
		switch family {
		case TreeFamilyEvidence, TreeFamilyIntent:
			return 1.0
		default:
			return 0.55
		}
	case normalized == "archivalist":
		switch family {
		case TreeFamilyOutcome, TreeFamilyIntent, TreeFamilyConstraint:
			return 1.0
		default:
			return 0.55
		}
	case normalized == "guide", normalized == "orchestrator":
		switch family {
		case TreeFamilyIntent, TreeFamilyConstraint, TreeFamilyOutcome:
			return 1.0
		default:
			return 0.6
		}
	default:
		return 0.5
	}
}

func computeBranchDepths(branches []*Branch) map[string]int {
	depths := make(map[string]int, len(branches))
	byID := make(map[string]*Branch, len(branches))
	for _, branch := range branches {
		if branch != nil {
			byID[branch.ID] = branch
		}
	}

	var depthFor func(branch *Branch) int
	depthFor = func(branch *Branch) int {
		if branch == nil {
			return 0
		}
		if depth, ok := depths[branch.ID]; ok {
			return depth
		}
		if branch.ParentID == "" {
			depths[branch.ID] = 0
			return 0
		}
		parent := byID[branch.ParentID]
		depth := 1 + depthFor(parent)
		depths[branch.ID] = depth
		return depth
	}

	for _, branch := range branches {
		depthFor(branch)
	}
	return depths
}

func packFloat32s(values []float32) []byte {
	if len(values) == 0 {
		return nil
	}
	buf := make([]byte, 4*len(values))
	for i, value := range values {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(value))
	}
	return buf
}

func unpackFloat32s(raw []byte) []float32 {
	if len(raw) == 0 {
		return nil
	}
	values := make([]float32, len(raw)/4)
	for i := range values {
		values[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return values
}

func summarizeFeatureSignals(vector []float32, limit int) []FeatureSignal {
	if limit <= 0 {
		limit = 6
	}
	signals := make([]FeatureSignal, 0, len(vector))
	for idx, value := range vector {
		if idx >= len(forestFeatureNames) || value <= 0 {
			continue
		}
		signals = append(signals, FeatureSignal{
			Name:  forestFeatureNames[idx],
			Value: float64(value),
		})
	}
	sort.SliceStable(signals, func(i, j int) bool {
		return signals[i].Value > signals[j].Value
	})
	if len(signals) > limit {
		signals = signals[:limit]
	}
	return signals
}
