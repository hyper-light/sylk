package vectorgraphdb

type DomainFilter struct {
	allowedDomains map[Domain]bool
	excludeDomains map[Domain]bool
	requireAll     bool
}

type DomainFilterConfig struct {
	AllowedDomains []Domain
	ExcludeDomains []Domain
	RequireAll     bool
}

func NewDomainFilter(config *DomainFilterConfig) *DomainFilter {
	f := &DomainFilter{
		allowedDomains: make(map[Domain]bool),
		excludeDomains: make(map[Domain]bool),
	}

	if config == nil {
		return f
	}

	f.requireAll = config.RequireAll

	for _, d := range config.AllowedDomains {
		f.allowedDomains[d] = true
	}

	for _, d := range config.ExcludeDomains {
		f.excludeDomains[d] = true
	}

	return f
}

func (f *DomainFilter) Allow(domain Domain) bool {
	if f.excludeDomains[domain] {
		return false
	}

	if len(f.allowedDomains) == 0 {
		return true
	}

	return f.allowedDomains[domain]
}

func (f *DomainFilter) AllowNode(node *GraphNode) bool {
	if node == nil {
		return false
	}
	return f.Allow(node.Domain)
}

func (f *DomainFilter) AllowVector(vec *VectorData) bool {
	if vec == nil {
		return false
	}
	return f.Allow(vec.Domain)
}

func (f *DomainFilter) FilterNodes(nodes []*GraphNode) []*GraphNode {
	if len(f.allowedDomains) == 0 && len(f.excludeDomains) == 0 {
		return nodes
	}

	filtered := make([]*GraphNode, 0, len(nodes))
	for _, node := range nodes {
		if f.AllowNode(node) {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func (f *DomainFilter) FilterVectors(vectors []*VectorData) []*VectorData {
	if len(f.allowedDomains) == 0 && len(f.excludeDomains) == 0 {
		return vectors
	}

	filtered := make([]*VectorData, 0, len(vectors))
	for _, vec := range vectors {
		if f.AllowVector(vec) {
			filtered = append(filtered, vec)
		}
	}
	return filtered
}

func (f *DomainFilter) FilterSearchResults(results []SearchResult) []SearchResult {
	if len(f.allowedDomains) == 0 && len(f.excludeDomains) == 0 {
		return results
	}

	filtered := make([]SearchResult, 0, len(results))
	for _, r := range results {
		if f.AllowNode(r.Node) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func (f *DomainFilter) FilterHybridResults(results []HybridResult) []HybridResult {
	if len(f.allowedDomains) == 0 && len(f.excludeDomains) == 0 {
		return results
	}

	filtered := make([]HybridResult, 0, len(results))
	for _, r := range results {
		if f.AllowNode(r.Node) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func (f *DomainFilter) AllowedDomains() []Domain {
	if len(f.allowedDomains) == 0 {
		return nil
	}

	domains := make([]Domain, 0, len(f.allowedDomains))
	for d := range f.allowedDomains {
		domains = append(domains, d)
	}
	return domains
}

func (f *DomainFilter) ExcludedDomains() []Domain {
	if len(f.excludeDomains) == 0 {
		return nil
	}

	domains := make([]Domain, 0, len(f.excludeDomains))
	for d := range f.excludeDomains {
		domains = append(domains, d)
	}
	return domains
}

func (f *DomainFilter) IsEmpty() bool {
	return len(f.allowedDomains) == 0 && len(f.excludeDomains) == 0
}

func (f *DomainFilter) AddAllowed(domains ...Domain) {
	for _, d := range domains {
		f.allowedDomains[d] = true
	}
}

func (f *DomainFilter) AddExcluded(domains ...Domain) {
	for _, d := range domains {
		f.excludeDomains[d] = true
	}
}

func (f *DomainFilter) RemoveAllowed(domains ...Domain) {
	for _, d := range domains {
		delete(f.allowedDomains, d)
	}
}

func (f *DomainFilter) RemoveExcluded(domains ...Domain) {
	for _, d := range domains {
		delete(f.excludeDomains, d)
	}
}

func (f *DomainFilter) Clear() {
	f.allowedDomains = make(map[Domain]bool)
	f.excludeDomains = make(map[Domain]bool)
}

func (f *DomainFilter) Clone() *DomainFilter {
	clone := &DomainFilter{
		allowedDomains: make(map[Domain]bool, len(f.allowedDomains)),
		excludeDomains: make(map[Domain]bool, len(f.excludeDomains)),
		requireAll:     f.requireAll,
	}

	for d := range f.allowedDomains {
		clone.allowedDomains[d] = true
	}

	for d := range f.excludeDomains {
		clone.excludeDomains[d] = true
	}

	return clone
}

func AllowAllDomains() *DomainFilter {
	return NewDomainFilter(nil)
}

func AllowKnowledgeDomains() *DomainFilter {
	return NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: KnowledgeDomains(),
	})
}

func AllowPipelineDomains() *DomainFilter {
	return NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: PipelineDomains(),
	})
}

func AllowControlDomains() *DomainFilter {
	return NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: ControlDomains(),
	})
}

func AllowSingleDomain(domain Domain) *DomainFilter {
	return NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{domain},
	})
}

func ExcludeSingleDomain(domain Domain) *DomainFilter {
	return NewDomainFilter(&DomainFilterConfig{
		ExcludeDomains: []Domain{domain},
	})
}
