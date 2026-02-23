package promptskills

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	promptskills "github.com/adalundhe/sylk/skills"
)

// DiscoverAllAgentSkills loads prompt-skill metadata from every
// agents/<agent>/skillfiles directory found in the repo.
func DiscoverAllAgentSkills() []promptskills.Skill {
	root := RepoRoot()
	if root == "" {
		return nil
	}
	agentsDir := filepath.Join(root, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil
	}

	skillDirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(agentsDir, entry.Name(), "skillfiles")
		if !isDir(dir) {
			continue
		}
		skillDirs = append(skillDirs, dir)
	}
	sort.Strings(skillDirs)
	return DiscoverFromSkillDirs(skillDirs)
}

// DiscoverAgentSkills loads prompt-skill metadata from a single
// agents/<agent>/skillfiles directory.
func DiscoverAgentSkills(agentID string) []promptskills.Skill {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	root := RepoRoot()
	if root == "" {
		return nil
	}
	dir := filepath.Join(root, "agents", agentID, "skillfiles")
	if !isDir(dir) {
		return nil
	}
	return DiscoverFromSkillDirs([]string{dir})
}

// DiscoverFromSkillDirs loads prompt-skill metadata from the provided
// directories. Missing or invalid directories are ignored.
func DiscoverFromSkillDirs(skillDirs []string) []promptskills.Skill {
	if len(skillDirs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(skillDirs))
	collected := make([]promptskills.Skill, 0, len(skillDirs))
	for _, dir := range skillDirs {
		trimmed := strings.TrimSpace(dir)
		if trimmed == "" || !isDir(trimmed) {
			continue
		}
		skills, err := promptskills.Discover(trimmed)
		if err != nil {
			continue
		}
		for _, skill := range skills {
			key := skill.Name + "\x00" + skill.Path
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			collected = append(collected, skill)
		}
	}
	sort.Slice(collected, func(i, j int) bool {
		if collected[i].Name != collected[j].Name {
			return collected[i].Name < collected[j].Name
		}
		return collected[i].Path < collected[j].Path
	})
	return collected
}

// RepoRoot resolves the repository root by probing from cwd and source file
// locations for a directory containing go.mod and agents/.
func RepoRoot() string {
	for _, candidate := range repoRootCandidates() {
		root := ascendToRepoRoot(candidate)
		if root != "" {
			return root
		}
	}
	return ""
}

func repoRootCandidates() []string {
	candidates := make([]string, 0, 3)
	if wd, err := os.Getwd(); err == nil && strings.TrimSpace(wd) != "" {
		candidates = append(candidates, wd)
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, file, filepath.Dir(file))
	}
	return uniqueNonEmpty(candidates)
}

func ascendToRepoRoot(start string) string {
	dir := strings.TrimSpace(start)
	if dir == "" {
		return ""
	}
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		if hasRepoMarkers(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func hasRepoMarkers(dir string) bool {
	if dir == "" {
		return false
	}
	return fileExists(filepath.Join(dir, "go.mod")) &&
		isDir(filepath.Join(dir, "agents"))
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func uniqueNonEmpty(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}
