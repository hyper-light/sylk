package purevfs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var (
	ErrStrictExecutionUnavailable = errors.New("purevfs: strict no-disk execution backend unavailable")
	ErrUnsupportedExecutionTarget = errors.New("purevfs: unsupported execution target")
)

type ExecutionMode string

const (
	ExecutionModeCompatibility ExecutionMode = "compatibility"
	ExecutionModeStrictNoDisk  ExecutionMode = "strict-no-disk"
)

type ExecutionIntent string

const (
	ExecutionIntentBuild   ExecutionIntent = "build"
	ExecutionIntentRun     ExecutionIntent = "run"
	ExecutionIntentTest    ExecutionIntent = "test"
	ExecutionIntentCommand ExecutionIntent = "command"
)

type ExecutionStrategy string

const (
	StrategyDirectPassthrough    ExecutionStrategy = "direct-passthrough"
	StrategyProcessBroker        ExecutionStrategy = "process-broker"
	StrategyGoOverlayManifest    ExecutionStrategy = "go-overlay-manifest"
	StrategyWorkspaceMaterialize ExecutionStrategy = "workspace-materialize"
)

type MountKind string

const (
	MountWorkspace MountKind = "workspace"
	MountToolchain MountKind = "toolchain"
	MountTemp      MountKind = "temp"
	MountCache     MountKind = "cache"
	MountHome      MountKind = "home"
	MountOutput    MountKind = "output"
)

type MountAccess string

const (
	MountReadOnly      MountAccess = "read-only"
	MountReadWrite     MountAccess = "read-write"
	MountOverlaySource MountAccess = "overlay-source"
)

type MountSpec struct {
	Kind        MountKind
	Access      MountAccess
	VirtualPath string
	BackingPath string
	InMemory    bool
	Mandatory   bool
	Description string
}

type ExecutionCapabilities struct {
	ProcessBroker         bool
	InMemoryScratch       bool
	GoOverlayManifest     bool
	WorkspaceMaterializer bool
	DirectPassthrough     bool
}

type ExecutionRequest struct {
	Mode           ExecutionMode
	Intent         ExecutionIntent
	Language       string
	FrameworkID    string
	WorkspaceRoot  string
	WorkingDir     string
	Roots          ExecRoots
	Overlay        bool
	OverlayDeletes bool
}

type ExecutionPlan struct {
	Mode                   ExecutionMode
	Intent                 ExecutionIntent
	Language               string
	FrameworkID            string
	Strategy               ExecutionStrategy
	WorkingDir             string
	Roots                  ExecRoots
	Cell                   ExecCellPlan
	Env                    map[string]string
	Mounts                 []MountSpec
	RequiresBroker         bool
	RequiresMaterialize    bool
	Overlay                bool
	CompatibilityFallbacks []string
	ComplianceNotes        []string
}

type MaterializeSpec struct {
	Plan ExecutionPlan
}

type MaterializedWorkspace struct {
	WorkspaceRoot string
	Roots         ExecRoots
}

type BrokerRunRequest struct {
	Plan ExecutionPlan
	Argv []string
	Env  map[string]string
}

type BrokerRunResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

type Materializer interface {
	Materialize(ctx context.Context, spec MaterializeSpec) (*MaterializedWorkspace, error)
}

type ExecutionBroker interface {
	Capabilities(ctx context.Context) (ExecutionCapabilities, error)
	Run(ctx context.Context, req BrokerRunRequest) (*BrokerRunResult, error)
}

type ExecutionPlanner struct {
	catalog      *Catalog
	capabilities ExecutionCapabilities
}

func NewExecutionPlanner(catalog *Catalog, capabilities ExecutionCapabilities) *ExecutionPlanner {
	if catalog == nil {
		catalog = DefaultCatalog()
	}
	return &ExecutionPlanner{
		catalog:      catalog,
		capabilities: capabilities,
	}
}

func HostCompatibilityCapabilities() ExecutionCapabilities {
	return ExecutionCapabilities{
		GoOverlayManifest:     true,
		WorkspaceMaterializer: true,
		DirectPassthrough:     true,
	}
}

func StrictBrokerCapabilities() ExecutionCapabilities {
	return ExecutionCapabilities{
		ProcessBroker:   true,
		InMemoryScratch: true,
	}
}

func DefaultLogicalExecRoots(workspace string) ExecRoots {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace = "/workspace"
	}
	return ExecRoots{
		Workspace: workspace,
		Temp:      "/tmp",
		Cache:     "/cache",
		Home:      "/home/agent",
		Out:       "/out",
	}
}

func (p *ExecutionPlanner) Plan(req ExecutionRequest) (ExecutionPlan, error) {
	mode := req.Mode
	if mode == "" {
		mode = ExecutionModeCompatibility
	}
	intent := req.Intent
	if intent == "" {
		intent = ExecutionIntentCommand
	}

	language := strings.TrimSpace(req.Language)
	if language == "" && strings.TrimSpace(req.WorkspaceRoot) != "" {
		summary := p.catalog.DetectProject(req.WorkspaceRoot)
		language = summary.PrimaryLanguage
	}
	if language == "" {
		language = "generic"
	}

	roots := req.Roots
	if strings.TrimSpace(roots.Workspace) == "" {
		roots = DefaultLogicalExecRoots(req.WorkspaceRoot)
	}

	cell, ok := p.catalog.PlanExecCell(language, roots)
	if !ok {
		if language != "generic" {
			return ExecutionPlan{}, fmt.Errorf("%w: %s", ErrUnsupportedExecutionTarget, language)
		}
		cell = genericExecCellPlan(roots)
	}

	plan := ExecutionPlan{
		Mode:        mode,
		Intent:      intent,
		Language:    language,
		FrameworkID: strings.TrimSpace(req.FrameworkID),
		WorkingDir:  resolveExecutionWorkingDir(roots.Workspace, req.WorkingDir),
		Roots:       roots,
		Cell:        cell,
		Env:         copyEnvMap(cell.Env),
		Overlay:     req.Overlay,
		Mounts: []MountSpec{
			{
				Kind:        MountWorkspace,
				Access:      mountWorkspaceAccess(req.Overlay),
				VirtualPath: cell.WorkspaceRoot,
				BackingPath: strings.TrimSpace(req.WorkspaceRoot),
				Mandatory:   true,
				Description: "project source tree",
			},
			{
				Kind:        MountTemp,
				Access:      MountReadWrite,
				VirtualPath: cell.TempRoot,
				BackingPath: roots.Temp,
				InMemory:    true,
				Mandatory:   true,
				Description: "temporary files",
			},
			{
				Kind:        MountCache,
				Access:      MountReadWrite,
				VirtualPath: cell.CacheRoot,
				BackingPath: roots.Cache,
				InMemory:    true,
				Mandatory:   true,
				Description: "language and package caches",
			},
			{
				Kind:        MountHome,
				Access:      MountReadWrite,
				VirtualPath: cell.HomeRoot,
				BackingPath: roots.Home,
				InMemory:    true,
				Mandatory:   true,
				Description: "synthetic user home",
			},
			{
				Kind:        MountOutput,
				Access:      MountReadWrite,
				VirtualPath: cell.OutRoot,
				BackingPath: roots.Out,
				InMemory:    true,
				Mandatory:   true,
				Description: "promotable execution outputs",
			},
		},
		ComplianceNotes: []string{
			"disable swap, crash dumps, and OS spill paths outside the execution layer",
			"treat toolchains and runtimes as read-only passthrough mounts",
			"route temp, cache, home, and output paths through execution cell roots",
		},
	}
	for _, tool := range cell.Toolchains {
		plan.Mounts = append(plan.Mounts, MountSpec{
			Kind:        MountToolchain,
			Access:      MountReadOnly,
			VirtualPath: tool,
			BackingPath: tool,
			Mandatory:   false,
			Description: "toolchain passthrough",
		})
	}

	if mode == ExecutionModeStrictNoDisk {
		if p.capabilities.ProcessBroker && p.capabilities.InMemoryScratch {
			plan.Strategy = StrategyProcessBroker
			plan.RequiresBroker = true
			return plan, nil
		}
		return ExecutionPlan{}, ErrStrictExecutionUnavailable
	}

	if req.Overlay {
		switch {
		case p.capabilities.ProcessBroker && p.capabilities.InMemoryScratch:
			plan.Strategy = StrategyProcessBroker
			plan.RequiresBroker = true
		case plan.FrameworkID == "go-test" && !req.OverlayDeletes && p.capabilities.GoOverlayManifest:
			plan.Strategy = StrategyGoOverlayManifest
			plan.CompatibilityFallbacks = append(plan.CompatibilityFallbacks,
				"host Go overlay manifests still require file-backed compatibility plumbing")
		case p.capabilities.WorkspaceMaterializer:
			plan.Strategy = StrategyWorkspaceMaterialize
			plan.RequiresMaterialize = true
			plan.CompatibilityFallbacks = append(plan.CompatibilityFallbacks,
				"workspace materialization writes compatibility artifacts outside strict no-disk mode")
		default:
			return ExecutionPlan{}, ErrStrictExecutionUnavailable
		}
		return plan, nil
	}

	switch {
	case p.capabilities.ProcessBroker && p.capabilities.InMemoryScratch:
		plan.Strategy = StrategyProcessBroker
		plan.RequiresBroker = true
	case p.capabilities.DirectPassthrough:
		plan.Strategy = StrategyDirectPassthrough
	default:
		return ExecutionPlan{}, ErrStrictExecutionUnavailable
	}
	return plan, nil
}

func genericExecCellPlan(roots ExecRoots) ExecCellPlan {
	return ExecCellPlan{
		Language:      "generic",
		WorkspaceRoot: roots.Workspace,
		TempRoot:      roots.Temp,
		CacheRoot:     roots.Cache,
		HomeRoot:      roots.Home,
		OutRoot:       roots.Out,
		Env: map[string]string{
			"TMPDIR": roots.Temp,
			"TMP":    roots.Temp,
			"TEMP":   roots.Temp,
			"HOME":   roots.Home,
		},
	}
}

func resolveExecutionWorkingDir(workspaceRoot, requested string) string {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return workspaceRoot
	}
	if filepath.IsAbs(requested) {
		return requested
	}
	if workspaceRoot == "" {
		return requested
	}
	return filepath.Join(workspaceRoot, requested)
}

func mountWorkspaceAccess(overlay bool) MountAccess {
	if overlay {
		return MountOverlaySource
	}
	return MountReadOnly
}

func copyEnvMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func MergeExecEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		out := make([]string, len(base))
		copy(out, base)
		return out
	}
	seen := make(map[string]int, len(base))
	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key := entry
		if idx := strings.IndexRune(entry, '='); idx >= 0 {
			key = entry[:idx]
		}
		seen[key] = len(out)
		out = append(out, entry)
	}
	for key, value := range overrides {
		entry := key + "=" + value
		if idx, ok := seen[key]; ok {
			out[idx] = entry
			continue
		}
		out = append(out, entry)
	}
	return out
}
