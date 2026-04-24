package designer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/skills"
)

// Phase 1 refactor (docs/PIPELINE_SKILL_REFACTOR.md):
// Removed per-target peer-communication skills — they all duplicated
// the Fabric primitives:
//
//   - request_engineer_review / request_inspector_check /
//     request_tester_validation → consult_peer(target_agent_type=…).
//   - report_to_engineer → handoff_next(target="engineer",…).
//   - report_to_orchestrator → handoff_next(target="orchestrator",…)
//     (the orchestrator also consumes fabric via the amplifier).
//
// The per-target wrappers fragmented the catalog for no benefit; the
// generic peer-aware primitives already route to any target agent.

// askUserClarificationSkill creates a skill that publishes a clarification request
// to the orchestrator for surfacing to the user. The only peer-facing
// skill kept in feedback.go — genuine user-escape hatch, no Fabric
// equivalent.
func askUserClarificationSkill(d *Designer) *skills.Skill {
	type params struct {
		Question string   `json:"question"`
		Context  string   `json:"context"`
		Options  []string `json:"options,omitempty"`
	}

	return skills.NewSkill("ask_user_clarification").
		Description("Ask the user for clarification when requirements are ambiguous or multiple valid approaches exist.").
		Domain("collaboration").
		Keywords("clarify", "question", "user", "ambiguous").
		Priority(70).
		StringParam("question", "Specific question for the user", true).
		StringParam("context", "Context explaining why clarification is needed", true).
		ArrayParam("options", "Concrete options for the user to choose from", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if d.bus == nil {
				return nil, fmt.Errorf("bus not available")
			}

			req := &guide.RouteRequest{
				Input:           fmt.Sprintf("Designer needs clarification: %s", p.Question),
				SourceAgentID:   d.id,
				SourceAgentName: "designer",
				TargetAgentID:   "orchestrator",
				FireAndForget:   true,
				SessionID:       d.config.SessionID,
				Timestamp:       time.Now(),
			}

			d.designerPostClaim(ctx,
				claims.Action{AgentID: "designer", Type: claims.ActionTypeConsultation},
				designerConsultClaim(
					"Ask user clarification via orchestrator: "+truncateDesigner(p.Question, 60),
					"Clarification request routed through orchestrator to user",
					"orchestrator",
					[]claims.ClaimScopeEntry{{Kind: "consultation", Key: "orchestrator"}},
					[]*claims.Validation{
						designerValidation(claims.ValidationTypeReceipt, false, "Clarification published", "message.published"),
					},
				),
			)

			msg := guide.NewRequestMessage(d.generateMessageID(), req)
			msg.Metadata = map[string]any{
				"type":       "clarification_request",
				"question":   p.Question,
				"context":    p.Context,
				"options":    p.Options,
				"from_agent": d.id,
			}

			if err := d.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
				return nil, fmt.Errorf("publish clarification: %w", err)
			}

			return map[string]any{
				"asked":    true,
				"question": p.Question,
			}, nil
		}).
		Build()
}
