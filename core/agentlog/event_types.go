package agentlog

// EventType identifies the kind of WAL event. uint16 gives 65536 slots,
// partitioned into 256-slot ranges per agent.
type EventType uint16

// Common events (0x0000–0x00FF) — written by every agent to events.wal.
const (
	EventActivated          EventType = 0x0001
	EventReady              EventType = 0x0002
	EventStopping           EventType = 0x0003
	EventStopped            EventType = 0x0004
	EventError              EventType = 0x0005
	EventBusRequestReceived EventType = 0x0010
	EventBusRequestSent     EventType = 0x0011
	EventBusResponseReceived EventType = 0x0012
	EventBusResponseSent    EventType = 0x0013
	EventBusErrorReceived   EventType = 0x0014
	EventBusErrorSent       EventType = 0x0015
	EventBusAckSent         EventType = 0x0016
	EventRegistryEvent      EventType = 0x0017
	EventAuthChanged        EventType = 0x0018
	EventLLMRequestSent     EventType = 0x0020
	EventLLMResponseReceived EventType = 0x0021
	EventLLMError           EventType = 0x0022
	EventSkillInvoked       EventType = 0x0030
	EventSkillCompleted     EventType = 0x0031
	EventSkillFailed        EventType = 0x0032
)

// Guide events (0x0100–0x01FF) — routing.wal.
const (
	EventRouteClassified   EventType = 0x0100
	EventRouteCacheHit     EventType = 0x0101
	EventRouteLearned      EventType = 0x0102
	EventRerouted          EventType = 0x0103
	EventAgentRegistered   EventType = 0x0104
	EventAgentUnregistered EventType = 0x0105
	EventForwardDispatched EventType = 0x0106
	EventResponseCorrelated EventType = 0x0107
	EventStreamStarted     EventType = 0x0108
	EventStreamCompleted   EventType = 0x0109
)

// Orchestrator events (0x0200–0x02FF) — dag.wal, pipeline.wal.
const (
	EventDAGStarted       EventType = 0x0200
	EventDAGCompleted     EventType = 0x0201
	EventDAGAborted       EventType = 0x0202
	EventDAGCancelled     EventType = 0x0203
	EventDAGModified      EventType = 0x0204
	EventNodeDispatched   EventType = 0x0205
	EventNodeCompleted    EventType = 0x0206
	EventNodeFailed       EventType = 0x0207
	EventLayerStarted     EventType = 0x0208
	EventLayerCompleted   EventType = 0x0209
	EventLayerDecisionReq EventType = 0x020A
	EventLayerDecisionRecv EventType = 0x020B
	EventPipelineUpdate      EventType = 0x0220
	EventPipelineStateChange EventType = 0x0221
	EventPipelineQuerySent   EventType = 0x0222
	EventPipelineQueryRecv   EventType = 0x0223
	EventTaskDispatched      EventType = 0x0224
	EventTaskCompleted       EventType = 0x0225
	EventTaskFailed          EventType = 0x0226
	EventWorkflowStatus      EventType = 0x0227
)

// Architect events (0x0300–0x03FF) — planning.wal.
const (
	EventPlanCreated       EventType = 0x0300
	EventPlanLeased        EventType = 0x0301
	EventPlanReleased      EventType = 0x0302
	EventPlanReaped        EventType = 0x0303
	EventPlanStepExecuted  EventType = 0x0304
	EventConsultationSent  EventType = 0x0305
	EventConsultationRecv  EventType = 0x0306
	EventCrossDomainQuery  EventType = 0x0307
	EventCrossDomainResult EventType = 0x0308
	EventProtocolStarted   EventType = 0x0309
	EventProtocolCompleted EventType = 0x030A
)

// Engineer events (0x0400–0x04FF) — tooluse.wal.
const (
	EventToolReadFile          EventType = 0x0400
	EventToolWriteFile         EventType = 0x0401
	EventToolGlob              EventType = 0x0402
	EventToolGrep              EventType = 0x0403
	EventToolExec              EventType = 0x0404
	EventToolEditFile          EventType = 0x0405
	EventDiscoveryStarted      EventType = 0x0406
	EventDiscoveryCompleted    EventType = 0x0407
	EventGenerationStarted     EventType = 0x0408
	EventGenerationCompleted   EventType = 0x0409
	EventToolAskUser           EventType = 0x040A
)

// Designer events (0x0500–0x05FF) — design.wal.
const (
	EventPromptComposed    EventType = 0x0500
	EventDesignGenerated   EventType = 0x0501
	EventDesignIteration   EventType = 0x0502
	EventComponentSelected EventType = 0x0503
	EventLayoutResolved    EventType = 0x0504
)

// Guardian events (0x0600–0x06FF) — gate.wal, scan.wal.
const (
	EventDiffReviewed      EventType = 0x0600
	EventDiffApproved      EventType = 0x0601
	EventDiffRejected      EventType = 0x0602
	EventApprovalRequested EventType = 0x0603
	EventApprovalReceived  EventType = 0x0604
	EventCheckpointCreated EventType = 0x0605
	EventRollbackExecuted  EventType = 0x0606
	EventContentScanned    EventType = 0x0620
	EventInjectionDetected EventType = 0x0621
	EventCredentialDetected EventType = 0x0622
	EventContentCleared    EventType = 0x0623
	EventHealthChecked     EventType = 0x0624
	EventHealthAlert       EventType = 0x0625
)

// Inspector events (0x0700–0x07FF) — audit.wal.
const (
	EventAuditStarted         EventType = 0x0700
	EventAuditFinding         EventType = 0x0701
	EventAuditCompleted       EventType = 0x0702
	EventValidationStarted    EventType = 0x0703
	EventValidationResult     EventType = 0x0704
	EventValidationCorrection EventType = 0x0705
)

// Tester events (0x0800–0x08FF) — testrun.wal.
const (
	EventTestPlanCreated    EventType = 0x0800
	EventSuiteStarted      EventType = 0x0801
	EventCaseStarted       EventType = 0x0802
	EventCasePassed        EventType = 0x0803
	EventCaseFailed        EventType = 0x0804
	EventSuiteCompleted    EventType = 0x0805
	EventCoverageComputed  EventType = 0x0806
	EventMutationStarted   EventType = 0x0807
	EventMutationCompleted EventType = 0x0808
	EventFlakyDetected     EventType = 0x0809
)

// Librarian events (0x0900–0x09FF) — search.wal.
const (
	EventSearchQuery     EventType = 0x0900
	EventSearchResult    EventType = 0x0901
	EventIndexStarted    EventType = 0x0902
	EventIndexCompleted  EventType = 0x0903
	EventDocRetrieved    EventType = 0x0904
	EventDocIngested     EventType = 0x0905
)

// Academic events (0x0A00–0x0AFF) — research.wal.
const (
	EventResearchQuery      EventType = 0x0A00
	EventResearchResult     EventType = 0x0A01
	EventCitationFound      EventType = 0x0A02
	EventSynthesisCompleted EventType = 0x0A03
	EventSourceEvaluated    EventType = 0x0A04
)

// Archivalist events (0x0B00–0x0BFF) — archive.wal.
const (
	EventFactExtracted     EventType = 0x0B00
	EventContextPreserved  EventType = 0x0B01
	EventMemoryStored      EventType = 0x0B02
	EventMemoryDecayed     EventType = 0x0B03
	EventKnowledgeArchived EventType = 0x0B04
	EventEventCaptured     EventType = 0x0B05
)

// Steering events (0x0C00–0x0CFF) — steering.wal.
// Cross-cutting: any agent can write these during steered execution.
const (
	EventSteeringBegin       EventType = 0x0C00
	EventSteeringCheckpoint  EventType = 0x0C01
	EventSteeringCommand     EventType = 0x0C02
	EventSteeringComplete    EventType = 0x0C03
	EventSteeringInterrupted EventType = 0x0C04
)

// eventNames maps every defined EventType to its human-readable name.
// Indexed by uint16 value for O(1) lookup.
var eventNames [1 << 16]string

func init() {
	// Common
	eventNames[EventActivated] = "Activated"
	eventNames[EventReady] = "Ready"
	eventNames[EventStopping] = "Stopping"
	eventNames[EventStopped] = "Stopped"
	eventNames[EventError] = "Error"
	eventNames[EventBusRequestReceived] = "BusRequestReceived"
	eventNames[EventBusRequestSent] = "BusRequestSent"
	eventNames[EventBusResponseReceived] = "BusResponseReceived"
	eventNames[EventBusResponseSent] = "BusResponseSent"
	eventNames[EventBusErrorReceived] = "BusErrorReceived"
	eventNames[EventBusErrorSent] = "BusErrorSent"
	eventNames[EventBusAckSent] = "BusAckSent"
	eventNames[EventRegistryEvent] = "RegistryEvent"
	eventNames[EventAuthChanged] = "AuthChanged"
	eventNames[EventLLMRequestSent] = "LLMRequestSent"
	eventNames[EventLLMResponseReceived] = "LLMResponseReceived"
	eventNames[EventLLMError] = "LLMError"
	eventNames[EventSkillInvoked] = "SkillInvoked"
	eventNames[EventSkillCompleted] = "SkillCompleted"
	eventNames[EventSkillFailed] = "SkillFailed"

	// Guide
	eventNames[EventRouteClassified] = "RouteClassified"
	eventNames[EventRouteCacheHit] = "RouteCacheHit"
	eventNames[EventRouteLearned] = "RouteLearned"
	eventNames[EventRerouted] = "Rerouted"
	eventNames[EventAgentRegistered] = "AgentRegistered"
	eventNames[EventAgentUnregistered] = "AgentUnregistered"
	eventNames[EventForwardDispatched] = "ForwardDispatched"
	eventNames[EventResponseCorrelated] = "ResponseCorrelated"
	eventNames[EventStreamStarted] = "StreamStarted"
	eventNames[EventStreamCompleted] = "StreamCompleted"

	// Orchestrator — DAG
	eventNames[EventDAGStarted] = "DAGStarted"
	eventNames[EventDAGCompleted] = "DAGCompleted"
	eventNames[EventDAGAborted] = "DAGAborted"
	eventNames[EventDAGCancelled] = "DAGCancelled"
	eventNames[EventDAGModified] = "DAGModified"
	eventNames[EventNodeDispatched] = "NodeDispatched"
	eventNames[EventNodeCompleted] = "NodeCompleted"
	eventNames[EventNodeFailed] = "NodeFailed"
	eventNames[EventLayerStarted] = "LayerStarted"
	eventNames[EventLayerCompleted] = "LayerCompleted"
	eventNames[EventLayerDecisionReq] = "LayerDecisionReq"
	eventNames[EventLayerDecisionRecv] = "LayerDecisionRecv"

	// Orchestrator — Pipeline
	eventNames[EventPipelineUpdate] = "PipelineUpdate"
	eventNames[EventPipelineStateChange] = "PipelineStateChange"
	eventNames[EventPipelineQuerySent] = "PipelineQuerySent"
	eventNames[EventPipelineQueryRecv] = "PipelineQueryRecv"
	eventNames[EventTaskDispatched] = "TaskDispatched"
	eventNames[EventTaskCompleted] = "TaskCompleted"
	eventNames[EventTaskFailed] = "TaskFailed"
	eventNames[EventWorkflowStatus] = "WorkflowStatus"

	// Architect
	eventNames[EventPlanCreated] = "PlanCreated"
	eventNames[EventPlanLeased] = "PlanLeased"
	eventNames[EventPlanReleased] = "PlanReleased"
	eventNames[EventPlanReaped] = "PlanReaped"
	eventNames[EventPlanStepExecuted] = "PlanStepExecuted"
	eventNames[EventConsultationSent] = "ConsultationSent"
	eventNames[EventConsultationRecv] = "ConsultationRecv"
	eventNames[EventCrossDomainQuery] = "CrossDomainQuery"
	eventNames[EventCrossDomainResult] = "CrossDomainResult"
	eventNames[EventProtocolStarted] = "ProtocolStarted"
	eventNames[EventProtocolCompleted] = "ProtocolCompleted"

	// Engineer
	eventNames[EventToolReadFile] = "ToolReadFile"
	eventNames[EventToolWriteFile] = "ToolWriteFile"
	eventNames[EventToolGlob] = "ToolGlob"
	eventNames[EventToolGrep] = "ToolGrep"
	eventNames[EventToolExec] = "ToolExec"
	eventNames[EventToolEditFile] = "ToolEditFile"
	eventNames[EventDiscoveryStarted] = "DiscoveryStarted"
	eventNames[EventDiscoveryCompleted] = "DiscoveryCompleted"
	eventNames[EventGenerationStarted] = "GenerationStarted"
	eventNames[EventGenerationCompleted] = "GenerationCompleted"
	eventNames[EventToolAskUser] = "ToolAskUser"

	// Designer
	eventNames[EventPromptComposed] = "PromptComposed"
	eventNames[EventDesignGenerated] = "DesignGenerated"
	eventNames[EventDesignIteration] = "DesignIteration"
	eventNames[EventComponentSelected] = "ComponentSelected"
	eventNames[EventLayoutResolved] = "LayoutResolved"

	// Guardian
	eventNames[EventDiffReviewed] = "DiffReviewed"
	eventNames[EventDiffApproved] = "DiffApproved"
	eventNames[EventDiffRejected] = "DiffRejected"
	eventNames[EventApprovalRequested] = "ApprovalRequested"
	eventNames[EventApprovalReceived] = "ApprovalReceived"
	eventNames[EventCheckpointCreated] = "CheckpointCreated"
	eventNames[EventRollbackExecuted] = "RollbackExecuted"
	eventNames[EventContentScanned] = "ContentScanned"
	eventNames[EventInjectionDetected] = "InjectionDetected"
	eventNames[EventCredentialDetected] = "CredentialDetected"
	eventNames[EventContentCleared] = "ContentCleared"
	eventNames[EventHealthChecked] = "HealthChecked"
	eventNames[EventHealthAlert] = "HealthAlert"

	// Inspector
	eventNames[EventAuditStarted] = "AuditStarted"
	eventNames[EventAuditFinding] = "AuditFinding"
	eventNames[EventAuditCompleted] = "AuditCompleted"
	eventNames[EventValidationStarted] = "ValidationStarted"
	eventNames[EventValidationResult] = "ValidationResult"
	eventNames[EventValidationCorrection] = "ValidationCorrection"

	// Tester
	eventNames[EventTestPlanCreated] = "TestPlanCreated"
	eventNames[EventSuiteStarted] = "SuiteStarted"
	eventNames[EventCaseStarted] = "CaseStarted"
	eventNames[EventCasePassed] = "CasePassed"
	eventNames[EventCaseFailed] = "CaseFailed"
	eventNames[EventSuiteCompleted] = "SuiteCompleted"
	eventNames[EventCoverageComputed] = "CoverageComputed"
	eventNames[EventMutationStarted] = "MutationStarted"
	eventNames[EventMutationCompleted] = "MutationCompleted"
	eventNames[EventFlakyDetected] = "FlakyDetected"

	// Librarian
	eventNames[EventSearchQuery] = "SearchQuery"
	eventNames[EventSearchResult] = "SearchResult"
	eventNames[EventIndexStarted] = "IndexStarted"
	eventNames[EventIndexCompleted] = "IndexCompleted"
	eventNames[EventDocRetrieved] = "DocRetrieved"
	eventNames[EventDocIngested] = "DocIngested"

	// Academic
	eventNames[EventResearchQuery] = "ResearchQuery"
	eventNames[EventResearchResult] = "ResearchResult"
	eventNames[EventCitationFound] = "CitationFound"
	eventNames[EventSynthesisCompleted] = "SynthesisCompleted"
	eventNames[EventSourceEvaluated] = "SourceEvaluated"

	// Archivalist
	eventNames[EventFactExtracted] = "FactExtracted"
	eventNames[EventContextPreserved] = "ContextPreserved"
	eventNames[EventMemoryStored] = "MemoryStored"
	eventNames[EventMemoryDecayed] = "MemoryDecayed"
	eventNames[EventKnowledgeArchived] = "KnowledgeArchived"
	eventNames[EventEventCaptured] = "EventCaptured"

	// Steering
	eventNames[EventSteeringBegin] = "SteeringBegin"
	eventNames[EventSteeringCheckpoint] = "SteeringCheckpoint"
	eventNames[EventSteeringCommand] = "SteeringCommand"
	eventNames[EventSteeringComplete] = "SteeringComplete"
	eventNames[EventSteeringInterrupted] = "SteeringInterrupted"
}

// String returns the human-readable name for an EventType,
// or "Unknown(0xNNNN)" for unregistered codes.
func (e EventType) String() string {
	if name := eventNames[e]; name != "" {
		return name
	}
	return "Unknown"
}

// Range boundary constants for ownership checks.
const (
	rangeCommonLo      EventType = 0x0000
	rangeCommonHi      EventType = 0x00FF
	rangeGuideLo       EventType = 0x0100
	rangeGuideHi       EventType = 0x01FF
	rangeOrchestratorLo EventType = 0x0200
	rangeOrchestratorHi EventType = 0x02FF
	rangeArchitectLo   EventType = 0x0300
	rangeArchitectHi   EventType = 0x03FF
	rangeEngineerLo    EventType = 0x0400
	rangeEngineerHi    EventType = 0x04FF
	rangeDesignerLo    EventType = 0x0500
	rangeDesignerHi    EventType = 0x05FF
	rangeGuardianLo    EventType = 0x0600
	rangeGuardianHi    EventType = 0x06FF
	rangeInspectorLo   EventType = 0x0700
	rangeInspectorHi   EventType = 0x07FF
	rangeTesterLo      EventType = 0x0800
	rangeTesterHi      EventType = 0x08FF
	rangeLibrarianLo   EventType = 0x0900
	rangeLibrarianHi   EventType = 0x09FF
	rangeAcademicLo    EventType = 0x0A00
	rangeAcademicHi    EventType = 0x0AFF
	rangeArchivalistLo EventType = 0x0B00
	rangeArchivalistHi EventType = 0x0BFF
	rangeSteeringLo    EventType = 0x0C00
	rangeSteeringHi    EventType = 0x0CFF
)

// IsCommon returns true if the event type is in the common range.
func (e EventType) IsCommon() bool { return e >= rangeCommonLo && e <= rangeCommonHi }

// IsGuide returns true if the event type belongs to the Guide agent.
func (e EventType) IsGuide() bool { return e >= rangeGuideLo && e <= rangeGuideHi }

// IsOrchestrator returns true if the event type belongs to the Orchestrator agent.
func (e EventType) IsOrchestrator() bool { return e >= rangeOrchestratorLo && e <= rangeOrchestratorHi }

// IsArchitect returns true if the event type belongs to the Architect agent.
func (e EventType) IsArchitect() bool { return e >= rangeArchitectLo && e <= rangeArchitectHi }

// IsEngineer returns true if the event type belongs to the Engineer agent.
func (e EventType) IsEngineer() bool { return e >= rangeEngineerLo && e <= rangeEngineerHi }

// IsDesigner returns true if the event type belongs to the Designer agent.
func (e EventType) IsDesigner() bool { return e >= rangeDesignerLo && e <= rangeDesignerHi }

// IsGuardian returns true if the event type belongs to the Guardian agent.
func (e EventType) IsGuardian() bool { return e >= rangeGuardianLo && e <= rangeGuardianHi }

// IsInspector returns true if the event type belongs to the Inspector agent.
func (e EventType) IsInspector() bool { return e >= rangeInspectorLo && e <= rangeInspectorHi }

// IsTester returns true if the event type belongs to the Tester agent.
func (e EventType) IsTester() bool { return e >= rangeTesterLo && e <= rangeTesterHi }

// IsLibrarian returns true if the event type belongs to the Librarian agent.
func (e EventType) IsLibrarian() bool { return e >= rangeLibrarianLo && e <= rangeLibrarianHi }

// IsAcademic returns true if the event type belongs to the Academic agent.
func (e EventType) IsAcademic() bool { return e >= rangeAcademicLo && e <= rangeAcademicHi }

// IsArchivalist returns true if the event type belongs to the Archivalist agent.
func (e EventType) IsArchivalist() bool { return e >= rangeArchivalistLo && e <= rangeArchivalistHi }

// IsSteering returns true if the event type is a cross-cutting steering event.
func (e EventType) IsSteering() bool { return e >= rangeSteeringLo && e <= rangeSteeringHi }

// OwnerAgent returns the agent name that owns this event type range,
// or "unknown" for unassigned ranges.
func (e EventType) OwnerAgent() string {
	switch {
	case e.IsCommon():
		return "common"
	case e.IsGuide():
		return "guide"
	case e.IsOrchestrator():
		return "orchestrator"
	case e.IsArchitect():
		return "architect"
	case e.IsEngineer():
		return "engineer"
	case e.IsDesigner():
		return "designer"
	case e.IsGuardian():
		return "guardian"
	case e.IsInspector():
		return "inspector"
	case e.IsTester():
		return "tester"
	case e.IsLibrarian():
		return "librarian"
	case e.IsAcademic():
		return "academic"
	case e.IsArchivalist():
		return "archivalist"
	case e.IsSteering():
		return "steering"
	default:
		return "unknown"
	}
}
