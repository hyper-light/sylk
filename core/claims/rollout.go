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
)

type ProjectionRolloutMode string

const (
	ProjectionRolloutOff           ProjectionRolloutMode = "off"
	ProjectionRolloutShadow        ProjectionRolloutMode = "shadow"
	ProjectionRolloutAuthoritative ProjectionRolloutMode = "authoritative"
)

type RolloutConfig struct {
	DurableSessionClaims      bool                  `json:"durable_session_claims"`
	ClaimsOutbox              bool                  `json:"claims_outbox"`
	ClaimsKnowledgeMirror     ProjectionRolloutMode `json:"claims_knowledge_mirror"`
	RecallForwardCrossSession bool                  `json:"recall_forward_cross_session"`
	ScribeContinuityNarration bool                  `json:"scribe_continuity_narration"`
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
	return cfg.Normalized()
}

func RolloutConfigFromEnvironment() RolloutConfig {
	return RolloutConfigFromEnv(os.Getenv)
}

func (cfg RolloutConfig) Normalized() RolloutConfig {
	cfg.ClaimsKnowledgeMirror = normalizeProjectionRolloutMode(cfg.ClaimsKnowledgeMirror)
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

func (cfg RolloutConfig) FeatureFlags() map[string]string {
	cfg = cfg.Normalized()
	return map[string]string{
		EnvDurableSessionClaims:      boolString(cfg.DurableSessionClaims),
		EnvClaimsOutbox:              boolString(cfg.ClaimsOutbox),
		EnvClaimsKnowledgeMirror:     string(cfg.ClaimsKnowledgeMirror),
		EnvRecallForwardCrossSession: boolString(cfg.RecallForwardCrossSession),
		EnvScribeContinuityNarration: boolString(cfg.ScribeContinuityNarration),
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

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
