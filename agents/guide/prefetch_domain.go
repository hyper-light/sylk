package guide

import (
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

type PrefetchCandidate struct {
	AgentType  concurrency.AgentType
	QueryHash  string
	Priority   int
	Relevance  float64
	IsRelevant bool
}

type PrefetchDomainFilter struct {
	minRelevance      float64
	enableCrossDomain bool
	maxCandidates     int
}

type PrefetchFilterConfig struct {
	MinRelevance      float64
	EnableCrossDomain bool
	MaxCandidates     int
}

func NewPrefetchDomainFilter(config *PrefetchFilterConfig) *PrefetchDomainFilter {
	cfg := applyFilterDefaults(config)
	return &PrefetchDomainFilter{
		minRelevance:      cfg.MinRelevance,
		enableCrossDomain: cfg.EnableCrossDomain,
		maxCandidates:     cfg.MaxCandidates,
	}
}

func applyFilterDefaults(config *PrefetchFilterConfig) *PrefetchFilterConfig {
	defaults := &PrefetchFilterConfig{
		MinRelevance:      0.3,
		EnableCrossDomain: true,
		MaxCandidates:     5,
	}

	if config == nil {
		return defaults
	}

	if config.MinRelevance > 0 {
		defaults.MinRelevance = config.MinRelevance
	}
	if config.MaxCandidates > 0 {
		defaults.MaxCandidates = config.MaxCandidates
	}
	defaults.EnableCrossDomain = config.EnableCrossDomain

	return defaults
}

func (f *PrefetchDomainFilter) Filter(
	candidates []PrefetchCandidate,
	domainCtx *domain.DomainContext,
) []PrefetchCandidate {
	if domainCtx == nil || domainCtx.IsEmpty() {
		return candidates
	}

	filtered := make([]PrefetchCandidate, 0, len(candidates))

	for _, candidate := range candidates {
		scored := f.scoreCandidate(candidate, domainCtx)
		if scored.IsRelevant {
			filtered = append(filtered, scored)
		}
	}

	return f.limitCandidates(filtered)
}

func (f *PrefetchDomainFilter) scoreCandidate(
	candidate PrefetchCandidate,
	domainCtx *domain.DomainContext,
) PrefetchCandidate {
	candidateDomain, ok := domain.DomainFromAgentType(candidate.AgentType)
	if !ok {
		candidate.IsRelevant = false
		return candidate
	}

	relevance := f.computeRelevance(candidateDomain, domainCtx)
	candidate.Relevance = relevance
	candidate.IsRelevant = relevance >= f.minRelevance

	return candidate
}

func (f *PrefetchDomainFilter) computeRelevance(
	candidateDomain domain.Domain,
	domainCtx *domain.DomainContext,
) float64 {
	if candidateDomain == domainCtx.PrimaryDomain {
		return domainCtx.GetConfidence(candidateDomain)
	}

	if f.enableCrossDomain && domainCtx.IsCrossDomain {
		return f.computeCrossDomainRelevance(candidateDomain, domainCtx)
	}

	if domainCtx.HasDomain(candidateDomain) {
		return domainCtx.GetConfidence(candidateDomain) * 0.8
	}

	return 0.0
}

func (f *PrefetchDomainFilter) computeCrossDomainRelevance(
	candidateDomain domain.Domain,
	domainCtx *domain.DomainContext,
) float64 {
	for i, secondary := range domainCtx.SecondaryDomains {
		if secondary == candidateDomain {
			decay := 1.0 - (float64(i) * 0.1)
			if decay < 0.5 {
				decay = 0.5
			}
			return domainCtx.GetConfidence(candidateDomain) * decay
		}
	}
	return 0.0
}

func (f *PrefetchDomainFilter) limitCandidates(candidates []PrefetchCandidate) []PrefetchCandidate {
	if len(candidates) <= f.maxCandidates {
		return candidates
	}

	f.sortByRelevance(candidates)
	return candidates[:f.maxCandidates]
}

func (f *PrefetchDomainFilter) sortByRelevance(candidates []PrefetchCandidate) {
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].Relevance > candidates[j-1].Relevance; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}
}

func (f *PrefetchDomainFilter) FilterByDomains(
	candidates []PrefetchCandidate,
	allowedDomains []domain.Domain,
) []PrefetchCandidate {
	allowed := make(map[domain.Domain]bool, len(allowedDomains))
	for _, d := range allowedDomains {
		allowed[d] = true
	}

	filtered := make([]PrefetchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidateDomain, ok := domain.DomainFromAgentType(candidate.AgentType)
		if ok && allowed[candidateDomain] {
			candidate.IsRelevant = true
			filtered = append(filtered, candidate)
		}
	}

	return f.limitCandidates(filtered)
}

func (f *PrefetchDomainFilter) IsRelevantAgent(
	agentType concurrency.AgentType,
	domainCtx *domain.DomainContext,
) bool {
	if domainCtx == nil || domainCtx.IsEmpty() {
		return true
	}

	candidateDomain, ok := domain.DomainFromAgentType(agentType)
	if !ok {
		return false
	}

	relevance := f.computeRelevance(candidateDomain, domainCtx)
	return relevance >= f.minRelevance
}

func (f *PrefetchDomainFilter) GetRelevantAgents(
	domainCtx *domain.DomainContext,
) []concurrency.AgentType {
	if domainCtx == nil || domainCtx.IsEmpty() {
		return nil
	}

	agents := make([]concurrency.AgentType, 0, len(domainCtx.AllowedDomains()))
	for _, d := range domainCtx.AllowedDomains() {
		agents = append(agents, d.ToAgentType())
	}

	return agents
}

func (f *PrefetchDomainFilter) MinRelevance() float64 {
	return f.minRelevance
}

func (f *PrefetchDomainFilter) MaxCandidates() int {
	return f.maxCandidates
}

func (f *PrefetchDomainFilter) IsCrossDomainEnabled() bool {
	return f.enableCrossDomain
}
