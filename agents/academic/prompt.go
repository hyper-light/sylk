package academic

import (
	"strings"

	"github.com/adalundhe/sylk/prompts"
)

const DefaultMaxOutputTokens = 16384

var (
	academicBaseSystem       = prompts.MustLoad("academic", "system")
	academicFabricAwareness  = prompts.MustLoad("shared", "fabric_awareness")
	academicClaimsNative     = prompts.MustLoad("shared", "claims_native")
	academicFabricAdvisory   = prompts.MustLoad("shared", "fabric_knowledge_advisory")

	// DefaultSystemPrompt composes the academic's base persona with the
	// uniform fabric awareness section + knowledge-agent advisory clause.
	DefaultSystemPrompt = strings.Join([]string{
		academicBaseSystem,
		academicFabricAwareness,
		academicClaimsNative,
		academicFabricAdvisory,
	}, "\n\n---\n\n")

	AcademicConversationPrompt = prompts.MustLoad("academic", "conversation")
)
