package claims

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeatureFlags_DurableBoardOnly(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultRolloutConfig()
	cfg.ClaimsOutbox = false
	cfg.ClaimsKnowledgeMirror = ProjectionRolloutOff
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:       "board-1",
		SessionID:     "session-1",
		TaskID:        "session",
		SessionDir:    filepath.Join(dir, "session-1"),
		DisableOutbox: true,
		Rollout:       cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "guide", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	if got := db.DrainOutbox(context.Background(), 32); got != 0 {
		t.Fatalf("drain outbox = %d, want 0 when outbox disabled", got)
	}
	if health := db.ProjectionHealth(); health.FeatureFlags[EnvClaimsOutbox] != "0" || !diagnosticsContain(health.Warnings, "no projection outbox") {
		t.Fatalf("health = %+v, want disabled outbox flag and warning", health)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if !DurableBoardWALExists(filepath.Join(dir, "session-1"), "board-1") {
		t.Fatal("durable board-only mode did not preserve canonical WAL")
	}
	reopened, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:       "board-1",
		SessionID:     "session-1",
		TaskID:        "session",
		SessionDir:    filepath.Join(dir, "session-1"),
		DisableOutbox: true,
		Rollout:       cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, ok := reopened.Board().CloneClaim("claim-1"); !ok {
		t.Fatal("claim not recovered from WAL in durable board-only mode")
	}
}

func TestFeatureFlags_ShadowPath(t *testing.T) {
	cfg := DefaultRolloutConfig()
	cfg.ClaimsKnowledgeMirror = ProjectionRolloutShadow
	projector := &countingProjector{name: ProjectorKnowledge}
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "session",
		SessionDir: filepath.Join(t.TempDir(), "session-1"),
		Projectors: []ClaimsProjector{
			projector,
		},
		Rollout: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "guide", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	health := db.ProjectionHealth()
	if health.FeatureFlags[EnvClaimsKnowledgeMirror] != string(ProjectionRolloutShadow) {
		t.Fatalf("health feature flags = %+v, want shadow mirror", health.FeatureFlags)
	}
	if len(health.ShadowDiffs) == 0 || !strings.Contains(health.ShadowDiffs[0], "shadow projection diff") {
		t.Fatalf("shadow diffs missing: %+v", health.ShadowDiffs)
	}
	recall, err := RecallForward(context.Background(), db.Board(), RecallForwardOptions{AgentID: "guide", Topic: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if !diagnosticsContain(recall.Diagnostics, "shadow projection diff") {
		t.Fatalf("recall diagnostics missing shadow diff: %+v", recall.Diagnostics)
	}
}

func TestRolloutConfigFromEnv(t *testing.T) {
	values := map[string]string{
		EnvDurableSessionClaims:      "0",
		EnvClaimsOutbox:              "false",
		EnvClaimsKnowledgeMirror:     "authoritative",
		EnvRecallForwardCrossSession: "off",
		EnvScribeContinuityNarration: "no",
	}
	cfg := RolloutConfigFromEnv(func(key string) string { return values[key] })
	if cfg.DurableSessionClaims || cfg.ClaimsOutbox || cfg.RecallForwardCrossSession || cfg.ScribeContinuityNarration {
		t.Fatalf("boolean rollout flags not parsed: %+v", cfg)
	}
	if cfg.ClaimsKnowledgeMirror != ProjectionRolloutAuthoritative || !cfg.ProjectionWarningsAuthoritative() {
		t.Fatalf("mirror rollout mode not parsed as authoritative: %+v", cfg)
	}
}
