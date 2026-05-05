package versioning

import (
	"context"
	"io/fs"
)

// ReadOnlyFileAccess is the read-only subset of FileAccess. Agents that
// hold a value of this type literally cannot call WriteFile / EditFile
// / DeleteFile / MkdirAll because those methods are not in this
// interface — compile-time enforcement of read-only access, with no
// runtime IsReadOnly check or upcast path.
//
// FileAccess embeds this interface, so any FileAccess value satisfies
// ReadOnlyFileAccess by Go's structural typing. The injection layer
// (volume_manager.InjectFileAccess) decides which interface to hand to
// each agent based on the consumer interface they implement.
type ReadOnlyFileAccess interface {
	// ReadFile reads the entire contents of a file.
	ReadFile(ctx context.Context, path string) ([]byte, error)

	// Exists reports whether a file exists at the given path.
	Exists(ctx context.Context, path string) (bool, error)

	// ListDir returns directory entries for the given directory.
	ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error)

	// Glob finds files matching a glob pattern under root, excluding paths
	// that match any of the exclude patterns.
	Glob(ctx context.Context, root, pattern string, exclude []string) ([]string, error)

	// Grep searches file contents under root for a regex pattern. Returns
	// up to maxMatches results. The include filter restricts to files
	// matching the given name pattern (e.g. "*.go").
	Grep(ctx context.Context, root, pattern, include string, contextLines, maxMatches int) ([]GrepMatch, error)

	// Stat returns file info for the given path.
	Stat(ctx context.Context, path string) (fs.FileInfo, error)

	// WorkingDir returns the base working directory.
	WorkingDir() string
}

// FileAccess abstracts file operations for agents. Implementations provide
// direct disk I/O, per-pipeline in-memory VFS overlay, or per-session global
// in-memory overlay access depending on the agent's role in the system.
//
// Embeds ReadOnlyFileAccess so a FileAccess can always be downcast to a
// read-only handle without copy or wrapper, but a ReadOnlyFileAccess
// cannot be upcast — there is no method set that lets a holder reach
// the write methods.
type FileAccess interface {
	ReadOnlyFileAccess

	// MkdirAll creates a directory path and any missing parents.
	MkdirAll(ctx context.Context, path string) error

	// WriteFile writes content to a file, creating it if it does not exist.
	WriteFile(ctx context.Context, path string, content []byte) error

	// EditFile applies search-and-replace edits to an existing file.
	EditFile(ctx context.Context, path string, edits []FileEdit) error

	// DeleteFile removes a file.
	DeleteFile(ctx context.Context, path string) error

	// IsReadOnly reports whether this access layer permits writes.
	// Retained for back-compat with consumers that still use the
	// runtime check; new code should prefer asking for
	// ReadOnlyFileAccess at injection time.
	IsReadOnly() bool
}

// FileEdit describes a single search-and-replace operation within a file.
type FileEdit struct {
	OldText string
	NewText string
}

// GrepMatch describes a single search result from Grep.
type GrepMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
	Context string `json:"context,omitempty"`
}

// FileAccessConsumer is implemented by agents that receive FileAccess at
// runtime rather than at creation time. Pipeline agents receive per-pipeline
// VFS access when dispatched; global agents receive per-session global-overlay
// access when a session starts.
type FileAccessConsumer interface {
	SetFileAccess(fa FileAccess)
}

// ReadOnlyFileAccessConsumer is implemented by agents that need only
// read access to the workspace (librarian, archivalist read paths,
// observational inspectors). Implementing this interface instead of
// FileAccessConsumer is a load-bearing safety guarantee: the agent
// can never call WriteFile / EditFile / DeleteFile / MkdirAll because
// the injected handle does not expose those methods.
//
// An agent must implement EITHER FileAccessConsumer OR
// ReadOnlyFileAccessConsumer, not both. If both are implemented, the
// injection layer prefers the writable path (FileAccessConsumer), which
// means the read-only declaration is silently downgraded — this is a
// correctness bug, not a security feature, so the type system makes it
// awkward to do accidentally.
type ReadOnlyFileAccessConsumer interface {
	SetReadOnlyFileAccess(fa ReadOnlyFileAccess)
}

// WorkspaceViewsConsumer is implemented by agents that can inspect multiple
// workspace layers explicitly: committed disk, session-global overlay, and
// task-local pipeline overlay.
type WorkspaceViewsConsumer interface {
	SetWorkspaceViews(views WorkspaceViewAccess)
}
