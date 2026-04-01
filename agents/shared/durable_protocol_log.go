package shared

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/google/uuid"
)

type durableProtocolEvent struct {
	EventID             string          `json:"event_id"`
	Namespace           string          `json:"namespace"`
	ScopeID             string          `json:"scope_id"`
	Kind                string          `json:"kind"`
	AgentType           string          `json:"agent_type,omitempty"`
	CorrelationID       string          `json:"correlation_id,omitempty"`
	ParentCorrelationID string          `json:"parent_correlation_id,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	Payload             json.RawMessage `json:"payload,omitempty"`
}

type durableProtocolSnapshotFile struct {
	Seq       uint64          `json:"seq"`
	UpdatedAt time.Time       `json:"updated_at"`
	Snapshot  json.RawMessage `json:"snapshot"`
}

type durableProtocolLog struct {
	namespace string
	scopeID   string
	dir       string
	journal   *agentlog.AgentJournal
}

func openDurableProtocolLog(sessionDir, namespace, scopeID string) (*durableProtocolLog, error) {
	sessionDir = strings.TrimSpace(sessionDir)
	namespace = strings.TrimSpace(namespace)
	scopeID = strings.TrimSpace(scopeID)
	if sessionDir == "" {
		return nil, fmt.Errorf("durable protocol log requires session dir")
	}
	if namespace == "" {
		return nil, fmt.Errorf("durable protocol log requires namespace")
	}
	if scopeID == "" {
		return nil, fmt.Errorf("durable protocol log requires scope id")
	}
	dir := filepath.Join(sessionDir, "protocols", namespace, scopeID, "wal")
	journal, err := agentlog.OpenJournalDirect(dir, agentlog.JournalConfig{
		WALName: "events",
	})
	if err != nil {
		return nil, err
	}
	return &durableProtocolLog{
		namespace: namespace,
		scopeID:   scopeID,
		dir:       dir,
		journal:   journal,
	}, nil
}

func (l *durableProtocolLog) Append(kind, agentType, correlationID, parentCorrelationID string, payload any) (uint64, *durableProtocolEvent, error) {
	if l == nil || l.journal == nil {
		return 0, nil, fmt.Errorf("durable protocol log is not initialized")
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return 0, nil, fmt.Errorf("durable protocol event kind is required")
	}
	var encoded json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, fmt.Errorf("encode durable protocol payload: %w", err)
		}
		encoded = data
	}
	event := &durableProtocolEvent{
		EventID:             uuid.NewString(),
		Namespace:           l.namespace,
		ScopeID:             l.scopeID,
		Kind:                kind,
		AgentType:           strings.TrimSpace(agentType),
		CorrelationID:       strings.TrimSpace(correlationID),
		ParentCorrelationID: strings.TrimSpace(parentCorrelationID),
		CreatedAt:           time.Now().UTC(),
		Payload:             encoded,
	}
	seq, err := l.journal.AppendJSON(agentlog.EventProtocolEventAppended, event)
	if err != nil {
		return 0, nil, err
	}
	if seq == 0 {
		return 0, nil, fmt.Errorf("durable protocol append returned zero sequence")
	}
	return seq, event, nil
}

func (l *durableProtocolLog) Replay(afterSeq uint64, fn func(uint64, *durableProtocolEvent) error) error {
	if l == nil || l.journal == nil {
		return fmt.Errorf("durable protocol log is not initialized")
	}
	if fn == nil {
		return nil
	}
	return l.journal.ReplayAll(afterSeq, func(entry agentlog.Entry) error {
		if entry.EventType != agentlog.EventProtocolEventAppended {
			return nil
		}
		var event durableProtocolEvent
		if err := json.Unmarshal(entry.Data, &event); err != nil {
			return fmt.Errorf("decode durable protocol event seq %d: %w", entry.Seq, err)
		}
		return fn(entry.Seq, &event)
	})
}

func (l *durableProtocolLog) LoadSnapshot(out any) (uint64, bool, error) {
	if l == nil {
		return 0, false, fmt.Errorf("durable protocol log is not initialized")
	}
	data, err := os.ReadFile(l.snapshotPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	var snapshot durableProtocolSnapshotFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return 0, false, err
	}
	if out != nil && len(snapshot.Snapshot) > 0 {
		if err := json.Unmarshal(snapshot.Snapshot, out); err != nil {
			return 0, false, err
		}
	}
	return snapshot.Seq, true, nil
}

func (l *durableProtocolLog) SaveSnapshot(seq uint64, snapshot any) error {
	if l == nil {
		return fmt.Errorf("durable protocol log is not initialized")
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	file := durableProtocolSnapshotFile{
		Seq:       seq,
		UpdatedAt: time.Now().UTC(),
		Snapshot:  data,
	}
	encoded, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.snapshotPath()), 0o755); err != nil {
		return err
	}
	tmp := l.snapshotPath() + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, l.snapshotPath()); err != nil {
		return err
	}
	_, err = l.journal.AppendJSON(agentlog.EventProtocolSnapshotSaved, &agentlog.ProtocolSnapshotPayload{
		Namespace: l.namespace,
		ScopeID:   l.scopeID,
		Seq:       seq,
	})
	return err
}

func (l *durableProtocolLog) Close() error {
	if l == nil || l.journal == nil {
		return nil
	}
	return l.journal.Close()
}

func (l *durableProtocolLog) snapshotPath() string {
	return filepath.Join(filepath.Dir(l.dir), "projection.snapshot.json")
}
