package versioning

import (
	"context"
	"io/fs"

	"github.com/adalundhe/sylk/core/activity"
)

// Instrument wraps any FileAccess implementation with Activity Fabric
// chokepoint instrumentation. Every workspace file operation flows
// through this single wrapper, so the fabric captures every read,
// write, edit, delete, and metadata probe with FabricContext-driven
// causal linking — without each underlying FileAccess implementation
// needing its own emission code.
//
// Reads are emitted at Atomic resolution (in-memory ring buffer only).
// Mutations are emitted at Fine resolution (durable for 24h plus the
// hot Ristretto cache) so file provenance is queryable post-hoc.
//
// If the wrapped impl is nil, Instrument returns nil — there's no
// chokepoint without a backing implementation.
func Instrument(fa FileAccess) FileAccess {
	if fa == nil {
		return nil
	}
	if _, already := fa.(*instrumentedFileAccess); already {
		return fa
	}
	return &instrumentedFileAccess{wrapped: fa}
}

// instrumentedFileAccess is the chokepoint wrapper. Every method
// brackets the underlying call in StartSpan / Span.End so failure
// paths are tagged and causal context propagates.
type instrumentedFileAccess struct {
	wrapped FileAccess
}

// Underlying unwraps any chain of fabric-instrumentation wrappers and
// returns the innermost FileAccess. Used by callers that type-assert
// for capability detection (e.g., distinguishing *DiskFileAccess from
// in-memory VFS to choose execution semantics). Safe with nil.
//
// All capability-detection type assertions in the codebase should call
// Underlying first, so the fabric wrapper is transparent to capability
// dispatch while still capturing every operation.
func Underlying(fa FileAccess) FileAccess {
	for {
		w, ok := fa.(*instrumentedFileAccess)
		if !ok || w == nil {
			return fa
		}
		fa = w.wrapped
	}
}

func (f *instrumentedFileAccess) ReadFile(ctx context.Context, path string) ([]byte, error) {
	span := activity.StartSpan(ctx, activity.ActionFileRead, activity.Subject{
		PathPrefix: path,
	})
	defer span.End()
	span.SetAttribute("path", path)
	data, err := f.wrapped.ReadFile(span.Context(), path)
	if err != nil {
		span.EndWithError(err)
		return nil, err
	}
	span.SetAttribute("bytes", len(data))
	return data, nil
}

func (f *instrumentedFileAccess) MkdirAll(ctx context.Context, path string) error {
	span := activity.StartSpan(ctx, activity.ActionFileWritten, activity.Subject{
		PathPrefix: path,
	})
	defer span.End()
	span.SetAttribute("path", path)
	span.SetAttribute("op", "mkdir_all")
	if err := f.wrapped.MkdirAll(span.Context(), path); err != nil {
		span.EndWithError(err)
		return err
	}
	return nil
}

func (f *instrumentedFileAccess) WriteFile(ctx context.Context, path string, content []byte) error {
	span := activity.StartSpan(ctx, activity.ActionFileWritten, activity.Subject{
		PathPrefix: path,
	})
	defer span.End()
	span.SetAttribute("path", path)
	span.SetAttribute("op", "write")
	span.SetAttribute("bytes", len(content))
	if err := f.wrapped.WriteFile(span.Context(), path, content); err != nil {
		span.EndWithError(err)
		return err
	}
	return nil
}

func (f *instrumentedFileAccess) EditFile(ctx context.Context, path string, edits []FileEdit) error {
	span := activity.StartSpan(ctx, activity.ActionFileWritten, activity.Subject{
		PathPrefix: path,
	})
	defer span.End()
	span.SetAttribute("path", path)
	span.SetAttribute("op", "edit")
	span.SetAttribute("edits", len(edits))
	if err := f.wrapped.EditFile(span.Context(), path, edits); err != nil {
		span.EndWithError(err)
		return err
	}
	return nil
}

func (f *instrumentedFileAccess) DeleteFile(ctx context.Context, path string) error {
	span := activity.StartSpan(ctx, activity.ActionFileDeleted, activity.Subject{
		PathPrefix: path,
	})
	defer span.End()
	span.SetAttribute("path", path)
	if err := f.wrapped.DeleteFile(span.Context(), path); err != nil {
		span.EndWithError(err)
		return err
	}
	return nil
}

func (f *instrumentedFileAccess) Exists(ctx context.Context, path string) (bool, error) {
	// Probes are Atomic-resolution reads — same as ReadFile.
	span := activity.StartSpan(ctx, activity.ActionFileRead, activity.Subject{
		PathPrefix: path,
	})
	defer span.End()
	span.SetAttribute("path", path)
	span.SetAttribute("op", "exists")
	exists, err := f.wrapped.Exists(span.Context(), path)
	if err != nil {
		span.EndWithError(err)
		return false, err
	}
	span.SetAttribute("exists", exists)
	return exists, nil
}

func (f *instrumentedFileAccess) ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	span := activity.StartSpan(ctx, activity.ActionFileRead, activity.Subject{
		PathPrefix: dir,
	})
	defer span.End()
	span.SetAttribute("path", dir)
	span.SetAttribute("op", "list_dir")
	entries, err := f.wrapped.ListDir(span.Context(), dir)
	if err != nil {
		span.EndWithError(err)
		return nil, err
	}
	span.SetAttribute("entries", len(entries))
	return entries, nil
}

func (f *instrumentedFileAccess) Glob(ctx context.Context, root, pattern string, exclude []string) ([]string, error) {
	span := activity.StartSpan(ctx, activity.ActionFileRead, activity.Subject{
		PathPrefix: root,
	})
	defer span.End()
	span.SetAttribute("root", root)
	span.SetAttribute("pattern", pattern)
	span.SetAttribute("op", "glob")
	matches, err := f.wrapped.Glob(span.Context(), root, pattern, exclude)
	if err != nil {
		span.EndWithError(err)
		return nil, err
	}
	span.SetAttribute("matches", len(matches))
	return matches, nil
}

func (f *instrumentedFileAccess) Grep(ctx context.Context, root, pattern, include string, contextLines, maxMatches int) ([]GrepMatch, error) {
	span := activity.StartSpan(ctx, activity.ActionFileRead, activity.Subject{
		PathPrefix: root,
	})
	defer span.End()
	span.SetAttribute("root", root)
	span.SetAttribute("pattern", pattern)
	span.SetAttribute("op", "grep")
	matches, err := f.wrapped.Grep(span.Context(), root, pattern, include, contextLines, maxMatches)
	if err != nil {
		span.EndWithError(err)
		return nil, err
	}
	span.SetAttribute("matches", len(matches))
	return matches, nil
}

func (f *instrumentedFileAccess) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	span := activity.StartSpan(ctx, activity.ActionFileRead, activity.Subject{
		PathPrefix: path,
	})
	defer span.End()
	span.SetAttribute("path", path)
	span.SetAttribute("op", "stat")
	info, err := f.wrapped.Stat(span.Context(), path)
	if err != nil {
		span.EndWithError(err)
		return nil, err
	}
	return info, nil
}

func (f *instrumentedFileAccess) WorkingDir() string {
	return f.wrapped.WorkingDir()
}

func (f *instrumentedFileAccess) IsReadOnly() bool {
	return f.wrapped.IsReadOnly()
}

// Modifications forwards the optional overlay-aware capability so
// callers that type-assert for an `interface { Modifications() []FileModification }`
// shape continue to work transparently through the wrapper. Returns
// nil when the wrapped impl doesn't expose modifications (i.e., when
// the wrapped impl is disk-backed rather than overlay-backed).
//
// Without this forwarder, every call site in the inspector / tester
// agents that does `fa.(overlayAwareFileAccess)` would silently
// degrade to "no overlay" once the wrapper is in place — visible
// behavior change. With the forwarder, the wrapper is transparent.
func (f *instrumentedFileAccess) Modifications() []FileModification {
	type overlayAware interface {
		Modifications() []FileModification
	}
	if oa, ok := f.wrapped.(overlayAware); ok {
		return oa.Modifications()
	}
	return nil
}

// Compile-time assertion.
var _ FileAccess = (*instrumentedFileAccess)(nil)
