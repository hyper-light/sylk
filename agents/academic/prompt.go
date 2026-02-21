package academic

import "github.com/adalundhe/sylk/prompts"

const DefaultMaxOutputTokens = 16384

var DefaultSystemPrompt = prompts.MustLoad("academic", "system")
