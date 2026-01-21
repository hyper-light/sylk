package ingestion

import (
	"context"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

type Ingester interface {
	Ingest(ctx context.Context, items []IngestItem) error
	IngestOne(ctx context.Context, item IngestItem) error
	Delete(ctx context.Context, ids []string) error
}

type IngestItem struct {
	ID       string
	Content  string
	Domain   vectorgraphdb.Domain
	NodeType uint16
	Metadata map[string]any
}

type NodeType = uint16

const (
	NodeTypeUnknown NodeType = iota

	NodeTypeFunction
	NodeTypeMethod
	NodeTypeStruct
	NodeTypeInterface
	NodeTypeVariable
	NodeTypeConstant
	NodeTypeFile
	NodeTypePackage

	NodeTypeEvent
	NodeTypeConversation
	NodeTypeDecision
	NodeTypeSummary

	NodeTypePaper
	NodeTypeAbstract
	NodeTypeCitation
	NodeTypeFinding
)

func NodeTypeString(nt NodeType) string {
	switch nt {
	case NodeTypeFunction:
		return "function"
	case NodeTypeMethod:
		return "method"
	case NodeTypeStruct:
		return "struct"
	case NodeTypeInterface:
		return "interface"
	case NodeTypeVariable:
		return "variable"
	case NodeTypeConstant:
		return "constant"
	case NodeTypeFile:
		return "file"
	case NodeTypePackage:
		return "package"
	case NodeTypeEvent:
		return "event"
	case NodeTypeConversation:
		return "conversation"
	case NodeTypeDecision:
		return "decision"
	case NodeTypeSummary:
		return "summary"
	case NodeTypePaper:
		return "paper"
	case NodeTypeAbstract:
		return "abstract"
	case NodeTypeCitation:
		return "citation"
	case NodeTypeFinding:
		return "finding"
	default:
		return "unknown"
	}
}
