package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKillSequenceManager_CleanExitOnSIGINT(t *testing.T) {
	mgr := NewKillSequenceManager(KillSequenceConfig{
		SIGINTGrace:  500 * time.Millisecond,
		SIGTERMGrace: 500 * time.Millisecond,
	})

	waitDone := make(chan struct{})

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(waitDone)
	}()

	result := mgr.Execute(nil, waitDone)

	assert.True(t, result.SentSIGINT)
	assert.False(t, result.SentSIGTERM)
	assert.False(t, result.SentSIGKILL)
	assert.Equal(t, "SIGINT", result.ExitedAfter)
	assert.Less(t, result.Duration, 200*time.Millisecond)
}

func TestKillSequenceManager_EscalateToSIGTERM(t *testing.T) {
	mgr := NewKillSequenceManager(KillSequenceConfig{
		SIGINTGrace:  50 * time.Millisecond,
		SIGTERMGrace: 500 * time.Millisecond,
	})

	waitDone := make(chan struct{})

	go func() {
		time.Sleep(100 * time.Millisecond)
		close(waitDone)
	}()

	result := mgr.Execute(nil, waitDone)

	assert.True(t, result.SentSIGINT)
	assert.True(t, result.SentSIGTERM)
	assert.False(t, result.SentSIGKILL)
	assert.Equal(t, "SIGTERM", result.ExitedAfter)
}

func TestKillSequenceManager_EscalateToSIGKILL(t *testing.T) {
	mgr := NewKillSequenceManager(KillSequenceConfig{
		SIGINTGrace:  50 * time.Millisecond,
		SIGTERMGrace: 50 * time.Millisecond,
	})

	waitDone := make(chan struct{})

	result := mgr.Execute(nil, waitDone)

	assert.True(t, result.SentSIGINT)
	assert.True(t, result.SentSIGTERM)
	assert.True(t, result.SentSIGKILL)
	assert.Equal(t, "SIGKILL", result.ExitedAfter)
	assert.GreaterOrEqual(t, result.Duration, 100*time.Millisecond)
}

func TestKillSequenceManager_TimingRespected(t *testing.T) {
	mgr := NewKillSequenceManager(KillSequenceConfig{
		SIGINTGrace:  100 * time.Millisecond,
		SIGTERMGrace: 100 * time.Millisecond,
	})

	waitDone := make(chan struct{})

	result := mgr.Execute(nil, waitDone)

	assert.GreaterOrEqual(t, result.Duration, 200*time.Millisecond)
	assert.Less(t, result.Duration, 300*time.Millisecond)
}

func TestKillSequenceManager_ExecuteWithProgress(t *testing.T) {
	mgr := NewKillSequenceManager(KillSequenceConfig{
		SIGINTGrace:  50 * time.Millisecond,
		SIGTERMGrace: 50 * time.Millisecond,
	})

	waitDone := make(chan struct{})

	var stages []string
	callback := func(stage string) {
		stages = append(stages, stage)
	}

	result := mgr.ExecuteWithProgress(nil, waitDone, callback)

	assert.Equal(t, []string{"stopping", "force_stopping", "killed"}, stages)
	assert.True(t, result.SentSIGKILL)
}

func TestKillSequenceManager_ExecuteWithProgressEarlyExit(t *testing.T) {
	mgr := NewKillSequenceManager(KillSequenceConfig{
		SIGINTGrace:  500 * time.Millisecond,
		SIGTERMGrace: 500 * time.Millisecond,
	})

	waitDone := make(chan struct{})

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(waitDone)
	}()

	var stages []string
	callback := func(stage string) {
		stages = append(stages, stage)
	}

	result := mgr.ExecuteWithProgress(nil, waitDone, callback)

	assert.Equal(t, []string{"stopping"}, stages)
	assert.Equal(t, "SIGINT", result.ExitedAfter)
}

func TestKillSequenceManager_ConfigAccessors(t *testing.T) {
	cfg := KillSequenceConfig{
		SIGINTGrace:  10 * time.Second,
		SIGTERMGrace: 5 * time.Second,
	}
	mgr := NewKillSequenceManager(cfg)

	assert.Equal(t, 10*time.Second, mgr.SIGINTGrace())
	assert.Equal(t, 5*time.Second, mgr.SIGTERMGrace())
	assert.Equal(t, 15*time.Second, mgr.TotalGrace())
}

func TestKillSequenceManager_DefaultConfig(t *testing.T) {
	cfg := DefaultKillSequenceConfig()

	assert.Equal(t, DefaultSIGINTGrace, cfg.SIGINTGrace)
	assert.Equal(t, DefaultSIGTERMGrace, cfg.SIGTERMGrace)
}

func TestKillSequenceManager_NormalizeConfig(t *testing.T) {
	cfg := KillSequenceConfig{}
	mgr := NewKillSequenceManager(cfg)

	assert.Equal(t, DefaultSIGINTGrace, mgr.SIGINTGrace())
	assert.Equal(t, DefaultSIGTERMGrace, mgr.SIGTERMGrace())
}

func TestKillSequenceManager_ExecuteAsync(t *testing.T) {
	mgr := NewKillSequenceManager(KillSequenceConfig{
		SIGINTGrace:  50 * time.Millisecond,
		SIGTERMGrace: 50 * time.Millisecond,
	})

	pg := NewProcessGroup()

	done := make(chan KillResult, 1)
	mgr.ExecuteAsync(pg, done)

	select {
	case result := <-done:
		assert.True(t, result.SentSIGINT)
		assert.Equal(t, "SIGINT", result.ExitedAfter)
	case <-time.After(1 * time.Second):
		t.Fatal("ExecuteAsync did not complete in time")
	}
}

func TestProcessGroup_Basic(t *testing.T) {
	pg := NewProcessGroup()

	assert.Equal(t, 0, pg.Pid())
	assert.False(t, pg.IsKilled())
}

func TestProcessGroup_KillWithoutStart(t *testing.T) {
	pg := NewProcessGroup()

	err := pg.Kill()
	require.NoError(t, err)

	assert.True(t, pg.IsKilled())
}

func TestProcessGroup_WaitWithoutStart(t *testing.T) {
	pg := NewProcessGroup()

	err := pg.Wait()
	require.NoError(t, err)
}

func TestOrphanTracker_Track(t *testing.T) {
	tracker := NewOrphanTracker(time.Minute)

	entry := tracker.Track(1234, "test-proc")

	assert.Equal(t, 1234, entry.PID)
	assert.Equal(t, "test-proc", entry.Description)
	assert.True(t, entry.StillAlive)
	assert.Equal(t, 1, tracker.Count())
}

func TestOrphanTracker_Remove(t *testing.T) {
	tracker := NewOrphanTracker(time.Minute)

	tracker.Track(1234, "test-proc")
	assert.Equal(t, 1, tracker.Count())

	tracker.Remove(1234)
	assert.Equal(t, 0, tracker.Count())
}

func TestOrphanTracker_Get(t *testing.T) {
	tracker := NewOrphanTracker(time.Minute)

	tracker.Track(1234, "test-proc")

	entry, ok := tracker.Get(1234)
	assert.True(t, ok)
	assert.Equal(t, 1234, entry.PID)

	_, ok = tracker.Get(9999)
	assert.False(t, ok)
}

func TestOrphanTracker_List(t *testing.T) {
	tracker := NewOrphanTracker(time.Minute)

	tracker.Track(1, "proc-1")
	tracker.Track(2, "proc-2")
	tracker.Track(3, "proc-3")

	list := tracker.List()
	assert.Len(t, list, 3)
}

func TestOrphanTracker_OnOrphan(t *testing.T) {
	tracker := NewOrphanTracker(time.Minute)

	var callbackEntry *OrphanEntry
	tracker.OnOrphan(func(entry *OrphanEntry) {
		callbackEntry = entry
	})

	tracker.Track(5678, "callback-test")

	assert.NotNil(t, callbackEntry)
	assert.Equal(t, 5678, callbackEntry.PID)
}

func TestOrphanedProcessError_Error(t *testing.T) {
	err := &OrphanedProcessError{PID: 1234, Description: "test-process"}

	msg := err.Error()
	assert.Contains(t, msg, "orphaned process")
	assert.Contains(t, msg, "1234")
	assert.Contains(t, msg, "test-process")
}

func TestExtendedKillSequenceManager_DefaultConfig(t *testing.T) {
	cfg := DefaultExtendedKillSequenceConfig()

	assert.Equal(t, DefaultSIGINTGrace, cfg.SIGINTGrace)
	assert.Equal(t, DefaultSIGTERMGrace, cfg.SIGTERMGrace)
	assert.Equal(t, 500*time.Millisecond, cfg.ForceCloseWait)
}

func TestExtendedKillSequenceManager_HasOrphanTracker(t *testing.T) {
	mgr := NewExtendedKillSequenceManager(DefaultExtendedKillSequenceConfig())

	assert.NotNil(t, mgr.OrphanTracker())
}

func TestExtendedKillSequenceManager_CustomOrphanTracker(t *testing.T) {
	tracker := NewOrphanTracker(time.Hour)

	cfg := DefaultExtendedKillSequenceConfig()
	cfg.OrphanTracker = tracker

	mgr := NewExtendedKillSequenceManager(cfg)

	assert.Same(t, tracker, mgr.OrphanTracker())
}

func TestKillPhase_Values(t *testing.T) {
	assert.Equal(t, KillPhase(0), KillPhaseSIGINT)
	assert.Equal(t, KillPhase(1), KillPhaseSIGTERM)
	assert.Equal(t, KillPhase(2), KillPhaseSIGKILL)
	assert.Equal(t, KillPhase(3), KillPhaseForceCloseResources)
	assert.Equal(t, KillPhase(4), KillPhaseOrphanTracking)
}
