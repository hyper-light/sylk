package pipeline

import (
	"testing"

	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/providers"
)

func TestApplyLLMRuntimeProfile_DisablesParallelToolUse(t *testing.T) {
	pt := &PipelineTester{
		config: testershared.PipelineTesterConfig{
			SessionID: "sess-1",
			Model:     "claude-opus-4-6",
		},
	}
	req := &providers.Request{Model: "claude-opus-4-6", MaxTokens: 1024}
	pt.applyLLMRuntimeProfile(req, "validation")
	if !req.DisableParallelToolUse {
		t.Fatal("DisableParallelToolUse = false, want true")
	}
}
