package signal

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

func TestOSSignalHandler_StartStop(t *testing.T) {
	controller := concurrency.NewPipelineController("test-pipeline", concurrency.PipelineControllerConfig{})
	handler := NewOSSignalHandler(controller)

	handler.Start()
	if !handler.IsRunning() {
		t.Error("expected handler to be running")
	}

	handler.Stop()
	if handler.IsRunning() {
		t.Error("expected handler to be stopped")
	}
}

func TestOSSignalHandler_DoubleStart(t *testing.T) {
	controller := concurrency.NewPipelineController("test-pipeline", concurrency.PipelineControllerConfig{})
	handler := NewOSSignalHandler(controller)

	handler.Start()
	handler.Start()

	if !handler.IsRunning() {
		t.Error("expected handler to be running")
	}

	handler.Stop()
}

func TestOSSignalHandler_DoubleStop(t *testing.T) {
	controller := concurrency.NewPipelineController("test-pipeline", concurrency.PipelineControllerConfig{})
	handler := NewOSSignalHandler(controller)

	handler.Start()
	handler.Stop()
	handler.Stop()

	if handler.IsRunning() {
		t.Error("expected handler to be stopped")
	}
}

func TestOSSignalHandler_InterruptReceived(t *testing.T) {
	controller := concurrency.NewPipelineController("test-pipeline", concurrency.PipelineControllerConfig{
		StopGracePeriod: 50 * time.Millisecond,
	})
	handler := NewOSSignalHandler(controller)

	if handler.InterruptReceived() {
		t.Error("expected no interrupt initially")
	}

	handler.interruptReceived.Store(true)

	if !handler.InterruptReceived() {
		t.Error("expected interrupt received to be true")
	}
}

func TestOSSignalHandler_FirstInterruptTriggersStop(t *testing.T) {
	controller := concurrency.NewPipelineController("test-pipeline", concurrency.PipelineControllerConfig{
		StopGracePeriod: 50 * time.Millisecond,
	})
	handler := NewOSSignalHandler(controller)
	handler.Start()
	defer handler.Stop()

	handler.handleInterrupt(nil)

	if !handler.InterruptReceived() {
		t.Error("expected interrupt received after first SIGINT")
	}

	time.Sleep(100 * time.Millisecond)
	if controller.State() != concurrency.ControllerStateStopped {
		t.Errorf("expected Stopped, got %v", controller.State())
	}
}

func TestOSSignalHandler_SecondInterruptTriggersKill(t *testing.T) {
	controller := concurrency.NewPipelineController("test-pipeline", concurrency.PipelineControllerConfig{
		KillGracePeriod:  50 * time.Millisecond,
		KillHardDeadline: 100 * time.Millisecond,
	})

	handler := NewOSSignalHandler(controller)
	handler.Start()
	defer handler.Stop()

	handler.interruptReceived.Store(true)
	handler.handleInterrupt(nil)

	time.Sleep(150 * time.Millisecond)
	if controller.State() != concurrency.ControllerStateKilled {
		t.Errorf("expected Killed, got %v", controller.State())
	}
}

func TestOSSignalHandler_TerminateTriggersKill(t *testing.T) {
	controller := concurrency.NewPipelineController("test-pipeline", concurrency.PipelineControllerConfig{
		KillGracePeriod:  50 * time.Millisecond,
		KillHardDeadline: 100 * time.Millisecond,
	})
	handler := NewOSSignalHandler(controller)
	handler.Start()
	defer handler.Stop()

	handler.handleTerminate(nil)

	time.Sleep(150 * time.Millisecond)
	if controller.State() != concurrency.ControllerStateKilled {
		t.Errorf("expected Killed, got %v", controller.State())
	}
}

func TestOSSignalHandler_SuspendTriggersPause(t *testing.T) {
	controller := concurrency.NewPipelineController("test-pipeline", concurrency.PipelineControllerConfig{
		PauseTimeout: 50 * time.Millisecond,
	})
	handler := NewOSSignalHandler(controller)
	handler.Start()
	defer handler.Stop()

	handler.handleSuspend(nil)

	if controller.State() != concurrency.ControllerStatePaused {
		t.Errorf("expected Paused, got %v", controller.State())
	}
}

func TestOSSignalHandler_InterruptResetsAfterStop(t *testing.T) {
	controller := concurrency.NewPipelineController("test-pipeline", concurrency.PipelineControllerConfig{
		StopGracePeriod: 50 * time.Millisecond,
	})
	handler := NewOSSignalHandler(controller)
	handler.Start()
	defer handler.Stop()

	handler.handleInterrupt(nil)
	time.Sleep(100 * time.Millisecond)

	if handler.InterruptReceived() {
		t.Error("expected interrupt to reset after successful stop")
	}
}
