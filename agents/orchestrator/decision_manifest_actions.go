package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/manifest"
)

// handleDecisionManifestAction dispatches manifest_declare / manifest_query
// bus actions to the orchestrator's DecisionManifestStore. Mirrors the
// shape of handleCoordinationAction. Returns (handled=true, err) when the
// action is a manifest action; (false, nil) otherwise so the caller can
// fall through to other action handlers.
func (o *Orchestrator) handleDecisionManifestAction(ctx context.Context, req *guide.ActionRequest) (bool, error) {
	if req == nil || o.decisionManifest == nil {
		return false, nil
	}

	author := manifest.AgentRef{
		AgentID:   req.SourceAgentID,
		AgentType: req.SourceAgentName,
	}
	if author.AgentType == "" {
		author.AgentType = req.SourceAgentID
	}

	var (
		result any
		err    error
	)

	switch req.Action {
	case manifest.ActionDeclareDecision:
		var input manifest.DeclareDecisionInput
		err = decodeManifestData(req.Data, &input)
		if err == nil {
			result, err = o.decisionManifest.Declare(ctx, o.config.SessionID, author, input)
		}
	case manifest.ActionQueryDecisions:
		var input manifest.QueryDecisionsInput
		err = decodeManifestData(req.Data, &input)
		if err == nil {
			result, err = o.decisionManifest.Query(ctx, o.config.SessionID, input)
		}
	default:
		return false, nil
	}

	if req.FireAndForget {
		return true, err
	}
	if err != nil {
		return true, o.publishCoordinationFailure(req, err)
	}
	return true, o.publishCoordinationSuccess(req, result)
}

func decodeManifestData(data any, dst any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode manifest payload: %w", err)
	}
	if err := json.Unmarshal(encoded, dst); err != nil {
		return fmt.Errorf("decode manifest payload: %w", err)
	}
	return nil
}
