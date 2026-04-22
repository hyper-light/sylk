package shared

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adalundhe/sylk/core/versioning"
)

type stubMergeLog struct {
	merges []versioning.MergeDescriptor
}

func (s *stubMergeLog) MergesAfter(ver versioning.SemanticVersion) []versioning.MergeDescriptor {
	var out []versioning.MergeDescriptor
	for _, d := range s.merges {
		if d.MergedVersion.Compare(ver) > 0 {
			out = append(out, d)
		}
	}
	return out
}

func TestMergesAfterSkill_ReturnsMergesStrictlyAfterBase(t *testing.T) {
	reader := &stubMergeLog{
		merges: []versioning.MergeDescriptor{
			{MergedVersion: versioning.SemanticVersion{Minor: 1}, PipelineID: "p1", Paths: []string{"a.go"}, PathCount: 1},
			{MergedVersion: versioning.SemanticVersion{Minor: 2}, PipelineID: "p2", Paths: []string{"b.go"}, PathCount: 1},
			{MergedVersion: versioning.SemanticVersion{Minor: 3}, PipelineID: "p3", Paths: []string{"c.go"}, PathCount: 1},
		},
	}
	skill := NewMergesAfterSkill(MergesAfterSkillConfig{
		LogReader: func() MergeLogReader { return reader },
	})

	input := json.RawMessage(`{"since_version": "0.1"}`)
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	m := result.(map[string]any)
	if m["count"].(int) != 2 {
		t.Fatalf("count = %v, want 2", m["count"])
	}
	merges := m["merges"].([]map[string]any)
	if len(merges) != 2 {
		t.Fatalf("merges len = %d, want 2", len(merges))
	}
	if merges[0]["merged_version"] != "0.2" || merges[1]["merged_version"] != "0.3" {
		t.Fatalf("versions = %v, %v, want 0.2, 0.3", merges[0]["merged_version"], merges[1]["merged_version"])
	}
}

func TestMergesAfterSkill_RequiresSinceVersion(t *testing.T) {
	reader := &stubMergeLog{}
	skill := NewMergesAfterSkill(MergesAfterSkillConfig{
		LogReader: func() MergeLogReader { return reader },
	})
	input := json.RawMessage(`{}`)
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for missing since_version")
	}
}

func TestMergesAfterSkill_RejectsMalformedVersion(t *testing.T) {
	reader := &stubMergeLog{}
	skill := NewMergesAfterSkill(MergesAfterSkillConfig{
		LogReader: func() MergeLogReader { return reader },
	})
	input := json.RawMessage(`{"since_version": "not-a-version"}`)
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for malformed version")
	}
}

func TestMergesAfterSkill_EmptyWhenNoReader(t *testing.T) {
	skill := NewMergesAfterSkill(MergesAfterSkillConfig{
		LogReader: func() MergeLogReader { return nil },
	})
	input := json.RawMessage(`{"since_version": "1.0"}`)
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	m := result.(map[string]any)
	if m["count"].(int) != 0 {
		t.Fatalf("count = %v, want 0", m["count"])
	}
}
