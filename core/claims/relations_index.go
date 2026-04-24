package claims

import "sync"

// relationsIndex maintains secondary indexes over Relations so board
// context queries (trace_claim_ancestry, list_action_claims,
// find_overlapping_claims, etc.) resolve in O(1) lookup + O(chain)
// traversal rather than scanning every object.
//
// Keys: (RelatedType, Relationship, RelatedID) → []ObjectID
//
// For example, for a Claim that has Relations containing
// { Related: "action_1", RelatedType: "action", Relationship: "claim_action" },
// the index maintains:
//   relations[indexKey("action", "claim_action", "action_1")] = [...claims that point at action_1 via claim_action...]
//
// Additionally maintains a scope index keyed by (ScopeKind, ScopeKey)
// → []ClaimID to serve find_overlapping_claims.
//
// All mutations run under the board's write lock; reads run under
// RLock. The index itself uses no internal locking — it relies on
// the board's mutex.
type relationsIndex struct {
	// mu guards only the index itself (not the board). Used so the
	// index can be consulted from read paths that don't want to take
	// the board's full RLock.
	mu sync.RWMutex

	// forward: (RelatedType, Relationship, RelatedID) → object IDs
	// that carry this Relation. The object IDs include claims,
	// testaments, artifacts, actions — anything with Relations.
	forward map[string][]string

	// scope: (ScopeKind, ScopeKey) → claim IDs whose Scope entries
	// match.
	scope map[string][]string
}

func newRelationsIndex() *relationsIndex {
	return &relationsIndex{
		forward: make(map[string][]string),
		scope:   make(map[string][]string),
	}
}

// ────────────────────────────────────────────────────────────────────
// Mutations
// ────────────────────────────────────────────────────────────────────

// addRelations inserts every Relation on objID into the forward
// index. Caller holds the board's write lock.
func (idx *relationsIndex) addRelations(objID string, relations []Relation) {
	if idx == nil || objID == "" || len(relations) == 0 {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, r := range relations {
		key := forwardKey(r.RelatedType, r.Relationship, r.Related)
		idx.forward[key] = appendUnique(idx.forward[key], objID)
	}
}

// addScope inserts a claim's scope entries into the scope index.
func (idx *relationsIndex) addScope(claimID string, scope []ClaimScopeEntry) {
	if idx == nil || claimID == "" || len(scope) == 0 {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, s := range scope {
		key := scopeKey(s.Kind, s.Key)
		idx.scope[key] = appendUnique(idx.scope[key], claimID)
	}
}

// ────────────────────────────────────────────────────────────────────
// Queries
// ────────────────────────────────────────────────────────────────────

// objectsWithRelation returns object IDs whose Relations contain
// (relatedType, relationship, relatedID). Returns a defensive copy
// so callers may mutate without affecting the index.
func (idx *relationsIndex) objectsWithRelation(relatedType, relationship, relatedID string) []string {
	if idx == nil {
		return nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	src := idx.forward[forwardKey(relatedType, relationship, relatedID)]
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// claimsWithScope returns claim IDs whose Scope contains
// (scopeKind, scopeKey).
func (idx *relationsIndex) claimsWithScope(scopeKind, key string) []string {
	if idx == nil {
		return nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	src := idx.scope[scopeKey(scopeKind, key)]
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// ────────────────────────────────────────────────────────────────────
// Key builders
// ────────────────────────────────────────────────────────────────────

// Use a non-printable separator so colliding user data never
// produces an index-key collision. Escape would be overkill; ASCII
// record separator (0x1E) is safe for use inside Go map keys.
const indexKeySeparator = "\x1e"

func forwardKey(relatedType, relationship, relatedID string) string {
	return relatedType + indexKeySeparator + relationship + indexKeySeparator + relatedID
}

func scopeKey(kind, key string) string {
	return kind + indexKeySeparator + key
}

func appendUnique(dst []string, val string) []string {
	for _, existing := range dst {
		if existing == val {
			return dst
		}
	}
	return append(dst, val)
}
