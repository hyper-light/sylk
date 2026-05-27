package shared

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestPhase7CrossPipelineConfigHasNoRouteSync(t *testing.T) {
	cfgType := reflect.TypeOf(CrossPipelineSkillConfig{})
	for i := 0; i < cfgType.NumField(); i++ {
		if strings.Contains(cfgType.Field(i).Name, "RouteSync") {
			t.Fatalf("CrossPipelineSkillConfig still exposes legacy %s", cfgType.Field(i).Name)
		}
	}
}

func TestPhase7ReactiveAgentsDoNotRegisterLegacyConsultSkill(t *testing.T) {
	root := repositoryRoot(t)
	paths := []string{
		"agents/academic/skills.go",
		"agents/librarian/skills.go",
		"agents/archivalist/skills_core.go",
	}
	for _, rel := range paths {
		t.Run(rel, func(t *testing.T) {
			src := readRepoFile(t, root, rel)
			if strings.Contains(src, "Register(consultSkill") {
				t.Fatalf("%s registers legacy synchronous consult skill", rel)
			}
		})
	}
}

func TestPhase7AgentsDoNotDefineLegacyConsultSkill(t *testing.T) {
	root := repositoryRoot(t)
	paths := []string{
		"agents/academic/skills.go",
		"agents/architect/skills_planning.go",
		"agents/archivalist/skills_core.go",
		"agents/librarian/skills.go",
		"agents/engineer/skills.go",
	}
	for _, rel := range paths {
		t.Run(rel, func(t *testing.T) {
			src := readRepoFile(t, root, rel)
			if strings.Contains(src, "func consultSkill") {
				t.Fatalf("%s still defines legacy synchronous consult skill", rel)
			}
		})
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}
