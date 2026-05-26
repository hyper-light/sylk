package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const ArtifactKindProjectionRebuildReport = "projection_rebuild_report"

type ProjectionRebuildOptions struct {
	Projectors            []string `json:"projectors,omitempty"`
	FromSequence          uint64   `json:"from_sequence,omitempty"`
	ToSequence            uint64   `json:"to_sequence,omitempty"`
	Limit                 int      `json:"limit,omitempty"`
	DryRun                bool     `json:"dry_run,omitempty"`
	ResumeFromLastSuccess bool     `json:"resume_from_last_success,omitempty"`
	EmitReport            bool     `json:"emit_report,omitempty"`
	Worker                string   `json:"worker,omitempty"`
}

type ProjectionRebuildResult struct {
	BoardID           string                          `json:"board_id,omitempty"`
	SessionID         string                          `json:"session_id,omitempty"`
	DryRun            bool                            `json:"dry_run"`
	Interrupted       bool                            `json:"interrupted,omitempty"`
	SelectedRecords   int                             `json:"selected_records"`
	Projected         int                             `json:"projected"`
	Succeeded         int                             `json:"succeeded"`
	Failed            int                             `json:"failed"`
	Skipped           int                             `json:"skipped"`
	LastSequence      uint64                          `json:"last_sequence,omitempty"`
	ResumeSequence    uint64                          `json:"resume_sequence,omitempty"`
	Projectors        []ProjectionRebuildProjectorRun `json:"projectors,omitempty"`
	Records           []ProjectionRebuildRecordRun    `json:"records,omitempty"`
	Warnings          []string                        `json:"warnings,omitempty"`
	ReportTestamentID string                          `json:"report_testament_id,omitempty"`
}

type ProjectionRebuildProjectorRun struct {
	Projector string `json:"projector"`
	Selected  int    `json:"selected"`
	Projected int    `json:"projected"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
	Skipped   int    `json:"skipped"`
}

type ProjectionRebuildRecordRun struct {
	RecordID     string `json:"record_id"`
	Projector    string `json:"projector"`
	Sequence     uint64 `json:"sequence"`
	EntityType   string `json:"entity_type"`
	EntityID     string `json:"entity_id"`
	MutationKind string `json:"mutation_kind"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
}

func (b *ClaimsBoard) RebuildProjections(ctx context.Context, opts ProjectionRebuildOptions) (*ProjectionRebuildResult, error) {
	if b == nil {
		return nil, fmt.Errorf("claims board is required")
	}
	if b.durable == nil {
		return nil, fmt.Errorf("claims board %s is not durable; projection rebuild requires durable outbox records", b.BoardID())
	}
	return b.durable.RebuildProjections(ctx, opts)
}

func (db *DurableBoard) RebuildProjections(ctx context.Context, opts ProjectionRebuildOptions) (*ProjectionRebuildResult, error) {
	if db == nil || db.board == nil {
		return nil, fmt.Errorf("durable board is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := &ProjectionRebuildResult{
		BoardID:   db.board.BoardID(),
		SessionID: db.board.SessionID(),
		DryRun:    opts.DryRun,
	}
	if db.outbox == nil {
		result.Warnings = append(result.Warnings, "claims projection outbox is unavailable")
		return result, nil
	}
	projectors := db.rebuildProjectors(opts.Projectors)
	if len(projectors) == 0 {
		return nil, fmt.Errorf("no matching claims projectors for rebuild")
	}
	records := selectRebuildRecords(db.outbox.Records(), opts)
	if opts.Limit > 0 && len(records) > opts.Limit {
		records = records[:opts.Limit]
	}
	result.SelectedRecords = len(records)
	byProjector := make(map[string]*ProjectionRebuildProjectorRun)
	for _, p := range projectors {
		if p == nil {
			continue
		}
		byProjector[p.Name()] = &ProjectionRebuildProjectorRun{Projector: p.Name()}
	}
	for _, rec := range records {
		if err := ctx.Err(); err != nil {
			result.Interrupted = true
			result.ResumeSequence = result.LastSequence
			result.Warnings = append(result.Warnings, "projection rebuild interrupted: "+err.Error())
			if opts.EmitReport {
				db.submitProjectionRebuildReport(context.Background(), result)
			}
			return result, nil
		}
		for _, projector := range projectors {
			if projector == nil {
				continue
			}
			name := projector.Name()
			run := byProjector[name]
			if run == nil {
				continue
			}
			slot, hasSlot := rec.Projectors[name]
			if !hasSlot {
				run.Skipped++
				result.Skipped++
				continue
			}
			if opts.ResumeFromLastSuccess && slot.Status == OutboxStatusSucceeded {
				run.Skipped++
				result.Skipped++
				result.Records = append(result.Records, rebuildRecordRun(rec, name, "skipped_succeeded", nil))
				continue
			}
			run.Selected++
			if opts.DryRun {
				result.Records = append(result.Records, rebuildRecordRun(rec, name, "dry_run", nil))
				continue
			}
			run.Projected++
			result.Projected++
			err := projector.Project(ctx, &rec, db.board)
			if err != nil {
				run.Failed++
				result.Failed++
				_ = db.outbox.MarkFailed(rec.ID, name, false, err)
				db.board.RecordProjectionError(rec, name, err)
				result.Records = append(result.Records, rebuildRecordRun(rec, name, "failed", err))
				continue
			}
			run.Succeeded++
			result.Succeeded++
			_ = db.outbox.MarkSucceeded(rec.ID, name)
			db.board.RecordProjectionSuccess(rec, name)
			result.LastSequence = rec.Sequence
			result.Records = append(result.Records, rebuildRecordRun(rec, name, "succeeded", nil))
		}
	}
	names := make([]string, 0, len(byProjector))
	for name := range byProjector {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		result.Projectors = append(result.Projectors, *byProjector[name])
	}
	if opts.EmitReport {
		db.submitProjectionRebuildReport(ctx, result)
	}
	return result, nil
}

func (db *DurableBoard) rebuildProjectors(names []string) []ClaimsProjector {
	if db == nil {
		return nil
	}
	want := normalizeProjectorNames(names)
	if len(want) == 0 {
		return append([]ClaimsProjector(nil), db.projectors...)
	}
	allowed := make(map[string]struct{}, len(want))
	for _, name := range want {
		allowed[name] = struct{}{}
	}
	var out []ClaimsProjector
	for _, projector := range db.projectors {
		if projector == nil {
			continue
		}
		if _, ok := allowed[projector.Name()]; ok {
			out = append(out, projector)
		}
	}
	return out
}

func selectRebuildRecords(records []ClaimsOutboxRecord, opts ProjectionRebuildOptions) []ClaimsOutboxRecord {
	out := make([]ClaimsOutboxRecord, 0, len(records))
	for _, rec := range records {
		if opts.FromSequence > 0 && rec.Sequence < opts.FromSequence {
			continue
		}
		if opts.ToSequence > 0 && rec.Sequence > opts.ToSequence {
			continue
		}
		out = append(out, rec)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sequence != out[j].Sequence {
			return out[i].Sequence < out[j].Sequence
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func rebuildRecordRun(rec ClaimsOutboxRecord, projector, status string, err error) ProjectionRebuildRecordRun {
	run := ProjectionRebuildRecordRun{
		RecordID:     rec.ID,
		Projector:    projector,
		Sequence:     rec.Sequence,
		EntityType:   rec.EntityType,
		EntityID:     rec.EntityID,
		MutationKind: rec.MutationKind,
		Status:       status,
	}
	if err != nil {
		run.Error = err.Error()
	}
	return run
}

func (db *DurableBoard) submitProjectionRebuildReport(ctx context.Context, result *ProjectionRebuildResult) {
	if db == nil || db.board == nil || result == nil {
		return
	}
	payload, _ := json.Marshal(result)
	reference := fmt.Sprintf("claims projection rebuild board=%s selected=%d succeeded=%d failed=%d dry_run=%t", result.BoardID, result.SelectedRecords, result.Succeeded, result.Failed, result.DryRun)
	artifact := &Artifact{
		AgentID:   projectionDiagnosticsAgentID,
		SessionID: result.SessionID,
		Kind:      ArtifactKindProjectionRebuildReport,
		Reference: string(payload),
		Metadata: map[string]any{
			"board_id":         result.BoardID,
			"session_id":       result.SessionID,
			"selected_records": result.SelectedRecords,
			"succeeded":        result.Succeeded,
			"failed":           result.Failed,
			"dry_run":          result.DryRun,
		},
	}
	artifacts := []*Artifact{artifact}
	if result.Failed > 0 {
		artifacts = append(artifacts, &Artifact{
			AgentID:   projectionDiagnosticsAgentID,
			SessionID: result.SessionID,
			Kind:      ArtifactKindErrorDiagnostic,
			Reference: strings.Join(result.Warnings, "\n"),
			Metadata: map[string]any{
				"operation": "projection_rebuild",
				"board_id":  result.BoardID,
				"failed":    result.Failed,
			},
		})
	}
	testament := Testament{
		AgentID:    projectionDiagnosticsAgentID,
		SessionID:  result.SessionID,
		Summary:    reference,
		Confidence: "committed",
		Artifacts:  artifacts,
	}
	if err := db.board.SubmitTestaments(ctx, Action{AgentID: projectionDiagnosticsAgentID, Type: ActionTypeTestament}, []Testament{testament}); err != nil {
		db.board.RecordNotificationError("projection rebuild report testament: " + err.Error())
		return
	}
	for _, t := range db.board.Projection().Testaments {
		if t.Summary == reference {
			result.ReportTestamentID = t.ID
			return
		}
	}
}
