package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/pipeline/coordination"
	"github.com/dgraph-io/ristretto"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type CoordinationServiceConfig struct {
	MaxCost int64
}

func DefaultCoordinationServiceConfig() CoordinationServiceConfig {
	return CoordinationServiceConfig{MaxCost: 2_000_000}
}

type CoordinationService struct {
	store        *Store
	cache        *ristretto.Cache
	archiveEvent func(context.Context, *coordination.ArchivalEvent)
	watchersMu   sync.Mutex
	watchers     map[string]map[chan struct{}]struct{}
}

func NewCoordinationService(store *Store, cfg CoordinationServiceConfig) (*CoordinationService, error) {
	if store == nil {
		return nil, fmt.Errorf("coordination service requires store")
	}
	if cfg.MaxCost <= 0 {
		cfg = DefaultCoordinationServiceConfig()
	}
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 10_000,
		MaxCost:     cfg.MaxCost,
		BufferItems: 64,
	})
	if err != nil {
		return nil, fmt.Errorf("coordination cache: %w", err)
	}
	return &CoordinationService{
		store:    store,
		cache:    cache,
		watchers: make(map[string]map[chan struct{}]struct{}),
	}, nil
}

func (s *CoordinationService) SetArchiveEmitter(fn func(context.Context, *coordination.ArchivalEvent)) {
	s.archiveEvent = fn
}

func (s *CoordinationService) Close() {
	if s == nil || s.cache == nil {
		return
	}
	s.watchersMu.Lock()
	channels := make([]chan struct{}, 0)
	for _, taskWatchers := range s.watchers {
		for ch := range taskWatchers {
			channels = append(channels, ch)
		}
	}
	s.watchers = make(map[string]map[chan struct{}]struct{})
	s.watchersMu.Unlock()
	for _, ch := range channels {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	s.cache.Close()
}

func (s *CoordinationService) ClaimScope(
	ctx context.Context,
	actor coordination.Actor,
	input coordination.ClaimScopeInput,
) (*coordination.Claim, error) {
	if strings.TrimSpace(input.TaskID) == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if strings.TrimSpace(input.ScopeKey) == "" || input.ScopeKind == "" {
		return nil, fmt.Errorf("scope_kind and scope_key are required")
	}
	if strings.TrimSpace(actor.AgentID) == "" || strings.TrimSpace(actor.AgentType) == "" {
		return nil, fmt.Errorf("coordination actor is required")
	}
	if input.Mode == "" {
		input.Mode = coordination.ClaimModeExclusive
	}
	if input.LeaseSeconds <= 0 {
		input.LeaseSeconds = 600
	}

	if err := s.expireClaims(ctx, input.TaskID); err != nil {
		return nil, err
	}
	if existing, err := s.findExistingClaim(ctx, actor, input); err != nil {
		return nil, err
	} else if existing != nil {
		renewed, renewErr := s.renewClaimLease(ctx, existing, input.LeaseSeconds)
		if renewErr != nil {
			return nil, renewErr
		}
		s.invalidateTask(existing.TaskID)
		return renewed, nil
	}
	conflicts, err := s.listConflictingClaims(ctx, actor, input)
	if err != nil {
		return nil, err
	}
	if len(conflicts) > 0 {
		return nil, fmt.Errorf("scope %s:%s already claimed by %s",
			input.ScopeKind,
			input.ScopeKey,
			conflictingOwners(conflicts),
		)
	}

	now := time.Now().UTC()
	claim := &coordination.Claim{
		ID:             uuid.NewString(),
		TaskID:         strings.TrimSpace(input.TaskID),
		TaskName:       strings.TrimSpace(input.TaskName),
		ScopeKind:      input.ScopeKind,
		ScopeKey:       strings.TrimSpace(input.ScopeKey),
		Purpose:        strings.TrimSpace(input.Purpose),
		Mode:           input.Mode,
		State:          coordination.ClaimStateActive,
		OwnerAgentID:   strings.TrimSpace(actor.AgentID),
		OwnerType:      strings.TrimSpace(actor.AgentType),
		IdempotencyKey: strings.TrimSpace(input.IdempotencyKey),
		Evidence:       cloneEvidence(input.Evidence),
		CreatedAt:      now,
		UpdatedAt:      now,
		LeaseExpiresAt: now.Add(time.Duration(input.LeaseSeconds) * time.Second),
	}
	if err := s.store.db.RunInWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := s.insertClaim(ctx, tx, claim); err != nil {
			return err
		}
		return s.insertAction(ctx, tx, coordination.ActionClaimScope, claim.TaskID, claim.TaskName, actor, claim.IdempotencyKey, claim)
	}); err != nil {
		return nil, err
	}
	s.invalidateTask(claim.TaskID)
	return claim, nil
}

func (s *CoordinationService) renewClaimLease(ctx context.Context, claim *coordination.Claim, leaseSeconds int) (*coordination.Claim, error) {
	if claim == nil {
		return nil, nil
	}
	if leaseSeconds <= 0 {
		leaseSeconds = 600
	}
	now := time.Now().UTC()
	claim.UpdatedAt = now
	claim.LeaseExpiresAt = now.Add(time.Duration(leaseSeconds) * time.Second)
	if err := s.store.db.RunInWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE coordination_claims SET updated_at = ?, lease_expires_at = ? WHERE id = ?`,
			claim.UpdatedAt, claim.LeaseExpiresAt, claim.ID,
		)
		if err != nil {
			return fmt.Errorf("renew coordination claim: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return claim, nil
}

func (s *CoordinationService) ReleaseScope(
	ctx context.Context,
	actor coordination.Actor,
	input coordination.ReleaseScopeInput,
) (*coordination.Claim, error) {
	if strings.TrimSpace(input.TaskID) == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	claim, err := s.lookupReleaseTarget(ctx, actor, input)
	if err != nil {
		return nil, err
	}
	if claim == nil {
		return nil, fmt.Errorf("active claim not found")
	}
	now := time.Now().UTC()
	claim.State = coordination.ClaimStateReleased
	claim.UpdatedAt = now
	if err := s.store.db.RunInWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE coordination_claims SET state = ?, updated_at = ? WHERE id = ?`,
			string(claim.State), claim.UpdatedAt, claim.ID,
		); err != nil {
			return fmt.Errorf("release coordination claim: %w", err)
		}
		return s.insertAction(ctx, tx, coordination.ActionReleaseScope, claim.TaskID, claim.TaskName, actor, strings.TrimSpace(input.IdempotencyKey), map[string]any{
			"claim_id":    claim.ID,
			"scope_kind":  claim.ScopeKind,
			"scope_key":   claim.ScopeKey,
			"resolution":  strings.TrimSpace(input.Resolution),
			"released_at": now,
		})
	}); err != nil {
		return nil, err
	}
	s.invalidateTask(claim.TaskID)
	return claim, nil
}

func (s *CoordinationService) PublishArtifact(
	ctx context.Context,
	actor coordination.Actor,
	input coordination.PublishArtifactInput,
) (*coordination.Artifact, error) {
	if strings.TrimSpace(input.TaskID) == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Summary) == "" {
		return nil, fmt.Errorf("artifact kind and summary are required")
	}
	if strings.TrimSpace(actor.AgentID) == "" || strings.TrimSpace(actor.AgentType) == "" {
		return nil, fmt.Errorf("coordination actor is required")
	}
	if input.Status == "" {
		input.Status = coordination.ArtifactStatusPublished
	}
	now := time.Now().UTC()
	var (
		artifact *coordination.Artifact
		created  bool
	)

	if err := s.store.db.RunInWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		persisted, wasCreated, err := s.publishArtifactTx(ctx, tx, actor, input, now)
		if err != nil {
			return err
		}
		artifact = persisted
		created = wasCreated
		return nil
	}); err != nil {
		return nil, err
	}
	if artifact == nil {
		return nil, fmt.Errorf("publish coordination artifact: no artifact persisted")
	}
	if !created {
		return artifact, nil
	}
	s.invalidateTask(artifact.TaskID)
	s.emitArchival(ctx, &coordination.ArchivalEvent{
		Type:     "coordination_artifact_published",
		TaskID:   artifact.TaskID,
		TaskName: artifact.TaskName,
		Actor:    actor,
		Summary:  fmt.Sprintf("%s published %s", actor.AgentType, artifact.Kind),
		Metadata: map[string]any{
			"artifact_id": artifact.ID,
			"kind":        artifact.Kind,
			"status":      artifact.Status,
			"scope_kind":  artifact.ScopeKind,
			"scope_key":   artifact.ScopeKey,
			"summary":     artifact.Summary,
		},
		Timestamp: time.Now().UTC(),
	})
	return artifact, nil
}

func (s *CoordinationService) RequestReview(
	ctx context.Context,
	actor coordination.Actor,
	input coordination.RequestReviewInput,
) (*coordination.Review, error) {
	if strings.TrimSpace(input.TaskID) == "" || strings.TrimSpace(input.ArtifactID) == "" || strings.TrimSpace(input.ReviewerType) == "" {
		return nil, fmt.Errorf("task_id, artifact_id, and reviewer_type are required")
	}
	now := time.Now().UTC()
	var (
		review  *coordination.Review
		created bool
	)
	if err := s.store.db.RunInWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		persisted, wasCreated, err := s.requestReviewTx(ctx, tx, actor, input, now)
		if err != nil {
			return err
		}
		review = persisted
		created = wasCreated
		return nil
	}); err != nil {
		return nil, err
	}
	if review == nil {
		return nil, fmt.Errorf("request coordination review: no review persisted")
	}
	if !created {
		return review, nil
	}
	s.invalidateTask(review.TaskID)
	return review, nil
}

func (s *CoordinationService) ResolveArtifact(
	ctx context.Context,
	actor coordination.Actor,
	input coordination.ResolveArtifactInput,
) (map[string]any, error) {
	if strings.TrimSpace(input.TaskID) == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if strings.TrimSpace(input.ArtifactID) == "" && strings.TrimSpace(input.ReviewID) == "" {
		return nil, fmt.Errorf("artifact_id or review_id is required")
	}
	now := time.Now().UTC()
	result := map[string]any{}

	if err := s.store.db.RunInWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if strings.TrimSpace(input.ArtifactID) != "" {
			status := input.Status
			if status == "" {
				status = coordination.ArtifactStatusResolved
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE coordination_artifacts SET status = ?, updated_at = ? WHERE id = ? AND task_id = ?`,
				string(status), now, strings.TrimSpace(input.ArtifactID), strings.TrimSpace(input.TaskID),
			); err != nil {
				return fmt.Errorf("resolve coordination artifact: %w", err)
			}
			result["artifact_id"] = strings.TrimSpace(input.ArtifactID)
			result["artifact_status"] = status
		}
		if strings.TrimSpace(input.ReviewID) != "" {
			status := input.ReviewStatus
			if status == "" {
				status = coordination.ReviewStatusAccepted
			}
			resultJSON, err := json.Marshal(map[string]any{
				"resolution_summary": strings.TrimSpace(input.ResolutionSummary),
				"actor":              actor,
				"resolved_at":        now,
			})
			if err != nil {
				return fmt.Errorf("marshal coordination review result: %w", err)
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE coordination_reviews SET status = ?, result_json = ?, updated_at = ? WHERE id = ? AND task_id = ?`,
				string(status), string(resultJSON), now, strings.TrimSpace(input.ReviewID), strings.TrimSpace(input.TaskID),
			); err != nil {
				return fmt.Errorf("resolve coordination review: %w", err)
			}
			result["review_id"] = strings.TrimSpace(input.ReviewID)
			result["review_status"] = status
		}
		return s.insertAction(ctx, tx, coordination.ActionResolveArtifact, strings.TrimSpace(input.TaskID), "", actor, strings.TrimSpace(input.IdempotencyKey), map[string]any{
			"artifact_id":        strings.TrimSpace(input.ArtifactID),
			"review_id":          strings.TrimSpace(input.ReviewID),
			"artifact_status":    result["artifact_status"],
			"review_status":      result["review_status"],
			"resolution_summary": strings.TrimSpace(input.ResolutionSummary),
			"resolved_at":        now,
		})
	}); err != nil {
		return nil, err
	}
	s.invalidateTask(strings.TrimSpace(input.TaskID))
	s.emitArchival(ctx, &coordination.ArchivalEvent{
		Type:      "coordination_resolution",
		TaskID:    strings.TrimSpace(input.TaskID),
		Actor:     actor,
		Summary:   strings.TrimSpace(input.ResolutionSummary),
		Metadata:  cloneMap(result),
		Timestamp: time.Now().UTC(),
	})
	return result, nil
}

func (s *CoordinationService) QueryView(
	ctx context.Context,
	input coordination.QueryViewInput,
) (*coordination.QueryViewResult, error) {
	if strings.TrimSpace(input.TaskID) == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if err := s.expireClaims(ctx, input.TaskID); err != nil {
		return nil, err
	}

	viewKey := s.viewCacheKey(input.TaskID, input.IncludeDrafts)
	if val, ok := s.cache.Get(viewKey); ok {
		if cached, castOK := val.(*coordination.TaskView); castOK && cached != nil {
			result := &coordination.QueryViewResult{View: *cached}
			if strings.TrimSpace(input.WorkerType) != "" {
				packet, err := s.buildWorkerPacket(input.TaskID, input.TaskName, strings.TrimSpace(input.WorkerType), cached)
				if err != nil {
					return nil, err
				}
				result.Packet = packet
			}
			return result, nil
		}
	}

	view, err := s.loadTaskView(ctx, input)
	if err != nil {
		return nil, err
	}
	s.cache.Set(viewKey, view, 1)
	result := &coordination.QueryViewResult{View: *view}
	if strings.TrimSpace(input.WorkerType) != "" {
		packet, err := s.buildWorkerPacket(input.TaskID, input.TaskName, strings.TrimSpace(input.WorkerType), view)
		if err != nil {
			return nil, err
		}
		result.Packet = packet
	}
	return result, nil
}

func (s *CoordinationService) WatchUpdates(
	ctx context.Context,
	input coordination.WatchUpdatesInput,
) (*coordination.WatchUpdatesResult, error) {
	if strings.TrimSpace(input.TaskID) == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if input.WaitSeconds <= 0 {
		input.WaitSeconds = 20
	}

	current, err := s.QueryView(ctx, coordination.QueryViewInput{
		TaskID:        input.TaskID,
		TaskName:      input.TaskName,
		WorkerType:    input.WorkerType,
		IncludeDrafts: input.IncludeDrafts,
	})
	if err != nil {
		return nil, err
	}
	if current != nil && current.View.Version > input.AfterVersion {
		return &coordination.WatchUpdatesResult{
			HasChanges: true,
			View:       current.View,
			Packet:     current.Packet,
		}, nil
	}

	waitCh := s.registerWatcher(strings.TrimSpace(input.TaskID))
	defer s.unregisterWatcher(strings.TrimSpace(input.TaskID), waitCh)

	timer := time.NewTimer(time.Duration(input.WaitSeconds) * time.Second)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			final, err := s.QueryView(ctx, coordination.QueryViewInput{
				TaskID:        input.TaskID,
				TaskName:      input.TaskName,
				WorkerType:    input.WorkerType,
				IncludeDrafts: input.IncludeDrafts,
			})
			if err != nil {
				return nil, err
			}
			return &coordination.WatchUpdatesResult{
				HasChanges: final != nil && final.View.Version > input.AfterVersion,
				View:       final.View,
				Packet:     final.Packet,
			}, nil
		case <-waitCh:
			next, err := s.QueryView(ctx, coordination.QueryViewInput{
				TaskID:        input.TaskID,
				TaskName:      input.TaskName,
				WorkerType:    input.WorkerType,
				IncludeDrafts: input.IncludeDrafts,
			})
			if err != nil {
				return nil, err
			}
			if next != nil && next.View.Version > input.AfterVersion {
				return &coordination.WatchUpdatesResult{
					HasChanges: true,
					View:       next.View,
					Packet:     next.Packet,
				}, nil
			}
		}
	}
}

func (s *CoordinationService) GetCachedPrecedents(taskName, taskSlug, workerType string) ([]coordination.PrecedentSummary, bool) {
	key := s.precedentCacheKey(taskName, taskSlug, workerType)
	if key == "" {
		return nil, false
	}
	if val, ok := s.cache.Get(key); ok {
		if typed, castOK := val.([]coordination.PrecedentSummary); castOK {
			cloned := append([]coordination.PrecedentSummary(nil), typed...)
			return cloned, true
		}
	}
	return nil, false
}

func (s *CoordinationService) StoreCachedPrecedents(taskName, taskSlug, workerType string, precedents []coordination.PrecedentSummary) {
	key := s.precedentCacheKey(taskName, taskSlug, workerType)
	if key == "" {
		return
	}
	cloned := append([]coordination.PrecedentSummary(nil), precedents...)
	s.cache.Set(key, cloned, int64(1+len(cloned)*4))
}

func (s *CoordinationService) ReleaseTaskClaims(ctx context.Context, taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	now := time.Now().UTC()
	if err := s.store.db.RunInWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE coordination_claims SET state = ?, updated_at = ? WHERE task_id = ? AND state = ?`,
			string(coordination.ClaimStateReleased),
			now,
			taskID,
			string(coordination.ClaimStateActive),
		)
		if err != nil {
			return fmt.Errorf("release task coordination claims: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	s.invalidateTask(taskID)
	return nil
}

func (s *CoordinationService) findExistingClaim(
	ctx context.Context,
	actor coordination.Actor,
	input coordination.ClaimScopeInput,
) (*coordination.Claim, error) {
	if strings.TrimSpace(input.IdempotencyKey) != "" {
		row := s.store.db.QueryRowContext(ctx,
			`SELECT id, task_id, COALESCE(task_name, ''), scope_kind, scope_key, COALESCE(purpose, ''), mode, state,
			 owner_agent_id, owner_type, COALESCE(idempotency_key, ''), COALESCE(evidence_json, '[]'),
			 created_at, updated_at, lease_expires_at
			 FROM coordination_claims
			 WHERE task_id = ? AND owner_agent_id = ? AND idempotency_key = ? LIMIT 1`,
			strings.TrimSpace(input.TaskID), strings.TrimSpace(actor.AgentID), strings.TrimSpace(input.IdempotencyKey),
		)
		claim, err := scanClaim(row)
		if err == nil && claim != nil {
			return claim, nil
		}
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("query existing claim: %w", err)
		}
	}

	row := s.store.db.QueryRowContext(ctx,
		`SELECT id, task_id, COALESCE(task_name, ''), scope_kind, scope_key, COALESCE(purpose, ''), mode, state,
		 owner_agent_id, owner_type, COALESCE(idempotency_key, ''), COALESCE(evidence_json, '[]'),
		 created_at, updated_at, lease_expires_at
		 FROM coordination_claims
		 WHERE task_id = ? AND owner_agent_id = ? AND scope_kind = ? AND scope_key = ? AND state = ? LIMIT 1`,
		strings.TrimSpace(input.TaskID), strings.TrimSpace(actor.AgentID), string(input.ScopeKind), strings.TrimSpace(input.ScopeKey), string(coordination.ClaimStateActive),
	)
	claim, err := scanClaim(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query matching claim: %w", err)
	}
	return claim, nil
}

func (s *CoordinationService) listConflictingClaims(
	ctx context.Context,
	actor coordination.Actor,
	input coordination.ClaimScopeInput,
) ([]coordination.Claim, error) {
	rows, err := s.store.db.QueryContext(ctx,
		`SELECT id, task_id, COALESCE(task_name, ''), scope_kind, scope_key, COALESCE(purpose, ''), mode, state,
		 owner_agent_id, owner_type, COALESCE(idempotency_key, ''), COALESCE(evidence_json, '[]'),
		 created_at, updated_at, lease_expires_at
		 FROM coordination_claims
		 WHERE task_id = ? AND scope_kind = ? AND state = ? AND owner_agent_id <> ?`,
		strings.TrimSpace(input.TaskID), string(input.ScopeKind), string(coordination.ClaimStateActive), strings.TrimSpace(actor.AgentID),
	)
	if err != nil {
		return nil, fmt.Errorf("query conflicting claims: %w", err)
	}
	defer rows.Close()

	var conflicts []coordination.Claim
	for rows.Next() {
		claim, err := scanClaimRows(rows)
		if err != nil {
			return nil, err
		}
		if !scopeKeysConflict(input.ScopeKind, input.ScopeKey, claim.ScopeKey) {
			continue
		}
		if input.Mode == coordination.ClaimModeShared && claim.Mode == coordination.ClaimModeShared {
			continue
		}
		conflicts = append(conflicts, *claim)
	}
	return conflicts, rows.Err()
}

func scopeKeysConflict(kind coordination.ScopeKind, requested, existing string) bool {
	requested = strings.TrimSpace(requested)
	existing = strings.TrimSpace(existing)
	if requested == "" || existing == "" {
		return false
	}
	if requested == existing {
		return true
	}

	switch kind {
	case coordination.ScopeKindFile, coordination.ScopeKindComponent, coordination.ScopeKindImplementation:
		return pathScopeOverlap(requested, existing)
	default:
		return tokenScopeOverlap(requested, existing)
	}
}

func pathScopeOverlap(a, b string) bool {
	a = strings.Trim(strings.ReplaceAll(a, "\\", "/"), "/")
	b = strings.Trim(strings.ReplaceAll(b, "\\", "/"), "/")
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func tokenScopeOverlap(a, b string) bool {
	if a == b {
		return true
	}
	aLower := strings.ToLower(a)
	bLower := strings.ToLower(b)
	return strings.Contains(aLower, bLower) || strings.Contains(bLower, aLower)
}

func conflictingOwners(claims []coordination.Claim) string {
	if len(claims) == 0 {
		return ""
	}
	owners := make([]string, 0, len(claims))
	for _, claim := range claims {
		owners = append(owners, fmt.Sprintf("%s(%s)", claim.OwnerType, claim.OwnerAgentID))
	}
	sort.Strings(owners)
	return strings.Join(owners, ", ")
}

func (s *CoordinationService) lookupReleaseTarget(
	ctx context.Context,
	actor coordination.Actor,
	input coordination.ReleaseScopeInput,
) (*coordination.Claim, error) {
	var row *sql.Row
	if strings.TrimSpace(input.ClaimID) != "" {
		row = s.store.db.QueryRowContext(ctx,
			`SELECT id, task_id, COALESCE(task_name, ''), scope_kind, scope_key, COALESCE(purpose, ''), mode, state,
			 owner_agent_id, owner_type, COALESCE(idempotency_key, ''), COALESCE(evidence_json, '[]'),
			 created_at, updated_at, lease_expires_at
			 FROM coordination_claims
			 WHERE id = ? AND task_id = ? AND owner_agent_id = ? AND state = ?`,
			strings.TrimSpace(input.ClaimID), strings.TrimSpace(input.TaskID), strings.TrimSpace(actor.AgentID), string(coordination.ClaimStateActive),
		)
	} else {
		row = s.store.db.QueryRowContext(ctx,
			`SELECT id, task_id, COALESCE(task_name, ''), scope_kind, scope_key, COALESCE(purpose, ''), mode, state,
			 owner_agent_id, owner_type, COALESCE(idempotency_key, ''), COALESCE(evidence_json, '[]'),
			 created_at, updated_at, lease_expires_at
			 FROM coordination_claims
			 WHERE task_id = ? AND owner_agent_id = ? AND scope_kind = ? AND scope_key = ? AND state = ? LIMIT 1`,
			strings.TrimSpace(input.TaskID), strings.TrimSpace(actor.AgentID), string(input.ScopeKind), strings.TrimSpace(input.ScopeKey), string(coordination.ClaimStateActive),
		)
	}
	claim, err := scanClaim(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup release claim: %w", err)
	}
	return claim, nil
}

func (s *CoordinationService) publishArtifactTx(
	ctx context.Context,
	tx bun.Tx,
	actor coordination.Actor,
	input coordination.PublishArtifactInput,
	now time.Time,
) (*coordination.Artifact, bool, error) {
	existing, err := s.findExistingArtifactInTx(ctx, tx, actor, input)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}
	artifact := newCoordinationArtifact(actor, input, now)
	version, err := s.nextArtifactVersionInTx(ctx, tx, artifact)
	if err != nil {
		return nil, false, err
	}
	artifact.Version = version
	if err := s.supersedeArtifactTx(ctx, tx, artifact, now); err != nil {
		return nil, false, err
	}
	if err := s.insertArtifact(ctx, tx, artifact); err != nil {
		return nil, false, err
	}
	if err := s.insertAction(ctx, tx, coordination.ActionPublishArtifact, artifact.TaskID, artifact.TaskName, actor, artifact.IdempotencyKey, artifact); err != nil {
		return nil, false, err
	}
	return artifact, true, nil
}

func newCoordinationArtifact(
	actor coordination.Actor,
	input coordination.PublishArtifactInput,
	now time.Time,
) *coordination.Artifact {
	return &coordination.Artifact{
		ID:                   uuid.NewString(),
		TaskID:               strings.TrimSpace(input.TaskID),
		TaskName:             strings.TrimSpace(input.TaskName),
		Kind:                 strings.TrimSpace(input.Kind),
		Title:                strings.TrimSpace(input.Title),
		Summary:              strings.TrimSpace(input.Summary),
		ScopeKind:            input.ScopeKind,
		ScopeKey:             strings.TrimSpace(input.ScopeKey),
		Status:               input.Status,
		ProducerAgentID:      strings.TrimSpace(actor.AgentID),
		ProducerType:         strings.TrimSpace(actor.AgentType),
		IdempotencyKey:       strings.TrimSpace(input.IdempotencyKey),
		Payload:              cloneMap(input.Payload),
		Evidence:             cloneEvidence(input.Evidence),
		UpstreamArtifactIDs:  append([]string(nil), input.UpstreamArtifactIDs...),
		SupersedesArtifactID: strings.TrimSpace(input.SupersedesArtifactID),
		Version:              1,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

func (s *CoordinationService) findExistingArtifactInTx(
	ctx context.Context,
	tx bun.Tx,
	actor coordination.Actor,
	input coordination.PublishArtifactInput,
) (*coordination.Artifact, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return nil, nil
	}
	row := tx.QueryRowContext(ctx,
		`SELECT id, task_id, COALESCE(task_name, ''), kind, COALESCE(title, ''), summary, COALESCE(scope_kind, ''), COALESCE(scope_key, ''),
		 status, producer_agent_id, producer_type, COALESCE(idempotency_key, ''), COALESCE(payload_json, '{}'),
		 COALESCE(evidence_json, '[]'), COALESCE(upstream_artifact_ids_json, '[]'), COALESCE(supersedes_artifact_id, ''), version, created_at, updated_at
		 FROM coordination_artifacts
		 WHERE task_id = ? AND producer_agent_id = ? AND idempotency_key = ? LIMIT 1`,
		strings.TrimSpace(input.TaskID), strings.TrimSpace(actor.AgentID), strings.TrimSpace(input.IdempotencyKey),
	)
	artifact, err := scanArtifact(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query existing artifact: %w", err)
	}
	return artifact, nil
}

func (s *CoordinationService) nextArtifactVersionInTx(
	ctx context.Context,
	tx bun.Tx,
	artifact *coordination.Artifact,
) (int, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM coordination_artifacts
		 WHERE task_id = ? AND kind = ? AND producer_type = ? AND COALESCE(scope_key, '') = ?`,
		artifact.TaskID, artifact.Kind, artifact.ProducerType, artifact.ScopeKey,
	)
	var current int
	if err := row.Scan(&current); err != nil {
		return 0, fmt.Errorf("query artifact version: %w", err)
	}
	return current + 1, nil
}

func (s *CoordinationService) supersedeArtifactTx(
	ctx context.Context,
	tx bun.Tx,
	artifact *coordination.Artifact,
	now time.Time,
) error {
	if artifact == nil || strings.TrimSpace(artifact.SupersedesArtifactID) == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE coordination_artifacts SET status = ?, updated_at = ? WHERE id = ?`,
		string(coordination.ArtifactStatusSuperseded),
		now,
		artifact.SupersedesArtifactID,
	); err != nil {
		return fmt.Errorf("supersede coordination artifact: %w", err)
	}
	return nil
}

func (s *CoordinationService) requestReviewTx(
	ctx context.Context,
	tx bun.Tx,
	actor coordination.Actor,
	input coordination.RequestReviewInput,
	now time.Time,
) (*coordination.Review, bool, error) {
	existing, err := s.findExistingReviewInTx(ctx, tx, actor, input)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}
	review := newCoordinationReview(actor, input, now)
	if err := s.insertReview(ctx, tx, review); err != nil {
		return nil, false, err
	}
	if err := s.insertAction(ctx, tx, coordination.ActionRequestReview, review.TaskID, "", actor, review.IdempotencyKey, review); err != nil {
		return nil, false, err
	}
	return review, true, nil
}

func newCoordinationReview(
	actor coordination.Actor,
	input coordination.RequestReviewInput,
	now time.Time,
) *coordination.Review {
	return &coordination.Review{
		ID:               uuid.NewString(),
		TaskID:           strings.TrimSpace(input.TaskID),
		ArtifactID:       strings.TrimSpace(input.ArtifactID),
		RequesterAgentID: strings.TrimSpace(actor.AgentID),
		RequesterType:    strings.TrimSpace(actor.AgentType),
		ReviewerType:     strings.TrimSpace(input.ReviewerType),
		Summary:          strings.TrimSpace(input.Summary),
		Criteria:         append([]string(nil), input.Criteria...),
		Status:           coordination.ReviewStatusPending,
		IdempotencyKey:   strings.TrimSpace(input.IdempotencyKey),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func (s *CoordinationService) findExistingReviewInTx(
	ctx context.Context,
	tx bun.Tx,
	actor coordination.Actor,
	input coordination.RequestReviewInput,
) (*coordination.Review, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return nil, nil
	}
	row := tx.QueryRowContext(ctx,
		`SELECT id, task_id, artifact_id, requester_agent_id, requester_type, reviewer_type, summary,
		 COALESCE(criteria_json, '[]'), status, COALESCE(idempotency_key, ''), COALESCE(result_json, '{}'),
		 created_at, updated_at
		 FROM coordination_reviews
		 WHERE task_id = ? AND requester_agent_id = ? AND idempotency_key = ? LIMIT 1`,
		strings.TrimSpace(input.TaskID), strings.TrimSpace(actor.AgentID), strings.TrimSpace(input.IdempotencyKey),
	)
	review, err := scanReview(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query existing review: %w", err)
	}
	return review, nil
}

func (s *CoordinationService) expireClaims(ctx context.Context, taskID string) error {
	now := time.Now().UTC()
	if err := s.store.db.RunInWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE coordination_claims SET state = ?, updated_at = ?
			 WHERE task_id = ? AND state = ? AND lease_expires_at < ?`,
			string(coordination.ClaimStateExpired), now, strings.TrimSpace(taskID), string(coordination.ClaimStateActive), now,
		)
		if err != nil {
			return fmt.Errorf("expire coordination claims: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	s.invalidateTask(taskID)
	return nil
}

func (s *CoordinationService) loadTaskView(
	ctx context.Context,
	input coordination.QueryViewInput,
) (*coordination.TaskView, error) {
	taskName := strings.TrimSpace(input.TaskName)
	claims, err := s.listClaims(ctx, input.TaskID)
	if err != nil {
		return nil, err
	}
	if taskName == "" {
		for _, claim := range claims {
			if strings.TrimSpace(claim.TaskName) != "" {
				taskName = claim.TaskName
				break
			}
		}
	}

	artifacts, err := s.listArtifacts(ctx, input.TaskID, input.IncludeDrafts)
	if err != nil {
		return nil, err
	}
	if taskName == "" {
		for _, artifact := range artifacts {
			if strings.TrimSpace(artifact.TaskName) != "" {
				taskName = artifact.TaskName
				break
			}
		}
	}

	reviews, err := s.listReviews(ctx, input.TaskID)
	if err != nil {
		return nil, err
	}

	updatedAt := latestCoordinationTimestamp(claims, artifacts, reviews)
	view := &coordination.TaskView{
		TaskID:              strings.TrimSpace(input.TaskID),
		TaskName:            taskName,
		Version:             updatedAt.UnixNano(),
		Claims:              claims,
		Artifacts:           artifacts,
		Reviews:             reviews,
		CoordinationSummary: buildCoordinationSummary(claims, artifacts, reviews),
		UpdatedAt:           updatedAt,
	}
	return view, nil
}

func (s *CoordinationService) buildWorkerPacket(
	taskID, taskName, workerType string,
	view *coordination.TaskView,
) (*coordination.WorkerPacket, error) {
	packetKey := s.packetCacheKey(taskID, workerType, view.Version)
	if val, ok := s.cache.Get(packetKey); ok {
		if cached, castOK := val.(*coordination.WorkerPacket); castOK && cached != nil {
			copy := *cached
			return &copy, nil
		}
	}

	packet := &coordination.WorkerPacket{
		TaskID:            strings.TrimSpace(taskID),
		TaskName:          firstNonEmptyTaskName(strings.TrimSpace(taskName), view.TaskName),
		WorkerType:        workerType,
		Version:           view.Version,
		MyClaims:          make([]coordination.Claim, 0),
		PeerClaims:        make([]coordination.Claim, 0),
		RelevantArtifacts: make([]coordination.Artifact, 0),
		PendingReviews:    make([]coordination.Review, 0),
		Contract:          coordinationContractForWorker(workerType),
		UpdatedAt:         view.UpdatedAt,
	}
	for _, claim := range view.Claims {
		if claim.OwnerType == workerType {
			packet.MyClaims = append(packet.MyClaims, claim)
		} else {
			packet.PeerClaims = append(packet.PeerClaims, claim)
		}
	}
	for _, artifact := range view.Artifacts {
		if artifactRelevantToWorker(workerType, artifact) {
			packet.RelevantArtifacts = append(packet.RelevantArtifacts, artifact)
		}
	}
	for _, review := range view.Reviews {
		if review.ReviewerType == workerType && review.Status == coordination.ReviewStatusPending {
			packet.PendingReviews = append(packet.PendingReviews, review)
		}
	}
	packet.Summary = buildWorkerSummary(workerType, packet)
	s.cache.Set(packetKey, packet, 1)
	return packet, nil
}

func artifactRelevantToWorker(workerType string, artifact coordination.Artifact) bool {
	kind := strings.ToLower(strings.TrimSpace(artifact.Kind))
	producer := strings.ToLower(strings.TrimSpace(artifact.ProducerType))
	switch workerType {
	case "inspector-pipeline":
		return producer != "inspector-pipeline"
	case "tester-pipeline":
		return producer == "inspector-pipeline" || strings.Contains(kind, "risk") || strings.Contains(kind, "invariant") || strings.Contains(kind, "implementation")
	case "engineer":
		return producer == "inspector-pipeline" || producer == "tester-pipeline" || producer == "designer" ||
			strings.Contains(kind, "risk") || strings.Contains(kind, "test") || strings.Contains(kind, "ux") || strings.Contains(kind, "component")
	case "designer":
		return producer == "tester-pipeline" || producer == "engineer" || producer == "inspector-pipeline" ||
			strings.Contains(kind, "ux") || strings.Contains(kind, "component") || strings.Contains(kind, "failure")
	default:
		return true
	}
}

func buildWorkerSummary(workerType string, packet *coordination.WorkerPacket) string {
	if packet == nil {
		return ""
	}
	var parts []string
	if len(packet.MyClaims) > 0 {
		parts = append(parts, fmt.Sprintf("You currently hold %d active claim(s).", len(packet.MyClaims)))
	}
	if len(packet.PeerClaims) > 0 {
		parts = append(parts, fmt.Sprintf("%d peer claim(s) are active; avoid overlapping their scope unless you explicitly review or share.", len(packet.PeerClaims)))
	}
	if len(packet.RelevantArtifacts) > 0 {
		parts = append(parts, fmt.Sprintf("%d relevant artifact(s) already exist for this task; consume them before rediscovering the same facts.", len(packet.RelevantArtifacts)))
	}
	if len(packet.PendingReviews) > 0 {
		parts = append(parts, fmt.Sprintf("%d review request(s) are pending for %s.", len(packet.PendingReviews), workerType))
	}
	if packet.Contract != nil && strings.TrimSpace(packet.Contract.Summary) != "" {
		parts = append(parts, packet.Contract.Summary)
	}
	if len(packet.HistoricalPrecedents) > 0 {
		parts = append(parts, fmt.Sprintf("%d historical coordination precedent(s) are available.", len(packet.HistoricalPrecedents)))
	}
	return strings.Join(parts, " ")
}

func buildCoordinationSummary(
	claims []coordination.Claim,
	artifacts []coordination.Artifact,
	reviews []coordination.Review,
) string {
	var parts []string
	if len(claims) > 0 {
		parts = append(parts, fmt.Sprintf("%d active coordination claim(s).", len(claims)))
	}
	if len(artifacts) > 0 {
		parts = append(parts, fmt.Sprintf("%d artifact(s) already published.", len(artifacts)))
	}
	pendingReviews := 0
	for _, review := range reviews {
		if review.Status == coordination.ReviewStatusPending {
			pendingReviews++
		}
	}
	if pendingReviews > 0 {
		parts = append(parts, fmt.Sprintf("%d review request(s) are pending.", pendingReviews))
	}
	return strings.Join(parts, " ")
}

func (s *CoordinationService) listClaims(ctx context.Context, taskID string) ([]coordination.Claim, error) {
	rows, err := s.store.db.QueryContext(ctx,
		`SELECT id, task_id, COALESCE(task_name, ''), scope_kind, scope_key, COALESCE(purpose, ''), mode, state,
		 owner_agent_id, owner_type, COALESCE(idempotency_key, ''), COALESCE(evidence_json, '[]'),
		 created_at, updated_at, lease_expires_at
		 FROM coordination_claims
		 WHERE task_id = ? AND state = ?
		 ORDER BY updated_at DESC, created_at DESC`,
		strings.TrimSpace(taskID), string(coordination.ClaimStateActive),
	)
	if err != nil {
		return nil, fmt.Errorf("list coordination claims: %w", err)
	}
	defer rows.Close()

	var claims []coordination.Claim
	for rows.Next() {
		claim, err := scanClaimRows(rows)
		if err != nil {
			return nil, err
		}
		claims = append(claims, *claim)
	}
	return claims, rows.Err()
}

func (s *CoordinationService) listArtifacts(ctx context.Context, taskID string, includeDrafts bool) ([]coordination.Artifact, error) {
	query := `SELECT id, task_id, COALESCE(task_name, ''), kind, COALESCE(title, ''), summary, COALESCE(scope_kind, ''), COALESCE(scope_key, ''),
		 status, producer_agent_id, producer_type, COALESCE(idempotency_key, ''), COALESCE(payload_json, '{}'),
		 COALESCE(evidence_json, '[]'), COALESCE(upstream_artifact_ids_json, '[]'), COALESCE(supersedes_artifact_id, ''), version, created_at, updated_at
		 FROM coordination_artifacts
		 WHERE task_id = ?`
	args := []any{strings.TrimSpace(taskID)}
	if !includeDrafts {
		query += ` AND status <> ?`
		args = append(args, string(coordination.ArtifactStatusDraft))
	}
	query += ` ORDER BY updated_at DESC, created_at DESC`

	rows, err := s.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list coordination artifacts: %w", err)
	}
	defer rows.Close()

	var artifacts []coordination.Artifact
	for rows.Next() {
		artifact, err := scanArtifactRows(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, *artifact)
	}
	return artifacts, rows.Err()
}

func (s *CoordinationService) listReviews(ctx context.Context, taskID string) ([]coordination.Review, error) {
	rows, err := s.store.db.QueryContext(ctx,
		`SELECT id, task_id, artifact_id, requester_agent_id, requester_type, reviewer_type, summary,
		 COALESCE(criteria_json, '[]'), status, COALESCE(idempotency_key, ''), COALESCE(result_json, '{}'),
		 created_at, updated_at
		 FROM coordination_reviews
		 WHERE task_id = ?
		 ORDER BY updated_at DESC, created_at DESC`,
		strings.TrimSpace(taskID),
	)
	if err != nil {
		return nil, fmt.Errorf("list coordination reviews: %w", err)
	}
	defer rows.Close()

	var reviews []coordination.Review
	for rows.Next() {
		review, err := scanReviewRows(rows)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, *review)
	}
	return reviews, rows.Err()
}

func latestCoordinationTimestamp(
	claims []coordination.Claim,
	artifacts []coordination.Artifact,
	reviews []coordination.Review,
) time.Time {
	var latest time.Time
	for _, claim := range claims {
		if latest.IsZero() || claim.UpdatedAt.After(latest) {
			latest = claim.UpdatedAt
		}
	}
	for _, artifact := range artifacts {
		if latest.IsZero() || artifact.UpdatedAt.After(latest) {
			latest = artifact.UpdatedAt
		}
	}
	for _, review := range reviews {
		if latest.IsZero() || review.UpdatedAt.After(latest) {
			latest = review.UpdatedAt
		}
	}
	if latest.IsZero() {
		latest = time.Unix(0, 0).UTC()
	}
	return latest
}

func (s *CoordinationService) insertAction(
	ctx context.Context,
	tx bun.Tx,
	kind string,
	taskID string,
	taskName string,
	actor coordination.Actor,
	idempotencyKey string,
	payload any,
) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal coordination action payload: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO coordination_actions (id, task_id, task_name, kind, actor_agent_id, actor_type, idempotency_key, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(),
		strings.TrimSpace(taskID),
		strings.TrimSpace(taskName),
		kind,
		strings.TrimSpace(actor.AgentID),
		strings.TrimSpace(actor.AgentType),
		strings.TrimSpace(idempotencyKey),
		string(payloadJSON),
		time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("insert coordination action: %w", err)
	}
	return nil
}

func (s *CoordinationService) insertClaim(ctx context.Context, tx bun.Tx, claim *coordination.Claim) error {
	evidenceJSON, err := json.Marshal(claim.Evidence)
	if err != nil {
		return fmt.Errorf("marshal coordination claim evidence: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO coordination_claims
		 (id, task_id, task_name, scope_kind, scope_key, purpose, mode, state, owner_agent_id, owner_type, idempotency_key, evidence_json, lease_expires_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		claim.ID, claim.TaskID, claim.TaskName, string(claim.ScopeKind), claim.ScopeKey, claim.Purpose,
		string(claim.Mode), string(claim.State), claim.OwnerAgentID, claim.OwnerType, claim.IdempotencyKey, string(evidenceJSON),
		claim.LeaseExpiresAt, claim.CreatedAt, claim.UpdatedAt,
	); err != nil {
		return fmt.Errorf("insert coordination claim: %w", err)
	}
	return nil
}

func (s *CoordinationService) insertArtifact(ctx context.Context, tx bun.Tx, artifact *coordination.Artifact) error {
	payloadJSON, err := json.Marshal(artifact.Payload)
	if err != nil {
		return fmt.Errorf("marshal coordination artifact payload: %w", err)
	}
	evidenceJSON, err := json.Marshal(artifact.Evidence)
	if err != nil {
		return fmt.Errorf("marshal coordination artifact evidence: %w", err)
	}
	upstreamJSON, err := json.Marshal(artifact.UpstreamArtifactIDs)
	if err != nil {
		return fmt.Errorf("marshal coordination artifact upstream: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO coordination_artifacts
		 (id, task_id, task_name, kind, title, summary, scope_kind, scope_key, status, producer_agent_id, producer_type, idempotency_key, payload_json, evidence_json, upstream_artifact_ids_json, supersedes_artifact_id, version, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		artifact.ID, artifact.TaskID, artifact.TaskName, artifact.Kind, artifact.Title, artifact.Summary,
		nullIfEmpty(string(artifact.ScopeKind)), nullIfEmpty(artifact.ScopeKey), string(artifact.Status),
		artifact.ProducerAgentID, artifact.ProducerType, artifact.IdempotencyKey, string(payloadJSON), string(evidenceJSON), string(upstreamJSON),
		nullIfEmpty(artifact.SupersedesArtifactID), artifact.Version, artifact.CreatedAt, artifact.UpdatedAt,
	); err != nil {
		return fmt.Errorf("insert coordination artifact: %w", err)
	}
	return nil
}

func (s *CoordinationService) insertReview(ctx context.Context, tx bun.Tx, review *coordination.Review) error {
	criteriaJSON, err := json.Marshal(review.Criteria)
	if err != nil {
		return fmt.Errorf("marshal coordination review criteria: %w", err)
	}
	resultJSON, err := json.Marshal(review.Result)
	if err != nil {
		return fmt.Errorf("marshal coordination review result: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO coordination_reviews
		 (id, task_id, artifact_id, requester_agent_id, requester_type, reviewer_type, summary, criteria_json, status, idempotency_key, result_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		review.ID, review.TaskID, review.ArtifactID, review.RequesterAgentID, review.RequesterType,
		review.ReviewerType, review.Summary, string(criteriaJSON), string(review.Status), review.IdempotencyKey,
		string(resultJSON), review.CreatedAt, review.UpdatedAt,
	); err != nil {
		return fmt.Errorf("insert coordination review: %w", err)
	}
	return nil
}

func scanClaim(row *sql.Row) (*coordination.Claim, error) {
	var evidenceJSON string
	claim := &coordination.Claim{}
	err := row.Scan(
		&claim.ID, &claim.TaskID, &claim.TaskName, &claim.ScopeKind, &claim.ScopeKey, &claim.Purpose,
		&claim.Mode, &claim.State, &claim.OwnerAgentID, &claim.OwnerType, &claim.IdempotencyKey, &evidenceJSON,
		&claim.CreatedAt, &claim.UpdatedAt, &claim.LeaseExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &claim.Evidence); err != nil {
		claim.Evidence = nil
	}
	return claim, nil
}

func scanClaimRows(rows *sql.Rows) (*coordination.Claim, error) {
	var evidenceJSON string
	claim := &coordination.Claim{}
	if err := rows.Scan(
		&claim.ID, &claim.TaskID, &claim.TaskName, &claim.ScopeKind, &claim.ScopeKey, &claim.Purpose,
		&claim.Mode, &claim.State, &claim.OwnerAgentID, &claim.OwnerType, &claim.IdempotencyKey, &evidenceJSON,
		&claim.CreatedAt, &claim.UpdatedAt, &claim.LeaseExpiresAt,
	); err != nil {
		return nil, fmt.Errorf("scan coordination claim: %w", err)
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &claim.Evidence); err != nil {
		claim.Evidence = nil
	}
	return claim, nil
}

func scanArtifact(row *sql.Row) (*coordination.Artifact, error) {
	var payloadJSON, evidenceJSON, upstreamJSON string
	artifact := &coordination.Artifact{}
	err := row.Scan(
		&artifact.ID, &artifact.TaskID, &artifact.TaskName, &artifact.Kind, &artifact.Title, &artifact.Summary,
		&artifact.ScopeKind, &artifact.ScopeKey, &artifact.Status, &artifact.ProducerAgentID, &artifact.ProducerType,
		&artifact.IdempotencyKey, &payloadJSON, &evidenceJSON, &upstreamJSON, &artifact.SupersedesArtifactID,
		&artifact.Version, &artifact.CreatedAt, &artifact.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	decodeArtifactJSON(artifact, payloadJSON, evidenceJSON, upstreamJSON)
	return artifact, nil
}

func scanArtifactRows(rows *sql.Rows) (*coordination.Artifact, error) {
	var payloadJSON, evidenceJSON, upstreamJSON string
	artifact := &coordination.Artifact{}
	if err := rows.Scan(
		&artifact.ID, &artifact.TaskID, &artifact.TaskName, &artifact.Kind, &artifact.Title, &artifact.Summary,
		&artifact.ScopeKind, &artifact.ScopeKey, &artifact.Status, &artifact.ProducerAgentID, &artifact.ProducerType,
		&artifact.IdempotencyKey, &payloadJSON, &evidenceJSON, &upstreamJSON, &artifact.SupersedesArtifactID,
		&artifact.Version, &artifact.CreatedAt, &artifact.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan coordination artifact: %w", err)
	}
	decodeArtifactJSON(artifact, payloadJSON, evidenceJSON, upstreamJSON)
	return artifact, nil
}

func decodeArtifactJSON(artifact *coordination.Artifact, payloadJSON, evidenceJSON, upstreamJSON string) {
	if err := json.Unmarshal([]byte(payloadJSON), &artifact.Payload); err != nil {
		artifact.Payload = nil
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &artifact.Evidence); err != nil {
		artifact.Evidence = nil
	}
	if err := json.Unmarshal([]byte(upstreamJSON), &artifact.UpstreamArtifactIDs); err != nil {
		artifact.UpstreamArtifactIDs = nil
	}
}

func scanReview(row *sql.Row) (*coordination.Review, error) {
	var criteriaJSON, resultJSON string
	review := &coordination.Review{}
	err := row.Scan(
		&review.ID, &review.TaskID, &review.ArtifactID, &review.RequesterAgentID, &review.RequesterType,
		&review.ReviewerType, &review.Summary, &criteriaJSON, &review.Status, &review.IdempotencyKey,
		&resultJSON, &review.CreatedAt, &review.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	decodeReviewJSON(review, criteriaJSON, resultJSON)
	return review, nil
}

func scanReviewRows(rows *sql.Rows) (*coordination.Review, error) {
	var criteriaJSON, resultJSON string
	review := &coordination.Review{}
	if err := rows.Scan(
		&review.ID, &review.TaskID, &review.ArtifactID, &review.RequesterAgentID, &review.RequesterType,
		&review.ReviewerType, &review.Summary, &criteriaJSON, &review.Status, &review.IdempotencyKey,
		&resultJSON, &review.CreatedAt, &review.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan coordination review: %w", err)
	}
	decodeReviewJSON(review, criteriaJSON, resultJSON)
	return review, nil
}

func decodeReviewJSON(review *coordination.Review, criteriaJSON, resultJSON string) {
	if err := json.Unmarshal([]byte(criteriaJSON), &review.Criteria); err != nil {
		review.Criteria = nil
	}
	if err := json.Unmarshal([]byte(resultJSON), &review.Result); err != nil {
		review.Result = nil
	}
}

func cloneMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneEvidence(values []coordination.EvidenceRef) []coordination.EvidenceRef {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]coordination.EvidenceRef, len(values))
	copy(cloned, values)
	return cloned
}

func firstNonEmptyTaskName(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func (s *CoordinationService) viewCacheKey(taskID string, includeDrafts bool) string {
	return fmt.Sprintf("coord:view:%s:%t", strings.TrimSpace(taskID), includeDrafts)
}

func (s *CoordinationService) packetCacheKey(taskID, workerType string, version int64) string {
	return fmt.Sprintf("coord:packet:%s:%s:%d", strings.TrimSpace(taskID), strings.TrimSpace(workerType), version)
}

func (s *CoordinationService) precedentCacheKey(taskName, taskSlug, workerType string) string {
	key := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(taskName),
		strings.TrimSpace(taskSlug),
		strings.TrimSpace(workerType),
	}, "|"))
	key = strings.Trim(key, "|")
	if key == "" {
		return ""
	}
	return "coord:precedent:" + key
}

func (s *CoordinationService) invalidateTask(taskID string) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || s.cache == nil {
		return
	}
	s.cache.Del(s.viewCacheKey(taskID, false))
	s.cache.Del(s.viewCacheKey(taskID, true))
	for _, workerType := range []string{"inspector-pipeline", "tester-pipeline", "engineer", "designer"} {
		// packet keys are versioned; dropping common prefixes via view invalidation
		// is sufficient because packets are keyed on the old view version.
		s.cache.Del(fmt.Sprintf("coord:packet:%s:%s", taskID, workerType))
	}
	s.notifyTaskWatchers(taskID)
}

func (s *CoordinationService) emitArchival(ctx context.Context, event *coordination.ArchivalEvent) {
	if s == nil || s.archiveEvent == nil || event == nil {
		return
	}
	s.archiveEvent(ctx, event)
}

func (s *CoordinationService) registerWatcher(taskID string) chan struct{} {
	ch := make(chan struct{}, 1)
	if s == nil || strings.TrimSpace(taskID) == "" {
		return ch
	}
	s.watchersMu.Lock()
	defer s.watchersMu.Unlock()
	taskID = strings.TrimSpace(taskID)
	taskWatchers := s.watchers[taskID]
	if taskWatchers == nil {
		taskWatchers = make(map[chan struct{}]struct{})
		s.watchers[taskID] = taskWatchers
	}
	taskWatchers[ch] = struct{}{}
	return ch
}

func (s *CoordinationService) unregisterWatcher(taskID string, ch chan struct{}) {
	if s == nil || strings.TrimSpace(taskID) == "" || ch == nil {
		return
	}
	s.watchersMu.Lock()
	defer s.watchersMu.Unlock()
	taskID = strings.TrimSpace(taskID)
	if taskWatchers := s.watchers[taskID]; taskWatchers != nil {
		delete(taskWatchers, ch)
		if len(taskWatchers) == 0 {
			delete(s.watchers, taskID)
		}
	}
}

func (s *CoordinationService) notifyTaskWatchers(taskID string) {
	if s == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	s.watchersMu.Lock()
	taskWatchers := s.watchers[strings.TrimSpace(taskID)]
	channels := make([]chan struct{}, 0, len(taskWatchers))
	for ch := range taskWatchers {
		channels = append(channels, ch)
	}
	s.watchersMu.Unlock()
	for _, ch := range channels {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func coordinationContractForWorker(workerType string) *coordination.CoordinationContract {
	switch strings.TrimSpace(workerType) {
	case "engineer":
		return &coordination.CoordinationContract{
			WorkerType:          "engineer",
			Summary:             "Claim concrete implementation scope before editing. Reuse upstream artifacts and watch for peer follow-up when coordination is active.",
			MustClaimBeforeWork: true,
			MinimumClaims:       1,
			WatchForPeerUpdates: true,
		}
	case "designer":
		return &coordination.CoordinationContract{
			WorkerType:             "designer",
			Summary:                "Claim UX/component scope before working and publish at least one design artifact so peers can review or reuse it.",
			MustClaimBeforeWork:    true,
			MinimumClaims:          1,
			MinimumArtifacts:       1,
			PreferredArtifactKinds: []string{"ux_contract", "design_decision", "design_validation_request"},
			WatchForPeerUpdates:    true,
		}
	case "inspector-pipeline":
		return &coordination.CoordinationContract{
			WorkerType:             "inspector-pipeline",
			Summary:                "Claim the investigation surface, then publish at least one inspection artifact so downstream workers can reuse the risk framing.",
			MustClaimBeforeWork:    true,
			MinimumClaims:          1,
			MinimumArtifacts:       1,
			PreferredArtifactKinds: []string{"risk_map", "quality_findings", "invariant_set", "design_validation_request"},
			WatchForPeerUpdates:    true,
		}
	case "tester-pipeline":
		return &coordination.CoordinationContract{
			WorkerType:             "tester-pipeline",
			Summary:                "Claim the test surface, then publish at least one verification artifact so engineer or designer can act on concrete findings.",
			MustClaimBeforeWork:    true,
			MinimumClaims:          1,
			MinimumArtifacts:       1,
			PreferredArtifactKinds: []string{"test_plan", "verification_result"},
			WatchForPeerUpdates:    true,
		}
	default:
		return nil
	}
}
