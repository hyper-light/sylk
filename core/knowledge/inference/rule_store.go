package inference

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrRuleNotFound is returned when a rule cannot be found by ID.
var ErrRuleNotFound = errors.New("inference rule not found")

// =============================================================================
// RuleStore (IE.2.1)
// =============================================================================

// RuleStore provides persistent storage for inference rules using SQLite.
// It supports optional in-memory caching for improved read performance.
type RuleStore struct {
	db    *sql.DB
	mu    sync.RWMutex
	cache map[string]*InferenceRule
}

// NewRuleStore creates a new RuleStore with the given database connection.
// The cache is initialized as empty and populated on first LoadRules call.
func NewRuleStore(db *sql.DB) *RuleStore {
	return &RuleStore{
		db:    db,
		cache: make(map[string]*InferenceRule),
	}
}

// LoadRules retrieves all inference rules from the database.
// Results are cached for subsequent reads.
func (s *RuleStore) LoadRules(ctx context.Context) ([]InferenceRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, head_subject, head_predicate, head_object,
		       body_json, priority, enabled, created_at
		FROM inference_rules
	`)
	if err != nil {
		return nil, fmt.Errorf("query rules: %w", err)
	}
	defer rows.Close()

	rules, err := s.scanRules(rows)
	if err != nil {
		return nil, err
	}

	s.updateCache(rules)
	return rules, nil
}

// scanRules scans all rows into InferenceRule slice.
func (s *RuleStore) scanRules(rows *sql.Rows) ([]InferenceRule, error) {
	var rules []InferenceRule
	for rows.Next() {
		rule, err := scanRuleRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rules: %w", err)
	}
	return rules, nil
}

// updateCache replaces the cache with the given rules.
// Each rule is copied before storing to avoid pointer aliasing.
func (s *RuleStore) updateCache(rules []InferenceRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = make(map[string]*InferenceRule, len(rules))
	for i := range rules {
		ruleCopy := rules[i]
		s.cache[ruleCopy.ID] = &ruleCopy
	}
}

// SaveRule inserts or updates an inference rule in the database.
// The cache is updated after a successful save.
func (s *RuleStore) SaveRule(ctx context.Context, rule InferenceRule) error {
	bodyJSON, err := json.Marshal(rule.Body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	enabled := boolToInt(rule.Enabled)
	createdAt := time.Now().UTC().Format(time.RFC3339)

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO inference_rules
		    (id, name, head_subject, head_predicate, head_object, body_json, priority, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		    name = excluded.name,
		    head_subject = excluded.head_subject,
		    head_predicate = excluded.head_predicate,
		    head_object = excluded.head_object,
		    body_json = excluded.body_json,
		    priority = excluded.priority,
		    enabled = excluded.enabled
	`,
		rule.ID, rule.Name,
		rule.Head.Subject, rule.Head.Predicate, rule.Head.Object,
		string(bodyJSON), rule.Priority, enabled, createdAt,
	)
	if err != nil {
		return fmt.Errorf("save rule: %w", err)
	}

	s.cacheRule(&rule)
	return nil
}

// cacheRule adds or updates a rule in the cache.
func (s *RuleStore) cacheRule(rule *InferenceRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ruleCopy := *rule
	s.cache[rule.ID] = &ruleCopy
}

// DeleteRule removes an inference rule from the database by ID.
// The cache is updated after a successful delete.
func (s *RuleStore) DeleteRule(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM inference_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("delete rule id=%s: %w", id, ErrRuleNotFound)
	}

	s.removeFromCache(id)
	return nil
}

// removeFromCache removes a rule from the cache.
func (s *RuleStore) removeFromCache(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, id)
}

// GetRulesByPriority retrieves all rules sorted by priority (ascending).
// Lower priority values are returned first.
func (s *RuleStore) GetRulesByPriority(ctx context.Context) ([]InferenceRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, head_subject, head_predicate, head_object,
		       body_json, priority, enabled, created_at
		FROM inference_rules
		ORDER BY priority ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query rules by priority: %w", err)
	}
	defer rows.Close()

	return s.scanRules(rows)
}

// GetEnabledRules retrieves all rules where enabled=1.
// Results are sorted by priority (ascending).
func (s *RuleStore) GetEnabledRules(ctx context.Context) ([]InferenceRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, head_subject, head_predicate, head_object,
		       body_json, priority, enabled, created_at
		FROM inference_rules
		WHERE enabled = 1
		ORDER BY priority ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query enabled rules: %w", err)
	}
	defer rows.Close()

	return s.scanRules(rows)
}

// GetRule retrieves a single rule by ID.
// Returns nil if the rule is not found.
func (s *RuleStore) GetRule(ctx context.Context, id string) (*InferenceRule, error) {
	if rule := s.getFromCache(id); rule != nil {
		return rule, nil
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, head_subject, head_predicate, head_object,
		       body_json, priority, enabled, created_at
		FROM inference_rules
		WHERE id = ?
	`, id)

	rule, err := scanSingleRuleRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rule: %w", err)
	}

	s.cacheRule(rule)
	return rule, nil
}

// getFromCache retrieves a rule from the cache if present.
func (s *RuleStore) getFromCache(id string) *InferenceRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if rule, ok := s.cache[id]; ok {
		ruleCopy := *rule
		return &ruleCopy
	}
	return nil
}

// scanRuleRow scans a single row from sql.Rows into an InferenceRule.
func scanRuleRow(rows *sql.Rows) (InferenceRule, error) {
	var rule InferenceRule
	var headSubject, headPredicate, headObject string
	var bodyJSON string
	var enabled int
	var createdAt string

	err := rows.Scan(
		&rule.ID, &rule.Name,
		&headSubject, &headPredicate, &headObject,
		&bodyJSON, &rule.Priority, &enabled, &createdAt,
	)
	if err != nil {
		return InferenceRule{}, err
	}

	rule.Head = RuleCondition{
		Subject:   headSubject,
		Predicate: headPredicate,
		Object:    headObject,
	}
	rule.Enabled = intToBool(enabled)

	if err := json.Unmarshal([]byte(bodyJSON), &rule.Body); err != nil {
		return InferenceRule{}, fmt.Errorf("unmarshal body: %w", err)
	}

	return rule, nil
}

// scanSingleRuleRow scans a single row from sql.Row into an InferenceRule.
func scanSingleRuleRow(row *sql.Row) (*InferenceRule, error) {
	var rule InferenceRule
	var headSubject, headPredicate, headObject string
	var bodyJSON string
	var enabled int
	var createdAt string

	err := row.Scan(
		&rule.ID, &rule.Name,
		&headSubject, &headPredicate, &headObject,
		&bodyJSON, &rule.Priority, &enabled, &createdAt,
	)
	if err != nil {
		return nil, err
	}

	rule.Head = RuleCondition{
		Subject:   headSubject,
		Predicate: headPredicate,
		Object:    headObject,
	}
	rule.Enabled = intToBool(enabled)

	if err := json.Unmarshal([]byte(bodyJSON), &rule.Body); err != nil {
		return nil, fmt.Errorf("unmarshal body: %w", err)
	}

	return &rule, nil
}

// boolToInt converts a boolean to an integer (0 or 1) for SQLite storage.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// intToBool converts an integer (0 or 1) to a boolean.
func intToBool(i int) bool {
	return i != 0
}
