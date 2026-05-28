package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/google/uuid"
)

const DeltaSchemaV1 = "sylk.claims.delta.v1"

// DeltaAction is the closed action vocabulary for canonical claims
// deltas. It is the top-level semantic verb; claim action type lives
// under context.claim.action.
type DeltaAction string

const (
	DeltaActionClaimGenerated                      DeltaAction = "claim.generated"
	DeltaActionClaimGenerationFailed               DeltaAction = "claim.generation_failed"
	DeltaActionClaimPosted                         DeltaAction = "claim.posted"
	DeltaActionClaimPostFailed                     DeltaAction = "claim.post_failed"
	DeltaActionClaimReceived                       DeltaAction = "claim.received"
	DeltaActionClaimReceiptFailed                  DeltaAction = "claim.receipt_failed"
	DeltaActionClaimProgressed                     DeltaAction = "claim.progressed"
	DeltaActionClaimProgressFailed                 DeltaAction = "claim.progress_failed"
	DeltaActionClaimTestamentGenerated             DeltaAction = "claim.testament_generated"
	DeltaActionClaimTestamentGenerationFailed      DeltaAction = "claim.testament_generation_failed"
	DeltaActionClaimTestamentAcknowledged          DeltaAction = "claim.testament_acknowledged"
	DeltaActionClaimTestamentAcknowledgementFailed DeltaAction = "claim.testament_acknowledgement_failed"
	DeltaActionClaimValidating                     DeltaAction = "claim.validating"
	DeltaActionClaimSatisfied                      DeltaAction = "claim.satisfied"
	DeltaActionClaimValidationIncomplete           DeltaAction = "claim.validation_incomplete"
	DeltaActionClaimValidationFailed               DeltaAction = "claim.validation_failed"
	DeltaActionClaimValidationErrored              DeltaAction = "claim.validation_errored"

	DeltaActionTestamentGenerated            DeltaAction = "testament.generated"
	DeltaActionTestamentPosted               DeltaAction = "testament.posted"
	DeltaActionTestamentReceived             DeltaAction = "testament.received"
	DeltaActionTestamentValidating           DeltaAction = "testament.validating"
	DeltaActionTestamentValidationIncomplete DeltaAction = "testament.validation_incomplete"
	DeltaActionTestamentValidationFailed     DeltaAction = "testament.validation_failed"
	DeltaActionTestamentValidated            DeltaAction = "testament.validated"

	DeltaActionValidationEvaluated DeltaAction = "validation.evaluated"
)

var claimLifecycleDeltaActions = map[ClaimLifecycleStatus]DeltaAction{
	ClaimLifecycleGenerated:                      DeltaActionClaimGenerated,
	ClaimLifecycleGenerationFailed:               DeltaActionClaimGenerationFailed,
	ClaimLifecyclePosted:                         DeltaActionClaimPosted,
	ClaimLifecyclePostFailed:                     DeltaActionClaimPostFailed,
	ClaimLifecycleReceived:                       DeltaActionClaimReceived,
	ClaimLifecycleReceiptFailed:                  DeltaActionClaimReceiptFailed,
	ClaimLifecycleProgressed:                     DeltaActionClaimProgressed,
	ClaimLifecycleProgressFailed:                 DeltaActionClaimProgressFailed,
	ClaimLifecycleTestamentGenerated:             DeltaActionClaimTestamentGenerated,
	ClaimLifecycleTestamentGenerationFailed:      DeltaActionClaimTestamentGenerationFailed,
	ClaimLifecycleTestamentAcknowledged:          DeltaActionClaimTestamentAcknowledged,
	ClaimLifecycleTestamentAcknowledgementFailed: DeltaActionClaimTestamentAcknowledgementFailed,
	ClaimLifecycleValidating:                     DeltaActionClaimValidating,
	ClaimLifecycleSatisfied:                      DeltaActionClaimSatisfied,
	ClaimLifecycleValidationIncomplete:           DeltaActionClaimValidationIncomplete,
	ClaimLifecycleValidationFailed:               DeltaActionClaimValidationFailed,
	ClaimLifecycleValidationErrored:              DeltaActionClaimValidationErrored,
}

var testamentLifecycleDeltaActions = map[TestamentLifecycleStatus]DeltaAction{
	TestamentLifecycleGenerated:            DeltaActionTestamentGenerated,
	TestamentLifecyclePosted:               DeltaActionTestamentPosted,
	TestamentLifecycleReceived:             DeltaActionTestamentReceived,
	TestamentLifecycleValidating:           DeltaActionTestamentValidating,
	TestamentLifecycleValidationIncomplete: DeltaActionTestamentValidationIncomplete,
	TestamentLifecycleValidationFailed:     DeltaActionTestamentValidationFailed,
	TestamentLifecycleValidated:            DeltaActionTestamentValidated,
}

var deltaActionClaimLifecycleStatuses = invertClaimLifecycleDeltaActions()
var deltaActionTestamentLifecycleStatuses = invertTestamentLifecycleDeltaActions()

// CanonicalDelta is the immutable envelope emitted after a board
// mutation commits. It intentionally has no top-level JSON "kind";
// Action is the semantic verb.
type CanonicalDelta struct {
	Schema     string         `json:"schema"`
	Action     DeltaAction    `json:"action"`
	DeltaID    string         `json:"delta_id"`
	Key        string         `json:"delta_key"`
	SessionID  string         `json:"session_id"`
	BoardID    string         `json:"board_id"`
	Sequence   uint64         `json:"sequence"`
	OccurredAt time.Time      `json:"occurred_at"`
	Actor      AgentRef       `json:"actor"`
	Delivery   *DeltaDelivery `json:"delivery,omitempty"`
	Refs       []DeltaRef     `json:"refs"`
	Context    map[string]any `json:"context,omitempty"`
}

func (d CanonicalDelta) DeltaKind() string      { return string(d.Action) }
func (d CanonicalDelta) DeltaKey() string       { return d.Key }
func (d CanonicalDelta) DeltaSequence() uint64  { return d.Sequence }
func (d CanonicalDelta) DeltaSessionID() string { return d.SessionID }

// NewCanonicalDelta stamps schema, id, and deterministic key defaults.
func NewCanonicalDelta(action DeltaAction, sessionID, boardID string, sequence uint64, occurredAt time.Time, actor AgentRef, refs []DeltaRef, delivery *DeltaDelivery, context map[string]any) CanonicalDelta {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	d := CanonicalDelta{
		Schema:     DeltaSchemaV1,
		Action:     action,
		DeltaID:    "delta_" + uuid.NewString(),
		SessionID:  strings.TrimSpace(sessionID),
		BoardID:    strings.TrimSpace(boardID),
		Sequence:   sequence,
		OccurredAt: occurredAt.UTC(),
		Actor:      actor.Normalized(),
		Delivery:   normalizeDelivery(delivery),
		Refs:       normalizeRefs(refs),
		Context:    context,
	}
	d.Key = BuildCanonicalDeltaKey(d.Action, d.SessionID, d.BoardID, d.Refs, d.Delivery)
	return d
}

// ValidateCanonicalDeltaStrict rejects malformed canonical deltas,
// including unknown actions.
func ValidateCanonicalDeltaStrict(d CanonicalDelta) error {
	return validateCanonicalDelta(d, true)
}

// ValidateCanonicalDeltaTolerant validates the envelope shape while
// allowing future actions to pass through observer paths.
func ValidateCanonicalDeltaTolerant(d CanonicalDelta) error {
	return validateCanonicalDelta(d, false)
}

func validateCanonicalDelta(d CanonicalDelta, strict bool) error {
	if strings.TrimSpace(d.Schema) != DeltaSchemaV1 {
		return fmt.Errorf("schema must be %q", DeltaSchemaV1)
	}
	if strings.TrimSpace(string(d.Action)) == "" {
		return fmt.Errorf("action is required")
	}
	if strict && !KnownDeltaAction(d.Action) {
		return fmt.Errorf("unknown delta action %q", d.Action)
	}
	if strings.TrimSpace(d.DeltaID) == "" {
		return fmt.Errorf("delta_id is required")
	}
	if strings.TrimSpace(d.Key) == "" {
		return fmt.Errorf("delta_key is required")
	}
	if strings.TrimSpace(d.SessionID) == "" {
		return fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(d.BoardID) == "" {
		return fmt.Errorf("board_id is required")
	}
	if d.Sequence == 0 {
		return fmt.Errorf("sequence is required")
	}
	if d.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at is required")
	}
	if err := d.Actor.Validate(); err != nil {
		return fmt.Errorf("actor: %w", err)
	}
	if len(d.Refs) == 0 {
		return fmt.Errorf("refs are required")
	}
	for idx, ref := range d.Refs {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("refs[%d]: %w", idx, err)
		}
	}
	if DeltaActionRequiresDelivery(d.Action) {
		if d.Delivery == nil || len(d.Delivery.To) == 0 {
			return fmt.Errorf("%s requires delivery.to", d.Action)
		}
	}
	if d.Delivery != nil {
		if err := d.Delivery.Validate(); err != nil {
			return fmt.Errorf("delivery: %w", err)
		}
	}
	return nil
}

func KnownDeltaAction(action DeltaAction) bool {
	if _, ok := deltaActionClaimLifecycleStatuses[action]; ok {
		return true
	}
	if _, ok := deltaActionTestamentLifecycleStatuses[action]; ok {
		return true
	}
	switch action {
	case DeltaActionValidationEvaluated:
		return true
	default:
		return false
	}
}

func KnownDeltaActions() []DeltaAction {
	out := make([]DeltaAction, 0, len(claimLifecycleDeltaActions)+len(testamentLifecycleDeltaActions)+1)
	for _, status := range KnownClaimLifecycleStatuses() {
		if action, ok := ClaimLifecycleDeltaAction(status); ok {
			out = append(out, action)
		}
	}
	for _, status := range KnownTestamentLifecycleStatuses() {
		if action, ok := TestamentLifecycleDeltaAction(status); ok {
			out = append(out, action)
		}
	}
	out = append(out, DeltaActionValidationEvaluated)
	return out
}

// DeltaActionMayCompleteExpectedWork reports whether a canonical
// action can satisfy an expectation registered by an issuing agent.
// Generated, posted claim, and progress notifications are deliberately false:
// they are narration/routing, never terminal peer-work completion.
func DeltaActionMayCompleteExpectedWork(action DeltaAction) bool {
	switch action {
	case DeltaActionTestamentPosted,
		DeltaActionTestamentReceived,
		DeltaActionTestamentValidated,
		DeltaActionTestamentValidationIncomplete,
		DeltaActionTestamentValidationFailed,
		DeltaActionClaimTestamentAcknowledged,
		DeltaActionClaimSatisfied,
		DeltaActionClaimValidationIncomplete,
		DeltaActionClaimValidationFailed,
		DeltaActionClaimValidationErrored,
		DeltaActionValidationEvaluated:
		return true
	default:
		return false
	}
}

func DeltaActionRequiresDelivery(action DeltaAction) bool {
	switch action {
	case DeltaActionClaimPosted,
		DeltaActionClaimReceived,
		DeltaActionClaimReceiptFailed,
		DeltaActionClaimTestamentAcknowledged,
		DeltaActionClaimTestamentAcknowledgementFailed,
		DeltaActionTestamentPosted,
		DeltaActionTestamentReceived:
		return true
	default:
		return false
	}
}

func ClaimLifecycleDeltaAction(status ClaimLifecycleStatus) (DeltaAction, bool) {
	action, ok := claimLifecycleDeltaActions[status]
	return action, ok
}

func TestamentLifecycleDeltaAction(status TestamentLifecycleStatus) (DeltaAction, bool) {
	action, ok := testamentLifecycleDeltaActions[status]
	return action, ok
}

func DeltaActionClaimLifecycleStatus(action DeltaAction) (ClaimLifecycleStatus, bool) {
	status, ok := deltaActionClaimLifecycleStatuses[action]
	return status, ok
}

func DeltaActionTestamentLifecycleStatus(action DeltaAction) (TestamentLifecycleStatus, bool) {
	status, ok := deltaActionTestamentLifecycleStatuses[action]
	return status, ok
}

func DeltaActionStartsClaimWork(action DeltaAction) bool {
	return action == DeltaActionClaimPosted
}

func DeltaActionIsGenerated(action DeltaAction) bool {
	return action == DeltaActionClaimGenerated || action == DeltaActionTestamentGenerated
}

func DeltaActionIsProgressOnly(action DeltaAction) bool {
	return action == DeltaActionClaimProgressed
}

func invertClaimLifecycleDeltaActions() map[DeltaAction]ClaimLifecycleStatus {
	out := make(map[DeltaAction]ClaimLifecycleStatus, len(claimLifecycleDeltaActions))
	for status, action := range claimLifecycleDeltaActions {
		out[action] = status
	}
	return out
}

func invertTestamentLifecycleDeltaActions() map[DeltaAction]TestamentLifecycleStatus {
	out := make(map[DeltaAction]TestamentLifecycleStatus, len(testamentLifecycleDeltaActions))
	for status, action := range testamentLifecycleDeltaActions {
		out[action] = status
	}
	return out
}

func BuildCanonicalDeltaKey(action DeltaAction, sessionID, boardID string, refs []DeltaRef, delivery *DeltaDelivery) string {
	parts := []string{
		string(action),
		strings.TrimSpace(sessionID),
		strings.TrimSpace(boardID),
	}
	for _, ref := range normalizeRefs(refs) {
		parts = append(parts, ref.Role+":"+ref.Type+":"+ref.ID)
	}
	if delivery != nil {
		to := make([]string, 0, len(delivery.To))
		for _, ref := range delivery.To {
			if key := ref.RouteKey(); key != "" {
				to = append(to, key)
			}
		}
		sort.Strings(to)
		if len(to) > 0 {
			parts = append(parts, "to:"+strings.Join(to, ","))
		}
		if relationship := strings.TrimSpace(delivery.Relationship); relationship != "" {
			parts = append(parts, "rel:"+relationship)
		}
	}
	return strings.Join(parts, ":")
}

// AgentRef is a claims-local projection of canonical agent identity.
// Legacy type-only references are explicit degraded refs.
type AgentRef struct {
	UID              string            `json:"uid,omitempty"`
	Namespace        string            `json:"namespace,omitempty"`
	Pod              string            `json:"pod,omitempty"`
	Name             string            `json:"name,omitempty"`
	Type             string            `json:"type,omitempty"`
	Category         string            `json:"category,omitempty"`
	Generation       uint64            `json:"generation,omitempty"`
	Model            string            `json:"model,omitempty"`
	Task             *AgentTaskRef     `json:"task,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	Unresolved       bool              `json:"unresolved,omitempty"`
	ResolutionReason string            `json:"resolution_reason,omitempty"`
}

type AgentTaskRef struct {
	UID        string `json:"uid,omitempty"`
	DisplayID  string `json:"display_id,omitempty"`
	PipelineID string `json:"pipeline_id,omitempty"`
}

// AgentRefResolver resolves legacy relation strings into canonical
// agent refs. It is optional: boards without an identity registry emit
// explicit degraded refs, while boards with a resolver route by UID.
type AgentRefResolver interface {
	ResolveAgentRef(ctx context.Context, sessionID, agentID string) (AgentRef, bool)
}

// AgentRefResolverFunc adapts a function to AgentRefResolver.
type AgentRefResolverFunc func(ctx context.Context, sessionID, agentID string) (AgentRef, bool)

func (f AgentRefResolverFunc) ResolveAgentRef(ctx context.Context, sessionID, agentID string) (AgentRef, bool) {
	if f == nil {
		return AgentRef{}, false
	}
	return f(ctx, sessionID, agentID)
}

func AgentRefFromIdentity(id *identity.AgentIdentity, task *identity.TaskRef) AgentRef {
	if id == nil {
		return AgentRef{Unresolved: true, ResolutionReason: "missing identity"}
	}
	pod := id.Pod()
	ref := AgentRef{
		UID:        string(id.UID()),
		Namespace:  string(id.Namespace()),
		Pod:        string(pod.ID),
		Name:       string(id.Name()),
		Type:       id.Kind().String(),
		Category:   id.Category().String(),
		Generation: uint64(id.Generation()),
		Model:      id.Model().String(),
		Labels:     mapStringString(id.Labels()),
	}
	if task != nil {
		ref.Task = &AgentTaskRef{
			UID:        string(task.UID()),
			DisplayID:  task.DisplayID(),
			PipelineID: string(task.PipelineID()),
		}
	}
	return ref.Normalized()
}

func DegradedAgentRef(agentType, reason string) AgentRef {
	agentType = strings.TrimSpace(agentType)
	if reason = strings.TrimSpace(reason); reason == "" {
		reason = "legacy reference did not carry canonical identity"
	}
	return AgentRef{
		Type:             agentType,
		Name:             agentType,
		Unresolved:       true,
		ResolutionReason: reason,
	}.Normalized()
}

func (r AgentRef) Normalized() AgentRef {
	r.UID = strings.TrimSpace(r.UID)
	r.Namespace = strings.TrimSpace(r.Namespace)
	r.Pod = strings.TrimSpace(r.Pod)
	r.Name = strings.TrimSpace(r.Name)
	r.Type = strings.TrimSpace(r.Type)
	r.Category = strings.TrimSpace(r.Category)
	r.Model = strings.TrimSpace(r.Model)
	r.ResolutionReason = strings.TrimSpace(r.ResolutionReason)
	if r.Type == "" && r.Name != "" {
		r.Type = r.Name
	}
	if r.Name == "" && r.Type != "" {
		r.Name = r.Type
	}
	if len(r.Labels) == 0 {
		r.Labels = nil
	}
	return r
}

func (r AgentRef) Validate() error {
	r = r.Normalized()
	if r.Unresolved {
		if r.Type == "" && r.Name == "" && r.UID == "" {
			return fmt.Errorf("unresolved ref requires type, name, or uid")
		}
		return nil
	}
	if r.UID == "" {
		return fmt.Errorf("uid is required unless unresolved=true")
	}
	if r.Type == "" {
		return fmt.Errorf("type is required")
	}
	if r.Generation == 0 && r.Model == "" {
		return fmt.Errorf("generation/model are required for canonical refs")
	}
	return nil
}

func (r AgentRef) RouteKey() string {
	r = r.Normalized()
	if r.UID != "" {
		return r.UID
	}
	if r.Type != "" {
		return r.Type
	}
	return r.Name
}

func (r AgentRef) MatchesAgentID(agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	r = r.Normalized()
	return agentID == r.UID || agentID == r.Type || agentID == r.Name
}

// DeltaDelivery identifies intended recipients for directed deltas.
type DeltaDelivery struct {
	To           []AgentRef `json:"to,omitempty"`
	Relationship string     `json:"relationship,omitempty"`
}

func (d DeltaDelivery) Validate() error {
	for idx, ref := range d.To {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("to[%d]: %w", idx, err)
		}
	}
	return nil
}

func normalizeDelivery(in *DeltaDelivery) *DeltaDelivery {
	if in == nil {
		return nil
	}
	out := &DeltaDelivery{
		Relationship: strings.TrimSpace(in.Relationship),
	}
	if len(in.To) > 0 {
		out.To = make([]AgentRef, 0, len(in.To))
		for _, ref := range in.To {
			normalized := ref.Normalized()
			if normalized.RouteKey() == "" {
				continue
			}
			out.To = append(out.To, normalized)
		}
	}
	if len(out.To) == 0 && out.Relationship == "" {
		return nil
	}
	return out
}

// DeltaRef is a typed object reference inside a canonical delta.
type DeltaRef struct {
	Role string `json:"role"`
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (r DeltaRef) Validate() error {
	if strings.TrimSpace(r.Role) == "" {
		return fmt.Errorf("role is required")
	}
	if strings.TrimSpace(r.Type) == "" {
		return fmt.Errorf("type is required")
	}
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("id is required")
	}
	return nil
}

func normalizeRefs(in []DeltaRef) []DeltaRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]DeltaRef, 0, len(in))
	for _, ref := range in {
		ref.Role = strings.TrimSpace(ref.Role)
		ref.Type = strings.TrimSpace(ref.Type)
		ref.ID = strings.TrimSpace(ref.ID)
		if ref.Role == "" || ref.Type == "" || ref.ID == "" {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func (d CanonicalDelta) RefID(role, typ string) string {
	role = strings.TrimSpace(role)
	typ = strings.TrimSpace(typ)
	for _, ref := range d.Refs {
		if ref.Role == role && ref.Type == typ {
			return ref.ID
		}
	}
	return ""
}

func (d CanonicalDelta) ClaimID() string {
	if id := d.RefID("claim", RelatedTypeClaim); id != "" {
		return id
	}
	if claim, ok := d.Context["claim"].(map[string]any); ok {
		if id, _ := claim["id"].(string); strings.TrimSpace(id) != "" {
			return id
		}
	}
	return ""
}

func (d CanonicalDelta) TestamentID() string {
	return d.RefID("testament", RelatedTypeTestament)
}

func (d CanonicalDelta) ValidationID() string {
	return d.RefID("validation", RelatedTypeValidation)
}

func (d CanonicalDelta) ClaimActionType() ActionType {
	claim, ok := d.Context["claim"].(map[string]any)
	if !ok {
		return ""
	}
	action, _ := claim["action"].(string)
	return ActionType(strings.TrimSpace(action))
}

func (d CanonicalDelta) ClaimToStatus() ClaimStatus {
	if lifecycle, ok := d.ClaimLifecycleStatus(); ok {
		return claimLifecycleCoarseStatus(lifecycle)
	}
	claim, ok := d.Context["claim"].(map[string]any)
	if !ok {
		return ""
	}
	if status, _ := claim["to_status"].(string); status != "" {
		return ClaimStatus(status)
	}
	if status, _ := claim["status"].(string); status != "" {
		return ClaimStatus(status)
	}
	return ""
}

func (d CanonicalDelta) ClaimLifecycleStatus() (ClaimLifecycleStatus, bool) {
	if status, ok := DeltaActionClaimLifecycleStatus(d.Action); ok {
		return status, true
	}
	claim, ok := d.Context["claim"].(map[string]any)
	if !ok {
		return "", false
	}
	if status, _ := claim["lifecycle_status"].(string); strings.TrimSpace(status) != "" {
		lifecycle := ClaimLifecycleStatus(strings.TrimSpace(status))
		return lifecycle, lifecycle.Valid()
	}
	return "", false
}

func (d CanonicalDelta) TestamentLifecycleStatus() (TestamentLifecycleStatus, bool) {
	if status, ok := DeltaActionTestamentLifecycleStatus(d.Action); ok {
		return status, true
	}
	testament, ok := d.Context["testament"].(map[string]any)
	if !ok {
		return "", false
	}
	if status, _ := testament["lifecycle_status"].(string); strings.TrimSpace(status) != "" {
		lifecycle := TestamentLifecycleStatus(strings.TrimSpace(status))
		return lifecycle, lifecycle.Valid()
	}
	return "", false
}

func claimLifecycleCoarseStatus(status ClaimLifecycleStatus) ClaimStatus {
	switch status {
	case ClaimLifecycleGenerated, ClaimLifecyclePosted:
		return ClaimStatusPending
	case ClaimLifecycleReceived, ClaimLifecycleProgressed:
		return ClaimStatusInProgress
	case ClaimLifecycleTestamentGenerated, ClaimLifecycleTestamentAcknowledged, ClaimLifecycleValidating:
		return ClaimStatusTestified
	case ClaimLifecycleSatisfied:
		return ClaimStatusAccepted
	case ClaimLifecycleGenerationFailed,
		ClaimLifecyclePostFailed,
		ClaimLifecycleReceiptFailed,
		ClaimLifecycleProgressFailed,
		ClaimLifecycleTestamentGenerationFailed,
		ClaimLifecycleTestamentAcknowledgementFailed,
		ClaimLifecycleValidationIncomplete,
		ClaimLifecycleValidationFailed,
		ClaimLifecycleValidationErrored:
		return ClaimStatusRejected
	default:
		return ""
	}
}

func (d CanonicalDelta) DeliveredTo(agentID string) bool {
	if d.Delivery == nil {
		return false
	}
	for _, ref := range d.Delivery.To {
		if ref.MatchesAgentID(agentID) {
			return true
		}
	}
	return false
}

func canonicalDeltaFromJSON(data []byte, strict bool) (CanonicalDelta, bool, error) {
	var probe struct {
		Schema string      `json:"schema"`
		Action DeltaAction `json:"action"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return CanonicalDelta{}, false, err
	}
	if probe.Schema != DeltaSchemaV1 || probe.Action == "" {
		return CanonicalDelta{}, false, nil
	}
	var d CanonicalDelta
	if err := json.Unmarshal(data, &d); err != nil {
		return CanonicalDelta{}, true, err
	}
	if strict {
		return d, true, ValidateCanonicalDeltaStrict(d)
	}
	return d, true, ValidateCanonicalDeltaTolerant(d)
}

func mapStringString(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func canonicalSequenceString(seq uint64) string {
	if seq == 0 {
		return ""
	}
	return strconv.FormatUint(seq, 10)
}
