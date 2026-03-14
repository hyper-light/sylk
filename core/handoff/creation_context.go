package handoff

import "context"

type factoryCreationContextKey string

const factoryCreationMetadataKey factoryCreationContextKey = "handoff_factory_creation_metadata"

// FactoryCreationMetadata carries session creation hints from handoff into the
// higher-level creator path without pulling container concerns into core/handoff.
type FactoryCreationMetadata struct {
	AgentType string
	AgentID   string
	TaskID    string
	TaskSlug  string
}

// WithFactoryCreationMetadata annotates ctx with factory creation metadata.
func WithFactoryCreationMetadata(ctx context.Context, metadata FactoryCreationMetadata) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, factoryCreationMetadataKey, metadata)
}

// FactoryCreationMetadataFromContext returns the handoff creation metadata, if present.
func FactoryCreationMetadataFromContext(ctx context.Context) (FactoryCreationMetadata, bool) {
	if ctx == nil {
		return FactoryCreationMetadata{}, false
	}
	metadata, ok := ctx.Value(factoryCreationMetadataKey).(FactoryCreationMetadata)
	return metadata, ok
}
