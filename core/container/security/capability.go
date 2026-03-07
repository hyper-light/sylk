package security

import "slices"

// CapabilitySet is the runtime-resolved capability set for a container.
// It is derived from the spec-level CapabilitySpec combined with role defaults
// and admission controller mutations.
type CapabilitySet struct {
	signals         map[string]struct{} // Allowed signal types
	publishTopics   map[string]struct{} // Bus topics publishable
	subscribeTopics map[string]struct{} // Bus topics subscribable
	pathRead        map[string]struct{} // Paths with read access
	pathWrite       map[string]struct{} // Paths with write access
	networkEgress   map[string]struct{} // Allowed egress domain patterns (e.g. "*.golang.org", "*")
	networkIngress  map[string]struct{} // Allowed ingress content types (e.g. "text/html", "application/pdf")
	canEscalate     bool                // Can request additional capabilities
}

// NewCapabilitySet creates a CapabilitySet from explicit allow lists.
func NewCapabilitySet(
	signals []string,
	publishTopics []string,
	subscribeTopics []string,
	pathRead []string,
	pathWrite []string,
	canEscalate bool,
) *CapabilitySet {
	return &CapabilitySet{
		signals:         toSet(signals),
		publishTopics:   toSet(publishTopics),
		subscribeTopics: toSet(subscribeTopics),
		pathRead:        toSet(pathRead),
		pathWrite:       toSet(pathWrite),
		networkEgress:   make(map[string]struct{}),
		networkIngress:  make(map[string]struct{}),
		canEscalate:     canEscalate,
	}
}

// NewCapabilitySetFull creates a CapabilitySet with all fields including network capabilities.
func NewCapabilitySetFull(
	signals []string,
	publishTopics []string,
	subscribeTopics []string,
	pathRead []string,
	pathWrite []string,
	networkEgress []string,
	networkIngress []string,
	canEscalate bool,
) *CapabilitySet {
	return &CapabilitySet{
		signals:         toSet(signals),
		publishTopics:   toSet(publishTopics),
		subscribeTopics: toSet(subscribeTopics),
		pathRead:        toSet(pathRead),
		pathWrite:       toSet(pathWrite),
		networkEgress:   toSet(networkEgress),
		networkIngress:  toSet(networkIngress),
		canEscalate:     canEscalate,
	}
}

// EmptyCapabilitySet creates a CapabilitySet with no capabilities.
func EmptyCapabilitySet() *CapabilitySet {
	return &CapabilitySet{
		signals:         make(map[string]struct{}),
		publishTopics:   make(map[string]struct{}),
		subscribeTopics: make(map[string]struct{}),
		pathRead:        make(map[string]struct{}),
		pathWrite:       make(map[string]struct{}),
		networkEgress:   make(map[string]struct{}),
		networkIngress:  make(map[string]struct{}),
	}
}

// CanSignal reports whether the given signal type is permitted.
func (c *CapabilitySet) CanSignal(signal string) bool {
	_, ok := c.signals[signal]
	return ok
}

// CanPublish reports whether publishing to the given topic is permitted.
func (c *CapabilitySet) CanPublish(topic string) bool {
	_, ok := c.publishTopics[topic]
	return ok
}

// CanSubscribe reports whether subscribing to the given topic is permitted.
func (c *CapabilitySet) CanSubscribe(topic string) bool {
	_, ok := c.subscribeTopics[topic]
	return ok
}

// CanReadPath reports whether reading the given path is permitted.
func (c *CapabilitySet) CanReadPath(path string) bool {
	_, ok := c.pathRead[path]
	return ok
}

// CanWritePath reports whether writing to the given path is permitted.
func (c *CapabilitySet) CanWritePath(path string) bool {
	_, ok := c.pathWrite[path]
	return ok
}

// CanEscalate reports whether the container can request additional capabilities.
func (c *CapabilitySet) CanEscalate() bool {
	return c.canEscalate
}

// Signals returns a sorted snapshot of allowed signal types.
func (c *CapabilitySet) Signals() []string {
	return sortedKeys(c.signals)
}

// PublishTopics returns a sorted snapshot of publishable topics.
func (c *CapabilitySet) PublishTopics() []string {
	return sortedKeys(c.publishTopics)
}

// SubscribeTopics returns a sorted snapshot of subscribable topics.
func (c *CapabilitySet) SubscribeTopics() []string {
	return sortedKeys(c.subscribeTopics)
}

// ReadPaths returns a sorted snapshot of readable paths.
func (c *CapabilitySet) ReadPaths() []string {
	return sortedKeys(c.pathRead)
}

// WritePaths returns a sorted snapshot of writable paths.
func (c *CapabilitySet) WritePaths() []string {
	return sortedKeys(c.pathWrite)
}

// CanNetworkEgress reports whether outbound access to the given domain is permitted.
// Supports wildcard patterns: "*" allows all, "*.example.com" allows subdomains.
func (c *CapabilitySet) CanNetworkEgress(domain string) bool {
	if _, ok := c.networkEgress["*"]; ok {
		return true
	}
	if _, ok := c.networkEgress[domain]; ok {
		return true
	}
	return matchWildcardDomain(c.networkEgress, domain)
}

// CanNetworkIngress reports whether inbound content of the given type is permitted.
func (c *CapabilitySet) CanNetworkIngress(contentType string) bool {
	if _, ok := c.networkIngress["*"]; ok {
		return true
	}
	_, ok := c.networkIngress[contentType]
	return ok
}

// NetworkEgressDomains returns a sorted snapshot of allowed egress domains.
func (c *CapabilitySet) NetworkEgressDomains() []string {
	return sortedKeys(c.networkEgress)
}

// NetworkIngressTypes returns a sorted snapshot of allowed ingress content types.
func (c *CapabilitySet) NetworkIngressTypes() []string {
	return sortedKeys(c.networkIngress)
}

// GrantNetworkEgress adds an egress domain capability. Returns true if newly added.
func (c *CapabilitySet) GrantNetworkEgress(domain string) bool {
	return addToSet(c.networkEgress, domain)
}

// GrantNetworkIngress adds an ingress content type capability. Returns true if newly added.
func (c *CapabilitySet) GrantNetworkIngress(contentType string) bool {
	return addToSet(c.networkIngress, contentType)
}

// RevokeNetworkEgress removes an egress domain capability. Returns true if removed.
func (c *CapabilitySet) RevokeNetworkEgress(domain string) bool {
	return removeFromSet(c.networkEgress, domain)
}

// GrantSignal adds a signal capability. Returns true if newly added.
func (c *CapabilitySet) GrantSignal(signal string) bool {
	return addToSet(c.signals, signal)
}

// RevokeSignal removes a signal capability. Returns true if removed.
func (c *CapabilitySet) RevokeSignal(signal string) bool {
	return removeFromSet(c.signals, signal)
}

// GrantPublish adds a publish topic capability. Returns true if newly added.
func (c *CapabilitySet) GrantPublish(topic string) bool {
	return addToSet(c.publishTopics, topic)
}

// GrantSubscribe adds a subscribe topic capability. Returns true if newly added.
func (c *CapabilitySet) GrantSubscribe(topic string) bool {
	return addToSet(c.subscribeTopics, topic)
}

// GrantReadPath adds a read path capability. Returns true if newly added.
func (c *CapabilitySet) GrantReadPath(path string) bool {
	return addToSet(c.pathRead, path)
}

// GrantWritePath adds a write path capability. Returns true if newly added.
func (c *CapabilitySet) GrantWritePath(path string) bool {
	return addToSet(c.pathWrite, path)
}

// Merge combines another CapabilitySet into this one (union).
func (c *CapabilitySet) Merge(other *CapabilitySet) {
	mergeSet(c.signals, other.signals)
	mergeSet(c.publishTopics, other.publishTopics)
	mergeSet(c.subscribeTopics, other.subscribeTopics)
	mergeSet(c.pathRead, other.pathRead)
	mergeSet(c.pathWrite, other.pathWrite)
	mergeSet(c.networkEgress, other.networkEgress)
	mergeSet(c.networkIngress, other.networkIngress)
	c.canEscalate = c.canEscalate || other.canEscalate
}

// Clone returns a deep copy of the capability set.
func (c *CapabilitySet) Clone() *CapabilitySet {
	return &CapabilitySet{
		signals:         cloneSet(c.signals),
		publishTopics:   cloneSet(c.publishTopics),
		subscribeTopics: cloneSet(c.subscribeTopics),
		pathRead:        cloneSet(c.pathRead),
		pathWrite:       cloneSet(c.pathWrite),
		networkEgress:   cloneSet(c.networkEgress),
		networkIngress:  cloneSet(c.networkIngress),
		canEscalate:     c.canEscalate,
	}
}

func toSet(items []string) map[string]struct{} {
	s := make(map[string]struct{}, len(items))
	for _, item := range items {
		s[item] = struct{}{}
	}
	return s
}

func addToSet(s map[string]struct{}, key string) bool {
	if _, exists := s[key]; exists {
		return false
	}
	s[key] = struct{}{}
	return true
}

func removeFromSet(s map[string]struct{}, key string) bool {
	if _, exists := s[key]; !exists {
		return false
	}
	delete(s, key)
	return true
}

func mergeSet(dst, src map[string]struct{}) {
	for k := range src {
		dst[k] = struct{}{}
	}
}

func cloneSet(s map[string]struct{}) map[string]struct{} {
	c := make(map[string]struct{}, len(s))
	for k := range s {
		c[k] = struct{}{}
	}
	return c
}

func sortedKeys(s map[string]struct{}) []string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// matchWildcardDomain checks if domain matches any "*.suffix" pattern in the set.
// For example, "docs.golang.org" matches "*.golang.org".
func matchWildcardDomain(patterns map[string]struct{}, domain string) bool {
	for pattern := range patterns {
		if len(pattern) < 3 || pattern[0] != '*' || pattern[1] != '.' {
			continue
		}
		suffix := pattern[1:] // ".golang.org"
		if len(domain) > len(suffix) && domain[len(domain)-len(suffix):] == suffix {
			return true
		}
	}
	return false
}
