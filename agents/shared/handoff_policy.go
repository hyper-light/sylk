package shared

import (
	"context"

	"github.com/adalundhe/sylk/agents/guide"
)

type automaticHandoffContextKey struct{}

// WithAutomaticHandoffEnabled stamps whether automatic context handoff is
// permitted for the current request.
func WithAutomaticHandoffEnabled(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, automaticHandoffContextKey{}, enabled)
}

// AutomaticHandoffEnabled reports whether automatic context handoff is allowed
// for the current request. The default is true.
func AutomaticHandoffEnabled(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	enabled, ok := ctx.Value(automaticHandoffContextKey{}).(bool)
	if !ok {
		return true
	}
	return enabled
}

// HasInterAgentBranchMetadata reports whether the metadata identifies a nested
// consult/challenge/approval/store child branch.
func HasInterAgentBranchMetadata(metadata map[string]any) bool {
	_, ok := interAgentBranchMetadataFromMetadata(metadata)
	return ok
}

// AutomaticHandoffAllowedForForwardedRequest reports whether a forwarded
// request should be eligible for automatic context handoff. Nested child
// consult/challenge/approval/store work must stay on the original correlation and
// should complete or fail in place rather than handing off.
func AutomaticHandoffAllowedForForwardedRequest(fwd *guide.ForwardedRequest) bool {
	if fwd == nil {
		return true
	}
	return !HasInterAgentBranchMetadata(fwd.Metadata)
}
