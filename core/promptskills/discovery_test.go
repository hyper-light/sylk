package promptskills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverFromSkillDirs_LoadsAndSortsSkills(t *testing.T) {
	root := t.TempDir()
	skillfiles := filepath.Join(root, "skillfiles")
	mustWriteSkill(t, skillfiles, "zeta", "zeta-skill", "Zeta description")
	mustWriteSkill(t, skillfiles, "alpha", "alpha-skill", "Alpha description")

	got := DiscoverFromSkillDirs([]string{skillfiles})
	if len(got) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(got))
	}
	if got[0].Name != "alpha-skill" {
		t.Fatalf("expected first skill alpha-skill, got %q", got[0].Name)
	}
	if got[1].Name != "zeta-skill" {
		t.Fatalf("expected second skill zeta-skill, got %q", got[1].Name)
	}
}

func TestDiscoverFromSkillDirs_IgnoresMissingDirectories(t *testing.T) {
	root := t.TempDir()
	skillfiles := filepath.Join(root, "skillfiles")
	mustWriteSkill(t, skillfiles, "alpha", "alpha-skill", "Alpha description")

	got := DiscoverFromSkillDirs([]string{
		filepath.Join(root, "missing"),
		skillfiles,
		"",
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(got))
	}
	if got[0].Name != "alpha-skill" {
		t.Fatalf("expected alpha-skill, got %q", got[0].Name)
	}
}

func TestDiscoverFromSkillDirs_DedupesSameSkillPath(t *testing.T) {
	root := t.TempDir()
	skillfiles := filepath.Join(root, "skillfiles")
	mustWriteSkill(t, skillfiles, "alpha", "alpha-skill", "Alpha description")

	got := DiscoverFromSkillDirs([]string{skillfiles, skillfiles})
	if len(got) != 1 {
		t.Fatalf("expected deduped 1 skill, got %d", len(got))
	}
}

func mustWriteSkill(t *testing.T, skillfilesDir, folder, name, desc string) {
	t.Helper()
	dir := filepath.Join(skillfilesDir, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}
