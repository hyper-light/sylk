package shared

import "strings"

// ThoughtEmitter batches thought deltas and emits cumulative snapshots only
// when warranted, reducing noisy progress churn while preserving liveness.
type ThoughtEmitter struct {
	buffer      strings.Builder
	lastEmitLen int
	enabled     bool
}

func NewThoughtEmitter(enabled bool) ThoughtEmitter {
	return ThoughtEmitter{enabled: enabled}
}

func (e *ThoughtEmitter) AddDelta(delta string) string {
	if delta == "" || !e.enabled {
		if delta != "" {
			e.buffer.WriteString(delta)
		}
		return ""
	}
	e.buffer.WriteString(delta)
	if !e.shouldEmit(delta) {
		return ""
	}
	e.lastEmitLen = e.buffer.Len()
	return strings.TrimSpace(e.buffer.String())
}

func (e *ThoughtEmitter) Flush() string {
	if !e.enabled || e.buffer.Len() == 0 || e.buffer.Len() == e.lastEmitLen {
		return ""
	}
	e.lastEmitLen = e.buffer.Len()
	return strings.TrimSpace(e.buffer.String())
}

func (e *ThoughtEmitter) shouldEmit(delta string) bool {
	return ContainsSentenceBoundary(delta)
}

func ContainsSentenceBoundary(s string) bool {
	for _, ch := range s {
		switch ch {
		case '.', '!', '?', '\n':
			return true
		}
	}
	return false
}
