package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

const auditDepthMetadataKey = "audit_depth_contract"

type AuditDepthContract struct {
	Version      int      `json:"version,omitempty"`
	Depth        string   `json:"depth,omitempty"`
	Rationale    string   `json:"rationale,omitempty"`
	ReconsiderIf []string `json:"reconsider_if,omitempty"`
}

type auditDepthState struct {
	mu       sync.RWMutex
	contract *AuditDepthContract
}

type auditDepthStateKey struct{}

func WithAuditDepthContext(ctx context.Context, metadata map[string]any) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing := auditDepthStateFromContext(ctx); existing != nil {
		if contract, ok := AuditDepthContractFromMetadata(metadata); ok {
			existing.Set(contract)
		}
		return ctx
	}
	state := &auditDepthState{}
	if contract, ok := AuditDepthContractFromMetadata(metadata); ok {
		state.Set(contract)
	}
	return context.WithValue(ctx, auditDepthStateKey{}, state)
}

func AuditDepthContractFromContext(ctx context.Context) (AuditDepthContract, bool) {
	if state := auditDepthStateFromContext(ctx); state != nil {
		if contract, ok := state.Contract(); ok {
			return contract, true
		}
	}
	if review := GlobalReviewStateFromContext(ctx); review != nil {
		if contract, ok := AuditDepthContractFromMetadata(review.BaseMetadata()); ok {
			return contract, true
		}
	}
	if stream, ok := StreamMetadataFromContext(ctx); ok {
		if contract, ok := AuditDepthContractFromMetadata(stream.Metadata); ok {
			return contract, true
		}
	}
	return AuditDepthContract{}, false
}

func EffectiveAuditDepthFromContext(ctx context.Context) ResearchDepth {
	contract, ok := AuditDepthContractFromContext(ctx)
	if !ok {
		return ResearchDepthUnset
	}
	return EffectiveResearchDepth(contract.Depth)
}

func PersistAuditDepthContract(ctx context.Context, contract AuditDepthContract) (AuditDepthContract, error) {
	normalized, ok := normalizeAuditDepthContract(contract)
	if !ok {
		return AuditDepthContract{}, fmt.Errorf("audit depth must be one of: %s", strings.Join(ResearchDepthEnumValues(), ", "))
	}
	if state := auditDepthStateFromContext(ctx); state != nil {
		state.Set(normalized)
	}
	if review := GlobalReviewStateFromContext(ctx); review != nil {
		review.SetBaseMetadata(MetadataWithAuditDepthContract(review.BaseMetadata(), normalized))
	}
	return normalized, nil
}

func AuditDepthContractFromMetadata(metadata map[string]any) (AuditDepthContract, bool) {
	if len(metadata) == 0 {
		return AuditDepthContract{}, false
	}
	raw, ok := metadata[auditDepthMetadataKey]
	if !ok || raw == nil {
		return AuditDepthContract{}, false
	}
	var contract AuditDepthContract
	switch typed := raw.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return AuditDepthContract{}, false
		}
		if err := json.Unmarshal([]byte(typed), &contract); err != nil {
			return AuditDepthContract{}, false
		}
	case map[string]any:
		payload, err := json.Marshal(typed)
		if err != nil {
			return AuditDepthContract{}, false
		}
		if err := json.Unmarshal(payload, &contract); err != nil {
			return AuditDepthContract{}, false
		}
	default:
		return AuditDepthContract{}, false
	}
	normalized, ok := normalizeAuditDepthContract(contract)
	if !ok {
		return AuditDepthContract{}, false
	}
	return normalized, true
}

func MetadataWithAuditDepthContract(metadata map[string]any, contract AuditDepthContract) map[string]any {
	cloned := CloneMetadataMap(metadata)
	if cloned == nil {
		cloned = make(map[string]any, 1)
	}
	normalized, ok := normalizeAuditDepthContract(contract)
	if !ok {
		delete(cloned, auditDepthMetadataKey)
		return cloned
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return cloned
	}
	cloned[auditDepthMetadataKey] = string(payload)
	return cloned
}

func (s *auditDepthState) Contract() (AuditDepthContract, bool) {
	if s == nil {
		return AuditDepthContract{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.contract == nil {
		return AuditDepthContract{}, false
	}
	return cloneAuditDepthContract(*s.contract), true
}

func (s *auditDepthState) Set(contract AuditDepthContract) {
	if s == nil {
		return
	}
	normalized, ok := normalizeAuditDepthContract(contract)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := cloneAuditDepthContract(normalized)
	s.contract = &copy
}

func auditDepthStateFromContext(ctx context.Context) *auditDepthState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(auditDepthStateKey{}).(*auditDepthState)
	return state
}

func normalizeAuditDepthContract(contract AuditDepthContract) (AuditDepthContract, bool) {
	depth := NormalizeResearchDepth(contract.Depth)
	if depth == ResearchDepthUnset {
		return AuditDepthContract{}, false
	}
	return AuditDepthContract{
		Version:      1,
		Depth:        string(depth),
		Rationale:    strings.TrimSpace(contract.Rationale),
		ReconsiderIf: normalizeStringList(contract.ReconsiderIf),
	}, true
}

func cloneAuditDepthContract(contract AuditDepthContract) AuditDepthContract {
	contract.ReconsiderIf = append([]string(nil), contract.ReconsiderIf...)
	return contract
}
