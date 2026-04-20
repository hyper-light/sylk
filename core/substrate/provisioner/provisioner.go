// Package provisioner is the language-agnostic facade the substrate
// exposes to agent skills.
//
// Agent-facing install skills (install_test_tooling,
// install_dependency_tooling) call into a Provisioner with structured
// coordinates — (ecosystem, name, version) — and the Provisioner:
//
//  1. Dispatches to the ecosystem plugin registered in the
//     ecosystem.Registry to build a recipe. The substrate itself knows
//     nothing about pip, npm, cargo, gem, go, maven, hex, opam, nuget,
//     composer, or any other ecosystem; that knowledge lives entirely
//     behind the Ecosystem interface.
//  2. Hands the recipe to provision.Runtime, which fetches, verifies,
//     extracts, and registers a manifest.
//  3. Records a lockfile pin so subsequent pipelines see the tool as
//     attached.
//
// The Provisioner is how install_* skills stop being shell wrappers
// around `pip install` (or `npm install`, or `cargo install`, etc.)
// and start being substrate-routed operations that respect the no-disk
// invariant. When no plugin is registered for the requested ecosystem,
// the Provisioner surfaces a structured error that the install skill
// can translate into agent-readable recovery guidance — typically a
// fall-through to the legacy broker path.
package provisioner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/substrate/ecosystem"
	"github.com/adalundhe/sylk/core/substrate/lock"
	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/provision"
	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// Provisioner ensures a tool is installed in the substrate. Safe for
// concurrent use.
type Provisioner interface {
	// Provision resolves the given coordinates to a recipe via the
	// ecosystem plugin registry, fetches + registers it into the
	// substrate if necessary, pins a witness on the session's
	// lockfile, and returns the resulting manifest ID.
	//
	// Idempotent: when the manifest is already registered for
	// (ecosystem, name, version), Provision short-circuits to the
	// existing ID and records the witness only.
	Provision(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error)
}

// ProvisionRequest is the structured install request.
type ProvisionRequest struct {
	// Ecosystem identifies the plugin in the ecosystem.Registry.
	// Whatever Name() the plugin reports is what the install skill (or
	// the registry's Parse helper) must put here.
	Ecosystem string

	// Name is the package name within the ecosystem.
	Name string

	// Version is the concrete version to pin. Empty selects the
	// registry's latest stable release — the plugin's recipe builder
	// resolves it.
	Version string

	// Constraint is the ecosystem-native version constraint recorded
	// as the lockfile witness's constraint string (informational —
	// the resolution uses Version). Optional.
	Constraint string

	// Platform is the host platform tuple, used by the plugin for
	// artifact selection (e.g. picking the right wheel, native gem,
	// or platform-specific binary).
	Platform recipe.Platform

	// SessionID scopes the lockfile witness.
	SessionID string

	// PipelineID identifies the witnessing pipeline.
	PipelineID string
}

// ProvisionResult reports what Provision did.
type ProvisionResult struct {
	// ManifestID is the substrate manifest the coordinates resolved to.
	ManifestID manifest.ID

	// PackageID is the canonical coordinate of the manifest
	// (ecosystem / name / resolved version).
	PackageID manifest.PackageID

	// AlreadyProvisioned is true when Provision short-circuited to an
	// existing manifest. False when the provisioning runtime actually
	// fetched + extracted + registered.
	AlreadyProvisioned bool

	// WitnessAdded is true when a new witness was appended to the
	// lockfile pin (false when the pin already had a witness from
	// this pipeline with the same constraint).
	WitnessAdded bool

	// BytesFetched, BlobsCreated, BlobsReused: substrate provisioning
	// metrics, zero when AlreadyProvisioned is true.
	BytesFetched int64
	BlobsCreated int
	BlobsReused  int

	// Elapsed is the wall-clock duration of the operation.
	Elapsed time.Duration
}

// Config wires the Provisioner's collaborators.
type Config struct {
	// Runtime executes recipes.
	Runtime *provision.Runtime

	// Manifests holds substrate manifests.
	Manifests *manifest.Store

	// LockfileResolver returns the lockfile for a session.
	LockfileResolver func(sessionID string) (*lock.Lockfile, bool)

	// Ecosystems is the language-agnostic plugin registry. The
	// Provisioner dispatches recipe construction through it; an empty
	// registry means every Provision call returns ErrUnsupportedEcosystem
	// and the install skill falls through to its legacy path.
	Ecosystems *ecosystem.Registry

	// MaxFetchBytes caps substrate fetches. Zero disables the cap.
	MaxFetchBytes int64
}

// New constructs a Provisioner.
func New(cfg Config) (Provisioner, error) {
	if cfg.Runtime == nil {
		return nil, fmt.Errorf("provisioner: provision runtime is required")
	}
	if cfg.Manifests == nil {
		return nil, fmt.Errorf("provisioner: manifest store is required")
	}
	if cfg.LockfileResolver == nil {
		return nil, fmt.Errorf("provisioner: lockfile resolver is required")
	}
	if cfg.Ecosystems == nil {
		return nil, fmt.Errorf("provisioner: ecosystem registry is required")
	}
	return &runtimeProvisioner{
		runtime:       cfg.Runtime,
		manifests:     cfg.Manifests,
		lockfiles:     cfg.LockfileResolver,
		ecosystems:    cfg.Ecosystems,
		maxFetchBytes: cfg.MaxFetchBytes,
	}, nil
}

type runtimeProvisioner struct {
	runtime       *provision.Runtime
	manifests     *manifest.Store
	lockfiles     func(sessionID string) (*lock.Lockfile, bool)
	ecosystems    *ecosystem.Registry
	maxFetchBytes int64
}

func (p *runtimeProvisioner) Provision(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error) {
	if err := preflight(req); err != nil {
		return nil, err
	}
	if _, ok := p.ecosystems.Lookup(req.Ecosystem); !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedEcosystem, req.Ecosystem)
	}
	start := time.Now()
	existing, err := p.maybeShortCircuit(req)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		witnessAdded, err := p.pinWitness(req, existing.ID)
		if err != nil {
			return nil, err
		}
		return &ProvisionResult{
			ManifestID:         existing.ID,
			PackageID:          existing.Package,
			AlreadyProvisioned: true,
			WitnessAdded:       witnessAdded,
			Elapsed:            time.Since(start),
		}, nil
	}
	rec, err := p.ecosystems.BuildRecipe(ctx, ecosystem.BuildRecipeRequest{
		Coords: ecosystem.Coordinates{
			Ecosystem:  req.Ecosystem,
			Name:       req.Name,
			Version:    req.Version,
			Constraint: req.Constraint,
		},
		Platform: req.Platform,
	})
	if err != nil {
		if errors.Is(err, ecosystem.ErrUnknownEcosystem) {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedEcosystem, req.Ecosystem)
		}
		return nil, fmt.Errorf("provisioner: build recipe: %w", err)
	}
	result, err := p.runtime.Provision(ctx, provision.ProvisionRequest{
		Recipe:        rec,
		Platform:      req.Platform,
		MaxFetchBytes: p.maxFetchBytes,
		PackageOverride: manifest.PackageID{
			Ecosystem: canonicalEcosystem(req.Ecosystem),
			Name:      req.Name,
			Version:   resolvedVersion(req.Version, rec),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("provisioner: runtime provision: %w", err)
	}
	witnessAdded, err := p.pinWitness(req, result.ManifestID)
	if err != nil {
		return nil, err
	}
	return &ProvisionResult{
		ManifestID:   result.ManifestID,
		PackageID:    result.PackageID,
		WitnessAdded: witnessAdded,
		BytesFetched: result.BytesFetched,
		BlobsCreated: result.BlobsCreated,
		BlobsReused:  result.BlobsReused,
		Elapsed:      time.Since(start),
	}, nil
}

func preflight(req ProvisionRequest) error {
	if strings.TrimSpace(req.Ecosystem) == "" {
		return fmt.Errorf("provisioner: ecosystem is required")
	}
	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("provisioner: name is required")
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return fmt.Errorf("provisioner: session_id is required")
	}
	if strings.TrimSpace(req.PipelineID) == "" {
		return fmt.Errorf("provisioner: pipeline_id is required")
	}
	return nil
}

// maybeShortCircuit returns the existing manifest when one is already
// registered for (ecosystem, name, version). When Version is empty
// (latest requested), we cannot short-circuit — "latest" is moving,
// and the registry is the authoritative resolver.
func (p *runtimeProvisioner) maybeShortCircuit(req ProvisionRequest) (*manifest.Manifest, error) {
	if strings.TrimSpace(req.Version) == "" {
		return nil, nil
	}
	pkg := manifest.PackageID{
		Ecosystem: canonicalEcosystem(req.Ecosystem),
		Name:      req.Name,
		Version:   req.Version,
	}
	existing, ok := p.manifests.GetByPackage(pkg)
	if !ok {
		return nil, nil
	}
	return existing, nil
}

func (p *runtimeProvisioner) pinWitness(req ProvisionRequest, manifestID manifest.ID) (bool, error) {
	lockfile, ok := p.lockfiles(req.SessionID)
	if !ok {
		// No lockfile — provisioning succeeded but the witness won't
		// be recorded. This is a recoverable configuration mismatch,
		// not a substrate failure; callers get the manifest ID.
		return false, nil
	}
	outcome, err := lockfile.Pin(lock.PinRequest{
		Coordinate: lock.Coordinate{
			Ecosystem: canonicalEcosystem(req.Ecosystem),
			Name:      req.Name,
		},
		Version:    resolvedVersionFromReq(req),
		ManifestID: manifestID,
		Witness: lock.Witness{
			PipelineID: req.PipelineID,
			Constraint: req.Constraint,
		},
	})
	if err != nil {
		return false, fmt.Errorf("provisioner: pin witness: %w", err)
	}
	return outcome.WitnessAdded, nil
}

func resolvedVersionFromReq(req ProvisionRequest) string {
	if v := strings.TrimSpace(req.Version); v != "" {
		return v
	}
	return "latest"
}

func resolvedVersion(explicit string, rec *recipe.Recipe) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	if rec != nil && strings.TrimSpace(rec.Metadata.Version) != "" {
		return rec.Metadata.Version
	}
	return "latest"
}

// canonicalEcosystem normalizes the ecosystem identifier for storage
// in the manifest store and lockfile. The substrate keys on
// lower-case, trimmed names; plugins are free to accept aliases at
// parse time but the canonical form is what every internal index uses.
func canonicalEcosystem(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// Errors.
var (
	// ErrUnsupportedEcosystem: no plugin is registered with the
	// substrate ecosystem registry for the requested ecosystem name.
	// The install skill surfaces this with recovery guidance — usually
	// "fall back to the legacy broker path" — rather than failing the
	// install outright.
	ErrUnsupportedEcosystem = errors.New("provisioner: ecosystem not supported by the substrate")
)
