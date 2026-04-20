package fetcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

func TestDiskFetcher_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.bin")
	want := []byte("disk fetcher fixture content")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	df := NewDiskFetcher()
	got, err := df.Fetch(context.Background(),
		recipe.FetchSpec{Source: "disk://" + path},
		FetchOptions{},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDiskFetcher_MissingFile(t *testing.T) {
	df := NewDiskFetcher()
	_, err := df.Fetch(context.Background(),
		recipe.FetchSpec{Source: "disk:///nonexistent/path/that/cannot/exist"},
		FetchOptions{},
	)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, ErrFetchFailed) {
		t.Errorf("expected ErrFetchFailed, got %v", err)
	}
}

func TestDiskFetcher_WrongScheme(t *testing.T) {
	df := NewDiskFetcher()
	_, err := df.Fetch(context.Background(),
		recipe.FetchSpec{Source: "https://example.com/x"},
		FetchOptions{},
	)
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource, got %v", err)
	}
}

func TestDiskFetcher_RespectsByteCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	content := make([]byte, 1024)
	for i := range content {
		content[i] = byte(i % 256)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	df := NewDiskFetcher()
	_, err := df.Fetch(context.Background(),
		recipe.FetchSpec{Source: "disk://" + path},
		FetchOptions{MaxBytes: 256},
	)
	if !errors.Is(err, ErrFetchTooLarge) {
		t.Fatalf("expected ErrFetchTooLarge, got %v", err)
	}
}

func TestDiskFetcher_EmptyFileIsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.bin")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	df := NewDiskFetcher()
	_, err := df.Fetch(context.Background(),
		recipe.FetchSpec{Source: "disk://" + path},
		FetchOptions{},
	)
	if !errors.Is(err, ErrEmptyResponse) {
		t.Fatalf("expected ErrEmptyResponse for empty file, got %v", err)
	}
}

func TestDiskFetcher_RespectsContextCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.bin")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	df := NewDiskFetcher()
	_, err := df.Fetch(ctx, recipe.FetchSpec{Source: "disk://" + path}, FetchOptions{})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}
