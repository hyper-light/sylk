package treesitter

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

type Query struct {
	inner   *sitter.Query
	pattern string
}

type QueryCursor struct {
	inner *sitter.QueryCursor
}

type QueryMatch struct {
	PatternIndex uint
	Captures     []QueryCapture
}

type QueryCapture struct {
	Name string
	Node *Node
}

func NewQuery(lang *sitter.Language, pattern string) (*Query, error) {
	q, err := sitter.NewQuery(lang, pattern)
	if err != nil {
		return nil, err
	}
	return &Query{inner: q, pattern: pattern}, nil
}

func (q *Query) Close() {
	q.inner.Close()
}

func (q *Query) PatternCount() uint {
	return q.inner.PatternCount()
}

func (q *Query) CaptureNames() []string {
	return q.inner.CaptureNames()
}

func NewQueryCursor() *QueryCursor {
	return &QueryCursor{inner: sitter.NewQueryCursor()}
}

func (c *QueryCursor) Close() {
	c.inner.Close()
}

func (c *QueryCursor) Matches(query *Query, node *Node, source []byte) []QueryMatch {
	iter := c.inner.Matches(query.inner, node.inner, source)

	var matches []QueryMatch
	for {
		match := iter.Next()
		if match == nil {
			break
		}

		names := query.CaptureNames()
		qm := QueryMatch{
			PatternIndex: uint(match.PatternIndex),
			Captures:     make([]QueryCapture, 0, len(match.Captures)),
		}

		for _, cap := range match.Captures {
			name := ""
			if int(cap.Index) < len(names) {
				name = names[cap.Index]
			}
			qm.Captures = append(qm.Captures, QueryCapture{
				Name: name,
				Node: &Node{inner: &cap.Node, source: source},
			})
		}

		matches = append(matches, qm)
	}

	return matches
}

func (c *QueryCursor) Captures(query *Query, node *Node, source []byte) []QueryCapture {
	iter := c.inner.Captures(query.inner, node.inner, source)

	names := query.CaptureNames()
	var captures []QueryCapture
	for {
		match, captureIdx := iter.Next()
		if match == nil {
			break
		}

		name := ""
		if int(captureIdx) < len(names) {
			name = names[captureIdx]
		}
		if int(captureIdx) < len(match.Captures) {
			cap := match.Captures[captureIdx]
			captures = append(captures, QueryCapture{
				Name: name,
				Node: &Node{inner: &cap.Node, source: source},
			})
		}
	}

	return captures
}
