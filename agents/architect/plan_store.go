package architect

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// restoreMaxAge is the maximum age of a persisted plan eligible for restore.
// Plans older than this are stale artifacts from prior sessions.
const restoreMaxAge = 24 * time.Hour

// restoreMaxPlans caps the number of plans restored on startup to prevent
// unbounded memory growth from accumulated plan files.
const restoreMaxPlans = 32

// PlanStore is a process-lifetime singleton that manages plan state
// independently of any single Architect instance. Plans survive agent
// demotion/re-promotion because the store is never torn down.
type PlanStore struct {
	mu           sync.RWMutex
	plans        map[string]*DesignPlan
	baseDir      string
	leaseManager *PlanLeaseManager
	logger       *slog.Logger
	reaper       *PlanReaper
	mirrorMu     sync.RWMutex
	mirror       func(*DesignPlan) error
}

// NewPlanStore creates a PlanStore rooted at baseDir, restores persisted plans
// from disk, and starts the background reaper goroutine.
func NewPlanStore(baseDir string, leaseManager *PlanLeaseManager, logger *slog.Logger) *PlanStore {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}
	s := &PlanStore{
		plans:        make(map[string]*DesignPlan),
		baseDir:      baseDir,
		leaseManager: leaseManager,
		logger:       logger,
	}
	if err := s.restoreFromDisk(); err != nil {
		logger.Warn("plan store: failed to restore from disk", "error", err)
	}
	s.reaper = NewPlanReaper(s, leaseManager, logger)
	s.reaper.Start()
	return s
}

// LeaseManager returns the store's lease manager for external callers
// (e.g. heartbeat handlers) that need to renew or grant leases.
func (s *PlanStore) LeaseManager() *PlanLeaseManager {
	return s.leaseManager
}

// -------------------------------------------------------------------------
// Core CRUD
// -------------------------------------------------------------------------

// Upsert atomically updates the in-memory map and persists to disk.
func (s *PlanStore) Upsert(plan *DesignPlan) error {
	if plan == nil || strings.TrimSpace(plan.ID) == "" {
		return nil
	}
	s.mu.Lock()
	s.plans[plan.ID] = plan
	s.mu.Unlock()
	if err := s.persistSnapshot(plan); err != nil {
		return err
	}
	return s.mirrorPlan(plan)
}

// Get returns the plan for the given ID, or nil.
func (s *PlanStore) Get(planID string) *DesignPlan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.plans[planID]
}

// Remove deletes a plan from the in-memory map.
func (s *PlanStore) Remove(planID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.plans, planID)
}

// Snapshot returns a shallow copy of all plans for safe iteration.
func (s *PlanStore) Snapshot() []*DesignPlan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	plans := make([]*DesignPlan, 0, len(s.plans))
	for _, p := range s.plans {
		plans = append(plans, p)
	}
	return plans
}

// Count returns the number of plans in the store.
func (s *PlanStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.plans)
}

// SetMirror registers a secondary persistence hook invoked after each plan
// snapshot is written locally. Passing nil removes the mirror.
func (s *PlanStore) SetMirror(mirror func(*DesignPlan) error) {
	s.mirrorMu.Lock()
	s.mirror = mirror
	s.mirrorMu.Unlock()
}

// -------------------------------------------------------------------------
// Query Methods
// -------------------------------------------------------------------------

// LatestByStatus returns the most recently updated plan matching the given
// status and session, provided it was updated within maxAge.
func (s *PlanStore) LatestByStatus(sessionID string, status PlanStatus, maxAge time.Duration) *DesignPlan {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cutoff := time.Now().Add(-maxAge)
	var best *DesignPlan
	for _, plan := range s.plans {
		if plan.SM().State() != status {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
			continue
		}
		if plan.UpdatedAt.Before(cutoff) {
			continue
		}
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	return best
}

// LatestStalled returns the most recently updated plan stuck at an
// intermediate state (Pending through Orchestrating) for the given session,
// excluding Clarifying. Plans older than maxAge are excluded.
func (s *PlanStore) LatestStalled(sessionID string, maxAge time.Duration) *DesignPlan {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cutoff := time.Now().Add(-maxAge)
	var best *DesignPlan
	for _, plan := range s.plans {
		if !isStalledState(plan.SM().State()) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
			continue
		}
		if plan.UpdatedAt.Before(cutoff) {
			continue
		}
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	return best
}

// LatestClarifying returns the most recently updated plan in Clarifying
// state for the given session. Returns nil if no matching plan exists.
func (s *PlanStore) LatestClarifying(sessionID string) *DesignPlan {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cutoff := time.Now().Add(-ReadyPlanMaxAge)
	var best *DesignPlan
	for _, plan := range s.plans {
		if plan.SM().State() != PlanStatusClarifying {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
			continue
		}
		if plan.UpdatedAt.Before(cutoff) {
			continue
		}
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	return best
}

// AllForSession returns all plans matching the given session ID.
func (s *PlanStore) AllForSession(sessionID string) []*DesignPlan {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*DesignPlan
	for _, plan := range s.plans {
		if strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
			result = append(result, plan)
		}
	}
	return result
}

// LatestHistorical returns the most recently updated plan for a session
// regardless of status. Used for conversation context enrichment.
func (s *PlanStore) LatestHistorical(sessionID string) *DesignPlan {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *DesignPlan
	for _, plan := range s.plans {
		if !strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
			continue
		}
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	return best
}

// LatestConsulting returns the most recently updated plan in a consulting-
// compatible state (Consulting, Clarifying, Analyzing, Designing) for the
// given session.
func (s *PlanStore) LatestConsulting(sessionID string) *DesignPlan {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *DesignPlan
	for _, plan := range s.plans {
		state := plan.SM().State()
		if state != PlanStatusConsulting && state != PlanStatusClarifying &&
			state != PlanStatusAnalyzing && state != PlanStatusDesigning {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
			continue
		}
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	return best
}

// MatchingQuery returns all plans whose Query contains substr (case-insensitive).
func (s *PlanStore) MatchingQuery(substr string) []*DesignPlan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*DesignPlan
	for _, plan := range s.plans {
		if containsIgnoreCase(plan.Query, substr) {
			result = append(result, plan)
		}
	}
	return result
}

// FirstMatchingQuery returns the first plan whose Query contains substr
// (case-insensitive), or nil.
func (s *PlanStore) FirstMatchingQuery(substr string) *DesignPlan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, plan := range s.plans {
		if containsIgnoreCase(plan.Query, substr) {
			return plan
		}
	}
	return nil
}

// -------------------------------------------------------------------------
// Persistence
// -------------------------------------------------------------------------

func (s *PlanStore) persistSnapshot(plan *DesignPlan) error {
	if plan == nil || strings.TrimSpace(plan.ID) == "" {
		return nil
	}
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	dir := s.PlanDir(plan.SessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	trimmedID := strings.TrimSpace(plan.ID)
	finalPath := filepath.Join(dir, trimmedID+".json")
	tmpPath := finalPath + ".tmp"
	if err := os.WriteFile(tmpPath, encoded, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, finalPath)
}

func (s *PlanStore) mirrorPlan(plan *DesignPlan) error {
	s.mirrorMu.RLock()
	mirror := s.mirror
	s.mirrorMu.RUnlock()
	if mirror == nil || plan == nil {
		return nil
	}
	return mirror(plan)
}

// PlanDir returns the directory path for plan files in the given session.
func (s *PlanStore) PlanDir(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		sessionID = "default"
	}
	return filepath.Join(s.baseDir, ".sylk", "sessions", sessionID, "agents", "architect", "plans")
}

func (s *PlanStore) restoreFromDisk() error {
	sessionsDir := filepath.Join(s.baseDir, ".sylk", "sessions")
	sessionEntries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-restoreMaxAge)
	restored := 0
	skipped := 0
	for _, sessionEntry := range sessionEntries {
		if !sessionEntry.IsDir() {
			continue
		}
		planDir := filepath.Join(sessionsDir, sessionEntry.Name(), "agents", "architect", "plans")
		entries, readErr := os.ReadDir(planDir)
		if readErr != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			if restored >= restoreMaxPlans {
				_ = os.Remove(filepath.Join(planDir, entry.Name()))
				skipped++
				continue
			}
			path := filepath.Join(planDir, entry.Name())
			if ok, restoreErr := s.restorePlanFromFile(path, cutoff); restoreErr != nil {
				s.logger.Warn("failed to restore plan", "path", path, "error", restoreErr)
				continue
			} else if ok {
				restored++
			} else {
				skipped++
			}
		}
	}
	s.logger.Info("plan store: restore complete",
		"sessions_dir", sessionsDir, "restored", restored, "skipped", skipped)
	return nil
}

func (s *PlanStore) restorePlanFromFile(path string, cutoff time.Time) (bool, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var plan DesignPlan
	if err := json.Unmarshal(payload, &plan); err != nil {
		return false, err
	}
	if strings.TrimSpace(plan.ID) == "" {
		return false, fmt.Errorf("restored plan missing id")
	}
	// Skip terminal-state plans only. Ready and Executing are restored —
	// the architect may be demoted while waiting for approval or execution.
	switch plan.Status {
	case PlanStatusFailed, PlanStatusCompleted, PlanStatusSuperseded:
		_ = os.Remove(path)
		return false, nil
	}
	if plan.UpdatedAt.Before(cutoff) {
		_ = os.Remove(path)
		return false, nil
	}
	plan.sm = NewPlanStateMachineWithEpoch(plan.ID, plan.Status, plan.Epoch)
	s.logger.Info("plan store: restored plan",
		"plan_id", plan.ID,
		"status", plan.Status.String(),
		"query", truncateString(plan.Query, 80),
		"tasks", len(plan.Tasks),
		"created_at", plan.CreatedAt.String())
	s.mu.Lock()
	s.plans[plan.ID] = &plan
	s.mu.Unlock()
	return true, nil
}

// RemoveDiskFile removes the on-disk plan file for eviction.
func (s *PlanStore) RemoveDiskFile(planID, sessionID string) {
	dir := s.PlanDir(sessionID)
	path := filepath.Join(dir, strings.TrimSpace(planID)+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		s.logger.Warn("plan store: failed to remove disk file",
			"path", path, "error", err)
	}
}

// -------------------------------------------------------------------------
// Lifecycle
// -------------------------------------------------------------------------

// Close stops the reaper goroutine.
func (s *PlanStore) Close() {
	if s.reaper != nil {
		s.reaper.Stop()
		s.reaper = nil
	}
}
