// Package exec is the substrate-aware execution backend.
//
// The Backend ties the substrate's content layers together at execution
// time:
//
//   1. Resolves required tools from the pipeline's lockfile
//   2. Looks up the corresponding substrate manifests
//   3. Composes a View from the manifest set + ecosystem contributions
//   4. Configures the spawned process's environment from the View's
//      5-layer env stack
//   5. Returns a SpawnPlan the host's actual process-broker / sandbox
//      consumes
//
// Phase 6 produces the SpawnPlan; Phase 2.5 (sandbox) is the consumer
// that turns the plan into a running process. Until Phase 2.5 lands the
// Backend's Spawn method returns the plan along with a typed error
// pointing at the missing capability — the broker can already validate
// view composition end-to-end without the sandbox runtime.
package exec

import (
	"errors"
	"time"

	"github.com/adalundhe/sylk/core/substrate/lock"
	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/recipe"
	"github.com/adalundhe/sylk/core/substrate/view"
)

// SpawnRequest is the input the substrate receives from an upstream
// caller (typically an agent skill or pipeline action).
type SpawnRequest struct {
	// PipelineID owns the resulting View and is the lockfile witness.
	// Required.
	PipelineID string

	// SessionID identifies the lockfile this request consults.
	// Required.
	SessionID string

	// Cmd is the argv to execute. Cmd[0] is resolved against the View's
	// PATH at composition time; if not found, Spawn returns
	// ErrToolNotProvisioned with the unsatisfied tool name.
	Cmd []string

	// Cwd is the working directory inside the spawned process's view
	// of the filesystem. Defaults to the View's MountRoot.
	Cwd string

	// Env supplies additional environment variables that compose with
	// the View's env stack (substrate first, request env appended).
	// Used for one-off env tweaks that don't belong in the View itself.
	Env map[string]string

	// Platform is the host platform tuple. Required.
	Platform recipe.Platform

	// Timeout caps wall-clock time for the spawned process. Zero
	// disables the cap; the sandbox's own resource governor still
	// applies.
	Timeout time.Duration

	// RequiredTools, when non-empty, is the explicit list of tool
	// coordinates the spawn requires. When empty, the Backend infers
	// the set from Cmd[0] using a best-effort tool-name → coordinate
	// lookup against the lockfile. Explicit RequiredTools is the
	// preferred pattern; the inference path exists for ergonomic
	// "substrate.Run('pytest')" calls.
	RequiredTools []ToolRequirement
}

// ToolRequirement names one tool a SpawnRequest depends on.
type ToolRequirement struct {
	Coordinate lock.Coordinate
	Constraint string // optional; lockfile's existing pin is honored when present
}

// SpawnPlan is the substrate's contribution to actually running the
// process. The host's sandbox/process-broker consumes the plan and
// returns the running Process; Phase 2.5 wires that consumer.
type SpawnPlan struct {
	// View is the composed projection the spawn runs against.
	View *view.View

	// Manifests is the closure of substrate manifests the View
	// includes. Each is already attached to the substrate's own
	// projection.Namespace; the spawn's caller can rely on the
	// underlying blobs being present.
	Manifests []*manifest.Manifest

	// Cmd, Cwd, Env, Timeout are passed through from the SpawnRequest
	// after View composition. Env is the composed env (View stack +
	// request additions, in that order).
	Cmd     []string
	Cwd     string
	Env     map[string]string
	Timeout time.Duration

	// Platform is the same as the request's Platform.
	Platform recipe.Platform
}

// SpawnResult is the outcome the sandbox returns after running the
// SpawnPlan. Phase 6 defines the type; Phase 2.5 produces it.
type SpawnResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Duration time.Duration
}

// Errors.
var (
	// ErrEmptyPipelineID, ErrEmptySessionID, ErrEmptyCmd: missing
	// required SpawnRequest fields.
	ErrEmptyPipelineID = errors.New("substrate exec: pipeline ID required")
	ErrEmptySessionID  = errors.New("substrate exec: session ID required")
	ErrEmptyCmd        = errors.New("substrate exec: empty Cmd argv")
	ErrEmptyPlatform   = errors.New("substrate exec: platform tuple required")

	// ErrToolNotProvisioned: the spawned command needs a tool that is
	// not in the lockfile. The Backend surfaces the unsatisfied
	// coordinate so callers can turn it into a `provision_tool_dependency`
	// action.
	ErrToolNotProvisioned = errors.New("substrate exec: required tool is not provisioned")

	// ErrNoLockfile: the SessionID has no associated lockfile yet.
	// Caller should pin the required tools before retrying.
	ErrNoLockfile = errors.New("substrate exec: no lockfile for session")

	// ErrSandboxNotWired: the SpawnPlan was constructed successfully
	// but the sandbox runtime (Phase 2.5) is not yet installed. The
	// caller can inspect the returned plan even when this is returned
	// — the substrate has done its part of the work.
	ErrSandboxNotWired = errors.New("substrate exec: sandbox runtime not wired (Phase 2.5)")
)
