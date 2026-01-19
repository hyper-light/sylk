// Package hooks provides AR.9.1: Hook Registration - convenience functions for
// registering all AR.7.x hooks with the adaptive hook registry.
package hooks

import (
	"errors"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Agent Type Constants
// =============================================================================

// knowledgeAgentTypes are agents that receive full speculative prefetch.
var knowledgeAgentTypes = []string{
	"librarian",
	"archivalist",
	"academic",
	"architect",
	"guide",
}

// pipelineAgentTypesForReg are agents that receive focused prefetch.
var pipelineAgentTypesForReg = []string{
	"engineer",
	"designer",
	"inspector",
	"tester",
}

// =============================================================================
// Hook Dependencies
// =============================================================================

// HookDependencies contains all dependencies needed to create AR hooks.
// Passed to RegisterAdaptiveRetrievalHooks for hook construction.
type HookDependencies struct {
	// Prefetcher provides speculative prefetch functionality.
	Prefetcher *ctxpkg.SpeculativePrefetcher

	// Augmenter provides query augmentation.
	Augmenter *ctxpkg.QueryAugmenter

	// EpisodeTracker tracks episode observations for Bayesian learning.
	EpisodeTracker *ctxpkg.EpisodeTracker

	// ContextManager for eviction operations.
	ContextManager EvictableContextManager

	// AccessTracker for recording content access patterns.
	AccessTracker AccessTracker

	// ContentPromoter for promoting content to hot cache.
	ContentPromoter ContentPromoter

	// FailureQuerier for querying past failure patterns.
	FailureQuerier FailurePatternQuerier

	// RoutingProvider for Guide routing history.
	RoutingProvider RoutingHistoryProvider

	// WorkflowProvider for Orchestrator workflow context.
	WorkflowProvider WorkflowContextProvider

	// FocusedAugmenter for pipeline agent prefetch.
	FocusedAugmenter FocusedAugmenter

	// ObservationLog for episode observation persistence.
	ObservationLog ObservationLogger
}

// =============================================================================
// Registration Functions
// =============================================================================

// RegisterAdaptiveRetrievalHooks registers all AR.7.x hooks with the given registry.
// Uses the provided dependencies to construct hooks with proper integrations.
func RegisterAdaptiveRetrievalHooks(registry *ctxpkg.AdaptiveHookRegistry, deps *HookDependencies) error {
	if registry == nil {
		return errors.New("registry is nil")
	}

	if deps == nil {
		deps = &HookDependencies{}
	}

	// Register global hooks (apply to all agents)
	if err := registerGlobalHooks(registry, deps); err != nil {
		return err
	}

	// Register knowledge agent hooks
	if err := registerKnowledgeAgentHooks(registry, deps); err != nil {
		return err
	}

	// Register pipeline agent hooks
	if err := registerPipelineAgentHooks(registry, deps); err != nil {
		return err
	}

	// Register Guide-specific hooks
	if err := registerGuideHooks(registry, deps); err != nil {
		return err
	}

	// Register Orchestrator-specific hooks
	if err := registerOrchestratorHooks(registry, deps); err != nil {
		return err
	}

	return nil
}

// registerGlobalHooks registers hooks that apply to all agents.
func registerGlobalHooks(registry *ctxpkg.AdaptiveHookRegistry, deps *HookDependencies) error {
	// AR.7.4: Failure Pattern Warning Hook (PrePrompt, Normal priority)
	if deps.FailureQuerier != nil {
		hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
			FailureQuerier: deps.FailureQuerier,
		})
		if err := registry.RegisterGlobalPromptHook(hook.ToPromptHook()); err != nil {
			return err
		}
	}

	// AR.7.5: Access Tracking Hook (PostPrompt, Normal priority)
	if deps.AccessTracker != nil {
		hook := NewAccessTrackingHook(AccessTrackingHookConfig{
			Tracker: deps.AccessTracker,
		})
		if err := registry.RegisterGlobalPromptHook(hook.ToPromptHook()); err != nil {
			return err
		}
	}

	// AR.7.5: Content Promotion Hook (PostTool, Late priority)
	if deps.AccessTracker != nil || deps.ContentPromoter != nil {
		hook := NewContentPromotionHook(ContentPromotionHookConfig{
			Tracker:  deps.AccessTracker,
			Promoter: deps.ContentPromoter,
		})
		if err := registry.RegisterGlobalToolCallHook(hook.ToToolCallHook()); err != nil {
			return err
		}
	}

	// AR.7.2: Episode Tracker Init Hook (PrePrompt, Early priority)
	if deps.EpisodeTracker != nil {
		initHook := NewEpisodeTrackerInitHook(EpisodeTrackerInitHookConfig{
			Tracker: deps.EpisodeTracker,
		})
		if err := registry.RegisterGlobalPromptHook(initHook.ToPromptHook()); err != nil {
			return err
		}

		// AR.7.2: Episode Observation Hook (PostPrompt, Late priority)
		obsHook := NewEpisodeObservationHook(EpisodeObservationHookConfig{
			Tracker:        deps.EpisodeTracker,
			ObservationLog: deps.ObservationLog,
		})
		if err := registry.RegisterGlobalPromptHook(obsHook.ToPromptHook()); err != nil {
			return err
		}

		// AR.7.2: Search Tool Observation Hook (PostTool, Normal priority)
		searchHook := NewSearchToolObservationHook(SearchToolObservationHookConfig{
			Tracker: deps.EpisodeTracker,
		})
		if err := registry.RegisterGlobalToolCallHook(searchHook.ToToolCallHook()); err != nil {
			return err
		}
	}

	return nil
}

// registerKnowledgeAgentHooks registers hooks for knowledge agents.
func registerKnowledgeAgentHooks(registry *ctxpkg.AdaptiveHookRegistry, deps *HookDependencies) error {
	for _, agentType := range knowledgeAgentTypes {
		// AR.7.1: Speculative Prefetch Hook (PrePrompt, First priority)
		if deps.Prefetcher != nil || deps.Augmenter != nil {
			hook := NewSpeculativePrefetchHook(PrefetchHookConfig{
				Prefetcher: deps.Prefetcher,
				Augmenter:  deps.Augmenter,
			})
			if err := registry.RegisterPromptHookForAgent(agentType, hook.ToPromptHook()); err != nil {
				return err
			}
		}

		// AR.7.3: Pressure Eviction Hook (PrePrompt, First priority)
		if deps.ContextManager != nil {
			hook := NewPressureEvictionHook(PressureEvictionHookConfig{
				ContextManager: deps.ContextManager,
			})
			if err := registry.RegisterPromptHookForAgent(agentType, hook.ToPromptHook()); err != nil {
				return err
			}
		}
	}

	return nil
}

// registerPipelineAgentHooks registers hooks for pipeline agents.
func registerPipelineAgentHooks(registry *ctxpkg.AdaptiveHookRegistry, deps *HookDependencies) error {
	for _, agentType := range pipelineAgentTypesForReg {
		// AR.7.8: Focused Prefetch Hook (PrePrompt, Early priority)
		if deps.FocusedAugmenter != nil {
			hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
				Augmenter: deps.FocusedAugmenter,
				MaxTokens: 1000, // Per CONTEXT.md specification
			})
			if err := registry.RegisterPromptHookForAgent(agentType, hook.ToPromptHook()); err != nil {
				return err
			}
		}
	}

	return nil
}

// registerGuideHooks registers Guide-specific hooks.
func registerGuideHooks(registry *ctxpkg.AdaptiveHookRegistry, deps *HookDependencies) error {
	// AR.7.6: Guide Routing Cache Hook (PrePrompt, Normal priority)
	if deps.RoutingProvider != nil {
		hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
			RoutingProvider: deps.RoutingProvider,
		})
		if err := registry.RegisterPromptHookForAgent("guide", hook.ToPromptHook()); err != nil {
			return err
		}
	}

	return nil
}

// registerOrchestratorHooks registers Orchestrator-specific hooks.
func registerOrchestratorHooks(registry *ctxpkg.AdaptiveHookRegistry, deps *HookDependencies) error {
	// AR.7.7: Workflow Context Hook (PrePrompt, Normal priority)
	if deps.WorkflowProvider != nil {
		hook := NewWorkflowContextHook(WorkflowContextHookConfig{
			WorkflowProvider: deps.WorkflowProvider,
		})
		if err := registry.RegisterPromptHookForAgent("orchestrator", hook.ToPromptHook()); err != nil {
			return err
		}
	}

	return nil
}
