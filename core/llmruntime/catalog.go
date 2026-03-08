package llmruntime

import "strings"

type agentProfileSet struct {
	defaultProfile string
	profiles       []StageProfile
}

var agentProfileCatalog = map[string]agentProfileSet{
	"academic": {
		defaultProfile: "research",
		profiles: []StageProfile{
			{
				Name:              "research",
				Intents:           []string{"recall", "check"},
				Domains:           []string{"research", "patterns", "decisions", "learnings"},
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "fetch",
				Intents:           []string{"fetch"},
				Domains:           []string{"research"},
				Deliberation:      DeliberationMedium,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismParallel,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
	"archivalist": {
		defaultProfile: "conversation",
		profiles: []StageProfile{
			{
				Name:              "conversation",
				Intents:           []string{"recall", "store", "check", "declare", "complete", "help"},
				Domains:           []string{"patterns", "failures", "decisions", "files", "learnings", "intents"},
				Deliberation:      DeliberationMedium,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "generation",
				Deliberation:      DeliberationMedium,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "synthesis",
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
	"architect": {
		defaultProfile: "planning_protocol",
		profiles: []StageProfile{
			{
				Name:              "planning_protocol",
				Intents:           []string{"plan", "design", "execute"},
				Domains:           []string{"design", "tasks"},
				Deliberation:      DeliberationMax,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilitySummary,
			},
			{
				Name:              "requirements",
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilitySummary,
			},
			{
				Name:              "design",
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilitySummary,
			},
			{
				Name:              "tasks",
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilitySummary,
			},
			{
				Name:              "conversation_clarification",
				Deliberation:      DeliberationLow,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "conversation_ready",
				Deliberation:      DeliberationLow,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "conversation_converse",
				Intents:           []string{"recall", "check", "help"},
				Domains:           []string{"design", "tasks"},
				Deliberation:      DeliberationLow,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "conversation_feedback",
				Deliberation:      DeliberationLow,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
	"designer": {
		defaultProfile: "design",
		profiles: []StageProfile{
			{
				Name:              "design",
				Intents:           []string{"design", "complete", "check"},
				Domains:           []string{"code", "files"},
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
	"engineer": {
		defaultProfile: "implementation",
		profiles: []StageProfile{
			{
				Name:              "implementation",
				Intents:           []string{"complete"},
				Domains:           []string{"code", "files"},
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "audit",
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
	"guide": {
		defaultProfile: "self_response",
		profiles: []StageProfile{
			{
				Name:              "classifier",
				Deliberation:      DeliberationLow,
				ResponseDetail:    ResponseDetailTerse,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinityNone,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "self_response",
				Intents:           []string{"chat", "help", "status"},
				Domains:           []string{"system", "general"},
				Deliberation:      DeliberationMedium,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "plan_acceptance",
				Deliberation:      DeliberationMedium,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
	"guardian": {
		defaultProfile: "conversation_chat",
		profiles: []StageProfile{
			{
				Name:              "conversation_chat",
				Intents:           []string{"chat", "help"},
				Domains:           []string{"system", "compliance"},
				Deliberation:      DeliberationMedium,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismParallel,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "conversation_report",
				Intents:           []string{"status"},
				Domains:           []string{"system", "compliance"},
				Deliberation:      DeliberationMedium,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismParallel,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "conversation_assess",
				Intents:           []string{"check"},
				Domains:           []string{"compliance"},
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismParallel,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "conversation_checkpoint",
				Intents:           []string{"check"},
				Domains:           []string{"system"},
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismParallel,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
	"inspector": {
		defaultProfile: "audit",
		profiles: []StageProfile{
			{
				Name:              "conversation",
				Intents:           []string{"chat", "help", "recall"},
				Domains:           []string{"code"},
				Deliberation:      DeliberationMedium,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilitySummary,
			},
			{
				Name:              "audit",
				Intents:           []string{"check"},
				Domains:           []string{"code"},
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilitySummary,
			},
			{
				Name:              "task",
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilitySummary,
			},
		},
	},
	"inspector-pipeline": {
		defaultProfile: "task",
		profiles: []StageProfile{
			{
				Name:              "task",
				Intents:           []string{"check", "recall", "help"},
				Domains:           []string{"code"},
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "validation",
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "revalidation",
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
	"librarian": {
		defaultProfile: "search",
		profiles: []StageProfile{
			{
				Name:              "search",
				Intents:           []string{"find", "search", "locate", "recall", "check", "help"},
				Domains:           []string{"code"},
				Deliberation:      DeliberationMedium,
				ResponseDetail:    ResponseDetailTerse,
				ToolParallelism:   ToolParallelismParallel,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "fetch_repository",
				Intents:           []string{"fetch"},
				Domains:           []string{"code"},
				Deliberation:      DeliberationMedium,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismParallel,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
	"orchestrator": {
		defaultProfile: "conversation",
		profiles: []StageProfile{
			{
				Name:              "conversation",
				Intents:           []string{"chat", "status", "recall", "help"},
				Domains:           []string{"tasks", "workflow", "health", "system"},
				Deliberation:      DeliberationMedium,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "background_loop",
				Deliberation:      DeliberationLow,
				ResponseDetail:    ResponseDetailTerse,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
	"scribe": {
		defaultProfile: "commentary",
		profiles: []StageProfile{
			{
				Name:              "commentary",
				Deliberation:      DeliberationLow,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
	"tester": {
		defaultProfile: "testing",
		profiles: []StageProfile{
			{
				Name:              "conversation",
				Intents:           []string{"chat", "help", "recall"},
				Domains:           []string{"code", "system"},
				Deliberation:      DeliberationMedium,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "testing",
				Intents:           []string{"check"},
				Domains:           []string{"code"},
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySticky,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
	"tester-pipeline": {
		defaultProfile: "validation",
		profiles: []StageProfile{
			{
				Name:              "validation",
				Intents:           []string{"check", "recall", "help"},
				Domains:           []string{"code"},
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailNormal,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
			{
				Name:              "revalidation",
				Deliberation:      DeliberationHigh,
				ResponseDetail:    ResponseDetailDetailed,
				ToolParallelism:   ToolParallelismSerial,
				CacheAffinity:     CacheAffinitySession,
				ThoughtVisibility: ThoughtVisibilityHidden,
			},
		},
	},
}

func AgentProfiles(agentType string) []StageProfile {
	set, ok := agentProfileSetFor(agentType)
	if !ok {
		return nil
	}
	return cloneStageProfiles(set.profiles)
}

func AgentDefaultProfile(agentType string) string {
	set, ok := agentProfileSetFor(agentType)
	if !ok {
		return ""
	}
	return set.defaultProfile
}

func AgentStageProfile(agentType string, name string) (StageProfile, bool) {
	set, ok := agentProfileSetFor(agentType)
	if !ok {
		return StageProfile{}, false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	for _, profile := range set.profiles {
		if strings.EqualFold(profile.Name, name) {
			return cloneStageProfile(profile), true
		}
	}
	return StageProfile{}, false
}

func ResolveAgentStageProfile(agentType string, name string) StageProfile {
	if profile, ok := AgentStageProfile(agentType, name); ok {
		return profile
	}
	if defaultName := AgentDefaultProfile(agentType); defaultName != "" {
		if profile, ok := AgentStageProfile(agentType, defaultName); ok {
			return profile
		}
	}
	return StageProfile{Name: strings.TrimSpace(name)}
}

func agentProfileSetFor(agentType string) (agentProfileSet, bool) {
	normalized := strings.ToLower(strings.TrimSpace(agentType))
	if strings.HasPrefix(normalized, "scribe-") {
		normalized = "scribe"
	}
	set, ok := agentProfileCatalog[normalized]
	return set, ok
}

func cloneStageProfiles(profiles []StageProfile) []StageProfile {
	if len(profiles) == 0 {
		return nil
	}
	cloned := make([]StageProfile, len(profiles))
	for i, profile := range profiles {
		cloned[i] = cloneStageProfile(profile)
	}
	return cloned
}

func cloneStageProfile(profile StageProfile) StageProfile {
	cloned := profile
	cloned.Intents = append([]string(nil), profile.Intents...)
	cloned.Domains = append([]string(nil), profile.Domains...)
	return cloned
}
