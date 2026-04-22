package versioning

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"time"
)

// ControlEntryKind distinguishes the different classes of session-level
// decision events that persist to the control WAL. The semantic WAL
// (versioned_wal.go) tracks file content; the control WAL tracks
// decisions *about* that content — commit-queue state transitions,
// Copy retention events, replica lifecycle, session epochs.
//
// Split-WAL rationale: semantic WAL entries are version-indexed
// (Major.Minor) because they correspond to observable file state.
// Control entries are decision events that don't change file content
// and don't need versioning; they need strict monotonic sequence and
// durable append semantics. Forcing them through the versioned WAL
// would create a spurious version bump per decision and scramble the
// lineage replay.
type ControlEntryKind uint8

const (
	// ControlKindCommitQueueEnqueue records a new MergeDescriptor
	// entering the commit queue in Auditing state.
	ControlKindCommitQueueEnqueue ControlEntryKind = 1
	// ControlKindCommitQueueMarkAccepted records the Auditing →
	// Accepted transition emitted by an audit replica.
	ControlKindCommitQueueMarkAccepted ControlEntryKind = 2
	// ControlKindCommitQueueMarkRejected records the Auditing →
	// Rejected transition with reasons + concerns.
	ControlKindCommitQueueMarkRejected ControlEntryKind = 3
	// ControlKindCommitQueueMarkSuperseded records the Rejected →
	// Superseded transition when a remediation claims the slot.
	ControlKindCommitQueueMarkSuperseded ControlEntryKind = 4
	// ControlKindCommitQueueMarkCommitted records the terminal
	// transition when the resolver flushes to disk.
	ControlKindCommitQueueMarkCommitted ControlEntryKind = 5
	// ControlKindCommitQueueAbandon records the terminal abandon
	// transition (DAG abort or architect-declared drop).
	ControlKindCommitQueueAbandon ControlEntryKind = 6

	// ControlKindRetentionRetain records a Copy refcount acquire
	// by a named holder (replica or pipeline).
	ControlKindRetentionRetain ControlEntryKind = 10
	// ControlKindRetentionRelease records a Copy refcount release.
	ControlKindRetentionRelease ControlEntryKind = 11
	// ControlKindRetentionAdvanceWaterLine records a new committed
	// water line.
	ControlKindRetentionAdvanceWaterLine ControlEntryKind = 12

	// ControlKindReplicaSpawned records that an audit replica was
	// launched against a specific merge seq.
	ControlKindReplicaSpawned ControlEntryKind = 20
	// ControlKindReplicaDecided records the replica's terminal
	// decision (accept/reject + payload).
	ControlKindReplicaDecided ControlEntryKind = 21
	// ControlKindReplicaCrashed records that a replica died without
	// emitting a decision. Resume re-dispatches a fresh replica with
	// the same deterministic identity.
	ControlKindReplicaCrashed ControlEntryKind = 22

	// ControlKindSessionOpenEpoch records a session Open boundary.
	// SLA timers sum active-epoch wall time across all open windows.
	ControlKindSessionOpenEpoch ControlEntryKind = 30
	// ControlKindSessionCloseEpoch records a session Close boundary.
	ControlKindSessionCloseEpoch ControlEntryKind = 31

	// ControlKindReplicaOverlayWrite records a write to a
	// ReplicaVFS's in-RAM overlay. Carries path + content + writer
	// attribution. See docs/PARALLEL_GLOBAL_VFS.md §3.6.
	ControlKindReplicaOverlayWrite ControlEntryKind = 40
	// ControlKindReplicaOverlayDelete records a delete against a
	// ReplicaVFS's in-RAM overlay.
	ControlKindReplicaOverlayDelete ControlEntryKind = 41
	// ControlKindReplicaOverlaySealed records the seal transition
	// (accept verdict applied; overlay frozen; diff submitted to
	// MergePipe as an audit-addendum merge).
	ControlKindReplicaOverlaySealed ControlEntryKind = 42
	// ControlKindReplicaOverlayAbandoned records the abandon
	// transition (reject verdict applied; chain bypass active).
	ControlKindReplicaOverlayAbandoned ControlEntryKind = 43
)

// ReplicaDecision is the terminal verdict a replica emits to the
// commit queue. Mirrors the queue's Accepted/Rejected states at the
// lifecycle-event layer.
type ReplicaDecision uint8

const (
	ReplicaDecisionAccepted ReplicaDecision = 1
	ReplicaDecisionRejected ReplicaDecision = 2
)

// ControlEntry is a single decision event persisted to the control
// WAL. All entries share the same envelope (seq, kind, timestamp,
// payload); kind-specific fields live in Payload.
type ControlEntry struct {
	Seq       uint64
	Kind      ControlEntryKind
	Timestamp time.Time
	Payload   ControlEntryPayload
}

// ControlEntryPayload is the per-kind body carried on a ControlEntry.
// Exactly one of the concrete payload fields is non-zero per kind.
// Kept as a value type (not interface) so binary round-trip is
// deterministic and payload identity is caller-controlled.
type ControlEntryPayload struct {
	// CommitQueueOp payloads — set on commit-queue kinds.
	MergedVersion   SemanticVersion
	PipelineID      string
	BasePath        []string // paths touched by the merge (stored as a packed string list)
	BaseVersion     SemanticVersion
	PathCount       uint32
	ReplicaID       string
	RejectionReason string
	Concerns        []string
	SupersedorVer   SemanticVersion

	// Retention payloads.
	RetentionVersion  SemanticVersion
	RetentionHolderID string
	WaterLine         SemanticVersion

	// Replica lifecycle payloads.
	ReplicaMergeVersion SemanticVersion
	ReplicaAgentType    string
	ReplicaDecision     ReplicaDecision
	ReplicaDecisionText string

	// Session epoch payloads.
	EpochMarker string // optional reason / cause

	// ReplicaVFS overlay payloads — set on overlay write / delete
	// / seal / abandon kinds. OverlayPath + OverlayContent carry
	// write content; OverlayWriter identifies the agent (inspector
	// or tester replica ID) for attribution.
	OverlayPath    string
	OverlayContent []byte
	OverlayWriter  string
}

// Binary layout for a control-WAL entry:
//
//	[1B kind][8B seq][8B timestamp_ns][4B payload_len][4B CRC32][payload bytes...]
//
// Payload is encoded per kind using length-prefixed strings and
// fixed-size integers. CRC is over the payload bytes only.
const controlEntryHeaderSize = 1 + 8 + 8 + 4 + 4 // 25 bytes

var (
	// ErrControlEntryCorrupted is returned when the payload decoder
	// cannot parse a field. The control WAL reader truncates the log
	// at the first corrupted entry — the previous fsync boundary is
	// the recovery point.
	ErrControlEntryCorrupted = errors.New("control entry: corrupted payload")
	// ErrControlEntryTruncated signals the entry is shorter than the
	// header + declared payload length. Indicates a partial write; the
	// reader truncates here.
	ErrControlEntryTruncated = errors.New("control entry: truncated")
	// ErrControlEntryCRCMismatch signals the CRC over the payload
	// disagrees with the stored CRC. The reader truncates here.
	ErrControlEntryCRCMismatch = errors.New("control entry: CRC mismatch")
	// ErrControlEntryUnknownKind signals an entry kind outside the
	// known enum. Future-proofing guard — a forward-compat reader
	// could skip these; current policy is to fail loudly.
	ErrControlEntryUnknownKind = errors.New("control entry: unknown kind")
)

// EncodeControlEntry serializes a ControlEntry to binary. The returned
// bytes are what gets appended to the control WAL file.
func EncodeControlEntry(e *ControlEntry) []byte {
	payload := encodeControlPayload(e.Kind, &e.Payload)
	crc := crc32.ChecksumIEEE(payload)

	buf := make([]byte, controlEntryHeaderSize+len(payload))
	buf[0] = byte(e.Kind)
	binary.BigEndian.PutUint64(buf[1:9], e.Seq)
	binary.BigEndian.PutUint64(buf[9:17], uint64(e.Timestamp.UnixNano()))
	binary.BigEndian.PutUint32(buf[17:21], uint32(len(payload)))
	binary.BigEndian.PutUint32(buf[21:25], crc)
	copy(buf[25:], payload)
	return buf
}

// DecodeControlEntry reads one entry from the head of data, returning
// the parsed entry and the number of bytes consumed. Partial or
// corrupted entries return errors; the caller truncates the log at
// that boundary.
func DecodeControlEntry(data []byte) (*ControlEntry, int, error) {
	if len(data) < controlEntryHeaderSize {
		return nil, 0, ErrControlEntryTruncated
	}
	kind := ControlEntryKind(data[0])
	seq := binary.BigEndian.Uint64(data[1:9])
	tsNano := binary.BigEndian.Uint64(data[9:17])
	payloadLen := binary.BigEndian.Uint32(data[17:21])
	storedCRC := binary.BigEndian.Uint32(data[21:25])

	if uint32(len(data)-controlEntryHeaderSize) < payloadLen {
		return nil, 0, ErrControlEntryTruncated
	}
	payload := data[controlEntryHeaderSize : controlEntryHeaderSize+int(payloadLen)]
	if crc32.ChecksumIEEE(payload) != storedCRC {
		return nil, 0, ErrControlEntryCRCMismatch
	}

	decoded, err := decodeControlPayload(kind, payload)
	if err != nil {
		return nil, 0, err
	}

	entry := &ControlEntry{
		Seq:       seq,
		Kind:      kind,
		Timestamp: time.Unix(0, int64(tsNano)),
		Payload:   decoded,
	}
	return entry, controlEntryHeaderSize + int(payloadLen), nil
}

// encodeControlPayload dispatches to the per-kind encoder.
func encodeControlPayload(kind ControlEntryKind, p *ControlEntryPayload) []byte {
	switch kind {
	case ControlKindCommitQueueEnqueue:
		return encodeEnqueue(p)
	case ControlKindCommitQueueMarkAccepted:
		return encodeMarkAccepted(p)
	case ControlKindCommitQueueMarkRejected:
		return encodeMarkRejected(p)
	case ControlKindCommitQueueMarkSuperseded:
		return encodeMarkSuperseded(p)
	case ControlKindCommitQueueMarkCommitted:
		return encodeCommitQueueVersion(p)
	case ControlKindCommitQueueAbandon:
		return encodeAbandon(p)
	case ControlKindRetentionRetain, ControlKindRetentionRelease:
		return encodeRetention(p)
	case ControlKindRetentionAdvanceWaterLine:
		return encodeWaterLine(p)
	case ControlKindReplicaSpawned:
		return encodeReplicaSpawned(p)
	case ControlKindReplicaDecided:
		return encodeReplicaDecided(p)
	case ControlKindReplicaCrashed:
		return encodeReplicaCrashed(p)
	case ControlKindSessionOpenEpoch, ControlKindSessionCloseEpoch:
		return encodeEpoch(p)
	case ControlKindReplicaOverlayWrite:
		return encodeOverlayWrite(p)
	case ControlKindReplicaOverlayDelete:
		return encodeOverlayDelete(p)
	case ControlKindReplicaOverlaySealed, ControlKindReplicaOverlayAbandoned:
		return encodeOverlayTerminal(p)
	default:
		return nil
	}
}

func decodeControlPayload(kind ControlEntryKind, data []byte) (ControlEntryPayload, error) {
	switch kind {
	case ControlKindCommitQueueEnqueue:
		return decodeEnqueue(data)
	case ControlKindCommitQueueMarkAccepted:
		return decodeMarkAccepted(data)
	case ControlKindCommitQueueMarkRejected:
		return decodeMarkRejected(data)
	case ControlKindCommitQueueMarkSuperseded:
		return decodeMarkSuperseded(data)
	case ControlKindCommitQueueMarkCommitted:
		return decodeCommitQueueVersion(data)
	case ControlKindCommitQueueAbandon:
		return decodeAbandon(data)
	case ControlKindRetentionRetain, ControlKindRetentionRelease:
		return decodeRetention(data)
	case ControlKindRetentionAdvanceWaterLine:
		return decodeWaterLine(data)
	case ControlKindReplicaSpawned:
		return decodeReplicaSpawned(data)
	case ControlKindReplicaDecided:
		return decodeReplicaDecided(data)
	case ControlKindReplicaCrashed:
		return decodeReplicaCrashed(data)
	case ControlKindSessionOpenEpoch, ControlKindSessionCloseEpoch:
		return decodeEpoch(data)
	case ControlKindReplicaOverlayWrite:
		return decodeOverlayWrite(data)
	case ControlKindReplicaOverlayDelete:
		return decodeOverlayDelete(data)
	case ControlKindReplicaOverlaySealed, ControlKindReplicaOverlayAbandoned:
		return decodeOverlayTerminal(data)
	default:
		return ControlEntryPayload{}, fmt.Errorf("%w: kind=%d", ErrControlEntryUnknownKind, kind)
	}
}

// ---------- per-kind encoders ----------

func encodeEnqueue(p *ControlEntryPayload) []byte {
	// [merged_ver][base_ver][pipeline_id][path_count][paths...]
	buf := encodeSemanticVersion(nil, p.MergedVersion)
	buf = encodeSemanticVersion(buf, p.BaseVersion)
	buf = encodeLenPrefixed(buf, []byte(p.PipelineID))
	buf = appendUint32(buf, p.PathCount)
	buf = appendUint32(buf, uint32(len(p.BasePath)))
	for _, path := range p.BasePath {
		buf = encodeLenPrefixed(buf, []byte(path))
	}
	return buf
}

func decodeEnqueue(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	off := 0
	var err error
	if p.MergedVersion, off, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	if p.BaseVersion, off, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	pipelineID, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.PipelineID = string(pipelineID)
	off += n
	if off+8 > len(data) {
		return p, ErrControlEntryCorrupted
	}
	p.PathCount = binary.BigEndian.Uint32(data[off : off+4])
	off += 4
	pathLen := binary.BigEndian.Uint32(data[off : off+4])
	off += 4
	paths := make([]string, 0, pathLen)
	for range pathLen {
		raw, n, err := decodeLenPrefixed(data, off)
		if err != nil {
			return p, ErrControlEntryCorrupted
		}
		paths = append(paths, string(raw))
		off += n
	}
	p.BasePath = paths
	return p, nil
}

func encodeMarkAccepted(p *ControlEntryPayload) []byte {
	buf := encodeSemanticVersion(nil, p.MergedVersion)
	buf = encodeLenPrefixed(buf, []byte(p.ReplicaID))
	return buf
}

func decodeMarkAccepted(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	var err error
	var off int
	if p.MergedVersion, off, err = decodeSemanticVersion(data, 0); err != nil {
		return p, ErrControlEntryCorrupted
	}
	replicaID, _, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.ReplicaID = string(replicaID)
	return p, nil
}

func encodeMarkRejected(p *ControlEntryPayload) []byte {
	buf := encodeSemanticVersion(nil, p.MergedVersion)
	buf = encodeLenPrefixed(buf, []byte(p.ReplicaID))
	buf = encodeLenPrefixed(buf, []byte(p.RejectionReason))
	buf = appendUint32(buf, uint32(len(p.Concerns)))
	for _, c := range p.Concerns {
		buf = encodeLenPrefixed(buf, []byte(c))
	}
	return buf
}

func decodeMarkRejected(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	off := 0
	var err error
	if p.MergedVersion, off, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	replicaID, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.ReplicaID = string(replicaID)
	off += n
	reason, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.RejectionReason = string(reason)
	off += n
	if off+4 > len(data) {
		return p, ErrControlEntryCorrupted
	}
	concernCount := binary.BigEndian.Uint32(data[off : off+4])
	off += 4
	concerns := make([]string, 0, concernCount)
	for range concernCount {
		raw, n, err := decodeLenPrefixed(data, off)
		if err != nil {
			return p, ErrControlEntryCorrupted
		}
		concerns = append(concerns, string(raw))
		off += n
	}
	p.Concerns = concerns
	return p, nil
}

func encodeMarkSuperseded(p *ControlEntryPayload) []byte {
	buf := encodeSemanticVersion(nil, p.MergedVersion)
	buf = encodeSemanticVersion(buf, p.SupersedorVer)
	return buf
}

func decodeMarkSuperseded(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	off := 0
	var err error
	if p.MergedVersion, off, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	if p.SupersedorVer, _, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	return p, nil
}

func encodeCommitQueueVersion(p *ControlEntryPayload) []byte {
	return encodeSemanticVersion(nil, p.MergedVersion)
}

func decodeCommitQueueVersion(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	ver, _, err := decodeSemanticVersion(data, 0)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.MergedVersion = ver
	return p, nil
}

func encodeAbandon(p *ControlEntryPayload) []byte {
	buf := encodeSemanticVersion(nil, p.MergedVersion)
	buf = encodeLenPrefixed(buf, []byte(p.RejectionReason))
	return buf
}

func decodeAbandon(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	off := 0
	var err error
	if p.MergedVersion, off, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	reason, _, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.RejectionReason = string(reason)
	return p, nil
}

func encodeRetention(p *ControlEntryPayload) []byte {
	buf := encodeSemanticVersion(nil, p.RetentionVersion)
	buf = encodeLenPrefixed(buf, []byte(p.RetentionHolderID))
	return buf
}

func decodeRetention(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	off := 0
	var err error
	if p.RetentionVersion, off, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	holder, _, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.RetentionHolderID = string(holder)
	return p, nil
}

func encodeWaterLine(p *ControlEntryPayload) []byte {
	return encodeSemanticVersion(nil, p.WaterLine)
}

func decodeWaterLine(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	ver, _, err := decodeSemanticVersion(data, 0)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.WaterLine = ver
	return p, nil
}

func encodeReplicaSpawned(p *ControlEntryPayload) []byte {
	buf := encodeSemanticVersion(nil, p.ReplicaMergeVersion)
	buf = encodeLenPrefixed(buf, []byte(p.ReplicaAgentType))
	buf = encodeLenPrefixed(buf, []byte(p.ReplicaID))
	return buf
}

func decodeReplicaSpawned(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	off := 0
	var err error
	if p.ReplicaMergeVersion, off, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	agentType, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.ReplicaAgentType = string(agentType)
	off += n
	replicaID, _, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.ReplicaID = string(replicaID)
	return p, nil
}

func encodeReplicaDecided(p *ControlEntryPayload) []byte {
	buf := encodeSemanticVersion(nil, p.ReplicaMergeVersion)
	buf = encodeLenPrefixed(buf, []byte(p.ReplicaID))
	buf = append(buf, byte(p.ReplicaDecision))
	buf = encodeLenPrefixed(buf, []byte(p.ReplicaDecisionText))
	buf = appendUint32(buf, uint32(len(p.Concerns)))
	for _, c := range p.Concerns {
		buf = encodeLenPrefixed(buf, []byte(c))
	}
	return buf
}

func decodeReplicaDecided(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	off := 0
	var err error
	if p.ReplicaMergeVersion, off, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	replicaID, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.ReplicaID = string(replicaID)
	off += n
	if off >= len(data) {
		return p, ErrControlEntryCorrupted
	}
	p.ReplicaDecision = ReplicaDecision(data[off])
	off++
	text, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.ReplicaDecisionText = string(text)
	off += n
	if off+4 > len(data) {
		return p, ErrControlEntryCorrupted
	}
	concernCount := binary.BigEndian.Uint32(data[off : off+4])
	off += 4
	concerns := make([]string, 0, concernCount)
	for range concernCount {
		raw, n, err := decodeLenPrefixed(data, off)
		if err != nil {
			return p, ErrControlEntryCorrupted
		}
		concerns = append(concerns, string(raw))
		off += n
	}
	p.Concerns = concerns
	return p, nil
}

func encodeReplicaCrashed(p *ControlEntryPayload) []byte {
	buf := encodeSemanticVersion(nil, p.ReplicaMergeVersion)
	buf = encodeLenPrefixed(buf, []byte(p.ReplicaID))
	buf = encodeLenPrefixed(buf, []byte(p.RejectionReason)) // reuse as crash reason
	return buf
}

func decodeReplicaCrashed(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	off := 0
	var err error
	if p.ReplicaMergeVersion, off, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	replicaID, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.ReplicaID = string(replicaID)
	off += n
	reason, _, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.RejectionReason = string(reason)
	return p, nil
}

func encodeEpoch(p *ControlEntryPayload) []byte {
	return encodeLenPrefixed(nil, []byte(p.EpochMarker))
}

func decodeEpoch(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	marker, _, err := decodeLenPrefixed(data, 0)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.EpochMarker = string(marker)
	return p, nil
}

// ---------- replica-overlay codecs ----------

func encodeOverlayWrite(p *ControlEntryPayload) []byte {
	buf := encodeSemanticVersion(nil, p.ReplicaMergeVersion)
	buf = encodeLenPrefixed(buf, []byte(p.ReplicaID))
	buf = encodeLenPrefixed(buf, []byte(p.OverlayPath))
	buf = encodeLenPrefixed(buf, p.OverlayContent)
	buf = encodeLenPrefixed(buf, []byte(p.OverlayWriter))
	return buf
}

func decodeOverlayWrite(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	off := 0
	var err error
	if p.ReplicaMergeVersion, off, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	replicaID, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.ReplicaID = string(replicaID)
	off += n
	path, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.OverlayPath = string(path)
	off += n
	content, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.OverlayContent = content
	off += n
	writer, _, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.OverlayWriter = string(writer)
	return p, nil
}

func encodeOverlayDelete(p *ControlEntryPayload) []byte {
	buf := encodeSemanticVersion(nil, p.ReplicaMergeVersion)
	buf = encodeLenPrefixed(buf, []byte(p.ReplicaID))
	buf = encodeLenPrefixed(buf, []byte(p.OverlayPath))
	buf = encodeLenPrefixed(buf, []byte(p.OverlayWriter))
	return buf
}

func decodeOverlayDelete(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	off := 0
	var err error
	if p.ReplicaMergeVersion, off, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	replicaID, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.ReplicaID = string(replicaID)
	off += n
	path, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.OverlayPath = string(path)
	off += n
	writer, _, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.OverlayWriter = string(writer)
	return p, nil
}

func encodeOverlayTerminal(p *ControlEntryPayload) []byte {
	buf := encodeSemanticVersion(nil, p.ReplicaMergeVersion)
	buf = encodeLenPrefixed(buf, []byte(p.ReplicaID))
	return buf
}

func decodeOverlayTerminal(data []byte) (ControlEntryPayload, error) {
	var p ControlEntryPayload
	off := 0
	var err error
	if p.ReplicaMergeVersion, off, err = decodeSemanticVersion(data, off); err != nil {
		return p, ErrControlEntryCorrupted
	}
	replicaID, _, err := decodeLenPrefixed(data, off)
	if err != nil {
		return p, ErrControlEntryCorrupted
	}
	p.ReplicaID = string(replicaID)
	return p, nil
}

// ---------- primitive codecs ----------

func encodeSemanticVersion(buf []byte, v SemanticVersion) []byte {
	buf = appendUint32(buf, v.Major)
	buf = appendUint32(buf, v.Minor)
	return buf
}

func decodeSemanticVersion(data []byte, off int) (SemanticVersion, int, error) {
	if off+8 > len(data) {
		return SemanticVersion{}, off, ErrControlEntryCorrupted
	}
	major := binary.BigEndian.Uint32(data[off : off+4])
	minor := binary.BigEndian.Uint32(data[off+4 : off+8])
	return SemanticVersion{Major: major, Minor: minor}, off + 8, nil
}
