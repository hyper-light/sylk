package quantization

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

type SQLiteBanditStore struct {
	db *sql.DB
}

func NewSQLiteBanditStore(db *sql.DB) *SQLiteBanditStore {
	return &SQLiteBanditStore{db: db}
}

func (s *SQLiteBanditStore) LoadPosteriors(codebaseID string) (map[ArmID][]BetaPosterior, error) {
	if s.db == nil {
		return nil, nil
	}

	rows, err := s.db.Query(`
		SELECT arm_id, choice_idx, alpha, beta
		FROM evq_bandit_posteriors
		WHERE codebase_id = ?
		ORDER BY arm_id, choice_idx
	`, codebaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[ArmID][]BetaPosterior)
	definitions := DefaultArmDefinitions()

	for rows.Next() {
		var row BanditPosteriorRow
		var armIDStr string
		if err := rows.Scan(&armIDStr, &row.ChoiceIdx, &row.Alpha, &row.Beta); err != nil {
			return nil, err
		}
		row.ArmID = ArmID(armIDStr)

		if result[row.ArmID] == nil {
			def := definitions[row.ArmID]
			result[row.ArmID] = make([]BetaPosterior, len(def.Choices))
			for i := range result[row.ArmID] {
				result[row.ArmID][i] = NewUniformPrior()
			}
		}

		if row.ChoiceIdx >= 0 && row.ChoiceIdx < len(result[row.ArmID]) {
			result[row.ArmID][row.ChoiceIdx] = BetaPosterior{
				Alpha: row.Alpha,
				Beta:  row.Beta,
			}
		}
	}

	return result, rows.Err()
}

func (s *SQLiteBanditStore) SavePosteriors(codebaseID string, posteriors map[ArmID][]BetaPosterior) error {
	if s.db == nil {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	stmt, err := tx.Prepare(`
		INSERT INTO evq_bandit_posteriors (codebase_id, arm_id, choice_idx, alpha, beta, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(codebase_id, arm_id, choice_idx) DO UPDATE SET
			alpha = excluded.alpha,
			beta = excluded.beta,
			updated_at = excluded.updated_at
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)

	for armID, armPosteriors := range posteriors {
		for choiceIdx, posterior := range armPosteriors {
			_, err := stmt.Exec(codebaseID, string(armID), choiceIdx, posterior.Alpha, posterior.Beta, now)
			if err != nil {
				return fmt.Errorf("exec for arm %s choice %d: %w", armID, choiceIdx, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

func (s *SQLiteBanditStore) DeleteCodebasePosteriors(codebaseID string) error {
	if s.db == nil {
		return nil
	}

	_, err := s.db.Exec(`DELETE FROM evq_bandit_posteriors WHERE codebase_id = ?`, codebaseID)
	return err
}

func (s *SQLiteBanditStore) GetAllCodebaseIDs() ([]string, error) {
	if s.db == nil {
		return nil, nil
	}

	rows, err := s.db.Query(`SELECT DISTINCT codebase_id FROM evq_bandit_posteriors`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	return ids, rows.Err()
}

type InMemoryBanditStore struct {
	data map[string]map[ArmID][]BetaPosterior
	mu   sync.RWMutex
}

func NewInMemoryBanditStore() *InMemoryBanditStore {
	return &InMemoryBanditStore{
		data: make(map[string]map[ArmID][]BetaPosterior),
	}
}

func (s *InMemoryBanditStore) LoadPosteriors(codebaseID string) (map[ArmID][]BetaPosterior, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	posteriors := s.data[codebaseID]
	if posteriors == nil {
		return nil, nil
	}

	result := make(map[ArmID][]BetaPosterior)
	for armID, armPosteriors := range posteriors {
		copied := make([]BetaPosterior, len(armPosteriors))
		copy(copied, armPosteriors)
		result[armID] = copied
	}

	return result, nil
}

func (s *InMemoryBanditStore) SavePosteriors(codebaseID string, posteriors map[ArmID][]BetaPosterior) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	copied := make(map[ArmID][]BetaPosterior)
	for armID, armPosteriors := range posteriors {
		copiedArm := make([]BetaPosterior, len(armPosteriors))
		copy(copiedArm, armPosteriors)
		copied[armID] = copiedArm
	}

	s.data[codebaseID] = copied
	return nil
}
