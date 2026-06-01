//go:build forest_legacy_archive

package forest

import (
	"context"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #10 Phase A — pruner tests
// ────────────────────────────────────────────────────────────────────

func TestStoragePruner_BranchTraceKeepsRecent(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()
	forest.branchTraceRetentionPerBranch = 5

	// Insert 12 trace rows for one branch.
	branchID := "branch-trace-test"
	for i := 0; i < 12; i++ {
		ts := time.Now().UTC().Add(time.Duration(i) * time.Second).Unix()
		_, err := db.Exec(`
			INSERT INTO forest_branch_traces (branch_id, accessed_at, access_type, context)
			VALUES (?, ?, ?, '')
		`, branchID, ts, "retrieval")
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := forest.pruneOneBranchTraces(ctx, branchID); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_branch_traces WHERE branch_id = ?`, branchID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("got %d traces, want 5 (retention)", count)
	}
}

func TestStoragePruner_BranchTraceLoadExcessIdentifiesBranches(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()
	forest.branchTraceRetentionPerBranch = 3

	// Branch A: 5 traces (excess); branch B: 2 traces (within limit).
	for i := 0; i < 5; i++ {
		_, err := db.Exec(`
			INSERT INTO forest_branch_traces (branch_id, accessed_at, access_type, context)
			VALUES (?, ?, 'retrieval', '')
		`, "a", time.Now().UTC().Add(time.Duration(i)*time.Second).Unix())
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		_, err := db.Exec(`
			INSERT INTO forest_branch_traces (branch_id, accessed_at, access_type, context)
			VALUES (?, ?, 'retrieval', '')
		`, "b", time.Now().UTC().Add(time.Duration(i)*time.Second).Unix())
		if err != nil {
			t.Fatal(err)
		}
	}

	excess, err := forest.loadBranchesWithExcessTraces(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(excess) != 1 || excess[0] != "a" {
		t.Errorf("expected only branch a; got %v", excess)
	}
}

func TestStoragePruner_BranchTraceResolverDefaults(t *testing.T) {
	if got := resolveBranchTraceRetentionPerBranch(0); got != defaultBranchTraceRetentionPerBranch {
		t.Errorf("retention default: got %d", got)
	}
	if got := resolveBranchTraceRetentionPerBranch(-1); got != defaultBranchTraceRetentionPerBranch {
		t.Errorf("retention default on negative: got %d", got)
	}
	if got := resolveBranchTracePruneInterval(0); got != defaultBranchTracePruneInterval {
		t.Errorf("interval default: got %v", got)
	}
}

func TestStoragePruner_TrainingExamplesSkipsUnlabeled(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()
	forest.trainingExamplesRetention = 1 * time.Second

	// Insert 1 unlabeled (should NOT be pruned) and 1 labeled-and-old
	// (SHOULD be pruned).
	now := time.Now().UTC().Unix()
	old := time.Now().UTC().Add(-time.Hour).Unix()

	_, err := db.Exec(`
		INSERT INTO forest_training_examples
		(retrieval_id, branch_id, root_id, session_id, caller_agent_type, branch_agent_type,
		 rank_position, base_score, predicted_utility, predicted_risk, features, metadata, created_at, updated_at)
		VALUES ('r1', 'b1', 'root', 's', '', '', 0, 0, 0, 0, X'00', '{}', ?, ?)
	`, old, old)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO forest_training_examples
		(retrieval_id, branch_id, root_id, session_id, caller_agent_type, branch_agent_type,
		 rank_position, base_score, predicted_utility, predicted_risk, features, metadata, utility_label, created_at, updated_at)
		VALUES ('r2', 'b2', 'root', 's', '', '', 0, 0, 0, 0, X'00', '{}', 0.8, ?, ?)
	`, old, old)
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := forest.runTrainingExamplesPruneOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Errorf("expected to delete 1 (labeled+old); got %d", deleted)
	}

	var unlabeled, labeled int
	db.QueryRow(`SELECT COUNT(*) FROM forest_training_examples WHERE retrieval_id = 'r1'`).Scan(&unlabeled)
	db.QueryRow(`SELECT COUNT(*) FROM forest_training_examples WHERE retrieval_id = 'r2'`).Scan(&labeled)
	if unlabeled != 1 {
		t.Errorf("unlabeled row should remain; got %d", unlabeled)
	}
	if labeled != 0 {
		t.Errorf("labeled+old row should be pruned; got %d", labeled)
	}

	_ = now
}

func TestStoragePruner_SubstrateStateInactiveSession(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()
	forest.substrateStateRetention = 1 * time.Hour

	// Inactive session: substrate_sessions row exists but no branches.
	_, err := db.Exec(`
		INSERT INTO forest_substrate_sessions
		(session_id, graph_version, substrate_version, dirty, dirty_at, updated_at)
		VALUES ('orphan', 0, 0, 0, 0, 0)
	`)
	if err != nil {
		t.Fatal(err)
	}

	inactive, err := forest.findInactiveSubstrateSessions(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(inactive) != 1 || inactive[0] != "orphan" {
		t.Errorf("expected orphan inactive; got %v", inactive)
	}

	if err := forest.dropSubstrateForSessions(ctx, inactive); err != nil {
		t.Fatal(err)
	}

	var remaining int
	db.QueryRow(`SELECT COUNT(*) FROM forest_substrate_sessions WHERE session_id = 'orphan'`).Scan(&remaining)
	if remaining != 0 {
		t.Errorf("orphan session not pruned; remaining=%d", remaining)
	}
}

func TestStoragePruner_SubstrateStateActiveSessionPreserved(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()
	forest.substrateStateRetention = 1 * time.Hour
	now := time.Now().UTC().Unix()

	// Active: substrate session with a recent branch update.
	_, err := db.Exec(`
		INSERT INTO forest_substrate_sessions
		(session_id, graph_version, substrate_version, dirty, dirty_at, updated_at)
		VALUES ('active', 0, 0, 0, 0, ?)
	`, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO forest_branches
		(id, root_id, family, scope, state, session_id, title, summary, confidence, salience, utility,
		 success_rate, scope_risk, conflict_score, support_count, counter_count, success_count, failure_count,
		 access_count, last_accessed_at, created_at, updated_at, metadata, last_applied_seq)
		VALUES ('br', 'root', ?, ?, ?, 'active', 't', '', 0.5, 0.5, 0.5, 0, 0, 0, 0, 0, 0, 0, 0, ?, ?, ?, '{}', 0)
	`, string(TreeFamilyDecision), string(ScopeWorking), string(BranchStateActive), now, now, now)
	if err != nil {
		t.Fatal(err)
	}

	inactive, err := forest.findInactiveSubstrateSessions(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	for _, sid := range inactive {
		if sid == "active" {
			t.Errorf("active session was flagged for pruning")
		}
	}
}

func TestStoragePruner_ResolverDefaults_All(t *testing.T) {
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"training retention", resolveTrainingExamplesRetention(0), defaultTrainingExamplesRetention},
		{"training interval", resolveTrainingExamplesPruneInterval(0), defaultTrainingExamplesPruneInterval},
		{"substrate retention", resolveSubstrateStateRetention(0), defaultSubstrateStateRetention},
		{"substrate interval", resolveSubstrateStatePruneInterval(0), defaultSubstrateStatePruneInterval},
		{"event archive age", resolveEventArchiveAge(0), defaultEventArchiveAge},
		{"retrieval archive age", resolveRetrievalEventArchiveAge(0), defaultRetrievalEventArchiveAge},
		{"archive interval", resolveEventArchiveInterval(0), defaultEventArchiveInterval},
		{"archive batch", resolveEventArchiveBatchSize(0), defaultEventArchiveBatchSize},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %v want %v", c.got, c.want)
			}
		})
	}
}

func TestStoragePruner_BuildSessionInArgs(t *testing.T) {
	placeholders, args := buildSessionInArgs([]string{"s1", "s2", "s3"})
	if placeholders != "?,?,?" {
		t.Errorf("placeholders: got %q want ?,?,?", placeholders)
	}
	if len(args) != 3 {
		t.Errorf("args len: got %d want 3", len(args))
	}
}
