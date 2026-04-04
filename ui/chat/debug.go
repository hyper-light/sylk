package chat

import (
	"fmt"
	"strings"
	"time"
)

// DebugRenderedEntry is a plain-text snapshot of one rendered chat entry.
// Lines match the chat panel rendering except ANSI styling is stripped.
type DebugRenderedEntry struct {
	ID            string
	Timestamp     time.Time
	CorrelationID string
	Source        ChatSource
	AgentType     string
	AgentID       string
	TaskID        string
	TaskName      string
	TaskSlug      string
	SessionID     string
	Lines         []string
}

// DebugRenderedEntries returns a full plain-text snapshot of the current chat
// history as rendered by the chat panel.
func (m *Model) DebugRenderedEntries() []DebugRenderedEntry {
	if m == nil || m.history == nil || m.viewport == nil {
		return nil
	}
	count := m.history.Len()
	if count == 0 {
		return nil
	}
	entries := make([]DebugRenderedEntry, 0, count)
	for i := 0; i < count; i++ {
		entry := m.history.Get(i)
		if entry == nil {
			continue
		}
		lines := m.viewport.renderEntry(i)
		plain := make([]string, len(lines))
		for j, line := range lines {
			plain[j] = stripANSIDebug(line)
		}
		entries = append(entries, DebugRenderedEntry{
			ID:            debugEntryKey(i, entry),
			Timestamp:     entry.Timestamp,
			CorrelationID: entry.CorrelationID,
			Source:        entry.Source,
			AgentType:     entry.AgentType,
			AgentID:       entry.AgentID,
			TaskID:        entry.TaskID,
			TaskName:      entry.TaskName,
			TaskSlug:      entry.TaskSlug,
			SessionID:     entry.SessionID,
			Lines:         plain,
		})
	}
	return entries
}

func debugEntryKey(index int, entry *ChatEntry) string {
	if entry == nil {
		return fmt.Sprintf("entry:%d", index)
	}
	if id := strings.TrimSpace(entry.ID); id != "" {
		return id
	}
	if cid := strings.TrimSpace(entry.CorrelationID); cid != "" {
		return "corr:" + cid
	}
	return fmt.Sprintf("entry:%d", index)
}

func stripANSIDebug(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !isCSITerminatorDebug(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func isCSITerminatorDebug(b byte) bool {
	return b >= 0x40 && b <= 0x7e
}
