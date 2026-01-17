package tools

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdaptiveTimeout_BaseTimeout(t *testing.T) {
	cfg := AdaptiveTimeoutConfig{
		BaseTimeout:    100 * time.Millisecond,
		ExtendOnOutput: 50 * time.Millisecond,
		MaxTimeout:     1 * time.Second,
	}

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	at.Start()

	assert.False(t, at.ShouldTimeout())

	time.Sleep(150 * time.Millisecond)

	assert.True(t, at.ShouldTimeout())
}

func TestAdaptiveTimeout_ExtendOnMeaningfulOutput(t *testing.T) {
	cfg := AdaptiveTimeoutConfig{
		BaseTimeout:    100 * time.Millisecond,
		ExtendOnOutput: 100 * time.Millisecond,
		MaxTimeout:     1 * time.Second,
	}

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	at.Start()

	time.Sleep(80 * time.Millisecond)
	at.OnOutput("meaningful output line")

	time.Sleep(50 * time.Millisecond)
	assert.False(t, at.ShouldTimeout())

	time.Sleep(80 * time.Millisecond)
	assert.True(t, at.ShouldTimeout())
}

func TestAdaptiveTimeout_NoiseDoesNotExtend(t *testing.T) {
	cfg := AdaptiveTimeoutConfig{
		BaseTimeout:    100 * time.Millisecond,
		ExtendOnOutput: 100 * time.Millisecond,
		MaxTimeout:     1 * time.Second,
	}

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	at.Start()

	time.Sleep(80 * time.Millisecond)

	at.OnOutput("...")
	at.OnOutput("||||")
	at.OnOutput("50%")
	at.OnOutput("   ")
	at.OnOutput("")

	time.Sleep(50 * time.Millisecond)
	assert.True(t, at.ShouldTimeout())
}

func TestAdaptiveTimeout_MaxTimeoutCaps(t *testing.T) {
	cfg := AdaptiveTimeoutConfig{
		BaseTimeout:    50 * time.Millisecond,
		ExtendOnOutput: 100 * time.Millisecond,
		MaxTimeout:     150 * time.Millisecond,
	}

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	at.Start()

	for i := 0; i < 10; i++ {
		time.Sleep(20 * time.Millisecond)
		at.OnOutput("keep alive output")
	}

	assert.True(t, at.ShouldTimeout())
}

func TestAdaptiveTimeout_Reset(t *testing.T) {
	cfg := DefaultAdaptiveTimeoutConfig()

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	at.Start()
	at.OnOutput("some output")

	assert.Greater(t, at.OutputCount(), int64(0))
	assert.False(t, at.Elapsed() == 0)

	at.Reset()

	assert.Equal(t, int64(0), at.OutputCount())
	assert.Equal(t, time.Duration(0), at.Elapsed())
}

func TestAdaptiveTimeout_ShouldTimeoutAt(t *testing.T) {
	cfg := AdaptiveTimeoutConfig{
		BaseTimeout:    100 * time.Millisecond,
		ExtendOnOutput: 50 * time.Millisecond,
		MaxTimeout:     1 * time.Second,
	}

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	at.Start()
	time.Sleep(150 * time.Millisecond)

	started := time.Now().Add(-200 * time.Millisecond)
	assert.True(t, at.ShouldTimeoutAt(started))

	at.OnOutput("output")
	assert.False(t, at.ShouldTimeoutAt(started))
}

func TestAdaptiveTimeout_Elapsed(t *testing.T) {
	cfg := DefaultAdaptiveTimeoutConfig()

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	assert.Equal(t, time.Duration(0), at.Elapsed())

	at.Start()
	time.Sleep(50 * time.Millisecond)

	elapsed := at.Elapsed()
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond)
	assert.Less(t, elapsed, 100*time.Millisecond)
}

func TestAdaptiveTimeout_TimeSinceLastOutput(t *testing.T) {
	cfg := DefaultAdaptiveTimeoutConfig()

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	assert.Equal(t, time.Duration(0), at.TimeSinceLastOutput())

	at.Start()
	at.OnOutput("output")
	time.Sleep(50 * time.Millisecond)

	sinceOutput := at.TimeSinceLastOutput()
	assert.GreaterOrEqual(t, sinceOutput, 50*time.Millisecond)
	assert.Less(t, sinceOutput, 100*time.Millisecond)
}

func TestAdaptiveTimeout_OutputCount(t *testing.T) {
	cfg := DefaultAdaptiveTimeoutConfig()

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	at.Start()

	assert.Equal(t, int64(0), at.OutputCount())

	at.OnOutput("line 1")
	at.OnOutput("line 2")
	at.OnOutput("line 3")
	at.OnOutput("...")

	assert.Equal(t, int64(3), at.OutputCount())
}

func TestAdaptiveTimeout_RemainingTime(t *testing.T) {
	cfg := AdaptiveTimeoutConfig{
		BaseTimeout:    200 * time.Millisecond,
		ExtendOnOutput: 100 * time.Millisecond,
		MaxTimeout:     500 * time.Millisecond,
	}

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	remaining := at.RemainingTime()
	assert.Equal(t, cfg.BaseTimeout, remaining)

	at.Start()
	time.Sleep(50 * time.Millisecond)

	remaining = at.RemainingTime()
	assert.Less(t, remaining, cfg.BaseTimeout)
	assert.Greater(t, remaining, time.Duration(0))
}

func TestAdaptiveTimeout_ConfigAccessors(t *testing.T) {
	cfg := AdaptiveTimeoutConfig{
		BaseTimeout:    100 * time.Millisecond,
		ExtendOnOutput: 200 * time.Millisecond,
		MaxTimeout:     300 * time.Millisecond,
	}

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	assert.Equal(t, cfg.BaseTimeout, at.BaseTimeout())
	assert.Equal(t, cfg.ExtendOnOutput, at.ExtendOnOutput())
	assert.Equal(t, cfg.MaxTimeout, at.MaxTimeout())
}

func TestAdaptiveTimeout_DefaultConfig(t *testing.T) {
	cfg := DefaultAdaptiveTimeoutConfig()

	assert.Equal(t, DefaultBaseTimeout, cfg.BaseTimeout)
	assert.Equal(t, DefaultExtendOnOutput, cfg.ExtendOnOutput)
	assert.Equal(t, DefaultMaxTimeout, cfg.MaxTimeout)
	assert.Equal(t, DefaultCheckInterval, cfg.CheckInterval)
	assert.Len(t, cfg.NoisePatterns, len(DefaultNoisePatterns))
}

func TestAdaptiveTimeout_CustomNoisePatterns(t *testing.T) {
	cfg := AdaptiveTimeoutConfig{
		BaseTimeout:    100 * time.Millisecond,
		ExtendOnOutput: 100 * time.Millisecond,
		MaxTimeout:     1 * time.Second,
		NoisePatterns:  []string{`^NOISE:.*$`},
	}

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	at.Start()

	time.Sleep(80 * time.Millisecond)

	at.OnOutput("NOISE: ignored")
	at.OnOutput("...")

	assert.Equal(t, int64(1), at.OutputCount())
}

func TestAdaptiveTimeout_InvalidNoisePattern(t *testing.T) {
	cfg := AdaptiveTimeoutConfig{
		NoisePatterns: []string{`[invalid`},
	}

	_, err := NewAdaptiveTimeout(cfg)
	assert.Error(t, err)
}

func TestAdaptiveTimeout_ConcurrentAccess(t *testing.T) {
	cfg := AdaptiveTimeoutConfig{
		BaseTimeout:    1 * time.Second,
		ExtendOnOutput: 500 * time.Millisecond,
		MaxTimeout:     5 * time.Second,
	}

	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	at.Start()

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				at.OnOutput("output from goroutine")
				at.ShouldTimeout()
				at.Elapsed()
				at.TimeSinceLastOutput()
				at.OutputCount()
				at.RemainingTime()
			}
		}(i)
	}

	wg.Wait()
}

func TestToolTimeoutManager_SetAndGet(t *testing.T) {
	defaultCfg := DefaultAdaptiveTimeoutConfig()
	mgr := NewToolTimeoutManager(defaultCfg)

	customCfg := AdaptiveTimeoutConfig{
		BaseTimeout:    5 * time.Minute,
		ExtendOnOutput: 1 * time.Minute,
		MaxTimeout:     1 * time.Hour,
	}

	mgr.SetToolTimeout("long-running-tool", customCfg)

	at, err := mgr.GetTimeoutForTool("long-running-tool")
	require.NoError(t, err)
	assert.Equal(t, 5*time.Minute, at.BaseTimeout())

	atDefault, err := mgr.GetTimeoutForTool("unknown-tool")
	require.NoError(t, err)
	assert.Equal(t, defaultCfg.BaseTimeout, atDefault.BaseTimeout())
}

func TestToolTimeoutManager_RemoveToolTimeout(t *testing.T) {
	defaultCfg := DefaultAdaptiveTimeoutConfig()
	mgr := NewToolTimeoutManager(defaultCfg)

	customCfg := AdaptiveTimeoutConfig{
		BaseTimeout: 5 * time.Minute,
	}

	mgr.SetToolTimeout("tool1", customCfg)

	at, err := mgr.GetTimeoutForTool("tool1")
	require.NoError(t, err)
	assert.Equal(t, 5*time.Minute, at.BaseTimeout())

	mgr.RemoveToolTimeout("tool1")

	at, err = mgr.GetTimeoutForTool("tool1")
	require.NoError(t, err)
	assert.Equal(t, defaultCfg.BaseTimeout, at.BaseTimeout())
}

func TestToolTimeoutManager_ListToolTimeouts(t *testing.T) {
	defaultCfg := DefaultAdaptiveTimeoutConfig()
	mgr := NewToolTimeoutManager(defaultCfg)

	tools := mgr.ListToolTimeouts()
	assert.Len(t, tools, 0)

	mgr.SetToolTimeout("tool1", AdaptiveTimeoutConfig{BaseTimeout: time.Minute})
	mgr.SetToolTimeout("tool2", AdaptiveTimeoutConfig{BaseTimeout: time.Minute})

	tools = mgr.ListToolTimeouts()
	assert.Len(t, tools, 2)
	assert.Contains(t, tools, "tool1")
	assert.Contains(t, tools, "tool2")
}

func TestToolTimeoutManager_ConcurrentAccess(t *testing.T) {
	defaultCfg := DefaultAdaptiveTimeoutConfig()
	mgr := NewToolTimeoutManager(defaultCfg)

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				toolName := "tool"
				mgr.SetToolTimeout(toolName, AdaptiveTimeoutConfig{
					BaseTimeout: time.Duration(id*j) * time.Millisecond,
				})
				mgr.GetTimeoutForTool(toolName)
				mgr.ListToolTimeouts()
			}
		}(i)
	}

	wg.Wait()
}

func TestAdaptiveTimeout_NoisePatternMatching(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		isNoise bool
	}{
		{"dots", "...", true},
		{"many dots", "...........", true},
		{"spinner pipe", "||||", true},
		{"spinner slash", "////", true},
		{"spinner backslash", "\\\\\\\\", true},
		{"spinner mixed", "|/-\\", true},
		{"percentage", "50%", true},
		{"percentage 100", "100%", true},
		{"whitespace", "   ", true},
		{"empty", "", true},
		{"progress bar", "[=====>    ]", true},
		{"meaningful text", "Building project...", false},
		{"error message", "Error: something failed", false},
		{"file path", "/path/to/file.go", false},
		{"percentage with text", "50% complete", false},
	}

	cfg := DefaultAdaptiveTimeoutConfig()
	at, err := NewAdaptiveTimeout(cfg)
	require.NoError(t, err)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			at.Reset()
			at.Start()

			initialCount := at.OutputCount()
			at.OnOutput(tc.line)

			if tc.isNoise {
				assert.Equal(t, initialCount, at.OutputCount())
			} else {
				assert.Equal(t, initialCount+1, at.OutputCount())
			}
		})
	}
}
