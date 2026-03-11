package shared

import "strings"

func AppendDiskOnlyContext(base string) string {
	return appendCapabilityContext(base, strings.TrimSpace(`
# Filesystem Access

You can read committed on-disk repository state only.
You do not have access to global VFS state or pipeline VFS state.
Treat disk as the source of truth.
`))
}

func AppendNoFilesystemContext(base string) string {
	return appendCapabilityContext(base, strings.TrimSpace(`
# Filesystem Access

You do not have direct filesystem access.
Do not plan around reading files yourself.
Rely on consultations, artifacts, summaries, and reports produced by other agents.
`))
}

func appendCapabilityContext(base, section string) string {
	if strings.TrimSpace(section) == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return section
	}
	return base + "\n\n---\n\n" + section
}
