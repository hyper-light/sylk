package ingestion

import (
	"errors"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

var (
	ErrInvalidDomain   = errors.New("invalid domain")
	ErrInvalidNodeType = errors.New("invalid node type for domain")
)

func ValidateLabel(domain vectorgraphdb.Domain, nodeType NodeType) error {
	if !domain.IsValid() {
		return ErrInvalidDomain
	}

	validTypes := validNodeTypesForDomain(domain)
	for _, vt := range validTypes {
		if vt == nodeType {
			return nil
		}
	}

	return ErrInvalidNodeType
}

func validNodeTypesForDomain(domain vectorgraphdb.Domain) []NodeType {
	switch domain {
	case vectorgraphdb.DomainCode:
		return []NodeType{
			NodeTypeFunction,
			NodeTypeMethod,
			NodeTypeStruct,
			NodeTypeInterface,
			NodeTypeVariable,
			NodeTypeConstant,
			NodeTypeFile,
			NodeTypePackage,
		}
	case vectorgraphdb.DomainHistory:
		return []NodeType{
			NodeTypeEvent,
			NodeTypeConversation,
			NodeTypeDecision,
			NodeTypeSummary,
		}
	case vectorgraphdb.DomainAcademic:
		return []NodeType{
			NodeTypePaper,
			NodeTypeAbstract,
			NodeTypeCitation,
			NodeTypeFinding,
		}
	default:
		return []NodeType{NodeTypeUnknown}
	}
}

func PackLabel(domain vectorgraphdb.Domain, nodeType NodeType) uint32 {
	return uint32(domain)<<16 | uint32(nodeType)
}

func UnpackLabel(packed uint32) (vectorgraphdb.Domain, NodeType) {
	return vectorgraphdb.Domain(packed >> 16), NodeType(packed & 0xFFFF)
}
