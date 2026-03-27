package providers

type StreamProvider interface {
	Provider
	StreamHandlerProvider
}

// NativeWebSearchEvidenceProvider reports whether a provider can surface
// native web-search evidence through ProviderMetadata/stream chunks.
type NativeWebSearchEvidenceProvider interface {
	SupportsNativeWebSearchEvidence() bool
}
