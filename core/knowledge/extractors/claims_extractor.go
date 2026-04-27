package extractors

import (
	"strings"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/knowledge"
)

// ClaimsExtractor produces indexable Entity records from claims board
// entities. Testaments with non-trivial summaries and claims with
// descriptions become searchable documents in the knowledge store.
type ClaimsExtractor struct{}

// ExtractFromClaim produces an Entity for a claim with a meaningful
// description. Returns nil for claims without sufficient content.
func (e *ClaimsExtractor) ExtractFromClaim(c *claims.Claim) *Entity {
	if c == nil || len(strings.TrimSpace(c.Description)) < 50 {
		return nil
	}
	return &Entity{
		ID:       generateEntityID("claims", "claim:"+c.ID),
		Name:     c.Title,
		Kind:     knowledge.EntityKindClaim,
		FilePath: "claims://" + c.ID,
	}
}

// ExtractFromTestament produces an Entity for a testament with a
// meaningful summary. Returns nil for trivial testaments.
func (e *ClaimsExtractor) ExtractFromTestament(t *claims.Testament, claimID string) *Entity {
	if t == nil || len(strings.TrimSpace(t.Summary)) < 50 {
		return nil
	}
	return &Entity{
		ID:       generateEntityID("claims", "testament:"+t.ID),
		Name:     truncateExtractor(t.Summary, 100),
		Kind:     knowledge.EntityKindTestament,
		FilePath: "claims://" + claimID + "/testament/" + t.ID,
	}
}

func truncateExtractor(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "\u2026"
}
