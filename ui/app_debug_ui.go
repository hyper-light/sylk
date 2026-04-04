package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adalundhe/sylk/ui/chat"
	tea "github.com/charmbracelet/bubbletea"
)

const chatDebugLogFilename = "chat.log"

type uiChatDebugCapture struct {
	path         string
	file         *os.File
	lastRendered map[string]string
}

func newUIChatDebugCapture(path string) (*uiChatDebugCapture, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	return &uiChatDebugCapture{
		path:         path,
		file:         file,
		lastRendered: make(map[string]string),
	}, nil
}

func (c *uiChatDebugCapture) closeWithMarker(marker string) error {
	if c == nil {
		return nil
	}
	var err error
	if marker = strings.TrimSpace(marker); marker != "" && c.file != nil {
		_, err = fmt.Fprintf(c.file, "=== %s | %s ===\n\n", time.Now().Format(time.RFC3339Nano), marker)
		if syncErr := c.file.Sync(); syncErr != nil {
			err = errorsJoin(err, syncErr)
		}
	}
	if c.file != nil {
		err = errorsJoin(err, c.file.Close())
	}
	return err
}

func (c *uiChatDebugCapture) append(entries []chat.DebugRenderedEntry, capturedAt time.Time) error {
	if c == nil || c.file == nil {
		return nil
	}
	current := make(map[string]string, len(entries))
	var wrote bool
	for _, entry := range entries {
		key := strings.TrimSpace(entry.ID)
		if key == "" {
			continue
		}
		rendered := strings.Join(entry.Lines, "\n")
		current[key] = rendered
		if prev, ok := c.lastRendered[key]; ok && prev == rendered {
			continue
		}
		if _, err := fmt.Fprintf(c.file, "%s\n", formatChatDebugHeader(entry, capturedAt)); err != nil {
			return err
		}
		if len(entry.Lines) > 0 {
			if _, err := fmt.Fprintln(c.file, rendered); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(c.file); err != nil {
			return err
		}
		wrote = true
	}
	if len(entries) == 0 && len(c.lastRendered) > 0 {
		if _, err := fmt.Fprintf(c.file, "=== %s | chat-cleared ===\n\n", capturedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		wrote = true
	}
	c.lastRendered = current
	if wrote {
		return c.file.Sync()
	}
	return nil
}

func formatChatDebugHeader(entry chat.DebugRenderedEntry, capturedAt time.Time) string {
	parts := []string{capturedAt.Format(time.RFC3339Nano), "source=" + chatSourceLabel(entry.Source)}
	if agent := firstNonEmpty(strings.TrimSpace(entry.AgentType), strings.TrimSpace(entry.AgentID)); agent != "" {
		parts = append(parts, "agent="+agent)
	}
	if corr := strings.TrimSpace(entry.CorrelationID); corr != "" {
		parts = append(parts, "corr="+corr)
	}
	if task := strings.TrimSpace(entry.TaskID); task != "" {
		parts = append(parts, "task="+task)
	}
	if sessionID := strings.TrimSpace(entry.SessionID); sessionID != "" {
		parts = append(parts, "session="+sessionID)
	}
	return "=== " + strings.Join(parts, " | ") + " ==="
}

func chatSourceLabel(source chat.ChatSource) string {
	switch source {
	case chat.SourceUser:
		return "user"
	case chat.SourceAgent:
		return "agent"
	case chat.SourceSystem:
		return "system"
	case chat.SourceTool:
		return "tool"
	case chat.SourceError:
		return "error"
	default:
		return "unknown"
	}
}

func (m *AppModel) handleDebugUICommand(mode string) tea.Cmd {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "toggle":
		if m.chatDebugCapture == nil {
			return m.enableChatDebugCapture()
		}
		return m.disableChatDebugCapture()
	case "enable", "on", "start":
		if m.chatDebugCapture != nil {
			m.pushSystemChat(fmt.Sprintf("UI debug capture already enabled: %s", m.chatDebugCapture.path))
			return nil
		}
		return m.enableChatDebugCapture()
	case "disable", "off", "stop":
		if m.chatDebugCapture == nil {
			m.pushSystemChat("UI debug capture already disabled.")
			return nil
		}
		return m.disableChatDebugCapture()
	default:
		m.pushSystemChat("Usage: /debug-ui [enable|disable]")
		return nil
	}
}

func (m *AppModel) enableChatDebugCapture() tea.Cmd {
	if m.chatDebugCapture == nil {
		wd, err := os.Getwd()
		if err != nil {
			m.pushSystemChat(fmt.Sprintf("UI debug capture failed: %v", err))
			return nil
		}
		path := filepath.Join(wd, chatDebugLogFilename)
		capture, err := newUIChatDebugCapture(path)
		if err != nil {
			m.pushSystemChat(fmt.Sprintf("UI debug capture failed: %v", err))
			return nil
		}
		m.chatDebugCapture = capture
		m.pushSystemChat(fmt.Sprintf("UI debug capture started: %s", path))
		return nil
	}
	return nil
}

func (m *AppModel) disableChatDebugCapture() tea.Cmd {
	path := m.chatDebugCapture.path
	if err := m.stopChatDebugCapture(); err != nil {
		m.pushSystemChat(fmt.Sprintf("UI debug capture stopped with error: %v", err))
		return nil
	}
	m.pushSystemChat(fmt.Sprintf("UI debug capture stopped: %s", path))
	return nil
}

func (m *AppModel) stopChatDebugCapture() error {
	if m == nil || m.chatDebugCapture == nil {
		return nil
	}
	capture := m.chatDebugCapture
	m.chatDebugCapture = nil
	return capture.closeWithMarker("debug-ui stopped")
}

func (m *AppModel) flushChatDebugCapture() {
	if m == nil || m.chatDebugCapture == nil || m.chat == nil {
		return
	}
	if err := m.chatDebugCapture.append(m.chat.DebugRenderedEntries(), time.Now()); err != nil {
		path := m.chatDebugCapture.path
		_ = m.stopChatDebugCapture()
		m.pushSystemChat(fmt.Sprintf("UI debug capture failed and was disabled: %s (%v)", path, err))
	}
}

func errorsJoin(errs ...error) error {
	var out error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if out == nil {
			out = err
			continue
		}
		out = fmt.Errorf("%v; %w", out, err)
	}
	return out
}
