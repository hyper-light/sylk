package sandbox

import "fmt"

// composeEnvironment merges the user-supplied env with the determinism
// profile's clamps. Determinism clamps win for the standard env vars
// (SOURCE_DATE_EPOCH, LC_ALL, HOSTNAME, USER, HOME); user env wins for
// anything else. EnvAdd overrides take precedence over defaults; EnvScrub
// removes entries entirely from the final result.
func composeEnvironment(userEnv map[string]string, profile DeterminismProfile) map[string]string {
	out := copyEnv(userEnv)
	applyDeterminismDefaults(out, profile)
	applyEnvAdd(out, profile.EnvAdd)
	applyEnvScrub(out, profile.EnvScrub)
	return out
}

func copyEnv(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func applyDeterminismDefaults(env map[string]string, profile DeterminismProfile) {
	env["SOURCE_DATE_EPOCH"] = fmt.Sprintf("%d", resolveSourceDateEpoch(profile.SourceDateEpoch))
	env["LC_ALL"] = resolveLocale(profile.Locale)
	env["LANG"] = env["LC_ALL"]
	env["HOSTNAME"] = resolveHostname(profile.Hostname)
	env["USER"] = resolveUser(profile.User)
	env["LOGNAME"] = env["USER"]
	env["HOME"] = resolveHome(profile.Home)
}

func resolveSourceDateEpoch(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func resolveLocale(s string) string {
	if s == "" {
		return "C"
	}
	return s
}

func resolveHostname(s string) string {
	if s == "" {
		return "substrate"
	}
	return s
}

func resolveUser(s string) string {
	if s == "" {
		return "builder"
	}
	return s
}

func resolveHome(s string) string {
	if s == "" {
		return "/sylk/home"
	}
	return s
}

func applyEnvAdd(env map[string]string, add map[string]string) {
	for k, v := range add {
		env[k] = v
	}
}

func applyEnvScrub(env map[string]string, scrub []string) {
	for _, k := range scrub {
		delete(env, k)
	}
}

// envSlice converts a map[string]string into the "K=V" slice form
// os/exec expects.
func envSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
