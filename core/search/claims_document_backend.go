package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

type ClaimsDocumentBackendConfig struct {
	System    *SearchSystem
	Bleve     BleveManager
	IndexPath string
	Clock     func() time.Time
}

type ClaimsDocumentBackend struct {
	system    *SearchSystem
	bleve     BleveManager
	indexPath string
	clock     func() time.Time
}

type claimsDocumentArgs struct {
	DocumentID string         `json:"document_id,omitempty"`
	Path       string         `json:"path,omitempty"`
	Type       string         `json:"type,omitempty"`
	Content    string         `json:"content,omitempty"`
	Query      string         `json:"query,omitempty"`
	Limit      int            `json:"limit,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

func NewClaimsDocumentBackend(cfg ClaimsDocumentBackendConfig) *ClaimsDocumentBackend {
	return &ClaimsDocumentBackend{
		system:    cfg.System,
		bleve:     cfg.Bleve,
		indexPath: strings.TrimSpace(cfg.IndexPath),
		clock:     firstClaimsSearchClock(cfg.Clock),
	}
}

func (b *ClaimsDocumentBackend) HandleDocumentOperation(ctx context.Context, req claims.DocumentOperationRequest) (claims.DocumentOperationArtifactData, error) {
	data := req.Requested
	args, err := decodeClaimsDocumentArgs(req.Call.Arguments)
	if err != nil {
		data.FailureReason = err.Error()
		return data, nil
	}
	data.DocumentID = firstClaimsSearchString(data.DocumentID, args.DocumentID, args.Path)
	data.IndexPath = firstClaimsSearchString(data.IndexPath, b.indexPath, "bleve:"+data.DocumentID)
	switch data.Operation {
	case claims.DocumentToolLookup, claims.DocumentToolMetadataQuery, claims.DocumentToolSearchBleve, claims.DocumentToolResultShape:
		return b.query(ctx, data, args), nil
	case claims.DocumentToolDelete:
		return b.delete(ctx, data), nil
	default:
		return b.index(ctx, data, args), nil
	}
}

func (b *ClaimsDocumentBackend) index(ctx context.Context, data claims.DocumentOperationArtifactData, args claimsDocumentArgs) claims.DocumentOperationArtifactData {
	bleve := b.bleveManager()
	if bleve == nil {
		data.FailureReason = "bleve index manager not configured"
		return data
	}
	doc := claimsDocument(args, data, b.clock())
	if err := bleve.Index(ctx, doc); err != nil {
		data.FailureReason = err.Error()
		return data
	}
	data.DocumentID = doc.ID
	data.ContentHash = doc.Checksum
	data.Indexed = true
	return data
}

func (b *ClaimsDocumentBackend) delete(ctx context.Context, data claims.DocumentOperationArtifactData) claims.DocumentOperationArtifactData {
	bleve := b.bleveManager()
	if bleve == nil {
		data.FailureReason = "bleve index manager not configured"
		return data
	}
	if err := bleve.Delete(ctx, data.DocumentID); err != nil {
		data.FailureReason = err.Error()
		return data
	}
	data.Deleted = true
	data.Indexed = false
	return data
}

func (b *ClaimsDocumentBackend) query(ctx context.Context, data claims.DocumentOperationArtifactData, args claimsDocumentArgs) claims.DocumentOperationArtifactData {
	searcher := b.searcher()
	if searcher == nil {
		data.FailureReason = "bleve searcher not configured"
		return data
	}
	result, err := searcher.Search(ctx, &SearchRequest{Query: firstClaimsSearchString(args.Query, data.DocumentID), Limit: args.Limit, IncludeHighlights: true})
	if err != nil {
		data.FailureReason = err.Error()
		return data
	}
	data.ResultCount = len(result.Documents)
	data.Indexed = true
	data.Stale = b.system != nil && !b.system.IsRunning()
	return data
}

type claimsDocumentSearcher interface {
	Search(context.Context, *SearchRequest) (*SearchResult, error)
}

func (b *ClaimsDocumentBackend) searcher() claimsDocumentSearcher {
	if b.system != nil && b.system.IsRunning() {
		return b.system
	}
	return b.bleveManager()
}

func (b *ClaimsDocumentBackend) bleveManager() BleveManager {
	if b.bleve != nil {
		return b.bleve
	}
	if b.system == nil {
		return nil
	}
	return b.system.bleve
}

func claimsDocument(args claimsDocumentArgs, data claims.DocumentOperationArtifactData, now time.Time) *Document {
	content := args.Content
	checksum := firstClaimsSearchString(data.ContentHash, claimsSearchHash(content))
	id := firstClaimsSearchString(data.DocumentID, args.DocumentID, checksum)
	return &Document{
		ID:         id,
		Path:       firstClaimsSearchString(args.Path, id),
		Type:       claimsDocumentType(args),
		Content:    content,
		Checksum:   checksum,
		ModifiedAt: now,
		IndexedAt:  now,
	}
}

func claimsDocumentType(args claimsDocumentArgs) DocumentType {
	if parsed := DocumentType(strings.TrimSpace(args.Type)); parsed.IsValid() {
		return parsed
	}
	if strings.EqualFold(filepath.Ext(args.Path), ".md") {
		return DocTypeMarkdown
	}
	return DocTypeSourceCode
}

func decodeClaimsDocumentArgs(args map[string]any) (claimsDocumentArgs, error) {
	var out claimsDocumentArgs
	raw, err := json.Marshal(args)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode document claims args: %w", err)
	}
	return out, nil
}

func claimsSearchHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func firstClaimsSearchClock(clock func() time.Time) func() time.Time {
	if clock != nil {
		return clock
	}
	return time.Now
}

func firstClaimsSearchString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
