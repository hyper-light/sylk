package claims_test

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
	claimsmocks "github.com/adalundhe/sylk/core/claims/mocks"
	"github.com/stretchr/testify/mock"
)

func TestValidationDispatcherMockDrivesInterfaceOnlyUsage(t *testing.T) {
	dispatcher := &claimsmocks.ValidationDispatcher{}
	req := claims.ValidationDispatchRequest{
		Claim:      &claims.Claim{ID: "c"},
		Artifact:   &claims.Artifact{ID: "a", ArtifactName: "plan"},
		Validation: &claims.Validation{ID: "v", TargetArtifactName: "plan"},
	}
	want := claims.ValidationDispatchResult{
		ValidationID: "v",
		Status:       claims.ValidationStatusValidated,
	}
	dispatcher.On("DispatchValidation", mock.Anything, req).Return(want, nil).Once()

	got, err := useValidationDispatcher(context.Background(), dispatcher, req)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got.Status != claims.ValidationStatusValidated {
		t.Fatalf("status = %s", got.Status)
	}
	dispatcher.AssertExpectations(t)
}

func useValidationDispatcher(ctx context.Context, dispatcher claims.ValidationDispatcher, req claims.ValidationDispatchRequest) (claims.ValidationDispatchResult, error) {
	return dispatcher.DispatchValidation(ctx, req)
}
