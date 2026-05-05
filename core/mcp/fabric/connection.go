package fabric

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp"
	"github.com/adalundhe/sylk/core/mcp/catalog"
	"github.com/adalundhe/sylk/core/mcp/client"
	"github.com/adalundhe/sylk/core/mcp/compiler"
	"github.com/adalundhe/sylk/core/mcp/transport"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// connectionState wraps a single per-server live binding: transport, session,
// compiled-skill bindings, and policy entries. Replaced atomically when the
// catalog hot-reloads or list_changed forces resync.
type connectionState struct {
	spec       catalog.ServerSpec
	tx         transport.Transport
	session    *client.Session
	snapshot   *client.CapabilitySnapshot
	tools      map[string]*compiler.CompiledTool
	prompts    map[string]*compiler.CompiledPrompt
	resources  []*compiler.CompiledResource
	policies   map[string]toolruntime.ToolPolicy
	skillNames []string
	scope      *concurrency.GoroutineScope
}

// connectionManager owns the live connections to every enabled server.
//
// Construction is lazy: a server is only dialed once the first compile is
// requested (the fabric calls Ensure when it first sees the server in the
// effective catalog). On reload, removed servers are torn down; new servers
// are dialed, existing servers are resynced when their catalog spec changed.
type connectionManager struct {
	scope      *concurrency.GoroutineScope
	registry   *skills.Registry
	statusReg  *fabricRegistry
	executor   compiler.ToolExecutor

	mu          sync.Mutex
	connections map[string]*connectionState
}

// connectionManagerConfig configures connection lifecycle.
type connectionManagerConfig struct {
	Scope     *concurrency.GoroutineScope
	Registry  *skills.Registry
	StatusReg *fabricRegistry
	Executor  compiler.ToolExecutor
}

func newConnectionManager(cfg connectionManagerConfig) (*connectionManager, error) {
	if cfg.Scope == nil {
		return nil, errors.New("mcp fabric: connection scope is required")
	}
	if cfg.Registry == nil {
		return nil, errors.New("mcp fabric: skill registry is required")
	}
	if cfg.Executor == nil {
		return nil, errors.New("mcp fabric: executor is required")
	}
	return &connectionManager{
		scope:       cfg.Scope,
		registry:    cfg.Registry,
		statusReg:   cfg.StatusReg,
		executor:    cfg.Executor,
		connections: map[string]*connectionState{},
	}, nil
}

// Apply diffs the supplied effective catalog against active connections and
// reconciles. Returns the names of newly-registered skills and the names of
// skills that were removed.
func (m *connectionManager) Apply(ctx context.Context, effective *catalog.EffectiveCatalog) (added, removed []string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	desired := desiredSpecs(effective)
	added, removed, err = m.dropMissing(desired)
	if err != nil {
		return added, removed, err
	}
	addAdditions, err := m.addOrUpdate(ctx, desired)
	if err != nil {
		return append(added, addAdditions...), removed, err
	}
	return append(added, addAdditions...), removed, nil
}

func desiredSpecs(effective *catalog.EffectiveCatalog) map[string]catalog.ServerSpec {
	out := map[string]catalog.ServerSpec{}
	if effective == nil {
		return out
	}
	for id, spec := range effective.Servers {
		if spec.Disabled {
			continue
		}
		out[id] = spec
	}
	return out
}

func (m *connectionManager) dropMissing(desired map[string]catalog.ServerSpec) (added, removed []string, err error) {
	for id, conn := range m.connections {
		if _, keep := desired[id]; keep {
			continue
		}
		removed = append(removed, m.tearDown(conn)...)
		delete(m.connections, id)
		if m.statusReg != nil {
			m.statusReg.drop(id)
		}
	}
	return added, removed, nil
}

func (m *connectionManager) addOrUpdate(ctx context.Context, desired map[string]catalog.ServerSpec) ([]string, error) {
	var added []string
	for id, spec := range desired {
		existing, ok := m.connections[id]
		if ok && specsEquivalent(existing.spec, spec) {
			continue
		}
		if ok {
			added = append(added, m.tearDown(existing)...)
			delete(m.connections, id)
		}
		state, err := m.dial(ctx, spec)
		if err != nil {
			m.recordDialError(spec.ID, err)
			continue
		}
		m.connections[id] = state
		added = append(added, state.skillNames...)
		m.recordDialSuccess(state)
	}
	return added, nil
}

func (m *connectionManager) recordDialError(id string, err error) {
	if m.statusReg == nil {
		return
	}
	m.statusReg.upsert(id, func(s *FabricStatus) {
		s.Connected = false
		s.LastError = err.Error()
		s.LastErrorAt = time.Now().UTC()
	})
}

func (m *connectionManager) recordDialSuccess(state *connectionState) {
	if m.statusReg == nil {
		return
	}
	m.statusReg.upsert(state.spec.ID, func(s *FabricStatus) {
		s.Connected = true
		s.HandshakeAt = time.Now().UTC()
		s.CapabilityFP = state.snapshot.Fingerprint
		s.ToolCount = len(state.snapshot.Tools)
		s.ResourceCount = len(state.snapshot.Resources)
		s.PromptCount = len(state.snapshot.Prompts)
		s.LastError = ""
	})
}

// tearDown closes a connection, unregisters all of its skills, and returns
// the names that were removed for caller-side bookkeeping.
func (m *connectionManager) tearDown(state *connectionState) []string {
	for _, name := range state.skillNames {
		m.registry.Unregister(name)
	}
	if state.session != nil {
		_ = state.session.Close()
	}
	if state.scope != nil {
		_ = state.scope.Shutdown(time.Second, 2*time.Second)
	}
	return state.skillNames
}

// dial brings up a single server: transport.Start → handshake → list → compile
// → register. Returns the wired connection state.
func (m *connectionManager) dial(ctx context.Context, spec catalog.ServerSpec) (*connectionState, error) {
	scope, err := m.spawnConnectionScope(spec.ID)
	if err != nil {
		return nil, err
	}
	tx, err := buildTransport(spec, scope)
	if err != nil {
		_ = scope.Shutdown(time.Second, 2*time.Second)
		return nil, err
	}
	session, err := m.openSession(ctx, spec, tx, scope)
	if err != nil {
		_ = tx.Close()
		_ = scope.Shutdown(time.Second, 2*time.Second)
		return nil, err
	}
	syncCtx, cancel := context.WithTimeout(ctx, capabilitySyncDeadline())
	defer cancel()
	snapshot, err := session.SyncAll(syncCtx)
	if err != nil {
		_ = session.Close()
		_ = scope.Shutdown(time.Second, 2*time.Second)
		return nil, fmtError("%s: capability sync: %w", spec.ID, err)
	}
	return m.compileAndRegister(spec, tx, session, snapshot, scope)
}

func (m *connectionManager) spawnConnectionScope(serverID string) (*concurrency.GoroutineScope, error) {
	name := "mcp.connection." + safeID(serverID)
	return concurrency.NewGoroutineScope(context.Background(), name, nil), nil
}

func (m *connectionManager) openSession(
	ctx context.Context,
	spec catalog.ServerSpec,
	tx transport.Transport,
	scope *concurrency.GoroutineScope,
) (*client.Session, error) {
	sess, err := client.NewSession(client.SessionConfig{
		ServerID:   spec.ID,
		Transport:  tx,
		Scope:      scope,
		ClientInfo: FabricImplementation,
		ClientCapabilities: mcp.ClientCapabilities{
			Roots:       &mcp.RootsCapability{ListChanged: true},
			Elicitation: &mcp.ElicitationCapability{},
		},
	})
	if err != nil {
		return nil, err
	}
	if err := sess.Start(ctx); err != nil {
		return nil, err
	}
	return sess, nil
}

func (m *connectionManager) compileAndRegister(
	spec catalog.ServerSpec,
	tx transport.Transport,
	session *client.Session,
	snapshot *client.CapabilitySnapshot,
	scope *concurrency.GoroutineScope,
) (*connectionState, error) {
	state := &connectionState{
		spec:      spec,
		tx:        tx,
		session:   session,
		snapshot:  snapshot,
		tools:     map[string]*compiler.CompiledTool{},
		prompts:   map[string]*compiler.CompiledPrompt{},
		policies:  map[string]toolruntime.ToolPolicy{},
		scope:     scope,
	}
	if err := m.compileTools(state, spec, snapshot); err != nil {
		return nil, err
	}
	if err := m.compilePrompts(state, spec, snapshot); err != nil {
		return nil, err
	}
	m.compileResources(state, spec, snapshot)
	return state, nil
}

func (m *connectionManager) compileTools(
	state *connectionState,
	spec catalog.ServerSpec,
	snapshot *client.CapabilitySnapshot,
) error {
	allow, deny := allowDenySets(spec)
	opts := compiler.CompileToolOptions{
		ServerDomains:      spec.Domains,
		TrustedReadOnly:    spec.TrustedReadOnly,
		ServerInstructions: spec.ServerInstructions,
		PriorityBoost:      spec.PriorityBoost,
	}
	for _, desc := range snapshot.Tools {
		if !shouldExposeName(desc.Name, allow, deny) {
			continue
		}
		compiled, err := compiler.CompileTool(spec.ID, desc, opts, m.executor)
		if err != nil {
			return err
		}
		if err := m.registry.Register(compiled.Skill); err != nil {
			return fmtError("%s: register %s: %w", spec.ID, compiled.Skill.Name, err)
		}
		state.tools[compiled.Skill.Name] = compiled
		state.policies[compiled.Skill.Name] = compiled.Spec.Policy
		state.skillNames = append(state.skillNames, compiled.Skill.Name)
	}
	return nil
}

func (m *connectionManager) compilePrompts(
	state *connectionState,
	spec catalog.ServerSpec,
	snapshot *client.CapabilitySnapshot,
) error {
	opts := compiler.CompilePromptOptions{
		ServerInstructions: spec.ServerInstructions,
		PriorityBoost:      spec.PriorityBoost,
	}
	for _, desc := range snapshot.Prompts {
		compiled, err := compiler.CompilePrompt(spec.ID, desc, opts, m.executor)
		if err != nil {
			return err
		}
		if err := m.registry.Register(compiled.Skill); err != nil {
			return fmtError("%s: register prompt %s: %w", spec.ID, compiled.Skill.Name, err)
		}
		state.prompts[compiled.Skill.Name] = compiled
		state.policies[compiled.Skill.Name] = compiled.Spec.Policy
		state.skillNames = append(state.skillNames, compiled.Skill.Name)
	}
	return nil
}

func (m *connectionManager) compileResources(
	state *connectionState,
	spec catalog.ServerSpec,
	snapshot *client.CapabilitySnapshot,
) {
	for _, desc := range snapshot.Resources {
		state.resources = append(state.resources, compiler.CompileResource(spec.ID, desc))
	}
}

// SessionFor implements runtime.SessionResolver.
func (m *connectionManager) SessionFor(serverID string) (*client.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.connections[serverID]
	if !ok || state.session == nil {
		return nil, fmt.Errorf("%w: %s", errFabricNotConnected, serverID)
	}
	return state.session, nil
}

// PolicyFor implements runtime.PolicyResolver.
func (m *connectionManager) PolicyFor(canonicalName string) (toolruntime.ToolPolicy, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, state := range m.connections {
		if policy, ok := state.policies[canonicalName]; ok {
			return policy, state.snapshot.Fingerprint, true
		}
	}
	return toolruntime.ToolPolicy{}, "", false
}

// Snapshot returns the union of all live snapshots — used by diagnostics.
func (m *connectionManager) Snapshot() []*client.CapabilitySnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*client.CapabilitySnapshot, 0, len(m.connections))
	for _, state := range m.connections {
		out = append(out, state.snapshot)
	}
	return out
}

// CompiledResources returns the union of compiled resource bindings across
// all connections. Used by the ingest layer.
func (m *connectionManager) CompiledResources() []*compiler.CompiledResource {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*compiler.CompiledResource
	for _, state := range m.connections {
		out = append(out, state.resources...)
	}
	return out
}

// CloseAll tears every connection down. Idempotent.
func (m *connectionManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, state := range m.connections {
		_ = m.tearDown(state)
		delete(m.connections, id)
		if m.statusReg != nil {
			m.statusReg.drop(id)
		}
	}
}

func allowDenySets(spec catalog.ServerSpec) (allow, deny map[string]struct{}) {
	allow = setFromList(spec.AllowTools)
	deny = setFromList(spec.DenyTools)
	return allow, deny
}

func setFromList(in []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, name := range in {
		out[name] = struct{}{}
	}
	return out
}

func shouldExposeName(name string, allow, deny map[string]struct{}) bool {
	if _, denied := deny[name]; denied {
		return false
	}
	if len(allow) == 0 {
		return true
	}
	_, allowed := allow[name]
	return allowed
}

// specsEquivalent reports whether two specs produce the same connection.
// We avoid reflect.DeepEqual to keep this allocation-free; the comparison
// covers every field that affects dial-time behaviour.
func specsEquivalent(a, b catalog.ServerSpec) bool {
	if a.Transport != b.Transport || a.Command != b.Command || a.WorkDir != b.WorkDir || a.Endpoint != b.Endpoint {
		return false
	}
	if !equalStringSlices(a.Args, b.Args) || !equalStringMaps(a.Env, b.Env) || !equalStringMaps(a.Headers, b.Headers) {
		return false
	}
	if a.CredentialRef != b.CredentialRef || a.TrustedReadOnly != b.TrustedReadOnly {
		return false
	}
	if !equalStringSlices(a.Domains, b.Domains) || !equalStringSlices(a.AllowTools, b.AllowTools) || !equalStringSlices(a.DenyTools, b.DenyTools) {
		return false
	}
	return true
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if other, ok := b[k]; !ok || other != v {
			return false
		}
	}
	return true
}
