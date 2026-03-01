package guide

import "context"

// FallbackClassifier wraps a primary LLM ClassifierService and a rule-based
// fallback. The LLM is always the primary classifier; rules exist only as a
// graceful degradation path when the LLM API is unavailable (stale credentials,
// quota exhaustion, timeout, etc.).
//
// Classification order:
//  1. LLM primary — for nuanced, context-dependent classification.
//  2. Rules fallback — if the LLM returns an error.
//
// Known LLM misclassification patterns should be fixed via the correction
// memory (AddCorrection), which seeds examples into the LLM prompt, NOT via
// keyword preemption rules.
type FallbackClassifier struct {
	primary  ClassifierService
	fallback ClassifierService
}

// NewFallbackClassifier creates a classifier that tries the LLM primary first
// and falls back to keyword rules only when the LLM is unavailable.
func NewFallbackClassifier(primary, fallback ClassifierService) *FallbackClassifier {
	return &FallbackClassifier{primary: primary, fallback: fallback}
}

func (c *FallbackClassifier) Classify(ctx context.Context, input string) (*ClassificationResult, error) {
	// Primary path: LLM classifier for nuanced intent detection.
	result, err := c.primary.Classify(ctx, input)
	if err == nil {
		return result, nil
	}

	// Degraded path: LLM failed, fall back to keyword heuristics.
	ruleResult, ruleErr := c.fallback.Classify(ctx, input)
	if ruleErr == nil && ruleResult != nil {
		ruleResult.ClassificationMethod = "rule_fallback"
		return ruleResult, nil
	}
	return nil, err
}

func (c *FallbackClassifier) AddCorrection(correction CorrectionRecord) {
	c.primary.AddCorrection(correction)
}

// RuleClassifierService implements ClassifierService using keyword heuristics
// directly. Used only as a degraded fallback when the LLM API is unavailable.
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
