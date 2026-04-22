package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

// mergesAfterInput is the JSON-decoded input payload.
type mergesAfterInput struct {
	SinceVersion string `json:"since_version"`
}

// MergeLogReader is the minimal interface the merges_after skill
// needs from the session VFS. SessionVFS satisfies this via its
// MergesAfter method; tests can substitute an in-memory stub.
type MergeLogReader interface {
	MergesAfter(ver versioning.SemanticVersion) []versioning.MergeDescriptor
}

// MergesAfterSkillConfig configures the shared merges_after skill.
// The LogReader lookup is invoked per tool call so the skill sees
// live state even when the session VFS is swapped or attached
// post-construction (e.g. after orchestrator bootstrap).
type MergesAfterSkillConfig struct {
	LogReader func() MergeLogReader
}

// NewMergesAfterSkill returns a skill that lets a per-merge audit
// replica query for merges that landed after its audit-base Copy.
// Per docs/PARALLEL_GLOBAL_VFS.md §3.8, this is the replica's tool
// for deciding whether to continue its audit on the current context,
// rebase to the latest, or emit rejection-with-hint referencing the
// later work. Autonomy is preserved — the replica decides.
//
// Input: {"since_version": "<major>.<minor>"}.
// Output: {"merges": [{"merged_version": "...", "pipeline_id": "...",
// "paths": [...], "path_count": N}, ...], "count": N}.
//
// An empty since_version is rejected — the caller must be explicit
// about its audit-base. This prevents accidental full-log fetches
// that would swamp the LLM's context.
func NewMergesAfterSkill(cfg MergesAfterSkillConfig) *skills.Skill {
	return skills.NewSkill("merges_after").
		Description("List merges that landed in the session's commit queue after a given base version. Use during audit to detect new merges arriving in parallel so you can rebase or reference them.").
		StringParam("since_version", "The audit-base version (format: \"major.minor\", e.g. \"3.7\"). Returns merges with MergedVersion > this.", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			if cfg.LogReader == nil {
				return nil, fmt.Errorf("merges_after: no log reader configured")
			}
			reader := cfg.LogReader()
			if reader == nil {
				return map[string]any{"merges": []any{}, "count": 0}, nil
			}
			var params mergesAfterInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("merges_after: invalid input: %w", err)
			}
			since, err := parseSemanticVersion(params.SinceVersion)
			if err != nil {
				return nil, err
			}
			descs := reader.MergesAfter(since)
			out := make([]map[string]any, 0, len(descs))
			for _, d := range descs {
				out = append(out, map[string]any{
					"merged_version": fmt.Sprintf("%d.%d", d.MergedVersion.Major, d.MergedVersion.Minor),
					"base_version":   fmt.Sprintf("%d.%d", d.BaseVersion.Major, d.BaseVersion.Minor),
					"pipeline_id":    d.PipelineID,
					"paths":          d.Paths,
					"path_count":     d.PathCount,
				})
			}
			return map[string]any{
				"merges": out,
				"count":  len(out),
			}, nil
		}).
		Build()
}

// parseSemanticVersion accepts "major.minor" and returns the
// corresponding SemanticVersion. Rejects empty strings and
// malformed inputs explicitly so callers can't accidentally use the
// zero value as "all merges."
func parseSemanticVersion(s string) (versioning.SemanticVersion, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return versioning.SemanticVersion{}, fmt.Errorf("merges_after: since_version required (e.g. \"3.7\")")
	}
	var major, minor uint32
	n, err := fmt.Sscanf(s, "%d.%d", &major, &minor)
	if err != nil || n != 2 {
		return versioning.SemanticVersion{}, fmt.Errorf("merges_after: invalid version %q (expected major.minor)", s)
	}
	return versioning.SemanticVersion{Major: major, Minor: minor}, nil
}
