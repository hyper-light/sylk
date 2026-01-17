package versioning

import (
	"crypto/sha256"
	"encoding/binary"
	"time"
)

type OperationID [HashSize]byte

type OpType int

const (
	OpInsert OpType = iota
	OpDelete
	OpReplace
	OpMove
)

var opTypeNames = map[OpType]string{
	OpInsert:  "insert",
	OpDelete:  "delete",
	OpReplace: "replace",
	OpMove:    "move",
}

func (o OpType) String() string {
	if name, ok := opTypeNames[o]; ok {
		return name
	}
	return "unknown"
}

type Operation struct {
	ID          OperationID
	BaseVersion VersionID
	FilePath    string
	Target      Target
	Type        OpType
	Content     []byte
	OldContent  []byte
	PipelineID  string
	SessionID   SessionID
	AgentID     string
	AgentRole   string
	Clock       VectorClock
	Timestamp   time.Time
}

func NewOperation(
	baseVersion VersionID,
	filePath string,
	target Target,
	opType OpType,
	content []byte,
	oldContent []byte,
	pipelineID string,
	sessionID SessionID,
	agentID string,
	clock VectorClock,
) Operation {
	op := Operation{
		BaseVersion: baseVersion,
		FilePath:    filePath,
		Target:      target,
		Type:        opType,
		Content:     cloneBytes(content),
		OldContent:  cloneBytes(oldContent),
		PipelineID:  pipelineID,
		SessionID:   sessionID,
		AgentID:     agentID,
		Clock:       clock.Clone(),
		Timestamp:   time.Now(),
	}
	op.ID = op.computeID()
	return op
}

func (o *Operation) computeID() OperationID {
	h := sha256.New()
	h.Write(o.BaseVersion[:])
	h.Write([]byte(o.FilePath))
	writeInt(h, int(o.Type))
	h.Write(o.Content)
	h.Write([]byte(o.PipelineID))
	h.Write([]byte(o.SessionID))
	writeTime(h, o.Timestamp)

	var id OperationID
	copy(id[:], h.Sum(nil))
	return id
}

func writeInt(h interface{ Write([]byte) (int, error) }, v int) {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(v))
	h.Write(buf)
}

func writeTime(h interface{ Write([]byte) (int, error) }, t time.Time) {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(t.UnixNano()))
	h.Write(buf)
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	result := make([]byte, len(b))
	copy(result, b)
	return result
}

func (o *Operation) Invert() Operation {
	inverted := *o
	inverted.Content, inverted.OldContent = o.OldContent, o.Content
	inverted.Type = invertOpType(o.Type)
	inverted.Timestamp = time.Now()
	inverted.ID = inverted.computeID()
	return inverted
}

func invertOpType(t OpType) OpType {
	switch t {
	case OpInsert:
		return OpDelete
	case OpDelete:
		return OpInsert
	default:
		return t
	}
}

func (o OperationID) String() string {
	return encodeHex(o[:])
}

func (o OperationID) Short() string {
	return encodeHex(o[:4])
}

func (o OperationID) IsZero() bool {
	for _, b := range o {
		if b != 0 {
			return false
		}
	}
	return true
}

func encodeHex(b []byte) string {
	const hextable = "0123456789abcdef"
	dst := make([]byte, len(b)*2)
	for i, v := range b {
		dst[i*2] = hextable[v>>4]
		dst[i*2+1] = hextable[v&0x0f]
	}
	return string(dst)
}

func (o *Operation) Clone() Operation {
	return Operation{
		ID:          o.ID,
		BaseVersion: o.BaseVersion,
		FilePath:    o.FilePath,
		Target:      o.Target.Clone(),
		Type:        o.Type,
		Content:     cloneBytes(o.Content),
		OldContent:  cloneBytes(o.OldContent),
		PipelineID:  o.PipelineID,
		SessionID:   o.SessionID,
		AgentID:     o.AgentID,
		AgentRole:   o.AgentRole,
		Clock:       o.Clock.Clone(),
		Timestamp:   o.Timestamp,
	}
}

func (o *Operation) Size() int {
	return len(o.Content) + len(o.OldContent)
}

func (o *Operation) IsEmpty() bool {
	return len(o.Content) == 0 && o.Type != OpDelete
}
