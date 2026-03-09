package container

import "context"

type creationContextKey string

const (
	creationSpecKey  creationContextKey = "container_creation_spec"
	creationPodIDKey creationContextKey = "container_creation_pod_id"
)

// WithCreationContext annotates ctx with the spec/pod metadata for agent factories.
func WithCreationContext(ctx context.Context, spec ContainerSpec, podID PodID) context.Context {
	ctx = context.WithValue(ctx, creationSpecKey, spec)
	if podID != "" {
		ctx = context.WithValue(ctx, creationPodIDKey, podID)
	}
	return ctx
}

// CreationSpecFromContext returns the current container spec, if available.
func CreationSpecFromContext(ctx context.Context) (ContainerSpec, bool) {
	if ctx == nil {
		return ContainerSpec{}, false
	}
	spec, ok := ctx.Value(creationSpecKey).(ContainerSpec)
	return spec, ok
}

// CreationPodIDFromContext returns the owning pod ID, if available.
func CreationPodIDFromContext(ctx context.Context) (PodID, bool) {
	if ctx == nil {
		return "", false
	}
	podID, ok := ctx.Value(creationPodIDKey).(PodID)
	return podID, ok
}
