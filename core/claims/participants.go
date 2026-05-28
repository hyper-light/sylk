package claims

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ParticipantCategoryAgent    ParticipantCategory = "agent"
	ParticipantCategoryService  ParticipantCategory = "service"
	ParticipantCategorySystem   ParticipantCategory = "system"
	ParticipantCategoryExternal ParticipantCategory = "external"

	HandlerDeterminismPure             HandlerDeterminism = "pure"
	HandlerDeterminismContent          HandlerDeterminism = "content"
	HandlerDeterminismSideEffect       HandlerDeterminism = "side_effect"
	HandlerDeterminismNondeterministic HandlerDeterminism = "nondeterministic"

	InitialParticipantGeneration uint64 = 1

	ParticipantUIDDerivationVersion = "v1"

	participantUIDNamespace = "participant"
)

var (
	ErrParticipantRegistrationInvalid  = errors.New("participant registration invalid")
	ErrParticipantRegistrationConflict = errors.New("participant registration conflicts with existing immutable metadata")
)

type ParticipantCategory string

type HandlerDeterminism string

type ParticipantRegistration struct {
	UID               string
	Category          ParticipantCategory
	RouteKey          string
	ScopeKeys         map[string]string
	QueueCapacity     int
	ConcurrencyBudget int
	HandlerTimeout    time.Duration
	Determinism       HandlerDeterminism
	Actions           []ActionType
	Generation        uint64
	Ref               AgentRef
}

type ParticipantRegistry struct {
	mu      sync.RWMutex
	records map[string]ParticipantRegistration
}

func NewParticipantRegistry() *ParticipantRegistry {
	return &ParticipantRegistry{records: make(map[string]ParticipantRegistration)}
}

func DeriveParticipantUID(category ParticipantCategory, routeKey string, scopeKeys map[string]string) (string, error) {
	category = ParticipantCategory(strings.TrimSpace(string(category)))
	routeKey = strings.TrimSpace(routeKey)
	scopeKeys = cloneStringMap(scopeKeys)
	if !category.Valid() || routeKey == "" {
		return "", fmt.Errorf("%w: category and route key are required", ErrParticipantRegistrationInvalid)
	}
	if len(scopeKeys) == 0 && category != ParticipantCategoryAgent {
		return "", fmt.Errorf("%w: non-agent participants require scope keys", ErrParticipantRegistrationInvalid)
	}
	hash := sha256.Sum256([]byte(participantIdentityMaterial(category, routeKey, scopeKeys)))
	return strings.Join([]string{participantUIDNamespace, string(category), sanitizeParticipantSegment(routeKey), hex.EncodeToString(hash[:])}, ":"), nil
}

func NewParticipantRegistration(category ParticipantCategory, routeKey string, scopeKeys map[string]string, queueCapacity, concurrencyBudget int, timeout time.Duration, determinism HandlerDeterminism, actions []ActionType) (ParticipantRegistration, error) {
	uid, err := DeriveParticipantUID(category, routeKey, scopeKeys)
	if err != nil {
		return ParticipantRegistration{}, err
	}
	reg := ParticipantRegistration{
		UID:               uid,
		Category:          ParticipantCategory(strings.TrimSpace(string(category))),
		RouteKey:          strings.TrimSpace(routeKey),
		ScopeKeys:         cloneStringMap(scopeKeys),
		QueueCapacity:     queueCapacity,
		ConcurrencyBudget: concurrencyBudget,
		HandlerTimeout:    timeout,
		Determinism:       HandlerDeterminism(strings.TrimSpace(string(determinism))),
		Actions:           normalizeActionTypes(actions),
		Generation:        InitialParticipantGeneration,
	}
	reg.Ref = reg.AgentRef()
	return reg, reg.Validate()
}

func NewServiceParticipantRegistration(routeKey string, scopeKeys map[string]string, queueCapacity, concurrencyBudget int, timeout time.Duration, actions []ActionType) (ParticipantRegistration, error) {
	return NewParticipantRegistration(ParticipantCategoryService, routeKey, scopeKeys, queueCapacity, concurrencyBudget, timeout, HandlerDeterminismContent, actions)
}

func ParticipantRegistrationFromAgentRef(ref AgentRef, queueCapacity, concurrencyBudget int, timeout time.Duration, actions []ActionType) (ParticipantRegistration, error) {
	ref = ref.Normalized()
	reg := ParticipantRegistration{
		UID:               ref.UID,
		Category:          ParticipantCategoryAgent,
		RouteKey:          ref.RouteKey(),
		ScopeKeys:         cloneStringMap(ref.Labels),
		QueueCapacity:     queueCapacity,
		ConcurrencyBudget: concurrencyBudget,
		HandlerTimeout:    timeout,
		Determinism:       HandlerDeterminismNondeterministic,
		Actions:           normalizeActionTypes(actions),
		Generation:        maxUint64(ref.Generation, InitialParticipantGeneration),
		Ref:               ref,
	}
	return reg, reg.Validate()
}

func (r ParticipantRegistration) Validate() error {
	if strings.TrimSpace(r.UID) == "" || strings.TrimSpace(r.RouteKey) == "" {
		return fmt.Errorf("%w: uid and route key are required", ErrParticipantRegistrationInvalid)
	}
	if !r.Category.Valid() {
		return fmt.Errorf("%w: category %q is invalid", ErrParticipantRegistrationInvalid, r.Category)
	}
	if r.QueueCapacity <= 0 || r.ConcurrencyBudget <= 0 {
		return fmt.Errorf("%w: queue capacity and concurrency budget must be bounded positive values", ErrParticipantRegistrationInvalid)
	}
	if r.HandlerTimeout <= 0 {
		return fmt.Errorf("%w: handler timeout must be a bounded positive value", ErrParticipantRegistrationInvalid)
	}
	if !r.Determinism.Valid() {
		return fmt.Errorf("%w: determinism is required", ErrParticipantRegistrationInvalid)
	}
	if len(r.Actions) == 0 {
		return fmt.Errorf("%w: at least one action subscription is required", ErrParticipantRegistrationInvalid)
	}
	if r.Generation == 0 {
		return fmt.Errorf("%w: generation is required", ErrParticipantRegistrationInvalid)
	}
	scopeKeys := cloneStringMap(r.ScopeKeys)
	if r.Category != ParticipantCategoryAgent && len(scopeKeys) == 0 {
		return fmt.Errorf("%w: non-agent participants require scope keys", ErrParticipantRegistrationInvalid)
	}
	if r.Category != ParticipantCategoryAgent {
		derivedUID, err := DeriveParticipantUID(r.Category, r.RouteKey, scopeKeys)
		if err != nil {
			return err
		}
		if derivedUID != strings.TrimSpace(r.UID) {
			return fmt.Errorf("%w: uid %q does not match deterministic uid %q", ErrParticipantRegistrationInvalid, r.UID, derivedUID)
		}
	}
	ref := r.AgentRef()
	if err := ref.Validate(); err != nil {
		return fmt.Errorf("%w: canonical ref invalid: %v", ErrParticipantRegistrationInvalid, err)
	}
	if ref.UID != strings.TrimSpace(r.UID) {
		return fmt.Errorf("%w: ref uid %q does not match registration uid %q", ErrParticipantRegistrationInvalid, ref.UID, r.UID)
	}
	if ref.Category != string(r.Category) {
		return fmt.Errorf("%w: ref category %q does not match registration category %q", ErrParticipantRegistrationInvalid, ref.Category, r.Category)
	}
	return nil
}

func (r ParticipantRegistration) AgentRef() AgentRef {
	ref := r.Ref.Normalized()
	if ref.UID == "" {
		ref.UID = strings.TrimSpace(r.UID)
	}
	if ref.Type == "" {
		ref.Type = strings.TrimSpace(r.RouteKey)
	}
	if ref.Name == "" {
		ref.Name = ref.Type
	}
	if r.Category.Valid() {
		ref.Category = string(r.Category)
	}
	if r.Generation != 0 {
		ref.Generation = r.Generation
	}
	if len(ref.Labels) == 0 && len(r.ScopeKeys) != 0 {
		ref.Labels = cloneStringMap(r.ScopeKeys)
	}
	return ref.Normalized()
}

func (r *ParticipantRegistry) Register(reg ParticipantRegistration) (ParticipantRegistration, error) {
	if r == nil {
		return ParticipantRegistration{}, fmt.Errorf("%w: registry is nil", ErrParticipantRegistrationInvalid)
	}
	normalized, err := normalizeParticipantRegistration(reg)
	if err != nil {
		return ParticipantRegistration{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.records[normalized.UID]
	if !ok {
		r.records[normalized.UID] = normalized
		return normalized, nil
	}
	if !participantImmutableFieldsEqual(existing, normalized) {
		return existing, ErrParticipantRegistrationConflict
	}
	if normalized.Generation > existing.Generation {
		r.records[normalized.UID] = normalized
		return normalized, nil
	}
	return existing, nil
}

func (r *ParticipantRegistry) Lookup(uid string) (ParticipantRegistration, bool) {
	if r == nil {
		return ParticipantRegistration{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	reg, ok := r.records[strings.TrimSpace(uid)]
	return cloneParticipantRegistration(reg), ok
}

func normalizeParticipantRegistration(reg ParticipantRegistration) (ParticipantRegistration, error) {
	reg.UID = strings.TrimSpace(reg.UID)
	reg.RouteKey = strings.TrimSpace(reg.RouteKey)
	reg.Category = ParticipantCategory(strings.TrimSpace(string(reg.Category)))
	reg.ScopeKeys = cloneStringMap(reg.ScopeKeys)
	reg.Actions = normalizeActionTypes(reg.Actions)
	reg.Ref = reg.AgentRef()
	return reg, reg.Validate()
}

func participantImmutableFieldsEqual(a, b ParticipantRegistration) bool {
	return a.UID == b.UID &&
		a.Category == b.Category &&
		a.RouteKey == b.RouteKey &&
		a.QueueCapacity == b.QueueCapacity &&
		a.ConcurrencyBudget == b.ConcurrencyBudget &&
		a.HandlerTimeout == b.HandlerTimeout &&
		a.Determinism == b.Determinism &&
		stringMapsEqual(a.ScopeKeys, b.ScopeKeys) &&
		actionTypesEqual(a.Actions, b.Actions)
}

func participantIdentityMaterial(category ParticipantCategory, routeKey string, scopeKeys map[string]string) string {
	parts := []string{ParticipantUIDDerivationVersion, string(category), strings.TrimSpace(routeKey)}
	for _, key := range sortedStringKeys(scopeKeys) {
		parts = append(parts, key+"="+strings.TrimSpace(scopeKeys[key]))
	}
	return strings.Join(parts, "\x00")
}

func cloneParticipantRegistration(reg ParticipantRegistration) ParticipantRegistration {
	reg.ScopeKeys = cloneStringMap(reg.ScopeKeys)
	reg.Actions = append([]ActionType(nil), reg.Actions...)
	reg.Ref.Labels = cloneStringMap(reg.Ref.Labels)
	return reg
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		if strings.TrimSpace(key) != "" {
			out[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return out
}

func sortedStringKeys(in map[string]string) []string {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, strings.TrimSpace(key))
	}
	sort.Strings(keys)
	return keys
}

func normalizeActionTypes(in []ActionType) []ActionType {
	seen := make(map[ActionType]bool, len(in))
	for _, action := range in {
		action = ActionType(strings.TrimSpace(string(action)))
		if action != "" {
			seen[action] = true
		}
	}
	out := make([]ActionType, 0, len(seen))
	for action := range seen {
		out = append(out, action)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func actionTypesEqual(a, b []ActionType) bool {
	a = normalizeActionTypes(a)
	b = normalizeActionTypes(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stringMapsEqual(a, b map[string]string) bool {
	a = cloneStringMap(a)
	b = cloneStringMap(b)
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func KnownParticipantCategories() []ParticipantCategory {
	return []ParticipantCategory{
		ParticipantCategoryAgent,
		ParticipantCategoryService,
		ParticipantCategorySystem,
		ParticipantCategoryExternal,
	}
}

func KnownHandlerDeterminismLevels() []HandlerDeterminism {
	return []HandlerDeterminism{
		HandlerDeterminismPure,
		HandlerDeterminismContent,
		HandlerDeterminismSideEffect,
		HandlerDeterminismNondeterministic,
	}
}

func (c ParticipantCategory) Valid() bool {
	switch c {
	case ParticipantCategoryAgent, ParticipantCategoryService, ParticipantCategorySystem, ParticipantCategoryExternal:
		return true
	default:
		return false
	}
}

func (d HandlerDeterminism) valid() bool {
	return d.Valid()
}

func (d HandlerDeterminism) Valid() bool {
	switch d {
	case HandlerDeterminismPure, HandlerDeterminismContent, HandlerDeterminismSideEffect, HandlerDeterminismNondeterministic:
		return true
	default:
		return false
	}
}

func sanitizeParticipantSegment(value string) string {
	return strings.NewReplacer(":", "_", "/", "_", ".", "_", " ", "_", "\t", "_", "\n", "_", "\r", "_").Replace(strings.TrimSpace(value))
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
