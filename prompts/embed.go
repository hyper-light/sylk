package prompts

import "embed"

//go:embed */*.md
var fs embed.FS

// MustLoad reads an embedded prompt file by agent and name.
// Panics if the file is missing — this is a build-time guarantee since
// embed.FS contents are resolved at compilation.
func MustLoad(agent, name string) string {
	data, err := fs.ReadFile(agent + "/" + name + ".md")
	if err != nil {
		panic("prompts: missing embedded file: " + agent + "/" + name + ".md")
	}
	return string(data)
}
