package claims

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	ArtifactKindOutboxRepairReport = "claims_outbox_repair_report"
	outboxRepairActorID            = "sys:claims_outbox_repair"
)

type OutboxRepairOptions struct {
	DryRun          bool
	Limit           int
	Projectors      []string
	BoardID         string
	SessionID       string
	WorkerID        string
	ActorID         string
	EmitReport      bool
	TerminalOnError bool
}

type OutboxRepairReport struct {
	DryRun              bool                       `json:"dry_run"`
	Scanned             int                        `json:"scanned"`
	Matched             int                        `json:"matched"`
	WouldProject        int                        `json:"would_project"`
	Projected           int                        `json:"projected"`
	RetryableFailures   []OutboxRepairFailure      `json:"retryable_failures,omitempty"`
	TerminalFailures    []ProjectionFailureSummary `json:"terminal_failures,omitempty"`
	Skipped             []string                   `json:"skipped,omitempty"`
	Bounded             bool                       `json:"bounded"`
	ReportTestamentID   string                     `json:"report_testament_id,omitempty"`
}

type OutboxRepairFailure struct {
	RecordID   string `json:"record_id"`
	Projector  string `json:"projector"`
	Attempts   int    `json:"attempts"`
	LastError  string `json:"last_error"`
	EntityID   string `json:"entity_id,omitempty"`
	EntityType string `json:"entity_type,omitempty"`
	Sequence   uint64 `json:"sequence,omitempty"`
}

func (db *DurableBoard) RepairOutbox(ctx context.Context, opts OutboxRepairOptions) (OutboxRepairReport, error) {
	if db == nil || db.outbox == nil || db.board == nil {
		return OutboxRepairReport{}, fmt.Errorf("claims outbox repair requires durable board with outbox")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	opts = db.normalizeOutboxRepairOptions(opts)
	report := OutboxRepairReport{DryRun: opts.DryRun}
	projectors := db.repairProjectors(opts.Projectors)
	records := db.outbox.Records()
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Scanned++
		if !outboxRepairRecordMatches(record, opts) {
			continue
		}
		report.Matched++
		for _, projector := range projectors {
			if projector == nil {
				continue
			}
			slot, ok := record.Projectors[projector.Name()]
			if !ok || !slot.Repairable(time.Now().UTC()) {
				continue
			}
			if report.WouldProject >= opts.Limit {
				report.Bounded = true
				return db.finishOutboxRepairReport(ctx, opts, report)
			}
			report.WouldProject++
			if opts.DryRun {
				continue
			}
			projected, failure := db.repairOutboxRecord(ctx, opts, projector, record)
			if projected {
				report.Projected++
				continue
			}
			if failure.LastError != "" {
				report.RetryableFailures = append(report.RetryableFailures, failure)
			}
		}
	}
	report = db.appendTerminalRepairFailures(report, records, projectors)
	return db.finishOutboxRepairReport(ctx, opts, report)
}

func (db *DurableBoard) normalizeOutboxRepairOptions(opts OutboxRepairOptions) OutboxRepairOptions {
	opts.BoardID = strings.TrimSpace(opts.BoardID)
	opts.SessionID = strings.TrimSpace(opts.SessionID)
	opts.WorkerID = firstNonEmpty(strings.TrimSpace(opts.WorkerID), "claims-outbox-repair")
	opts.ActorID = firstNonEmpty(strings.TrimSpace(opts.ActorID), outboxRepairActorID)
	if opts.Limit <= 0 {
		opts.Limit = db.operations.Budgets.OutboxRepairLimit
	}
	return opts
}

func (db *DurableBoard) repairProjectors(names []string) []ClaimsProjector {
	allowed := map[string]struct{}{}
	for _, name := range normalizeProjectorNames(names) {
		allowed[name] = struct{}{}
	}
	out := make([]ClaimsProjector, 0, len(db.projectors))
	for _, projector := range db.projectors {
		if projector == nil {
			continue
		}
		if len(allowed) != 0 {
			if _, ok := allowed[projector.Name()]; !ok {
				continue
			}
		}
		out = append(out, projector)
	}
	return out
}

func outboxRepairRecordMatches(record ClaimsOutboxRecord, opts OutboxRepairOptions) bool {
	if opts.BoardID != "" && record.BoardID != opts.BoardID {
		return false
	}
	if opts.SessionID != "" && record.SessionID != opts.SessionID {
		return false
	}
	return true
}

func (slot OutboxProjectorSlot) Repairable(now time.Time) bool {
	return slotClaimable(slot, now)
}

func (db *DurableBoard) repairOutboxRecord(ctx context.Context, opts OutboxRepairOptions, projector ClaimsProjector, record ClaimsOutboxRecord) (bool, OutboxRepairFailure) {
	ok, err := db.outbox.Claim(record.ID, projector.Name(), opts.WorkerID, time.Now().UTC().Add(db.operations.Budgets.OutboxLease))
	if err != nil {
		db.board.RecordProjectionError(record, projector.Name(), err)
		return false, repairFailure(record, projector.Name(), record.Projectors[projector.Name()].Attempts, err)
	}
	if !ok {
		return false, OutboxRepairFailure{}
	}
	if err := projector.Project(ctx, &record, db.board); err != nil {
		_ = db.outbox.MarkFailed(record.ID, projector.Name(), opts.TerminalOnError, err)
		db.board.RecordProjectionError(record, projector.Name(), err)
		return false, repairFailure(record, projector.Name(), record.Projectors[projector.Name()].Attempts+1, err)
	}
	if err := db.outbox.MarkSucceeded(record.ID, projector.Name()); err != nil {
		db.board.RecordProjectionError(record, projector.Name(), err)
		return false, repairFailure(record, projector.Name(), record.Projectors[projector.Name()].Attempts, err)
	}
	db.board.RecordProjectionSuccess(record, projector.Name())
	return true, OutboxRepairFailure{}
}

func repairFailure(record ClaimsOutboxRecord, projector string, attempts int, err error) OutboxRepairFailure {
	if err == nil {
		return OutboxRepairFailure{}
	}
	return OutboxRepairFailure{
		RecordID:   record.ID,
		Projector:  projector,
		Attempts:   attempts,
		LastError:  err.Error(),
		EntityID:   record.EntityID,
		EntityType: record.EntityType,
		Sequence:   record.Sequence,
	}
}

func (db *DurableBoard) appendTerminalRepairFailures(report OutboxRepairReport, records []ClaimsOutboxRecord, projectors []ClaimsProjector) OutboxRepairReport {
	projectorNames := make(map[string]struct{}, len(projectors))
	for _, projector := range projectors {
		if projector != nil {
			projectorNames[projector.Name()] = struct{}{}
		}
	}
	for _, record := range records {
		for name, slot := range record.Projectors {
			if len(projectorNames) != 0 {
				if _, ok := projectorNames[name]; !ok {
					continue
				}
			}
			if slot.Status != OutboxStatusFailedTerminal {
				continue
			}
			report.TerminalFailures = append(report.TerminalFailures, ProjectionFailureSummary{
				RecordID:     record.ID,
				Sequence:     record.Sequence,
				EntityType:   record.EntityType,
				EntityID:     record.EntityID,
				MutationKind: record.MutationKind,
				LastError:    slot.LastError,
			})
			if len(report.TerminalFailures) >= db.operations.Budgets.AuditFindingLimit {
				return report
			}
		}
	}
	return report
}

func (db *DurableBoard) finishOutboxRepairReport(ctx context.Context, opts OutboxRepairOptions, report OutboxRepairReport) (OutboxRepairReport, error) {
	if !opts.EmitReport || opts.DryRun {
		return report, nil
	}
	artifact := &Artifact{
		AgentID:      opts.ActorID,
		ArtifactName: "claims_outbox_repair_report",
		Kind:         ArtifactKindOutboxRepairReport,
		Reference:    fmt.Sprintf("outbox repair projected=%d failures=%d", report.Projected, len(report.RetryableFailures)+len(report.TerminalFailures)),
		Metadata: map[string]any{
			"dry_run":            report.DryRun,
			"projected":          report.Projected,
			"retryable_failures": len(report.RetryableFailures),
			"terminal_failures":  len(report.TerminalFailures),
			"bounded":            report.Bounded,
		},
	}
	result, err := RecordInfrastructureEvidence(ctx, InfrastructureEvidenceOptions{
		Board:     db.board,
		ActorID:   opts.ActorID,
		SubjectID: opts.ActorID,
		Operation: "claims_outbox_repair",
		Artifact:  artifact,
	})
	report.ReportTestamentID = result.TestamentID
	return report, err
}
