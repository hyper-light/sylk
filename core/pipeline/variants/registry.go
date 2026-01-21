package variants

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type Registry interface {
	Register(ctx context.Context, variant *Variant) error
	Unregister(ctx context.Context, id VariantID) error
	Get(id VariantID) (*Variant, error)
	GetInfo(id VariantID) (*VariantInfo, error)
	List(filter *VariantFilter) []*VariantInfo
	ListActive() []*VariantInfo
	UpdateState(id VariantID, state VariantState) error
	Subscribe(callback VariantEventCallback) func()
	Stats() RegistryStats
	Close() error
}

type MemoryRegistry struct {
	mu         sync.RWMutex
	variants   map[VariantID]*Variant
	callbacks  []VariantEventCallback
	callbackMu sync.RWMutex
	closed     atomic.Bool
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		variants:  make(map[VariantID]*Variant),
		callbacks: make([]VariantEventCallback, 0),
	}
}

func (r *MemoryRegistry) Register(ctx context.Context, variant *Variant) error {
	if r.closed.Load() {
		return ErrRegistryClosed
	}

	if variant == nil {
		return ErrVariantNotFound
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.variants[variant.ID]; exists {
		return ErrVariantAlreadyExists
	}

	r.variants[variant.ID] = variant
	r.emitEvent(VariantEvent{
		Type:      VariantEventCreated,
		VariantID: variant.ID,
		Timestamp: time.Now(),
		NewState:  variant.State,
	})

	return nil
}

func (r *MemoryRegistry) Unregister(ctx context.Context, id VariantID) error {
	if r.closed.Load() {
		return ErrRegistryClosed
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	variant, exists := r.variants[id]
	if !exists {
		return ErrVariantNotFound
	}

	if !variant.State.IsTerminal() {
		return ErrVariantInvalidState
	}

	delete(r.variants, id)
	return nil
}

func (r *MemoryRegistry) Get(id VariantID) (*Variant, error) {
	if r.closed.Load() {
		return nil, ErrRegistryClosed
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	variant, exists := r.variants[id]
	if !exists {
		return nil, ErrVariantNotFound
	}

	return variant, nil
}

func (r *MemoryRegistry) GetInfo(id VariantID) (*VariantInfo, error) {
	if r.closed.Load() {
		return nil, ErrRegistryClosed
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	variant, exists := r.variants[id]
	if !exists {
		return nil, ErrVariantNotFound
	}

	return variant.Info(), nil
}

func (r *MemoryRegistry) List(filter *VariantFilter) []*VariantInfo {
	if r.closed.Load() {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*VariantInfo, 0, len(r.variants))
	for _, v := range r.variants {
		info := v.Info()
		if filter == nil || filter.Matches(info) {
			result = append(result, info)
		}
	}

	return result
}

func (r *MemoryRegistry) ListActive() []*VariantInfo {
	return r.List(&VariantFilter{
		States: []VariantState{VariantStateActive},
	})
}

func (r *MemoryRegistry) ListBySession(sessionID string) []*VariantInfo {
	return r.List(&VariantFilter{
		SessionID: sessionID,
	})
}

func (r *MemoryRegistry) ListByPipeline(pipelineID string) []*VariantInfo {
	return r.List(&VariantFilter{
		BasePipelineID: pipelineID,
	})
}

func (r *MemoryRegistry) UpdateState(id VariantID, newState VariantState) error {
	if r.closed.Load() {
		return ErrRegistryClosed
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	variant, exists := r.variants[id]
	if !exists {
		return ErrVariantNotFound
	}

	oldState := variant.State
	if !oldState.CanTransitionTo(newState) {
		return ErrVariantInvalidState
	}

	variant.State = newState
	variant.UpdatedAt = time.Now()

	r.updateMetricsForStateChange(variant, oldState, newState)
	r.emitEventForStateChange(variant.ID, oldState, newState)

	return nil
}

func (r *MemoryRegistry) updateMetricsForStateChange(variant *Variant, oldState, newState VariantState) {
	if variant.Metrics == nil {
		return
	}

	switch newState {
	case VariantStateActive:
		if oldState == VariantStateCreated {
			variant.Metrics.RecordStart()
		}
	case VariantStateComplete, VariantStateFailed, VariantStateCancelled:
		variant.Metrics.RecordCompletion()
	}
}

func (r *MemoryRegistry) emitEventForStateChange(id VariantID, oldState, newState VariantState) {
	eventType := r.stateChangeToEventType(oldState, newState)

	r.emitEvent(VariantEvent{
		Type:      eventType,
		VariantID: id,
		Timestamp: time.Now(),
		OldState:  oldState,
		NewState:  newState,
	})
}

func (r *MemoryRegistry) stateChangeToEventType(oldState, newState VariantState) VariantEventType {
	switch newState {
	case VariantStateActive:
		if oldState == VariantStateSuspended {
			return VariantEventResumed
		}
		return VariantEventStarted
	case VariantStateSuspended:
		return VariantEventSuspended
	case VariantStateComplete:
		return VariantEventCompleted
	case VariantStateFailed:
		if oldState == VariantStateMerging {
			return VariantEventMergeFailed
		}
		return VariantEventFailed
	case VariantStateMerging:
		return VariantEventMergeStarted
	case VariantStateMerged:
		return VariantEventMergeCompleted
	case VariantStateCancelled:
		return VariantEventCancelled
	default:
		return VariantEventCreated
	}
}

func (r *MemoryRegistry) Subscribe(callback VariantEventCallback) func() {
	r.callbackMu.Lock()
	r.callbacks = append(r.callbacks, callback)
	idx := len(r.callbacks) - 1
	r.callbackMu.Unlock()

	return func() {
		r.callbackMu.Lock()
		defer r.callbackMu.Unlock()
		if idx < len(r.callbacks) {
			r.callbacks = append(r.callbacks[:idx], r.callbacks[idx+1:]...)
		}
	}
}

func (r *MemoryRegistry) emitEvent(event VariantEvent) {
	r.callbackMu.RLock()
	callbacks := make([]VariantEventCallback, len(r.callbacks))
	copy(callbacks, r.callbacks)
	r.callbackMu.RUnlock()

	for _, cb := range callbacks {
		go cb(event)
	}
}

func (r *MemoryRegistry) Stats() RegistryStats {
	if r.closed.Load() {
		return RegistryStats{}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := RegistryStats{
		TotalVariants: len(r.variants),
	}

	for _, v := range r.variants {
		switch v.State {
		case VariantStateActive:
			stats.ActiveVariants++
		case VariantStateSuspended:
			stats.SuspendedVariants++
		case VariantStateComplete:
			stats.CompletedVariants++
		case VariantStateFailed:
			stats.FailedVariants++
		case VariantStateMerged:
			stats.MergedVariants++
		case VariantStateCancelled:
			stats.CancelledVariants++
		}
	}

	return stats
}

func (r *MemoryRegistry) Close() error {
	if r.closed.Swap(true) {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.variants = nil
	r.callbacks = nil

	return nil
}

func (r *MemoryRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.variants)
}

func (r *MemoryRegistry) GetByState(state VariantState) []*Variant {
	if r.closed.Load() {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Variant, 0)
	for _, v := range r.variants {
		if v.State == state {
			result = append(result, v)
		}
	}

	return result
}

func (r *MemoryRegistry) PurgeTerminal(olderThan time.Duration) int {
	if r.closed.Load() {
		return 0
	}

	cutoff := time.Now().Add(-olderThan)

	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0
	for id, v := range r.variants {
		if v.State.IsTerminal() && v.UpdatedAt.Before(cutoff) {
			delete(r.variants, id)
			count++
		}
	}

	return count
}

type Variant struct {
	ID             VariantID
	Name           string
	Description    string
	State          VariantState
	BasePipelineID string
	BaseVersionID  string
	SessionID      string
	AgentID        string
	Priority       VariantPriority
	IsolationLevel IsolationLevel
	ResourceLimits ResourceLimits
	AutoMerge      bool
	Timeout        time.Duration
	Tags           []string
	Metadata       map[string]any
	Metrics        *VariantMetrics
	CreatedAt      time.Time
	UpdatedAt      time.Time

	mu  sync.RWMutex
	vfs any
}

func NewVariant(cfg VariantConfig) (*Variant, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	id := cfg.ID
	if id.IsZero() {
		id = NewVariantID()
	}

	now := time.Now()
	return &Variant{
		ID:             id,
		Name:           cfg.Name,
		Description:    cfg.Description,
		State:          VariantStateCreated,
		BasePipelineID: cfg.BasePipelineID,
		BaseVersionID:  cfg.BaseVersionID,
		SessionID:      cfg.SessionID,
		AgentID:        cfg.AgentID,
		Priority:       cfg.Priority,
		IsolationLevel: cfg.IsolationLevel,
		ResourceLimits: cfg.ResourceLimits,
		AutoMerge:      cfg.AutoMerge,
		Timeout:        cfg.Timeout,
		Tags:           cloneTags(cfg.Tags),
		Metadata:       cloneMetadata(cfg.Metadata),
		Metrics:        NewVariantMetrics(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

func (v *Variant) Info() *VariantInfo {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return &VariantInfo{
		ID:             v.ID,
		Name:           v.Name,
		Description:    v.Description,
		State:          v.State,
		BasePipelineID: v.BasePipelineID,
		SessionID:      v.SessionID,
		AgentID:        v.AgentID,
		Priority:       v.Priority,
		IsolationLevel: v.IsolationLevel,
		Metrics:        v.Metrics.Clone(),
		Tags:           cloneTags(v.Tags),
		CreatedAt:      v.CreatedAt,
		UpdatedAt:      v.UpdatedAt,
	}
}

func (v *Variant) Summary() *VariantSummary {
	v.mu.RLock()
	defer v.mu.RUnlock()

	duration := time.Duration(0)
	if v.Metrics != nil {
		duration = v.Metrics.Duration
	}

	return &VariantSummary{
		ID:        v.ID,
		Name:      v.Name,
		State:     v.State,
		CreatedAt: v.CreatedAt,
		Duration:  duration,
	}
}

func (v *Variant) SetVFS(vfs any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.vfs = vfs
}

func (v *Variant) GetVFS() any {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.vfs
}

func (v *Variant) HasTag(tag string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()

	for _, t := range v.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

func (v *Variant) AddTag(tag string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	for _, t := range v.Tags {
		if t == tag {
			return
		}
	}
	v.Tags = append(v.Tags, tag)
	v.UpdatedAt = time.Now()
}

func (v *Variant) RemoveTag(tag string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	for i, t := range v.Tags {
		if t == tag {
			v.Tags = append(v.Tags[:i], v.Tags[i+1:]...)
			v.UpdatedAt = time.Now()
			return
		}
	}
}

func (v *Variant) SetMetadata(key string, value any) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.Metadata == nil {
		v.Metadata = make(map[string]any)
	}
	v.Metadata[key] = value
	v.UpdatedAt = time.Now()
}

func (v *Variant) GetMetadata(key string) (any, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.Metadata == nil {
		return nil, false
	}
	val, ok := v.Metadata[key]
	return val, ok
}

func cloneTags(tags []string) []string {
	if tags == nil {
		return nil
	}
	result := make([]string, len(tags))
	copy(result, tags)
	return result
}

func cloneMetadata(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
