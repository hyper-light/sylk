package claims

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	OutboxStatusPending         OutboxProjectorStatus = "pending"
	OutboxStatusInProgress      OutboxProjectorStatus = "in_progress"
	OutboxStatusSucceeded       OutboxProjectorStatus = "succeeded"
	OutboxStatusFailedRetryable OutboxProjectorStatus = "failed_retryable"
	OutboxStatusFailedTerminal  OutboxProjectorStatus = "failed_terminal"

	defaultOutboxLease = 30 * time.Second
)

type OutboxProjectorStatus string

type ClaimsOutboxRecord struct {
	ID           string                         `json:"id"`
	BoardID      string                         `json:"board_id"`
	SessionID    string                         `json:"session_id,omitempty"`
	TaskID       string                         `json:"task_id,omitempty"`
	Sequence     uint64                         `json:"sequence"`
	EntityType   string                         `json:"entity_type"`
	EntityID     string                         `json:"entity_id"`
	MutationKind string                         `json:"mutation_kind"`
	CreatedAt    time.Time                      `json:"created_at"`
	Projectors   map[string]OutboxProjectorSlot `json:"projectors"`
}

type OutboxProjectorSlot struct {
	Status     OutboxProjectorStatus `json:"status"`
	Attempts   int                   `json:"attempts,omitempty"`
	LeaseOwner string                `json:"lease_owner,omitempty"`
	LeaseUntil time.Time             `json:"lease_until,omitempty"`
	LastError  string                `json:"last_error,omitempty"`
	UpdatedAt  time.Time             `json:"updated_at,omitempty"`
}

type claimsOutboxEvent struct {
	Kind       string                `json:"kind"`
	Record     *ClaimsOutboxRecord   `json:"record,omitempty"`
	RecordID   string                `json:"record_id,omitempty"`
	Projector  string                `json:"projector,omitempty"`
	Status     OutboxProjectorStatus `json:"status,omitempty"`
	LeaseOwner string                `json:"lease_owner,omitempty"`
	LeaseUntil time.Time             `json:"lease_until,omitempty"`
	LastError  string                `json:"last_error,omitempty"`
	UpdatedAt  time.Time             `json:"updated_at,omitempty"`
}

type ClaimsOutbox struct {
	mu         sync.Mutex
	path       string
	file       *os.File
	projectors []string
	records    map[string]*ClaimsOutboxRecord
	order      []string
}

func OpenClaimsOutbox(dir string, projectors []string) (*ClaimsOutbox, error) {
	o := &ClaimsOutbox{
		projectors: normalizeProjectorNames(projectors),
		records:    make(map[string]*ClaimsOutboxRecord),
	}
	if strings.TrimSpace(dir) == "" {
		return o, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create claims outbox dir: %w", err)
	}
	o.path = filepath.Join(dir, "events.outbox.jsonl")
	if err := o.replay(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(o.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open claims outbox: %w", err)
	}
	o.file = f
	return o, nil
}

func (o *ClaimsOutbox) Close() error {
	if o == nil || o.file == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.file.Close()
}

func (o *ClaimsOutbox) Insert(record ClaimsOutboxRecord) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.insertLocked(record, true)
}

func (o *ClaimsOutbox) InsertMany(records []ClaimsOutboxRecord) error {
	if o == nil || len(records) == 0 {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, record := range records {
		if err := o.insertLocked(record, true); err != nil {
			return err
		}
	}
	return nil
}

func (o *ClaimsOutbox) Pending(projector string, limit int, now time.Time) []ClaimsOutboxRecord {
	if o == nil {
		return nil
	}
	projector = strings.TrimSpace(projector)
	if projector == "" {
		return nil
	}
	if limit <= 0 {
		limit = 64
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]ClaimsOutboxRecord, 0, limit)
	for _, id := range o.order {
		rec := o.records[id]
		if rec == nil {
			continue
		}
		slot, ok := rec.Projectors[projector]
		if !ok || !slotClaimable(slot, now) {
			continue
		}
		out = append(out, cloneOutboxRecord(rec))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (o *ClaimsOutbox) Claim(recordID, projector, worker string, leaseUntil time.Time) (bool, error) {
	if o == nil {
		return false, nil
	}
	projector = strings.TrimSpace(projector)
	worker = strings.TrimSpace(worker)
	if projector == "" {
		return false, nil
	}
	if worker == "" {
		worker = "claims-projector"
	}
	if leaseUntil.IsZero() {
		leaseUntil = time.Now().UTC().Add(defaultOutboxLease)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	rec := o.records[recordID]
	if rec == nil {
		return false, nil
	}
	slot, ok := rec.Projectors[projector]
	if !ok || !slotClaimable(slot, time.Now().UTC()) {
		return false, nil
	}
	slot.Status = OutboxStatusInProgress
	slot.LeaseOwner = worker
	slot.LeaseUntil = leaseUntil.UTC()
	slot.UpdatedAt = time.Now().UTC()
	rec.Projectors[projector] = slot
	return true, o.appendLocked(claimsOutboxEvent{
		Kind:       "projector_status",
		RecordID:   recordID,
		Projector:  projector,
		Status:     slot.Status,
		LeaseOwner: slot.LeaseOwner,
		LeaseUntil: slot.LeaseUntil,
		UpdatedAt:  slot.UpdatedAt,
	})
}

func (o *ClaimsOutbox) MarkSucceeded(recordID, projector string) error {
	return o.setProjectorStatus(recordID, projector, OutboxStatusSucceeded, "")
}

func (o *ClaimsOutbox) MarkFailed(recordID, projector string, terminal bool, err error) error {
	status := OutboxStatusFailedRetryable
	if terminal {
		status = OutboxStatusFailedTerminal
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return o.setProjectorStatus(recordID, projector, status, msg)
}

func (o *ClaimsOutbox) Records() []ClaimsOutboxRecord {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]ClaimsOutboxRecord, 0, len(o.order))
	for _, id := range o.order {
		if rec := o.records[id]; rec != nil {
			out = append(out, cloneOutboxRecord(rec))
		}
	}
	return out
}

func (o *ClaimsOutbox) ProjectPending(ctx context.Context, board *ClaimsBoard, projectors []ClaimsProjector, limit int) {
	if o == nil || board == nil || len(projectors) == 0 {
		return
	}
	for _, projector := range projectors {
		if projector == nil {
			continue
		}
		name := projector.Name()
		records := o.Pending(name, limit, time.Now().UTC())
		for _, rec := range records {
			if err := ctx.Err(); err != nil {
				return
			}
			ok, err := o.Claim(rec.ID, name, "claims-board", time.Now().UTC().Add(defaultOutboxLease))
			if err != nil {
				board.RecordProjectionError(rec, name, err)
				continue
			}
			if !ok {
				continue
			}
			if err := projector.Project(ctx, &rec, board); err != nil {
				_ = o.MarkFailed(rec.ID, name, false, err)
				board.RecordProjectionError(rec, name, err)
				continue
			}
			_ = o.MarkSucceeded(rec.ID, name)
		}
	}
}

func (o *ClaimsOutbox) insertLocked(record ClaimsOutboxRecord, appendEvent bool) error {
	record.ID = stableOutboxRecordID(record)
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if record.Projectors == nil {
		record.Projectors = make(map[string]OutboxProjectorSlot, len(o.projectors))
	}
	now := time.Now().UTC()
	for _, name := range o.projectors {
		if _, ok := record.Projectors[name]; !ok {
			record.Projectors[name] = OutboxProjectorSlot{Status: OutboxStatusPending, UpdatedAt: now}
		}
	}
	if existing := o.records[record.ID]; existing != nil {
		for name, slot := range record.Projectors {
			if _, ok := existing.Projectors[name]; !ok {
				existing.Projectors[name] = slot
			}
		}
		return nil
	}
	cp := cloneOutboxRecord(&record)
	o.records[record.ID] = &cp
	o.order = append(o.order, record.ID)
	sort.SliceStable(o.order, func(i, j int) bool {
		a, b := o.records[o.order[i]], o.records[o.order[j]]
		if a == nil || b == nil {
			return o.order[i] < o.order[j]
		}
		if a.BoardID != b.BoardID {
			return a.BoardID < b.BoardID
		}
		if a.Sequence != b.Sequence {
			return a.Sequence < b.Sequence
		}
		return a.ID < b.ID
	})
	if !appendEvent {
		return nil
	}
	return o.appendLocked(claimsOutboxEvent{Kind: "record", Record: &record})
}

func (o *ClaimsOutbox) setProjectorStatus(recordID, projector string, status OutboxProjectorStatus, lastErr string) error {
	if o == nil {
		return nil
	}
	projector = strings.TrimSpace(projector)
	if projector == "" {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	rec := o.records[recordID]
	if rec == nil {
		return nil
	}
	slot := rec.Projectors[projector]
	slot.Status = status
	slot.LeaseOwner = ""
	slot.LeaseUntil = time.Time{}
	slot.UpdatedAt = time.Now().UTC()
	slot.LastError = lastErr
	if status == OutboxStatusFailedRetryable || status == OutboxStatusFailedTerminal {
		slot.Attempts++
	}
	rec.Projectors[projector] = slot
	return o.appendLocked(claimsOutboxEvent{
		Kind:      "projector_status",
		RecordID:  recordID,
		Projector: projector,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: slot.UpdatedAt,
	})
}

func (o *ClaimsOutbox) replay() error {
	if strings.TrimSpace(o.path) == "" {
		return nil
	}
	f, err := os.Open(o.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open claims outbox for replay: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev claimsOutboxEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		o.applyEventLocked(ev)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan claims outbox: %w", err)
	}
	return nil
}

func (o *ClaimsOutbox) appendLocked(ev claimsOutboxEvent) error {
	if o.file == nil {
		return nil
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal claims outbox event: %w", err)
	}
	line = append(line, '\n')
	if _, err := o.file.Write(line); err != nil {
		return fmt.Errorf("write claims outbox event: %w", err)
	}
	return nil
}

func (o *ClaimsOutbox) applyEventLocked(ev claimsOutboxEvent) {
	switch ev.Kind {
	case "record":
		if ev.Record != nil {
			_ = o.insertLocked(*ev.Record, false)
		}
	case "projector_status":
		rec := o.records[ev.RecordID]
		if rec == nil || ev.Projector == "" {
			return
		}
		slot := rec.Projectors[ev.Projector]
		slot.Status = ev.Status
		slot.LeaseOwner = ev.LeaseOwner
		slot.LeaseUntil = ev.LeaseUntil
		slot.LastError = ev.LastError
		slot.UpdatedAt = ev.UpdatedAt
		if ev.Status == OutboxStatusFailedRetryable || ev.Status == OutboxStatusFailedTerminal {
			slot.Attempts++
		}
		rec.Projectors[ev.Projector] = slot
	}
}

func slotClaimable(slot OutboxProjectorSlot, now time.Time) bool {
	switch slot.Status {
	case "", OutboxStatusPending, OutboxStatusFailedRetryable:
		return true
	case OutboxStatusInProgress:
		return !slot.LeaseUntil.IsZero() && now.After(slot.LeaseUntil)
	default:
		return false
	}
}

func stableOutboxRecordID(record ClaimsOutboxRecord) string {
	parts := []string{
		record.BoardID,
		strconv.FormatUint(record.Sequence, 10),
		record.EntityType,
		record.EntityID,
		record.MutationKind,
	}
	return strings.Join(parts, "\x1f")
}

func normalizeProjectorNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func cloneOutboxRecord(record *ClaimsOutboxRecord) ClaimsOutboxRecord {
	if record == nil {
		return ClaimsOutboxRecord{}
	}
	cp := *record
	if record.Projectors != nil {
		cp.Projectors = make(map[string]OutboxProjectorSlot, len(record.Projectors))
		for k, v := range record.Projectors {
			cp.Projectors[k] = v
		}
	}
	return cp
}
