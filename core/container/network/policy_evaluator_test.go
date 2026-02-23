package network

import "testing"

func TestPolicyEvaluator_DefaultDeny(t *testing.T) {
	pe := NewPolicyEvaluator(nil)
	req := EvaluationRequest{
		SourceAgentType: "engineer",
		TargetAgentType: "inspector",
		Topic:           "results",
		Direction:       DirectionIngress,
	}
	if pe.Evaluate(req) != PolicyDeny {
		t.Fatal("default policy should deny")
	}
}

func TestPolicyEvaluator_IngressAllow(t *testing.T) {
	policy := &NetworkPolicy{
		Name:        "allow-inspector-to-engineer",
		PodSelector: map[string]string{}, // match all
		Ingress: []IngressRule{{
			From: []PeerSelector{{
				AgentTypes: []string{"inspector"},
			}},
			AllowedTopics: []string{"results"},
		}},
	}
	pe := NewPolicyEvaluator([]*NetworkPolicy{policy})

	req := EvaluationRequest{
		SourceAgentType: "inspector",
		TargetLabels:    map[string]string{},
		Topic:           "results",
		Direction:       DirectionIngress,
	}
	if pe.Evaluate(req) != PolicyAllow {
		t.Fatal("should allow inspector→engineer on results topic")
	}
}

func TestPolicyEvaluator_IngressDenyWrongType(t *testing.T) {
	policy := &NetworkPolicy{
		Name:        "allow-inspector-only",
		PodSelector: map[string]string{},
		Ingress: []IngressRule{{
			From: []PeerSelector{{
				AgentTypes: []string{"inspector"},
			}},
		}},
	}
	pe := NewPolicyEvaluator([]*NetworkPolicy{policy})

	req := EvaluationRequest{
		SourceAgentType: "engineer",
		Topic:           "any",
		Direction:       DirectionIngress,
	}
	if pe.Evaluate(req) != PolicyDeny {
		t.Fatal("should deny engineer when only inspector is allowed")
	}
}

func TestPolicyEvaluator_WildcardTopic(t *testing.T) {
	policy := &NetworkPolicy{
		Name:        "allow-all-topics",
		PodSelector: map[string]string{},
		Ingress: []IngressRule{{
			From:          []PeerSelector{{}},
			AllowedTopics: []string{"*"},
		}},
	}
	pe := NewPolicyEvaluator([]*NetworkPolicy{policy})

	req := EvaluationRequest{
		Topic:     "anything",
		Direction: DirectionIngress,
	}
	if pe.Evaluate(req) != PolicyAllow {
		t.Fatal("wildcard topic should match anything")
	}
}

func TestPolicyEvaluator_EmptySelector_MatchesAll(t *testing.T) {
	policy := &NetworkPolicy{
		Name:        "open-policy",
		PodSelector: map[string]string{},
		Egress: []EgressRule{{
			To: []PeerSelector{{}}, // empty = match all
		}},
	}
	pe := NewPolicyEvaluator([]*NetworkPolicy{policy})

	req := EvaluationRequest{
		SourceAgentType: "engineer",
		TargetAgentType: "any",
		Topic:           "any",
		Direction:       DirectionEgress,
	}
	if pe.Evaluate(req) != PolicyAllow {
		t.Fatal("empty selector should match all")
	}
}

func TestPolicyEvaluator_LabelSelector(t *testing.T) {
	policy := &NetworkPolicy{
		Name:        "label-policy",
		PodSelector: map[string]string{"tier": "backend"},
		Ingress: []IngressRule{{
			From: []PeerSelector{{
				Labels: map[string]string{"tier": "backend"},
			}},
		}},
	}
	pe := NewPolicyEvaluator([]*NetworkPolicy{policy})

	// Matching labels
	req := EvaluationRequest{
		SourceLabels: map[string]string{"tier": "backend"},
		TargetLabels: map[string]string{"tier": "backend"},
		Topic:        "any",
		Direction:    DirectionIngress,
	}
	if pe.Evaluate(req) != PolicyAllow {
		t.Fatal("matching labels should allow")
	}

	// Non-matching labels
	req.TargetLabels = map[string]string{"tier": "frontend"}
	if pe.Evaluate(req) != PolicyDeny {
		t.Fatal("non-matching target labels should deny")
	}
}

func TestPolicyEvaluator_RoleSelector(t *testing.T) {
	policy := &NetworkPolicy{
		Name:        "role-policy",
		PodSelector: map[string]string{},
		Ingress: []IngressRule{{
			From: []PeerSelector{{
				AgentRoles: []string{"supervisor"},
			}},
		}},
	}
	pe := NewPolicyEvaluator([]*NetworkPolicy{policy})

	req := EvaluationRequest{
		SourceAgentRole: "supervisor",
		Direction:       DirectionIngress,
	}
	if pe.Evaluate(req) != PolicyAllow {
		t.Fatal("supervisor role should be allowed")
	}

	req.SourceAgentRole = "worker"
	if pe.Evaluate(req) != PolicyDeny {
		t.Fatal("worker role should be denied")
	}
}

func TestPolicyEvaluator_SetPolicies(t *testing.T) {
	pe := NewPolicyEvaluator(nil)
	if pe.PolicyCount() != 0 {
		t.Fatal("expected 0 policies")
	}

	pe.SetPolicies([]*NetworkPolicy{{Name: "p1"}})
	if pe.PolicyCount() != 1 {
		t.Fatal("expected 1 policy after set")
	}
}
