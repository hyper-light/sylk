package security

// AgentRole defines the permission level for an agent
type AgentRole string

const (
	RoleObserver          AgentRole = "observer"
	RoleObserverKnowledge AgentRole = "observer_knowledge"
	RoleWorker            AgentRole = "worker"
	RoleSupervisor        AgentRole = "supervisor"
	RoleOrchestrator      AgentRole = "orchestrator"
	RoleAdmin             AgentRole = "admin"
)

// DefaultAgentRoles maps agent types to their default roles
var DefaultAgentRoles = map[string]AgentRole{
	"guide":        RoleSupervisor,
	"architect":    RoleSupervisor,
	"academic":     RoleObserver,
	"librarian":    RoleObserverKnowledge,
	"archivalist":  RoleObserverKnowledge,
	"engineer":     RoleWorker,
	"designer":     RoleWorker,
	"inspector":    RoleObserver,
	"tester":       RoleObserver,
	"orchestrator": RoleOrchestrator,
}

// ActionType defines the type of permission action
type ActionType string

const (
	ActionTypeCommand    ActionType = "command"
	ActionTypeNetwork    ActionType = "network"
	ActionTypePath       ActionType = "path"
	ActionTypeConfig     ActionType = "config"
	ActionTypeCredential ActionType = "credential"
)

// roleCapabilities maps roles to their allowed action types
var roleCapabilities = map[AgentRole]map[ActionType]bool{
	RoleObserver: {
		ActionTypePath: true, // read-only paths
	},
	RoleObserverKnowledge: {
		ActionTypePath:    true,
		ActionTypeNetwork: true, // for API calls to knowledge services
	},
	RoleWorker: {
		ActionTypeCommand: true,
		ActionTypeNetwork: true,
		ActionTypePath:    true,
	},
	RoleSupervisor: {
		ActionTypeCommand: true,
		ActionTypeNetwork: true,
		ActionTypePath:    true,
		ActionTypeConfig:  true,
	},
	RoleOrchestrator: {
		ActionTypePath: true, // read-only, coordinates others
	},
	RoleAdmin: {
		ActionTypeCommand:    true,
		ActionTypeNetwork:    true,
		ActionTypePath:       true,
		ActionTypeConfig:     true,
		ActionTypeCredential: true,
	},
}

// RoleHasCapability checks if a role has a specific capability
func RoleHasCapability(role AgentRole, actionType ActionType) bool {
	caps, ok := roleCapabilities[role]
	if !ok {
		return false
	}
	return caps[actionType]
}

// GetRoleForAgent returns the default role for an agent type
func GetRoleForAgent(agentType string) AgentRole {
	if role, ok := DefaultAgentRoles[agentType]; ok {
		return role
	}
	return RoleObserver
}

// IsHigherRole returns true if role1 has higher or equal permissions than role2
func IsHigherRole(role1, role2 AgentRole) bool {
	order := map[AgentRole]int{
		RoleObserver:          0,
		RoleObserverKnowledge: 1,
		RoleWorker:            2,
		RoleSupervisor:        3,
		RoleOrchestrator:      3,
		RoleAdmin:             4,
	}
	return order[role1] >= order[role2]
}
