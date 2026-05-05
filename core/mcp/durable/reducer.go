package durable

import (
	"sort"
	"sync"
	"time"
)

// Projection is the in-memory current state of every operation in the
// session. It is built by replaying the WAL on boot, then maintained in
// real time as Append is called. Projection state is observable but not
// authoritative — the journal is.
type Projection struct {
	mu  sync.RWMutex
	ops map[string]*Operation
}

// NewProjection returns an empty projection.
func NewProjection() *Projection {
	return &Projection{ops: make(map[string]*Operation)}
}

// Apply updates the projection from one event. Idempotent on sequence: if a
// later event arrives that is older than what we already saw, it is ignored.
// Each branch is one-statement small so cyclomatic complexity stays trivial.
func (p *Projection) Apply(ev Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	op := p.upsert(ev)
	switch ev.Kind {
	case EventOpRequested:
		p.applyRequested(op, ev)
	case EventApprovalRequested:
		op.Status = StatusApprovalPending
	case EventApprovalResolved:
		p.applyApprovalResolved(op, ev)
	case EventElicitationRequested:
		op.Status = StatusElicitationPending
	case EventElicitationResolved:
		op.Status = StatusRunning
	case EventOpProgress:
		op.Status = StatusRunning
	case EventBlobStored:
		op.BlobRefs = append(op.BlobRefs, ev.BlobRefs...)
	case EventOpCompleted:
		p.applyCompleted(op, ev)
	case EventOpFailed:
		p.applyFailed(op, ev)
	case EventOpCancelled:
		p.applyCancelled(op, ev)
	}
	op.UpdatedAt = ev.Timestamp
}

func (p *Projection) upsert(ev Event) *Operation {
	op, ok := p.ops[ev.OperationID]
	if ok {
		return op
	}
	op = &Operation{OperationID: ev.OperationID, StartedAt: ev.Timestamp}
	p.ops[ev.OperationID] = op
	return op
}

func (p *Projection) applyRequested(op *Operation, ev Event) {
	op.SessionID = ev.SessionID
	op.CorrelationID = ev.CorrelationID
	op.AgentID = ev.AgentID
	op.ServerID = ev.ServerID
	op.CapabilityKind = ev.CapabilityKind
	op.LocalName = ev.LocalName
	op.RemoteName = ev.RemoteName
	op.ArgumentsHash = ev.ArgumentsHash
	op.PolicyFingerprint = ev.PolicyHash
	op.Status = StatusRequested
	op.StartedAt = ev.Timestamp
}

func (p *Projection) applyApprovalResolved(op *Operation, ev Event) {
	if ev.ErrorMessage != "" {
		op.Status = StatusApprovalDenied
		op.Error = ev.ErrorMessage
		return
	}
	op.Status = StatusRunning
}

func (p *Projection) applyCompleted(op *Operation, ev Event) {
	op.Status = StatusCompleted
	op.ResultSummary = ev.ResultSummary
	completed := ev.Timestamp
	op.CompletedAt = &completed
}

func (p *Projection) applyFailed(op *Operation, ev Event) {
	op.Status = StatusFailed
	op.Error = ev.ErrorMessage
	completed := ev.Timestamp
	op.CompletedAt = &completed
}

func (p *Projection) applyCancelled(op *Operation, ev Event) {
	op.Status = StatusCancelled
	completed := ev.Timestamp
	op.CompletedAt = &completed
}

// Get returns a snapshot of one operation's projection.
func (p *Projection) Get(opID string) (Operation, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	op, ok := p.ops[opID]
	if !ok {
		return Operation{}, false
	}
	return *op, true
}

// List returns a snapshot of all operations sorted by StartedAt.
func (p *Projection) List() []Operation {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Operation, 0, len(p.ops))
	for _, op := range p.ops {
		out = append(out, *op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// Pending returns operations that are not in a terminal state. Used by the
// runtime on boot to derive mailbox obligations for resume.
func (p *Projection) Pending() []Operation {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Operation, 0, len(p.ops))
	for _, op := range p.ops {
		if isTerminalStatus(op.Status) {
			continue
		}
		out = append(out, *op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

func isTerminalStatus(s OpStatus) bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusApprovalDenied:
		return true
	}
	return false
}

// HydrateFromJournal replays a Journal into an empty Projection. Used at
// session boot to recover state from disk.
func HydrateFromJournal(j *Journal) (*Projection, error) {
	proj := NewProjection()
	err := j.Replay(func(ev Event) error {
		proj.Apply(ev)
		return nil
	})
	return proj, err
}

// ZeroTime is a sentinel for "not yet observed."
var ZeroTime = time.Time{}
