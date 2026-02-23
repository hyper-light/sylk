package network

import "sync"

// PolicyDecision is the result of a policy evaluation.
type PolicyDecision int

const (
	PolicyAllow PolicyDecision = iota
	PolicyDeny
)

// EvaluationRequest describes a traffic flow to evaluate.
type EvaluationRequest struct {
	SourceAgentType string
	SourceAgentRole string
	SourceLabels    map[string]string
	TargetAgentType string
	TargetAgentRole string
	TargetLabels    map[string]string
	Topic           string
	Direction       TrafficDirection
}

// TrafficDirection distinguishes ingress from egress evaluation.
type TrafficDirection int

const (
	DirectionIngress TrafficDirection = iota
	DirectionEgress
)

// PolicyEvaluator evaluates network policies against traffic requests.
// Default policy is deny — traffic is only allowed if a matching rule exists.
type PolicyEvaluator struct {
	mu       sync.RWMutex
	policies []*NetworkPolicy
}

// NewPolicyEvaluator creates an evaluator with the given policies.
func NewPolicyEvaluator(policies []*NetworkPolicy) *PolicyEvaluator {
	p := make([]*NetworkPolicy, len(policies))
	copy(p, policies)
	return &PolicyEvaluator{policies: p}
}

// Evaluate checks whether the given traffic request is allowed.
// Returns PolicyDeny if no matching policy allows it (default deny).
func (pe *PolicyEvaluator) Evaluate(req EvaluationRequest) PolicyDecision {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	for _, policy := range pe.policies {
		if pe.policyApplies(policy, req) {
			return PolicyAllow
		}
	}
	return PolicyDeny
}

func (pe *PolicyEvaluator) policyApplies(policy *NetworkPolicy, req EvaluationRequest) bool {
	if !pe.selectorMatchesTarget(policy.PodSelector, req.TargetLabels) {
		return false
	}
	switch req.Direction {
	case DirectionIngress:
		return pe.matchesIngress(policy.Ingress, req)
	case DirectionEgress:
		return pe.matchesEgress(policy.Egress, req)
	default:
		return false
	}
}

func (pe *PolicyEvaluator) matchesIngress(rules []IngressRule, req EvaluationRequest) bool {
	for _, rule := range rules {
		if pe.ingressRuleMatches(rule, req) {
			return true
		}
	}
	return false
}

func (pe *PolicyEvaluator) matchesEgress(rules []EgressRule, req EvaluationRequest) bool {
	for _, rule := range rules {
		if pe.egressRuleMatches(rule, req) {
			return true
		}
	}
	return false
}

func (pe *PolicyEvaluator) ingressRuleMatches(rule IngressRule, req EvaluationRequest) bool {
	if !pe.topicAllowed(rule.AllowedTopics, req.Topic) {
		return false
	}
	return pe.anyPeerMatches(rule.From, req.SourceAgentType, req.SourceAgentRole, req.SourceLabels)
}

func (pe *PolicyEvaluator) egressRuleMatches(rule EgressRule, req EvaluationRequest) bool {
	if !pe.topicAllowed(rule.AllowedTopics, req.Topic) {
		return false
	}
	return pe.anyPeerMatches(rule.To, req.TargetAgentType, req.TargetAgentRole, req.TargetLabels)
}

func (pe *PolicyEvaluator) anyPeerMatches(selectors []PeerSelector, agentType, agentRole string, labels map[string]string) bool {
	if len(selectors) == 0 {
		return true // empty selector = match all
	}
	for _, sel := range selectors {
		if pe.peerSelectorMatches(sel, agentType, agentRole, labels) {
			return true
		}
	}
	return false
}

func (pe *PolicyEvaluator) peerSelectorMatches(sel PeerSelector, agentType, agentRole string, labels map[string]string) bool {
	if !pe.matchesAgentTypes(sel.AgentTypes, agentType) {
		return false
	}
	if !pe.matchesAgentRoles(sel.AgentRoles, agentRole) {
		return false
	}
	return pe.selectorMatchesTarget(sel.Labels, labels)
}

func (pe *PolicyEvaluator) matchesAgentTypes(allowed []string, agentType string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, at := range allowed {
		if at == agentType {
			return true
		}
	}
	return false
}

func (pe *PolicyEvaluator) matchesAgentRoles(allowed []string, agentRole string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, ar := range allowed {
		if ar == agentRole {
			return true
		}
	}
	return false
}

func (pe *PolicyEvaluator) topicAllowed(allowedTopics []string, topic string) bool {
	if len(allowedTopics) == 0 {
		return true // empty = allow all topics
	}
	for _, at := range allowedTopics {
		if at == "*" || at == topic {
			return true
		}
	}
	return false
}

func (pe *PolicyEvaluator) selectorMatchesTarget(selector, target map[string]string) bool {
	if len(selector) == 0 {
		return true // empty selector = match all
	}
	for k, v := range selector {
		if target[k] != v {
			return false
		}
	}
	return true
}

// SetPolicies replaces the policy set. Thread-safe.
func (pe *PolicyEvaluator) SetPolicies(policies []*NetworkPolicy) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	p := make([]*NetworkPolicy, len(policies))
	copy(p, policies)
	pe.policies = p
}

// PolicyCount returns the number of policies.
func (pe *PolicyEvaluator) PolicyCount() int {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	return len(pe.policies)
}
