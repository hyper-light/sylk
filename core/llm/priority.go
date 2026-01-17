package llm

// RequestPriority determines the processing order for LLM requests.
// Higher values indicate higher priority and are processed first.
type RequestPriority int

const (
	// PriorityBackground is for non-urgent background tasks (Archivalist, Librarian)
	PriorityBackground RequestPriority = 20

	// PriorityValidation is for validation tasks (Inspector, Tester)
	PriorityValidation RequestPriority = 40

	// PriorityExecution is for execution tasks (Engineer, Designer)
	PriorityExecution RequestPriority = 60

	// PriorityPlanning is for planning tasks (Architect)
	PriorityPlanning RequestPriority = 80

	// PriorityUserInteractive is for user-invoked requests (highest priority)
	PriorityUserInteractive RequestPriority = 100
)

// String returns a human-readable name for the priority level.
func (p RequestPriority) String() string {
	switch p {
	case PriorityUserInteractive:
		return "user_interactive"
	case PriorityPlanning:
		return "planning"
	case PriorityExecution:
		return "execution"
	case PriorityValidation:
		return "validation"
	case PriorityBackground:
		return "background"
	default:
		return "custom"
	}
}

// DeterminePriority returns the appropriate priority for an LLM request.
// User-invoked requests always get UserInteractive priority.
// Otherwise, priority is determined by agent type.
func DeterminePriority(agentType string, isUserInvoked bool) RequestPriority {
	if isUserInvoked {
		return PriorityUserInteractive
	}
	return priorityForAgentType(agentType)
}

// priorityForAgentType maps agent types to their default priorities.
func priorityForAgentType(agentType string) RequestPriority {
	switch agentType {
	case "architect":
		return PriorityPlanning
	case "engineer", "designer":
		return PriorityExecution
	case "inspector", "tester":
		return PriorityValidation
	case "archivalist", "librarian":
		return PriorityBackground
	default:
		return PriorityExecution
	}
}
