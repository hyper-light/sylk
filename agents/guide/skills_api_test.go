package guide

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSkills_UsesGuideManifestCoreSkills(t *testing.T) {
	_, loaderCfg, _ := buildSkills(Config{})
	assert.Equal(t, guideCoreSkillNames(), loaderCfg.CoreSkills)
}

func TestBuildSkills_GuideSafetyHookMatchesManifest(t *testing.T) {
	_, _, hooks := buildSkills(Config{})
	require.NotNil(t, hooks)

	for _, name := range guideAllSkillNames() {
		_, result, err := hooks.ExecutePreToolCallHooks(context.Background(), &skills.ToolCallHookData{
			ToolName: name,
		})
		require.NoErrorf(t, err, "allowed tool %q should pass guide safety hook", name)
		assert.Truef(t, result.Continue, "allowed tool %q should continue", name)
	}

	_, result, err := hooks.ExecutePreToolCallHooks(context.Background(), &skills.ToolCallHookData{
		ToolName: "write_file",
	})
	require.Error(t, err)
	assert.False(t, result.Continue)
	assert.Contains(t, err.Error(), `Guide agent is not permitted to execute tool "write_file"`)
}

func TestValidateGuideSkillConfiguration(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() {
		_ = bus.Close()
	}()

	g, err := NewWithAPIKey("", Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
	})
	require.NoError(t, err)
	require.NoError(t, validateGuideSkillConfiguration(g.skills))
}
