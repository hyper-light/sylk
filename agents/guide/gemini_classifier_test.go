package guide

import "testing"

func TestDecodeClassificationResult_JSONCandidates(t *testing.T) {
	raw := `noise prefix {"intent":"help","domain":"general","target_agent":"guide","confidence":0.9} noise suffix`
	result, err := decodeClassificationResult(raw)
	if err != nil {
		t.Fatalf("decodeClassificationResult failed: %v", err)
	}
	if result.Intent != "help" {
		t.Fatalf("intent = %q", result.Intent)
	}
	if result.TargetAgent != "guide" {
		t.Fatalf("target_agent = %q", result.TargetAgent)
	}
}

func TestDecodeClassificationResult_FencedJSON(t *testing.T) {
	raw := "```json\n{\"intent\":\"status\",\"domain\":\"system\",\"target_agent\":\"guide\",\"confidence\":0.8}\n```"
	result, err := decodeClassificationResult(raw)
	if err != nil {
		t.Fatalf("decodeClassificationResult failed: %v", err)
	}
	if result.Intent != "status" {
		t.Fatalf("intent = %q", result.Intent)
	}
}
