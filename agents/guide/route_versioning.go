package guide

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// =============================================================================
// Route Versioning
// =============================================================================

// RouteVersion represents a version of a route configuration
type RouteVersion struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
	Comment   string    `json:"comment,omitempty"`

	// Route configuration at this version
	Routes []VersionedRoute `json:"routes"`

	// Migration from previous version (nil for v1)
	Migration *RouteMigration `json:"migration,omitempty"`
}

// VersionedRoute represents a route with version tracking
type VersionedRoute struct {
	ID            string      `json:"id"`
	Input         string      `json:"input"`
	TargetAgentID string      `json:"target_agent_id"`
	Intent        Intent      `json:"intent"`
	Domain        Domain      `json:"domain"`
	Confidence    float64     `json:"confidence"`
	Source        RouteSource `json:"source"`

	// Version metadata
	CreatedVersion  int       `json:"created_version"`
	ModifiedVersion int       `json:"modified_version"`
	CreatedAt       time.Time `json:"created_at"`
	ModifiedAt      time.Time `json:"modified_at"`

	// Deprecation info
	Deprecated       bool   `json:"deprecated,omitempty"`
	DeprecatedReason string `json:"deprecated_reason,omitempty"`
	ReplacedBy       string `json:"replaced_by,omitempty"`
}

// RouteSource indicates how a route was created
type RouteSource string

const (
	RouteSourceManual     RouteSource = "manual"     // Manually configured
	RouteSourceLLM        RouteSource = "llm"        // Learned from LLM classification
	RouteSourceCorrection RouteSource = "correction" // From user correction
	RouteSourceMigration  RouteSource = "migration"  // Created during migration
)

// RouteMigration describes how to migrate between versions
type RouteMigration struct {
	FromVersion int                  `json:"from_version"`
	ToVersion   int                  `json:"to_version"`
	Steps       []RouteMigrationStep `json:"steps"`
	Reversible  bool                 `json:"reversible"`
}

// RouteMigrationStep describes a single migration operation
type RouteMigrationStep struct {
	Type        MigrationStepType `json:"type"`
	RouteID     string            `json:"route_id,omitempty"`
	OldValue    json.RawMessage   `json:"old_value,omitempty"`
	NewValue    json.RawMessage   `json:"new_value,omitempty"`
	Description string            `json:"description"`
}

// MigrationStepType indicates the type of migration step
type MigrationStepType string

const (
	MigrationStepAdd       MigrationStepType = "add"       // Add a new route
	MigrationStepRemove    MigrationStepType = "remove"    // Remove a route
	MigrationStepModify    MigrationStepType = "modify"    // Modify a route
	MigrationStepRename    MigrationStepType = "rename"    // Rename an agent
	MigrationStepDeprecate MigrationStepType = "deprecate" // Mark as deprecated
)

// =============================================================================
// Route Version Store
// =============================================================================

// RouteVersionStore manages versioned routes
type RouteVersionStore struct {
	mu sync.RWMutex

	// All versions (oldest to newest)
	versions []*RouteVersion

	// Current version number
	currentVersion int

	// Current routes (denormalized for fast lookup)
	currentRoutes map[string]*VersionedRoute

	// Route cache to update when versions change
	cache *RouteCache

	// Statistics
	stats RouteVersionStats
}

// RouteVersionStats contains version store statistics
type RouteVersionStats struct {
	TotalVersions   int       `json:"total_versions"`
	CurrentVersion  int       `json:"current_version"`
	TotalRoutes     int       `json:"total_routes"`
	DeprecatedCount int       `json:"deprecated_count"`
	LastMigration   time.Time `json:"last_migration,omitempty"`
	MigrationCount  int       `json:"migration_count"`
}

// NewRouteVersionStore creates a new version store
func NewRouteVersionStore(cache *RouteCache) *RouteVersionStore {
	store := &RouteVersionStore{
		versions:       make([]*RouteVersion, 0),
		currentRoutes:  make(map[string]*VersionedRoute),
		cache:          cache,
		currentVersion: 0,
	}

	// Create initial version
	store.CreateVersion("system", "Initial version")

	return store
}

// =============================================================================
// Version Management
// =============================================================================

// CreateVersion creates a new version from current state
func (rvs *RouteVersionStore) CreateVersion(createdBy, comment string) int {
	rvs.mu.Lock()
	defer rvs.mu.Unlock()

	rvs.currentVersion++
	version := &RouteVersion{
		Version:   rvs.currentVersion,
		CreatedAt: time.Now(),
		CreatedBy: createdBy,
		Comment:   comment,
		Routes:    make([]VersionedRoute, 0, len(rvs.currentRoutes)),
	}

	// Copy current routes to version
	for _, route := range rvs.currentRoutes {
		version.Routes = append(version.Routes, *route)
	}

	rvs.versions = append(rvs.versions, version)
	rvs.stats.TotalVersions = len(rvs.versions)
	rvs.stats.CurrentVersion = rvs.currentVersion

	return rvs.currentVersion
}

// GetVersion retrieves a specific version
func (rvs *RouteVersionStore) GetVersion(version int) *RouteVersion {
	rvs.mu.RLock()
	defer rvs.mu.RUnlock()

	for _, v := range rvs.versions {
		if v.Version == version {
			return v
		}
	}
	return nil
}

// GetCurrentVersion returns the current version number
func (rvs *RouteVersionStore) GetCurrentVersion() int {
	rvs.mu.RLock()
	defer rvs.mu.RUnlock()
	return rvs.currentVersion
}

// ListVersions returns all version numbers with metadata
func (rvs *RouteVersionStore) ListVersions() []VersionSummary {
	rvs.mu.RLock()
	defer rvs.mu.RUnlock()

	summaries := make([]VersionSummary, len(rvs.versions))
	for i, v := range rvs.versions {
		summaries[i] = VersionSummary{
			Version:    v.Version,
			CreatedAt:  v.CreatedAt,
			CreatedBy:  v.CreatedBy,
			Comment:    v.Comment,
			RouteCount: len(v.Routes),
		}
	}
	return summaries
}

// VersionSummary provides version metadata without full routes
type VersionSummary struct {
	Version    int       `json:"version"`
	CreatedAt  time.Time `json:"created_at"`
	CreatedBy  string    `json:"created_by"`
	Comment    string    `json:"comment,omitempty"`
	RouteCount int       `json:"route_count"`
}

// =============================================================================
// Route Operations
// =============================================================================

// AddRoute adds a route to the current version
func (rvs *RouteVersionStore) AddRoute(route *VersionedRoute) error {
	rvs.mu.Lock()
	defer rvs.mu.Unlock()

	if route.ID == "" {
		route.ID = fmt.Sprintf("route_%d", time.Now().UnixNano())
	}

	if _, exists := rvs.currentRoutes[route.ID]; exists {
		return fmt.Errorf("route already exists: %s", route.ID)
	}

	route.CreatedVersion = rvs.currentVersion
	route.ModifiedVersion = rvs.currentVersion
	route.CreatedAt = time.Now()
	route.ModifiedAt = time.Now()

	rvs.currentRoutes[route.ID] = route
	rvs.stats.TotalRoutes = len(rvs.currentRoutes)

	// Update cache
	if rvs.cache != nil {
		rvs.cache.SetFromRoute(route.Input, route.TargetAgentID, route.Intent, route.Domain)
	}

	return nil
}

// UpdateRoute updates an existing route
func (rvs *RouteVersionStore) UpdateRoute(routeID string, update func(*VersionedRoute)) error {
	rvs.mu.Lock()
	defer rvs.mu.Unlock()

	route, exists := rvs.currentRoutes[routeID]
	if !exists {
		return fmt.Errorf("route not found: %s", routeID)
	}

	update(route)
	route.ModifiedVersion = rvs.currentVersion
	route.ModifiedAt = time.Now()

	// Update cache
	if rvs.cache != nil {
		rvs.cache.SetFromRoute(route.Input, route.TargetAgentID, route.Intent, route.Domain)
	}

	return nil
}

// RemoveRoute removes a route from the current version
func (rvs *RouteVersionStore) RemoveRoute(routeID string) error {
	rvs.mu.Lock()
	defer rvs.mu.Unlock()

	route, exists := rvs.currentRoutes[routeID]
	if !exists {
		return fmt.Errorf("route not found: %s", routeID)
	}

	// Invalidate cache
	if rvs.cache != nil {
		rvs.cache.Invalidate(route.Input)
	}

	delete(rvs.currentRoutes, routeID)
	rvs.stats.TotalRoutes = len(rvs.currentRoutes)

	return nil
}

// DeprecateRoute marks a route as deprecated
func (rvs *RouteVersionStore) DeprecateRoute(routeID, reason, replacedBy string) error {
	rvs.mu.Lock()
	defer rvs.mu.Unlock()

	route, exists := rvs.currentRoutes[routeID]
	if !exists {
		return fmt.Errorf("route not found: %s", routeID)
	}

	route.Deprecated = true
	route.DeprecatedReason = reason
	route.ReplacedBy = replacedBy
	route.ModifiedVersion = rvs.currentVersion
	route.ModifiedAt = time.Now()

	rvs.stats.DeprecatedCount++

	return nil
}

// GetRoute retrieves a route by ID
func (rvs *RouteVersionStore) GetRoute(routeID string) *VersionedRoute {
	rvs.mu.RLock()
	defer rvs.mu.RUnlock()
	return rvs.currentRoutes[routeID]
}

// GetRouteByInput retrieves a route by input string
func (rvs *RouteVersionStore) GetRouteByInput(input string) *VersionedRoute {
	rvs.mu.RLock()
	defer rvs.mu.RUnlock()

	for _, route := range rvs.currentRoutes {
		if route.Input == input {
			return route
		}
	}
	return nil
}

// GetAllRoutes returns all current routes
func (rvs *RouteVersionStore) GetAllRoutes() []*VersionedRoute {
	rvs.mu.RLock()
	defer rvs.mu.RUnlock()

	routes := make([]*VersionedRoute, 0, len(rvs.currentRoutes))
	for _, route := range rvs.currentRoutes {
		routes = append(routes, route)
	}
	return routes
}

// =============================================================================
// Migration
// =============================================================================

// Migrate applies a migration to move to a new version
func (rvs *RouteVersionStore) Migrate(migration *RouteMigration, createdBy string) error {
	rvs.mu.Lock()
	defer rvs.mu.Unlock()

	if err := rvs.validateMigrationVersion(migration); err != nil {
		return err
	}

	if err := rvs.applyMigrationSteps(migration); err != nil {
		return err
	}

	version := rvs.buildMigrationVersion(migration, createdBy)
	rvs.appendMigrationVersion(version)
	rvs.updateMigrationStats()

	return nil
}

func (rvs *RouteVersionStore) validateMigrationVersion(migration *RouteMigration) error {
	if migration.FromVersion == rvs.currentVersion {
		return nil
	}
	return fmt.Errorf("migration from version %d does not match current version %d",
		migration.FromVersion, rvs.currentVersion)
}

func (rvs *RouteVersionStore) applyMigrationSteps(migration *RouteMigration) error {
	for _, step := range migration.Steps {
		if err := rvs.applyMigrationStep(step); err != nil {
			return fmt.Errorf("migration step failed: %w", err)
		}
	}
	return nil
}

func (rvs *RouteVersionStore) buildMigrationVersion(migration *RouteMigration, createdBy string) *RouteVersion {
	rvs.currentVersion = migration.ToVersion
	version := &RouteVersion{
		Version:   rvs.currentVersion,
		CreatedAt: time.Now(),
		CreatedBy: createdBy,
		Comment:   fmt.Sprintf("Migration from v%d to v%d", migration.FromVersion, migration.ToVersion),
		Routes:    make([]VersionedRoute, 0, len(rvs.currentRoutes)),
		Migration: migration,
	}
	for _, route := range rvs.currentRoutes {
		version.Routes = append(version.Routes, *route)
	}
	return version
}

func (rvs *RouteVersionStore) appendMigrationVersion(version *RouteVersion) {
	rvs.versions = append(rvs.versions, version)
}

func (rvs *RouteVersionStore) updateMigrationStats() {
	rvs.stats.TotalVersions = len(rvs.versions)
	rvs.stats.CurrentVersion = rvs.currentVersion
	rvs.stats.LastMigration = time.Now()
	rvs.stats.MigrationCount++
}

// applyMigrationStep applies a single migration step
func (rvs *RouteVersionStore) applyMigrationStep(step RouteMigrationStep) error {
	handler := rvs.stepHandler(step.Type)
	if handler == nil {
		return nil
	}
	return handler(step)
}

type migrationStepHandler func(step RouteMigrationStep) error

type migrationStepHandlers struct {
	add       migrationStepHandler
	remove    migrationStepHandler
	modify    migrationStepHandler
	deprecate migrationStepHandler
	rename    migrationStepHandler
}

func (rvs *RouteVersionStore) stepHandler(stepType MigrationStepType) migrationStepHandler {
	handlers := rvs.migrationStepHandlers()
	switch stepType {
	case MigrationStepAdd:
		return handlers.add
	case MigrationStepRemove:
		return handlers.remove
	case MigrationStepModify:
		return handlers.modify
	case MigrationStepDeprecate:
		return handlers.deprecate
	case MigrationStepRename:
		return handlers.rename
	default:
		return nil
	}
}

func (rvs *RouteVersionStore) migrationStepHandlers() migrationStepHandlers {
	return migrationStepHandlers{
		add:       rvs.applyMigrationAdd,
		remove:    rvs.applyMigrationRemove,
		modify:    rvs.applyMigrationModify,
		deprecate: rvs.applyMigrationDeprecate,
		rename:    rvs.applyMigrationRename,
	}
}

func (rvs *RouteVersionStore) applyMigrationAdd(step RouteMigrationStep) error {
	route, err := rvs.decodeNewRoute(step.NewValue)
	if err != nil {
		return err
	}
	rvs.addMigrationRoute(route)
	return nil
}

func (rvs *RouteVersionStore) decodeNewRoute(data []byte) (*VersionedRoute, error) {
	var route VersionedRoute
	if err := json.Unmarshal(data, &route); err != nil {
		return nil, err
	}
	return &route, nil
}

func (rvs *RouteVersionStore) addMigrationRoute(route *VersionedRoute) {
	rvs.initializeMigrationRoute(route)
	rvs.currentRoutes[route.ID] = route
}

func (rvs *RouteVersionStore) initializeMigrationRoute(route *VersionedRoute) {
	version := rvs.nextVersion()
	route.CreatedVersion = version
	route.ModifiedVersion = version
	route.CreatedAt = time.Now()
	route.ModifiedAt = time.Now()
	route.Source = RouteSourceMigration
}

func (rvs *RouteVersionStore) applyMigrationRemove(step RouteMigrationStep) error {
	delete(rvs.currentRoutes, step.RouteID)
	return nil
}

func (rvs *RouteVersionStore) applyMigrationModify(step RouteMigrationStep) error {
	route, err := rvs.getMigrationRoute(step.RouteID)
	if err != nil {
		return err
	}
	updates, err := rvs.decodeRouteUpdates(step.NewValue)
	if err != nil {
		return err
	}
	rvs.applyRouteUpdates(route, updates)
	rvs.touchRoute(route)
	return nil
}

func (rvs *RouteVersionStore) applyMigrationDeprecate(step RouteMigrationStep) error {
	route, err := rvs.getMigrationRoute(step.RouteID)
	if err != nil {
		return err
	}
	rvs.deprecateRoute(route)
	meta, err := rvs.decodeDeprecationMeta(step.NewValue)
	if err == nil {
		rvs.applyDeprecationMeta(route, meta)
	}
	rvs.touchRoute(route)
	rvs.stats.DeprecatedCount++
	return nil
}

func (rvs *RouteVersionStore) applyMigrationRename(step RouteMigrationStep) error {
	rename, err := rvs.decodeRename(step.NewValue)
	if err != nil {
		return err
	}
	rvs.renameTargetAgent(rename)
	return nil
}

func (rvs *RouteVersionStore) getMigrationRoute(routeID string) (*VersionedRoute, error) {
	route, exists := rvs.currentRoutes[routeID]
	if !exists {
		return nil, fmt.Errorf("route not found: %s", routeID)
	}
	return route, nil
}

func (rvs *RouteVersionStore) decodeRouteUpdates(data []byte) (map[string]interface{}, error) {
	var updates map[string]interface{}
	if err := json.Unmarshal(data, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

func (rvs *RouteVersionStore) applyRouteUpdates(route *VersionedRoute, updates map[string]interface{}) {
	if target, ok := updates["target_agent_id"].(string); ok {
		route.TargetAgentID = target
	}
	if intent, ok := updates["intent"].(string); ok {
		route.Intent = Intent(intent)
	}
	if domain, ok := updates["domain"].(string); ok {
		route.Domain = Domain(domain)
	}
}

func (rvs *RouteVersionStore) decodeDeprecationMeta(data []byte) (map[string]string, error) {
	var meta map[string]string
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func (rvs *RouteVersionStore) applyDeprecationMeta(route *VersionedRoute, meta map[string]string) {
	route.DeprecatedReason = meta["reason"]
	route.ReplacedBy = meta["replaced_by"]
}

func (rvs *RouteVersionStore) deprecateRoute(route *VersionedRoute) {
	route.Deprecated = true
}

func (rvs *RouteVersionStore) decodeRename(data []byte) (*struct {
	OldAgentID string `json:"old_agent_id"`
	NewAgentID string `json:"new_agent_id"`
}, error) {
	var rename struct {
		OldAgentID string `json:"old_agent_id"`
		NewAgentID string `json:"new_agent_id"`
	}
	if err := json.Unmarshal(data, &rename); err != nil {
		return nil, err
	}
	return &rename, nil
}

func (rvs *RouteVersionStore) renameTargetAgent(rename *struct {
	OldAgentID string `json:"old_agent_id"`
	NewAgentID string `json:"new_agent_id"`
}) {
	version := rvs.nextVersion()
	for _, route := range rvs.currentRoutes {
		if route.TargetAgentID == rename.OldAgentID {
			route.TargetAgentID = rename.NewAgentID
			route.ModifiedVersion = version
			route.ModifiedAt = time.Now()
		}
	}
}

func (rvs *RouteVersionStore) touchRoute(route *VersionedRoute) {
	version := rvs.nextVersion()
	route.ModifiedVersion = version
	route.ModifiedAt = time.Now()
}

func (rvs *RouteVersionStore) nextVersion() int {
	return rvs.currentVersion + 1
}

// Rollback reverts to a previous version
func (rvs *RouteVersionStore) Rollback(targetVersion int) error {
	rvs.mu.Lock()
	defer rvs.mu.Unlock()

	var targetVer *RouteVersion
	for _, v := range rvs.versions {
		if v.Version == targetVersion {
			targetVer = v
			break
		}
	}

	if targetVer == nil {
		return fmt.Errorf("version not found: %d", targetVersion)
	}

	// Restore routes from target version
	rvs.currentRoutes = make(map[string]*VersionedRoute)
	for _, route := range targetVer.Routes {
		r := route // Copy
		rvs.currentRoutes[route.ID] = &r
	}

	// Invalidate cache and re-populate
	if rvs.cache != nil {
		rvs.cache.Clear()
		for _, route := range rvs.currentRoutes {
			rvs.cache.SetFromRoute(route.Input, route.TargetAgentID, route.Intent, route.Domain)
		}
	}

	rvs.currentVersion = targetVersion
	rvs.stats.CurrentVersion = targetVersion
	rvs.stats.TotalRoutes = len(rvs.currentRoutes)
	rvs.stats.DeprecatedCount = rvs.countDeprecated()

	return nil
}

// countDeprecated counts deprecated routes
func (rvs *RouteVersionStore) countDeprecated() int {
	count := 0
	for _, route := range rvs.currentRoutes {
		if route.Deprecated {
			count++
		}
	}
	return count
}

// =============================================================================
// Diff and Comparison
// =============================================================================

// Diff compares two versions and returns the differences
func (rvs *RouteVersionStore) Diff(fromVersion, toVersion int) (*VersionDiff, error) {
	rvs.mu.RLock()
	defer rvs.mu.RUnlock()

	fromVer, toVer := rvs.findVersions(fromVersion, toVersion)
	if err := validateDiffVersions(fromVersion, toVersion, fromVer, toVer); err != nil {
		return nil, err
	}

	diff := newVersionDiff(fromVersion, toVersion)
	fromRoutes := buildRouteMap(fromVer.Routes)
	toRoutes := buildRouteMap(toVer.Routes)

	addRoutes(diff, fromRoutes, toRoutes)
	removeRoutes(diff, fromRoutes, toRoutes)

	return diff, nil
}

func (rvs *RouteVersionStore) findVersions(fromVersion int, toVersion int) (*RouteVersion, *RouteVersion) {
	var fromVer *RouteVersion
	var toVer *RouteVersion
	for _, version := range rvs.versions {
		if version.Version == fromVersion {
			fromVer = version
		}
		if version.Version == toVersion {
			toVer = version
		}
	}
	return fromVer, toVer
}

func validateDiffVersions(fromVersion int, toVersion int, fromVer *RouteVersion, toVer *RouteVersion) error {
	if fromVer == nil {
		return fmt.Errorf("from version not found: %d", fromVersion)
	}
	if toVer == nil {
		return fmt.Errorf("to version not found: %d", toVersion)
	}
	return nil
}

func newVersionDiff(fromVersion int, toVersion int) *VersionDiff {
	return &VersionDiff{
		FromVersion: fromVersion,
		ToVersion:   toVersion,
		Added:       make([]VersionedRoute, 0),
		Removed:     make([]VersionedRoute, 0),
		Modified:    make([]RouteDiff, 0),
	}
}

func buildRouteMap(routes []VersionedRoute) map[string]VersionedRoute {
	result := make(map[string]VersionedRoute)
	for _, route := range routes {
		result[route.ID] = route
	}
	return result
}

func addRoutes(diff *VersionDiff, fromRoutes map[string]VersionedRoute, toRoutes map[string]VersionedRoute) {
	for id, toRoute := range toRoutes {
		if fromRoute, exists := fromRoutes[id]; exists {
			addModifiedRoute(diff, id, fromRoute, toRoute)
			continue
		}
		diff.Added = append(diff.Added, toRoute)
	}
}

func addModifiedRoute(diff *VersionDiff, id string, fromRoute VersionedRoute, toRoute VersionedRoute) {
	if routesEqual(fromRoute, toRoute) {
		return
	}
	diff.Modified = append(diff.Modified, RouteDiff{
		RouteID: id,
		Before:  fromRoute,
		After:   toRoute,
	})
}

func removeRoutes(diff *VersionDiff, fromRoutes map[string]VersionedRoute, toRoutes map[string]VersionedRoute) {
	for id, fromRoute := range fromRoutes {
		if _, exists := toRoutes[id]; exists {
			continue
		}
		diff.Removed = append(diff.Removed, fromRoute)
	}
}

// VersionDiff represents differences between two versions
type VersionDiff struct {
	FromVersion int              `json:"from_version"`
	ToVersion   int              `json:"to_version"`
	Added       []VersionedRoute `json:"added"`
	Removed     []VersionedRoute `json:"removed"`
	Modified    []RouteDiff      `json:"modified"`
}

// RouteDiff represents changes to a single route
type RouteDiff struct {
	RouteID string         `json:"route_id"`
	Before  VersionedRoute `json:"before"`
	After   VersionedRoute `json:"after"`
}

// routesEqual checks if two routes are equal (ignoring timestamps)
func routesEqual(a, b VersionedRoute) bool {
	return a.Input == b.Input &&
		a.TargetAgentID == b.TargetAgentID &&
		a.Intent == b.Intent &&
		a.Domain == b.Domain &&
		a.Deprecated == b.Deprecated
}

// =============================================================================
// Statistics
// =============================================================================

// Stats returns version store statistics
func (rvs *RouteVersionStore) Stats() RouteVersionStats {
	rvs.mu.RLock()
	defer rvs.mu.RUnlock()
	return rvs.stats
}
