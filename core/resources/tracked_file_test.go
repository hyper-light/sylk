package resources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrackedFile_Type(t *testing.T) {
	tf := &TrackedFile{}
	if tf.Type() != "file" {
		t.Errorf("expected 'file', got %q", tf.Type())
	}
}

func TestTrackedFile_ID(t *testing.T) {
	tf := &TrackedFile{
		path: "/tmp/test.txt",
		fd:   42,
	}
	expected := "file:/tmp/test.txt:42"
	if tf.ID() != expected {
		t.Errorf("expected %q, got %q", expected, tf.ID())
	}
}

func TestTrackedFile_ForceClose_ReleasesBudget(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	budget := NewFileHandleBudget(DefaultFileHandleBudgetConfig())
	session := budget.RegisterSession("sess-1")
	agent := session.RegisterAgent("agent-1", "editor")
	agent.used.Store(1)
	session.used.Store(1)
	budget.globalUsed.Store(1)

	tf := NewTrackedFile(f, tmpFile, ModeWrite, "sess-1", "agent-1", agent)

	usedBefore := agent.Used()
	if err := tf.ForceClose(); err != nil {
		t.Errorf("ForceClose failed: %v", err)
	}
	usedAfter := agent.Used()

	if usedAfter != usedBefore-1 {
		t.Errorf("expected used to decrease by 1, before=%d, after=%d", usedBefore, usedAfter)
	}
}

func TestTrackedFile_DoubleClose_Safe(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	tf := NewTrackedFile(f, tmpFile, ModeWrite, "sess", "agent", nil)

	if err := tf.Close(); err != nil {
		t.Errorf("first Close failed: %v", err)
	}
	if err := tf.Close(); err != nil {
		t.Errorf("second Close should not fail: %v", err)
	}
}

func TestTrackedFile_ReadWrite_UpdatesLastAccess(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer f.Close()

	tf := NewTrackedFile(f, tmpFile, ModeReadWrite, "sess", "agent", nil)
	initialAccess := tf.lastAccess.Load().(time.Time)

	time.Sleep(10 * time.Millisecond)

	_, err = tf.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	afterWrite := tf.lastAccess.Load().(time.Time)
	if !afterWrite.After(initialAccess) {
		t.Error("Write should update lastAccess")
	}
}

func TestTrackedFile_Getters(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer f.Close()

	tf := NewTrackedFile(f, tmpFile, ModeAppend, "sess-1", "agent-1", nil)

	if tf.Path() != tmpFile {
		t.Errorf("expected path %q, got %q", tmpFile, tf.Path())
	}
	if tf.Mode() != ModeAppend {
		t.Errorf("expected mode ModeAppend, got %v", tf.Mode())
	}
	if tf.SessionID() != "sess-1" {
		t.Errorf("expected session 'sess-1', got %q", tf.SessionID())
	}
	if tf.AgentID() != "agent-1" {
		t.Errorf("expected agent 'agent-1', got %q", tf.AgentID())
	}
	if tf.Fd() < 0 {
		t.Errorf("expected valid fd, got %d", tf.Fd())
	}
	if tf.File() != f {
		t.Error("File() should return underlying os.File")
	}
}

func TestTrackedFile_OpenDuration(t *testing.T) {
	tf := &TrackedFile{
		openedAt: time.Now().Add(-100 * time.Millisecond),
	}

	duration := tf.OpenDuration()
	if duration < 100*time.Millisecond {
		t.Errorf("expected duration >= 100ms, got %v", duration)
	}
}

func TestTrackedFile_IdleDuration(t *testing.T) {
	tf := &TrackedFile{}
	tf.lastAccess.Store(time.Now().Add(-50 * time.Millisecond))

	duration := tf.IdleDuration()
	if duration < 50*time.Millisecond {
		t.Errorf("expected idle duration >= 50ms, got %v", duration)
	}
}

func TestTrackedFile_IsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	tf := NewTrackedFile(f, tmpFile, ModeWrite, "sess", "agent", nil)

	if tf.IsClosed() {
		t.Error("expected IsClosed() to be false initially")
	}

	tf.Close()

	if !tf.IsClosed() {
		t.Error("expected IsClosed() to be true after Close()")
	}
}

func TestFileMode_String(t *testing.T) {
	tests := []struct {
		mode     FileMode
		expected string
	}{
		{ModeRead, "read"},
		{ModeWrite, "write"},
		{ModeReadWrite, "read_write"},
		{ModeAppend, "append"},
		{FileMode(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.mode.String(); got != tt.expected {
			t.Errorf("FileMode(%d).String() = %q, want %q", tt.mode, got, tt.expected)
		}
	}
}

func TestFileMode_ToOSFlags(t *testing.T) {
	tests := []struct {
		mode     FileMode
		expected int
	}{
		{ModeRead, os.O_RDONLY},
		{ModeWrite, os.O_WRONLY | os.O_CREATE | os.O_TRUNC},
		{ModeReadWrite, os.O_RDWR | os.O_CREATE},
		{ModeAppend, os.O_WRONLY | os.O_CREATE | os.O_APPEND},
		{FileMode(99), os.O_RDONLY},
	}

	for _, tt := range tests {
		if got := tt.mode.ToOSFlags(); got != tt.expected {
			t.Errorf("FileMode(%d).ToOSFlags() = %d, want %d", tt.mode, got, tt.expected)
		}
	}
}

func TestTrackedResource_Interface(t *testing.T) {
	var _ TrackedResource = (*TrackedFile)(nil)
}
