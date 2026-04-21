package agentlog

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// LoggingConfig is the on-disk representation of the logging block in
// .sylk/config.yaml. See docs/LOGGING.md for field semantics. All
// fields are optional; missing or zero values resolve to the
// design-doc defaults at ToSessionLoggerConfig time.
type LoggingConfig struct {
	Enabled  bool              `yaml:"enabled"`
	Rotation RotationConfig    `yaml:"rotation"`
	Streams  StreamsConfig     `yaml:"streams"`
	LLM      LLMConfig         `yaml:"llm"`
	Redact   RedactConfig      `yaml:"redact"`
}

// RotationConfig controls size and time-based rotation.
type RotationConfig struct {
	MaxBytesPerFile int64  `yaml:"max_bytes_per_file"`
	Timezone        string `yaml:"timezone"` // "local", "UTC", or IANA name
}

// StreamsConfig enables/disables individual log streams.
type StreamsConfig struct {
	Events *bool `yaml:"events"`
	Tools  *bool `yaml:"tools"`
	LLM    *bool `yaml:"llm"`
	Bus    *bool `yaml:"bus"`
}

// LLMConfig tunes LLM-record compaction.
type LLMConfig struct {
	ContentAddressPrompts *bool `yaml:"content_address_prompts"`
	InlineMessages        *bool `yaml:"inline_messages"`
	TruncateMessageTextAt int   `yaml:"truncate_message_text_at"`
	TruncateToolOutputAt  int   `yaml:"truncate_tool_output_at"`
}

// RedactConfig populates the redactor chain.
type RedactConfig struct {
	EnvVars            []string `yaml:"env_vars"`
	FieldNames         []string `yaml:"field_names"`
	Patterns           []string `yaml:"patterns"`
	IncludeFingerprint *bool    `yaml:"include_fingerprint"`
}

// DefaultLoggingConfig returns a config matching the design-doc
// defaults: enabled, 64 MiB files, local TZ, events/tools/llm on,
// bus off, content-addressed LLM prompts, fingerprinted redaction
// of the common env var names and field denylist.
func DefaultLoggingConfig() LoggingConfig {
	tr := true
	fa := false
	return LoggingConfig{
		Enabled: true,
		Rotation: RotationConfig{
			MaxBytesPerFile: defaultMaxBytesPerFile,
			Timezone:        "local",
		},
		Streams: StreamsConfig{
			Events: &tr,
			Tools:  &tr,
			LLM:    &tr,
			Bus:    &fa,
		},
		LLM: LLMConfig{
			ContentAddressPrompts: &tr,
			InlineMessages:        &tr,
			TruncateMessageTextAt: defaultTruncateMessageAt,
			TruncateToolOutputAt:  64 * 1024,
		},
		Redact: RedactConfig{
			EnvVars:            []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"},
			FieldNames:         []string{"authorization", "api_key", "token", "password", "secret"},
			IncludeFingerprint: &tr,
		},
	}
}

// LoadLoggingConfig reads a .sylk/config.yaml file and returns the
// parsed logging block merged with DefaultLoggingConfig. Missing or
// unreadable config files resolve to defaults (with Enabled=true).
// A malformed yaml file surfaces an error — we don't silently drop
// user-supplied config.
func LoadLoggingConfig(configPath string) (LoggingConfig, error) {
	cfg := DefaultLoggingConfig()
	if configPath == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("agentlog: read config: %w", err)
	}
	var wrapper struct {
		Logging LoggingConfig `yaml:"logging"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		return cfg, fmt.Errorf("agentlog: parse logging config: %w", err)
	}
	return cfg.Merge(wrapper.Logging), nil
}

// Merge layers overrides from `other` onto the receiver. Zero values
// in `other` are left untouched; non-zero values replace. Nil
// pointers on BoolPointer fields signal "not set".
func (c LoggingConfig) Merge(other LoggingConfig) LoggingConfig {
	out := c
	if other.Enabled {
		out.Enabled = true
	}
	if other.Rotation.MaxBytesPerFile > 0 {
		out.Rotation.MaxBytesPerFile = other.Rotation.MaxBytesPerFile
	}
	if other.Rotation.Timezone != "" {
		out.Rotation.Timezone = other.Rotation.Timezone
	}
	if other.Streams.Events != nil {
		out.Streams.Events = other.Streams.Events
	}
	if other.Streams.Tools != nil {
		out.Streams.Tools = other.Streams.Tools
	}
	if other.Streams.LLM != nil {
		out.Streams.LLM = other.Streams.LLM
	}
	if other.Streams.Bus != nil {
		out.Streams.Bus = other.Streams.Bus
	}
	if other.LLM.ContentAddressPrompts != nil {
		out.LLM.ContentAddressPrompts = other.LLM.ContentAddressPrompts
	}
	if other.LLM.InlineMessages != nil {
		out.LLM.InlineMessages = other.LLM.InlineMessages
	}
	if other.LLM.TruncateMessageTextAt > 0 {
		out.LLM.TruncateMessageTextAt = other.LLM.TruncateMessageTextAt
	}
	if other.LLM.TruncateToolOutputAt > 0 {
		out.LLM.TruncateToolOutputAt = other.LLM.TruncateToolOutputAt
	}
	if len(other.Redact.EnvVars) > 0 {
		out.Redact.EnvVars = other.Redact.EnvVars
	}
	if len(other.Redact.FieldNames) > 0 {
		out.Redact.FieldNames = other.Redact.FieldNames
	}
	if len(other.Redact.Patterns) > 0 {
		out.Redact.Patterns = other.Redact.Patterns
	}
	if other.Redact.IncludeFingerprint != nil {
		out.Redact.IncludeFingerprint = other.Redact.IncludeFingerprint
	}
	return out
}

// ToSessionLoggerConfig converts the parsed config into the runtime
// shape consumed by SessionEventLogger.SetConfig. getEnv is passed to
// the redactor for env-value snapshotting; nil is tolerated.
func (c LoggingConfig) ToSessionLoggerConfig(getEnv func(string) string) SessionLoggerConfig {
	loc := time.Local
	switch c.Rotation.Timezone {
	case "", "local":
		loc = time.Local
	case "UTC", "utc":
		loc = time.UTC
	default:
		if parsed, err := time.LoadLocation(c.Rotation.Timezone); err == nil {
			loc = parsed
		}
	}

	redactCfg := RedactorConfig{
		EnvVars:            c.Redact.EnvVars,
		FieldNames:         c.Redact.FieldNames,
		ExtraPatterns:      c.Redact.Patterns,
		IncludeFingerprint: boolDeref(c.Redact.IncludeFingerprint, true),
	}
	redactor := NewRedactor(redactCfg, getEnv)

	return SessionLoggerConfig{
		StreamWriterConfig: StreamWriterConfig{
			MaxBytesPerFile: c.Rotation.MaxBytesPerFile,
			Location:        loc,
		},
		Redactor:          redactor,
		EnableBusStream:   boolDeref(c.Streams.Bus, false),
		DisableToolStream: !boolDeref(c.Streams.Tools, true),
		DisableLLMStream:  !boolDeref(c.Streams.LLM, true),
		LLMRecorderConfig: LLMRecorderConfig{
			TruncateMessageAt:     c.LLM.TruncateMessageTextAt,
			InlineMessages:        boolDeref(c.LLM.InlineMessages, true),
			ContentAddressPrompts: boolDeref(c.LLM.ContentAddressPrompts, true),
		},
	}
}

func boolDeref(p *bool, fallback bool) bool {
	if p == nil {
		return fallback
	}
	return *p
}
