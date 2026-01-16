package archivalist

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArchivalist_SkillsLoaded(t *testing.T) {
	archivalist, err := New(Config{})
	require.NoError(t, err)

	defs := archivalist.GetLoadedSkillDefinitions()
	assert.NotEmpty(t, defs)
}

func TestArchivalist_ExtendedSkillsRegistered(t *testing.T) {
	archivalist, err := New(Config{})
	require.NoError(t, err)

	loaded := archivalist.skills.GetToolDefinitions()
	assert.NotEmpty(t, loaded)
}
