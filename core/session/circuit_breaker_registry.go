package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrCircuitBreakerRegistryClosed = errors.New("circuit breaker registry is closed")
	ErrCircuitOpen                  = errors.New("circuit breaker is open")
)

const (
	SignalCircuitStateChange SignalType = "circuit_state_change"
)

type CircuitState int

const (
	CircuitClosed CircuitState = iota
	CircuitOpen
	CircuitHalfOpen
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

type CircuitBreakerState struct {
	ResourceID      string       `json:"resource_id"`
	State           CircuitState `json:"state"`
	Failures        int          `json:"failures"`
	LastFailure     *time.Time   `json:"last_failure,omitempty"`
	LastStateChange time.Time    `json:"last_state_change"`
	CooldownEnds    *time.Time   `json:"cooldown_ends,omitempty"`
}

type CircuitBreakerConfig struct {
	ConsecutiveFailures int
	CooldownDuration    time.Duration
	HalfOpenMaxProbes   int
}

func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		ConsecutiveFailures: 5,
		CooldownDuration:    30 * time.Second,
		HalfOpenMaxProbes:   1,
	}
}

type CircuitBreakerRegistryConfig struct {
	DBDir            string
	SignalDispatcher *SignalDispatcher
	DefaultConfig    CircuitBreakerConfig
}

type GlobalCircuitBreakerRegistry struct {
	db               *sql.DB
	signalDispatcher *SignalDispatcher
	defaultConfig    CircuitBreakerConfig

	localCache map[string]*CircuitBreakerState
	cacheMu    sync.RWMutex
	closed     bool
}

func NewGlobalCircuitBreakerRegistry(cfg CircuitBreakerRegistryConfig) (*GlobalCircuitBreakerRegistry, error) {
	cfg = normalizeCircuitBreakerConfig(cfg)

	if err := os.MkdirAll(cfg.DBDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db dir: %w", err)
	}

	db, err := openCircuitBreakerDB(cfg.DBDir)
	if err != nil {
		return nil, err
	}

	registry := &GlobalCircuitBreakerRegistry{
		db:               db,
		signalDispatcher: cfg.SignalDispatcher,
		defaultConfig:    cfg.DefaultConfig,
		localCache:       make(map[string]*CircuitBreakerState),
	}

	if err := registry.loadCache(); err != nil {
		db.Close()
		return nil, err
	}

	return registry, nil
}

func normalizeCircuitBreakerConfig(cfg CircuitBreakerRegistryConfig) CircuitBreakerRegistryConfig {
	if cfg.DBDir == "" {
		homeDir, _ := os.UserHomeDir()
		cfg.DBDir = filepath.Join(homeDir, ".sylk", "shared")
	}
	if cfg.DefaultConfig.ConsecutiveFailures <= 0 {
		cfg.DefaultConfig = DefaultCircuitBreakerConfig()
	}
	return cfg
}

func openCircuitBreakerDB(dbDir string) (*sql.DB, error) {
	dbPath := filepath.Join(dbDir, "circuit_breakers.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := createCircuitBreakerSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func createCircuitBreakerSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS circuit_breakers (
			resource_id TEXT PRIMARY KEY,
			state INTEGER NOT NULL DEFAULT 0,
			failures INTEGER NOT NULL DEFAULT 0,
			last_failure TIMESTAMP,
			last_state_change TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			cooldown_ends TIMESTAMP
		)
	`)
	return err
}

func (r *GlobalCircuitBreakerRegistry) loadCache() error {
	rows, err := r.db.Query(`
		SELECT resource_id, state, failures, last_failure, last_state_change, cooldown_ends
		FROM circuit_breakers
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var state CircuitBreakerState
		err := rows.Scan(
			&state.ResourceID,
			&state.State,
			&state.Failures,
			&state.LastFailure,
			&state.LastStateChange,
			&state.CooldownEnds,
		)
		if err != nil {
			continue
		}
		r.localCache[state.ResourceID] = &state
	}

	return rows.Err()
}

func (r *GlobalCircuitBreakerRegistry) RecordResult(resourceID string, success bool) (*CircuitBreakerState, error) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	if r.closed {
		return nil, ErrCircuitBreakerRegistryClosed
	}

	state, err := r.getCurrentState(resourceID)
	if err != nil {
		return nil, err
	}

	oldState := state.State
	r.applyResult(state, success)

	if err := r.persistState(state); err != nil {
		return nil, err
	}

	r.localCache[resourceID] = state
	r.maybeNotifyStateChange(state, oldState)

	return state, nil
}

func (r *GlobalCircuitBreakerRegistry) getCurrentState(resourceID string) (*CircuitBreakerState, error) {
	var state CircuitBreakerState

	err := r.db.QueryRow(`
		SELECT resource_id, state, failures, last_failure, last_state_change, cooldown_ends
		FROM circuit_breakers WHERE resource_id = ?
	`, resourceID).Scan(
		&state.ResourceID,
		&state.State,
		&state.Failures,
		&state.LastFailure,
		&state.LastStateChange,
		&state.CooldownEnds,
	)

	if err == sql.ErrNoRows {
		return r.newCircuitState(resourceID), nil
	}
	if err != nil {
		return nil, err
	}

	return &state, nil
}

func (r *GlobalCircuitBreakerRegistry) newCircuitState(resourceID string) *CircuitBreakerState {
	return &CircuitBreakerState{
		ResourceID:      resourceID,
		State:           CircuitClosed,
		LastStateChange: time.Now(),
	}
}

func (r *GlobalCircuitBreakerRegistry) applyResult(state *CircuitBreakerState, success bool) {
	if success {
		r.applySuccess(state)
	} else {
		r.applyFailure(state)
	}
}

func (r *GlobalCircuitBreakerRegistry) applySuccess(state *CircuitBreakerState) {
	state.Failures = 0
	if state.State == CircuitHalfOpen {
		state.State = CircuitClosed
		state.LastStateChange = time.Now()
		state.CooldownEnds = nil
	}
}

func (r *GlobalCircuitBreakerRegistry) applyFailure(state *CircuitBreakerState) {
	state.Failures++
	now := time.Now()
	state.LastFailure = &now

	r.maybeTrip(state, now)
}

func (r *GlobalCircuitBreakerRegistry) maybeTrip(state *CircuitBreakerState, now time.Time) {
	cfg := r.defaultConfig

	if state.State == CircuitClosed && state.Failures >= cfg.ConsecutiveFailures {
		r.tripCircuit(state, now, cfg.CooldownDuration)
	} else if state.State == CircuitHalfOpen {
		r.tripCircuit(state, now, cfg.CooldownDuration)
	}
}

func (r *GlobalCircuitBreakerRegistry) tripCircuit(state *CircuitBreakerState, now time.Time, cooldown time.Duration) {
	state.State = CircuitOpen
	state.LastStateChange = now
	cooldownEnd := now.Add(cooldown)
	state.CooldownEnds = &cooldownEnd
}

func (r *GlobalCircuitBreakerRegistry) persistState(state *CircuitBreakerState) error {
	_, err := r.db.Exec(`
		INSERT INTO circuit_breakers (resource_id, state, failures, last_failure, last_state_change, cooldown_ends)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (resource_id) DO UPDATE SET
			state = ?, failures = ?, last_failure = ?, last_state_change = ?, cooldown_ends = ?
	`,
		state.ResourceID, state.State, state.Failures, state.LastFailure, state.LastStateChange, state.CooldownEnds,
		state.State, state.Failures, state.LastFailure, state.LastStateChange, state.CooldownEnds,
	)
	return err
}

func (r *GlobalCircuitBreakerRegistry) maybeNotifyStateChange(state *CircuitBreakerState, oldState CircuitState) {
	if state.State == oldState {
		return
	}
	if r.signalDispatcher == nil {
		return
	}

	r.broadcastStateChange(state)
}

func (r *GlobalCircuitBreakerRegistry) broadcastStateChange(state *CircuitBreakerState) {
	if r.signalDispatcher == nil {
		return
	}

	payload, _ := json.Marshal(state)

	r.signalDispatcher.SendSignal(CrossSessionSignal{
		Type:      SignalCircuitStateChange,
		Timestamp: time.Now(),
		Payload:   string(payload),
	})
}

func (r *GlobalCircuitBreakerRegistry) Allow(resourceID string) bool {
	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()

	if r.closed {
		return false
	}

	state, ok := r.localCache[resourceID]
	if !ok {
		return true
	}

	return r.isAllowed(state)
}

func (r *GlobalCircuitBreakerRegistry) isAllowed(state *CircuitBreakerState) bool {
	switch state.State {
	case CircuitClosed:
		return true
	case CircuitOpen:
		return r.isCooldownExpired(state)
	case CircuitHalfOpen:
		return true
	default:
		return false
	}
}

func (r *GlobalCircuitBreakerRegistry) isCooldownExpired(state *CircuitBreakerState) bool {
	if state.CooldownEnds == nil {
		return true
	}
	return time.Now().After(*state.CooldownEnds)
}

func (r *GlobalCircuitBreakerRegistry) GetState(resourceID string) *CircuitBreakerState {
	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()

	state, ok := r.localCache[resourceID]
	if !ok {
		return nil
	}

	copy := *state
	return &copy
}

func (r *GlobalCircuitBreakerRegistry) GetAllStates() map[string]*CircuitBreakerState {
	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()

	result := make(map[string]*CircuitBreakerState, len(r.localCache))
	for id, state := range r.localCache {
		copy := *state
		result[id] = &copy
	}
	return result
}

func (r *GlobalCircuitBreakerRegistry) OnSignalReceived(signal CrossSessionSignal) {
	if signal.Type != SignalCircuitStateChange {
		return
	}

	var state CircuitBreakerState
	if err := json.Unmarshal([]byte(signal.Payload), &state); err != nil {
		return
	}

	r.cacheMu.Lock()
	r.localCache[state.ResourceID] = &state
	r.cacheMu.Unlock()
}

func (r *GlobalCircuitBreakerRegistry) Reset(resourceID string) error {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	if r.closed {
		return ErrCircuitBreakerRegistryClosed
	}

	_, err := r.db.Exec(`DELETE FROM circuit_breakers WHERE resource_id = ?`, resourceID)
	if err != nil {
		return err
	}

	delete(r.localCache, resourceID)
	return nil
}

func (r *GlobalCircuitBreakerRegistry) TransitionToHalfOpen(resourceID string) error {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	if r.closed {
		return ErrCircuitBreakerRegistryClosed
	}

	state := r.localCache[resourceID]
	if !r.canTransitionToHalfOpen(state) {
		return nil
	}

	state.State = CircuitHalfOpen
	state.LastStateChange = time.Now()

	if err := r.persistState(state); err != nil {
		return err
	}

	r.broadcastStateChange(state)
	return nil
}

func (r *GlobalCircuitBreakerRegistry) canTransitionToHalfOpen(state *CircuitBreakerState) bool {
	return state != nil && state.State == CircuitOpen
}

func (r *GlobalCircuitBreakerRegistry) Close() error {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	if r.closed {
		return ErrCircuitBreakerRegistryClosed
	}
	r.closed = true

	r.localCache = nil
	return r.db.Close()
}
