package versioning

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrResolverNotReady   = errors.New("resolver not ready")
	ErrResolutionTimeout  = errors.New("resolution timeout")
	ErrNoResolutionChosen = errors.New("no resolution chosen")
	ErrInvalidResolution  = errors.New("invalid resolution")
	ErrBatchEmpty         = errors.New("empty conflict batch")
)

// AutoResolvePolicy defines automatic resolution behavior.
type AutoResolvePolicy int

const (
	AutoResolvePolicyNone       AutoResolvePolicy = iota // Always prompt user
	AutoResolvePolicyKeepNewest                          // Keep most recent
	AutoResolvePolicyKeepOldest                          // Keep oldest
	AutoResolvePolicyKeepBoth                            // Merge both changes
)

var autoResolvePolicyNames = map[AutoResolvePolicy]string{
	AutoResolvePolicyNone:       "none",
	AutoResolvePolicyKeepNewest: "keep_newest",
	AutoResolvePolicyKeepOldest: "keep_oldest",
	AutoResolvePolicyKeepBoth:   "keep_both",
}

func (p AutoResolvePolicy) String() string {
	if name, ok := autoResolvePolicyNames[p]; ok {
		return name
	}
	return "unknown"
}

// ConflictResolver handles conflict resolution UI.
type ConflictResolver interface {
	ResolveConflict(ctx context.Context, conflict *Conflict) (*Resolution, error)
	ResolveConflictBatch(ctx context.Context, conflicts []*Conflict) ([]*Resolution, error)
	AutoResolve(conflict *Conflict, policy AutoResolvePolicy) (*Resolution, bool)
}

// ConflictMessage is sent to user via Guide.
type ConflictMessage struct {
	Conflict   *Conflict
	FilePath   string
	SessionID  string
	PipelineID string
	Options    []ConflictOption
}

// ConflictOption represents a resolution choice.
type ConflictOption struct {
	ID          string
	Label       string
	Description string
	Preview     string
}

// ConflictResponse from user.
type ConflictResponse struct {
	ChosenOptionID string
	CustomMerge    []byte
}

// ConflictMessageSender sends conflict messages to users.
type ConflictMessageSender interface {
	SendConflictMessage(ctx context.Context, msg *ConflictMessage) error
	ReceiveResponse(ctx context.Context, conflictID string) (*ConflictResponse, error)
}

// GuideConflictResolver routes conflicts through Guide to user.
type GuideConflictResolver struct {
	sender  ConflictMessageSender
	policy  AutoResolvePolicy
	timeout time.Duration
	mu      sync.RWMutex
}

// GuideConflictResolverConfig configures the resolver.
type GuideConflictResolverConfig struct {
	Sender  ConflictMessageSender
	Policy  AutoResolvePolicy
	Timeout time.Duration
}

// NewGuideConflictResolver creates a new Guide-based conflict resolver.
func NewGuideConflictResolver(cfg GuideConflictResolverConfig) *GuideConflictResolver {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	return &GuideConflictResolver{
		sender:  cfg.Sender,
		policy:  cfg.Policy,
		timeout: timeout,
	}
}

// ResolveConflict presents conflict to user and returns chosen resolution.
func (r *GuideConflictResolver) ResolveConflict(ctx context.Context, conflict *Conflict) (*Resolution, error) {
	if conflict == nil {
		return nil, ErrInvalidResolution
	}

	if resolution, ok := r.AutoResolve(conflict, r.policy); ok {
		return resolution, nil
	}

	return r.resolveViaUser(ctx, conflict)
}

func (r *GuideConflictResolver) resolveViaUser(ctx context.Context, conflict *Conflict) (*Resolution, error) {
	if r.sender == nil {
		return nil, ErrResolverNotReady
	}

	msg := r.buildMessage(conflict)
	if err := r.sender.SendConflictMessage(ctx, msg); err != nil {
		return nil, err
	}

	return r.waitForResponse(ctx, conflict)
}

func (r *GuideConflictResolver) buildMessage(conflict *Conflict) *ConflictMessage {
	options := make([]ConflictOption, len(conflict.Resolutions))
	for i, res := range conflict.Resolutions {
		options[i] = r.buildOption(i, res)
	}

	return &ConflictMessage{
		Conflict: conflict,
		FilePath: r.extractFilePath(conflict),
		Options:  options,
	}
}

func (r *GuideConflictResolver) buildOption(index int, res Resolution) ConflictOption {
	return ConflictOption{
		ID:          r.optionID(index),
		Label:       res.Label,
		Description: res.Description,
		Preview:     r.buildPreview(res),
	}
}

func (r *GuideConflictResolver) optionID(index int) string {
	return "option_" + itoa(index)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	return itoaRecursive(i, "")
}

func itoaRecursive(i int, acc string) string {
	if i == 0 {
		return acc
	}
	digit := byte('0' + i%10)
	return itoaRecursive(i/10, string(digit)+acc)
}

func (r *GuideConflictResolver) buildPreview(res Resolution) string {
	if res.ResultOp == nil {
		return ""
	}
	return string(res.ResultOp.Content)
}

func (r *GuideConflictResolver) extractFilePath(conflict *Conflict) string {
	if conflict.Op1 != nil {
		return conflict.Op1.FilePath
	}
	if conflict.Op2 != nil {
		return conflict.Op2.FilePath
	}
	return ""
}

func (r *GuideConflictResolver) waitForResponse(ctx context.Context, conflict *Conflict) (*Resolution, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	conflictID := r.conflictID(conflict)
	resp, err := r.sender.ReceiveResponse(ctx, conflictID)
	if err != nil {
		return nil, r.wrapTimeoutError(err)
	}

	return r.mapResponse(resp, conflict)
}

func (r *GuideConflictResolver) conflictID(conflict *Conflict) string {
	if conflict.Op1 != nil {
		return conflict.Op1.ID.String()
	}
	return ""
}

func (r *GuideConflictResolver) wrapTimeoutError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrResolutionTimeout
	}
	return err
}

func (r *GuideConflictResolver) mapResponse(resp *ConflictResponse, conflict *Conflict) (*Resolution, error) {
	if resp.CustomMerge != nil {
		return r.buildCustomResolution(resp.CustomMerge, conflict), nil
	}

	return r.findResolutionByID(resp.ChosenOptionID, conflict)
}

func (r *GuideConflictResolver) buildCustomResolution(content []byte, conflict *Conflict) *Resolution {
	var baseOp *Operation
	if conflict.Op1 != nil {
		baseOp = conflict.Op1
	} else if conflict.Op2 != nil {
		baseOp = conflict.Op2
	}

	resultOp := r.createMergedOp(baseOp, content)

	return &Resolution{
		Label:       "Custom merge",
		Description: "User-provided resolution",
		ResultOp:    resultOp,
	}
}

func (r *GuideConflictResolver) createMergedOp(baseOp *Operation, content []byte) *Operation {
	if baseOp == nil {
		return &Operation{Content: content}
	}

	merged := baseOp.Clone()
	merged.Content = content
	return &merged
}

func (r *GuideConflictResolver) findResolutionByID(id string, conflict *Conflict) (*Resolution, error) {
	for i, res := range conflict.Resolutions {
		if r.optionID(i) == id {
			return &res, nil
		}
	}
	return nil, ErrNoResolutionChosen
}

// ResolveConflictBatch presents multiple conflicts.
func (r *GuideConflictResolver) ResolveConflictBatch(ctx context.Context, conflicts []*Conflict) ([]*Resolution, error) {
	if len(conflicts) == 0 {
		return nil, ErrBatchEmpty
	}

	resolutions := make([]*Resolution, 0, len(conflicts))
	for _, conflict := range conflicts {
		res, err := r.ResolveConflict(ctx, conflict)
		if err != nil {
			return resolutions, err
		}
		resolutions = append(resolutions, res)
	}

	return resolutions, nil
}

// AutoResolve attempts automatic resolution based on policy.
func (r *GuideConflictResolver) AutoResolve(conflict *Conflict, policy AutoResolvePolicy) (*Resolution, bool) {
	if conflict == nil || policy == AutoResolvePolicyNone {
		return nil, false
	}

	return r.applyPolicy(conflict, policy)
}

func (r *GuideConflictResolver) applyPolicy(conflict *Conflict, policy AutoResolvePolicy) (*Resolution, bool) {
	switch policy {
	case AutoResolvePolicyKeepNewest:
		return r.resolveKeepNewest(conflict)
	case AutoResolvePolicyKeepOldest:
		return r.resolveKeepOldest(conflict)
	case AutoResolvePolicyKeepBoth:
		return r.resolveKeepBoth(conflict)
	default:
		return nil, false
	}
}

func (r *GuideConflictResolver) resolveKeepNewest(conflict *Conflict) (*Resolution, bool) {
	if !r.canCompareTimestamps(conflict) {
		return nil, false
	}

	newer := r.selectNewer(conflict)
	return &Resolution{
		Label:       "Keep newest",
		Description: "Automatically selected most recent change",
		ResultOp:    newer,
	}, true
}

func (r *GuideConflictResolver) canCompareTimestamps(conflict *Conflict) bool {
	return conflict.Op1 != nil && conflict.Op2 != nil
}

func (r *GuideConflictResolver) selectNewer(conflict *Conflict) *Operation {
	if conflict.Op1.Timestamp.After(conflict.Op2.Timestamp) {
		return conflict.Op1
	}
	return conflict.Op2
}

func (r *GuideConflictResolver) resolveKeepOldest(conflict *Conflict) (*Resolution, bool) {
	if !r.canCompareTimestamps(conflict) {
		return nil, false
	}

	older := r.selectOlder(conflict)
	return &Resolution{
		Label:       "Keep oldest",
		Description: "Automatically selected oldest change",
		ResultOp:    older,
	}, true
}

func (r *GuideConflictResolver) selectOlder(conflict *Conflict) *Operation {
	if conflict.Op1.Timestamp.Before(conflict.Op2.Timestamp) {
		return conflict.Op1
	}
	return conflict.Op2
}

func (r *GuideConflictResolver) resolveKeepBoth(conflict *Conflict) (*Resolution, bool) {
	if conflict.Type == ConflictTypeDeleteEdit {
		return nil, false
	}

	merged := r.mergeOperations(conflict)
	if merged == nil {
		return nil, false
	}

	return &Resolution{
		Label:       "Keep both",
		Description: "Automatically merged both changes",
		ResultOp:    merged,
	}, true
}

func (r *GuideConflictResolver) mergeOperations(conflict *Conflict) *Operation {
	if !r.canMergeOps(conflict) {
		return nil
	}

	merged := conflict.Op1.Clone()
	merged.Content = append(merged.Content, conflict.Op2.Content...)
	return &merged
}

func (r *GuideConflictResolver) canMergeOps(conflict *Conflict) bool {
	if conflict.Op1 == nil || conflict.Op2 == nil {
		return false
	}
	return conflict.Op1.Type == OpInsert && conflict.Op2.Type == OpInsert
}

// SetTimeout updates the resolution timeout.
func (r *GuideConflictResolver) SetTimeout(timeout time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timeout = timeout
}

// SetPolicy updates the auto-resolve policy.
func (r *GuideConflictResolver) SetPolicy(policy AutoResolvePolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policy = policy
}

// NoOpConflictResolver always returns the first resolution option.
type NoOpConflictResolver struct{}

// NewNoOpConflictResolver creates a no-op resolver for testing.
func NewNoOpConflictResolver() *NoOpConflictResolver {
	return &NoOpConflictResolver{}
}

// ResolveConflict returns the first resolution option.
func (r *NoOpConflictResolver) ResolveConflict(_ context.Context, conflict *Conflict) (*Resolution, error) {
	if conflict == nil || len(conflict.Resolutions) == 0 {
		return nil, ErrInvalidResolution
	}
	return &conflict.Resolutions[0], nil
}

// ResolveConflictBatch resolves each conflict with first option.
func (r *NoOpConflictResolver) ResolveConflictBatch(ctx context.Context, conflicts []*Conflict) ([]*Resolution, error) {
	if len(conflicts) == 0 {
		return nil, ErrBatchEmpty
	}

	resolutions := make([]*Resolution, len(conflicts))
	for i, conflict := range conflicts {
		res, err := r.ResolveConflict(ctx, conflict)
		if err != nil {
			return nil, err
		}
		resolutions[i] = res
	}
	return resolutions, nil
}

// AutoResolve always returns false (no auto-resolve).
func (r *NoOpConflictResolver) AutoResolve(_ *Conflict, _ AutoResolvePolicy) (*Resolution, bool) {
	return nil, false
}

// MockConflictMessageSender is a test double for ConflictMessageSender.
type MockConflictMessageSender struct {
	mu        sync.Mutex
	messages  []*ConflictMessage
	responses map[string]*ConflictResponse
	sendErr   error
	recvErr   error
}

// NewMockConflictMessageSender creates a mock sender.
func NewMockConflictMessageSender() *MockConflictMessageSender {
	return &MockConflictMessageSender{
		messages:  make([]*ConflictMessage, 0),
		responses: make(map[string]*ConflictResponse),
	}
}

// SendConflictMessage records the message.
func (m *MockConflictMessageSender) SendConflictMessage(_ context.Context, msg *ConflictMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendErr != nil {
		return m.sendErr
	}
	m.messages = append(m.messages, msg)
	return nil
}

// ReceiveResponse returns a pre-configured response.
func (m *MockConflictMessageSender) ReceiveResponse(ctx context.Context, conflictID string) (*ConflictResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recvErr != nil {
		return nil, m.recvErr
	}
	if resp, ok := m.responses[conflictID]; ok {
		return resp, nil
	}
	return nil, ErrNoResolutionChosen
}

// SetResponse configures a response for a conflict ID.
func (m *MockConflictMessageSender) SetResponse(conflictID string, resp *ConflictResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[conflictID] = resp
}

// SetSendError configures an error for SendConflictMessage.
func (m *MockConflictMessageSender) SetSendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendErr = err
}

// SetReceiveError configures an error for ReceiveResponse.
func (m *MockConflictMessageSender) SetReceiveError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recvErr = err
}

// Messages returns all sent messages.
func (m *MockConflictMessageSender) Messages() []*ConflictMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*ConflictMessage, len(m.messages))
	copy(result, m.messages)
	return result
}
