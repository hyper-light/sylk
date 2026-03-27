package architect

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adalundhe/sylk/core/storage"
	coreversioning "github.com/adalundhe/sylk/core/versioning"
)

var planArtifactVersionSuffixPattern = regexp.MustCompile(`_v\d+\.\d+$`)

func normalizedPlanArtifactVersion(plan *DesignPlan) coreversioning.SemanticVersion {
	if plan == nil {
		return coreversioning.SemanticVersion{Major: 1, Minor: 0}
	}
	if !plan.ArtifactVersion.IsZero() {
		return plan.ArtifactVersion
	}
	if plan.Revision <= 1 {
		return coreversioning.SemanticVersion{Major: 1, Minor: 0}
	}
	return coreversioning.SemanticVersion{Major: 1, Minor: uint32(plan.Revision - 1)}
}

func ensurePlanArtifactMetadata(baseDir string, plan *DesignPlan) error {
	if plan == nil {
		return nil
	}
	if plan.Revision <= 0 {
		plan.Revision = 1
	}
	plan.ArtifactVersion = normalizedPlanArtifactVersion(plan)
	if strings.TrimSpace(plan.PlanFile) == "" {
		plan.PlanFile = defaultVersionedPlanMarkdownPath(baseDir, plan.SessionID, plan.Query, plan.ID, plan.ArtifactVersion)
	}
	return ensurePlanMarkdownFileExists(plan.PlanFile, plan.Query, plan.ArtifactVersion)
}

func defaultVersionedPlanMarkdownPath(baseDir, sessionID, query, planID string, version coreversioning.SemanticVersion) string {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}
	sessionID = normalizeSessionID(sessionID)
	slug := storage.Slug(query)
	if strings.TrimSpace(slug) == "" {
		slug = "plan"
	}
	filename := fmt.Sprintf("%s_%s_v%s.md", slug, strings.TrimSpace(planID), version.String())
	return filepath.Join(baseDir, ".sylk", "sessions", sessionID, "plans", filename)
}

func versionedPlanSnapshotPath(baseDir, sessionID, planID string, version coreversioning.SemanticVersion) string {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}
	sessionID = normalizeSessionID(sessionID)
	filename := fmt.Sprintf("%s_v%s.json", strings.TrimSpace(planID), version.String())
	return filepath.Join(baseDir, ".sylk", "sessions", sessionID, "agents", "architect", "plans", filename)
}

func legacyPlanSnapshotPath(baseDir, sessionID, planID string) string {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}
	sessionID = normalizeSessionID(sessionID)
	filename := fmt.Sprintf("%s.json", strings.TrimSpace(planID))
	return filepath.Join(baseDir, ".sylk", "sessions", sessionID, "agents", "architect", "plans", filename)
}

func nextVersionedPlanMarkdownPath(baseDir string, plan *DesignPlan, next coreversioning.SemanticVersion) string {
	if plan == nil {
		return ""
	}
	existing := strings.TrimSpace(plan.PlanFile)
	if existing == "" {
		return defaultVersionedPlanMarkdownPath(baseDir, plan.SessionID, plan.Query, plan.ID, next)
	}
	dir := filepath.Dir(existing)
	name := filepath.Base(existing)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	base = planArtifactVersionSuffixPattern.ReplaceAllString(base, "")
	if ext == "" {
		ext = ".md"
	}
	return filepath.Join(dir, fmt.Sprintf("%s_v%s%s", base, next.String(), ext))
}

func ensurePlanMarkdownFileExists(path string, query string, version coreversioning.SemanticVersion) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	content := fmt.Sprintf("# Plan\n\n## Version\n%s\n\n## Task\n%s\n", version.String(), strings.TrimSpace(query))
	return os.WriteFile(path, []byte(content), 0o644)
}

func clonePlanMarkdownToVersion(oldPath, newPath, query string, version coreversioning.SemanticVersion) error {
	if strings.TrimSpace(newPath) == "" {
		return nil
	}
	if strings.TrimSpace(oldPath) == "" || samePath(oldPath, newPath) {
		return ensurePlanMarkdownFileExists(newPath, query, version)
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(oldPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ensurePlanMarkdownFileExists(newPath, query, version)
		}
		return err
	}
	if _, err := os.Stat(newPath); err == nil {
		return nil
	}
	return os.WriteFile(newPath, data, 0o644)
}

func samePath(a, b string) bool {
	return filepath.Clean(strings.TrimSpace(a)) == filepath.Clean(strings.TrimSpace(b))
}
