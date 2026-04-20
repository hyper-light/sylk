package fetcher

import (
	"context"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

type stubFetcher struct {
	scheme string
	bytes  []byte
	err    error
	calls  int
}

func (s *stubFetcher) Scheme() string { return s.scheme }
func (s *stubFetcher) Fetch(_ context.Context, _ recipe.FetchSpec, _ FetchOptions) ([]byte, error) {
	s.calls++
	return s.bytes, s.err
}

func TestRegistry_DispatchesByScheme(t *testing.T) {
	r := NewRegistry()
	stub := &stubFetcher{scheme: "stub", bytes: []byte("hello")}
	r.Install(stub)

	got, err := r.Fetch(context.Background(), recipe.FetchSpec{Source: "stub://anywhere"}, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
	if stub.calls != 1 {
		t.Errorf("stub.calls = %d, want 1", stub.calls)
	}
}

func TestRegistry_UnknownSchemeIsError(t *testing.T) {
	r := NewRegistry()
	_, err := r.Fetch(context.Background(), recipe.FetchSpec{Source: "missing://nowhere"}, FetchOptions{})
	if !errors.Is(err, ErrUnknownScheme) {
		t.Fatalf("expected ErrUnknownScheme, got %v", err)
	}
}

func TestRegistry_InvalidSourceIsError(t *testing.T) {
	r := NewRegistry()
	_, err := r.Fetch(context.Background(), recipe.FetchSpec{Source: "no-scheme-here"}, FetchOptions{})
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource, got %v", err)
	}
}

func TestRegistry_ReplacementOverridesPrevious(t *testing.T) {
	r := NewRegistry()
	first := &stubFetcher{scheme: "x", bytes: []byte("first")}
	second := &stubFetcher{scheme: "x", bytes: []byte("second")}
	r.Install(first)
	r.Install(second)

	got, err := r.Fetch(context.Background(), recipe.FetchSpec{Source: "x://path"}, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("expected the replacement fetcher to be used; got %q", got)
	}
	if first.calls != 0 {
		t.Errorf("first fetcher should not have been called after replacement, got %d calls", first.calls)
	}
}

func TestRegistry_AppliesPlatformSubstitution(t *testing.T) {
	captured := ""
	stub := &capturingFetcher{scheme: "z", capture: &captured}
	r := NewRegistry()
	r.Install(stub)

	_, _ = r.Fetch(context.Background(),
		recipe.FetchSpec{Source: "z://example.com/{os}/{arch}/file.tar.gz"},
		FetchOptions{Platform: recipe.Platform{OS: "linux", Arch: "amd64"}},
	)
	want := "z://example.com/linux/amd64/file.tar.gz"
	if captured != want {
		t.Errorf("source after substitution = %q, want %q", captured, want)
	}
}

type capturingFetcher struct {
	scheme  string
	capture *string
}

func (c *capturingFetcher) Scheme() string { return c.scheme }
func (c *capturingFetcher) Fetch(_ context.Context, spec recipe.FetchSpec, _ FetchOptions) ([]byte, error) {
	*c.capture = spec.Source
	return []byte{0}, nil
}

func TestPickFetchSpec_PrefersMatchingPin(t *testing.T) {
	specs := []recipe.FetchSpec{
		{Source: "https://generic.example.com/x.tar.gz"},
		{Source: "https://linux.example.com/x-linux.tar.gz", PlatformPin: &recipe.Platform{OS: "linux"}},
		{Source: "https://darwin.example.com/x-darwin.tar.gz", PlatformPin: &recipe.Platform{OS: "darwin"}},
	}
	picked, err := PickFetchSpec(specs, recipe.Platform{OS: "linux"})
	if err != nil {
		t.Fatalf("PickFetchSpec: %v", err)
	}
	if picked.Source != "https://linux.example.com/x-linux.tar.gz" {
		t.Errorf("picked %q, want the linux-pinned source", picked.Source)
	}
}

func TestPickFetchSpec_FallsBackToUnpinned(t *testing.T) {
	specs := []recipe.FetchSpec{
		{Source: "https://generic.example.com/x.tar.gz"},
		{Source: "https://linux.example.com/x-linux.tar.gz", PlatformPin: &recipe.Platform{OS: "linux"}},
	}
	picked, err := PickFetchSpec(specs, recipe.Platform{OS: "freebsd"})
	if err != nil {
		t.Fatalf("PickFetchSpec: %v", err)
	}
	if picked.Source != "https://generic.example.com/x.tar.gz" {
		t.Errorf("expected fallback to unpinned, got %q", picked.Source)
	}
}

func TestPickFetchSpec_NoMatchReturnsError(t *testing.T) {
	specs := []recipe.FetchSpec{
		{Source: "https://linux.example.com/x.tar.gz", PlatformPin: &recipe.Platform{OS: "linux"}},
	}
	_, err := PickFetchSpec(specs, recipe.Platform{OS: "darwin"})
	if !errors.Is(err, ErrNoMatchingFetchSpec) {
		t.Fatalf("expected ErrNoMatchingFetchSpec, got %v", err)
	}
}

func TestSubstitutePlatform(t *testing.T) {
	out := SubstitutePlatform(
		"https://example.com/{os}/{arch}/{libc}/file.tar.gz",
		recipe.Platform{OS: "linux", Arch: "amd64", Libc: "glibc"},
	)
	want := "https://example.com/linux/amd64/glibc/file.tar.gz"
	if out != want {
		t.Errorf("SubstitutePlatform = %q, want %q", out, want)
	}
}

func TestSubstitutePlatform_LeavesEmptyFieldsLiteral(t *testing.T) {
	// When Libc is unset, {libc} is replaced with the empty string.
	out := SubstitutePlatform(
		"https://example.com/{os}/{libc}/file.tar.gz",
		recipe.Platform{OS: "linux"},
	)
	want := "https://example.com/linux//file.tar.gz"
	if out != want {
		t.Errorf("SubstitutePlatform with empty Libc = %q, want %q", out, want)
	}
}
