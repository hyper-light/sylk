package engineer

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/versioning"
)

type overlayAwareFileAccess interface {
	versioning.FileAccess
	Modifications() []versioning.FileModification
}

type commandExecContext struct {
	workDir string
	cleanup func()
	plan    purevfs.ExecutionPlan
}

func (e *Engineer) effectiveWorkingDirectory() string {
	if e.fileAccess != nil && strings.TrimSpace(e.fileAccess.WorkingDir()) != "" {
		return e.fileAccess.WorkingDir()
	}
	return strings.TrimSpace(e.config.EngineerConfig.WorkingDirectory)
}

func needsMaterializedWorkspace(fa versioning.FileAccess) bool {
	overlay, ok := fa.(overlayAwareFileAccess)
	return ok && len(overlay.Modifications()) > 0
}

func (e *Engineer) commandExecutionContext(ctx context.Context, requested string) (commandExecContext, error) {
	workspaceRoot := e.effectiveWorkingDirectory()
	plan, err := e.executionPlan(requested)
	if err != nil {
		return commandExecContext{}, err
	}
	execRoot := workspaceRoot
	cleanup := func() {}

	if plan.RequiresMaterialize {
		tmpDir, tmpCleanup, err := e.materializeWorkspace(ctx)
		if err != nil {
			return commandExecContext{}, err
		}
		execRoot = tmpDir
		cleanup = tmpCleanup
	}

	runDir := resolveCommandWorkingDir(workspaceRoot, execRoot, requested)
	if strings.TrimSpace(runDir) == "" {
		runDir = "."
	}
	plan.WorkingDir = runDir
	return commandExecContext{
		workDir: runDir,
		cleanup: cleanup,
		plan:    plan,
	}, nil
}

func (e *Engineer) executionPlan(requested string) (purevfs.ExecutionPlan, error) {
	overlay, overlayDeletes := overlayState(e.fileAccess)
	workspaceRoot := e.effectiveWorkingDirectory()
	planner := purevfs.NewExecutionPlanner(nil, purevfs.HostCompatibilityCapabilities())
	return planner.Plan(purevfs.ExecutionRequest{
		Mode:           purevfs.ExecutionModeCompatibility,
		Intent:         purevfs.ExecutionIntentCommand,
		Language:       inferExecutionLanguage(workspaceRoot, e.fileAccess),
		WorkspaceRoot:  workspaceRoot,
		WorkingDir:     requested,
		Overlay:        overlay,
		OverlayDeletes: overlayDeletes,
	})
}

func inferExecutionLanguage(workspaceRoot string, fa versioning.FileAccess) string {
	if summary := purevfs.DefaultCatalog().DetectProject(workspaceRoot); summary.PrimaryLanguage != "" {
		return summary.PrimaryLanguage
	}
	overlay, ok := fa.(overlayAwareFileAccess)
	if !ok {
		return ""
	}
	files := make([]string, 0, len(overlay.Modifications()))
	for _, mod := range overlay.Modifications() {
		files = append(files, mod.OriginalPath)
	}
	return purevfs.InferPrimaryLanguage(files, "engineer")
}

func overlayState(fa versioning.FileAccess) (bool, bool) {
	overlay, ok := fa.(overlayAwareFileAccess)
	if !ok {
		return false, false
	}
	mods := overlay.Modifications()
	if len(mods) == 0 {
		return false, false
	}
	hasDeletes := false
	for _, mod := range mods {
		if mod.Operation == versioning.FileOpDelete {
			hasDeletes = true
			break
		}
	}
	return true, hasDeletes
}

func resolveCommandWorkingDir(workspaceRoot, execRoot, requested string) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return execRoot
	}
	if !filepath.IsAbs(requested) {
		if execRoot == "" {
			return requested
		}
		return filepath.Join(execRoot, requested)
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		return requested
	}
	rel, err := filepath.Rel(workspaceRoot, requested)
	if err != nil || strings.HasPrefix(rel, "..") {
		return requested
	}
	if rel == "." {
		return execRoot
	}
	return filepath.Join(execRoot, rel)
}

func (e *Engineer) materializeWorkspace(_ context.Context) (string, func(), error) {
	root := e.effectiveWorkingDirectory()
	if root == "" {
		root = "."
	}
	tmpDir, err := os.MkdirTemp("", "sylk-engineer-workspace-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(tmpDir, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		if err := os.Link(path, target); err == nil {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, info.Mode())
	})
	if err != nil {
		cleanup()
		return "", nil, err
	}

	if fa, ok := e.fileAccess.(overlayAwareFileAccess); ok {
		for _, mod := range fa.Modifications() {
			rel, err := filepath.Rel(root, mod.OriginalPath)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			target := filepath.Join(tmpDir, rel)
			switch mod.Operation {
			case versioning.FileOpDelete:
				_ = os.Remove(target)
			default:
				if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					cleanup()
					return "", nil, err
				}
				if err := os.WriteFile(target, mod.NewContent, 0644); err != nil {
					cleanup()
					return "", nil, err
				}
			}
		}
	}

	return tmpDir, cleanup, nil
}
