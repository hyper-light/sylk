package sandbox

import "testing"

func TestComposeEnvironment_AppliesDeterminismDefaults(t *testing.T) {
	out := composeEnvironment(nil, DeterminismProfile{})
	want := map[string]string{
		"SOURCE_DATE_EPOCH": "0",
		"LC_ALL":            "C",
		"LANG":              "C",
		"HOSTNAME":          "substrate",
		"USER":              "builder",
		"LOGNAME":           "builder",
		"HOME":              "/sylk/home",
	}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("env[%q] = %q, want %q", k, out[k], v)
		}
	}
}

func TestComposeEnvironment_RespectsExplicitProfileOverrides(t *testing.T) {
	profile := DeterminismProfile{
		SourceDateEpoch: 1700000000,
		Locale:          "en_US.UTF-8",
		Hostname:        "ci-host",
		User:            "ci-user",
		Home:            "/srv/ci/home",
	}
	out := composeEnvironment(nil, profile)
	if out["SOURCE_DATE_EPOCH"] != "1700000000" {
		t.Errorf("SOURCE_DATE_EPOCH = %q", out["SOURCE_DATE_EPOCH"])
	}
	if out["LC_ALL"] != "en_US.UTF-8" {
		t.Errorf("LC_ALL = %q", out["LC_ALL"])
	}
	if out["HOSTNAME"] != "ci-host" {
		t.Errorf("HOSTNAME = %q", out["HOSTNAME"])
	}
}

func TestComposeEnvironment_UserEnvPreservedForNonClampedKeys(t *testing.T) {
	in := map[string]string{
		"PATH":         "/usr/bin",
		"PYTHONPATH":   "/site-packages",
		"CUSTOM_VAR":   "custom-value",
	}
	out := composeEnvironment(in, DeterminismProfile{})
	if out["PATH"] != "/usr/bin" {
		t.Errorf("PATH = %q, want preserved", out["PATH"])
	}
	if out["CUSTOM_VAR"] != "custom-value" {
		t.Errorf("CUSTOM_VAR = %q, want preserved", out["CUSTOM_VAR"])
	}
}

func TestComposeEnvironment_EnvAddOverridesDefaults(t *testing.T) {
	profile := DeterminismProfile{
		EnvAdd: map[string]string{
			"HOSTNAME": "override-host",
			"NEWVAR":   "added-value",
		},
	}
	out := composeEnvironment(nil, profile)
	if out["HOSTNAME"] != "override-host" {
		t.Errorf("HOSTNAME = %q, EnvAdd should override default", out["HOSTNAME"])
	}
	if out["NEWVAR"] != "added-value" {
		t.Errorf("NEWVAR = %q, want %q", out["NEWVAR"], "added-value")
	}
}

func TestComposeEnvironment_EnvScrubRemovesEntries(t *testing.T) {
	in := map[string]string{
		"PYTHONSTARTUP": "/host/.pythonrc",
		"PATH":          "/usr/bin",
	}
	profile := DeterminismProfile{
		EnvScrub: []string{"PYTHONSTARTUP"},
	}
	out := composeEnvironment(in, profile)
	if _, exists := out["PYTHONSTARTUP"]; exists {
		t.Error("PYTHONSTARTUP should be scrubbed from env")
	}
	if out["PATH"] != "/usr/bin" {
		t.Error("non-scrubbed entries should remain")
	}
}

func TestEnvSlice_FormatsAsKVPairs(t *testing.T) {
	in := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}
	out := envSlice(in)
	if len(out) != 2 {
		t.Fatalf("envSlice returned %d entries, want 2", len(out))
	}
	for _, entry := range out {
		// Just confirm K=V shape.
		if !contains(entry, '=') {
			t.Errorf("entry %q missing = separator", entry)
		}
	}
}

func contains(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}
