package archivalist

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTokenSavings_Report(t *testing.T) {
	archivalist, err := New(Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	report := archivalist.GetTokenSavings(archivalist.defaultSessionID)
	assert.Equal(t, archivalist.defaultSessionID, report.SessionID)
}
