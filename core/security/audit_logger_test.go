package security

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAuditLogger(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 10,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	assert.Equal(t, uint64(0), al.Sequence())
	assert.NotNil(t, al.publicKey)
}

func TestAuditLogger_Log(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 100,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	entry := NewAuditEntry(AuditCategoryPermission, "test_event", "test_action")
	entry.Target = "test_target"
	entry.Outcome = "success"

	err = al.Log(entry)
	require.NoError(t, err)

	assert.Equal(t, uint64(1), al.Sequence())
	assert.NotEmpty(t, al.PreviousHash())
}

func TestAuditLogger_HashChain(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 100,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	var hashes []string
	for i := 0; i < 5; i++ {
		entry := NewAuditEntry(AuditCategoryFile, "file_write", "write")
		entry.Target = "/path/to/file"
		err := al.Log(entry)
		require.NoError(t, err)
		hashes = append(hashes, al.PreviousHash())
	}

	for i := 1; i < len(hashes); i++ {
		assert.NotEqual(t, hashes[i-1], hashes[i])
	}
}

func TestAuditLogger_VerifyIntegrity(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 5,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		entry := NewAuditEntry(AuditCategoryProcess, "process_exec", "execute")
		entry.Target = "ls -la"
		err := al.Log(entry)
		require.NoError(t, err)
	}

	report, err := al.VerifyIntegrity()
	require.NoError(t, err)

	assert.True(t, report.Valid)
	assert.Equal(t, 10, report.EntriesVerified)
	assert.GreaterOrEqual(t, report.SignaturesVerified, 1)
	assert.Empty(t, report.Errors)

	al.Close()
}

func TestAuditLogger_VerifyIntegrity_TamperDetection(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 100,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		entry := NewAuditEntry(AuditCategoryNetwork, "network_request", "request")
		err := al.Log(entry)
		require.NoError(t, err)
	}
	al.Close()

	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	tampered := make([]byte, len(data))
	copy(tampered, data)
	for i := range tampered {
		if tampered[i] == 'n' {
			tampered[i] = 'N'
			break
		}
	}
	err = os.WriteFile(logPath, tampered, 0600)
	require.NoError(t, err)

	al2, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al2.Close()

	report, err := al2.VerifyIntegrity()
	require.NoError(t, err)

	assert.False(t, report.Valid)
	assert.NotEmpty(t, report.Errors)
}

func TestAuditLogger_Signature(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 3,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)

	for i := 0; i < 6; i++ {
		entry := NewAuditEntry(AuditCategoryLLM, "llm_call", "call")
		err := al.Log(entry)
		require.NoError(t, err)
	}
	al.Close()

	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	assert.Contains(t, string(data), "SIG:")
}

func TestAuditLogger_ConcurrentLogging(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 50,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry := NewAuditEntry(AuditCategorySession, "session_event", "event")
			entry.Details = map[string]interface{}{"iteration": i}
			_ = al.Log(entry)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, uint64(100), al.Sequence())

	report, err := al.VerifyIntegrity()
	require.NoError(t, err)
	assert.True(t, report.Valid)
}

func TestAuditLogger_SequenceMonotonic(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 100,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	for i := 1; i <= 10; i++ {
		entry := NewAuditEntry(AuditCategoryConfig, "config_change", "change")
		err := al.Log(entry)
		require.NoError(t, err)
		assert.Equal(t, uint64(i), al.Sequence())
	}
}

func TestAuditLogger_NilLogFile(t *testing.T) {
	cfg := AuditLogConfig{
		SignatureInterval: 100,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	entry := NewAuditEntry(AuditCategoryPermission, "test", "test")
	err = al.Log(entry)
	require.NoError(t, err)

	assert.Equal(t, uint64(1), al.Sequence())
}

func TestAuditLogger_ClosedLogger(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 100,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)

	err = al.Close()
	require.NoError(t, err)

	entry := NewAuditEntry(AuditCategoryPermission, "test", "test")
	err = al.Log(entry)
	assert.ErrorIs(t, err, ErrLoggerClosed)
}

func TestAuditLogger_LogPermissionGranted(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 100,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	action := PermissionAction{
		Type:   ActionTypeCommand,
		Target: "ls",
	}
	al.LogPermissionGranted("agent-1", action, "safe_list")

	assert.Equal(t, uint64(1), al.Sequence())
}

func TestAuditLogger_LogPermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 100,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	action := PermissionAction{
		Type:   ActionTypeNetwork,
		Target: "evil.com",
	}
	al.LogPermissionDenied("agent-2", action, "not_in_allowlist")

	assert.Equal(t, uint64(1), al.Sequence())
}

func TestAuditLogger_LogFileOperation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{LogPath: logPath}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	al.LogFileOperation("write", "/tmp/test.txt", "agent-1", 1024, "abc123")

	assert.Equal(t, uint64(1), al.Sequence())
}

func TestAuditLogger_LogProcessExecution(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{LogPath: logPath}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	al.LogProcessExecution("ls -la", "agent-1", 0)

	assert.Equal(t, uint64(1), al.Sequence())
}

func TestAuditLogger_LogNetworkAllowedBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{LogPath: logPath}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	al.LogNetworkAllowed("github.com", "https://github.com/api")
	al.LogNetworkBlocked("evil.com", "https://evil.com/malware")

	assert.Equal(t, uint64(2), al.Sequence())
}

func TestAuditLogger_LogLLMCall(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{LogPath: logPath}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	al.LogLLMCall("anthropic", "claude-3-opus", 1500, 0.05)

	assert.Equal(t, uint64(1), al.Sequence())
}

func TestAuditLogger_LogSessionEvent(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{LogPath: logPath}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	al.LogSessionEvent("start", "session-123")
	al.LogSessionEvent("end", "session-123")

	assert.Equal(t, uint64(2), al.Sequence())
}

func TestAuditEntry_Builder(t *testing.T) {
	entry := NewAuditEntry(AuditCategoryFile, "file_read", "read")
	entry.WithSeverity(AuditSeverityWarning).
		WithTarget("/etc/passwd").
		WithOutcome("denied").
		WithSessionID("sess-1").
		WithAgentID("agent-1").
		WithDetails(map[string]interface{}{"reason": "sensitive"})

	assert.Equal(t, AuditCategoryFile, entry.Category)
	assert.Equal(t, AuditSeverityWarning, entry.Severity)
	assert.Equal(t, "/etc/passwd", entry.Target)
	assert.Equal(t, "denied", entry.Outcome)
	assert.Equal(t, "sess-1", entry.SessionID)
	assert.Equal(t, "agent-1", entry.AgentID)
	assert.Equal(t, "sensitive", entry.Details["reason"])
}

func TestDefaultAuditLogConfig(t *testing.T) {
	cfg := DefaultAuditLogConfig()

	assert.Equal(t, 100, cfg.SignatureInterval)
	assert.Equal(t, int64(100*1024*1024), cfg.RotateSize)
	assert.Equal(t, "indefinite", cfg.RetentionPolicy)
}

func TestIntegrityReport_Timing(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 100,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al.Close()

	for i := 0; i < 5; i++ {
		entry := NewAuditEntry(AuditCategoryPermission, "test", "test")
		_ = al.Log(entry)
	}

	report, err := al.VerifyIntegrity()
	require.NoError(t, err)

	assert.False(t, report.StartTime.IsZero())
	assert.False(t, report.EndTime.IsZero())
	assert.True(t, report.EndTime.After(report.StartTime) || report.EndTime.Equal(report.StartTime))
}

func TestAuditLogger_ResumeFromExisting(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 100,
	}

	al1, err := NewAuditLogger(cfg)
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		entry := NewAuditEntry(AuditCategoryPermission, "test", "test")
		_ = al1.Log(entry)
	}
	lastSeq := al1.Sequence()
	lastHash := al1.PreviousHash()
	al1.Close()

	al2, err := NewAuditLogger(cfg)
	require.NoError(t, err)
	defer al2.Close()

	assert.Equal(t, lastSeq, al2.Sequence())
	assert.Equal(t, lastHash, al2.PreviousHash())

	entry := NewAuditEntry(AuditCategoryPermission, "test", "test")
	_ = al2.Log(entry)
	assert.Equal(t, lastSeq+1, al2.Sequence())

	report, err := al2.VerifyIntegrity()
	require.NoError(t, err)
	assert.Equal(t, 6, report.EntriesVerified)
}

func TestAuditLogger_DoubleClose(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{LogPath: logPath}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)

	err = al.Close()
	require.NoError(t, err)

	err = al.Close()
	require.NoError(t, err)
}

func TestAuditLogger_RaceCondition(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	cfg := AuditLogConfig{
		LogPath:           logPath,
		SignatureInterval: 10,
	}

	al, err := NewAuditLogger(cfg)
	require.NoError(t, err)

	var wg sync.WaitGroup
	done := make(chan struct{})

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					entry := NewAuditEntry(AuditCategoryPermission, "test", "test")
					_ = al.Log(entry)
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_, _ = al.VerifyIntegrity()
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)
	close(done)
	wg.Wait()

	al.Close()
}
