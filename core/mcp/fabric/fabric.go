package fabric

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp/catalog"
	"github.com/adalundhe/sylk/core/mcp/client"
	"github.com/adalundhe/sylk/core/mcp/durable"
	"github.com/adalundhe/sylk/core/mcp/runtime"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// FabricConfig configures the top-level Fabric.
type FabricConfig struct {
	// SessionID is the active Sylk session. The journal is keyed off it.
	SessionID string
	// SessionDir is where the journal/blobs live.
	SessionDir string
	// Catalog is the layered server config.
	Catalog *catalog.Manager
	// Registry is the skill registry compiled MCP tools register into.
	Registry *skills.Registry
	// Guardian is the approval gate. Required for mutating tools.
	Guardian runtime.GuardianGate
	// Outcomes receives outcome signals (Forest, telemetry).
	Outcomes runtime.OutcomeListener
	// Ingester receives indexed content (Knowledge / Forest).
	Ingester runtime.ContentIngester
	// Scope is the parent scope; the fabric spawns its own children.
	Scope *concurrency.GoroutineScope
	// ReloadInterval is how often the catalog is rescanned for mtime changes.
	ReloadInterval time.Duration
}

// reloadInterval defaults to a value tuned to feel snappy in interactive
// flows without thrashing the fs. The constant is derived from the
// per-edit perception threshold (500ms) doubled for headroom.
const (
	defaultReloadInterval = 1 * time.Second
)

// Fabric is the entrypoint to Sylk's MCP capability plane. One Fabric per
// session; multiple agents share it.
type Fabric struct {
	cfg FabricConfig

	scope     *concurrency.GoroutineScope
	ownsScope bool

	journal    *durable.Journal
	projection *durable.Projection
	blobs      *durable.BlobStore

	connections *connectionManager
	runtime     *runtime.Runtime
	statusReg   *fabricRegistry

	startedMu sync.Mutex
	started   bool
	closeMu   sync.Mutex
	closed    bool
}

// NewFabric constructs a Fabric. Start must be called separately.
func NewFabric(cfg FabricConfig) (*Fabric, error) {
	if cfg.SessionID == "" {
		return nil, errors.New("mcp fabric: session id is required")
	}
	if cfg.SessionDir == "" {
		return nil, errors.New("mcp fabric: session dir is required")
	}
	if cfg.Registry == nil {
		return nil, errors.New("mcp fabric: skill registry is required")
	}
	if cfg.Catalog == nil {
		cfg.Catalog = catalog.NewManager()
	}
	if cfg.ReloadInterval <= 0 {
		cfg.ReloadInterval = defaultReloadInterval
	}
	scope, owns := scopeForFabric(context.Background(), "mcp.fabric."+cfg.SessionID, cfg.Scope)
	journal, err := durable.Open(durable.JournalConfig{SessionDir: cfg.SessionDir})
	if err != nil {
		return nil, fmtError("open journal: %w", err)
	}
	projection, err := durable.HydrateFromJournal(journal)
	if err != nil {
		_ = journal.Close()
		return nil, fmtError("hydrate projection: %w", err)
	}
	blobs, err := durable.NewBlobStore(cfg.SessionDir)
	if err != nil {
		_ = journal.Close()
		return nil, err
	}
	statusReg := newFabricRegistry()
	fabric := &Fabric{
		cfg:        cfg,
		scope:      scope,
		ownsScope:  owns,
		journal:    journal,
		projection: projection,
		blobs:      blobs,
		statusReg:  statusReg,
	}
	if err := fabric.initRuntime(); err != nil {
		_ = journal.Close()
		return nil, err
	}
	if err := fabric.initConnections(); err != nil {
		_ = journal.Close()
		return nil, err
	}
	return fabric, nil
}

func (f *Fabric) initRuntime() error {
	rt, err := runtime.New(runtime.Config{
		SessionID:       f.cfg.SessionID,
		Journal:         f.journal,
		Projection:      f.projection,
		Blobs:           f.blobs,
		SessionResolver: &sessionResolverAdapter{fabric: f},
		PolicyResolver:  &policyResolverAdapter{fabric: f},
		Guardian:        f.cfg.Guardian,
		Outcomes:        f.cfg.Outcomes,
		Ingester:        f.cfg.Ingester,
		Scope:           f.scope,
	})
	if err != nil {
		return err
	}
	f.runtime = rt
	return nil
}

func (f *Fabric) initConnections() error {
	cm, err := newConnectionManager(connectionManagerConfig{
		Scope:     f.scope,
		Registry:  f.cfg.Registry,
		StatusReg: f.statusReg,
		Executor:  f.runtime,
	})
	if err != nil {
		return err
	}
	f.connections = cm
	return nil
}

// Start brings up active connections from the catalog and spawns the
// reload watcher.
func (f *Fabric) Start(ctx context.Context) error {
	f.startedMu.Lock()
	defer f.startedMu.Unlock()
	if f.started {
		return errors.New("mcp fabric: already started")
	}
	if _, _, err := f.connections.Apply(ctx, f.cfg.Catalog.Effective()); err != nil {
		return err
	}
	if err := f.scope.Go("mcp.fabric.watcher", 0, f.runWatcher); err != nil {
		return fmtError("schedule watcher: %w", err)
	}
	f.started = true
	return nil
}

// Close tears down all connections and the journal.
func (f *Fabric) Close() error {
	f.closeMu.Lock()
	defer f.closeMu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	if f.connections != nil {
		f.connections.CloseAll()
	}
	if f.ownsScope && f.scope != nil {
		_ = f.scope.Shutdown(time.Second, 2*time.Second)
	}
	if f.journal != nil {
		_ = f.journal.Close()
	}
	return nil
}

// Status returns a snapshot of every server's diagnostic state.
func (f *Fabric) Status() []FabricStatus { return f.statusReg.snapshot() }

// Snapshots returns the current capability snapshots.
func (f *Fabric) Snapshots() []*client.CapabilitySnapshot {
	if f.connections == nil {
		return nil
	}
	return f.connections.Snapshot()
}

// Pending returns the operations the fabric considers in-flight or
// resumable. Used by an agent's restart-resume path to surface obligations.
func (f *Fabric) Pending() []durable.Operation { return f.projection.Pending() }

// AppendCatalogInvalidation marks a catalog as stale so the next list call
// triggers a re-sync. Wired to the client.Session's notification fan-out.
func (f *Fabric) AppendCatalogInvalidation(serverID string) {
	ev := durable.Event{Kind: durable.EventCatalogInvalidated, ServerID: serverID, OperationID: "catalog/" + serverID}
	_ = f.journal.Append(ev)
	f.projection.Apply(ev)
}

func (f *Fabric) runWatcher(ctx context.Context) error {
	ticker := time.NewTicker(f.cfg.ReloadInterval)
	defer ticker.Stop()
	catalogChan := f.cfg.Catalog.Watch()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			f.tickReload(ctx)
		case <-catalogChan:
			f.applyEffective(ctx)
		}
	}
}

func (f *Fabric) tickReload(ctx context.Context) {
	changed, err := f.cfg.Catalog.Reload()
	if err != nil || !changed {
		return
	}
	f.applyEffective(ctx)
}

func (f *Fabric) applyEffective(ctx context.Context) {
	_, _, _ = f.connections.Apply(ctx, f.cfg.Catalog.Effective())
}

// ExecuteTool is the public entrypoint a Sylk-side agent uses to invoke an
// MCP tool by canonical name. Mirrors the compiler.ToolExecutor signature.
func (f *Fabric) ExecuteTool(ctx context.Context, serverID, canonicalName string, arguments json.RawMessage) (any, error) {
	if f.runtime == nil {
		return nil, errors.New("mcp fabric: runtime not initialized")
	}
	return f.runtime.ExecuteTool(ctx, serverID, canonicalName, arguments)
}

// ReadResource implements the same surface for resources.
func (f *Fabric) ReadResource(ctx context.Context, serverID, uri string) (any, error) {
	if f.runtime == nil {
		return nil, errors.New("mcp fabric: runtime not initialized")
	}
	return f.runtime.ReadResource(ctx, serverID, uri)
}

// GetPrompt implements the same surface for prompts.
func (f *Fabric) GetPrompt(ctx context.Context, serverID, promptName string, args map[string]string) (any, error) {
	if f.runtime == nil {
		return nil, errors.New("mcp fabric: runtime not initialized")
	}
	return f.runtime.GetPrompt(ctx, serverID, promptName, args)
}

// ToolRuntimePolicies returns the union of every compiled MCP policy. The
// caller (typically the agent boot path) merges these into the toolruntime
// PolicyManifest so MCP tools become first-class citizens of the policy
// surface.
func (f *Fabric) ToolRuntimePolicies() []toolruntime.ToolPolicy {
	if f.connections == nil {
		return nil
	}
	out := []toolruntime.ToolPolicy{}
	f.connections.mu.Lock()
	defer f.connections.mu.Unlock()
	for _, state := range f.connections.connections {
		for _, p := range state.policies {
			out = append(out, p)
		}
	}
	return out
}

// EnabledServerIDs returns the active server ids.
func (f *Fabric) EnabledServerIDs() []string {
	if f.cfg.Catalog == nil {
		return nil
	}
	return f.cfg.Catalog.EnabledIDs()
}

// CompiledResources delegates to the connection manager so the ingest layer
// can iterate without tight coupling.
func (f *Fabric) CompiledResources() []*compiledResourceView {
	if f.connections == nil {
		return nil
	}
	resources := f.connections.CompiledResources()
	out := make([]*compiledResourceView, 0, len(resources))
	for _, r := range resources {
		out = append(out, &compiledResourceView{
			ServerID:    r.Binding.ServerID,
			URI:         r.URI,
			Title:       r.Title,
			Description: r.Description,
			MIMEType:    r.MIMEType,
		})
	}
	return out
}

// compiledResourceView is the public-facing view of a compiled resource so
// downstream consumers don't import the internal compiler types.
type compiledResourceView struct {
	ServerID    string
	URI         string
	Title       string
	Description string
	MIMEType    string
}

// ServerID returns the server id.
func (v *compiledResourceView) ServerIDValue() string { return v.ServerID }

// adapters bridging the connection manager's method names into the runtime's
// resolver interfaces. Keeps the runtime package independent of the manager.

type sessionResolverAdapter struct{ fabric *Fabric }

func (a *sessionResolverAdapter) Resolve(serverID string) (*client.Session, error) {
	if a.fabric == nil || a.fabric.connections == nil {
		return nil, errFabricNotConnected
	}
	return a.fabric.connections.SessionFor(serverID)
}

type policyResolverAdapter struct{ fabric *Fabric }

func (a *policyResolverAdapter) ResolvePolicy(canonicalName string) (toolruntime.ToolPolicy, string, bool) {
	if a.fabric == nil || a.fabric.connections == nil {
		return toolruntime.ToolPolicy{}, "", false
	}
	return a.fabric.connections.PolicyFor(canonicalName)
}

// Pending count helper for diagnostics.
func (f *Fabric) PendingCount() int { return len(f.Pending()) }

// Health is a one-line summary used by Sylk's diagnostic surface.
func (f *Fabric) Health() string {
	statuses := f.Status()
	connected := 0
	for _, s := range statuses {
		if s.Connected {
			connected++
		}
	}
	return fmt.Sprintf("mcp: %d/%d servers connected, %d operations pending", connected, len(statuses), f.PendingCount())
}
