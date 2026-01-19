package classifier

import (
	"context"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

type ClassificationStage interface {
	Classify(ctx context.Context, query string, caller concurrency.AgentType) (*StageResult, error)
	Name() string
	Priority() int
}

type StageResult struct {
	Domains        []domain.Domain            `json:"domains"`
	Confidences    map[domain.Domain]float64  `json:"confidences"`
	Signals        map[domain.Domain][]string `json:"signals"`
	ShouldContinue bool                       `json:"should_continue"`
	Method         string                     `json:"method"`
}

func NewStageResult() *StageResult {
	return &StageResult{
		Domains:        make([]domain.Domain, 0),
		Confidences:    make(map[domain.Domain]float64),
		Signals:        make(map[domain.Domain][]string),
		ShouldContinue: true,
	}
}

func (r *StageResult) AddDomain(d domain.Domain, confidence float64, signals []string) {
	if !r.hasDomain(d) {
		r.Domains = append(r.Domains, d)
	}
	r.Confidences[d] = confidence
	if signals != nil {
		r.Signals[d] = append(r.Signals[d], signals...)
	}
}

func (r *StageResult) hasDomain(d domain.Domain) bool {
	for _, existing := range r.Domains {
		if existing == d {
			return true
		}
	}
	return false
}

func (r *StageResult) GetConfidence(d domain.Domain) float64 {
	return r.Confidences[d]
}

func (r *StageResult) GetSignals(d domain.Domain) []string {
	return r.Signals[d]
}

func (r *StageResult) HighestConfidence() (domain.Domain, float64) {
	var maxDomain domain.Domain
	var maxConf float64

	for d, conf := range r.Confidences {
		if conf > maxConf {
			maxDomain = d
			maxConf = conf
		}
	}

	return maxDomain, maxConf
}

func (r *StageResult) IsTerminal(threshold float64) bool {
	_, maxConf := r.HighestConfidence()
	return maxConf >= threshold && len(r.Domains) == 1
}

func (r *StageResult) SetMethod(method string) {
	r.Method = method
}

func (r *StageResult) MarkComplete() {
	r.ShouldContinue = false
}

func (r *StageResult) IsEmpty() bool {
	return len(r.Domains) == 0
}

func (r *StageResult) DomainCount() int {
	return len(r.Domains)
}

func (r *StageResult) Clone() *StageResult {
	if r == nil {
		return nil
	}

	clone := &StageResult{
		ShouldContinue: r.ShouldContinue,
		Method:         r.Method,
	}

	clone.Domains = make([]domain.Domain, len(r.Domains))
	copy(clone.Domains, r.Domains)

	clone.Confidences = make(map[domain.Domain]float64, len(r.Confidences))
	for k, v := range r.Confidences {
		clone.Confidences[k] = v
	}

	clone.Signals = make(map[domain.Domain][]string, len(r.Signals))
	for k, v := range r.Signals {
		signalsCopy := make([]string, len(v))
		copy(signalsCopy, v)
		clone.Signals[k] = signalsCopy
	}

	return clone
}

func (r *StageResult) Merge(other *StageResult) {
	if other == nil {
		return
	}

	for _, d := range other.Domains {
		if !r.hasDomain(d) {
			r.Domains = append(r.Domains, d)
		}
		if other.Confidences[d] > r.Confidences[d] {
			r.Confidences[d] = other.Confidences[d]
		}
		r.Signals[d] = append(r.Signals[d], other.Signals[d]...)
	}
}
