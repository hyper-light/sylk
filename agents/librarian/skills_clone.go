package librarian

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/skills"
)

// cloneRepositorySkill clones a remote git repository into the librarian's
// VFS-backed package store. Once cloned, the repository's files are searchable
// via the librarian's existing tools (grep, glob, read_file, find_symbol).
func cloneRepositorySkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("clone_repository").
		Description(
			"Clone a remote git repository into the local package store for code analysis. "+
				"The cloned code becomes searchable with grep, glob, read_file, and find_symbol. "+
				"Supports GitHub, GitLab, Bitbucket, Codeberg, and sr.ht. "+
				"Clones are shallow (depth=1) and size-bounded (100 MiB max).",
		).
		Domain("code").
		Keywords("clone", "fetch", "download", "repository", "repo", "package", "git", "remote").
		Priority(85).
		TokenEstimate(400).
		StringParam("url", "Repository URL (e.g. github.com/owner/repo, https://github.com/owner/repo.git)", true).
		StringParam("branch", "Branch to clone (default: default branch)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				URL    string `json:"url"`
				Branch string `json:"branch"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.URL) == "" {
				return nil, fmt.Errorf("url is required")
			}
			return l.executeClone(ctx, params.URL, params.Branch)
		}).
		Build()
}

// listPackagesSkill lists all cloned packages in the store.
func listPackagesSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("list_packages").
		Description(
			"List all remote packages that have been cloned into the local package store. "+
				"Shows repository name, URL, branch, file count, and clone time.",
		).
		Domain("code").
		Keywords("packages", "cloned", "repositories", "list", "remote").
		Priority(70).
		TokenEstimate(200).
		Handler(func(_ context.Context, _ json.RawMessage) (any, error) {
			return l.listPackages()
		}).
		Build()
}

// removePackageSkill removes a cloned package from the store.
func removePackageSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("remove_package").
		Description("Remove a previously cloned package from the local store.").
		Domain("code").
		Keywords("remove", "delete", "uninstall", "package").
		Priority(60).
		TokenEstimate(100).
		StringParam("package", "Package key (owner/repo) to remove", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Package string `json:"package"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Package) == "" {
				return nil, fmt.Errorf("package key is required")
			}
			return l.removePackage(params.Package)
		}).
		Build()
}

func (l *Librarian) executeClone(ctx context.Context, repoURL, branch string) (any, error) {
	l.mu.Lock()
	store := l.cloneStore
	l.mu.Unlock()

	if store == nil {
		return nil, fmt.Errorf("package store not initialized")
	}

	l.publishActivity(events.EventTypeAgentAction, fmt.Sprintf("Cloning %s", repoURL))

	entry, err := store.Clone(ctx, repoURL, branch)
	if err != nil {
		l.publishActivity(events.EventTypeAgentError, fmt.Sprintf("Clone failed: %v", err))
		return nil, fmt.Errorf("clone failed: %w", err)
	}

	l.publishActivity(events.EventTypeSuccess, fmt.Sprintf("Cloned %s/%s (%d files)", entry.Owner, entry.RepoName, entry.FileCount))

	// Clone testament.
	l.librarianSubmitTestament(ctx, l.librarianTestament(
		fmt.Sprintf("Cloned %s/%s: %d files", entry.Owner, entry.RepoName, entry.FileCount),
		"committed",
		[]*claims.Artifact{
			l.librarianArtifact("url", entry.URL),
			l.librarianArtifact("owner", entry.Owner),
			l.librarianArtifact("repo", entry.RepoName),
			l.librarianArtifact("commit", entry.CommitHash),
			l.librarianArtifact("file_count", fmt.Sprintf("%d", entry.FileCount)),
		},
	))

	return map[string]any{
		"success":     true,
		"package":     entry.ID,
		"url":         entry.URL,
		"owner":       entry.Owner,
		"repo":        entry.RepoName,
		"branch":      entry.Branch,
		"commit":      entry.CommitHash,
		"file_count":  entry.FileCount,
		"total_bytes": entry.TotalBytes,
		"disk_path":   entry.DiskPath,
		"duration":    entry.Duration.String(),
		"hint":        fmt.Sprintf("Files are now searchable. Use grep/glob/read_file with path prefix '.sylk/packages/%s/%s/' to explore.", entry.Owner, entry.RepoName),
	}, nil
}

func (l *Librarian) listPackages() (any, error) {
	l.mu.Lock()
	store := l.cloneStore
	l.mu.Unlock()

	if store == nil {
		return map[string]any{"packages": []any{}, "count": 0}, nil
	}

	entries := store.List()
	packages := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		packages = append(packages, map[string]any{
			"package":     e.ID,
			"url":         e.URL,
			"branch":      e.Branch,
			"commit":      e.CommitHash,
			"file_count":  e.FileCount,
			"total_bytes": e.TotalBytes,
			"disk_path":   e.DiskPath,
			"cloned_at":   e.ClonedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	return map[string]any{
		"packages": packages,
		"count":    len(packages),
		"base_dir": store.BaseDir(),
	}, nil
}

func (l *Librarian) removePackage(key string) (any, error) {
	l.mu.Lock()
	store := l.cloneStore
	l.mu.Unlock()

	if store == nil {
		return nil, fmt.Errorf("package store not initialized")
	}

	if err := store.Remove(key); err != nil {
		return nil, err
	}

	return map[string]any{
		"success": true,
		"removed": key,
	}, nil
}
