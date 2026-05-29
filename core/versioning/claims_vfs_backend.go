package versioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
)

type ClaimsVFSBackendConfig struct {
	Session    *SessionVFS
	Manager    VFSManager
	WorkingDir string
	AgentID    string
	AgentRole  string
}

type ClaimsVFSBackend struct {
	session    *SessionVFS
	manager    VFSManager
	workingDir string
	agentID    string
	agentRole  string
}

type claimsVFSArgs struct {
	PipelineID string   `json:"pipeline_id,omitempty"`
	SessionID  string   `json:"session_id,omitempty"`
	WorkingDir string   `json:"working_dir,omitempty"`
	AgentID    string   `json:"agent_id,omitempty"`
	AgentRole  string   `json:"agent_role,omitempty"`
	Files      []string `json:"files,omitempty"`
}

func NewClaimsVFSBackend(cfg ClaimsVFSBackendConfig) *ClaimsVFSBackend {
	return &ClaimsVFSBackend{
		session:    cfg.Session,
		manager:    cfg.Manager,
		workingDir: strings.TrimSpace(cfg.WorkingDir),
		agentID:    strings.TrimSpace(cfg.AgentID),
		agentRole:  strings.TrimSpace(cfg.AgentRole),
	}
}

func (b *ClaimsVFSBackend) HandleVFSOperation(ctx context.Context, req claims.VFSOperationRequest) (claims.VFSOperationArtifactData, error) {
	data := req.Requested
	if b == nil {
		data.FailureReason = "VFS backend not configured"
		return data, nil
	}
	args, err := decodeClaimsVFSArgs(req.Call.Arguments)
	if err != nil {
		data.FailureReason = err.Error()
		return data, nil
	}
	switch data.Operation {
	case claims.VFSProvisionerToolProvision, claims.VFSProvisionerToolAttach:
		return b.begin(data, args), nil
	case claims.VFSProvisionerToolDetach:
		return b.detach(data, args), nil
	case claims.VFSProvisionerToolMerge, claims.VFSProvisionerToolPromote:
		return b.merge(ctx, data, args), nil
	case claims.VFSProvisionerToolRollback:
		return b.rollback(data, args), nil
	default:
		return b.snapshot(data), nil
	}
}

func (b *ClaimsVFSBackend) begin(data claims.VFSOperationArtifactData, args claimsVFSArgs) claims.VFSOperationArtifactData {
	pipelineID := claimsVFSPipelineID(data, args)
	if pipelineID == "" {
		data.FailureReason = "pipeline id is required"
		return data
	}
	if b.session != nil {
		_, err := b.session.BeginPipeline(beginPipelineConfig(data, args, pipelineID, b))
		return b.afterPipelineResult(data, err)
	}
	manager := b.vfsManager()
	if manager == nil {
		data.FailureReason = "VFS manager not configured"
		return data
	}
	_, err := manager.CreatePipelineVFS(vfsConfig(data, args, pipelineID, b))
	return b.afterPipelineResult(data, err)
}

func (b *ClaimsVFSBackend) detach(data claims.VFSOperationArtifactData, args claimsVFSArgs) claims.VFSOperationArtifactData {
	pipelineID := claimsVFSPipelineID(data, args)
	if pipelineID == "" {
		data.FailureReason = "pipeline id is required"
		return data
	}
	manager := b.vfsManager()
	if manager == nil {
		data.FailureReason = "VFS manager not configured"
		return data
	}
	if err := manager.ClosePipelineVFS(pipelineID); err != nil {
		data.FailureReason = err.Error()
	}
	data.Attached = false
	return b.snapshot(data)
}

func (b *ClaimsVFSBackend) merge(ctx context.Context, data claims.VFSOperationArtifactData, args claimsVFSArgs) claims.VFSOperationArtifactData {
	if b.session == nil {
		data.FailureReason = "session VFS not configured"
		return data
	}
	result, err := b.session.MergePipelineIntoGreen(ctx, claimsVFSPipelineID(data, args), PipelineInspectorCertificate{})
	if err != nil {
		data.FailureReason = err.Error()
		return data
	}
	data.ResultingVersion = result.MergedVersion.String()
	data.Attached = false
	return data
}

func (b *ClaimsVFSBackend) rollback(data claims.VFSOperationArtifactData, args claimsVFSArgs) claims.VFSOperationArtifactData {
	pipelineID := claimsVFSPipelineID(data, args)
	if b.session != nil {
		_, err := b.session.RollbackPipelineIfTracked(pipelineID)
		return b.afterPipelineResult(data, err)
	}
	manager := b.vfsManager()
	if manager == nil {
		data.FailureReason = "VFS manager not configured"
		return data
	}
	if err := manager.ClosePipelineVFS(pipelineID); err != nil {
		data.FailureReason = err.Error()
	}
	data.Attached = false
	return data
}

func (b *ClaimsVFSBackend) snapshot(data claims.VFSOperationArtifactData) claims.VFSOperationArtifactData {
	if b.session != nil {
		stats := b.session.Stats()
		data.ResultingVersion = firstClaimsVFSString(data.ResultingVersion, stats.CurrentVersion.String())
	}
	if manager := b.vfsManager(); manager != nil {
		stats := manager.Stats()
		data.Metadata = mergeClaimsVFSMetadata(data.Metadata, map[string]any{"active_vfses": stats.ActiveVFSes, "total_pipelines": stats.TotalPipelines})
	}
	return data
}

func (b *ClaimsVFSBackend) afterPipelineResult(data claims.VFSOperationArtifactData, err error) claims.VFSOperationArtifactData {
	if err != nil {
		data.FailureReason = err.Error()
		return data
	}
	data.Attached = data.Operation != claims.VFSProvisionerToolDetach && data.Operation != claims.VFSProvisionerToolRollback
	return b.snapshot(data)
}

func (b *ClaimsVFSBackend) vfsManager() VFSManager {
	if b.manager != nil {
		return b.manager
	}
	if b.session != nil {
		return b.session.vfsManager
	}
	return nil
}

func beginPipelineConfig(data claims.VFSOperationArtifactData, args claimsVFSArgs, pipelineID string, b *ClaimsVFSBackend) BeginPipelineConfig {
	return BeginPipelineConfig{
		PipelineID: pipelineID,
		SessionID:  SessionID(firstClaimsVFSString(args.SessionID)),
		AgentID:    firstClaimsVFSString(args.AgentID, b.agentID),
		AgentRole:  firstClaimsVFSString(args.AgentRole, b.agentRole),
		WorkingDir: firstClaimsVFSString(args.WorkingDir, b.workingDir),
		Files:      append([]string(nil), args.Files...),
	}
}

func vfsConfig(data claims.VFSOperationArtifactData, args claimsVFSArgs, pipelineID string, b *ClaimsVFSBackend) VFSConfig {
	return VFSConfig{
		PipelineID:   pipelineID,
		SessionID:    SessionID(firstClaimsVFSString(args.SessionID)),
		WorkingDir:   firstClaimsVFSString(args.WorkingDir, b.workingDir),
		AllowedPaths: append([]string(nil), args.Files...),
		ReadOnly:     data.ReadOnly,
		AgentID:      firstClaimsVFSString(args.AgentID, b.agentID),
		AgentRole:    firstClaimsVFSString(args.AgentRole, b.agentRole),
	}
}

func claimsVFSPipelineID(data claims.VFSOperationArtifactData, args claimsVFSArgs) string {
	if strings.TrimSpace(args.PipelineID) != "" {
		return strings.TrimSpace(args.PipelineID)
	}
	parts := strings.Split(strings.TrimSpace(data.MountRef), ":")
	for idx := len(parts) - 1; idx >= 0; idx-- {
		if strings.TrimSpace(parts[idx]) != "" {
			return strings.TrimSpace(parts[idx])
		}
	}
	return ""
}

func decodeClaimsVFSArgs(args map[string]any) (claimsVFSArgs, error) {
	var out claimsVFSArgs
	raw, err := json.Marshal(args)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode VFS claims args: %w", err)
	}
	return out, nil
}

func mergeClaimsVFSMetadata(primary, fallback map[string]any) map[string]any {
	out := make(map[string]any, len(primary)+len(fallback))
	for key, value := range fallback {
		out[key] = value
	}
	for key, value := range primary {
		out[key] = value
	}
	return out
}

func firstClaimsVFSString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
