package coordination

import "time"

const (
	ActionClaimScope      = "coord_claim_scope"
	ActionReleaseScope    = "coord_release_scope"
	ActionPublishArtifact = "coord_publish_artifact"
	ActionRequestReview   = "coord_request_review"
	ActionResolveArtifact = "coord_resolve_artifact"
	ActionQueryView       = "coord_query_view"
	ActionWatchUpdates    = "coord_watch_updates"
)

type ScopeKind string

const (
	ScopeKindFile           ScopeKind = "file"
	ScopeKindSymbol         ScopeKind = "symbol"
	ScopeKindAPI            ScopeKind = "api"
	ScopeKindTestSurface    ScopeKind = "test_surface"
	ScopeKindUXSurface      ScopeKind = "ux_surface"
	ScopeKindInvariant      ScopeKind = "invariant"
	ScopeKindInvestigation  ScopeKind = "investigation"
	ScopeKindImplementation ScopeKind = "implementation"
	ScopeKindComponent      ScopeKind = "component"
)

type ClaimMode string

const (
	ClaimModeExclusive ClaimMode = "exclusive"
	ClaimModeShared    ClaimMode = "shared"
	ClaimModeReview    ClaimMode = "review"
)

type ClaimState string

const (
	ClaimStateActive     ClaimState = "active"
	ClaimStateReleased   ClaimState = "released"
	ClaimStateExpired    ClaimState = "expired"
	ClaimStateSuperseded ClaimState = "superseded"
)

type ArtifactStatus string

const (
	ArtifactStatusDraft      ArtifactStatus = "draft"
	ArtifactStatusPublished  ArtifactStatus = "published"
	ArtifactStatusAccepted   ArtifactStatus = "accepted"
	ArtifactStatusSuperseded ArtifactStatus = "superseded"
	ArtifactStatusResolved   ArtifactStatus = "resolved"
	ArtifactStatusRejected   ArtifactStatus = "rejected"
)

type ReviewStatus string

const (
	ReviewStatusPending          ReviewStatus = "pending"
	ReviewStatusAccepted         ReviewStatus = "accepted"
	ReviewStatusChangesRequested ReviewStatus = "changes_requested"
	ReviewStatusRejected         ReviewStatus = "rejected"
)

type Actor struct {
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	SessionID string `json:"session_id,omitempty"`
}

type EvidenceRef struct {
	Kind  string `json:"kind,omitempty"`
	Value string `json:"value"`
}

type ClaimScopeInput struct {
	TaskID         string        `json:"task_id"`
	TaskName       string        `json:"task_name,omitempty"`
	ScopeKind      ScopeKind     `json:"scope_kind"`
	ScopeKey       string        `json:"scope_key"`
	Purpose        string        `json:"purpose,omitempty"`
	Mode           ClaimMode     `json:"mode,omitempty"`
	LeaseSeconds   int           `json:"lease_seconds,omitempty"`
	IdempotencyKey string        `json:"idempotency_key,omitempty"`
	Evidence       []EvidenceRef `json:"evidence,omitempty"`
}

type ReleaseScopeInput struct {
	TaskID         string    `json:"task_id"`
	ClaimID        string    `json:"claim_id,omitempty"`
	ScopeKind      ScopeKind `json:"scope_kind,omitempty"`
	ScopeKey       string    `json:"scope_key,omitempty"`
	Resolution     string    `json:"resolution,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
}

type PublishArtifactInput struct {
	TaskID               string         `json:"task_id"`
	TaskName             string         `json:"task_name,omitempty"`
	Kind                 string         `json:"kind"`
	Title                string         `json:"title,omitempty"`
	Summary              string         `json:"summary"`
	ScopeKind            ScopeKind      `json:"scope_kind,omitempty"`
	ScopeKey             string         `json:"scope_key,omitempty"`
	Status               ArtifactStatus `json:"status,omitempty"`
	Payload              map[string]any `json:"payload,omitempty"`
	Evidence             []EvidenceRef  `json:"evidence,omitempty"`
	UpstreamArtifactIDs  []string       `json:"upstream_artifact_ids,omitempty"`
	SupersedesArtifactID string         `json:"supersedes_artifact_id,omitempty"`
	IdempotencyKey       string         `json:"idempotency_key,omitempty"`
}

type RequestReviewInput struct {
	TaskID         string   `json:"task_id"`
	ArtifactID     string   `json:"artifact_id"`
	ReviewerType   string   `json:"reviewer_type"`
	Summary        string   `json:"summary"`
	Criteria       []string `json:"criteria,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
}

type ResolveArtifactInput struct {
	TaskID            string         `json:"task_id"`
	ArtifactID        string         `json:"artifact_id,omitempty"`
	ReviewID          string         `json:"review_id,omitempty"`
	Status            ArtifactStatus `json:"status,omitempty"`
	ReviewStatus      ReviewStatus   `json:"review_status,omitempty"`
	ResolutionSummary string         `json:"resolution_summary,omitempty"`
	IdempotencyKey    string         `json:"idempotency_key,omitempty"`
}

type QueryViewInput struct {
	TaskID        string `json:"task_id"`
	TaskName      string `json:"task_name,omitempty"`
	WorkerType    string `json:"worker_type,omitempty"`
	IncludeDrafts bool   `json:"include_drafts,omitempty"`
}

type WatchUpdatesInput struct {
	TaskID        string `json:"task_id"`
	TaskName      string `json:"task_name,omitempty"`
	WorkerType    string `json:"worker_type,omitempty"`
	AfterVersion  int64  `json:"after_version,omitempty"`
	IncludeDrafts bool   `json:"include_drafts,omitempty"`
	WaitSeconds   int    `json:"wait_seconds,omitempty"`
}

type Claim struct {
	ID             string        `json:"id"`
	TaskID         string        `json:"task_id"`
	TaskName       string        `json:"task_name,omitempty"`
	ScopeKind      ScopeKind     `json:"scope_kind"`
	ScopeKey       string        `json:"scope_key"`
	Purpose        string        `json:"purpose,omitempty"`
	Mode           ClaimMode     `json:"mode"`
	State          ClaimState    `json:"state"`
	OwnerAgentID   string        `json:"owner_agent_id"`
	OwnerType      string        `json:"owner_type"`
	IdempotencyKey string        `json:"idempotency_key,omitempty"`
	Evidence       []EvidenceRef `json:"evidence,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	LeaseExpiresAt time.Time     `json:"lease_expires_at"`
}

type Artifact struct {
	ID                   string         `json:"id"`
	TaskID               string         `json:"task_id"`
	TaskName             string         `json:"task_name,omitempty"`
	Kind                 string         `json:"kind"`
	Title                string         `json:"title,omitempty"`
	Summary              string         `json:"summary"`
	ScopeKind            ScopeKind      `json:"scope_kind,omitempty"`
	ScopeKey             string         `json:"scope_key,omitempty"`
	Status               ArtifactStatus `json:"status"`
	ProducerAgentID      string         `json:"producer_agent_id"`
	ProducerType         string         `json:"producer_type"`
	IdempotencyKey       string         `json:"idempotency_key,omitempty"`
	Payload              map[string]any `json:"payload,omitempty"`
	Evidence             []EvidenceRef  `json:"evidence,omitempty"`
	UpstreamArtifactIDs  []string       `json:"upstream_artifact_ids,omitempty"`
	SupersedesArtifactID string         `json:"supersedes_artifact_id,omitempty"`
	Version              int            `json:"version"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

type Review struct {
	ID               string         `json:"id"`
	TaskID           string         `json:"task_id"`
	ArtifactID       string         `json:"artifact_id"`
	RequesterAgentID string         `json:"requester_agent_id"`
	RequesterType    string         `json:"requester_type"`
	ReviewerType     string         `json:"reviewer_type"`
	Summary          string         `json:"summary"`
	Criteria         []string       `json:"criteria,omitempty"`
	Status           ReviewStatus   `json:"status"`
	IdempotencyKey   string         `json:"idempotency_key,omitempty"`
	Result           map[string]any `json:"result,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type TaskView struct {
	TaskID              string     `json:"task_id"`
	TaskName            string     `json:"task_name,omitempty"`
	Version             int64      `json:"version"`
	Claims              []Claim    `json:"claims"`
	Artifacts           []Artifact `json:"artifacts"`
	Reviews             []Review   `json:"reviews"`
	CoordinationSummary string     `json:"coordination_summary,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type CoordinationContract struct {
	WorkerType             string   `json:"worker_type"`
	Summary                string   `json:"summary"`
	MustClaimBeforeWork    bool     `json:"must_claim_before_work"`
	MinimumClaims          int      `json:"minimum_claims"`
	MinimumArtifacts       int      `json:"minimum_artifacts"`
	PreferredArtifactKinds []string `json:"preferred_artifact_kinds,omitempty"`
	WatchForPeerUpdates    bool     `json:"watch_for_peer_updates"`
}

type PrecedentSummary struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id,omitempty"`
	Category  string         `json:"category,omitempty"`
	Title     string         `json:"title,omitempty"`
	Summary   string         `json:"summary"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type WorkerPacket struct {
	TaskID               string                `json:"task_id"`
	TaskName             string                `json:"task_name,omitempty"`
	WorkerType           string                `json:"worker_type"`
	Version              int64                 `json:"version"`
	MyClaims             []Claim               `json:"my_claims,omitempty"`
	PeerClaims           []Claim               `json:"peer_claims,omitempty"`
	RelevantArtifacts    []Artifact            `json:"relevant_artifacts,omitempty"`
	PendingReviews       []Review              `json:"pending_reviews,omitempty"`
	Contract             *CoordinationContract `json:"contract,omitempty"`
	HistoricalPrecedents []PrecedentSummary    `json:"historical_precedents,omitempty"`
	Summary              string                `json:"summary,omitempty"`
	UpdatedAt            time.Time             `json:"updated_at"`
}

type QueryViewResult struct {
	View   TaskView      `json:"view"`
	Packet *WorkerPacket `json:"packet,omitempty"`
}

type WatchUpdatesResult struct {
	HasChanges bool          `json:"has_changes"`
	View       TaskView      `json:"view"`
	Packet     *WorkerPacket `json:"packet,omitempty"`
}

type ArchivalEvent struct {
	Type      string         `json:"type"`
	TaskID    string         `json:"task_id"`
	TaskName  string         `json:"task_name,omitempty"`
	Actor     Actor          `json:"actor"`
	Summary   string         `json:"summary"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}
