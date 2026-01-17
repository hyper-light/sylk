package credentials

import (
	"sync"
	"testing"
)

func TestDefaultScopes_Librarian(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()
	scope := sm.GetScope("librarian")

	if !containsProvider(scope.Allowed, "openai") {
		t.Error("librarian should have openai allowed")
	}
	if !containsProvider(scope.Allowed, "anthropic") {
		t.Error("librarian should have anthropic allowed")
	}
	if !containsProvider(scope.Allowed, "voyage") {
		t.Error("librarian should have voyage allowed")
	}
	if !containsProvider(scope.Denied, "github") {
		t.Error("librarian should have github denied")
	}
	if !containsProvider(scope.Denied, "aws") {
		t.Error("librarian should have aws denied")
	}
}

func TestDefaultScopes_Engineer(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()
	scope := sm.GetScope("engineer")

	if !containsProvider(scope.Allowed, "github") {
		t.Error("engineer should have github allowed")
	}
	if !scope.RequireAuth {
		t.Error("engineer should require auth")
	}
}

func TestDefaultScopes_Orchestrator(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()
	scope := sm.GetScope("orchestrator")

	if len(scope.Allowed) != 0 {
		t.Error("orchestrator should have no allowed credentials")
	}
	if !containsProvider(scope.Denied, "*") {
		t.Error("orchestrator should deny all")
	}
}

func TestDefaultScopes_UnknownAgent(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()
	scope := sm.GetScope("unknown_agent")

	if len(scope.Allowed) != 0 {
		t.Error("unknown agent should have no allowed")
	}
	if !containsProvider(scope.Denied, "*") {
		t.Error("unknown agent should deny all by default")
	}
}

func TestIsAllowed_DenyOverridesAllow(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()

	if sm.IsAllowed("librarian", "github") {
		t.Error("librarian should not be allowed github (in deny list)")
	}
	if !sm.IsAllowed("librarian", "openai") {
		t.Error("librarian should be allowed openai")
	}
}

func TestIsAllowed_WildcardDeny(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()

	if sm.IsAllowed("orchestrator", "openai") {
		t.Error("orchestrator with wildcard deny should not be allowed any provider")
	}
	if sm.IsAllowed("orchestrator", "anthropic") {
		t.Error("orchestrator with wildcard deny should not be allowed any provider")
	}
}

func TestProjectOverrides_RestrictOnly(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()

	override := &CredentialScope{
		AgentType: "librarian",
		Allowed:   []string{"openai"},
		Denied:    []string{},
	}
	sm.SetProjectOverride(override)

	if sm.IsAllowed("librarian", "anthropic") {
		t.Error("override should restrict librarian to only openai")
	}
	if !sm.IsAllowed("librarian", "openai") {
		t.Error("override should still allow openai")
	}
}

func TestProjectOverrides_AddDenied(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()

	override := &CredentialScope{
		AgentType: "librarian",
		Allowed:   []string{"openai", "anthropic", "voyage"},
		Denied:    []string{"voyage"},
	}
	sm.SetProjectOverride(override)

	if sm.IsAllowed("librarian", "voyage") {
		t.Error("override should deny voyage")
	}
}

func TestProjectOverrides_RequireAuthMerge(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()

	override := &CredentialScope{
		AgentType:   "librarian",
		Allowed:     []string{"openai"},
		RequireAuth: true,
	}
	sm.SetProjectOverride(override)

	scope := sm.GetScope("librarian")
	if !scope.RequireAuth {
		t.Error("merged scope should require auth when override does")
	}
}

func TestClearProjectOverrides(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()

	sm.SetProjectOverride(&CredentialScope{
		AgentType: "librarian",
		Allowed:   []string{"openai"},
	})

	sm.ClearProjectOverrides()

	if !sm.IsAllowed("librarian", "anthropic") {
		t.Error("after clearing overrides, default should apply")
	}
}

func TestListAllowedProviders(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()

	allowed := sm.ListAllowedProviders("librarian")

	if !containsProvider(allowed, "openai") {
		t.Error("allowed providers should include openai")
	}
	if containsProvider(allowed, "github") {
		t.Error("allowed providers should not include denied github")
	}
}

func TestRequiresAuth(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()

	if !sm.RequiresAuth("engineer", "github") {
		t.Error("engineer should require auth")
	}
	if sm.RequiresAuth("librarian", "openai") {
		t.Error("librarian should not require auth")
	}
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(3)

		go func() {
			defer wg.Done()
			_ = sm.GetScope("librarian")
		}()

		go func() {
			defer wg.Done()
			_ = sm.IsAllowed("engineer", "github")
		}()

		go func(n int) {
			defer wg.Done()
			sm.SetProjectOverride(&CredentialScope{
				AgentType: "test",
				Allowed:   []string{"openai"},
			})
		}(i)
	}

	wg.Wait()
}

func TestAllDefaultAgents(t *testing.T) {
	t.Parallel()

	sm := NewScopeManager()
	agents := []string{
		"librarian", "academic", "archivalist", "engineer",
		"designer", "inspector", "tester", "guide", "architect", "orchestrator",
	}

	for _, agent := range agents {
		scope := sm.GetScope(agent)
		if scope.AgentType != agent {
			t.Errorf("scope for %s has wrong agent type", agent)
		}
	}
}

func containsProvider(list []string, provider string) bool {
	for _, p := range list {
		if p == provider {
			return true
		}
	}
	return false
}
