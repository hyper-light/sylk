package orchestrator

// PipelineStage identifies a stage within an expanded pipeline node.
type PipelineStage string

const (
	StageInspect PipelineStage = "inspect"
	StageTest    PipelineStage = "test"
	StageExecute PipelineStage = "execute"
)

func pipelineExpandable(agentType string) bool {
	return agentType == "engineer" || agentType == "designer"
}

// SubNodeID returns the sub-node ID for a given parent and stage.
func SubNodeID(parentID string, stage PipelineStage) string {
	return parentID + ":" + string(stage)
}

// ParentNodeID extracts the parent node ID from a sub-node ID.
// Returns the original ID and false if not a sub-node.
func ParentNodeID(subNodeID string) (string, bool) {
	for i := len(subNodeID) - 1; i >= 0; i-- {
		if subNodeID[i] == ':' {
			suffix := subNodeID[i+1:]
			if suffix == string(StageInspect) || suffix == string(StageTest) || suffix == string(StageExecute) {
				return subNodeID[:i], true
			}
			break
		}
	}
	return subNodeID, false
}

// StageFromSubNodeID extracts the stage from a sub-node ID.
func StageFromSubNodeID(subNodeID string) PipelineStage {
	for i := len(subNodeID) - 1; i >= 0; i-- {
		if subNodeID[i] == ':' {
			return PipelineStage(subNodeID[i+1:])
		}
	}
	return ""
}
