package toolruntime

import (
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

type ManifestBuildConfig struct {
	AgentID          string
	CapabilityScope  string
	Registry         *skills.Registry
	VisibleByDefault []string
	Mutating         []string
	PolicyOverrides  []ToolPolicy
}

func BuildManifestFromRegistry(cfg ManifestBuildConfig) *PolicyManifest {
	manifest := &PolicyManifest{
		AgentID:         strings.TrimSpace(cfg.AgentID),
		CapabilityScope: strings.TrimSpace(cfg.CapabilityScope),
		Tools:           make(map[string]ToolPolicy),
	}

	visible := make(map[string]struct{}, len(cfg.VisibleByDefault))
	for _, name := range cfg.VisibleByDefault {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		visible[name] = struct{}{}
	}

	mutating := make(map[string]struct{}, len(cfg.Mutating))
	for _, name := range cfg.Mutating {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		mutating[name] = struct{}{}
	}

	if cfg.Registry != nil {
		for _, skill := range cfg.Registry.GetAll() {
			if skill == nil {
				continue
			}
			name := strings.TrimSpace(skill.Name)
			if name == "" {
				continue
			}

			effect := EffectReadOnly
			execution := ExecutionModeLocal
			if _, ok := mutating[name]; ok {
				effect = EffectMutating
				execution = ExecutionModeLocalWorker
			}

			policy := NewToolPolicy(name, effect, InferEffectDomain(skill.Domain), execution)
			policy.CapabilityScope = manifest.CapabilityScope
			if _, ok := visible[name]; ok {
				policy.VisibleByDefault = true
			}
			manifest.Tools[name] = policy
		}
	}

	for _, policy := range cfg.PolicyOverrides {
		if strings.TrimSpace(policy.Name) == "" {
			continue
		}
		policy.CapabilityScope = manifest.CapabilityScope
		manifest.Tools[policy.Name] = policy
	}

	if _, ok := manifest.Tools[SearchToolName]; !ok {
		searchPolicy := NewToolPolicy(
			SearchToolName,
			EffectReadOnly,
			DomainDiscovery,
			ExecutionModeLocal,
			WithVisibleByDefault(),
			WithSearchable(false),
		)
		searchPolicy.CapabilityScope = manifest.CapabilityScope
		manifest.Tools[SearchToolName] = searchPolicy
	}

	return ApplyAuthorityProfile(manifest.AgentID, manifest)
}

func InferEffectDomain(domain string) EffectDomain {
	switch strings.TrimSpace(strings.ToLower(domain)) {
	case "audit":
		return DomainAudit
	case "chronicle":
		return DomainChronicle
	case "code":
		return DomainCode
	case "communication":
		return DomainCommunication
	case "consultation":
		return DomainConsultation
	case "control":
		return DomainControl
	case "discovery":
		return DomainDiscovery
	case "filesystem", "file", "files":
		return DomainFilesystem
	case "git":
		return DomainGit
	case "knowledge":
		return DomainKnowledge
	case "memory":
		return DomainMemory
	case "observability":
		return DomainObservability
	case "orchestration":
		return DomainOrchestration
	case "planning":
		return DomainPlanning
	case "quality":
		return DomainQuality
	case "research":
		return DomainResearch
	case "system", "diagnostics", "diagnostic":
		return DomainSystem
	case "testing", "test":
		return DomainTesting
	case "ui", "design":
		return DomainUI
	case "validation":
		return DomainValidation
	default:
		return DomainSystem
	}
}
