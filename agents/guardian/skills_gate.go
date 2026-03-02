package guardian

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/core/skills"
)

// ---------------------------------------------------------------------------
// review_gate — Domain: gate, Priority: 90
// ---------------------------------------------------------------------------

type reviewGateInput struct {
	Action        string   `json:"action"`
	DiffContent   string   `json:"diff_content,omitempty"`
	Paths         []string `json:"paths,omitempty"`
}

func reviewGateSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *reviewGateInput) (any, error)
	dispatch := map[string]handler{
		"review_diff": func(_ context.Context, p *reviewGateInput) (any, error) {
			if p.DiffContent == "" {
				return nil, fmt.Errorf("diff_content is required for review_diff")
			}
			result := g.diffGate.ReviewDiff(p.DiffContent)
			return map[string]any{
				"clean":    result.Clean,
				"findings": result.Findings,
				"stats":    result.Stats,
			}, nil
		},
		"pre_commit_check": func(_ context.Context, p *reviewGateInput) (any, error) {
			if p.DiffContent == "" {
				return nil, fmt.Errorf("diff_content is required for pre_commit_check")
			}
			readFile := func(path string) (string, error) {
				if g.fileAccess == nil {
					return "", fmt.Errorf("no file access configured")
				}
				ctx := context.Background()
				data, err := g.fileAccess.ReadFile(ctx, path)
				if err != nil {
					return "", err
				}
				return string(data), nil
			}
			result := g.diffGate.PreCommitCheck(p.DiffContent, p.Paths, readFile)
			return map[string]any{
				"clean":         result.Clean,
				"findings":      result.Findings,
				"finding_count": len(result.Findings),
				"stats":         result.Stats,
			}, nil
		},
		"detect_anomaly": func(_ context.Context, _ *reviewGateInput) (any, error) {
			anomalies := g.healthMon.DetectAnomalies()
			return map[string]any{
				"anomalies":     anomalies,
				"anomaly_count": len(anomalies),
				"clean":         len(anomalies) == 0,
			}, nil
		},
	}

	return skills.NewSkill("review_gate").
		Description("Diff review and pre-commit gating: review diffs for suspicious changes, run composite pre-commit checks.\n\n"+
			"Actions:\n"+
			"- review_diff: Review a diff for suspicious patterns (params: diff_content [required])\n"+
			"- pre_commit_check: Full pre-commit review including credential scan (params: diff_content [required], paths)\n"+
			"- detect_anomaly: Detect operational anomalies across the system").
		Domain("gate").
		Keywords("review", "diff", "pre-commit", "gate", "check", "anomaly").
		Priority(90).
		TokenEstimate(500).
		EnumParam("action", "Gate action", []string{
			"review_diff", "pre_commit_check", "detect_anomaly",
		}, true).
		StringParam("diff_content", "Diff content to review (for review_diff, pre_commit_check)", false).
		ArrayParam("paths", "Staged file paths (for pre_commit_check)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params reviewGateInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown review_gate action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}
