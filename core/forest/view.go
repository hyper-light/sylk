package forest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ViewSnapshotInput configures a structural forest snapshot for the TUI.
type ViewSnapshotInput struct {
	SessionID string        `json:"session_id,omitempty"`
	Horizon   CanopyHorizon `json:"horizon,omitempty"`
}

// ViewSnapshot contains the grouped trees and nodes needed for the memory view.
type ViewSnapshot struct {
	SessionID        string        `json:"session_id,omitempty"`
	Horizon          CanopyHorizon `json:"horizon,omitempty"`
	Trees            []ViewTree    `json:"trees,omitempty"`
	SelectedBranchID string        `json:"selected_branch_id,omitempty"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

// ViewTree is a single family projection rendered as a distinct tree.
type ViewTree struct {
	Family    TreeFamily   `json:"family"`
	Label     string       `json:"label"`
	Nodes     []ViewNode   `json:"nodes,omitempty"`
	Roots     []string     `json:"roots,omitempty"`
	UpdatedAt time.Time    `json:"updated_at"`
	Scope     MemoryScope  `json:"scope,omitempty"`
	State     BranchState  `json:"state,omitempty"`
}

// ViewNode captures the minimal structural branch state required by the TUI.
type ViewNode struct {
	ID         string       `json:"id"`
	ParentID   string       `json:"parent_id,omitempty"`
	RootID     string       `json:"root_id"`
	Family     TreeFamily   `json:"family"`
	Title      string       `json:"title"`
	Summary    string       `json:"summary"`
	Depth      int          `json:"depth"`
	Leaf       bool         `json:"leaf"`
	Scope      MemoryScope  `json:"scope"`
	State      BranchState  `json:"state"`
	Confidence float64      `json:"confidence"`
	Salience   float64      `json:"salience"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

// Snapshot returns a forest-structured view tailored for the TUI memory mode.
func (m *MemoryForest) Snapshot(ctx context.Context, input ViewSnapshotInput) (*ViewSnapshot, error) {
	if m == nil {
		return &ViewSnapshot{SessionID: input.SessionID, Horizon: input.Horizon, UpdatedAt: time.Now().UTC()}, nil
	}

	horizon := input.Horizon
	if horizon == "" {
		horizon = CanopyHorizonSession
	}

	branches, err := m.loadViewBranches(ctx, strings.TrimSpace(input.SessionID))
	if err != nil {
		return nil, err
	}

	snapshot := &ViewSnapshot{
		SessionID: strings.TrimSpace(input.SessionID),
		Horizon:   horizon,
		UpdatedAt: time.Now().UTC(),
	}
	if len(branches) == 0 {
		return snapshot, nil
	}

	byFamily := make(map[TreeFamily][]*Branch, len(defaultFamilies()))
	var newest *Branch
	for _, branch := range branches {
		byFamily[branch.Family] = append(byFamily[branch.Family], branch)
		if newest == nil || branch.UpdatedAt.After(newest.UpdatedAt) {
			newest = branch
		}
	}
	if newest != nil {
		snapshot.SelectedBranchID = newest.ID
	}

	for _, family := range defaultFamilies() {
		familyBranches := byFamily[family]
		if len(familyBranches) == 0 {
			continue
		}
		tree := buildViewTree(family, familyBranches)
		if len(tree.Nodes) == 0 {
			continue
		}
		snapshot.Trees = append(snapshot.Trees, tree)
		if tree.UpdatedAt.After(snapshot.UpdatedAt) {
			snapshot.UpdatedAt = tree.UpdatedAt
		}
	}

	return snapshot, nil
}

func (m *MemoryForest) loadViewBranches(ctx context.Context, sessionID string) ([]*Branch, error) {
	if sessionID != "" {
		sessionBranches, err := m.loadViewBranchesWithScope(ctx, sessionID, false)
		if err != nil {
			return nil, err
		}
		if len(sessionBranches) > 0 {
			return sessionBranches, nil
		}
	}
	return m.loadViewBranchesWithScope(ctx, "", true)
}

func (m *MemoryForest) loadViewBranchesWithScope(ctx context.Context, sessionID string, semanticOnly bool) ([]*Branch, error) {
	query := `
		SELECT id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
		       intent_id, title, summary, confidence, salience, utility, success_rate,
		       scope_risk, conflict_score, support_count, counter_count, success_count,
		       failure_count, access_count, last_accessed_at, created_at, updated_at, metadata
		FROM forest_branches
		WHERE state != ?
	`
	args := []any{string(BranchStateSuperseded)}

	if sessionID != "" {
		query += " AND session_id = ?"
		args = append(args, sessionID)
	}
	if semanticOnly {
		query += " AND scope = ?"
		args = append(args, string(ScopeSemantic))
	}
	query += " ORDER BY family ASC, created_at ASC, updated_at ASC"

	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query memory view branches: %w", err)
	}
	defer rows.Close()

	branches := make([]*Branch, 0, 64)
	for rows.Next() {
		branch, err := scanBranch(rows)
		if err != nil {
			return nil, err
		}
		if branch != nil {
			branches = append(branches, branch)
		}
	}
	return branches, rows.Err()
}

func buildViewTree(family TreeFamily, branches []*Branch) ViewTree {
	branchByID := make(map[string]*Branch, len(branches))
	children := make(map[string][]*Branch, len(branches))
	roots := make([]*Branch, 0, len(branches))

	for _, branch := range branches {
		branchByID[branch.ID] = branch
	}
	for _, branch := range branches {
		parent, ok := branchByID[branch.ParentID]
		if !ok || branch.ParentID == "" || parent.Family != family {
			roots = append(roots, branch)
			continue
		}
		children[parent.ID] = append(children[parent.ID], branch)
	}

	sortBranchesChronologically(roots)
	for id := range children {
		sortBranchesChronologically(children[id])
	}

	nodes := make([]ViewNode, 0, len(branches))
	visited := make(map[string]struct{}, len(branches))
	rootIDs := make([]string, 0, len(roots))
	var newest time.Time
	scope := ScopeSemantic
	state := BranchStateActive

	var walk func(branch *Branch, depth int)
	walk = func(branch *Branch, depth int) {
		if branch == nil {
			return
		}
		if _, ok := visited[branch.ID]; ok {
			return
		}
		visited[branch.ID] = struct{}{}
		node := ViewNode{
			ID:         branch.ID,
			ParentID:   branch.ParentID,
			RootID:     branch.RootID,
			Family:     branch.Family,
			Title:      firstNonEmpty(strings.TrimSpace(branch.Title), strings.TrimSpace(branch.Summary)),
			Summary:    branch.Summary,
			Depth:      depth,
			Leaf:       len(children[branch.ID]) == 0,
			Scope:      branch.Scope,
			State:      branch.State,
			Confidence: branch.Confidence,
			Salience:   branch.Salience,
			CreatedAt:  branch.CreatedAt,
			UpdatedAt:  branch.UpdatedAt,
		}
		nodes = append(nodes, node)
		if branch.UpdatedAt.After(newest) {
			newest = branch.UpdatedAt
			scope = branch.Scope
			state = branch.State
		}
		for _, child := range children[branch.ID] {
			walk(child, depth+1)
		}
	}

	for _, root := range roots {
		rootIDs = append(rootIDs, root.ID)
		walk(root, 0)
	}
	for _, branch := range branches {
		if _, ok := visited[branch.ID]; ok {
			continue
		}
		rootIDs = append(rootIDs, branch.ID)
		walk(branch, 0)
	}

	sort.SliceStable(nodes, func(i, j int) bool {
		if !nodes[i].CreatedAt.Equal(nodes[j].CreatedAt) {
			return nodes[i].CreatedAt.Before(nodes[j].CreatedAt)
		}
		if nodes[i].Depth != nodes[j].Depth {
			return nodes[i].Depth < nodes[j].Depth
		}
		return nodes[i].ID < nodes[j].ID
	})

	return ViewTree{
		Family:    family,
		Label:     viewFamilyLabel(family),
		Nodes:     nodes,
		Roots:     dedupeStrings(rootIDs),
		UpdatedAt: newest,
		Scope:     scope,
		State:     state,
	}
}

func sortBranchesChronologically(branches []*Branch) {
	sort.SliceStable(branches, func(i, j int) bool {
		if !branches[i].CreatedAt.Equal(branches[j].CreatedAt) {
			return branches[i].CreatedAt.Before(branches[j].CreatedAt)
		}
		if !branches[i].UpdatedAt.Equal(branches[j].UpdatedAt) {
			return branches[i].UpdatedAt.Before(branches[j].UpdatedAt)
		}
		return branches[i].ID < branches[j].ID
	})
}

func viewFamilyLabel(family TreeFamily) string {
	switch family {
	case TreeFamilyIntent:
		return "Intent Tree"
	case TreeFamilyConstraint:
		return "Constraint Tree"
	case TreeFamilyEvidence:
		return "Evidence Tree"
	case TreeFamilyDecision:
		return "Decision Tree"
	case TreeFamilyOutcome:
		return "Outcome Tree"
	case TreeFamilyPreference:
		return "Preference Tree"
	case TreeFamilyCapability:
		return "Capability Tree"
	case TreeFamilyOpportunity:
		return "Opportunity Tree"
	case TreeFamilyConflict:
		return "Conflict Tree"
	default:
		return "Memory Tree"
	}
}
