package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newTestCircuitBreakerRegistry(t *testing.T) (*GlobalCircuitBreakerRegistry, string) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "circuit-breaker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cfg := CircuitBreakerRegistryConfig{
		DBDir: tmpDir,
		DefaultConfig: CircuitBreakerConfig{
			ConsecutiveFailures: 3,
			CooldownDuration:    100 * time.Millisecond,
			HalfOpenMaxProbes:   1,
		},
	}

	registry, err := NewGlobalCircuitBreakerRegistry(cfg)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create registry: %v", err)
	}

	return registry, tmpDir
}

func TestNewGlobalCircuitBreakerRegistry(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	if registry == nil {
		t.Fatal("expected non-nil registry")
	}
	if registry.db == nil {
		t.Error("expected non-nil db")
	}
	if registry.localCache == nil {
		t.Error("expected non-nil localCache")
	}

	dbPath := filepath.Join(tmpDir, "circuit_breakers.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("expected database file to exist")
	}
}

func TestGlobalCircuitBreakerRegistry_RecordResult_Success(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	state, err := registry.RecordResult("llm:anthropic", true)
	if err != nil {
		t.Fatalf("RecordResult failed: %v", err)
	}

	if state.State != CircuitClosed {
		t.Errorf("expected CircuitClosed, got %v", state.State)
	}
	if state.Failures != 0 {
		t.Errorf("expected 0 failures, got %d", state.Failures)
	}
}

func TestGlobalCircuitBreakerRegistry_RecordResult_Failure(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	state, err := registry.RecordResult("llm:anthropic", false)
	if err != nil {
		t.Fatalf("RecordResult failed: %v", err)
	}

	if state.Failures != 1 {
		t.Errorf("expected 1 failure, got %d", state.Failures)
	}
	if state.LastFailure == nil {
		t.Error("expected non-nil LastFailure")
	}
}

func TestGlobalCircuitBreakerRegistry_TripCircuit(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	for i := 0; i < 3; i++ {
		registry.RecordResult("llm:anthropic", false)
	}

	state := registry.GetState("llm:anthropic")
	if state.State != CircuitOpen {
		t.Errorf("expected CircuitOpen after 3 failures, got %v", state.State)
	}
	if state.CooldownEnds == nil {
		t.Error("expected non-nil CooldownEnds")
	}
}

func TestGlobalCircuitBreakerRegistry_Allow_Closed(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	registry.RecordResult("llm:anthropic", true)

	if !registry.Allow("llm:anthropic") {
		t.Error("expected Allow=true for closed circuit")
	}
}

func TestGlobalCircuitBreakerRegistry_Allow_Open(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	for i := 0; i < 3; i++ {
		registry.RecordResult("llm:anthropic", false)
	}

	if registry.Allow("llm:anthropic") {
		t.Error("expected Allow=false for open circuit")
	}
}

func TestGlobalCircuitBreakerRegistry_Allow_CooldownExpired(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	for i := 0; i < 3; i++ {
		registry.RecordResult("llm:anthropic", false)
	}

	time.Sleep(150 * time.Millisecond)

	if !registry.Allow("llm:anthropic") {
		t.Error("expected Allow=true after cooldown expires")
	}
}

func TestGlobalCircuitBreakerRegistry_Allow_Unknown(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	if !registry.Allow("unknown:resource") {
		t.Error("expected Allow=true for unknown resource")
	}
}

func TestGlobalCircuitBreakerRegistry_RecoverFromHalfOpen(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	for i := 0; i < 3; i++ {
		registry.RecordResult("llm:anthropic", false)
	}

	registry.TransitionToHalfOpen("llm:anthropic")
	registry.RecordResult("llm:anthropic", true)

	state := registry.GetState("llm:anthropic")
	if state.State != CircuitClosed {
		t.Errorf("expected CircuitClosed after recovery, got %v", state.State)
	}
}

func TestGlobalCircuitBreakerRegistry_FailFromHalfOpen(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	for i := 0; i < 3; i++ {
		registry.RecordResult("llm:anthropic", false)
	}

	registry.TransitionToHalfOpen("llm:anthropic")
	registry.RecordResult("llm:anthropic", false)

	state := registry.GetState("llm:anthropic")
	if state.State != CircuitOpen {
		t.Errorf("expected CircuitOpen after half-open failure, got %v", state.State)
	}
}

func TestGlobalCircuitBreakerRegistry_GetState_NotFound(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	state := registry.GetState("nonexistent")
	if state != nil {
		t.Error("expected nil for nonexistent resource")
	}
}

func TestGlobalCircuitBreakerRegistry_GetAllStates(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	registry.RecordResult("llm:anthropic", true)
	registry.RecordResult("llm:openai", true)

	states := registry.GetAllStates()
	if len(states) != 2 {
		t.Errorf("expected 2 states, got %d", len(states))
	}
}

func TestGlobalCircuitBreakerRegistry_Reset(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	for i := 0; i < 3; i++ {
		registry.RecordResult("llm:anthropic", false)
	}

	err := registry.Reset("llm:anthropic")
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	state := registry.GetState("llm:anthropic")
	if state != nil {
		t.Error("expected nil after reset")
	}
}

func TestGlobalCircuitBreakerRegistry_OnSignalReceived(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	signal := CrossSessionSignal{
		Type:    SignalCircuitStateChange,
		Payload: `{"resource_id":"llm:test","state":1,"failures":5}`,
	}

	registry.OnSignalReceived(signal)

	state := registry.GetState("llm:test")
	if state == nil {
		t.Fatal("expected state to be set from signal")
	}
	if state.State != CircuitOpen {
		t.Errorf("expected CircuitOpen, got %v", state.State)
	}
}

func TestGlobalCircuitBreakerRegistry_OnSignalReceived_WrongType(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	signal := CrossSessionSignal{
		Type:    "other_signal",
		Payload: `{"resource_id":"llm:test","state":1}`,
	}

	registry.OnSignalReceived(signal)

	state := registry.GetState("llm:test")
	if state != nil {
		t.Error("expected nil - wrong signal type should be ignored")
	}
}

func TestGlobalCircuitBreakerRegistry_Persistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "circuit-breaker-persist-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := CircuitBreakerRegistryConfig{
		DBDir: tmpDir,
		DefaultConfig: CircuitBreakerConfig{
			ConsecutiveFailures: 3,
			CooldownDuration:    1 * time.Hour,
		},
	}

	registry1, err := NewGlobalCircuitBreakerRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		registry1.RecordResult("llm:anthropic", false)
	}
	registry1.Close()

	registry2, err := NewGlobalCircuitBreakerRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry2.Close()

	state := registry2.GetState("llm:anthropic")
	if state == nil {
		t.Fatal("expected state to persist")
	}
	if state.State != CircuitOpen {
		t.Errorf("expected CircuitOpen to persist, got %v", state.State)
	}
}

func TestGlobalCircuitBreakerRegistry_Close(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)

	err := registry.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, err = registry.RecordResult("llm:test", true)
	if err != ErrCircuitBreakerRegistryClosed {
		t.Errorf("expected ErrCircuitBreakerRegistryClosed, got %v", err)
	}
}

func TestGlobalCircuitBreakerRegistry_DoubleClose(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)

	registry.Close()
	err := registry.Close()

	if err != ErrCircuitBreakerRegistryClosed {
		t.Errorf("expected ErrCircuitBreakerRegistryClosed on double close, got %v", err)
	}
}

func TestGlobalCircuitBreakerRegistry_TransitionToHalfOpen(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	for i := 0; i < 3; i++ {
		registry.RecordResult("llm:anthropic", false)
	}

	err := registry.TransitionToHalfOpen("llm:anthropic")
	if err != nil {
		t.Fatalf("TransitionToHalfOpen failed: %v", err)
	}

	state := registry.GetState("llm:anthropic")
	if state.State != CircuitHalfOpen {
		t.Errorf("expected CircuitHalfOpen, got %v", state.State)
	}
}

func TestGlobalCircuitBreakerRegistry_TransitionToHalfOpen_NotOpen(t *testing.T) {
	registry, tmpDir := newTestCircuitBreakerRegistry(t)
	defer os.RemoveAll(tmpDir)
	defer registry.Close()

	registry.RecordResult("llm:anthropic", true)

	err := registry.TransitionToHalfOpen("llm:anthropic")
	if err != nil {
		t.Fatalf("TransitionToHalfOpen failed: %v", err)
	}

	state := registry.GetState("llm:anthropic")
	if state.State != CircuitClosed {
		t.Errorf("expected CircuitClosed to remain unchanged, got %v", state.State)
	}
}

func TestCircuitState_String(t *testing.T) {
	tests := []struct {
		state    CircuitState
		expected string
	}{
		{CircuitClosed, "closed"},
		{CircuitOpen, "open"},
		{CircuitHalfOpen, "half_open"},
		{CircuitState(99), "unknown"},
	}

	for _, tt := range tests {
		result := tt.state.String()
		if result != tt.expected {
			t.Errorf("CircuitState(%d).String() = %s, want %s", tt.state, result, tt.expected)
		}
	}
}
