package mcp

// Standard MCP method names. Centralized so wire / client / server / dispatcher
// use one source of truth and typos surface at compile time.
const (
	MethodInitialize  = "initialize"
	MethodInitialized = "notifications/initialized"
	MethodPing        = "ping"
	MethodShutdown    = "shutdown"

	MethodToolsList = "tools/list"
	MethodToolsCall = "tools/call"
	MethodToolsListChanged = "notifications/tools/list_changed"

	MethodResourcesList          = "resources/list"
	MethodResourcesRead          = "resources/read"
	MethodResourcesSubscribe     = "resources/subscribe"
	MethodResourcesUnsubscribe   = "resources/unsubscribe"
	MethodResourcesUpdated       = "notifications/resources/updated"
	MethodResourcesListChanged   = "notifications/resources/list_changed"

	MethodPromptsList        = "prompts/list"
	MethodPromptsGet         = "prompts/get"
	MethodPromptsListChanged = "notifications/prompts/list_changed"

	MethodLogMessage  = "notifications/message"
	MethodProgress    = "notifications/progress"
	MethodCancelled   = "notifications/cancelled"

	MethodElicitationCreate = "elicitation/create"
	MethodSamplingCreate    = "sampling/createMessage"
	MethodRootsList         = "roots/list"

	MethodCompletionsComplete = "completion/complete"
)

// IsNotification returns true when method should be sent without an id.
func IsNotification(method string) bool {
	switch method {
	case MethodInitialized,
		MethodToolsListChanged,
		MethodResourcesListChanged,
		MethodResourcesUpdated,
		MethodPromptsListChanged,
		MethodLogMessage,
		MethodProgress,
		MethodCancelled:
		return true
	}
	return false
}
