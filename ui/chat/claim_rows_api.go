package chat

// ClaimRowByCycleID returns a snapshot of the cycle row for the given
// CycleID, or nil when no row exists. Returned struct is a deep-enough
// copy that callers may inspect freely without locking; mutations are
// not reflected back into the chat state.
func (m *Model) ClaimRowByCycleID(cycleID string) *ClaimRow {
	if m == nil || m.claimRows == nil {
		return nil
	}
	r := m.claimRows.getClaimRow(cycleID)
	if r == nil {
		return nil
	}
	return cloneClaimRow(r)
}

// ArtifactRowByID returns a snapshot of the artifact row keyed by
// ArtifactID, or nil when no row exists. Same snapshot semantics as
// ClaimRowByCycleID.
func (m *Model) ArtifactRowByID(artifactID string) *ArtifactRow {
	if m == nil || m.claimRows == nil {
		return nil
	}
	r := m.claimRows.getArtifactRow(artifactID)
	if r == nil {
		return nil
	}
	return cloneArtifactRow(r)
}

// TestamentRowByID returns a snapshot of the testament row keyed by
// either AccumulatorID (in-flight) or TestamentID (sealed). Returns
// nil when no row exists under either key.
func (m *Model) TestamentRowByID(id string) *TestamentRow {
	if m == nil || m.claimRows == nil {
		return nil
	}
	r := m.claimRows.getTestamentRowByAnyID(id)
	if r == nil {
		return nil
	}
	return cloneTestamentRow(r)
}

// AccumulatorRowByID returns the in-flight testament row keyed
// strictly by AccumulatorID. Distinct from TestamentRowByID — this
// is the spec-named accessor for the pre-flush rebind anchor.
func (m *Model) AccumulatorRowByID(accumulatorID string) *TestamentRow {
	if m == nil || m.claimRows == nil {
		return nil
	}
	r := m.claimRows.getAccumulatorRow(accumulatorID)
	if r == nil {
		return nil
	}
	return cloneTestamentRow(r)
}

// SealedTestamentRowByID returns the sealed testament row keyed
// strictly by TestamentID. Distinct from TestamentRowByID — this is
// the spec-named accessor for the post-flush rebind anchor.
func (m *Model) SealedTestamentRowByID(testamentID string) *TestamentRow {
	if m == nil || m.claimRows == nil {
		return nil
	}
	r := m.claimRows.getTestamentRow(testamentID)
	if r == nil {
		return nil
	}
	return cloneTestamentRow(r)
}

func cloneClaimRow(r *ClaimRow) *ClaimRow {
	if r == nil {
		return nil
	}
	out := *r
	if len(r.Artifacts) > 0 {
		out.Artifacts = append([]string(nil), r.Artifacts...)
	}
	return &out
}

func cloneArtifactRow(r *ArtifactRow) *ArtifactRow {
	if r == nil {
		return nil
	}
	out := *r
	if len(r.Children) > 0 {
		out.Children = append([]string(nil), r.Children...)
	}
	if len(r.Metadata) > 0 {
		md := make(map[string]any, len(r.Metadata))
		for k, v := range r.Metadata {
			md[k] = v
		}
		out.Metadata = md
	}
	return &out
}

func cloneTestamentRow(r *TestamentRow) *TestamentRow {
	if r == nil {
		return nil
	}
	out := *r
	return &out
}
