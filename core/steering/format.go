package steering

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/providers"
)

// FormatSteeringMessages compacts multiple steering messages into a single
// providers.Message with role=user. Multiple messages between checkpoints
// are combined to prevent req.Messages explosion.
func FormatSteeringMessages(texts []string) providers.Message {
	var b strings.Builder
	b.WriteString("[LIVE STEERING] The user has provided additional direction while you work:\n\n")
	for _, text := range texts {
		fmt.Fprintf(&b, "- %s\n", text)
	}
	b.WriteString("\nIncorporate this into your current work. Do not restart unless ")
	b.WriteString("the input fundamentally changes direction. Continue your current phase.")
	return providers.Message{Role: providers.RoleUser, Content: b.String()}
}
