package boot

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// dotenvFile is the name of the local environment file loaded at startup.
const dotenvFile = ".env.local"

// LoadDotenv reads KEY=VALUE pairs from .env.local in the given directory
// and sets them as environment variables. Existing variables are NOT
// overridden — explicit env vars always take priority.
//
// Lines starting with # and blank lines are skipped. Values may be
// optionally quoted with single or double quotes.
func LoadDotenv(dir string) error {
	path := filepath.Join(dir, dotenvFile)
	f, err := os.Open(path)
	if err != nil {
		return err // caller can ignore os.IsNotExist
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}

		key, value, ok := parseEnvLine(line)
		if !ok {
			continue
		}

		// Never override explicitly set variables.
		if os.Getenv(key) != "" {
			continue
		}
		os.Setenv(key, value)
	}
	return scanner.Err()
}

// parseEnvLine splits "KEY=VALUE" and strips optional quotes from VALUE.
func parseEnvLine(line string) (key, value string, ok bool) {
	idx := strings.IndexByte(line, '=')
	if idx < 1 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	value = unquote(value)
	return key, value, true
}

// unquote strips matching single or double quotes from s.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
