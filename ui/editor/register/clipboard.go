package register

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// ClipboardProvider abstracts system clipboard read/write operations.
type ClipboardProvider interface {
	Get() (string, error)
	Set(content string) error
}

// clipboardEntry describes the OS commands used to interact with the
// system clipboard on a specific platform.
type clipboardEntry struct {
	copyCmd   string
	copyArgs  []string
	pasteCmd  string
	pasteArgs []string
	envGuard  string // Environment variable that must be non-empty to use this entry.
}

// clipboardTable maps GOOS values to their clipboard commands.
// Entries are probed in order; the first that passes both the envGuard
// check and PATH lookup wins.
var clipboardTable = map[string][]clipboardEntry{
	"darwin": {
		{copyCmd: "pbcopy", pasteCmd: "pbpaste"},
	},
	"linux": {
		{copyCmd: "wl-copy", pasteCmd: "wl-paste", envGuard: "WAYLAND_DISPLAY"},
		{copyCmd: "xclip", copyArgs: []string{"-selection", "clipboard"}, pasteCmd: "xclip", pasteArgs: []string{"-selection", "clipboard", "-o"}},
		{copyCmd: "xsel", copyArgs: []string{"--clipboard", "--input"}, pasteCmd: "xsel", pasteArgs: []string{"--clipboard", "--output"}},
	},
	"freebsd": {
		{copyCmd: "xclip", copyArgs: []string{"-selection", "clipboard"}, pasteCmd: "xclip", pasteArgs: []string{"-selection", "clipboard", "-o"}},
	},
}

// OSClipboard interacts with the system clipboard using OS-specific commands.
// It probes available commands at construction time and falls back to an
// internal buffer when no clipboard utility is found.
type OSClipboard struct {
	mu       sync.Mutex
	entry    *clipboardEntry
	fallback string
}

// NewOSClipboard creates a clipboard provider for the current platform.
// If no system clipboard utility is available, an internal fallback is used.
func NewOSClipboard() *OSClipboard {
	cb := &OSClipboard{}
	cb.entry = probeClipboard()
	return cb
}

// probeClipboard checks available clipboard commands for the current OS.
// Entries with an envGuard are skipped when that environment variable is unset.
func probeClipboard() *clipboardEntry {
	entries, ok := clipboardTable[runtime.GOOS]
	if !ok {
		return nil
	}
	for i := range entries {
		if entries[i].envGuard != "" && os.Getenv(entries[i].envGuard) == "" {
			continue
		}
		if commandAvailable(entries[i].copyCmd) && commandAvailable(entries[i].pasteCmd) {
			return &entries[i]
		}
	}
	return nil
}

// commandAvailable reports whether a command exists on the PATH.
func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// Get reads text from the system clipboard. Falls back to the internal
// buffer if no system clipboard is available.
func (c *OSClipboard) Get() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entry == nil {
		return c.fallback, nil
	}
	return c.runPaste()
}

// Set writes text to the system clipboard. Falls back to the internal
// buffer if no system clipboard is available.
func (c *OSClipboard) Set(content string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entry == nil {
		c.fallback = clampContent(content)
		return nil
	}
	return c.runCopy(content)
}

// runPaste executes the paste command and returns its stdout.
func (c *OSClipboard) runPaste() (string, error) {
	cmd := exec.Command(c.entry.pasteCmd, c.entry.pasteArgs...)
	out, err := cmd.Output()
	if err != nil {
		return c.fallback, err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// runCopy executes the copy command, writing content to its stdin.
func (c *OSClipboard) runCopy(content string) error {
	cmd := exec.Command(c.entry.copyCmd, c.entry.copyArgs...)
	cmd.Stdin = strings.NewReader(content)
	return cmd.Run()
}

// InternalClipboard is a purely in-memory clipboard for environments
// without system clipboard access. It is always available as a fallback.
type InternalClipboard struct {
	mu      sync.Mutex
	content string
}

// Get returns the internal clipboard content.
func (ic *InternalClipboard) Get() (string, error) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	return ic.content, nil
}

// Set stores content in the internal clipboard.
func (ic *InternalClipboard) Set(content string) error {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.content = clampContent(content)
	return nil
}
