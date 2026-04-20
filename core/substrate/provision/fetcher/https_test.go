package fetcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

func TestHTTPSFetcher_RoundTrip(t *testing.T) {
	want := []byte("https fixture body")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(want)
	}))
	defer server.Close()

	cfg := DefaultHTTPSConfig()
	cfg.InsecureSkipVerify = true // local self-signed test server
	hf := NewHTTPSFetcher(cfg)

	got, err := hf.Fetch(context.Background(),
		recipe.FetchSpec{Source: server.URL + "/anything"},
		FetchOptions{},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHTTPSFetcher_Non200IsError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := DefaultHTTPSConfig()
	cfg.InsecureSkipVerify = true
	hf := NewHTTPSFetcher(cfg)

	_, err := hf.Fetch(context.Background(),
		recipe.FetchSpec{Source: server.URL + "/missing"},
		FetchOptions{},
	)
	if !errors.Is(err, ErrFetchFailed) {
		t.Fatalf("expected ErrFetchFailed for 404, got %v", err)
	}
}

func TestHTTPSFetcher_RespectsByteCap(t *testing.T) {
	body := make([]byte, 1024)
	for i := range body {
		body[i] = byte(i % 256)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	cfg := DefaultHTTPSConfig()
	cfg.InsecureSkipVerify = true
	hf := NewHTTPSFetcher(cfg)

	_, err := hf.Fetch(context.Background(),
		recipe.FetchSpec{Source: server.URL + "/x"},
		FetchOptions{MaxBytes: 256},
	)
	if !errors.Is(err, ErrFetchTooLarge) {
		t.Fatalf("expected ErrFetchTooLarge, got %v", err)
	}
}

func TestHTTPSFetcher_AppliesHeaders(t *testing.T) {
	var capturedAuth string
	var capturedUA string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	cfg := DefaultHTTPSConfig()
	cfg.InsecureSkipVerify = true
	hf := NewHTTPSFetcher(cfg)

	_, _ = hf.Fetch(context.Background(),
		recipe.FetchSpec{
			Source:  server.URL + "/x",
			Headers: map[string]string{"Authorization": "Bearer xyz"},
		},
		FetchOptions{},
	)
	if capturedAuth != "Bearer xyz" {
		t.Errorf("server saw Authorization=%q, want %q", capturedAuth, "Bearer xyz")
	}
	if capturedUA == "" {
		t.Error("expected default User-Agent to be set")
	}
}

func TestHTTPSFetcher_ExpandsEnvRefsInHeaders(t *testing.T) {
	t.Setenv("FETCHER_TEST_TOKEN", "the-secret-token")

	var captured string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("X-Token")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	cfg := DefaultHTTPSConfig()
	cfg.InsecureSkipVerify = true
	hf := NewHTTPSFetcher(cfg)

	_, _ = hf.Fetch(context.Background(),
		recipe.FetchSpec{
			Source:  server.URL + "/x",
			Headers: map[string]string{"X-Token": "${FETCHER_TEST_TOKEN}"},
		},
		FetchOptions{},
	)
	if captured != "the-secret-token" {
		t.Errorf("env-expanded header = %q, want %q", captured, "the-secret-token")
	}
}

func TestHTTPSFetcher_LeavesUnresolvedEnvRefsLiteral(t *testing.T) {
	// Verify that an unresolved env ref is passed through literally
	// rather than silently being dropped — recipe authors get to see
	// their typo.
	_ = os.Unsetenv("HOPEFULLY_UNSET_ENV_VAR_FOR_FETCHER_TEST")
	out := expandEnvRefs("${HOPEFULLY_UNSET_ENV_VAR_FOR_FETCHER_TEST}")
	if out != "${HOPEFULLY_UNSET_ENV_VAR_FOR_FETCHER_TEST}" {
		t.Errorf("unresolved env ref = %q, want literal pass-through", out)
	}
}
