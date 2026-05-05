package compiler

import (
	"strings"

	"github.com/adalundhe/sylk/core/mcp"
)

// CompileResource turns a wire-side resource descriptor into a CompiledResource
// with binding metadata. Resources are NOT registered as Skills; instead the
// ingest layer indexes them into the knowledge substrate.
func CompileResource(serverID string, desc mcp.ResourceDescriptor) *CompiledResource {
	canonicalURI := CanonicalResourceURI(serverID, desc.URI)
	return &CompiledResource{
		Binding: MCPBindingSpec{
			ServerID:       serverID,
			CapabilityKind: string(mcp.CapabilityResource),
			RemoteURI:      desc.URI,
			Fingerprint:    canonicalURI,
		},
		Description: strings.TrimSpace(desc.Description),
		MIMEType:    desc.MIMEType,
		Title:       desc.Name,
		URI:         canonicalURI,
	}
}
