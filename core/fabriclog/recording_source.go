package fabriclog

import (
	"context"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// RecordingSource wraps an activity.Source and emits KindRead + one
// KindConsume per returned activity for every lens call. The
// underlying source is always invoked; observability is additive.
//
// Install via NewRecordingSource and activity.SetDefaultSource.
// When the logger is nil the decorator is a transparent passthrough.
type RecordingSource struct {
	inner  activity.Source
	logger *FabricLogger
}

// NewRecordingSource wraps source with observability. If logger is
// nil, the returned decorator behaves exactly like source. Unlike
// NewRecordingSink, source MUST NOT be nil: a nil Source cannot
// satisfy the interface at all.
func NewRecordingSource(source activity.Source, logger *FabricLogger) *RecordingSource {
	return &RecordingSource{inner: source, logger: logger}
}

// FilterActivities records a KindRead record plus one KindConsume
// per returned activity before returning. Err and elapsed time are
// captured on the KindRead so analysis can spot slow or failing
// lens invocations.
func (r *RecordingSource) FilterActivities(ctx context.Context, filter activity.QueryFilter) ([]activity.AgentActivity, error) {
	start := time.Now()
	result, err := r.inner.FilterActivities(ctx, filter)
	elapsed := time.Since(start)

	if r.logger == nil {
		return result, err
	}
	// Lift QueryFilter into the portable FilterBody shape. This
	// intentionally omits confidences and a few rarely-populated
	// fields to keep the record shape stable across fabric schema
	// evolution — the raw filter is reconstructable from the log
	// plus the returned IDs anyway.
	body := filterBodyFrom(filter)
	returnedIDs := make([]string, 0, len(result))
	for _, a := range result {
		returnedIDs = append(returnedIDs, string(a.ID))
	}
	readerSeq := r.logger.RecordRead(
		ctx,
		"FilterActivities",
		aliasFromContext(ctx),
		body,
		returnedIDs,
		len(result),
		elapsed,
		err,
	)
	for i := range result {
		r.logger.RecordConsume(ctx, readerSeq, result[i])
	}
	return result, err
}

// GetActivity records one KindRead + one KindConsume (when the
// activity was found). A miss still writes a KindRead with
// ReturnedCount=0 so consumers can see failed point lookups.
func (r *RecordingSource) GetActivity(ctx context.Context, id activity.ActivityID) (*activity.AgentActivity, error) {
	start := time.Now()
	result, err := r.inner.GetActivity(ctx, id)
	elapsed := time.Since(start)

	if r.logger == nil {
		return result, err
	}
	var returnedIDs []string
	count := 0
	if result != nil {
		returnedIDs = []string{string(result.ID)}
		count = 1
	}
	body := &FilterBody{
		// Encode the point-lookup in a uniform shape: the "activity
		// ID" slot is expressed as Caused so filter-shape analysis
		// sees it without inventing a new field.
		Caused: string(id),
	}
	body.FilterHash = HashFilter(*body)
	readerSeq := r.logger.RecordRead(
		ctx,
		"GetActivity",
		aliasFromContext(ctx),
		body,
		returnedIDs,
		count,
		elapsed,
		err,
	)
	if result != nil {
		r.logger.RecordConsume(ctx, readerSeq, *result)
	}
	return result, err
}

// LatestActivityID records a KindRead with zero returned IDs (the
// call returns only the single latest ID, which is low-value to
// record as a consume). Elapsed time + filter shape are still
// captured.
func (r *RecordingSource) LatestActivityID(ctx context.Context, filter activity.QueryFilter) (activity.ActivityID, error) {
	start := time.Now()
	result, err := r.inner.LatestActivityID(ctx, filter)
	elapsed := time.Since(start)

	if r.logger == nil {
		return result, err
	}
	body := filterBodyFrom(filter)
	var returnedIDs []string
	count := 0
	if result != "" {
		returnedIDs = []string{string(result)}
		count = 1
	}
	_ = r.logger.RecordRead(
		ctx,
		"LatestActivityID",
		aliasFromContext(ctx),
		body,
		returnedIDs,
		count,
		elapsed,
		err,
	)
	return result, err
}

// Inner returns the wrapped source for teardown paths.
func (r *RecordingSource) Inner() activity.Source {
	if r == nil {
		return nil
	}
	return r.inner
}

// filterBodyFrom translates an activity.QueryFilter into a
// FilterBody suitable for JSON serialization, including a stable
// fingerprint hash.
func filterBodyFrom(f activity.QueryFilter) *FilterBody {
	kinds := make([]string, 0, len(f.ActionKinds))
	for _, k := range f.ActionKinds {
		kinds = append(kinds, string(k))
	}
	states := make([]string, 0, len(f.States))
	for _, s := range f.States {
		states = append(states, string(s))
	}
	body := &FilterBody{
		ActionKinds:        kinds,
		States:             states,
		ActorAgentID:       f.ActorAgentID,
		ActorAgentType:     f.ActorAgentType,
		ActorPipelineID:    f.ActorPipelineID,
		SubjectPathPrefix:  f.SubjectPathPrefix,
		SubjectTargetAgent: f.SubjectTargetAgent,
		SubjectDomain:      f.SubjectDomain,
		Since:              f.Since,
		Until:              f.Until,
		Limit:              f.Limit,
	}
	if f.Caused != nil {
		body.Caused = string(*f.Caused)
	}
	if f.Resolves != nil {
		body.Resolves = string(*f.Resolves)
	}
	body.FilterHash = HashFilter(*body)
	return body
}

// aliasCtxKey carries the semantic lens name (e.g.
// "WhatAreTheyDoing") through context so the RecordingSource can
// label a Source-level read with the higher-level lens that
// triggered it. Optional; zero value produces an empty LensAlias.
type aliasCtxKey struct{}

// WithLensAlias tags ctx with the semantic lens name that is about
// to call into Source. Lens packages (core/activity/lenses) should
// wrap ctx before calling Source so every underlying FilterActivities
// / GetActivity / LatestActivityID call inherits the alias.
func WithLensAlias(ctx context.Context, alias string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if alias == "" {
		return ctx
	}
	return context.WithValue(ctx, aliasCtxKey{}, alias)
}

func aliasFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(aliasCtxKey{}).(string); ok {
		return v
	}
	return ""
}
