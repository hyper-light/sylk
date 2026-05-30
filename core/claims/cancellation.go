package claims

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type ClaimCancelRegistry struct {
	mu      sync.Mutex
	entries map[string]map[string]context.CancelFunc
	nextID  uint64
}

type ClaimCancelRegistration struct {
	ClaimID string
	ID      string
	cancel  context.CancelFunc
	owner   *ClaimCancelRegistry
}

func NewClaimCancelRegistry() *ClaimCancelRegistry {
	return &ClaimCancelRegistry{entries: make(map[string]map[string]context.CancelFunc)}
}

func (r *ClaimCancelRegistry) Context(ctx context.Context, claimID string) (context.Context, ClaimCancelRegistration) {
	if ctx == nil {
		ctx = context.Background()
	}
	claimID = strings.TrimSpace(claimID)
	if r == nil || claimID == "" {
		runCtx, cancel := context.WithCancel(ctx)
		return runCtx, ClaimCancelRegistration{ClaimID: claimID, cancel: cancel}
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.nextID++
	id := stableCancelRegistrationID(r.nextID)
	if r.entries[claimID] == nil {
		r.entries[claimID] = make(map[string]context.CancelFunc)
	}
	r.entries[claimID][id] = cancel
	r.mu.Unlock()
	return runCtx, ClaimCancelRegistration{ClaimID: claimID, ID: id, cancel: cancel, owner: r}
}

func (r *ClaimCancelRegistry) CancelClaim(claimID string) int {
	claimID = strings.TrimSpace(claimID)
	if r == nil || claimID == "" {
		return 0
	}
	r.mu.Lock()
	entries := r.entries[claimID]
	delete(r.entries, claimID)
	r.mu.Unlock()
	count := 0
	for _, cancel := range entries {
		if cancel != nil {
			cancel()
			count++
		}
	}
	return count
}

func (r *ClaimCancelRegistry) ActiveClaimIDs() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.entries))
	for claimID, entries := range r.entries {
		if len(entries) != 0 {
			ids = append(ids, claimID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (reg ClaimCancelRegistration) Done() {
	if reg.cancel != nil {
		reg.cancel()
	}
	if reg.owner == nil || strings.TrimSpace(reg.ClaimID) == "" || strings.TrimSpace(reg.ID) == "" {
		return
	}
	reg.owner.mu.Lock()
	if entries := reg.owner.entries[reg.ClaimID]; entries != nil {
		delete(entries, reg.ID)
		if len(entries) == 0 {
			delete(reg.owner.entries, reg.ClaimID)
		}
	}
	reg.owner.mu.Unlock()
}

func stableCancelRegistrationID(seq uint64) string {
	return "claim_cancel:" + strconv.FormatUint(seq, 10)
}
