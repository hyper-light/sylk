package agentlog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// PromptCorpus is a content-addressed blob store for repetitive
// payloads that would otherwise bloat LLM stream records — system
// prompts and tool-definition bundles. Each unique payload is written
// once per session to
//
//	{baseDir}/{sha256}.txt
//
// and referenced from LLM records by its SHA256 hex. The store is
// safe for concurrent use; duplicate puts short-circuit on an
// in-memory seen-set to avoid stat/write overhead on the hot path.
type PromptCorpus struct {
	baseDir  string
	redactor Redactor

	mu   sync.Mutex
	seen map[string]struct{}
}

// NewPromptCorpus creates a corpus anchored at baseDir. The directory
// is created lazily on first Put. If redactor is nil, content is
// written unchanged — callers with sensitive inputs should pass the
// same Redactor used by their StreamWriter so on-disk content is
// consistent across streams and corpus.
func NewPromptCorpus(baseDir string, redactor Redactor) *PromptCorpus {
	if redactor == nil {
		redactor = noopRedactor{}
	}
	return &PromptCorpus{
		baseDir:  baseDir,
		redactor: redactor,
		seen:     make(map[string]struct{}),
	}
}

// Put writes content to the corpus (or skips if already written in
// this process) and returns the SHA256 hex that llm records should
// reference. An empty content value returns the empty string — the
// caller is responsible for deciding whether to emit a sha reference
// in that case.
func (c *PromptCorpus) Put(content string) (string, error) {
	if c == nil || content == "" {
		return "", nil
	}
	redacted := c.redactor.RedactString(content)
	sum := sha256.Sum256([]byte(redacted))
	hash := hex.EncodeToString(sum[:])

	c.mu.Lock()
	if _, ok := c.seen[hash]; ok {
		c.mu.Unlock()
		return hash, nil
	}
	// Mark seen before the write: subsequent calls with the same
	// content skip IO. If the write fails, the in-memory seen set
	// is still populated — the next call will see no file on disk
	// but will also skip rewriting. This is acceptable because a
	// write failure is surfaced to the caller and already logged;
	// the LLM record can still reference the hash and the `sylk
	// trace` tooling degrades gracefully on missing corpus files.
	c.seen[hash] = struct{}{}
	c.mu.Unlock()

	if err := os.MkdirAll(c.baseDir, 0o755); err != nil {
		return hash, fmt.Errorf("agentlog: create corpus dir: %w", err)
	}
	path := filepath.Join(c.baseDir, hash+".txt")

	// Write atomically via temp + rename to avoid partial-file
	// readers in concurrent scenarios. If the final path already
	// exists on disk (different process wrote it earlier), the
	// rename overwrites which is fine — same content by hash.
	tmp, err := os.CreateTemp(c.baseDir, "corpus-*.tmp")
	if err != nil {
		return hash, fmt.Errorf("agentlog: corpus temp: %w", err)
	}
	if _, err := tmp.WriteString(redacted); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return hash, fmt.Errorf("agentlog: corpus write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return hash, fmt.Errorf("agentlog: corpus close: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return hash, fmt.Errorf("agentlog: corpus rename: %w", err)
	}
	return hash, nil
}

// Get retrieves the content for a previously-written hash. Returns
// ("", false) when the hash is not in the corpus on disk. This is
// used by sylk trace to resolve references during replay.
func (c *PromptCorpus) Get(hash string) (string, bool) {
	if c == nil || hash == "" {
		return "", false
	}
	path := filepath.Join(c.baseDir, hash+".txt")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}
