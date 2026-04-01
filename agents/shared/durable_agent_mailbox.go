package shared

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/google/uuid"
)

type durableMailboxItem struct {
	ItemID    string          `json:"item_id"`
	Key       string          `json:"key"`
	Namespace string          `json:"namespace"`
	ScopeID   string          `json:"scope_id"`
	AgentID   string          `json:"agent_id"`
	ItemKind  string          `json:"item_kind"`
	Action    string          `json:"action,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type durableMailboxAck struct {
	ItemID         string    `json:"item_id,omitempty"`
	Key            string    `json:"key"`
	Namespace      string    `json:"namespace"`
	ScopeID        string    `json:"scope_id"`
	AgentID        string    `json:"agent_id"`
	Resolution     string    `json:"resolution,omitempty"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}

type durableAgentMailbox struct {
	agentID string
	dir     string
	journal *agentlog.AgentJournal
}

func openDurableAgentMailbox(sessionDir, agentID string) (*durableAgentMailbox, error) {
	sessionDir = strings.TrimSpace(sessionDir)
	agentID = strings.TrimSpace(agentID)
	if sessionDir == "" {
		return nil, fmt.Errorf("durable mailbox requires session dir")
	}
	if agentID == "" {
		return nil, fmt.Errorf("durable mailbox requires agent id")
	}
	dir := filepath.Join(sessionDir, "agents", agentID, "mailbox")
	journal, err := agentlog.OpenJournalDirect(dir, agentlog.JournalConfig{
		WALName: "mailbox",
	})
	if err != nil {
		return nil, err
	}
	return &durableAgentMailbox{
		agentID: agentID,
		dir:     dir,
		journal: journal,
	}, nil
}

func (m *durableAgentMailbox) Sync(namespace, scopeID string, desired []durableMailboxItem) error {
	if m == nil || m.journal == nil {
		return fmt.Errorf("durable mailbox is not initialized")
	}
	namespace = strings.TrimSpace(namespace)
	scopeID = strings.TrimSpace(scopeID)
	current, err := m.Pending(namespace, scopeID)
	if err != nil {
		return err
	}
	currentByKey := make(map[string]durableMailboxItem, len(current))
	for _, item := range current {
		currentByKey[item.Key] = item
	}
	desiredByKey := make(map[string]durableMailboxItem, len(desired))
	for _, item := range desired {
		if item.Key == "" {
			continue
		}
		item.Namespace = namespace
		item.ScopeID = scopeID
		item.AgentID = m.agentID
		desiredByKey[item.Key] = item
		if _, ok := currentByKey[item.Key]; ok {
			continue
		}
		if err := m.enqueue(item); err != nil {
			return err
		}
	}
	for key, item := range currentByKey {
		if _, ok := desiredByKey[key]; ok {
			continue
		}
		if err := m.ack(item, "superseded"); err != nil {
			return err
		}
	}
	return nil
}

func (m *durableAgentMailbox) Pending(namespace, scopeID string) ([]durableMailboxItem, error) {
	if m == nil || m.journal == nil {
		return nil, fmt.Errorf("durable mailbox is not initialized")
	}
	type mailboxState struct {
		item    durableMailboxItem
		itemSeq uint64
		ackSeq  uint64
	}
	stateByKey := map[string]*mailboxState{}
	err := m.journal.ReplayAll(0, func(entry agentlog.Entry) error {
		switch entry.EventType {
		case agentlog.EventMailboxItemEnqueued:
			var item durableMailboxItem
			if err := json.Unmarshal(entry.Data, &item); err != nil {
				return err
			}
			if strings.TrimSpace(item.Namespace) != namespace || strings.TrimSpace(item.ScopeID) != scopeID {
				return nil
			}
			key := strings.TrimSpace(item.Key)
			if key == "" {
				return nil
			}
			state := stateByKey[key]
			if state == nil {
				state = &mailboxState{}
				stateByKey[key] = state
			}
			state.item = item
			state.itemSeq = entry.Seq
		case agentlog.EventMailboxItemAcknowledged:
			var ack durableMailboxAck
			if err := json.Unmarshal(entry.Data, &ack); err != nil {
				return err
			}
			if strings.TrimSpace(ack.Namespace) != namespace || strings.TrimSpace(ack.ScopeID) != scopeID {
				return nil
			}
			key := strings.TrimSpace(ack.Key)
			if key == "" {
				return nil
			}
			state := stateByKey[key]
			if state == nil {
				state = &mailboxState{}
				stateByKey[key] = state
			}
			if entry.Seq > state.ackSeq {
				state.ackSeq = entry.Seq
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	items := make([]durableMailboxItem, 0, len(stateByKey))
	for _, state := range stateByKey {
		if state.itemSeq == 0 || state.itemSeq <= state.ackSeq {
			continue
		}
		items = append(items, state.item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (m *durableAgentMailbox) Close() error {
	if m == nil || m.journal == nil {
		return nil
	}
	return m.journal.Close()
}

func (m *durableAgentMailbox) enqueue(item durableMailboxItem) error {
	if item.ItemID == "" {
		item.ItemID = uuid.NewString()
	}
	item.Namespace = strings.TrimSpace(item.Namespace)
	item.ScopeID = strings.TrimSpace(item.ScopeID)
	item.AgentID = strings.TrimSpace(item.AgentID)
	item.Key = strings.TrimSpace(item.Key)
	item.ItemKind = strings.TrimSpace(item.ItemKind)
	item.Action = strings.TrimSpace(item.Action)
	item.Summary = strings.TrimSpace(item.Summary)
	item.CreatedAt = time.Now().UTC()
	if item.Key == "" {
		return fmt.Errorf("durable mailbox item key is required")
	}
	if item.ItemKind == "" {
		return fmt.Errorf("durable mailbox item kind is required")
	}
	seq, err := m.journal.AppendJSON(agentlog.EventMailboxItemEnqueued, item)
	if err != nil {
		return err
	}
	if seq == 0 {
		return fmt.Errorf("durable mailbox enqueue returned zero sequence")
	}
	return nil
}

func (m *durableAgentMailbox) ack(item durableMailboxItem, resolution string) error {
	ack := durableMailboxAck{
		ItemID:         strings.TrimSpace(item.ItemID),
		Key:            strings.TrimSpace(item.Key),
		Namespace:      strings.TrimSpace(item.Namespace),
		ScopeID:        strings.TrimSpace(item.ScopeID),
		AgentID:        strings.TrimSpace(item.AgentID),
		Resolution:     strings.TrimSpace(resolution),
		AcknowledgedAt: time.Now().UTC(),
	}
	seq, err := m.journal.AppendJSON(agentlog.EventMailboxItemAcknowledged, ack)
	if err != nil {
		return err
	}
	if seq == 0 {
		return fmt.Errorf("durable mailbox ack returned zero sequence")
	}
	return nil
}

func durableMailboxScopePath(sessionDir, agentID string) string {
	return filepath.Join(sessionDir, "agents", strings.TrimSpace(agentID), "mailbox")
}

func durableMailboxExists(sessionDir, agentID string) bool {
	info, err := os.Stat(durableMailboxScopePath(sessionDir, agentID))
	return err == nil && info.IsDir()
}
