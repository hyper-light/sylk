package graph

import "github.com/adalundhe/sylk/core/vectorgraphdb"

type edgeKey struct {
	src uint32
	dst uint32
	typ uint8
}

func makeEdgeKey(sourceID, targetID uint32, edgeType vectorgraphdb.EdgeType) edgeKey {
	return edgeKey{
		src: sourceID,
		dst: targetID,
		typ: uint8(edgeType),
	}
}

func (k edgeKey) SourceID() uint32 {
	return k.src
}

func (k edgeKey) TargetID() uint32 {
	return k.dst
}

func (k edgeKey) EdgeType() vectorgraphdb.EdgeType {
	return vectorgraphdb.EdgeType(k.typ)
}
