// Package errors implements a 5-tier error taxonomy with classification and handling behavior.
package errors

import (
	"sort"
	"time"
)

// RemedyAction represents the type of remediation action to take.
type RemedyAction string

const (
	// RemedyRetry indicates the operation should be retried.
	RemedyRetry RemedyAction = "retry"

	// RemedyAlternative indicates an alternative approach should be used.
	RemedyAlternative RemedyAction = "alternative"

	// RemedyAbort indicates the operation should be aborted.
	RemedyAbort RemedyAction = "abort"

	// RemedyManual indicates manual user intervention is required.
	RemedyManual RemedyAction = "manual"
)

// Remedy represents a potential fix for an error.
type Remedy struct {
	ID          string
	Description string
	Action      RemedyAction
	Confidence  float64
	TokenCost   int
	Metadata    map[string]string
}

// NewRemedy creates a new Remedy with the given parameters.
func NewRemedy(description string, action RemedyAction, confidence float64) *Remedy {
	return &Remedy{
		ID:          generateRemedyID(),
		Description: description,
		Action:      action,
		Confidence:  normalizeConfidence(confidence),
		TokenCost:   0,
		Metadata:    make(map[string]string),
	}
}

// generateRemedyID creates a unique ID for a remedy.
func generateRemedyID() string {
	return time.Now().Format("20060102150405.000000")
}

// normalizeConfidence ensures confidence is within [0.0, 1.0].
func normalizeConfidence(conf float64) float64 {
	if conf < 0.0 {
		return 0.0
	}
	if conf > 1.0 {
		return 1.0
	}
	return conf
}

// WithTokenCost sets the token cost for the remedy.
func (r *Remedy) WithTokenCost(cost int) *Remedy {
	r.TokenCost = cost
	return r
}

// WithMetadata adds a metadata key-value pair to the remedy.
func (r *Remedy) WithMetadata(key, value string) *Remedy {
	r.Metadata[key] = value
	return r
}

// RemedySet is a ranked collection of remedies for an error.
type RemedySet struct {
	Error     error
	Remedies  []*Remedy
	Timestamp time.Time
}

// NewRemedySet creates a new RemedySet for the given error.
func NewRemedySet(err error) *RemedySet {
	return &RemedySet{
		Error:     err,
		Remedies:  make([]*Remedy, 0),
		Timestamp: time.Now(),
	}
}

// Add appends a remedy to the set.
func (rs *RemedySet) Add(remedy *Remedy) {
	if remedy == nil {
		return
	}
	rs.Remedies = append(rs.Remedies, remedy)
}

// Best returns the highest-confidence remedy, or nil if empty.
func (rs *RemedySet) Best() *Remedy {
	if len(rs.Remedies) == 0 {
		return nil
	}
	rs.sortByConfidence()
	return rs.Remedies[0]
}

// sortByConfidence sorts remedies by confidence descending.
func (rs *RemedySet) sortByConfidence() {
	sort.Slice(rs.Remedies, func(i, j int) bool {
		return rs.Remedies[i].Confidence > rs.Remedies[j].Confidence
	})
}

// All returns all remedies sorted by confidence descending.
func (rs *RemedySet) All() []*Remedy {
	rs.sortByConfidence()
	return rs.Remedies
}

// Count returns the number of remedies in the set.
func (rs *RemedySet) Count() int {
	return len(rs.Remedies)
}

// IsEmpty returns true if the set has no remedies.
func (rs *RemedySet) IsEmpty() bool {
	return len(rs.Remedies) == 0
}
