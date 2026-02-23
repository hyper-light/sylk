package network

// NetworkPolicy defines ingress and egress rules for pod traffic.
type NetworkPolicy struct {
	Name        string
	PodSelector map[string]string // Labels that select target pods
	Ingress     []IngressRule
	Egress      []EgressRule
}

// IngressRule defines allowed inbound traffic sources and topics.
type IngressRule struct {
	From          []PeerSelector
	AllowedTopics []string
}

// EgressRule defines allowed outbound traffic destinations and topics.
type EgressRule struct {
	To            []PeerSelector
	AllowedTopics []string
}

// PeerSelector identifies a set of agents/pods by type, role, or labels.
type PeerSelector struct {
	AgentTypes []string
	AgentRoles []string // Maps to security.AgentRole strings
	Labels     map[string]string
}
