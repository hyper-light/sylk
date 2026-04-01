package forest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/knowledge/memory"
)

// WarmthStore provides ACT-R-compatible branch activation over forest traces.
type WarmthStore struct {
	db        *sql.DB
	maxTraces int
}

func newWarmthStore(db *sql.DB, maxTraces int) *WarmthStore {
	if maxTraces <= 0 {
		maxTraces = 64
	}
	return &WarmthStore{db: db, maxTraces: maxTraces}
}

func (w *WarmthStore) RecordAccess(ctx context.Context, branchID string, accessType memory.AccessType, contextText string) error {
	if branchID == "" {
		return nil
	}

	now := time.Now().UTC()
	_, err := w.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO forest_branch_traces (branch_id, accessed_at, access_type, context)
		VALUES (?, ?, ?, ?)
	`, branchID, now.Unix(), accessType.String(), nullString(contextText))
	if err != nil {
		return fmt.Errorf("insert branch trace: %w", err)
	}

	_, err = w.db.ExecContext(ctx, `
		UPDATE forest_branches
		SET access_count = access_count + 1,
		    last_accessed_at = ?
		WHERE id = ?
	`, now.Unix(), branchID)
	if err != nil {
		return fmt.Errorf("update branch access stats: %w", err)
	}

	return w.prune(ctx, branchID)
}

func (w *WarmthStore) Activation(ctx context.Context, branchID string, family TreeFamily, now time.Time) (float64, error) {
	traceMap, err := w.BatchActivation(ctx, []branchKey{{ID: branchID, Family: family}}, now)
	if err != nil {
		return 0, err
	}
	return traceMap[branchID], nil
}

type branchKey struct {
	ID     string
	Family TreeFamily
}

func (w *WarmthStore) BatchActivation(ctx context.Context, branches []branchKey, now time.Time) (map[string]float64, error) {
	result := make(map[string]float64, len(branches))
	if len(branches) == 0 {
		return result, nil
	}

	placeholders := make([]string, 0, len(branches))
	args := make([]any, 0, len(branches))
	familyByID := make(map[string]TreeFamily, len(branches))
	for _, branch := range branches {
		if branch.ID == "" {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, branch.ID)
		familyByID[branch.ID] = branch.Family
	}
	if len(placeholders) == 0 {
		return result, nil
	}

	rows, err := w.db.QueryContext(ctx, `
		SELECT branch_id, accessed_at, access_type, context
		FROM forest_branch_traces
		WHERE branch_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY branch_id ASC, accessed_at DESC
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query branch traces: %w", err)
	}
	defer rows.Close()

	traceBuckets := make(map[string][]memory.AccessTrace, len(branches))
	for rows.Next() {
		var (
			branchID      string
			accessedAt    int64
			accessTypeRaw string
			contextText   sql.NullString
		)
		if err := rows.Scan(&branchID, &accessedAt, &accessTypeRaw, &contextText); err != nil {
			return nil, fmt.Errorf("scan branch trace: %w", err)
		}
		if len(traceBuckets[branchID]) >= w.maxTraces {
			continue
		}
		accessType, ok := memory.ParseAccessType(accessTypeRaw)
		if !ok {
			accessType = memory.AccessReference
		}
		traceBuckets[branchID] = append(traceBuckets[branchID], memory.AccessTrace{
			AccessedAt: time.Unix(accessedAt, 0).UTC(),
			AccessType: accessType,
			Context:    contextText.String,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate branch traces: %w", err)
	}

	for _, branch := range branches {
		traces := traceBuckets[branch.ID]
		if len(traces) == 0 {
			result[branch.ID] = 0
			continue
		}
		mem := &memory.ACTRMemory{
			NodeID:      branch.ID,
			Domain:      domainIndexForFamily(familyByID[branch.ID]),
			Traces:      traces,
			MaxTraces:   w.maxTraces,
			CreatedAt:   traces[len(traces)-1].AccessedAt,
			AccessCount: len(traces),
		}
		params := memory.DefaultDomainDecay(mem.Domain)
		mem.DecayAlpha = params.DecayAlpha
		mem.DecayBeta = params.DecayBeta
		mem.BaseOffsetMean = params.BaseOffsetMean
		mem.BaseOffsetVariance = params.BaseOffsetVar
		result[branch.ID] = sigmoid(mem.Activation(now))
	}

	return result, nil
}

func (w *WarmthStore) prune(ctx context.Context, branchID string) error {
	rows, err := w.db.QueryContext(ctx, `
		SELECT accessed_at
		FROM forest_branch_traces
		WHERE branch_id = ?
		ORDER BY accessed_at DESC
		LIMIT -1 OFFSET ?
	`, branchID, w.maxTraces)
	if err != nil {
		return fmt.Errorf("query prune traces: %w", err)
	}
	defer rows.Close()

	var cutoff []int64
	for rows.Next() {
		var accessedAt int64
		if err := rows.Scan(&accessedAt); err != nil {
			return fmt.Errorf("scan prune trace: %w", err)
		}
		cutoff = append(cutoff, accessedAt)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate prune traces: %w", err)
	}
	if len(cutoff) == 0 {
		return nil
	}

	_, err = w.db.ExecContext(ctx, `
		DELETE FROM forest_branch_traces
		WHERE branch_id = ? AND accessed_at <= ?
	`, branchID, cutoff[len(cutoff)-1])
	if err != nil {
		return fmt.Errorf("delete prune traces: %w", err)
	}
	return nil
}

func domainIndexForFamily(family TreeFamily) int {
	switch family {
	case TreeFamilyEvidence:
		return 0 // librarian-style evidence
	case TreeFamilyOutcome, TreeFamilyConflict:
		return 2 // archivalist-style outcome memory
	case TreeFamilyOpportunity, TreeFamilyCapability:
		return 4 // engineer-style task utility
	default:
		return 3 // architect-style planning / intent structure
	}
}

func nullString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
