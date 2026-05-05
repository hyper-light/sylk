package runtime

import (
	"strings"

	"github.com/adalundhe/sylk/core/mcp"
	"github.com/adalundhe/sylk/core/mcp/durable"
)

// summaryByteCap caps result summaries written into the projection. The
// projection is meant to be inspectable; large summaries belong in the
// content store via the ingester. The cap is derived from the JSONL line
// budget that keeps the projection.snapshot.json easily searchable in a
// terminal: 4 KB ≈ 50 lines at typical width.
const summaryByteCap = 4 * 1024

func summarizeContent(blocks []mcp.ContentBlock) string {
	var sb strings.Builder
	for _, block := range blocks {
		appendBlockSummary(&sb, block)
		if sb.Len() >= summaryByteCap {
			break
		}
	}
	return clampSummary(sb.String())
}

func appendBlockSummary(sb *strings.Builder, block mcp.ContentBlock) {
	switch block.Type {
	case "text":
		sb.WriteString(block.Text)
	case "resource":
		if block.Resource != nil {
			sb.WriteString(block.Resource.Text)
		}
	case "image", "audio":
		sb.WriteString("<")
		sb.WriteString(block.Type)
		sb.WriteString(":")
		sb.WriteString(block.MIMEType)
		sb.WriteString(">")
	}
	sb.WriteString("\n")
}

func summarizeResourceContents(contents []mcp.ResourceContent) string {
	var sb strings.Builder
	for _, content := range contents {
		if content.Text != "" {
			sb.WriteString(content.Text)
		} else if content.Blob != "" {
			sb.WriteString("<blob:")
			sb.WriteString(content.MIMEType)
			sb.WriteString(">")
		}
		sb.WriteString("\n")
		if sb.Len() >= summaryByteCap {
			break
		}
	}
	return clampSummary(sb.String())
}

func clampSummary(s string) string {
	if len(s) <= summaryByteCap {
		return s
	}
	return s[:summaryByteCap] + "...(truncated)"
}

// contentToIngest extracts ingest payloads from a tool result. Text blocks
// become indexable knowledge; embedded resources become indirect references.
func contentToIngest(op durable.Operation, blocks []mcp.ContentBlock) []IngestPayload {
	var payloads []IngestPayload
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		payloads = append(payloads, IngestPayload{
			Source:         "mcp.tool/" + op.LocalName,
			ServerID:       op.ServerID,
			OperationID:    op.OperationID,
			SessionID:      op.SessionID,
			CapabilityKind: string(mcp.CapabilityTool),
			MIMEType:       "text/plain",
			Title:          op.LocalName,
			Text:           block.Text,
		})
	}
	return payloads
}

func resourceToIngest(op durable.Operation, contents []mcp.ResourceContent) []IngestPayload {
	var payloads []IngestPayload
	for _, content := range contents {
		if content.Text == "" {
			continue
		}
		payloads = append(payloads, IngestPayload{
			Source:         content.URI,
			ServerID:       op.ServerID,
			OperationID:    op.OperationID,
			SessionID:      op.SessionID,
			CapabilityKind: string(mcp.CapabilityResource),
			MIMEType:       content.MIMEType,
			Title:          content.URI,
			Text:           content.Text,
		})
	}
	return payloads
}

func promptToIngest(op durable.Operation, result *mcp.GetPromptResult) []IngestPayload {
	if result == nil {
		return nil
	}
	var sb strings.Builder
	for _, msg := range result.Messages {
		for _, block := range msg.Content {
			if block.Type == "text" {
				sb.WriteString(block.Text)
				sb.WriteString("\n")
			}
		}
	}
	body := sb.String()
	if body == "" {
		return nil
	}
	return []IngestPayload{{
		Source:         "mcp.prompt/" + op.LocalName,
		ServerID:       op.ServerID,
		OperationID:    op.OperationID,
		SessionID:      op.SessionID,
		CapabilityKind: string(mcp.CapabilityPrompt),
		MIMEType:       "text/markdown",
		Title:          op.LocalName,
		Text:           body,
	}}
}
