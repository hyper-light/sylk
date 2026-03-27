package architect

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	coreversioning "github.com/adalundhe/sylk/core/versioning"
)

func TestApplyPlanRevision_CreatesNewVersionedArtifactsAndRotatesPlanMode(t *testing.T) {
	now := time.Now().UTC()
	plan := &DesignPlan{
		ID:        "plan-revise",
		SessionID: "sess-1",
		Query:     "Build the feature",
		Revision:  1,
		Status:    PlanStatusReady,
		CreatedAt: now,
		UpdatedAt: now,
	}
	plan.sm = NewPlanStateMachine(plan.ID, plan.Status)

	a := testArchitectWithControlStore(t, plan)
	a.config.WorkingDirectory = a.planStore.baseDir
	a.planModes = make(map[string]*PlanModeState)

	current := a.planStore.Get(plan.ID)
	if current == nil {
		t.Fatal("expected stored plan")
	}
	oldVersion := current.ArtifactVersion
	oldPlanFile := current.PlanFile
	oldSnapshot := versionedPlanSnapshotPath(a.planStore.baseDir, current.SessionID, current.ID, oldVersion)
	if _, err := os.Stat(oldSnapshot); err != nil {
		t.Fatalf("expected original snapshot to exist: %v", err)
	}
	if _, err := os.Stat(oldPlanFile); err != nil {
		t.Fatalf("expected original plan markdown to exist: %v", err)
	}

	a.planModes[normalizeSessionID(current.SessionID)] = &PlanModeState{
		SessionID: current.SessionID,
		PlanID:    "mode-1",
		Enabled:   true,
		PlanFile:  oldPlanFile,
		UpdatedAt: now,
	}

	updated := a.applyPlanRevision(current, "user requested revision", nil)
	if updated.Revision != 2 {
		t.Fatalf("revision = %d, want 2", updated.Revision)
	}
	if updated.ArtifactVersion != (coreversioning.SemanticVersion{Major: 1, Minor: 1}) {
		t.Fatalf("artifact version = %s, want 1.1", updated.ArtifactVersion.String())
	}
	if samePath(updated.PlanFile, oldPlanFile) {
		t.Fatalf("plan file = %q, want rotated versioned path", updated.PlanFile)
	}

	if _, err := os.Stat(oldPlanFile); err != nil {
		t.Fatalf("expected original plan markdown to remain: %v", err)
	}
	if _, err := os.Stat(updated.PlanFile); err != nil {
		t.Fatalf("expected revised plan markdown to exist: %v", err)
	}

	newSnapshot := versionedPlanSnapshotPath(a.planStore.baseDir, updated.SessionID, updated.ID, updated.ArtifactVersion)
	if _, err := os.Stat(newSnapshot); err != nil {
		t.Fatalf("expected revised snapshot to exist: %v", err)
	}
	if _, err := os.Stat(oldSnapshot); err != nil {
		t.Fatalf("expected original snapshot to remain: %v", err)
	}

	mode, err := a.getPlanMode(updated.SessionID)
	if err != nil {
		t.Fatalf("getPlanMode: %v", err)
	}
	if !samePath(mode.PlanFile, updated.PlanFile) {
		t.Fatalf("plan mode file = %q, want %q", mode.PlanFile, updated.PlanFile)
	}
}

func TestPlanStore_RestoreFromDisk_PrefersLatestArtifactVersion(t *testing.T) {
	dir := t.TempDir()
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	now := time.Now().UTC()

	writeSnapshot := func(plan DesignPlan) {
		t.Helper()
		path := versionedPlanSnapshotPath(dir, plan.SessionID, plan.ID, plan.ArtifactVersion)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir snapshot dir: %v", err)
		}
		data, err := json.MarshalIndent(&plan, "", "  ")
		if err != nil {
			t.Fatalf("marshal snapshot: %v", err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write snapshot: %v", err)
		}
	}

	v10 := DesignPlan{
		ID:              "plan-restore",
		SessionID:       "sess-1",
		Query:           "Build the feature",
		Revision:        1,
		ArtifactVersion: coreversioning.SemanticVersion{Major: 1, Minor: 0},
		PlanFile:        defaultVersionedPlanMarkdownPath(dir, "sess-1", "Build the feature", "plan-restore", coreversioning.SemanticVersion{Major: 1, Minor: 0}),
		Status:          PlanStatusReady,
		CreatedAt:       now.Add(-10 * time.Minute),
		UpdatedAt:       now.Add(-5 * time.Minute),
	}
	v11 := DesignPlan{
		ID:              "plan-restore",
		SessionID:       "sess-1",
		Query:           "Build the feature",
		Revision:        2,
		ArtifactVersion: coreversioning.SemanticVersion{Major: 1, Minor: 1},
		PlanFile:        defaultVersionedPlanMarkdownPath(dir, "sess-1", "Build the feature", "plan-restore", coreversioning.SemanticVersion{Major: 1, Minor: 1}),
		Status:          PlanStatusExecuting,
		CreatedAt:       now.Add(-10 * time.Minute),
		UpdatedAt:       now.Add(-2 * time.Minute),
	}
	writeSnapshot(v10)
	writeSnapshot(v11)

	store := NewPlanStore(dir, lm, slogDiscardLogger())
	defer store.Close()

	got := store.Get("plan-restore")
	if got == nil {
		t.Fatal("expected restored plan")
	}
	if got.ArtifactVersion != v11.ArtifactVersion {
		t.Fatalf("artifact version = %s, want %s", got.ArtifactVersion.String(), v11.ArtifactVersion.String())
	}
	if got.Revision != v11.Revision {
		t.Fatalf("revision = %d, want %d", got.Revision, v11.Revision)
	}
	if got.SM().State() != PlanStatusExecuting {
		t.Fatalf("state = %s, want executing", got.SM().State())
	}
	if !samePath(got.PlanFile, v11.PlanFile) {
		t.Fatalf("plan file = %q, want %q", got.PlanFile, v11.PlanFile)
	}
}

func TestResolvePlanFile_CustomPathUsesVersionedSibling(t *testing.T) {
	root := t.TempDir()

	got := resolvePlanFile(root, "sess-1", "Build the feature", "plan-1", filepath.Join("docs", "plan.md"))
	want := filepath.Join(root, "docs", "plan_v1.0.md")
	if !samePath(got, want) {
		t.Fatalf("resolvePlanFile = %q, want %q", got, want)
	}
}

func slogDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}
