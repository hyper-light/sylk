// Package substrate is the Tool VFS — an in-memory, content-addressed,
// hermetic, language-agnostic place where tools, compilers, libraries,
// runtimes, and resources are installed for agent use.
//
// The substrate is strictly no-disk for content. Bytes never persist to host
// disk; they live in process memory, served through FUSE projections,
// replayable from registries on restart. Install once, share across every
// pipeline. Fully self-contained on Linux, macOS, and Windows.
//
// See docs/TOOL_VFS.md for the full design, phase plan, recipe examples,
// and architectural rationale.
package substrate
