package vamana

import (
	"errors"
	"fmt"
)

// Sentinel errors for errors.Is() usage.
var (
	ErrNodeNotFound         = errors.New("node not found")
	ErrEmptyVector          = errors.New("vector is nil or zero-length")
	ErrDimensionMismatch    = errors.New("vector dimension mismatch")
	ErrIndexEmpty           = errors.New("index is empty")
	ErrSnapshotCorrupt      = errors.New("snapshot failed integrity check")
	ErrWALCorrupt           = errors.New("WAL failed checksum validation")
	ErrCompactionInProgress = errors.New("operation blocked by compaction")
	ErrDeltaFull            = errors.New("delta layer at capacity")
)

// NodeNotFoundError wraps ErrNodeNotFound with the missing node ID.
type NodeNotFoundError struct {
	NodeID uint64
}

func (e *NodeNotFoundError) Error() string {
	return fmt.Sprintf("node not found: %d", e.NodeID)
}

func (e *NodeNotFoundError) Is(target error) bool {
	return target == ErrNodeNotFound
}

func (e *NodeNotFoundError) Unwrap() error {
	return ErrNodeNotFound
}

// DimensionMismatchError wraps ErrDimensionMismatch with expected and actual dimensions.
type DimensionMismatchError struct {
	Expected int
	Actual   int
}

func (e *DimensionMismatchError) Error() string {
	return fmt.Sprintf("vector dimension mismatch: expected %d, got %d", e.Expected, e.Actual)
}

func (e *DimensionMismatchError) Is(target error) bool {
	return target == ErrDimensionMismatch
}

func (e *DimensionMismatchError) Unwrap() error {
	return ErrDimensionMismatch
}

// SnapshotCorruptError wraps ErrSnapshotCorrupt with details about the corruption.
type SnapshotCorruptError struct {
	Reason string
}

func (e *SnapshotCorruptError) Error() string {
	return fmt.Sprintf("snapshot failed integrity check: %s", e.Reason)
}

func (e *SnapshotCorruptError) Is(target error) bool {
	return target == ErrSnapshotCorrupt
}

func (e *SnapshotCorruptError) Unwrap() error {
	return ErrSnapshotCorrupt
}

// WALCorruptError wraps ErrWALCorrupt with details about the corruption.
type WALCorruptError struct {
	Offset   int64
	Expected uint32
	Actual   uint32
}

func (e *WALCorruptError) Error() string {
	return fmt.Sprintf("WAL failed checksum validation at offset %d: expected %08x, got %08x", e.Offset, e.Expected, e.Actual)
}

func (e *WALCorruptError) Is(target error) bool {
	return target == ErrWALCorrupt
}

func (e *WALCorruptError) Unwrap() error {
	return ErrWALCorrupt
}

// DeltaFullError wraps ErrDeltaFull with capacity information.
type DeltaFullError struct {
	Capacity int
	Size     int
}

func (e *DeltaFullError) Error() string {
	return fmt.Sprintf("delta layer at capacity: %d/%d", e.Size, e.Capacity)
}

func (e *DeltaFullError) Is(target error) bool {
	return target == ErrDeltaFull
}

func (e *DeltaFullError) Unwrap() error {
	return ErrDeltaFull
}
