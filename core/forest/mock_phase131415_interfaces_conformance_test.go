package forest

func _() {
	var _ NeighborIndex = (*MockNeighborIndex)(nil)
	var _ TextSearcher = (*MockTextSearcher)(nil)
	var _ ForestProposalSink = (*MockForestProposalSink)(nil)
	var _ ClaimProposalSink = (*MockForestProposalSink)(nil)
	var _ SkillArtifactWriter = (*MockSkillArtifactWriter)(nil)
	var _ PolicyOutcomeSource = (*MockPolicyOutcomeSource)(nil)
	var _ AgentTurnProvider = (*MockAgentTurnProvider)(nil)
	var _ GuardianApprover = (*MockGuardianApprover)(nil)
}
