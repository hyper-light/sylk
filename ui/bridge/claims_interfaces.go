package bridge

import (
	"context"

	"github.com/adalundhe/sylk/core/claims"
)

//go:generate mockery --name=ClaimPresentationSink --output=./mocks --outpkg=mocks
//go:generate mockery --name=ClaimArtifactSink --output=./mocks --outpkg=mocks

// ClaimPresentationSink is the bridge boundary that receives user-visible
// claim artifacts after claims visibility filtering has already run.
type ClaimPresentationSink interface {
	PublishClaimPresentation(ctx context.Context, claimID string, artifact *claims.Artifact) error
}

// ClaimArtifactSink is the bridge boundary for artifact lifecycle updates
// that affect cycle rows and artifact completion pairing.
type ClaimArtifactSink interface {
	PublishClaimArtifact(ctx context.Context, claimID string, artifact *claims.Artifact) error
}
