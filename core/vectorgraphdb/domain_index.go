package vectorgraphdb

import (
	"sync"
)

type DomainIndex struct {
	mu      sync.RWMutex
	index   map[Domain]map[string]bool
	reverse map[string]Domain
}

func NewDomainIndex() *DomainIndex {
	return &DomainIndex{
		index:   make(map[Domain]map[string]bool),
		reverse: make(map[string]Domain),
	}
}

func (di *DomainIndex) Index(vectorID string, domain Domain) {
	di.mu.Lock()
	defer di.mu.Unlock()

	if existing, ok := di.reverse[vectorID]; ok && existing != domain {
		di.removeFromDomain(vectorID, existing)
	}

	if di.index[domain] == nil {
		di.index[domain] = make(map[string]bool)
	}

	di.index[domain][vectorID] = true
	di.reverse[vectorID] = domain
}

func (di *DomainIndex) removeFromDomain(vectorID string, domain Domain) {
	if di.index[domain] != nil {
		delete(di.index[domain], vectorID)
	}
}

func (di *DomainIndex) Remove(vectorID string) {
	di.mu.Lock()
	defer di.mu.Unlock()

	if domain, ok := di.reverse[vectorID]; ok {
		di.removeFromDomain(vectorID, domain)
		delete(di.reverse, vectorID)
	}
}

func (di *DomainIndex) GetVectorsByDomain(domain Domain) []string {
	di.mu.RLock()
	defer di.mu.RUnlock()

	domainSet := di.index[domain]
	if domainSet == nil {
		return nil
	}

	vectors := make([]string, 0, len(domainSet))
	for id := range domainSet {
		vectors = append(vectors, id)
	}
	return vectors
}

func (di *DomainIndex) GetDomain(vectorID string) (Domain, bool) {
	di.mu.RLock()
	defer di.mu.RUnlock()

	domain, ok := di.reverse[vectorID]
	return domain, ok
}

func (di *DomainIndex) ContainsDomain(domain Domain) bool {
	di.mu.RLock()
	defer di.mu.RUnlock()

	return len(di.index[domain]) > 0
}

func (di *DomainIndex) CountByDomain(domain Domain) int {
	di.mu.RLock()
	defer di.mu.RUnlock()

	return len(di.index[domain])
}

func (di *DomainIndex) TotalCount() int {
	di.mu.RLock()
	defer di.mu.RUnlock()

	return len(di.reverse)
}

func (di *DomainIndex) Stats() map[Domain]int {
	di.mu.RLock()
	defer di.mu.RUnlock()

	stats := make(map[Domain]int)
	for domain, set := range di.index {
		stats[domain] = len(set)
	}
	return stats
}

func (di *DomainIndex) FilterVectorIDs(vectorIDs []string, allowedDomains []Domain) []string {
	if len(allowedDomains) == 0 {
		return vectorIDs
	}

	di.mu.RLock()
	defer di.mu.RUnlock()

	allowed := make(map[Domain]bool, len(allowedDomains))
	for _, d := range allowedDomains {
		allowed[d] = true
	}

	filtered := make([]string, 0, len(vectorIDs))
	for _, id := range vectorIDs {
		if domain, ok := di.reverse[id]; ok && allowed[domain] {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

func (di *DomainIndex) GetVectorsByDomains(domains []Domain) []string {
	if len(domains) == 0 {
		return nil
	}

	di.mu.RLock()
	defer di.mu.RUnlock()

	var totalSize int
	for _, d := range domains {
		totalSize += len(di.index[d])
	}

	vectors := make([]string, 0, totalSize)
	for _, d := range domains {
		for id := range di.index[d] {
			vectors = append(vectors, id)
		}
	}
	return vectors
}

func (di *DomainIndex) BatchIndex(entries []DomainIndexEntry) {
	di.mu.Lock()
	defer di.mu.Unlock()

	for _, entry := range entries {
		if existing, ok := di.reverse[entry.VectorID]; ok && existing != entry.Domain {
			di.removeFromDomain(entry.VectorID, existing)
		}

		if di.index[entry.Domain] == nil {
			di.index[entry.Domain] = make(map[string]bool)
		}

		di.index[entry.Domain][entry.VectorID] = true
		di.reverse[entry.VectorID] = entry.Domain
	}
}

type DomainIndexEntry struct {
	VectorID string
	Domain   Domain
}

func (di *DomainIndex) BatchRemove(vectorIDs []string) {
	di.mu.Lock()
	defer di.mu.Unlock()

	for _, id := range vectorIDs {
		if domain, ok := di.reverse[id]; ok {
			di.removeFromDomain(id, domain)
			delete(di.reverse, id)
		}
	}
}

func (di *DomainIndex) Clear() {
	di.mu.Lock()
	defer di.mu.Unlock()

	di.index = make(map[Domain]map[string]bool)
	di.reverse = make(map[string]Domain)
}

func (di *DomainIndex) Rebuild(vectors []*VectorData) {
	di.mu.Lock()
	defer di.mu.Unlock()

	di.index = make(map[Domain]map[string]bool)
	di.reverse = make(map[string]Domain, len(vectors))

	for _, vec := range vectors {
		if di.index[vec.Domain] == nil {
			di.index[vec.Domain] = make(map[string]bool)
		}
		di.index[vec.Domain][vec.NodeID] = true
		di.reverse[vec.NodeID] = vec.Domain
	}
}

func (di *DomainIndex) ExistingDomains() []Domain {
	di.mu.RLock()
	defer di.mu.RUnlock()

	domains := make([]Domain, 0, len(di.index))
	for d, set := range di.index {
		if len(set) > 0 {
			domains = append(domains, d)
		}
	}
	return domains
}
