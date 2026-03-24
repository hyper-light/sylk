package shared

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/versioning"
)

// WorkspacePromptOptions controls how workspace-layer guidance is rendered into
// an agent system prompt.
type WorkspacePromptOptions struct {
	DefaultView     versioning.WorkspaceView
	IncludePipeline bool
}

// AppendWorkspaceViewContext appends a bounded explanation of the three
// workspace layers and the explicit view-aware skills agents can use to
// compare them.
func AppendWorkspaceViewContext(base string, opts WorkspacePromptOptions) string {
	section := BuildWorkspaceViewContext(opts)
	if section == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return section
	}
	return base + "\n\n---\n\n" + section
}

// BuildWorkspaceViewContext renders the workspace-layer contract. Disk remains
// the committed source of truth for the actual codebase state.
func BuildWorkspaceViewContext(opts WorkspacePromptOptions) string {
	defaultView := opts.DefaultView
	if defaultView == "" {
		defaultView = versioning.WorkspaceViewDisk
	}
	var b strings.Builder
	b.WriteString("# Workspace Layers\n\n")
	b.WriteString("Treat on-disk files as the committed source of truth for the actual codebase. Session and pipeline overlays are in-progress working state layered on top.\n")
	b.WriteString("\n- Disk: committed repository state on disk. This is the authoritative actual codebase state.\n")
	b.WriteString("- Global VFS: session-scoped merged but uncommitted work.\n")
	if opts.IncludePipeline {
		b.WriteString("- Pipeline VFS: task-scoped unmerged in-progress work for the active pipeline.\n")
	}
	b.WriteString(fmt.Sprintf("\nDefault read/write tools for this agent operate on the `%s` view unless a tool explicitly says otherwise.\n", defaultView))
	b.WriteString("Brokered execution tools such as `run_command` and `run_shell_script` execute against that same layered workspace view when strict execution is available, so they can read VFS-backed files before those changes are committed to disk.\n")
	b.WriteString("Install/remediation tools such as `install_dependency_tooling` and `install_test_tooling` are the exception: they execute approved commands against the real disk workspace so dependencies and utilities persist outside VFS overlays.\n")
	b.WriteString("Use `read_workspace_file`, `workspace_glob`, `workspace_grep`, `inspect_workspace_state`, and `summarize_workspace_state` whenever you need to compare disk against merged or task-local in-progress work.\n")
	b.WriteString("If live disk and overlay state disagree, reconcile that path before recreating work or issuing another validation loop.\n")
	b.WriteString("Before mutating VFS-backed files, use the matching `prepare_*_write_context` skill, inspect the returned diffs/basis, and then call the corresponding `write_*`, `edit_*`, `delete_*`, or `create_*_directory` skill with that basis.\n")
	return strings.TrimSpace(b.String())
}

// BuildTaskWorkspaceRuntimeContext renders a bounded live workspace snapshot for
// the task surface so pipeline agents do not need to discover layer deltas from
// scratch on every turn.
func BuildTaskWorkspaceRuntimeContext(
	ctx context.Context,
	views versioning.WorkspaceViewAccess,
	task *PipelineTaskInput,
) string {
	if views == nil || task == nil {
		return ""
	}
	paths := collectTaskWorkspacePaths(task)
	if len(paths) == 0 {
		return ""
	}
	summary, err := views.SummarizePaths(ctx, paths, strings.TrimSpace(task.TaskID))
	if err != nil {
		return "## Workspace Snapshot\n\n- unavailable: " + strings.TrimSpace(err.Error())
	}
	return FormatWorkspaceSummary(summary)
}

// FormatWorkspaceSummary renders a structured workspace summary as markdown.
func FormatWorkspaceSummary(summary *versioning.WorkspaceSummary) string {
	if summary == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Workspace Snapshot\n\n")
	writeListSection(&b, "Paths Considered", summary.Paths)
	writeListSection(&b, "Views Available", workspaceViewNames(summary.ViewsAvailable))
	writeListSection(&b, "Views Unavailable", workspaceViewNames(summary.ViewsUnavailable))
	writeListSection(&b, "Global Changed vs Disk", summary.GlobalChangedPaths)
	writeListSection(&b, "Pipeline Changed vs Disk", summary.PipelineChangedPaths)
	writeListSection(&b, "Pipeline Changed vs Global", summary.PipelineChangedFromGlobal)
	writeListSection(&b, "Paths With Unavailable Views", summary.PathsWithUnavailableViews)
	writeListSection(&b, "Paths With Errors", summary.PathsWithErrors)
	writeListSection(&b, "Path Reconciliation Alerts", workspaceReconciliationAlerts(summary))
	return strings.TrimSpace(b.String())
}

func workspaceReconciliationAlerts(summary *versioning.WorkspaceSummary) []string {
	if summary == nil || len(summary.Entries) == 0 {
		return nil
	}
	alerts := make([]string, 0, len(summary.Entries))
	for _, entry := range summary.Entries {
		if alert := workspaceReconciliationAlert(entry, summary.DefaultView); alert != "" {
			alerts = append(alerts, alert)
		}
	}
	return alerts
}

func workspaceReconciliationAlert(entry versioning.WorkspacePathState, defaultView versioning.WorkspaceView) string {
	notes := make([]string, 0, 4)
	if entry.GlobalDiffKnown && entry.GlobalDiffersFromDisk {
		notes = append(notes, "global differs from disk")
	}
	if entry.PipelineDiffFromDiskKnown && entry.PipelineDiffersFromDisk {
		notes = append(notes, "pipeline differs from disk")
	}
	if entry.PipelineDiffFromGlobalKnown && entry.PipelineDiffersFromGlobal {
		notes = append(notes, "pipeline differs from global")
	}
	if len(entry.ViewsUnavailable) > 0 {
		notes = append(notes, fmt.Sprintf("unavailable views: %s", strings.Join(workspaceViewNames(entry.ViewsUnavailable), ", ")))
	}
	if active := workspaceActiveViewAlert(entry, defaultView); active != "" {
		notes = append(notes, active)
	}
	if len(notes) == 0 {
		return ""
	}
	return fmt.Sprintf("%s — %s", entry.Path, strings.Join(notes, "; "))
}

func workspaceActiveViewAlert(entry versioning.WorkspacePathState, defaultView versioning.WorkspaceView) string {
	switch defaultView {
	case versioning.WorkspaceViewPipeline:
		if entry.PipelineDiffFromDiskKnown && entry.PipelineDiffersFromDisk {
			return "active pipeline view shadows live disk; reconcile before duplicating disk-written work"
		}
	case versioning.WorkspaceViewGlobal:
		if entry.GlobalDiffKnown && entry.GlobalDiffersFromDisk {
			return "active global view shadows live disk; reconcile before duplicating disk-written work"
		}
	}
	return ""
}

func collectTaskWorkspacePaths(task *PipelineTaskInput) []string {
	if task == nil {
		return nil
	}
	paths := make([]string, 0, 16)
	paths = append(paths, extractAffectedPaths(task.Context)...)
	paths = append(paths, decodeWorkspacePaths(task.Context, "read_set")...)
	paths = append(paths, decodeWorkspacePaths(task.Context, "write_set")...)
	paths = append(paths, decodeWorkspacePaths(task.Context, "test_surface")...)
	paths = append(paths, decodeWorkspacePaths(task.Context, "prefetch_paths")...)
	if packet := decodeWorkerPacket(task.Context, PipelineWorkerType(task)); len(packet) > 0 {
		paths = append(paths, decodeAnyStringList(packet["read_set"])...)
		paths = append(paths, decodeAnyStringList(packet["write_set"])...)
	}
	return uniqueTaskWorkspacePaths(paths)
}

func uniqueTaskWorkspacePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	sort.Strings(result)
	return result
}

func workspaceViewNames(views []versioning.WorkspaceView) []string {
	if len(views) == 0 {
		return nil
	}
	result := make([]string, 0, len(views))
	for _, view := range views {
		if trimmed := strings.TrimSpace(string(view)); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	sort.Strings(result)
	return result
}
