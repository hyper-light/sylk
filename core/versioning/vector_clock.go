package versioning

type SessionID string

type VectorClock map[SessionID]uint64

func NewVectorClock() VectorClock {
	return make(VectorClock)
}

func (vc VectorClock) Increment(session SessionID) VectorClock {
	result := vc.Clone()
	result[session]++
	return result
}

func (vc VectorClock) Merge(other VectorClock) VectorClock {
	result := vc.Clone()
	for session, count := range other {
		if count > result[session] {
			result[session] = count
		}
	}
	return result
}

func (vc VectorClock) HappensBefore(other VectorClock) bool {
	if vc.bothEmpty(other) {
		return false
	}
	lessOrEqual, strictlyLess := vc.compareEntries(other)
	return lessOrEqual && (strictlyLess || vc.hasNewSessions(other))
}

func (vc VectorClock) bothEmpty(other VectorClock) bool {
	return len(vc) == 0 && len(other) == 0
}

func (vc VectorClock) compareEntries(other VectorClock) (allLessOrEqual, someStrictlyLess bool) {
	allLessOrEqual = true
	for session, count := range vc {
		otherCount := other[session]
		if count > otherCount {
			return false, false
		}
		if count < otherCount {
			someStrictlyLess = true
		}
	}
	return allLessOrEqual, someStrictlyLess
}

func (vc VectorClock) hasNewSessions(other VectorClock) bool {
	for session, count := range other {
		_, exists := vc[session]
		if !exists && count > 0 {
			return true
		}
	}
	return false
}

func (vc VectorClock) Concurrent(other VectorClock) bool {
	return !vc.HappensBefore(other) && !other.HappensBefore(vc)
}

func (vc VectorClock) Clone() VectorClock {
	result := make(VectorClock, len(vc))
	for k, v := range vc {
		result[k] = v
	}
	return result
}

func (vc VectorClock) Get(session SessionID) uint64 {
	return vc[session]
}

func (vc VectorClock) Set(session SessionID, value uint64) VectorClock {
	result := vc.Clone()
	result[session] = value
	return result
}

func (vc VectorClock) IsZero() bool {
	for _, v := range vc {
		if v > 0 {
			return false
		}
	}
	return true
}

func (vc VectorClock) Equal(other VectorClock) bool {
	if len(vc) != len(other) {
		return false
	}
	for session, count := range vc {
		if other[session] != count {
			return false
		}
	}
	return true
}

func (vc VectorClock) Compare(other VectorClock) int {
	if vc.HappensBefore(other) {
		return -1
	}
	if other.HappensBefore(vc) {
		return 1
	}
	if vc.Equal(other) {
		return 0
	}
	return 2
}

func (vc VectorClock) Sessions() []SessionID {
	sessions := make([]SessionID, 0, len(vc))
	for session := range vc {
		sessions = append(sessions, session)
	}
	return sessions
}
