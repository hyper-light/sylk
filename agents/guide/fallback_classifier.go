package guide

import "context"

// rulePreemptThreshold is the minimum rule-classifier confidence at which
// an unambiguous keyword match preempts the LLM classifier. Keywords at or
// above this threshold (e.g. "hand it off" → IntentExecute at 0.90) are
// authoritative signals that the LLM consistently misclassifies.
const rulePreemptThreshold = 0.90

// FallbackClassifier wraps a primary ClassifierService and a rule-based
// fallback. Classification order:
//  1. Rules first — if an unambiguous keyword match hits at >= rulePreemptThreshold,
//     use it directly. This catches execution phrases the LLM misclassifies.
//  2. LLM primary — for nuanced, context-dependent classification.
//  3. Rules fallback — if the LLM returns an error (stale credentials, quota, etc.).
type FallbackClassifier struct {
	primary  ClassifierService
	fallback ClassifierService
}

// NewFallbackClassifier creates a classifier that tries high-confidence rules
// first, then the LLM primary, then rules as a degraded fallback.
func NewFallbackClassifier(primary, fallback ClassifierService) *FallbackClassifier {
	return &FallbackClassifier{primary: primary, fallback: fallback}
}

func (c *FallbackClassifier) Classify(ctx context.Context, input string) (*ClassificationResult, error) {
	// Fast path: high-confidence rule matches preempt the LLM.
	// Execution keywords ("go ahead", "hand it off", "ship it") are
	// unambiguous signals that the LLM consistently maps to IntentPlan.
	ruleResult, ruleErr := c.fallback.Classify(ctx, input)
	if ruleErr == nil && ruleResult != nil &&
		ruleResult.Intent != IntentUnknown && ruleResult.Intent != IntentHelp &&
		ruleResult.Confidence >= rulePreemptThreshold {
		ruleResult.ClassificationMethod = "rule_preempt"
		return ruleResult, nil
	}

	// Normal path: LLM classifier for nuanced intent detection.
	result, err := c.primary.Classify(ctx, input)
	if err == nil {
		return result, nil
	}

	// Degraded path: LLM failed, use whatever rules returned.
	if ruleResult != nil && ruleErr == nil {
		return ruleResult, nil
	}
	return nil, err
}

func (c *FallbackClassifier) AddCorrection(correction CorrectionRecord) {
	c.primary.AddCorrection(correction)
}

// RuleClassifierService implements ClassifierService using keyword heuristics
// directly, without going through the Anthropic ClassifierClient adapter.
type RuleClassifierService struct{}

func NewRuleClassifierService() *RuleClassifierService {
	return &RuleClassifierService{}
}

func (c *RuleClassifierService) Classify(_ context.Context, input string) (*ClassificationResult, error) {
	intent, domain, target, confidence := classifyByRules(input)
	return normalizeClassificationResult(&ClassificationResult{
		Intent:      intent,
		Domain:      domain,
		TargetAgent: TargetAgent(target),
		Confidence:  confidence,
	}), nil
}

func (c *RuleClassifierService) AddCorrection(_ CorrectionRecord) {}

// Ensure both implement ClassifierService.
var (
	_ ClassifierService = (*FallbackClassifier)(nil)
	_ ClassifierService = (*RuleClassifierService)(nil)
)
