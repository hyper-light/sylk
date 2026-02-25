// Package inspector provides the root package for the Inspector agent system.
// The actual implementations live in sub-packages:
//
//   - inspector/shared:   Shared types, tools, skills, and streaming infrastructure
//   - inspector/pipeline: Per-task pipeline inspector (TDD phases 1 & 4)
//   - inspector/global:   Cross-file architectural quality auditor
//
// This root package is intentionally empty. All consumers should import
// the appropriate sub-package directly.
package inspector
