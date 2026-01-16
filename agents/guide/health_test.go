package guide_test

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentHealth_NewAgentHealth tests creating agent health tracker
func TestAgentHealth_NewAgentHealth(t *testing.T) {
	health := guide.NewAgentHealth("test-agent", guide.DefaultAgentHealthConfig())
	require.NotNil(t, health)

	assert.Equal(t, "test-agent", health.AgentID)
	assert.Equal(t, guide.HealthStatusUnknown, health.Status())
	assert.False(t, health.IsHealthy())
}

// TestAgentHealth_RecordHeartbeat tests recording heartbeats
func TestAgentHealth_RecordHeartbeat(t *testing.T) {
	health := guide.NewAgentHealth("test-agent", guide.DefaultAgentHealthConfig())

	// Initially unknown
	assert.Equal(t, guide.HealthStatusUnknown, health.Status())

	// Record heartbeat
	health.RecordHeartbeat()

	// Should now be healthy
	assert.Equal(t, guide.HealthStatusHealthy, health.Status())
	assert.True(t, health.IsHealthy())

	info := health.Info()
	assert.Equal(t, 0, info.MissedHeartbeats)
}

// TestAgentHealth_RecordMissedHeartbeat tests missed heartbeat tracking
func TestAgentHealth_RecordMissedHeartbeat(t *testing.T) {
	cfg := guide.AgentHealthConfig{
		HeartbeatInterval: time.Second,
		UnhealthyAfter:    2,
		DeadAfter:         4,
		ResponseTimeCap:   100,
	}
	health := guide.NewAgentHealth("test-agent", cfg)

	// First heartbeat makes it healthy
	health.RecordHeartbeat()
	assert.Equal(t, guide.HealthStatusHealthy, health.Status())

	// Miss heartbeats until unhealthy
	health.RecordMissedHeartbeat()
	assert.Equal(t, guide.HealthStatusHealthy, health.Status()) // 1 missed - still healthy

	health.RecordMissedHeartbeat()
	assert.Equal(t, guide.HealthStatusUnhealthy, health.Status()) // 2 missed - unhealthy

	// Miss more until dead
	health.RecordMissedHeartbeat()
	health.RecordMissedHeartbeat()
	assert.Equal(t, guide.HealthStatusDead, health.Status()) // 4 missed - dead
}

// TestAgentHealth_HeartbeatResetsCounter tests that heartbeat resets missed count
func TestAgentHealth_HeartbeatResetsCounter(t *testing.T) {
	cfg := guide.AgentHealthConfig{
		HeartbeatInterval: time.Second,
		UnhealthyAfter:    2,
		DeadAfter:         4,
		ResponseTimeCap:   100,
	}
	health := guide.NewAgentHealth("test-agent", cfg)

	health.RecordHeartbeat()
	health.RecordMissedHeartbeat()
	health.RecordMissedHeartbeat()
	assert.Equal(t, guide.HealthStatusUnhealthy, health.Status())

	// Heartbeat should reset
	health.RecordHeartbeat()
	assert.Equal(t, guide.HealthStatusHealthy, health.Status())

	info := health.Info()
	assert.Equal(t, 0, info.MissedHeartbeats)
}

// TestAgentHealth_RecordResponse tests response time tracking
func TestAgentHealth_RecordResponse(t *testing.T) {
	health := guide.NewAgentHealth("test-agent", guide.DefaultAgentHealthConfig())

	// Record some responses
	health.RecordResponse(100 * time.Millisecond)
	health.RecordResponse(200 * time.Millisecond)
	health.RecordResponse(300 * time.Millisecond)

	info := health.Info()
	assert.True(t, info.AvgResponseTimeMs > 0)
}

// TestAgentHealth_SetDegraded tests degraded status
func TestAgentHealth_SetDegraded(t *testing.T) {
	health := guide.NewAgentHealth("test-agent", guide.DefaultAgentHealthConfig())

	// Must be healthy first to be degraded
	health.RecordHeartbeat()
	assert.Equal(t, guide.HealthStatusHealthy, health.Status())

	health.SetDegraded()
	assert.Equal(t, guide.HealthStatusDegraded, health.Status())
	assert.True(t, health.IsHealthy()) // Degraded is still considered "healthy"
}

// TestAgentHealth_Info tests info retrieval
func TestAgentHealth_Info(t *testing.T) {
	health := guide.NewAgentHealth("test-agent", guide.DefaultAgentHealthConfig())
	health.RecordHeartbeat()
	health.RecordResponse(150 * time.Millisecond)

	info := health.Info()

	assert.Equal(t, "test-agent", info.AgentID)
	assert.Equal(t, guide.HealthStatusHealthy, info.Status)
	assert.False(t, info.LastHeartbeat.IsZero())
	assert.False(t, info.LastResponse.IsZero())
	assert.Equal(t, 0, info.MissedHeartbeats)
}

// TestHealthStatus_String tests status string conversion
func TestHealthStatus_String(t *testing.T) {
	tests := []struct {
		status guide.HealthStatus
		expect string
	}{
		{guide.HealthStatusUnknown, "unknown"},
		{guide.HealthStatusHealthy, "healthy"},
		{guide.HealthStatusDegraded, "degraded"},
		{guide.HealthStatusUnhealthy, "unhealthy"},
		{guide.HealthStatusDead, "dead"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expect, tt.status.String())
	}
}

// TestHealthMonitor_NewHealthMonitor tests creating health monitor
func TestHealthMonitor_NewHealthMonitor(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	monitor := guide.NewHealthMonitor(bus, guide.HealthMonitorConfig{
		AgentConfig: guide.DefaultAgentHealthConfig(),
	})
	require.NotNil(t, monitor)
}

// TestHealthMonitor_RegisterUnregister tests agent registration
func TestHealthMonitor_RegisterUnregister(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	monitor := guide.NewHealthMonitor(bus, guide.HealthMonitorConfig{
		AgentConfig: guide.DefaultAgentHealthConfig(),
	})

	// Register agent
	monitor.Register("agent-1")

	// Check status
	status := monitor.GetStatus("agent-1")
	assert.Equal(t, guide.HealthStatusUnknown, status)

	// Unregister
	monitor.Unregister("agent-1")
	status = monitor.GetStatus("agent-1")
	assert.Equal(t, guide.HealthStatusUnknown, status) // Returns unknown for non-existent
}

// TestHealthMonitor_RecordResponse tests response recording
func TestHealthMonitor_RecordResponse(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	monitor := guide.NewHealthMonitor(bus, guide.HealthMonitorConfig{
		AgentConfig: guide.DefaultAgentHealthConfig(),
	})

	monitor.Register("agent-1")
	monitor.RecordResponse("agent-1", 100*time.Millisecond)

	stats := monitor.Stats()
	assert.Equal(t, 1, stats.Total)
}

// TestHealthMonitor_IsHealthy tests health check
func TestHealthMonitor_IsHealthy(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	monitor := guide.NewHealthMonitor(bus, guide.HealthMonitorConfig{
		AgentConfig: guide.DefaultAgentHealthConfig(),
	})

	// Non-existent agent is not healthy
	assert.False(t, monitor.IsHealthy("unknown"))

	// Registered but no heartbeat - not healthy
	monitor.Register("agent-1")
	assert.False(t, monitor.IsHealthy("agent-1"))
}

// TestHealthMonitor_Stats tests statistics retrieval
func TestHealthMonitor_Stats(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	monitor := guide.NewHealthMonitor(bus, guide.HealthMonitorConfig{
		AgentConfig: guide.DefaultAgentHealthConfig(),
	})

	monitor.Register("agent-1")
	monitor.Register("agent-2")

	stats := monitor.Stats()
	assert.Equal(t, 2, stats.Total)
	assert.Equal(t, 0, stats.Healthy)
	assert.Len(t, stats.Agents, 2)
}

// TestHealthMonitor_StartStop tests starting and stopping
func TestHealthMonitor_StartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	monitor := guide.NewHealthMonitor(bus, guide.HealthMonitorConfig{
		AgentConfig: guide.AgentHealthConfig{
			HeartbeatInterval: 50 * time.Millisecond,
			UnhealthyAfter:    2,
			DeadAfter:         4,
			ResponseTimeCap:   100,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	monitor.Start(ctx)

	// Let it run briefly
	time.Sleep(100 * time.Millisecond)

	// Stop both ways
	cancel()
	monitor.Stop()

	// Double stop should be safe
	monitor.Stop()
}

// TestHeartbeatSender_NewHeartbeatSender tests creating heartbeat sender
func TestHeartbeatSender_NewHeartbeatSender(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	sender := guide.NewHeartbeatSender("test-agent", bus, time.Second)
	require.NotNil(t, sender)
}

// TestHeartbeatSender_StartStop tests starting and stopping heartbeat sender
func TestHeartbeatSender_StartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var received int
	_, err := bus.Subscribe("agents.heartbeat", func(msg *guide.Message) error {
		received++
		return nil
	})
	require.NoError(t, err)

	sender := guide.NewHeartbeatSender("test-agent", bus, 50*time.Millisecond)
	sender.Start()

	// Wait for some heartbeats
	time.Sleep(200 * time.Millisecond)

	sender.Stop()

	// Should have received multiple heartbeats
	assert.Greater(t, received, 1)

	// Double stop should be safe
	sender.Stop()
}

// TestHeartbeatSender_DefaultInterval tests default interval
func TestHeartbeatSender_DefaultInterval(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	// Zero interval should use default
	sender := guide.NewHeartbeatSender("test-agent", bus, 0)
	require.NotNil(t, sender)
}
