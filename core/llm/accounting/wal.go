package accounting

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
)

// NoopWAL satisfies the WAL interface without persisting anything.
// Useful for tests and for runs where durable accounting is not
// required.
type NoopWAL struct{}

// Append implements WAL.Append as a no-op.
func (NoopWAL) Append(UsageDelta) error { return nil }

// Replay implements WAL.Replay as a no-op — there is nothing to
// replay.
func (NoopWAL) Replay(func(UsageDelta) error) error { return nil }

// Close implements WAL.Close as a no-op.
func (NoopWAL) Close() error { return nil }

// FileWAL is a JSONL append-only WAL. Each UsageDelta is serialized
// as one line. Replay reads sequentially; corrupt lines are
// skipped with a logged warning rather than aborting startup.
//
// The design deliberately trades performance for inspectability —
// accounting state is not hot-path. fsync is best-effort (called
// once per Append for durability under crash).
type FileWAL struct {
	mu   sync.Mutex
	path string
	file *os.File
	bw   *bufio.Writer
}

// OpenFileWAL opens (or creates) the WAL at path. Creates any
// missing parent directories. Always appends; existing contents
// are preserved.
func OpenFileWAL(path string) (*FileWAL, error) {
	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("wal: mkdir: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o640)
	if err != nil {
		return nil, fmt.Errorf("wal: open: %w", err)
	}
	return &FileWAL{
		path: path,
		file: f,
		bw:   bufio.NewWriterSize(f, 32*1024),
	}, nil
}

// Path returns the WAL file path.
func (w *FileWAL) Path() string { return w.path }

// Append serializes d as one JSONL line, flushes, and fsyncs.
// Safe for concurrent use.
func (w *FileWAL) Append(d UsageDelta) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return errors.New("wal: closed")
	}
	rec := toRecord(d)
	buf, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("wal: marshal: %w", err)
	}
	if _, err := w.bw.Write(buf); err != nil {
		return fmt.Errorf("wal: write: %w", err)
	}
	if err := w.bw.WriteByte('\n'); err != nil {
		return fmt.Errorf("wal: write newline: %w", err)
	}
	if err := w.bw.Flush(); err != nil {
		return fmt.Errorf("wal: flush: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("wal: sync: %w", err)
	}
	return nil
}

// Replay reads every record from the start of the file and calls
// fn for each. Corrupt lines are skipped. The file position is
// restored to the end so subsequent Appends do not truncate.
func (w *FileWAL) Replay(fn func(UsageDelta) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return errors.New("wal: closed")
	}
	// Seek to start for reading.
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("wal: seek start: %w", err)
	}
	defer func() {
		// Always reposition at end so the next Append writes after
		// existing records.
		_, _ = w.file.Seek(0, io.SeekEnd)
	}()

	scanner := bufio.NewScanner(w.file)
	// Allow large records.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec walRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// Skip malformed lines; accounting is best-effort on
			// replay so a single garbage record doesn't brick the
			// session.
			continue
		}
		delta, err := rec.toDelta()
		if err != nil {
			continue
		}
		if err := fn(delta); err != nil {
			return fmt.Errorf("wal: replay callback line %d: %w", lineNum, err)
		}
	}
	return scanner.Err()
}

// Close flushes and closes the underlying file. Safe to call more
// than once.
func (w *FileWAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.bw.Flush()
	_ = w.file.Sync()
	closeErr := w.file.Close()
	w.file = nil
	w.bw = nil
	if err != nil {
		return err
	}
	return closeErr
}

// walRecord is the on-disk schema. Keeping it flat-ish with
// explicit fields makes it readable and stable across migrations.
type walRecord struct {
	Schema     int       `json:"schema"`
	Timestamp  time.Time `json:"ts"`
	Phase      string    `json:"phase"`
	Billable   bool      `json:"billable"`
	ElapsedNs  int64     `json:"elapsed_ns"`
	Usage      walUsage  `json:"usage"`
	Identity   walIdent  `json:"identity"`
	Task       walTask   `json:"task"`
}

type walUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Reasoning  int `json:"reasoning,omitempty"`
	CacheRead  int `json:"cache_read,omitempty"`
	CacheWrite int `json:"cache_write,omitempty"`
}

type walIdent struct {
	UID        string            `json:"uid"`
	Namespace  string            `json:"namespace"`
	PodID      string            `json:"pod_id"`
	PodType    string            `json:"pod_type"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Category   string            `json:"category"`
	Model      string            `json:"model"`
	Generation uint64            `json:"generation"`
	Window     uint32            `json:"window"`
	Labels     map[string]string `json:"labels,omitempty"`
	OwnerUID   string            `json:"owner_uid,omitempty"`
	OwnerName  string            `json:"owner_name,omitempty"`
	OwnerKind  string            `json:"owner_kind,omitempty"`
}

type walTask struct {
	UID         string            `json:"uid"`
	DisplayID   string            `json:"display_id,omitempty"`
	Correlation string            `json:"correlation"`
	Namespace   string            `json:"namespace"`
	PipelineID  string            `json:"pipeline_id,omitempty"`
	Stage       string            `json:"stage,omitempty"`
	ParentTask  string            `json:"parent_task,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

const walSchemaVersion = 1

func toRecord(d UsageDelta) walRecord {
	rec := walRecord{
		Schema:    walSchemaVersion,
		Timestamp: d.Timestamp,
		Phase:     d.Phase.String(),
		Billable:  d.Billable,
		ElapsedNs: int64(d.Elapsed),
		Usage: walUsage{
			Input:      d.Usage.InputTokens,
			Output:     d.Usage.OutputTokens,
			Reasoning:  d.Usage.ReasoningTokens,
			CacheRead:  d.Usage.CacheReadTokens,
			CacheWrite: d.Usage.CacheWriteTokens,
		},
	}
	if d.Identity != nil {
		rec.Identity = walIdent{
			UID:        string(d.Identity.UID()),
			Namespace:  string(d.Identity.Namespace()),
			PodID:      string(d.Identity.Pod().ID),
			PodType:    d.Identity.Pod().Type.String(),
			Name:       string(d.Identity.Name()),
			Kind:       d.Identity.Kind().String(),
			Category:   d.Identity.Category().String(),
			Model:      string(d.Identity.Model()),
			Generation: uint64(d.Identity.Generation()),
			Window:     uint32(d.Identity.Window()),
			Labels:     map[string]string(d.Identity.Labels()),
		}
		if o := d.Identity.Owner(); o != nil {
			rec.Identity.OwnerUID = string(o.UID)
			rec.Identity.OwnerName = string(o.Name)
			rec.Identity.OwnerKind = o.Kind.String()
		}
	}
	if d.Task != nil {
		rec.Task = walTask{
			UID:         string(d.Task.UID()),
			DisplayID:   d.Task.DisplayID(),
			Correlation: string(d.Task.Correlation()),
			Namespace:   string(d.Task.Namespace()),
			Labels:      map[string]string(d.Task.Labels()),
		}
		if p := d.Task.Pipeline(); p != nil {
			rec.Task.PipelineID = string(p.ID)
			rec.Task.Stage = string(p.Stage)
			rec.Task.ParentTask = string(p.Parent)
		}
	}
	return rec
}

// toDelta reconstructs a UsageDelta from a walRecord. The returned
// delta's Identity and Task are freshly-constructed value copies —
// they are NOT the same instance as what the accountant's factory
// minted during the live session. They are structurally identical
// for accounting purposes.
func (r walRecord) toDelta() (UsageDelta, error) {
	ident, err := rebuildIdentity(r.Identity)
	if err != nil {
		return UsageDelta{}, fmt.Errorf("rebuild identity: %w", err)
	}
	task, err := rebuildTask(r.Task)
	if err != nil {
		return UsageDelta{}, fmt.Errorf("rebuild task: %w", err)
	}
	return UsageDelta{
		Key: AccountingKey{
			Container: ident.Key(),
			Task:      task.UID(),
		},
		Identity:  ident,
		Task:      task,
		Usage:     UsageSample{
			InputTokens:      r.Usage.Input,
			OutputTokens:     r.Usage.Output,
			ReasoningTokens:  r.Usage.Reasoning,
			CacheReadTokens:  r.Usage.CacheRead,
			CacheWriteTokens: r.Usage.CacheWrite,
		},
		Elapsed:   time.Duration(r.ElapsedNs),
		Phase:     phaseFromString(r.Phase),
		Billable:  r.Billable,
		Timestamp: r.Timestamp,
	}, nil
}

func phaseFromString(s string) Phase {
	switch s {
	case "request":
		return PhaseRequest
	case "response":
		return PhaseResponse
	case "error":
		return PhaseError
	default:
		return PhaseUnspecified
	}
}

// rebuildIdentity reconstructs a value-form AgentIdentity from the
// WAL record. This is intentionally not a Factory call — replayed
// identities skip ordinal registration because their UID already
// exists. Use identity.RebuildForReplay which the identity package
// exposes for this purpose.
func rebuildIdentity(w walIdent) (*identity.AgentIdentity, error) {
	if w.UID == "" {
		return nil, errors.New("empty uid")
	}
	kind, ok := identity.AgentTypeFromString(w.Kind)
	if !ok {
		return nil, fmt.Errorf("unknown kind %q", w.Kind)
	}
	category := categoryFromString(w.Category)
	podType := podTypeFromString(w.PodType)
	var owner *identity.OwnerRef
	if w.OwnerUID != "" {
		ownerKind, _ := identity.AgentTypeFromString(w.OwnerKind)
		owner = &identity.OwnerRef{
			UID:  identity.UID(w.OwnerUID),
			Name: identity.Name(w.OwnerName),
			Kind: ownerKind,
		}
	}
	return identity.RebuildForReplay(identity.ReplayAgentIdentity{
		UID:        identity.UID(w.UID),
		Namespace:  identity.Namespace(w.Namespace),
		Pod:        identity.PodRef{ID: identity.PodID(w.PodID), Type: podType},
		Name:       identity.Name(w.Name),
		Kind:       kind,
		Category:   category,
		Model:      identity.ModelID(w.Model),
		Generation: identity.Generation(w.Generation),
		Window:     identity.ContextWindow(w.Window),
		Labels:     identity.Labels(w.Labels),
		Owner:      owner,
	}), nil
}

func rebuildTask(w walTask) (*identity.TaskRef, error) {
	if w.UID == "" {
		return nil, errors.New("empty task uid")
	}
	var pipeline *identity.PipelineRef
	if w.PipelineID != "" {
		pipeline = &identity.PipelineRef{
			ID:     identity.PipelineID(w.PipelineID),
			Stage:  identity.PipelineStage(w.Stage),
			Parent: identity.TaskUID(w.ParentTask),
		}
	}
	return identity.RebuildTaskForReplay(identity.ReplayTaskRef{
		UID:         identity.TaskUID(w.UID),
		DisplayID:   w.DisplayID,
		Correlation: identity.CorrelationID(w.Correlation),
		Namespace:   identity.Namespace(w.Namespace),
		Pipeline:    pipeline,
		Labels:      identity.Labels(w.Labels),
	}), nil
}

func categoryFromString(s string) identity.Category {
	switch s {
	case "knowledge":
		return identity.CategoryKnowledge
	case "standalone":
		return identity.CategoryStandalone
	case "pipeline":
		return identity.CategoryPipeline
	case "system":
		return identity.CategorySystem
	default:
		return identity.CategoryUnspecified
	}
}

func podTypeFromString(s string) identity.PodType {
	switch s {
	case "pipeline":
		return identity.PodTypePipeline
	case "singleton":
		return identity.PodTypeSingleton
	case "daemon":
		return identity.PodTypeDaemon
	default:
		return identity.PodTypePipeline
	}
}
