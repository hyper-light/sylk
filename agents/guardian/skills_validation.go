package guardian

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/skills"
)

// ---------------------------------------------------------------------------
// content_scan — Domain: validation, Priority: 95
// ---------------------------------------------------------------------------

type contentScanInput struct {
	Action  string   `json:"action"`
	Content string   `json:"content,omitempty"`
	Paths   []string `json:"paths,omitempty"`
}

func contentScanSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *contentScanInput) (any, error)
	dispatch := map[string]handler{
		"scan_output": func(ctx context.Context, p *contentScanInput) (any, error) {
			if p.Content == "" {
				return nil, fmt.Errorf("content is required for scan_output")
			}
			findings := g.contentValidator.ScanContent(p.Content)
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventContentScanned,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.DiffPayload{Verdict: fmt.Sprintf("%d findings", len(findings))})
				for _, f := range findings {
					if f.Type == FindingCredentialLeak {
						shared.LogAgentEvent(lm.EventLogger, agentlog.EventCredentialDetected,
							lm.AgentID, lm.SessionID, lm.CorrID, "warn",
							&agentlog.DiffPayload{Reason: f.Title})
					}
				}
			}
			return map[string]any{
				"findings":      findings,
				"finding_count": len(findings),
				"clean":         len(findings) == 0,
			}, nil
		},
		"scan_staged": func(ctx context.Context, p *contentScanInput) (any, error) {
			if len(p.Paths) == 0 {
				return nil, fmt.Errorf("paths required for scan_staged")
			}
			readFile := func(path string) (string, error) {
				if g.fileAccess == nil {
					return "", fmt.Errorf("no file access configured")
				}
				bgCtx := context.Background()
				data, err := g.fileAccess.ReadFile(bgCtx, path)
				if err != nil {
					return "", err
				}
				return string(data), nil
			}
			findings := g.contentValidator.ScanPaths(p.Paths, readFile)
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventContentScanned,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.DiffPayload{Verdict: fmt.Sprintf("%d paths, %d findings", len(p.Paths), len(findings))})
				for _, f := range findings {
					if f.Type == FindingCredentialLeak {
						shared.LogAgentEvent(lm.EventLogger, agentlog.EventCredentialDetected,
							lm.AgentID, lm.SessionID, lm.CorrID, "warn",
							&agentlog.DiffPayload{Reason: f.Title})
					}
				}
			}
			return map[string]any{
				"findings":      findings,
				"finding_count": len(findings),
				"paths_scanned": len(p.Paths),
				"clean":         len(findings) == 0,
			}, nil
		},
		"detect_injection": func(ctx context.Context, p *contentScanInput) (any, error) {
			if p.Content == "" {
				return nil, fmt.Errorf("content is required for detect_injection")
			}
			findings := g.contentValidator.injectionScanner.Scan(p.Content)
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				for _, f := range findings {
					shared.LogAgentEvent(lm.EventLogger, agentlog.EventInjectionDetected,
						lm.AgentID, lm.SessionID, lm.CorrID, "warn",
						&agentlog.DiffPayload{Reason: f.Title})
				}
			}
			return map[string]any{
				"findings":      findings,
				"finding_count": len(findings),
				"clean":         len(findings) == 0,
			}, nil
		},
	}

	return skills.NewSkill("content_scan").
		Description("Content validation: scan for credentials, injections, and schema violations.\n\n"+
			"Actions:\n"+
			"- scan_output: Scan text content for credentials and injections (params: content [required])\n"+
			"- scan_staged: Scan staged files for credentials (params: paths [required])\n"+
			"- detect_injection: Detect prompt injection patterns (params: content [required])").
		Domain("validation").
		Keywords("scan", "credential", "injection", "secret", "leak", "validate").
		Priority(95).
		TokenEstimate(400).
		EnumParam("action", "Scan action", []string{
			"scan_output", "scan_staged", "detect_injection",
		}, true).
		StringParam("content", "Text content to scan (for scan_output, detect_injection)", false).
		ArrayParam("paths", "File paths to scan (for scan_staged)", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params contentScanInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown content_scan action: %q", params.Action)
			}

			// Post claim: content validation request.
			sessionID := g.activeSessionID
			g.guardianPostClaim(ctx,
				guardianClaimAction(claims.ActionTypeTask),
				guardianClaim(
					"Validate content: "+params.Action,
					"Content scan for credentials and injection patterns",
					"guardian",
					[]claims.ClaimScopeEntry{{Kind: "content", Key: params.Action}},
					claims.ActionTypeTask,
					[]*claims.Validation{
						guardianValidation(claims.ValidationTypeInspection, true, "No secrets, API keys, or tokens detected", "SecretSanitizer returns zero credential findings"),
						guardianValidation(claims.ValidationTypeInspection, true, "No prompt injection or code injection patterns", "InjectionScanner returns zero findings"),
					},
				),
			)

			result, err := fn(ctx, &params)

			// Submit testament with scan results.
			if err != nil {
				g.guardianSubmitTestament(ctx, guardianTestamentAction(), guardianTestament(
					sessionID, "Content scan failed: "+err.Error(), "committed", "",
					[]*claims.Artifact{guardianArtifact(sessionID, "error", err.Error())},
				))
			} else {
				g.guardianSubmitTestament(ctx, guardianTestamentAction(), guardianTestament(
					sessionID, fmt.Sprintf("Content scan %s complete", params.Action), "committed", "",
					[]*claims.Artifact{guardianJSONArtifact(sessionID, "scan_results", result)},
				))
			}
			return result, err
		}).
		Build()
}
