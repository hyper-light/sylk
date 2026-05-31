package librarian

import (
	"strings"

	"github.com/adalundhe/sylk/prompts"
)

var (
	librarianBaseSystem       = prompts.MustLoad("librarian", "system")
	HealthAssessmentPrompt    = prompts.MustLoad("librarian", "health")
	PatternDetectionPrompt    = prompts.MustLoad("librarian", "patterns")
	QueryClassificationPrompt = prompts.MustLoad("librarian", "query_classification")
	librarianFabricAwareness  = prompts.MustLoad("shared", "fabric_awareness")
	librarianClaimsNative     = prompts.MustLoad("shared", "claims_native")
	librarianFabricAdvisory   = prompts.MustLoad("shared", "fabric_knowledge_advisory")

	// DefaultSystemPrompt composes the librarian's base persona with the
	// uniform Activity Fabric awareness section and the knowledge-agent
	// advisory clause that documents auto-emit + proactive subscription.
	DefaultSystemPrompt = strings.Join([]string{
		librarianBaseSystem,
		librarianFabricAwareness,
		librarianClaimsNative,
		librarianFabricAdvisory,
	}, "\n\n---\n\n")
)
