package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/adalundhe/sylk/core/storage"
	"gopkg.in/yaml.v3"
)

type Manager struct {
	configPtr unsafe.Pointer
	dirs      *storage.Dirs
	watchers  []func(*Config)
	watcherMu sync.RWMutex
	stopWatch chan struct{}
	watchOnce sync.Once
}

type Config struct {
	LLM       LLMConfig       `yaml:"llm"`
	Memory    MemoryConfig    `yaml:"memory"`
	Resources ResourcesConfig `yaml:"resources"`
	Disk      DiskConfig      `yaml:"disk"`
	Broker    BrokerConfig    `yaml:"broker"`
	Session   SessionConfig   `yaml:"session"`
	Agent     AgentConfig     `yaml:"agent"`
}

type LLMConfig struct {
	Timeout         time.Duration `yaml:"timeout"`
	DefaultProvider string        `yaml:"default_provider"`
	MaxRetries      int           `yaml:"max_retries"`
	RateLimitRPS    int           `yaml:"rate_limit_rps"`
}

type MemoryConfig struct {
	QueryCache             string  `yaml:"query_cache"`
	Staging                string  `yaml:"staging"`
	AgentContext           string  `yaml:"agent_context"`
	WAL                    string  `yaml:"wal"`
	GlobalCeilingPercent   float64 `yaml:"global_ceiling_percent"`
	EvictionTriggerPercent float64 `yaml:"eviction_trigger_percent"`
	MonitorInterval        string  `yaml:"monitor_interval"`
}

type ResourcesConfig struct {
	FileHandlePercent         float64 `yaml:"file_handle_percent"`
	UserReservedPercent       float64 `yaml:"user_reserved_percent"`
	ConnectionsPerProvider    int     `yaml:"connections_per_provider"`
	ConnectionsGlobal         int     `yaml:"connections_global"`
	MaxSubprocessesMultiplier int     `yaml:"max_subprocesses_multiplier"`
	PipelineWaitTimeout       string  `yaml:"pipeline_wait_timeout"`
	AllowPreemption           bool    `yaml:"allow_preemption"`
}

type DiskConfig struct {
	QuotaMin         string  `yaml:"quota_min"`
	QuotaMax         string  `yaml:"quota_max"`
	QuotaPercent     float64 `yaml:"quota_percent"`
	WarningThreshold float64 `yaml:"warning_threshold"`
	CleanupThreshold float64 `yaml:"cleanup_threshold"`
}

type BrokerConfig struct {
	AcquisitionTimeout string `yaml:"acquisition_timeout"`
	DeadlockDetection  bool   `yaml:"deadlock_detection"`
	DetectionInterval  string `yaml:"detection_interval"`
}

type SessionConfig struct {
	MaxConcurrent    int    `yaml:"max_concurrent"`
	IdleTimeout      string `yaml:"idle_timeout"`
	PersistenceDir   string `yaml:"persistence_dir"`
	AutoSaveInterval string `yaml:"auto_save_interval"`
}

type AgentConfig struct {
	MaxContextTokens int    `yaml:"max_context_tokens"`
	DefaultModel     string `yaml:"default_model"`
	StreamingEnabled bool   `yaml:"streaming_enabled"`
	ToolTimeout      string `yaml:"tool_timeout"`
}

func NewManager(dirs *storage.Dirs) *Manager {
	m := &Manager{
		dirs:      dirs,
		stopWatch: make(chan struct{}),
	}
	cfg := DefaultConfig()
	atomic.StorePointer(&m.configPtr, unsafe.Pointer(cfg))
	return m
}

func DefaultConfig() *Config {
	return &Config{
		LLM: LLMConfig{
			Timeout:         2 * time.Minute,
			DefaultProvider: "anthropic",
			MaxRetries:      3,
			RateLimitRPS:    10,
		},
		Memory: MemoryConfig{
			QueryCache:             "500MB",
			Staging:                "1GB",
			AgentContext:           "500MB",
			WAL:                    "200MB",
			GlobalCeilingPercent:   0.8,
			EvictionTriggerPercent: 0.7,
			MonitorInterval:        "1s",
		},
		Resources: ResourcesConfig{
			FileHandlePercent:         0.5,
			UserReservedPercent:       0.2,
			ConnectionsPerProvider:    10,
			ConnectionsGlobal:         50,
			MaxSubprocessesMultiplier: 2,
			PipelineWaitTimeout:       "30s",
			AllowPreemption:           true,
		},
		Disk: DiskConfig{
			QuotaMin:         "1GB",
			QuotaMax:         "20GB",
			QuotaPercent:     0.1,
			WarningThreshold: 0.8,
			CleanupThreshold: 0.9,
		},
		Broker: BrokerConfig{
			AcquisitionTimeout: "30s",
			DeadlockDetection:  true,
			DetectionInterval:  "5s",
		},
		Session: SessionConfig{
			MaxConcurrent:    10,
			IdleTimeout:      "30m",
			AutoSaveInterval: "5m",
		},
		Agent: AgentConfig{
			MaxContextTokens: 200000,
			DefaultModel:     "claude-sonnet-4-20250514",
			StreamingEnabled: true,
			ToolTimeout:      "5m",
		},
	}
}

func (m *Manager) Get() *Config {
	return (*Config)(atomic.LoadPointer(&m.configPtr))
}

func (m *Manager) Load() error {
	cfg := DefaultConfig()

	if err := m.loadProjectConfig(cfg); err != nil {
		return fmt.Errorf("project config: %w", err)
	}

	if err := m.loadUserConfig(cfg); err != nil {
		return fmt.Errorf("user config: %w", err)
	}

	if err := m.loadLocalConfig(cfg); err != nil {
		return fmt.Errorf("local config: %w", err)
	}

	m.applyEnvironment(cfg)

	atomic.StorePointer(&m.configPtr, unsafe.Pointer(cfg))
	m.notifyWatchers(cfg)

	return nil
}

func (m *Manager) loadProjectConfig(cfg *Config) error {
	projectDirs := storage.ResolveProjectDirs(".")
	return m.loadYAMLFile(projectDirs.Config, cfg)
}

func (m *Manager) loadUserConfig(cfg *Config) error {
	userConfigPath := m.dirs.ConfigDir("config.yaml")
	return m.loadYAMLFile(userConfigPath, cfg)
}

func (m *Manager) loadLocalConfig(cfg *Config) error {
	projectDirs := storage.ResolveProjectDirs(".")
	localPath := filepath.Join(projectDirs.Local, "config.yaml")
	return m.loadYAMLFile(localPath, cfg)
}

func (m *Manager) loadYAMLFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	return yaml.Unmarshal(data, cfg)
}

func (m *Manager) applyEnvironment(cfg *Config) {
	if v := os.Getenv("SYLK_LLM_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.LLM.Timeout = d
		}
	}
	if v := os.Getenv("SYLK_LLM_DEFAULT_PROVIDER"); v != "" {
		cfg.LLM.DefaultProvider = v
	}
	if v := os.Getenv("SYLK_LLM_MAX_RETRIES"); v != "" {
		if n, err := parseInt(v); err == nil {
			cfg.LLM.MaxRetries = n
		}
	}
	if v := os.Getenv("SYLK_MEMORY_GLOBAL_CEILING"); v != "" {
		if f, err := parseFloat(v); err == nil {
			cfg.Memory.GlobalCeilingPercent = f
		}
	}
	if v := os.Getenv("SYLK_RESOURCES_FILE_HANDLE_PERCENT"); v != "" {
		if f, err := parseFloat(v); err == nil {
			cfg.Resources.FileHandlePercent = f
		}
	}
	if v := os.Getenv("SYLK_SESSION_MAX_CONCURRENT"); v != "" {
		if n, err := parseInt(v); err == nil {
			cfg.Session.MaxConcurrent = n
		}
	}
	if v := os.Getenv("SYLK_AGENT_DEFAULT_MODEL"); v != "" {
		cfg.Agent.DefaultModel = v
	}
	if v := os.Getenv("SYLK_AGENT_STREAMING"); v != "" {
		cfg.Agent.StreamingEnabled = strings.ToLower(v) == "true"
	}
}

func (m *Manager) OnChange(fn func(*Config)) {
	m.watcherMu.Lock()
	m.watchers = append(m.watchers, fn)
	m.watcherMu.Unlock()
}

func (m *Manager) notifyWatchers(cfg *Config) {
	m.watcherMu.RLock()
	watchers := m.watchers
	m.watcherMu.RUnlock()

	for _, fn := range watchers {
		fn(cfg)
	}
}

func (m *Manager) Reload() error {
	return m.Load()
}

func (m *Manager) Close() error {
	m.watchOnce.Do(func() {
		close(m.stopWatch)
	})
	return nil
}

func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}
