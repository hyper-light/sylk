package container

import (
	"errors"
	"testing"
	"time"
)

func TestRestartController_ShouldRestart_Always(t *testing.T) {
	rc := NewRestartController(DefaultRestartControllerConfig(RestartAlways))
	if !rc.ShouldRestart(nil) {
		t.Fatal("RestartAlways should restart on nil error")
	}
	if !rc.ShouldRestart(errors.New("fail")) {
		t.Fatal("RestartAlways should restart on error")
	}
}

func TestRestartController_ShouldRestart_OnFailure(t *testing.T) {
	rc := NewRestartController(DefaultRestartControllerConfig(RestartOnFailure))
	if rc.ShouldRestart(nil) {
		t.Fatal("RestartOnFailure should not restart on nil error")
	}
	if !rc.ShouldRestart(errors.New("fail")) {
		t.Fatal("RestartOnFailure should restart on error")
	}
}

func TestRestartController_ShouldRestart_Never(t *testing.T) {
	rc := NewRestartController(DefaultRestartControllerConfig(RestartNever))
	if rc.ShouldRestart(nil) {
		t.Fatal("RestartNever should not restart")
	}
	if rc.ShouldRestart(errors.New("fail")) {
		t.Fatal("RestartNever should not restart even on error")
	}
}

func TestRestartController_ExponentialBackoff(t *testing.T) {
	rc := NewRestartController(RestartControllerConfig{
		Policy:       RestartAlways,
		BaseDelay:    100 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		ResetTimeout: time.Hour,
	})

	delay1 := rc.RecordRestart()
	delay2 := rc.RecordRestart()
	delay3 := rc.RecordRestart()

	if delay2 <= delay1 {
		t.Fatalf("delay2 (%v) should be > delay1 (%v)", delay2, delay1)
	}
	if delay3 <= delay2 {
		t.Fatalf("delay3 (%v) should be > delay2 (%v)", delay3, delay2)
	}
}

func TestRestartController_BackoffCap(t *testing.T) {
	rc := NewRestartController(RestartControllerConfig{
		Policy:       RestartAlways,
		BaseDelay:    1 * time.Second,
		MaxDelay:     5 * time.Second,
		ResetTimeout: time.Hour,
	})

	// Many restarts should cap at MaxDelay
	var lastDelay time.Duration
	for range 20 {
		lastDelay = rc.RecordRestart()
	}
	if lastDelay > 5*time.Second {
		t.Fatalf("backoff should cap at MaxDelay, got %v", lastDelay)
	}
}

func TestRestartController_Reset(t *testing.T) {
	rc := NewRestartController(DefaultRestartControllerConfig(RestartAlways))
	rc.RecordRestart()
	rc.RecordRestart()
	if rc.Attempts() != 2 {
		t.Fatalf("expected 2 attempts, got %d", rc.Attempts())
	}
	rc.Reset()
	if rc.Attempts() != 0 {
		t.Fatalf("expected 0 attempts after reset, got %d", rc.Attempts())
	}
}

func TestRestartController_AutoReset(t *testing.T) {
	rc := NewRestartController(RestartControllerConfig{
		Policy:       RestartAlways,
		BaseDelay:    1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		ResetTimeout: 50 * time.Millisecond,
	})

	rc.RecordRestart()
	rc.RecordRestart()
	if rc.Attempts() != 2 {
		t.Fatalf("expected 2, got %d", rc.Attempts())
	}

	time.Sleep(60 * time.Millisecond)

	// Next RecordRestart should auto-reset
	rc.RecordRestart()
	if rc.Attempts() != 1 {
		t.Fatalf("expected 1 after auto-reset, got %d", rc.Attempts())
	}
}
