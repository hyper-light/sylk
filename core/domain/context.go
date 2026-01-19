package domain

import (
	"sort"
	"time"
)

type DomainContext struct {
	OriginalQuery        string              `json:"original_query"`
	DetectedDomains      []Domain            `json:"detected_domains"`
	DomainConfidences    map[Domain]float64  `json:"domain_confidences"`
	IsCrossDomain        bool                `json:"is_cross_domain"`
	PrimaryDomain        Domain              `json:"primary_domain"`
	SecondaryDomains     []Domain            `json:"secondary_domains,omitempty"`
	ClassificationMethod string              `json:"classification_method"`
	Signals              map[Domain][]string `json:"signals"`
	Confidence           float64             `json:"confidence"`
	CacheHit             bool                `json:"cache_hit"`
	CacheKey             string              `json:"cache_key,omitempty"`
	ClassifiedAt         time.Time           `json:"classified_at"`
}

func NewDomainContext(query string) *DomainContext {
	return &DomainContext{
		OriginalQuery:     query,
		DetectedDomains:   make([]Domain, 0),
		DomainConfidences: make(map[Domain]float64),
		Signals:           make(map[Domain][]string),
		ClassifiedAt:      time.Now(),
	}
}

func (dc *DomainContext) AllowedDomains() []Domain {
	if dc.IsCrossDomain {
		return dc.DetectedDomains
	}
	return []Domain{dc.PrimaryDomain}
}

func (dc *DomainContext) HighestConfidenceDomain() Domain {
	return dc.PrimaryDomain
}

func (dc *DomainContext) HasDomain(d Domain) bool {
	for _, detected := range dc.DetectedDomains {
		if detected == d {
			return true
		}
	}
	return false
}

func (dc *DomainContext) AddDomain(d Domain, confidence float64, signals []string) {
	if !dc.HasDomain(d) {
		dc.DetectedDomains = append(dc.DetectedDomains, d)
	}

	dc.DomainConfidences[d] = confidence

	if signals != nil {
		dc.Signals[d] = append(dc.Signals[d], signals...)
	}

	dc.updatePrimarySecondary()
}

func (dc *DomainContext) updatePrimarySecondary() {
	if len(dc.DetectedDomains) == 0 {
		return
	}

	sorted := dc.domainsByConfidence()
	dc.PrimaryDomain = sorted[0]

	if len(sorted) > 1 {
		dc.SecondaryDomains = sorted[1:]
	} else {
		dc.SecondaryDomains = nil
	}
}

func (dc *DomainContext) domainsByConfidence() []Domain {
	type domainConf struct {
		domain     Domain
		confidence float64
	}

	pairs := make([]domainConf, 0, len(dc.DetectedDomains))
	for _, d := range dc.DetectedDomains {
		pairs = append(pairs, domainConf{d, dc.DomainConfidences[d]})
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].confidence > pairs[j].confidence
	})

	result := make([]Domain, len(pairs))
	for i, p := range pairs {
		result[i] = p.domain
	}
	return result
}

func (dc *DomainContext) GetConfidence(d Domain) float64 {
	return dc.DomainConfidences[d]
}

func (dc *DomainContext) GetSignals(d Domain) []string {
	return dc.Signals[d]
}

func (dc *DomainContext) SetCrossDomain(isCross bool) {
	dc.IsCrossDomain = isCross
}

func (dc *DomainContext) SetClassificationMethod(method string) {
	dc.ClassificationMethod = method
}

func (dc *DomainContext) SetOverallConfidence(confidence float64) {
	dc.Confidence = confidence
}

func (dc *DomainContext) MarkCacheHit(key string) {
	dc.CacheHit = true
	dc.CacheKey = key
}

func (dc *DomainContext) Clone() *DomainContext {
	if dc == nil {
		return nil
	}

	clone := &DomainContext{
		OriginalQuery:        dc.OriginalQuery,
		IsCrossDomain:        dc.IsCrossDomain,
		PrimaryDomain:        dc.PrimaryDomain,
		ClassificationMethod: dc.ClassificationMethod,
		Confidence:           dc.Confidence,
		CacheHit:             dc.CacheHit,
		CacheKey:             dc.CacheKey,
		ClassifiedAt:         dc.ClassifiedAt,
	}

	clone.DetectedDomains = cloneDomainSlice(dc.DetectedDomains)
	clone.SecondaryDomains = cloneDomainSlice(dc.SecondaryDomains)
	clone.DomainConfidences = cloneConfidenceMap(dc.DomainConfidences)
	clone.Signals = cloneSignalsMap(dc.Signals)

	return clone
}

func cloneDomainSlice(s []Domain) []Domain {
	if s == nil {
		return nil
	}
	clone := make([]Domain, len(s))
	copy(clone, s)
	return clone
}

func cloneConfidenceMap(m map[Domain]float64) map[Domain]float64 {
	if m == nil {
		return nil
	}
	clone := make(map[Domain]float64, len(m))
	for k, v := range m {
		clone[k] = v
	}
	return clone
}

func cloneSignalsMap(m map[Domain][]string) map[Domain][]string {
	if m == nil {
		return nil
	}
	clone := make(map[Domain][]string, len(m))
	for k, v := range m {
		clonedSlice := make([]string, len(v))
		copy(clonedSlice, v)
		clone[k] = clonedSlice
	}
	return clone
}

func (dc *DomainContext) IsEmpty() bool {
	return len(dc.DetectedDomains) == 0
}

func (dc *DomainContext) DomainCount() int {
	return len(dc.DetectedDomains)
}
