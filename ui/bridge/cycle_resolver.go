package bridge

import (
	"strings"
	"sync"
)

// cycleResolver computes per-cycle membership from board state and
// real-time artifact emissions. The bridge owns one resolver per
// session; SwitchSession resets it.
//
// Cycles open when an agent posts an unparented top-level claim or
// becomes the successor of a handoff. They stay open while the owner
// has open subject claims OR in-flight (started-but-not-completed)
// artifacts in the cycle's transitive subtree. They close on full
// drain. UI_DESIGN.md §2.1, §5.
//
// The resolver is the single component that walks Relations. The UI
// (chat panel, agent panel) consumes only the derived messages and
// never inspects relations directly.
type cycleResolver struct {
	mu sync.Mutex

	// cycles maps cycle root claim ID → cycle state.
	cycles map[string]*cycleState

	// agentCycle maps owner agent ID → currently active cycle root
	// claim ID. One active cycle per agent at a time.
	agentCycle map[string]string

	// claimCycle maps every claim ID seen → the cycle root it belongs
	// to. Lets the resolver attribute artifacts and child claims
	// without re-walking caused_by.
	claimCycle map[string]string

	// claimParent maps claim ID → its caused_by parent claim ID (one
	// hop). Used to walk to the cycle root on first encounter.
	claimParent map[string]string

	// claimOwner maps claim ID → owner (issuer) agent ID for fast
	// lookup at completion / drain time.
	claimOwner map[string]string

	// claimSubject maps claim ID → subject agent ID.
	claimSubject map[string]string

	// startedArtifact maps started artifact ID → the cycle it lives
	// in. Used by the artifact completion path to confirm pairing.
	startedArtifact map[string]string

	// completedStarted records started artifact IDs that have already
	// been paired with a completion. Used to dedup if a completion
	// artifact arrives via two paths (sink + testament replay).
	completedStarted map[string]struct{}

	// pendingChildren maps an unknown parent claim ID → the cycle
	// resolution defers until that parent appears (out-of-order
	// delivery; see UI_DESIGN.md §7 P4.1 edge case).
	pendingChildren map[string][]string

	// pendingArtifacts maps an unknown claim ID → started artifact IDs
	// that arrived before the claim was registered with the resolver.
	// The artifact-emission path (ArtifactProgressSink → bridge) is
	// synchronous from the agent's accumulator, while claim_created
	// notifications fire on a separate path; nothing guarantees ordering
	// when the bridge auto-adopts a session mid-flight or when the very
	// first artifact arrives before the bridge's projection replay
	// finishes. Without this queue, the artifact is dropped silently
	// (the original behavior produced "no tool calls visible" in the
	// chat panel even though agents were doing real work). Flushed by
	// onClaimCreated when the missing claim finally registers.
	pendingArtifacts map[string][]string
}

func newCycleResolver() *cycleResolver {
	return &cycleResolver{
		cycles:           make(map[string]*cycleState),
		agentCycle:       make(map[string]string),
		claimCycle:       make(map[string]string),
		claimParent:      make(map[string]string),
		claimOwner:       make(map[string]string),
		claimSubject:     make(map[string]string),
		startedArtifact:  make(map[string]string),
		completedStarted: make(map[string]struct{}),
		pendingChildren:  make(map[string][]string),
		pendingArtifacts: make(map[string][]string),
	}
}

// cycleState tracks the open work + in-flight artifacts of a single
// cycle. A cycle closes when both maps are empty (no open subject
// claims AND no in-flight artifacts).
type cycleState struct {
	CycleID      string
	OwnerAgentID string

	// openClaims is the set of open claims that belong to this cycle.
	// Includes the root claim plus every claim transitively caused_by
	// a claim already in the cycle.
	openClaims map[string]struct{}

	// inFlightArtifacts is the set of started artifact IDs without a
	// matching completed artifact yet seen.
	inFlightArtifacts map[string]struct{}
}

func newCycleState(cycleID, owner string) *cycleState {
	return &cycleState{
		CycleID:           cycleID,
		OwnerAgentID:      owner,
		openClaims:        make(map[string]struct{}),
		inFlightArtifacts: make(map[string]struct{}),
	}
}

func (c *cycleState) drained() bool {
	return len(c.openClaims) == 0 && len(c.inFlightArtifacts) == 0
}

// claimCreatedOutcome captures every cycle-state change a single new
// claim can trigger. The bridge consumes this to decide which
// ClaimsAgentStatusMsg events to emit. Maximally explicit so the
// resolver never returns implicit signals through side channels.
type claimCreatedOutcome struct {
	// CycleOpened is the new cycle root claim if this claim opened a
	// fresh cycle (top-level OR handoff successor). Nil otherwise.
	CycleOpened *cycleState
	// PredecessorClosed is the predecessor cycle if a handoff caused
	// it to close. Nil unless this claim carried handoff_from.
	PredecessorClosed *cycleState
	// AttachedToCycle is the existing cycle the claim joined as a
	// child via caused_by. Nil for top-level / handoff successor.
	AttachedToCycle *cycleState
}

// onClaimCreated registers a new claim with the resolver. The
// returned claimCreatedOutcome is the complete set of cycle-state
// transitions the claim caused.
//
// The caller passes the resolved owner + subject + caused_by parent +
// handoff_from predecessor (read from c.Relations on its side); the
// resolver does not re-walk relations.
func (r *cycleResolver) onClaimCreated(claimID, owner, subject, causedByParent, handoffFrom string) claimCreatedOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.claimOwner[claimID] = owner
	r.claimSubject[claimID] = subject
	if causedByParent != "" {
		r.claimParent[claimID] = causedByParent
	}

	switch {
	case handoffFrom != "":
		// Successor cycle of a handoff. Per UI_DESIGN.md §2.2 +
		// §5.2 (close-immediately rule): the handoff IS the
		// predecessor's terminal event for cycle-ownership purposes.
		// Close the predecessor cycle immediately, regardless of its
		// root claim status — HandoffEligible already proved zero
		// open child work + zero in-flight artifacts at handoff time,
		// so the only thing left was the root claim, and the handoff
		// itself is the agent's signal that they're done with it.
		predecessorClosed := r.closePredecessorOnHandoffLocked(handoffFrom)
		st := newCycleState(claimID, owner)
		st.openClaims[claimID] = struct{}{}
		r.cycles[claimID] = st
		r.claimCycle[claimID] = claimID
		if owner != "" {
			r.agentCycle[owner] = claimID
		}
		r.flushPendingChildrenLocked(claimID)
		return claimCreatedOutcome{CycleOpened: st, PredecessorClosed: predecessorClosed}

	case causedByParent != "":
		parentCycle, known := r.claimCycle[causedByParent]
		if !known {
			// Parent not yet seen — defer attribution. When the parent
			// arrives, we'll walk pendingChildren to attach.
			r.pendingChildren[causedByParent] = append(r.pendingChildren[causedByParent], claimID)
			return claimCreatedOutcome{}
		}
		st, ok := r.cycles[parentCycle]
		if !ok {
			// Parent's cycle was closed. Treat the child as a fresh
			// top-level cycle to avoid orphan rendering.
			st = newCycleState(claimID, owner)
			r.cycles[claimID] = st
			r.claimCycle[claimID] = claimID
			if owner != "" {
				r.agentCycle[owner] = claimID
			}
			st.openClaims[claimID] = struct{}{}
			r.flushPendingChildrenLocked(claimID)
			return claimCreatedOutcome{CycleOpened: st}
		}
		st.openClaims[claimID] = struct{}{}
		r.claimCycle[claimID] = parentCycle
		r.flushPendingChildrenLocked(claimID)
		return claimCreatedOutcome{AttachedToCycle: st}

	default:
		// Unparented top-level claim — opens a fresh cycle.
		st := newCycleState(claimID, owner)
		st.openClaims[claimID] = struct{}{}
		r.cycles[claimID] = st
		r.claimCycle[claimID] = claimID
		if owner != "" {
			r.agentCycle[owner] = claimID
		}
		r.flushPendingChildrenLocked(claimID)
		return claimCreatedOutcome{CycleOpened: st}
	}
}

// closePredecessorOnHandoffLocked terminates the predecessor cycle
// immediately on handoff, returning a snapshot of the closed cycle so
// the bridge can emit ClaimsAgentStatusMsg{Active=false}. Idempotent:
// returns nil when the predecessor cycle was already torn down or
// never registered. Caller holds mu.
func (r *cycleResolver) closePredecessorOnHandoffLocked(predecessorClaimID string) *cycleState {
	predecessorCycleID, ok := r.claimCycle[predecessorClaimID]
	if !ok {
		// Predecessor's cycle wasn't seen — nothing to close. The
		// handoff still proceeds (successor cycle opens normally).
		return nil
	}
	st, ok := r.cycles[predecessorCycleID]
	if !ok {
		return nil
	}
	// Snapshot the cycle for return BEFORE teardown so the caller has
	// the OwnerAgentID + CycleID for the demote message.
	snapshot := &cycleState{
		CycleID:           st.CycleID,
		OwnerAgentID:      st.OwnerAgentID,
		openClaims:        nil, // forced-closed; do not surface stale set
		inFlightArtifacts: nil,
	}
	r.teardownCycleLocked(predecessorCycleID)
	return snapshot
}

// flushPendingChildrenLocked attaches any children that arrived
// before their parent. Caller holds mu.
func (r *cycleResolver) flushPendingChildrenLocked(parentID string) {
	pending, ok := r.pendingChildren[parentID]
	if !ok {
		return
	}
	delete(r.pendingChildren, parentID)
	parentCycle, ok := r.claimCycle[parentID]
	if !ok {
		return
	}
	st, ok := r.cycles[parentCycle]
	if !ok {
		return
	}
	for _, childID := range pending {
		st.openClaims[childID] = struct{}{}
		r.claimCycle[childID] = parentCycle
		// Recursively flush in case a grandchild arrived before this
		// child. flushPendingChildrenLocked is safe to recurse into:
		// the parent map shrinks each call.
		r.flushPendingChildrenLocked(childID)
	}
}

// onClaimClosed marks a claim terminal. Returns the cycle if the
// closure caused the cycle to drain, so the bridge can emit
// ClaimsAgentStatusMsg{Active=false}.
func (r *cycleResolver) onClaimClosed(claimID string) (drainedCycle *cycleState) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cycleID, ok := r.claimCycle[claimID]
	if !ok {
		return nil
	}
	st, ok := r.cycles[cycleID]
	if !ok {
		return nil
	}
	delete(st.openClaims, claimID)
	if !st.drained() {
		return nil
	}
	// Cycle drained. Tear down state for this cycle.
	r.teardownCycleLocked(cycleID)
	return st
}

// onArtifactStarted records a *_started artifact in the resolver and
// returns the cycle it belongs to (if known). When the artifact's
// claim hasn't been seen yet, the artifact is queued onto
// pendingArtifacts and flushed once the claim registers via
// onClaimCreated. Returns nil in that case so the caller knows to
// defer routing to the UI; the bridge will route the queued
// artifact when flushPendingArtifactsLocked fires.
func (r *cycleResolver) onArtifactStarted(artifactID, claimID string) (cycle *cycleState) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cycleID, ok := r.claimCycle[claimID]
	if !ok {
		// Claim not yet registered — queue and flush on registration.
		r.pendingArtifacts[claimID] = append(r.pendingArtifacts[claimID], artifactID)
		return nil
	}
	st, ok := r.cycles[cycleID]
	if !ok {
		r.pendingArtifacts[claimID] = append(r.pendingArtifacts[claimID], artifactID)
		return nil
	}
	st.inFlightArtifacts[artifactID] = struct{}{}
	r.startedArtifact[artifactID] = cycleID
	return st
}

// drainPendingArtifactsLocked extracts and registers any started
// artifacts that were queued for claimID. Returns the (artifactID,
// cycleState) pairs the caller should use to emit ClaimArtifactAddedMsg
// for each one. Caller holds mu.
func (r *cycleResolver) drainPendingArtifactsLocked(claimID string) []pendingArtifactFlush {
	pending, ok := r.pendingArtifacts[claimID]
	if !ok || len(pending) == 0 {
		return nil
	}
	delete(r.pendingArtifacts, claimID)
	cycleID, known := r.claimCycle[claimID]
	if !known {
		return nil
	}
	st, ok := r.cycles[cycleID]
	if !ok {
		return nil
	}
	out := make([]pendingArtifactFlush, 0, len(pending))
	for _, artifactID := range pending {
		st.inFlightArtifacts[artifactID] = struct{}{}
		r.startedArtifact[artifactID] = cycleID
		out = append(out, pendingArtifactFlush{ArtifactID: artifactID, Cycle: st})
	}
	return out
}

// pendingArtifactFlush is the (artifactID, cycle) pair returned by
// drainPendingArtifactsLocked so the bridge can emit the deferred
// ClaimArtifactAddedMsg now that the claim is known.
type pendingArtifactFlush struct {
	ArtifactID string
	Cycle      *cycleState
}

// onArtifactCompleted pairs a completion artifact with its started
// counterpart. Returns the cycle if the pairing caused the cycle to
// drain, so the bridge can emit ClaimsAgentStatusMsg{Active=false}.
// Returns the started artifact ID for routing the
// ClaimArtifactCompletedMsg even when the cycle did not drain.
func (r *cycleResolver) onArtifactCompleted(startedArtifactID string) (cycle *cycleState, drained bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, dup := r.completedStarted[startedArtifactID]; dup {
		return nil, false
	}
	r.completedStarted[startedArtifactID] = struct{}{}

	cycleID, ok := r.startedArtifact[startedArtifactID]
	if !ok {
		// Started artifact never registered — orphan completion. No
		// cycle to attribute, but the bridge still emits the
		// ClaimArtifactCompletedMsg so any UI row will close.
		return nil, false
	}
	st, ok := r.cycles[cycleID]
	if !ok {
		return nil, false
	}
	delete(st.inFlightArtifacts, startedArtifactID)
	delete(r.startedArtifact, startedArtifactID)
	if !st.drained() {
		return st, false
	}
	r.teardownCycleLocked(cycleID)
	return st, true
}

// teardownCycleLocked removes a fully-drained cycle's state. Caller
// holds mu.
func (r *cycleResolver) teardownCycleLocked(cycleID string) {
	st := r.cycles[cycleID]
	if st == nil {
		return
	}
	delete(r.cycles, cycleID)
	if owner := st.OwnerAgentID; owner != "" && r.agentCycle[owner] == cycleID {
		delete(r.agentCycle, owner)
	}
	for cid, attached := range r.claimCycle {
		if attached == cycleID {
			delete(r.claimCycle, cid)
			delete(r.claimOwner, cid)
			delete(r.claimSubject, cid)
			delete(r.claimParent, cid)
		}
	}
	for aid, c := range r.startedArtifact {
		if c == cycleID {
			delete(r.startedArtifact, aid)
			delete(r.completedStarted, aid)
		}
	}
}

// Reset clears all resolver state. Called on session switch.
func (r *cycleResolver) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cycles = make(map[string]*cycleState)
	r.agentCycle = make(map[string]string)
	r.claimCycle = make(map[string]string)
	r.claimParent = make(map[string]string)
	r.claimOwner = make(map[string]string)
	r.claimSubject = make(map[string]string)
	r.startedArtifact = make(map[string]string)
	r.completedStarted = make(map[string]struct{})
	r.pendingChildren = make(map[string][]string)
	r.pendingArtifacts = make(map[string][]string)
}

// CycleForClaim returns the cycle root claim ID for the given claim,
// or empty string if unknown.
func (r *cycleResolver) CycleForClaim(claimID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.claimCycle[strings.TrimSpace(claimID)]
}

// CycleForArtifact returns the cycle root claim ID for a started
// artifact, or empty if unknown.
func (r *cycleResolver) CycleForArtifact(startedArtifactID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.startedArtifact[strings.TrimSpace(startedArtifactID)]
}
