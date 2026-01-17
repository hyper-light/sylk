package security

import (
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

type PathPerm struct {
	Read   bool `yaml:"read"`
	Write  bool `yaml:"write"`
	Delete bool `yaml:"delete"`
}

type ProjectPermissions struct {
	Commands             []string          `yaml:"commands"`
	Domains              []string          `yaml:"domains"`
	Paths                map[string]string `yaml:"paths"`
	AllowModifyEnv       bool              `yaml:"allow_modify_env"`
	AllowModifyGitignore bool              `yaml:"allow_modify_gitignore"`
}

type Allowlist struct {
	mu               sync.RWMutex
	projectPath      string
	commandAllowlist map[string]bool
	domainAllowlist  map[string]bool
	pathAllowlist    map[string]PathPerm
	allowModifyEnv   bool
	allowModifyGit   bool
}

func NewAllowlist(projectPath string) *Allowlist {
	return &Allowlist{
		projectPath:      projectPath,
		commandAllowlist: make(map[string]bool),
		domainAllowlist:  make(map[string]bool),
		pathAllowlist:    make(map[string]PathPerm),
	}
}

func (a *Allowlist) Load() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	permPath := filepath.Join(a.projectPath, ".sylk", "local", "permissions.yaml")
	data, err := os.ReadFile(permPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var perms ProjectPermissions
	if err := yaml.Unmarshal(data, &perms); err != nil {
		return err
	}

	return a.applyPermissions(&perms)
}

func (a *Allowlist) applyPermissions(perms *ProjectPermissions) error {
	for _, cmd := range perms.Commands {
		a.commandAllowlist[cmd] = true
	}
	for _, domain := range perms.Domains {
		a.domainAllowlist[domain] = true
	}
	for path, permStr := range perms.Paths {
		a.pathAllowlist[path] = parsePathPerm(permStr)
	}
	a.allowModifyEnv = perms.AllowModifyEnv
	a.allowModifyGit = perms.AllowModifyGitignore
	return nil
}

func parsePathPerm(s string) PathPerm {
	switch s {
	case "read":
		return PathPerm{Read: true}
	case "write":
		return PathPerm{Read: true, Write: true}
	case "delete":
		return PathPerm{Read: true, Write: true, Delete: true}
	default:
		return PathPerm{Read: true}
	}
}

func (a *Allowlist) Save() error {
	a.mu.RLock()
	perms := a.toProjectPermissions()
	a.mu.RUnlock()

	data, err := yaml.Marshal(perms)
	if err != nil {
		return err
	}

	permDir := filepath.Join(a.projectPath, ".sylk", "local")
	if err := os.MkdirAll(permDir, 0755); err != nil {
		return err
	}

	permPath := filepath.Join(permDir, "permissions.yaml")
	return os.WriteFile(permPath, data, 0600)
}

func (a *Allowlist) toProjectPermissions() ProjectPermissions {
	perms := ProjectPermissions{
		Commands:             make([]string, 0, len(a.commandAllowlist)),
		Domains:              make([]string, 0, len(a.domainAllowlist)),
		Paths:                make(map[string]string, len(a.pathAllowlist)),
		AllowModifyEnv:       a.allowModifyEnv,
		AllowModifyGitignore: a.allowModifyGit,
	}
	for cmd := range a.commandAllowlist {
		perms.Commands = append(perms.Commands, cmd)
	}
	for domain := range a.domainAllowlist {
		perms.Domains = append(perms.Domains, domain)
	}
	for path, perm := range a.pathAllowlist {
		perms.Paths[path] = serializePathPerm(perm)
	}
	return perms
}

func serializePathPerm(p PathPerm) string {
	if p.Delete {
		return "delete"
	}
	if p.Write {
		return "write"
	}
	return "read"
}

func (a *Allowlist) IsCommandAllowed(cmd string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.commandAllowlist[cmd]
}

func (a *Allowlist) IsDomainAllowed(domain string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.domainAllowlist[domain]
}

func (a *Allowlist) GetPathPerm(path string) (PathPerm, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	perm, ok := a.pathAllowlist[path]
	return perm, ok
}

func (a *Allowlist) AllowCommand(cmd string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.commandAllowlist[cmd] = true
}

func (a *Allowlist) AllowDomain(domain string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.domainAllowlist[domain] = true
}

func (a *Allowlist) AllowPath(path string, perm PathPerm) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pathAllowlist[path] = perm
}

func (a *Allowlist) CanModifyEnv() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.allowModifyEnv
}

func (a *Allowlist) CanModifyGitignore() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.allowModifyGit
}

func (a *Allowlist) SetAllowModifyEnv(allow bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.allowModifyEnv = allow
}

func (a *Allowlist) SetAllowModifyGitignore(allow bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.allowModifyGit = allow
}
