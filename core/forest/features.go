package forest

import (
	"encoding/binary"
	"math"
	"sort"
	"strings"
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
	featureFamilyDecision
	featureFamilyOutcome
	featureFamilyPreference
	featureFamilyCapability
	featureFamilyOpportunity
	featureFamilyConflict
	featureSourceGuardian
	featureSourceScribe
	featureSourceEngineer
	featureSourceDesigner
	featureSourceAcademic
	featureSourceLibrarian
	featureSourceArchivalist
	featureSourceOther
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
	"family_decision",
	"family_outcome",
	"family_preference",
	"family_capability",
	"family_opportunity",
	"family_conflict",
	"source_guardian",
	"source_scribe",
	"source_engineer",
	"source_designer",
	"source_academic",
	"source_librarian",
	"source_archivalist",
	"source_other",
}

func buildFeatureVector(
	query Query,
	branch *Branch,
	input scoreInput,
	support []PacketEvidence,
	counter []PacketEvidence,
	baseScore float64,
	relayMass float64,
	depth int,
	substrate substrateSignal,
) []float32 {
	vector := make([]float32, featureCount)
	vector[featureQueryMatch] = float32(clamp01(input.QueryMatch))
	vector[featureEvidence] = float32(clamp01(input.Evidence))
	vector[featureCanopy] = float32(clamp01(input.Canopy))
	vector[featureConfidence] = float32(clamp01(input.Confidence))
	vector[featureRecency] = float32(clamp01(input.Recency))
	vector[featureWarmth] = float32(clamp01(input.Warmth))
	vector[featureUtility] = float32(clamp01(input.Utility))
	vector[featureSalience] = float32(clamp01(input.Salience))
	vector[featureConflictSafety] = float32(clamp01(input.ConflictSafety))
	vector[featureScopeSafety] = float32(clamp01(input.ScopeSafety))
	vector[featureBaseScore] = float32(clamp01(baseScore))
	vector[featureSupportDensity] = float32(clamp01(float64(len(support)) / 4.0))
	vector[featureCounterDensity] = float32(clamp01(float64(len(counter)) / 4.0))
	vector[featureSuccessBalance] = float32(successBalance(branch))
	vector[featureFailurePressure] = float32(failurePressure(branch))
	vector[featureAccessPressure] = float32(accessPressure(branch))
	vector[featureRelayMass] = float32(clamp01(relayMass))
	vector[featureSessionAffinity] = float32(sessionAffinity(query.SessionID, branch.SessionID))
	vector[featureAgentFamilyAffinity] = float32(agentFamilyAffinity(query.AgentType, branch.Family))
	vector[featureBranchDepth] = float32(clamp01(float64(depth) / 6.0))
	vector[featureSubstratePotential] = float32(clamp01(substrate.Potential))
	vector[featureFrontierScore] = float32(clamp01(substrate.Frontier))
	vector[featureInhibitionSafety] = float32(clamp01(1 - substrate.Inhibition))

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
	case TreeFamilyDecision:
		vector[featureFamilyDecision] = 1
	case TreeFamilyOutcome:
		vector[featureFamilyOutcome] = 1
	case TreeFamilyPreference:
		vector[featureFamilyPreference] = 1
	case TreeFamilyCapability:
		vector[featureFamilyCapability] = 1
	case TreeFamilyOpportunity:
		vector[featureFamilyOpportunity] = 1
	case TreeFamilyConflict:
		vector[featureFamilyConflict] = 1
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
	switch {
	case normalized == "engineer":
		switch family {
		case TreeFamilyCapability, TreeFamilyDecision, TreeFamilyEvidence, TreeFamilyOutcome:
			return 1.0
		case TreeFamilyConstraint, TreeFamilyConflict:
			return 0.85
		default:
			return 0.55
		}
	case normalized == "designer":
		switch family {
		case TreeFamilyIntent, TreeFamilyConstraint, TreeFamilyEvidence, TreeFamilyOutcome:
			return 1.0
		case TreeFamilyPreference, TreeFamilyDecision:
			return 0.85
		default:
			return 0.5
		}
	case normalized == "guardian":
		switch family {
		case TreeFamilyConstraint, TreeFamilyConflict, TreeFamilyOutcome:
			return 1.0
		case TreeFamilyDecision, TreeFamilyEvidence:
			return 0.8
		default:
			return 0.35
		}
	case strings.HasPrefix(normalized, "scribe"):
		switch family {
		case TreeFamilyEvidence, TreeFamilyDecision, TreeFamilyOutcome:
			return 1.0
		default:
			return 0.6
		}
	case normalized == "academic":
		switch family {
		case TreeFamilyEvidence, TreeFamilyConflict, TreeFamilyConstraint:
			return 1.0
		default:
			return 0.55
		}
	case normalized == "librarian":
		switch family {
		case TreeFamilyEvidence, TreeFamilyCapability, TreeFamilyDecision:
			return 1.0
		default:
			return 0.55
		}
	case normalized == "archivalist":
		switch family {
		case TreeFamilyOutcome, TreeFamilyDecision, TreeFamilyPreference:
			return 1.0
		default:
			return 0.55
		}
	case normalized == "guide", normalized == "orchestrator":
		switch family {
		case TreeFamilyIntent, TreeFamilyConstraint, TreeFamilyCapability, TreeFamilyOutcome:
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
