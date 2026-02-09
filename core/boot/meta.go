package boot

import (
	"encoding/json"
	"os"
	"time"
)

const SchemaVersion = 1

type Meta struct {
	SchemaVersion     int               `json:"schema_version"`
	CreatedAt         time.Time         `json:"created_at"`
	LastIndexedAt     time.Time         `json:"last_indexed_at"`
	LastIndexedCommit string            `json:"last_indexed_commit,omitempty"`
	IsGitRepo         bool              `json:"is_git_repo"`
	NodeCount         int64             `json:"node_count"`
	EdgeCount         int64             `json:"edge_count"`
	VectorCount       int64             `json:"vector_count"`
	EmbedderSource    string            `json:"embedder_source"`
	FileHashes        map[string]string `json:"file_hashes"`
}

func NewMeta(isGitRepo bool, embedderSource string) *Meta {
	now := time.Now().UTC()
	return &Meta{
		SchemaVersion:  SchemaVersion,
		CreatedAt:      now,
		LastIndexedAt:  now,
		IsGitRepo:      isGitRepo,
		EmbedderSource: embedderSource,
		FileHashes:     make(map[string]string),
	}
}

func LoadMeta(projectRoot string) (*Meta, error) {
	data, err := os.ReadFile(MetaPath(projectRoot))
	if err != nil {
		return nil, err
	}

	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}

	return &meta, nil
}

func (m *Meta) Save(projectRoot string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(MetaPath(projectRoot), data, 0644)
}

func (m *Meta) UpdateStats(nodes, edges, vectors int64) {
	m.NodeCount = nodes
	m.EdgeCount = edges
	m.VectorCount = vectors
	m.LastIndexedAt = time.Now().UTC()
}

func (m *Meta) SetCommit(commit string) {
	m.LastIndexedCommit = commit
}

func (m *Meta) SetFileHash(path, hash string) {
	m.FileHashes[path] = hash
}

func (m *Meta) GetFileHash(path string) (string, bool) {
	hash, ok := m.FileHashes[path]
	return hash, ok
}

func (m *Meta) ClearFileHashes() {
	m.FileHashes = make(map[string]string)
}
