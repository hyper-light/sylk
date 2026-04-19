package identity

import (
	"fmt"
	"sort"
	"strings"
)

// SelectorOp enumerates the label selector operations we support.
// This is a deliberately minimal subset of the K8s selector spec —
// enough for the accountant and routing views, not the full
// set-based grammar.
type SelectorOp uint8

const (
	// OpExists matches any value for key (including "").
	OpExists SelectorOp = iota
	// OpNotExists matches absence of key.
	OpNotExists
	// OpEquals matches key == value.
	OpEquals
	// OpNotEquals matches key != value (or key absent).
	OpNotEquals
	// OpIn matches key ∈ values.
	OpIn
	// OpNotIn matches key ∉ values (or key absent).
	OpNotIn
)

// Requirement is one predicate in a Selector.
type Requirement struct {
	Key    string
	Op     SelectorOp
	Values []string
}

// Selector is a conjunction of Requirements. Zero-requirement
// selectors match everything.
type Selector struct {
	requirements []Requirement
}

// NewSelector constructs an empty (match-all) Selector. Use Add for
// requirements or the Equals/In/Exists helpers for concise
// construction.
func NewSelector() *Selector {
	return &Selector{}
}

// Equals returns a selector that matches when labels[key] == value.
func Equals(key, value string) *Selector {
	s := NewSelector()
	s.requirements = append(s.requirements, Requirement{
		Key: key, Op: OpEquals, Values: []string{value},
	})
	return s
}

// Exists returns a selector that matches when labels has key.
func Exists(key string) *Selector {
	s := NewSelector()
	s.requirements = append(s.requirements, Requirement{Key: key, Op: OpExists})
	return s
}

// In returns a selector that matches when labels[key] ∈ values.
func In(key string, values ...string) *Selector {
	s := NewSelector()
	s.requirements = append(s.requirements, Requirement{
		Key: key, Op: OpIn, Values: append([]string(nil), values...),
	})
	return s
}

// Add composes another requirement onto the selector and returns
// the receiver for chaining.
func (s *Selector) Add(req Requirement) *Selector {
	s.requirements = append(s.requirements, req)
	return s
}

// Equals appends an equals requirement.
func (s *Selector) Equals(key, value string) *Selector {
	return s.Add(Requirement{Key: key, Op: OpEquals, Values: []string{value}})
}

// NotEquals appends a not-equals requirement.
func (s *Selector) NotEquals(key, value string) *Selector {
	return s.Add(Requirement{Key: key, Op: OpNotEquals, Values: []string{value}})
}

// In appends an in-set requirement.
func (s *Selector) In(key string, values ...string) *Selector {
	return s.Add(Requirement{Key: key, Op: OpIn, Values: append([]string(nil), values...)})
}

// NotIn appends a not-in-set requirement.
func (s *Selector) NotIn(key string, values ...string) *Selector {
	return s.Add(Requirement{Key: key, Op: OpNotIn, Values: append([]string(nil), values...)})
}

// Exists appends an existence requirement.
func (s *Selector) Exists(key string) *Selector {
	return s.Add(Requirement{Key: key, Op: OpExists})
}

// NotExists appends an absence requirement.
func (s *Selector) NotExists(key string) *Selector {
	return s.Add(Requirement{Key: key, Op: OpNotExists})
}

// Matches reports whether labels satisfies every requirement.
func (s *Selector) Matches(labels Labels) bool {
	if s == nil {
		return true
	}
	for _, req := range s.requirements {
		if !requirementMatches(req, labels) {
			return false
		}
	}
	return true
}

// MatchesIdentity reports whether the identity's labels satisfy
// the selector.
func (s *Selector) MatchesIdentity(id *AgentIdentity) bool {
	if id == nil {
		return false
	}
	return s.Matches(id.labels)
}

// MatchesTask reports whether the task's labels satisfy the selector.
func (s *Selector) MatchesTask(task *TaskRef) bool {
	if task == nil {
		return false
	}
	return s.Matches(task.labels)
}

// String renders the selector in K8s selector syntax for log output.
// Zero-requirement selectors render as "<match-all>".
func (s *Selector) String() string {
	if s == nil || len(s.requirements) == 0 {
		return "<match-all>"
	}
	// Sort requirements by key for deterministic rendering.
	reqs := make([]Requirement, len(s.requirements))
	copy(reqs, s.requirements)
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].Key < reqs[j].Key })

	parts := make([]string, 0, len(reqs))
	for _, r := range reqs {
		parts = append(parts, requirementString(r))
	}
	return strings.Join(parts, ",")
}

// requirementMatches is the match primitive.
func requirementMatches(req Requirement, labels Labels) bool {
	switch req.Op {
	case OpExists:
		return labels.Has(req.Key)
	case OpNotExists:
		return !labels.Has(req.Key)
	case OpEquals:
		if len(req.Values) == 0 {
			return false
		}
		return labels.Has(req.Key) && labels.Get(req.Key) == req.Values[0]
	case OpNotEquals:
		if len(req.Values) == 0 {
			return true
		}
		return !labels.Has(req.Key) || labels.Get(req.Key) != req.Values[0]
	case OpIn:
		if !labels.Has(req.Key) {
			return false
		}
		return sliceContains(req.Values, labels.Get(req.Key))
	case OpNotIn:
		if !labels.Has(req.Key) {
			return true
		}
		return !sliceContains(req.Values, labels.Get(req.Key))
	}
	return false
}

// requirementString renders one Requirement in K8s selector syntax.
func requirementString(r Requirement) string {
	switch r.Op {
	case OpExists:
		return r.Key
	case OpNotExists:
		return "!" + r.Key
	case OpEquals:
		if len(r.Values) == 0 {
			return r.Key + "="
		}
		return r.Key + "=" + r.Values[0]
	case OpNotEquals:
		if len(r.Values) == 0 {
			return r.Key + "!="
		}
		return r.Key + "!=" + r.Values[0]
	case OpIn:
		return fmt.Sprintf("%s in (%s)", r.Key, strings.Join(r.Values, ","))
	case OpNotIn:
		return fmt.Sprintf("%s notin (%s)", r.Key, strings.Join(r.Values, ","))
	}
	return "?"
}

func sliceContains(set []string, value string) bool {
	for _, s := range set {
		if s == value {
			return true
		}
	}
	return false
}
