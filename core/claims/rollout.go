package claims

import (
	"os"
	"strings"
	"sync"
)

const (
	EnvDurableSessionClaims      = "SYLK_DURABLE_SESSION_CLAIMS"
	EnvClaimsOutbox              = "SYLK_CLAIMS_OUTBOX"
	EnvClaimsKnowledgeMirror     = "SYLK_CLAIMS_KNOWLEDGE_MIRROR"
	EnvRecallForwardCrossSession = "SYLK_RECALL_FORWARD_CROSS_SESSION"
	EnvScribeContinuityNarration = "SYLK_SCRIBE_CONTINUITY_NARRATION"
	EnvClaimsInfraDAG            = "SYLK_CLAIMS_INFRA_DAG"
	EnvClaimsInfraVFS            = "SYLK_CLAIMS_INFRA_VFS"
	EnvClaimsInfraToolRuntime    = "SYLK_CLAIMS_INFRA_TOOL_RUNTIME"
	EnvClaimsInfraKnowledge      = "SYLK_CLAIMS_INFRA_KNOWLEDGE"
	EnvClaimsInfraMemory         = "SYLK_CLAIMS_INFRA_MEMORY"
	EnvClaimsInfraDocument       = "SYLK_CLAIMS_INFRA_DOCUMENT"
	EnvClaimsInfraGuardian       = "SYLK_CLAIMS_INFRA_GUARDIAN"
	EnvClaimsInfraProvider       = "SYLK_CLAIMS_INFRA_PROVIDER"
	EnvClaimsInfraExternal       = "SYLK_CLAIMS_INFRA_EXTERNAL"
	EnvClaimsInfraActivation     = "SYLK_CLAIMS_INFRA_ACTIVATION"
	EnvClaimsInfraSession        = "SYLK_CLAIMS_INFRA_SESSION"
	EnvClaimsInfraFabric         = "SYLK_CLAIMS_INFRA_FABRIC"
	EnvClaimsInfraBus            = "SYLK_CLAIMS_INFRA_BUS"
)

type ProjectionRolloutMode string

const (
	ProjectionRolloutOff           ProjectionRolloutMode = "off"
	ProjectionRolloutShadow        ProjectionRolloutMode = "shadow"
	ProjectionRolloutAuthoritative ProjectionRolloutMode = "authoritative"
)

type InfrastructureRolloutMode string

const (
	InfrastructureRolloutDisabled InfrastructureRolloutMode = "disabled"
	InfrastructureRolloutShadow   InfrastructureRolloutMode = "shadow"
	InfrastructureRolloutEnabled  InfrastructureRolloutMode = "enabled"
)

type RolloutConfig struct {
	DurableSessionClaims      bool                      `json:"durable_session_claims"`
	ClaimsOutbox              bool                      `json:"claims_outbox"`
	ClaimsKnowledgeMirror     ProjectionRolloutMode     `json:"claims_knowledge_mirror"`
	RecallForwardCrossSession bool                      `json:"recall_forward_cross_session"`
	ScribeContinuityNarration bool                      `json:"scribe_continuity_narration"`
	ClaimsInfraDAG            InfrastructureRolloutMode `json:"claims_infra_dag"`
	ClaimsInfraVFS            InfrastructureRolloutMode `json:"claims_infra_vfs"`
	ClaimsInfraToolRuntime    InfrastructureRolloutMode `json:"claims_infra_tool_runtime"`
	ClaimsInfraKnowledge      InfrastructureRolloutMode `json:"claims_infra_knowledge"`
	ClaimsInfraMemory         InfrastructureRolloutMode `json:"claims_infra_memory"`
	ClaimsInfraDocument       InfrastructureRolloutMode `json:"claims_infra_document"`
	ClaimsInfraGuardian       InfrastructureRolloutMode `json:"claims_infra_guardian"`
	ClaimsInfraProvider       InfrastructureRolloutMode `json:"claims_infra_provider"`
	ClaimsInfraExternal       InfrastructureRolloutMode `json:"claims_infra_external"`
	ClaimsInfraActivation     InfrastructureRolloutMode `json:"claims_infra_activation"`
	ClaimsInfraSession        InfrastructureRolloutMode `json:"claims_infra_session"`
	ClaimsInfraFabric         InfrastructureRolloutMode `json:"claims_infra_fabric"`
	ClaimsInfraBus            InfrastructureRolloutMode `json:"claims_infra_bus"`
}

var defaultRolloutConfig = struct {
	mu  sync.RWMutex
	cfg RolloutConfig
}{
	cfg: DefaultRolloutConfig(),
}

func DefaultRolloutConfig() RolloutConfig {
	return RolloutConfig{
		DurableSessionClaims:      true,
		ClaimsOutbox:              true,
		ClaimsKnowledgeMirror:     ProjectionRolloutShadow,
		RecallForwardCrossSession: true,
		ScribeContinuityNarration: true,
		ClaimsInfraDAG:            InfrastructureRolloutEnabled,
		ClaimsInfraVFS:            InfrastructureRolloutEnabled,
		ClaimsInfraToolRuntime:    InfrastructureRolloutEnabled,
		ClaimsInfraKnowledge:      InfrastructureRolloutEnabled,
		ClaimsInfraMemory:         InfrastructureRolloutEnabled,
		ClaimsInfraDocument:       InfrastructureRolloutEnabled,
		ClaimsInfraGuardian:       InfrastructureRolloutEnabled,
		ClaimsInfraProvider:       InfrastructureRolloutEnabled,
		ClaimsInfraExternal:       InfrastructureRolloutEnabled,
		ClaimsInfraActivation:     InfrastructureRolloutEnabled,
		ClaimsInfraSession:        InfrastructureRolloutEnabled,
		ClaimsInfraFabric:         InfrastructureRolloutEnabled,
		ClaimsInfraBus:            InfrastructureRolloutEnabled,
	}
}

func SetDefaultRolloutConfig(cfg RolloutConfig) {
	defaultRolloutConfig.mu.Lock()
	defaultRolloutConfig.cfg = cfg.Normalized()
	defaultRolloutConfig.mu.Unlock()
}

func CurrentRolloutConfig() RolloutConfig {
	defaultRolloutConfig.mu.RLock()
	cfg := defaultRolloutConfig.cfg
	defaultRolloutConfig.mu.RUnlock()
	return cfg.Normalized()
}

func RolloutConfigFromEnv(getenv func(string) string) RolloutConfig {
	if getenv == nil {
		getenv = os.Getenv
	}
	cfg := DefaultRolloutConfig()
	cfg.DurableSessionClaims = boolEnv(getenv, EnvDurableSessionClaims, cfg.DurableSessionClaims)
	cfg.ClaimsOutbox = boolEnv(getenv, EnvClaimsOutbox, cfg.ClaimsOutbox)
	cfg.ClaimsKnowledgeMirror = projectionModeEnv(getenv, EnvClaimsKnowledgeMirror, cfg.ClaimsKnowledgeMirror)
	cfg.RecallForwardCrossSession = boolEnv(getenv, EnvRecallForwardCrossSession, cfg.RecallForwardCrossSession)
	cfg.ScribeContinuityNarration = boolEnv(getenv, EnvScribeContinuityNarration, cfg.ScribeContinuityNarration)
	cfg.ClaimsInfraDAG = infrastructureModeEnv(getenv, EnvClaimsInfraDAG, cfg.ClaimsInfraDAG)
	cfg.ClaimsInfraVFS = infrastructureModeEnv(getenv, EnvClaimsInfraVFS, cfg.ClaimsInfraVFS)
	cfg.ClaimsInfraToolRuntime = infrastructureModeEnv(getenv, EnvClaimsInfraToolRuntime, cfg.ClaimsInfraToolRuntime)
	cfg.ClaimsInfraKnowledge = infrastructureModeEnv(getenv, EnvClaimsInfraKnowledge, cfg.ClaimsInfraKnowledge)
	cfg.ClaimsInfraMemory = infrastructureModeEnv(getenv, EnvClaimsInfraMemory, cfg.ClaimsInfraMemory)
	cfg.ClaimsInfraDocument = infrastructureModeEnv(getenv, EnvClaimsInfraDocument, cfg.ClaimsInfraDocument)
	cfg.ClaimsInfraGuardian = infrastructureModeEnv(getenv, EnvClaimsInfraGuardian, cfg.ClaimsInfraGuardian)
	cfg.ClaimsInfraProvider = infrastructureModeEnv(getenv, EnvClaimsInfraProvider, cfg.ClaimsInfraProvider)
	cfg.ClaimsInfraExternal = infrastructureModeEnv(getenv, EnvClaimsInfraExternal, cfg.ClaimsInfraExternal)
	cfg.ClaimsInfraActivation = infrastructureModeEnv(getenv, EnvClaimsInfraActivation, cfg.ClaimsInfraActivation)
	cfg.ClaimsInfraSession = infrastructureModeEnv(getenv, EnvClaimsInfraSession, cfg.ClaimsInfraSession)
	cfg.ClaimsInfraFabric = infrastructureModeEnv(getenv, EnvClaimsInfraFabric, cfg.ClaimsInfraFabric)
	cfg.ClaimsInfraBus = infrastructureModeEnv(getenv, EnvClaimsInfraBus, cfg.ClaimsInfraBus)
	return cfg.Normalized()
}

func RolloutConfigFromEnvironment() RolloutConfig {
	return RolloutConfigFromEnv(os.Getenv)
}

func (cfg RolloutConfig) Normalized() RolloutConfig {
	cfg.ClaimsKnowledgeMirror = normalizeProjectionRolloutMode(cfg.ClaimsKnowledgeMirror)
	cfg.ClaimsInfraDAG = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraDAG)
	cfg.ClaimsInfraVFS = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraVFS)
	cfg.ClaimsInfraToolRuntime = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraToolRuntime)
	cfg.ClaimsInfraKnowledge = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraKnowledge)
	cfg.ClaimsInfraMemory = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraMemory)
	cfg.ClaimsInfraDocument = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraDocument)
	cfg.ClaimsInfraGuardian = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraGuardian)
	cfg.ClaimsInfraProvider = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraProvider)
	cfg.ClaimsInfraExternal = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraExternal)
	cfg.ClaimsInfraActivation = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraActivation)
	cfg.ClaimsInfraSession = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraSession)
	cfg.ClaimsInfraFabric = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraFabric)
	cfg.ClaimsInfraBus = normalizeInfrastructureRolloutMode(cfg.ClaimsInfraBus)
	return cfg
}

func boardRolloutConfig(cfg RolloutConfig) RolloutConfig {
	if cfg == (RolloutConfig{}) {
		return CurrentRolloutConfig()
	}
	return cfg.Normalized()
}

func (cfg RolloutConfig) ClaimsKnowledgeMirrorEnabled() bool {
	return normalizeProjectionRolloutMode(cfg.ClaimsKnowledgeMirror) != ProjectionRolloutOff
}

func (cfg RolloutConfig) ProjectionWarningsAuthoritative() bool {
	return normalizeProjectionRolloutMode(cfg.ClaimsKnowledgeMirror) == ProjectionRolloutAuthoritative
}

func (cfg RolloutConfig) InfrastructureMode(subsystem string) InfrastructureRolloutMode {
	cfg = cfg.Normalized()
	switch strings.TrimSpace(subsystem) {
	case "dag", "dag_processor":
		return cfg.ClaimsInfraDAG
	case "vfs", "vfs_provisioner":
		return cfg.ClaimsInfraVFS
	case "tool", "tool_runtime":
		return cfg.ClaimsInfraToolRuntime
	case "knowledge", "knowledge_graph":
		return cfg.ClaimsInfraKnowledge
	case "memory", "memory_continuity":
		return cfg.ClaimsInfraMemory
	case "document", "document_db":
		return cfg.ClaimsInfraDocument
	case "guardian":
		return cfg.ClaimsInfraGuardian
	case "provider", "provider_gateway":
		return cfg.ClaimsInfraProvider
	case "external", "external_adapter":
		return cfg.ClaimsInfraExternal
	case "activation", "activation_controller":
		return cfg.ClaimsInfraActivation
	case "session", "session_manager":
		return cfg.ClaimsInfraSession
	case "fabric", "fabric_subscriber":
		return cfg.ClaimsInfraFabric
	case "bus", "bus_administrator":
		return cfg.ClaimsInfraBus
	default:
		return InfrastructureRolloutDisabled
	}
}

func (cfg RolloutConfig) InfrastructureDispatchEnabled(subsystem string) bool {
	mode := cfg.InfrastructureMode(subsystem)
	return mode == InfrastructureRolloutEnabled || mode == InfrastructureRolloutShadow
}

func (cfg RolloutConfig) FeatureFlags() map[string]string {
	cfg = cfg.Normalized()
	return map[string]string{
		EnvDurableSessionClaims:      boolString(cfg.DurableSessionClaims),
		EnvClaimsOutbox:              boolString(cfg.ClaimsOutbox),
		EnvClaimsKnowledgeMirror:     string(cfg.ClaimsKnowledgeMirror),
		EnvRecallForwardCrossSession: boolString(cfg.RecallForwardCrossSession),
		EnvScribeContinuityNarration: boolString(cfg.ScribeContinuityNarration),
		EnvClaimsInfraDAG:            string(cfg.ClaimsInfraDAG),
		EnvClaimsInfraVFS:            string(cfg.ClaimsInfraVFS),
		EnvClaimsInfraToolRuntime:    string(cfg.ClaimsInfraToolRuntime),
		EnvClaimsInfraKnowledge:      string(cfg.ClaimsInfraKnowledge),
		EnvClaimsInfraMemory:         string(cfg.ClaimsInfraMemory),
		EnvClaimsInfraDocument:       string(cfg.ClaimsInfraDocument),
		EnvClaimsInfraGuardian:       string(cfg.ClaimsInfraGuardian),
		EnvClaimsInfraProvider:       string(cfg.ClaimsInfraProvider),
		EnvClaimsInfraExternal:       string(cfg.ClaimsInfraExternal),
		EnvClaimsInfraActivation:     string(cfg.ClaimsInfraActivation),
		EnvClaimsInfraSession:        string(cfg.ClaimsInfraSession),
		EnvClaimsInfraFabric:         string(cfg.ClaimsInfraFabric),
		EnvClaimsInfraBus:            string(cfg.ClaimsInfraBus),
	}
}

func (cfg RolloutConfig) Diagnostics() []string {
	flags := cfg.FeatureFlags()
	return []string{
		"rollout " + EnvDurableSessionClaims + "=" + flags[EnvDurableSessionClaims],
		"rollout " + EnvClaimsOutbox + "=" + flags[EnvClaimsOutbox],
		"rollout " + EnvClaimsKnowledgeMirror + "=" + flags[EnvClaimsKnowledgeMirror],
		"rollout " + EnvRecallForwardCrossSession + "=" + flags[EnvRecallForwardCrossSession],
		"rollout " + EnvScribeContinuityNarration + "=" + flags[EnvScribeContinuityNarration],
		"rollout " + EnvClaimsInfraDAG + "=" + flags[EnvClaimsInfraDAG],
		"rollout " + EnvClaimsInfraVFS + "=" + flags[EnvClaimsInfraVFS],
		"rollout " + EnvClaimsInfraToolRuntime + "=" + flags[EnvClaimsInfraToolRuntime],
		"rollout " + EnvClaimsInfraKnowledge + "=" + flags[EnvClaimsInfraKnowledge],
		"rollout " + EnvClaimsInfraMemory + "=" + flags[EnvClaimsInfraMemory],
		"rollout " + EnvClaimsInfraDocument + "=" + flags[EnvClaimsInfraDocument],
		"rollout " + EnvClaimsInfraGuardian + "=" + flags[EnvClaimsInfraGuardian],
		"rollout " + EnvClaimsInfraProvider + "=" + flags[EnvClaimsInfraProvider],
		"rollout " + EnvClaimsInfraExternal + "=" + flags[EnvClaimsInfraExternal],
		"rollout " + EnvClaimsInfraActivation + "=" + flags[EnvClaimsInfraActivation],
		"rollout " + EnvClaimsInfraSession + "=" + flags[EnvClaimsInfraSession],
		"rollout " + EnvClaimsInfraFabric + "=" + flags[EnvClaimsInfraFabric],
		"rollout " + EnvClaimsInfraBus + "=" + flags[EnvClaimsInfraBus],
	}
}

func normalizeProjectionRolloutMode(mode ProjectionRolloutMode) ProjectionRolloutMode {
	switch ProjectionRolloutMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case ProjectionRolloutOff, "0", "false", "disabled", "disable":
		return ProjectionRolloutOff
	case ProjectionRolloutAuthoritative, "on", "1", "true", "enabled", "enable":
		return ProjectionRolloutAuthoritative
	case ProjectionRolloutShadow, "":
		return ProjectionRolloutShadow
	default:
		return ProjectionRolloutShadow
	}
}

func boolEnv(getenv func(string) string, key string, fallback bool) bool {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "y", "on", "enabled", "enable":
		return true
	case "0", "false", "no", "n", "off", "disabled", "disable":
		return false
	default:
		return fallback
	}
}

func projectionModeEnv(getenv func(string) string, key string, fallback ProjectionRolloutMode) ProjectionRolloutMode {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback
	}
	return normalizeProjectionRolloutMode(ProjectionRolloutMode(raw))
}

func normalizeInfrastructureRolloutMode(mode InfrastructureRolloutMode) InfrastructureRolloutMode {
	switch InfrastructureRolloutMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case InfrastructureRolloutDisabled, "off", "0", "false", "disable":
		return InfrastructureRolloutDisabled
	case InfrastructureRolloutShadow:
		return InfrastructureRolloutShadow
	case InfrastructureRolloutEnabled, "", "on", "1", "true", "enable", "authoritative":
		return InfrastructureRolloutEnabled
	default:
		return InfrastructureRolloutDisabled
	}
}

func infrastructureModeEnv(getenv func(string) string, key string, fallback InfrastructureRolloutMode) InfrastructureRolloutMode {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback
	}
	return normalizeInfrastructureRolloutMode(InfrastructureRolloutMode(raw))
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
