package archivalist

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Registry Unit Tests
// =============================================================================

func TestRegistry_Register(t *testing.T) {
	registry := newTestRegistry(t)

	agent, err := registry.Register("test-agent", "session-1", "", SourceModelClaudeOpus45)

	require.NoError(t, err, "Register")
	assert.NotEmpty(t, agent.ID, "ID should not be empty")
	assert.Equal(t, "test-agent", agent.Name, "Name should match")
	assert.Equal(t, "session-1", agent.SessionID, "SessionID should match")
	assert.Equal(t, AgentStatusActive, agent.Status, "Status should be active")
	assert.Equal(t, SourceModelClaudeOpus45, agent.Source, "Source should match")
	assert.False(t, agent.RegisteredAt.IsZero(), "RegisteredAt should be set")
	assert.False(t, agent.LastSeenAt.IsZero(), "LastSeenAt should be set")
}

func TestRegistry_Register_WithParent(t *testing.T) {
	registry := newTestRegistry(t)

	parent, _ := registry.Register("parent-agent", "session-1", "", SourceModelClaudeOpus45)
	child, err := registry.Register("child-agent", "session-1", parent.ID, SourceModelClaudeOpus45)

	require.NoError(t, err, "Register child")
	assert.Equal(t, parent.ID, child.ParentID, "Parent ID should match")
	assert.True(t, child.IsSubAgent(), "Child should be a sub-agent")

	// Verify parent has child in children list
	updatedParent := registry.Get(parent.ID)
	assert.Contains(t, updatedParent.Children, child.ID, "Parent should have child in children list")
}

func TestRegistry_Register_InvalidParent(t *testing.T) {
	registry := newTestRegistry(t)

	_, err := registry.Register("child-agent", "session-1", "nonexistent-parent", SourceModelClaudeOpus45)

	assert.Error(t, err, "Register with invalid parent should fail")
}

func TestRegistry_Register_ParentDifferentSession(t *testing.T) {
	registry := newTestRegistry(t)

	parent, _ := registry.Register("parent-agent", "session-1", "", SourceModelClaudeOpus45)
	_, err := registry.Register("child-agent", "session-2", parent.ID, SourceModelClaudeOpus45)

	assert.Error(t, err, "Register with parent in different session should fail")
}

func TestRegistry_Register_ReactivateExisting(t *testing.T) {
	registry := newTestRegistry(t)

	// Register agent
	agent1, _ := registry.Register("test-agent", "session-1", "", SourceModelClaudeOpus45)
	agent1.Status = AgentStatusIdle

	// Re-register same name in same session
	agent2, err := registry.Register("test-agent", "session-1", "", SourceModelClaudeOpus45)

	require.NoError(t, err, "Re-register")
	assert.Equal(t, agent1.ID, agent2.ID, "Should return same agent")
	assert.Equal(t, AgentStatusActive, agent2.Status, "Should be reactivated")
}

func TestRegistry_Unregister(t *testing.T) {
	registry := newTestRegistry(t)

	agent, _ := registry.Register("test-agent", "session-1", "", SourceModelClaudeOpus45)

	err := registry.Unregister(agent.ID)

	require.NoError(t, err, "Unregister")
	assert.Nil(t, registry.Get(agent.ID), "Agent should be removed")
}

func TestRegistry_Unregister_WithChildren(t *testing.T) {
	registry := newTestRegistry(t)

	parent, _ := registry.Register("parent", "session-1", "", SourceModelClaudeOpus45)
	child1, _ := registry.Register("child1", "session-1", parent.ID, SourceModelClaudeOpus45)
	child2, _ := registry.Register("child2", "session-1", parent.ID, SourceModelClaudeOpus45)

	err := registry.Unregister(parent.ID)

	require.NoError(t, err, "Unregister parent")
	assert.Nil(t, registry.Get(parent.ID), "Parent should be removed")
	assert.Nil(t, registry.Get(child1.ID), "Child1 should be removed")
	assert.Nil(t, registry.Get(child2.ID), "Child2 should be removed")
}

func TestRegistry_Unregister_NotFound(t *testing.T) {
	registry := newTestRegistry(t)

	err := registry.Unregister("nonexistent")

	assert.Error(t, err, "Unregister nonexistent should fail")
}

func TestRegistry_Get(t *testing.T) {
	registry := newTestRegistry(t)

	agent, _ := registry.Register("test-agent", "session-1", "", SourceModelClaudeOpus45)

	retrieved := registry.Get(agent.ID)

	assert.NotNil(t, retrieved, "Should find agent")
	assert.Equal(t, agent.ID, retrieved.ID, "ID should match")
}

func TestRegistry_Get_NotFound(t *testing.T) {
	registry := newTestRegistry(t)

	retrieved := registry.Get("nonexistent")

	assert.Nil(t, retrieved, "Should return nil for nonexistent")
}

func TestRegistry_GetByName(t *testing.T) {
	registry := newTestRegistry(t)

	agent, _ := registry.Register("unique-name", "session-1", "", SourceModelClaudeOpus45)

	retrieved := registry.GetByName("unique-name")

	assert.NotNil(t, retrieved, "Should find agent by name")
	assert.Equal(t, agent.ID, retrieved.ID, "ID should match")
}

func TestRegistry_GetByName_NotFound(t *testing.T) {
	registry := newTestRegistry(t)

	retrieved := registry.GetByName("nonexistent")

	assert.Nil(t, retrieved, "Should return nil")
}

func TestRegistry_GetBySession(t *testing.T) {
	registry := newTestRegistry(t)

	registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)
	registry.Register("agent2", "session-1", "", SourceModelClaudeOpus45)
	registry.Register("agent3", "session-2", "", SourceModelClaudeOpus45)

	agents := registry.GetBySession("session-1")

	assert.Len(t, agents, 2, "Should find 2 agents in session-1")
}

func TestRegistry_Touch(t *testing.T) {
	registry := newTestRegistry(t)

	agent, _ := registry.Register("test-agent", "session-1", "", SourceModelClaudeOpus45)
	originalLastSeen := agent.LastSeenAt
	originalClock := agent.Clock

	time.Sleep(10 * time.Millisecond)

	err := registry.Touch(agent.ID)

	require.NoError(t, err, "Touch")

	updated := registry.Get(agent.ID)
	assert.True(t, updated.LastSeenAt.After(originalLastSeen), "LastSeenAt should be updated")
	assert.Greater(t, updated.Clock, originalClock, "Clock should be incremented")
	assert.Equal(t, AgentStatusActive, updated.Status, "Status should be active")
}

func TestRegistry_Touch_NotFound(t *testing.T) {
	registry := newTestRegistry(t)

	err := registry.Touch("nonexistent")

	assert.Error(t, err, "Touch nonexistent should fail")
}

func TestRegistry_UpdateVersion(t *testing.T) {
	registry := newTestRegistry(t)

	agent, _ := registry.Register("test-agent", "session-1", "", SourceModelClaudeOpus45)

	err := registry.UpdateVersion(agent.ID, "v5")

	require.NoError(t, err, "UpdateVersion")

	updated := registry.Get(agent.ID)
	assert.Equal(t, "v5", updated.LastVersion, "Version should be updated")
}

func TestRegistry_UpdateStatuses(t *testing.T) {
	registry := NewRegistry(RegistryConfig{
		IdleTimeout:     50 * time.Millisecond,
		InactiveTimeout: 100 * time.Millisecond,
	})

	agent, _ := registry.Register("test-agent", "session-1", "", SourceModelClaudeOpus45)

	// Wait for idle timeout
	time.Sleep(60 * time.Millisecond)
	registry.UpdateStatuses()
	assert.Equal(t, AgentStatusIdle, registry.Get(agent.ID).Status, "Should be idle")

	// Wait for inactive timeout
	time.Sleep(50 * time.Millisecond)
	registry.UpdateStatuses()
	assert.Equal(t, AgentStatusInactive, registry.Get(agent.ID).Status, "Should be inactive")
}

func TestRegistry_GetActiveAgents(t *testing.T) {
	registry := NewRegistry(RegistryConfig{
		IdleTimeout:     50 * time.Millisecond,
		InactiveTimeout: 100 * time.Millisecond,
	})

	registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)
	agent2, _ := registry.Register("agent2", "session-1", "", SourceModelClaudeOpus45)

	// Make agent2 idle
	time.Sleep(60 * time.Millisecond)
	registry.Touch(agent2.ID) // Keep agent2 active
	registry.UpdateStatuses()

	active := registry.GetActiveAgents()

	assert.Len(t, active, 1, "Should have 1 active agent")
	assert.Equal(t, "agent2", active[0].Name, "Active agent should be agent2")
}

func TestRegistry_Versioning(t *testing.T) {
	registry := newTestRegistry(t)

	// Initial version
	v0 := registry.GetVersion()
	assert.Equal(t, "v0", v0, "Initial version should be v0")

	// Increment version
	v1 := registry.IncrementVersion()
	assert.Equal(t, "v1", v1, "First increment should be v1")

	v2 := registry.IncrementVersion()
	assert.Equal(t, "v2", v2, "Second increment should be v2")

	// Get current version
	current := registry.GetVersion()
	assert.Equal(t, "v2", current, "Current version should be v2")

	// Get version number
	num := registry.GetVersionNumber()
	assert.Equal(t, uint64(2), num, "Version number should be 2")
}

func TestRegistry_ParseVersion(t *testing.T) {
	registry := newTestRegistry(t)

	num, err := registry.ParseVersion("v42")

	require.NoError(t, err, "ParseVersion")
	assert.Equal(t, uint64(42), num, "Parsed version should be 42")
}

func TestRegistry_ParseVersion_Invalid(t *testing.T) {
	registry := newTestRegistry(t)

	_, err := registry.ParseVersion("invalid")

	assert.Error(t, err, "ParseVersion should fail for invalid format")
}

func TestRegistry_IsVersionCurrent(t *testing.T) {
	registry := newTestRegistry(t)

	registry.IncrementVersion() // v1
	registry.IncrementVersion() // v2

	assert.True(t, registry.IsVersionCurrent("v2"), "v2 should be current")
	assert.False(t, registry.IsVersionCurrent("v1"), "v1 should not be current")
	assert.False(t, registry.IsVersionCurrent("v3"), "v3 should not be current")
}

func TestRegistry_GetVersionDelta(t *testing.T) {
	registry := newTestRegistry(t)

	registry.IncrementVersion() // v1
	registry.IncrementVersion() // v2
	registry.IncrementVersion() // v3

	delta, err := registry.GetVersionDelta("v1")

	require.NoError(t, err, "GetVersionDelta")
	assert.Equal(t, int64(2), delta, "Delta from v1 to v3 should be 2")
}

func TestRegistry_Clock(t *testing.T) {
	registry := newTestRegistry(t)

	// Initial clock
	c0 := registry.GetGlobalClock()
	assert.Equal(t, uint64(0), c0, "Initial clock should be 0")

	// Increment clock
	c1 := registry.IncrementGlobalClock()
	assert.Equal(t, uint64(1), c1, "Clock should be 1")

	c2 := registry.IncrementGlobalClock()
	assert.Equal(t, uint64(2), c2, "Clock should be 2")
}

func TestRegistry_SyncClock(t *testing.T) {
	registry := newTestRegistry(t)

	agent, _ := registry.Register("test-agent", "session-1", "", SourceModelClaudeOpus45)

	// Sync with higher incoming clock
	newClock := registry.SyncClock(agent.ID, 10)

	assert.Greater(t, newClock, uint64(10), "Synced clock should be > incoming")
	assert.Equal(t, newClock, registry.GetGlobalClock(), "Global clock should match")
}

func TestRegistry_GetAgentHierarchy(t *testing.T) {
	registry := newTestRegistry(t)

	root, _ := registry.Register("root", "session-1", "", SourceModelClaudeOpus45)
	child, _ := registry.Register("child", "session-1", root.ID, SourceModelClaudeOpus45)
	grandchild, _ := registry.Register("grandchild", "session-1", child.ID, SourceModelClaudeOpus45)

	hierarchy := registry.GetAgentHierarchy(grandchild.ID)

	assert.Len(t, hierarchy, 3, "Hierarchy should have 3 levels")
	assert.Equal(t, "root", hierarchy[0].Name, "First should be root")
	assert.Equal(t, "child", hierarchy[1].Name, "Second should be child")
	assert.Equal(t, "grandchild", hierarchy[2].Name, "Third should be grandchild")
}

func TestRegistry_GetRootAgent(t *testing.T) {
	registry := newTestRegistry(t)

	root, _ := registry.Register("root", "session-1", "", SourceModelClaudeOpus45)
	child, _ := registry.Register("child", "session-1", root.ID, SourceModelClaudeOpus45)
	grandchild, _ := registry.Register("grandchild", "session-1", child.ID, SourceModelClaudeOpus45)

	foundRoot := registry.GetRootAgent(grandchild.ID)

	assert.Equal(t, root.ID, foundRoot.ID, "Should find root agent")
}

func TestRegistry_GetDescendants(t *testing.T) {
	registry := newTestRegistry(t)

	root, _ := registry.Register("root", "session-1", "", SourceModelClaudeOpus45)
	child1, _ := registry.Register("child1", "session-1", root.ID, SourceModelClaudeOpus45)
	child2, _ := registry.Register("child2", "session-1", root.ID, SourceModelClaudeOpus45)
	registry.Register("grandchild", "session-1", child1.ID, SourceModelClaudeOpus45)
	_ = child2

	descendants := registry.GetDescendants(root.ID)

	assert.Len(t, descendants, 3, "Root should have 3 descendants")
}

func TestRegistry_GetStats(t *testing.T) {
	registry := newTestRegistry(t)

	registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)
	registry.Register("agent2", "session-1", "", SourceModelClaudeOpus45)
	registry.Register("agent3", "session-2", "", SourceModelClaudeOpus45)
	registry.IncrementVersion()
	registry.IncrementGlobalClock()

	stats := registry.GetStats()

	assert.Equal(t, 3, stats.TotalAgents, "Total agents")
	assert.Equal(t, 3, stats.ActiveAgents, "Active agents")
	assert.Equal(t, 2, stats.SessionCount, "Session count")
	assert.Equal(t, uint64(1), stats.GlobalClock, "Global clock")
	assert.Equal(t, "v1", stats.CurrentVersion, "Current version")
	assert.Equal(t, 2, stats.AgentsBySession["session-1"], "Agents in session-1")
	assert.Equal(t, 1, stats.AgentsBySession["session-2"], "Agents in session-2")
}

// =============================================================================
// Coordination Tests
// =============================================================================

func TestCoordination_SimultaneousRegistration(t *testing.T) {
	registry := newTestRegistry(t)
	numAgents := 50

	var wg sync.WaitGroup
	var mu sync.Mutex
	agents := make([]*RegisteredAgent, 0, numAgents)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agent, err := registry.Register(
				fmt.Sprintf("agent-%d", idx),
				"session-1",
				"",
				SourceModelClaudeOpus45,
			)
			if err == nil {
				mu.Lock()
				agents = append(agents, agent)
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	assert.Len(t, agents, numAgents, "All agents should be registered")

	// Verify unique IDs
	ids := make(map[string]bool)
	for _, a := range agents {
		assert.False(t, ids[a.ID], "Duplicate ID found: %s", a.ID)
		ids[a.ID] = true
	}
}

func TestCoordination_OrphanedSubAgents(t *testing.T) {
	registry := newTestRegistry(t)

	parent, _ := registry.Register("parent", "session-1", "", SourceModelClaudeOpus45)
	child, _ := registry.Register("child", "session-1", parent.ID, SourceModelClaudeOpus45)

	// Unregister parent (which also removes children)
	registry.Unregister(parent.ID)

	// Child should be gone
	assert.Nil(t, registry.Get(child.ID), "Child should be removed when parent is unregistered")
}

func TestCoordination_CrossSessionAgentLookup(t *testing.T) {
	registry := newTestRegistry(t)

	agent1, _ := registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)
	agent2, _ := registry.Register("agent2", "session-2", "", SourceModelClaudeOpus45)

	// Should be able to find agents by ID regardless of session
	found1 := registry.Get(agent1.ID)
	found2 := registry.Get(agent2.ID)

	assert.NotNil(t, found1, "Should find agent1")
	assert.NotNil(t, found2, "Should find agent2")
	assert.Equal(t, "session-1", found1.SessionID, "Session should match")
	assert.Equal(t, "session-2", found2.SessionID, "Session should match")
}

func TestCoordination_AgentUnregisterCleanup(t *testing.T) {
	registry := newTestRegistry(t)

	agent, _ := registry.Register("test-agent", "session-1", "", SourceModelClaudeOpus45)

	registry.Unregister(agent.ID)

	// Verify all lookups return nothing
	assert.Nil(t, registry.Get(agent.ID), "Get should return nil")
	assert.Nil(t, registry.GetByName("test-agent"), "GetByName should return nil")
	assert.Empty(t, registry.GetBySession("session-1"), "GetBySession should return empty")
}

func TestCoordination_DeepAgentHierarchy(t *testing.T) {
	registry := newTestRegistry(t)
	depth := 10

	var parentID string
	var agents []*RegisteredAgent

	for i := 0; i < depth; i++ {
		agent, err := registry.Register(
			fmt.Sprintf("agent-level-%d", i),
			"session-1",
			parentID,
			SourceModelClaudeOpus45,
		)
		require.NoError(t, err, "Register at level %d", i)
		agents = append(agents, agent)
		parentID = agent.ID
	}

	// Verify hierarchy from deepest agent
	deepest := agents[len(agents)-1]
	hierarchy := registry.GetAgentHierarchy(deepest.ID)
	assert.Len(t, hierarchy, depth, "Hierarchy should have %d levels", depth)

	// Verify root from deepest
	root := registry.GetRootAgent(deepest.ID)
	assert.Equal(t, agents[0].ID, root.ID, "Root should be first agent")

	// Verify descendants from root
	descendants := registry.GetDescendants(agents[0].ID)
	assert.Len(t, descendants, depth-1, "Root should have %d descendants", depth-1)
}
