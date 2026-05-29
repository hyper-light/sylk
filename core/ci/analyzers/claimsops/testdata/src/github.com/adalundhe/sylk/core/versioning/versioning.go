package versioning

import "context"

type SemanticVersion struct{}

type PipelineInspectorCertificate struct{}

type VFSConfig struct{}

type PipelineVFS struct{}

type MergePipelineResult struct{}

type SessionVFS struct{}

func (s *SessionVFS) BeginPipeline(cfg BeginPipelineConfig) (*PipelineVFS, error) {
	return nil, nil
}

func (s *SessionVFS) CommitPipeline(ctx context.Context, pipelineID string) (SemanticVersion, error) {
	return SemanticVersion{}, nil
}

func (s *SessionVFS) MergePipelineIntoGreen(ctx context.Context, pipelineID string, cert PipelineInspectorCertificate) (MergePipelineResult, error) {
	return MergePipelineResult{}, nil
}

func (s *SessionVFS) RollbackPipelineIfTracked(pipelineID string) (bool, error) {
	return false, nil
}

type BeginPipelineConfig struct{}

type VFSManager interface {
	CreatePipelineVFS(cfg VFSConfig) (*PipelineVFS, error)
}
