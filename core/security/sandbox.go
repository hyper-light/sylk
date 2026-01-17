package security

import (
	"context"
	"errors"
	"os/exec"
	"sync"
)

var (
	ErrSandboxUnavailable = errors.New("sandbox unavailable on this platform")
	ErrSandboxDisabled    = errors.New("sandbox is disabled")
	ErrPathOutsideSandbox = errors.New("path violates sandbox boundary")
	ErrNetworkBlocked     = errors.New("network request blocked by sandbox")
)

type SandboxLayer string

const (
	LayerOS      SandboxLayer = "os"
	LayerVFS     SandboxLayer = "vfs"
	LayerNetwork SandboxLayer = "network"
)

type SandboxConfig struct {
	Enabled         bool     `yaml:"enabled"`
	OSIsolation     bool     `yaml:"os_isolation"`
	VFSLayer        bool     `yaml:"vfs_layer"`
	NetworkProxy    bool     `yaml:"network_proxy"`
	WorkingDir      string   `yaml:"working_dir"`
	AllowedPaths    []string `yaml:"allowed_paths"`
	AllowedDomains  []string `yaml:"allowed_domains"`
	GracefulDegrade bool     `yaml:"graceful_degrade"`
}

func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Enabled:         false,
		OSIsolation:     false,
		VFSLayer:        false,
		NetworkProxy:    false,
		GracefulDegrade: true,
	}
}

type SandboxStatus struct {
	Enabled       bool
	OSAvailable   bool
	OSActive      bool
	VFSActive     bool
	NetworkActive bool
	Platform      string
	Errors        []string
}

type Sandbox struct {
	mu           sync.RWMutex
	config       SandboxConfig
	osWrapper    OSWrapper
	vfs          *VirtualFilesystem
	networkProxy *NetworkProxy
	auditLogger  *AuditLogger
}

type OSWrapper interface {
	Available() bool
	WrapCommand(cmd *exec.Cmd, workDir string, allowedPaths []string) (*exec.Cmd, error)
	Platform() string
}

func NewSandbox(cfg SandboxConfig, auditLogger *AuditLogger) *Sandbox {
	s := &Sandbox{
		config:      cfg,
		auditLogger: auditLogger,
	}

	s.osWrapper = newPlatformOSWrapper()

	if cfg.VFSLayer {
		s.vfs = NewVirtualFilesystem(cfg.WorkingDir, cfg.AllowedPaths)
	}

	if cfg.NetworkProxy {
		s.networkProxy = NewNetworkProxy(cfg.AllowedDomains)
	}

	return s
}

func (s *Sandbox) Execute(ctx context.Context, cmd *exec.Cmd) (*exec.Cmd, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.config.Enabled {
		return cmd, nil
	}

	var errs []error

	if s.config.OSIsolation {
		wrapped, err := s.wrapWithOS(cmd)
		if err != nil {
			errs = append(errs, err)
		} else {
			cmd = wrapped
		}
	}

	if s.config.VFSLayer && s.vfs != nil {
		if err := s.vfs.PrepareForCommand(cmd); err != nil {
			errs = append(errs, err)
		}
	}

	if s.config.NetworkProxy && s.networkProxy != nil {
		s.networkProxy.RouteCommand(cmd)
	}

	if len(errs) > 0 && !s.config.GracefulDegrade {
		return nil, errs[0]
	}

	s.logExecution(cmd, errs)

	return cmd, nil
}

func (s *Sandbox) wrapWithOS(cmd *exec.Cmd) (*exec.Cmd, error) {
	if s.osWrapper == nil || !s.osWrapper.Available() {
		if s.config.GracefulDegrade {
			return cmd, nil
		}
		return nil, ErrSandboxUnavailable
	}

	return s.osWrapper.WrapCommand(cmd, s.config.WorkingDir, s.config.AllowedPaths)
}

func (s *Sandbox) Enable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.Enabled = true
}

func (s *Sandbox) Disable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.Enabled = false
}

func (s *Sandbox) EnableLayer(layer SandboxLayer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch layer {
	case LayerOS:
		s.config.OSIsolation = true
	case LayerVFS:
		s.config.VFSLayer = true
		if s.vfs == nil {
			s.vfs = NewVirtualFilesystem(s.config.WorkingDir, s.config.AllowedPaths)
		}
	case LayerNetwork:
		s.config.NetworkProxy = true
		if s.networkProxy == nil {
			s.networkProxy = NewNetworkProxy(s.config.AllowedDomains)
		}
	}
}

func (s *Sandbox) DisableLayer(layer SandboxLayer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch layer {
	case LayerOS:
		s.config.OSIsolation = false
	case LayerVFS:
		s.config.VFSLayer = false
	case LayerNetwork:
		s.config.NetworkProxy = false
	}
}

func (s *Sandbox) AllowPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.AllowedPaths = append(s.config.AllowedPaths, path)
	if s.vfs != nil {
		s.vfs.AddAllowedPath(path)
	}
}

func (s *Sandbox) AllowDomain(domain string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.AllowedDomains = append(s.config.AllowedDomains, domain)
	if s.networkProxy != nil {
		s.networkProxy.AddAllowedDomain(domain)
	}
}

func (s *Sandbox) Status() SandboxStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := SandboxStatus{
		Enabled:       s.config.Enabled,
		VFSActive:     s.config.VFSLayer && s.vfs != nil,
		NetworkActive: s.config.NetworkProxy && s.networkProxy != nil,
	}

	if s.osWrapper != nil {
		status.OSAvailable = s.osWrapper.Available()
		status.OSActive = s.config.OSIsolation && s.osWrapper.Available()
		status.Platform = s.osWrapper.Platform()
	}

	return status
}

func (s *Sandbox) ValidatePath(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.config.VFSLayer || s.vfs == nil {
		return nil
	}

	return s.vfs.ValidatePath(path)
}

func (s *Sandbox) logExecution(cmd *exec.Cmd, errs []error) {
	if s.auditLogger == nil {
		return
	}

	entry := NewAuditEntry(AuditCategoryProcess, "sandbox_execute", "execute")
	entry.Target = cmd.Path
	if len(errs) > 0 {
		entry.Outcome = "degraded"
		errStrs := make([]string, len(errs))
		for i, e := range errs {
			errStrs[i] = e.Error()
		}
		entry.Details = map[string]any{
			"errors": errStrs,
		}
	} else {
		entry.Outcome = "success"
		entry.Details = map[string]any{}
	}
	entry.Details["layers"] = s.activeLayers()
	_ = s.auditLogger.Log(entry)
}

func (s *Sandbox) activeLayers() []string {
	var layers []string
	if s.config.OSIsolation {
		layers = append(layers, "os")
	}
	if s.config.VFSLayer {
		layers = append(layers, "vfs")
	}
	if s.config.NetworkProxy {
		layers = append(layers, "network")
	}
	return layers
}

func (s *Sandbox) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.networkProxy != nil {
		s.networkProxy.Stop()
	}
}
