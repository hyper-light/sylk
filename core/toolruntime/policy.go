package toolruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

type EffectKind string

const (
	EffectReadOnly EffectKind = "read_only"
	EffectMutating EffectKind = "mutating"
)

type ExecutionMode string

const (
	ExecutionModeLocal       ExecutionMode = "local"
	ExecutionModeLocalWorker ExecutionMode = "local_worker"
	ExecutionModeGuardian    ExecutionMode = "guardian_controlled"
)

type EffectDomain string

const (
	DomainAudit         EffectDomain = "audit"
	DomainChronicle     EffectDomain = "chronicle"
	DomainCode          EffectDomain = "code"
	DomainCommunication EffectDomain = "communication"
	DomainControl       EffectDomain = "control"
	DomainConsultation  EffectDomain = "consultation"
	DomainDiscovery     EffectDomain = "discovery"
	DomainFilesystem    EffectDomain = "filesystem"
	DomainGit           EffectDomain = "git"
	DomainKnowledge     EffectDomain = "knowledge"
	DomainMemory        EffectDomain = "memory"
	DomainOrchestration EffectDomain = "orchestration"
	DomainPlanning      EffectDomain = "planning"
	DomainQuality       EffectDomain = "quality"
	DomainResearch      EffectDomain = "research"
	DomainSystem        EffectDomain = "system"
	DomainTesting       EffectDomain = "testing"
	DomainUI            EffectDomain = "ui"
	DomainValidation    EffectDomain = "validation"
	DomainObservability EffectDomain = "observability"
)

type ToolPolicy struct {
	Name              string        `json:"name"`
	CapabilityScope   string        `json:"capability_scope"`
	Effect            EffectKind    `json:"effect"`
	Domain            EffectDomain  `json:"domain"`
	Execution         ExecutionMode `json:"execution"`
	ApprovalSensitive bool          `json:"approval_sensitive,omitempty"`
	VisibleByDefault  bool          `json:"visible_by_default,omitempty"`
	Searchable        bool          `json:"searchable,omitempty"`
}

type PolicyManifest struct {
	AgentID         string                `json:"agent_id"`
	CapabilityScope string                `json:"capability_scope"`
	Tools           map[string]ToolPolicy `json:"tools"`
}

type PolicyOption func(*ToolPolicy)

func NewToolPolicy(name string, effect EffectKind, domain EffectDomain, execution ExecutionMode, opts ...PolicyOption) ToolPolicy {
	policy := ToolPolicy{
		Name:       strings.TrimSpace(name),
		Effect:     effect,
		Domain:     domain,
		Execution:  execution,
		Searchable: true,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&policy)
		}
	}
	return policy
}

func WithApprovalSensitive() PolicyOption {
	return func(policy *ToolPolicy) {
		if policy == nil {
			return
		}
		policy.ApprovalSensitive = true
	}
}

func WithVisibleByDefault() PolicyOption {
	return func(policy *ToolPolicy) {
		if policy == nil {
			return
		}
		policy.VisibleByDefault = true
	}
}

func WithSearchable(searchable bool) PolicyOption {
	return func(policy *ToolPolicy) {
		if policy == nil {
			return
		}
		policy.Searchable = searchable
	}
}

func NewManifest(agentID, capabilityScope string, policies ...ToolPolicy) *PolicyManifest {
	manifest := &PolicyManifest{
		AgentID:         strings.TrimSpace(agentID),
		CapabilityScope: strings.TrimSpace(capabilityScope),
		Tools:           make(map[string]ToolPolicy, len(policies)),
	}
	for _, policy := range policies {
		if policy.Name == "" {
			continue
		}
		policy.CapabilityScope = manifest.CapabilityScope
		manifest.Tools[policy.Name] = policy
	}
	return manifest
}

func (m *PolicyManifest) Validate(registry *skills.Registry, controlPlaneConfigured bool) error {
	if m == nil {
		return fmt.Errorf("tool policy manifest is required")
	}
	if strings.TrimSpace(m.AgentID) == "" {
		return fmt.Errorf("tool policy manifest agent_id is required")
	}
	if strings.TrimSpace(m.CapabilityScope) == "" {
		return fmt.Errorf("tool policy manifest capability_scope is required")
	}
	if len(m.Tools) == 0 {
		return fmt.Errorf("tool policy manifest must declare at least one tool")
	}

	for name, policy := range m.Tools {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("tool policy contains an empty tool name")
		}
		if policy.Name != name {
			return fmt.Errorf("tool policy name mismatch: key=%q policy=%q", name, policy.Name)
		}
		if policy.CapabilityScope != "" && policy.CapabilityScope != m.CapabilityScope {
			return fmt.Errorf("tool %q capability_scope mismatch: %q != %q", name, policy.CapabilityScope, m.CapabilityScope)
		}
		if policy.Effect != EffectReadOnly && policy.Effect != EffectMutating {
			return fmt.Errorf("tool %q has invalid effect %q", name, policy.Effect)
		}
		if strings.TrimSpace(string(policy.Domain)) == "" {
			return fmt.Errorf("tool %q must declare an effect domain", name)
		}
		if policy.Execution != ExecutionModeLocal && policy.Execution != ExecutionModeLocalWorker && policy.Execution != ExecutionModeGuardian {
			return fmt.Errorf("tool %q has invalid execution mode %q", name, policy.Execution)
		}
		if policy.Effect == EffectMutating && policy.Execution == ExecutionModeLocal {
			return fmt.Errorf("tool %q is mutating and must use local_worker or guardian_controlled execution", name)
		}
		if policy.Execution == ExecutionModeGuardian && policy.Effect != EffectMutating {
			return fmt.Errorf("tool %q uses guardian-controlled execution but is not marked mutating", name)
		}
		if policy.Execution == ExecutionModeGuardian && !policy.ApprovalSensitive {
			return fmt.Errorf("tool %q uses guardian-controlled execution but is not marked approval-sensitive", name)
		}
		if policy.ApprovalSensitive && m.AgentID != "guardian" && policy.Execution != ExecutionModeGuardian {
			return fmt.Errorf("tool %q is approval-sensitive and must use guardian-controlled execution", name)
		}
		if policy.Execution == ExecutionModeGuardian && !controlPlaneConfigured && m.AgentID != "guardian" {
			return fmt.Errorf("tool %q requires guardian control but no guardian control plane is configured", name)
		}
		if !isSyntheticTool(name) {
			if registry == nil {
				return fmt.Errorf("tool registry is required to validate %q", name)
			}
			if registry.Get(name) == nil {
				return fmt.Errorf("tool %q is declared in policy but not registered", name)
			}
		}
	}
	if registry != nil {
		missing := make([]string, 0)
		for _, skill := range registry.GetAll() {
			if skill == nil {
				continue
			}
			name := strings.TrimSpace(skill.Name)
			if name == "" || isSyntheticTool(name) {
				continue
			}
			if _, ok := m.Tools[name]; !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return fmt.Errorf("tool policy manifest is missing registered tools: %s", strings.Join(missing, ", "))
		}
	}
	return nil
}

func (m *PolicyManifest) AllowedNames() []string {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.Tools))
	for name := range m.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *PolicyManifest) DefaultVisibleNames() []string {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.Tools))
	for name, policy := range m.Tools {
		if policy.VisibleByDefault {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (m *PolicyManifest) SearchableNames() []string {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.Tools))
	for name, policy := range m.Tools {
		if policy.Searchable {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (m *PolicyManifest) Policy(name string) (ToolPolicy, bool) {
	if m == nil {
		return ToolPolicy{}, false
	}
	policy, ok := m.Tools[strings.TrimSpace(name)]
	return policy, ok
}

func (m *PolicyManifest) Fingerprint() string {
	if m == nil {
		return ""
	}
	type manifestJSON struct {
		AgentID         string       `json:"agent_id"`
		CapabilityScope string       `json:"capability_scope"`
		Tools           []ToolPolicy `json:"tools"`
	}
	tools := make([]ToolPolicy, 0, len(m.Tools))
	for _, name := range m.AllowedNames() {
		policy := m.Tools[name]
		policy.CapabilityScope = m.CapabilityScope
		tools = append(tools, policy)
	}
	payload, err := json.Marshal(manifestJSON{
		AgentID:         m.AgentID,
		CapabilityScope: m.CapabilityScope,
		Tools:           tools,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func isSyntheticTool(name string) bool {
	return strings.TrimSpace(name) == SearchToolName
}
