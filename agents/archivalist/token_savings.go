package archivalist

func (a *Archivalist) GetTokenSavings(sessionID string) TokenSavingsReport {
	if a.queryCache == nil {
		return TokenSavingsReport{SessionID: sessionID}
	}

	stats := a.queryCache.StatsBySession(sessionID)
	return TokenSavingsReport{
		SessionID:        sessionID,
		CacheHits:        stats.CacheHits,
		CacheMisses:      stats.CacheMisses,
		CachedResponses:  stats.CachedResponses,
		CachedEmbeddings: stats.CachedEmbeddings,
	}
}

func (a *Archivalist) GetGlobalTokenSavings() TokenSavingsReport {
	if a.queryCache == nil {
		return TokenSavingsReport{}
	}
	stats := a.queryCache.Stats()
	return TokenSavingsReport{
		CacheHits:        stats.CacheHits,
		CacheMisses:      stats.CacheMisses,
		CachedResponses:  stats.CachedResponses,
		CachedEmbeddings: stats.CachedEmbeddings,
	}
}
