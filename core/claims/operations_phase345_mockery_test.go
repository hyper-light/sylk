package claims_test

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
	claimsmocks "github.com/adalundhe/sylk/core/claims/mocks"
	"github.com/stretchr/testify/mock"
)

func TestStartClaimsOperationsAuditsSchedulesUnderMockeryScope(t *testing.T) {
	scope := &claimsmocks.ScopeProvider{}
	scope.On("Go", "claims_operations_audit", mock.Anything, mock.Anything).Return(nil).Once()

	if err := claims.StartClaimsOperationsAudits(context.Background(), scope, claims.OperationsAuditOptions{}); err != nil {
		t.Fatalf("StartClaimsOperationsAudits: %v", err)
	}
	scope.AssertExpectations(t)
}
