package credentials

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/storage"
	"gopkg.in/yaml.v3"
)

// authPrefsFile is the filename for per-provider auth mode preferences.
const authPrefsFile = "auth_prefs.yaml"

// authPrefsData is the serialised map of provider → auth mode.
type authPrefsData struct {
	Providers map[string]string `yaml:"providers"`
}

var (
	prefsOnce sync.Once
	prefsPath string
)

func resolvePrefsPath() string {
	prefsOnce.Do(func() {
		dirs, err := storage.ResolveDirs()
		if err != nil || dirs == nil {
			return
		}
		prefsPath = filepath.Join(dirs.Config, authPrefsFile)
	})
	return prefsPath
}

// LoadAuthPref returns the stored auth mode for a provider (e.g. "oauth",
// "api_key"). Returns "" if no preference is stored.
func LoadAuthPref(provider string) string {
	path := resolvePrefsPath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var prefs authPrefsData
	if yaml.Unmarshal(data, &prefs) != nil || prefs.Providers == nil {
		return ""
	}
	return prefs.Providers[strings.ToLower(strings.TrimSpace(provider))]
}

// SaveAuthPref persists the auth mode preference for a provider.
// Merges into the existing file so other providers are preserved.
func SaveAuthPref(provider, authMode string) error {
	path := resolvePrefsPath()
	if path == "" {
		return nil
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	authMode = strings.TrimSpace(authMode)

	var prefs authPrefsData
	if existing, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(existing, &prefs)
	}
	if prefs.Providers == nil {
		prefs.Providers = make(map[string]string)
	}
	prefs.Providers[provider] = authMode

	out, err := yaml.Marshal(&prefs)
	if err != nil {
		return err
	}
	if err := storage.EnsureSensitiveDir(filepath.Dir(path)); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
