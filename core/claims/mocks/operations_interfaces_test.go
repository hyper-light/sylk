package mocks

import "github.com/adalundhe/sylk/core/claims"

func _() {
	var _ claims.DeltaPublisher = (*DeltaPublisher)(nil)
	var _ claims.DeltaSubscriber = (*DeltaSubscriber)(nil)
	var _ claims.DeltaBus = (*DeltaBus)(nil)
	var _ claims.DeltaSubscription = (*DeltaSubscription)(nil)
	var _ claims.ClaimsProjector = (*ClaimsProjector)(nil)
	var _ claims.AgentRefResolver = (*AgentRefResolver)(nil)
	var _ claims.ClaimPostPolicy = (*ClaimPostPolicy)(nil)
	var _ claims.ScopeProvider = (*ScopeProvider)(nil)
	var _ claims.ServiceHandler = (*ServiceHandler)(nil)
	var _ claims.ExpectedToolExecutor = (*ExpectedToolExecutor)(nil)
	var _ claims.ExpectedToolPolicy = (*ExpectedToolPolicy)(nil)
	var _ claims.ExpectedToolArgumentRedactor = (*ExpectedToolArgumentRedactor)(nil)
	var _ claims.ValidationExpectedToolRemediationPoster = (*ValidationExpectedToolRemediationPoster)(nil)
}
