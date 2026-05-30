package claims

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrResultTestamentInvalid = errors.New("result testament invalid")

type ResultTestamentBuilderConfig struct {
	Board   *ClaimsBoard
	ActorID string
}

type ResultTestamentBuilder struct {
	board   *ClaimsBoard
	actorID string
}

type ResultTestamentRequest struct {
	ClaimID           string
	SourceTestamentID string
	Artifacts         []*Artifact
	ActorID           string
	IdempotencyKey    string
	Reason            string
}

type ResultTestamentResult struct {
	Action      Action
	Testament   Testament
	Skipped     bool
	ArtifactIDs []string
}

func NewResultTestamentBuilder(cfg ResultTestamentBuilderConfig) (*ResultTestamentBuilder, error) {
	if cfg.Board == nil {
		return nil, fmt.Errorf("%w: board is required", ErrResultTestamentInvalid)
	}
	return &ResultTestamentBuilder{board: cfg.Board, actorID: strings.TrimSpace(cfg.ActorID)}, nil
}

func (b *ResultTestamentBuilder) Build(ctx context.Context, req ResultTestamentRequest) (ResultTestamentResult, error) {
	if b == nil || b.board == nil {
		return ResultTestamentResult{}, fmt.Errorf("%w: builder is not initialized", ErrResultTestamentInvalid)
	}
	req.ActorID = firstNonEmpty(req.ActorID, b.actorID, "claims.result_validator")
	return b.board.BuildValidationResultTestament(ctx, req)
}

func (b *ClaimsBoard) BuildValidationResultTestament(ctx context.Context, req ResultTestamentRequest) (ResultTestamentResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ResultTestamentResult{}, err
	}
	if len(req.Artifacts) == 0 {
		return ResultTestamentResult{Skipped: true}, nil
	}
	b.mu.Lock()
	result, err := b.buildValidationResultTestamentLocked(req)
	b.mu.Unlock()
	if err == nil && !result.Skipped {
		b.projectDurableOutbox(ctx)
		b.notifySubscribers()
	}
	return result, err
}

func (b *ClaimsBoard) buildValidationResultTestamentLocked(req ResultTestamentRequest) (ResultTestamentResult, error) {
	req = normalizeResultTestamentRequest(req)
	if existing := b.generatedTestamentActionByKeyLocked(req.IdempotencyKey); existing != nil {
		return existingResultTestament(existing), nil
	}
	if err := b.validateResultTestamentRequestLocked(req); err != nil {
		return ResultTestamentResult{}, err
	}
	now := time.Now().UTC()
	prevSeq := b.seq.Load()
	action, testament := b.stampResultTestamentLocked(req, now)
	outbox := b.outboxRecordsForResultTestamentLocked(testament, now)
	if err := b.appendDurableEventLocked(walEventTestamentActionGenerated, action.AgentID, map[string]any{"action": action, "testaments": []Testament{testament}}, outbox); err != nil {
		b.seq.Store(prevSeq)
		return ResultTestamentResult{}, err
	}
	b.storeResultTestamentLocked(&action, &testament)
	b.invalidateProjectionCache()
	return resultTestamentSnapshot(action, testament), nil
}

func normalizeResultTestamentRequest(req ResultTestamentRequest) ResultTestamentRequest {
	req.ClaimID = strings.TrimSpace(req.ClaimID)
	req.SourceTestamentID = strings.TrimSpace(req.SourceTestamentID)
	req.ActorID = strings.TrimSpace(req.ActorID)
	req.IdempotencyKey = firstNonEmpty(req.IdempotencyKey, "validation_results:"+req.ClaimID+":"+req.SourceTestamentID)
	req.Reason = lifecycleReason(req.Reason, "validation result testament posted")
	return req
}

func (b *ClaimsBoard) validateResultTestamentRequestLocked(req ResultTestamentRequest) error {
	if req.ClaimID == "" {
		return fmt.Errorf("%w: claim_id is required", ErrResultTestamentInvalid)
	}
	if _, ok := b.claims[req.ClaimID]; !ok {
		return fmt.Errorf("%w: claim %q not found", ErrResultTestamentInvalid, req.ClaimID)
	}
	if req.SourceTestamentID != "" {
		if _, ok := b.testaments[req.SourceTestamentID]; !ok {
			return fmt.Errorf("%w: source testament %q not found", ErrResultTestamentInvalid, req.SourceTestamentID)
		}
	}
	return validateResultArtifacts(req)
}

func validateResultArtifacts(req ResultTestamentRequest) error {
	seen := make(map[string]struct{}, len(req.Artifacts))
	for _, artifact := range req.Artifacts {
		if artifact == nil {
			return fmt.Errorf("%w: nil result artifact", ErrResultTestamentInvalid)
		}
		if err := validateResultArtifact(req, artifact, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateResultArtifact(req ResultTestamentRequest, artifact *Artifact, seen map[string]struct{}) error {
	if strings.TrimSpace(artifact.ID) != "" {
		if _, ok := seen[artifact.ID]; ok {
			return fmt.Errorf("%w: duplicate result artifact %q", ErrResultTestamentInvalid, artifact.ID)
		}
		seen[artifact.ID] = struct{}{}
	}
	if strings.TrimSpace(artifact.ClaimID) != "" && artifact.ClaimID != req.ClaimID {
		return fmt.Errorf("%w: artifact %q belongs to claim %q, want %q", ErrResultTestamentInvalid, artifact.ID, artifact.ClaimID, req.ClaimID)
	}
	return nil
}

func (b *ClaimsBoard) stampResultTestamentLocked(req ResultTestamentRequest, now time.Time) (Action, Testament) {
	action := resultTestamentAction(req, b, now)
	testament := resultTestament(req, b, action.ID, now)
	for _, artifact := range req.Artifacts {
		testament.Artifacts = append(testament.Artifacts, b.stampResultTestamentArtifactLocked(CloneArtifact(artifact), req, testament.ID, now))
	}
	return action, testament
}

func resultTestamentAction(req ResultTestamentRequest, b *ClaimsBoard, now time.Time) Action {
	return Action{
		ID:             uuid.NewString(),
		AgentID:        req.ActorID,
		Type:           ActionTypeTestament,
		Status:         ActionStatusComplete,
		SessionID:      b.sessionID,
		PipelineID:     b.pipelineID,
		TaskID:         b.taskID,
		Sequence:       b.nextSeq(),
		Created:        now,
		Accessed:       now,
		IdempotencyKey: req.IdempotencyKey,
		StatusHistory:  []StatusChange{statusChange("", ActionStatusComplete, req.ActorID, "validation result action generated", now)},
	}
}

func resultTestament(req ResultTestamentRequest, b *ClaimsBoard, actionID string, now time.Time) Testament {
	return Testament{
		ID:              uuid.NewString(),
		AgentID:         req.ActorID,
		SessionID:       b.sessionID,
		PipelineID:      b.pipelineID,
		TaskID:          b.taskID,
		Sequence:        b.nextSeq(),
		Created:         now,
		Accessed:        now,
		Summary:         "validation result artifacts",
		Confidence:      "deterministic",
		IdempotencyKey:  req.IdempotencyKey,
		LifecycleStatus: TestamentLifecyclePosted,
		LifecycleHistory: []StatusChange{
			statusChange("", TestamentLifecycleGenerated, req.ActorID, "validation result testament generated", now),
			statusChange(TestamentLifecycleGenerated, TestamentLifecyclePosted, req.ActorID, req.Reason, now),
		},
		Relations: resultTestamentRelations(req, actionID),
	}
}

func resultTestamentRelations(req ResultTestamentRequest, actionID string) []Relation {
	relations := []Relation{
		{Related: actionID, RelatedType: RelatedTypeAction, Relationship: RelationshipTestamentAction},
		{Related: req.ClaimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		{Related: req.ActorID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
	}
	if req.SourceTestamentID != "" {
		relations = append(relations, Relation{Related: req.SourceTestamentID, RelatedType: RelatedTypeTestament, Relationship: RelationshipDerivedFrom})
	}
	return relations
}

func (b *ClaimsBoard) stampResultTestamentArtifactLocked(artifact *Artifact, req ResultTestamentRequest, testamentID string, now time.Time) *Artifact {
	if artifact.ID == "" {
		artifact.ID = uuid.NewString()
	}
	artifact.TestamentID = testamentID
	artifact.ClaimID = req.ClaimID
	artifact.AgentID = firstNonEmpty(artifact.AgentID, req.ActorID)
	artifact.ParticipantID = firstNonEmpty(artifact.ParticipantID, artifact.AgentID)
	artifact.SessionID = b.sessionID
	artifact.PipelineID = b.pipelineID
	artifact.TaskID = b.taskID
	if artifact.Sequence == 0 {
		artifact.Sequence = b.nextSeq()
	}
	artifact.Created = firstNonZeroTime(artifact.Created, now)
	artifact.Accessed = now
	artifact.Status = ArtifactStatusGenerated
	artifact.StatusHistory = capStatusHistory(append(artifact.StatusHistory, statusChange("", ArtifactStatusGenerated, req.ActorID, "validation result artifact generated", now)))
	artifact.Size = artifactSize(artifact)
	return artifact
}

func (b *ClaimsBoard) outboxRecordsForResultTestamentLocked(testament Testament, now time.Time) []ClaimsOutboxRecord {
	records := b.outboxRecordsForTestamentLifecycleLocked([]Testament{testament}, TestamentLifecycleGenerated, now)
	records = append(records, b.outboxRecordsForTestamentLifecycleLocked([]Testament{testament}, TestamentLifecyclePosted, now)...)
	for _, artifact := range testament.Artifacts {
		if artifact != nil {
			records = append(records, b.outboxRecordLocked(artifact.Sequence, RelatedTypeArtifact, artifact.ID, string(DeltaActionArtifactGenerated), now))
		}
	}
	return records
}

func (b *ClaimsBoard) storeResultTestamentLocked(action *Action, testament *Testament) {
	b.actions[action.ID] = action
	b.indexRelations(action.ID, action.Relations)
	if action.IdempotencyKey != "" {
		b.testamentGenerationKeys[action.IdempotencyKey] = action.ID
	}
	b.testaments[testament.ID] = testament
	b.indexRelations(testament.ID, testament.Relations)
	for _, artifact := range testament.Artifacts {
		b.indexArtifactLocked(artifact)
		b.indexRelations(artifact.ID, artifact.Relations)
	}
}

func resultTestamentSnapshot(action Action, testament Testament) ResultTestamentResult {
	out := ResultTestamentResult{Action: action, Testament: testament}
	for _, artifact := range testament.Artifacts {
		if artifact != nil {
			out.ArtifactIDs = append(out.ArtifactIDs, artifact.ID)
		}
	}
	return out
}

func existingResultTestament(existing *GeneratedTestamentAction) ResultTestamentResult {
	if existing == nil || len(existing.Testaments) == 0 {
		return ResultTestamentResult{Skipped: true}
	}
	return resultTestamentSnapshot(existing.Action, existing.Testaments[0])
}
