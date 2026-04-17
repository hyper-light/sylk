package prompts

import (
	"embed"
	"errors"
	"fmt"
	"sort"
	"sync"
)

//go:embed */*.md
var fs embed.FS

var (
	missingMu    sync.Mutex
	missingLoads = map[string]error{}
)

// MissingPromptError reports an embedded prompt that could not be loaded.
type MissingPromptError struct {
	Path string
	Err  error
}

func (e *MissingPromptError) Error() string {
	if e == nil {
		return "prompts: missing embedded prompt"
	}
	return fmt.Sprintf("prompts: missing embedded prompt %s: %v", e.Path, e.Err)
}

func (e *MissingPromptError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Load reads an embedded prompt file by agent and name.
func Load(agent, name string) (string, error) {
	path := promptPath(agent, name)
	data, err := fs.ReadFile(path)
	if err != nil {
		return "", &MissingPromptError{Path: path, Err: err}
	}
	return string(data), nil
}

// MustLoad reads an embedded prompt file by agent and name.
// Missing prompts are recorded for later validation so the caller can
// surface a normal startup error instead of crashing the UI during init.
func MustLoad(agent, name string) string {
	path := promptPath(agent, name)
	data, err := Load(agent, name)
	if err != nil {
		recordMissing(path, err)
		return "[missing embedded prompt: " + path + "]"
	}
	return data
}

// Validate reports any prompt loads that failed during package initialization.
func Validate() error {
	missingMu.Lock()
	defer missingMu.Unlock()
	if len(missingLoads) == 0 {
		return nil
	}

	keys := make([]string, 0, len(missingLoads))
	for path := range missingLoads {
		keys = append(keys, path)
	}
	sort.Strings(keys)

	errs := make([]error, 0, len(keys))
	for _, path := range keys {
		errs = append(errs, missingLoads[path])
	}
	return errors.Join(errs...)
}

func promptPath(agent, name string) string {
	return agent + "/" + name + ".md"
}

func recordMissing(path string, err error) {
	missingMu.Lock()
	defer missingMu.Unlock()
	missingLoads[path] = err
}
