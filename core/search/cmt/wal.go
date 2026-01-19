// Package cmt provides a Cartesian Merkle Tree implementation for the Sylk
// Document Search System. This file implements the Write-Ahead Log (WAL) for
// crash-safe logging of CMT operations.
package cmt

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"sync"
	"time"
)

// =============================================================================
// Constants
// =============================================================================

// DefaultWALMaxSize is the default maximum WAL size before suggesting checkpoint.
const DefaultWALMaxSize = 64 * 1024 * 1024 // 64 MB

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrWALPathEmpty indicates the WAL path was not provided.
	ErrWALPathEmpty = errors.New("wal path cannot be empty")

	// ErrWALFileInfoNil indicates FileInfo was nil for an insert operation.
	ErrWALFileInfoNil = errors.New("file info cannot be nil for insert")

	// ErrWALKeyEmpty indicates the key was empty.
	ErrWALKeyEmpty = errors.New("key cannot be empty")

	// ErrWALClosed indicates the WAL is closed.
	ErrWALClosed = errors.New("wal is closed")

	// ErrWALCorrupted indicates a WAL entry failed checksum validation.
	ErrWALCorrupted = errors.New("wal entry corrupted")
)

// =============================================================================
// WAL Operation Types
// =============================================================================

// WALOp represents a WAL operation type.
type WALOp byte

const (
	// WALOpInsert indicates an insert operation.
	WALOpInsert WALOp = 1

	// WALOpDelete indicates a delete operation.
	WALOpDelete WALOp = 2

	// WALOpCheckpoint indicates a checkpoint marker.
	WALOpCheckpoint WALOp = 3

	// WALOpTruncate indicates a truncate operation.
	WALOpTruncate WALOp = 4
)

// =============================================================================
// WAL Entry
// =============================================================================

// WALEntry represents a single entry in the write-ahead log.
type WALEntry struct {
	// Op is the operation type.
	Op WALOp

	// Timestamp is the Unix nanosecond timestamp when the entry was written.
	Timestamp int64

	// Key is the file path key.
	Key string

	// Info is the file information (only for INSERT operations).
	Info *FileInfo

	// Checksum is the CRC32 checksum of the entry for corruption detection.
	Checksum uint32
}

// =============================================================================
// WAL Config
// =============================================================================

// WALConfig configures the WAL.
type WALConfig struct {
	// Path is the file path for the WAL.
	Path string

	// SyncOnWrite enables fsync after each write (default: true when not set).
	SyncOnWrite bool

	// MaxSize is the maximum WAL size before suggesting checkpoint.
	MaxSize int64
}

// =============================================================================
// WAL
// =============================================================================

// WAL provides crash-safe logging of CMT operations.
type WAL struct {
	config WALConfig
	file   *os.File
	mu     sync.Mutex
	size   int64
	closed bool
}

// NewWAL creates a new WAL at the configured path.
// If the file exists, it opens it for appending; otherwise creates a new file.
func NewWAL(config WALConfig) (*WAL, error) {
	if config.Path == "" {
		return nil, ErrWALPathEmpty
	}

	if config.MaxSize <= 0 {
		config.MaxSize = DefaultWALMaxSize
	}

	file, err := os.OpenFile(config.Path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	return &WAL{
		config: config,
		file:   file,
		size:   stat.Size(),
	}, nil
}

// Close syncs and closes the WAL file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}

	w.closed = true

	if err := w.file.Sync(); err != nil {
		w.file.Close()
		return err
	}

	return w.file.Close()
}

// LogInsert records an insert operation.
func (w *WAL) LogInsert(key string, info *FileInfo) error {
	if key == "" {
		return ErrWALKeyEmpty
	}
	if info == nil {
		return ErrWALFileInfoNil
	}

	entry := WALEntry{
		Op:        WALOpInsert,
		Timestamp: time.Now().UnixNano(),
		Key:       key,
		Info:      info,
	}

	return w.writeEntry(&entry)
}

// LogDelete records a delete operation.
func (w *WAL) LogDelete(key string) error {
	if key == "" {
		return ErrWALKeyEmpty
	}

	entry := WALEntry{
		Op:        WALOpDelete,
		Timestamp: time.Now().UnixNano(),
		Key:       key,
	}

	return w.writeEntry(&entry)
}

// Checkpoint writes a checkpoint marker.
func (w *WAL) Checkpoint() error {
	entry := WALEntry{
		Op:        WALOpCheckpoint,
		Timestamp: time.Now().UnixNano(),
	}

	return w.writeEntry(&entry)
}

// Truncate clears the WAL after successful checkpoint to storage.
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrWALClosed
	}

	if err := w.file.Truncate(0); err != nil {
		return err
	}

	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	w.size = 0

	if w.config.SyncOnWrite {
		return w.file.Sync()
	}

	return nil
}

// Recover reads all entries from the WAL for replay.
// Returns entries in order, filtering out corrupted entries.
func (w *WAL) Recover() ([]WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	var entries []WALEntry

	for {
		entry, err := w.readEntry()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Skip corrupted or incomplete entries
			continue
		}
		entries = append(entries, *entry)
	}

	// Reset file position to end for further writes
	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return entries, err
	}

	return entries, nil
}

// Size returns the current WAL file size.
func (w *WAL) Size() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

// NeedsCheckpoint returns true if WAL exceeds MaxSize.
func (w *WAL) NeedsCheckpoint() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size > w.config.MaxSize
}

// =============================================================================
// Binary Encoding
// =============================================================================

// writeEntry writes a single entry to the WAL in binary format.
// Format: [1 byte Op][8 bytes Timestamp][4 bytes KeyLen][Key bytes][FileInfo if INSERT][4 bytes CRC32]
func (w *WAL) writeEntry(entry *WALEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrWALClosed
	}

	data := encodeEntry(entry)

	n, err := w.file.Write(data)
	if err != nil {
		return err
	}

	w.size += int64(n)

	if w.config.SyncOnWrite {
		return w.file.Sync()
	}

	return nil
}

// encodeEntry encodes a WALEntry to binary format.
func encodeEntry(entry *WALEntry) []byte {
	// Calculate size: op(1) + timestamp(8) + keyLen(4) + key + fileInfo(if insert) + crc(4)
	keyBytes := []byte(entry.Key)
	keyLen := len(keyBytes)

	size := 1 + 8 + 4 + keyLen + 4 // Base size + CRC

	if entry.Op == WALOpInsert && entry.Info != nil {
		size += encodedFileInfoSize(entry.Info)
	}

	buf := make([]byte, size)
	offset := 0

	// Op (1 byte)
	buf[offset] = byte(entry.Op)
	offset++

	// Timestamp (8 bytes, big-endian)
	binary.BigEndian.PutUint64(buf[offset:], uint64(entry.Timestamp))
	offset += 8

	// KeyLen (4 bytes, big-endian)
	binary.BigEndian.PutUint32(buf[offset:], uint32(keyLen))
	offset += 4

	// Key
	copy(buf[offset:], keyBytes)
	offset += keyLen

	// FileInfo (only for INSERT)
	if entry.Op == WALOpInsert && entry.Info != nil {
		offset = encodeFileInfo(buf, offset, entry.Info)
	}

	// CRC32 (computed over everything except the CRC itself)
	checksum := crc32.ChecksumIEEE(buf[:offset])
	binary.BigEndian.PutUint32(buf[offset:], checksum)

	return buf
}

// encodedFileInfoSize returns the size needed to encode a FileInfo.
func encodedFileInfoSize(info *FileInfo) int {
	pathBytes := []byte(info.Path)
	// pathLen(4) + path + contentHash(32) + size(8) + modTime(8) + permissions(4) + indexed(1)
	return 4 + len(pathBytes) + 32 + 8 + 8 + 4 + 1
}

// encodeFileInfo encodes FileInfo into the buffer at the given offset.
func encodeFileInfo(buf []byte, offset int, info *FileInfo) int {
	pathBytes := []byte(info.Path)

	// PathLen (4 bytes)
	binary.BigEndian.PutUint32(buf[offset:], uint32(len(pathBytes)))
	offset += 4

	// Path
	copy(buf[offset:], pathBytes)
	offset += len(pathBytes)

	// ContentHash (32 bytes)
	copy(buf[offset:], info.ContentHash[:])
	offset += 32

	// Size (8 bytes)
	binary.BigEndian.PutUint64(buf[offset:], uint64(info.Size))
	offset += 8

	// ModTime (8 bytes, Unix nano)
	binary.BigEndian.PutUint64(buf[offset:], uint64(info.ModTime.UnixNano()))
	offset += 8

	// Permissions (4 bytes)
	binary.BigEndian.PutUint32(buf[offset:], info.Permissions)
	offset += 4

	// Indexed (1 byte)
	if info.Indexed {
		buf[offset] = 1
	} else {
		buf[offset] = 0
	}
	offset++

	return offset
}

// =============================================================================
// Binary Decoding
// =============================================================================

// readEntry reads a single entry from the current file position.
func (w *WAL) readEntry() (*WALEntry, error) {
	// Read header: Op(1) + Timestamp(8) + KeyLen(4) = 13 bytes
	header := make([]byte, 13)
	if _, err := io.ReadFull(w.file, header); err != nil {
		return nil, err
	}

	entry := &WALEntry{}
	offset := 0

	// Op
	entry.Op = WALOp(header[offset])
	offset++

	// Timestamp
	entry.Timestamp = int64(binary.BigEndian.Uint64(header[offset:]))
	offset += 8

	// KeyLen
	keyLen := binary.BigEndian.Uint32(header[offset:])

	// Read key
	if keyLen > 0 {
		keyBytes := make([]byte, keyLen)
		if _, err := io.ReadFull(w.file, keyBytes); err != nil {
			return nil, err
		}
		entry.Key = string(keyBytes)
	}

	// Read FileInfo if INSERT
	if entry.Op == WALOpInsert {
		info, err := w.readFileInfo()
		if err != nil {
			return nil, err
		}
		entry.Info = info
	}

	// Read CRC32
	crcBuf := make([]byte, 4)
	if _, err := io.ReadFull(w.file, crcBuf); err != nil {
		return nil, err
	}
	entry.Checksum = binary.BigEndian.Uint32(crcBuf)

	// Verify checksum
	if !w.verifyEntryChecksum(entry, header, keyLen) {
		return nil, ErrWALCorrupted
	}

	return entry, nil
}

// readFileInfo reads a FileInfo from the current file position.
func (w *WAL) readFileInfo() (*FileInfo, error) {
	// Read PathLen
	pathLenBuf := make([]byte, 4)
	if _, err := io.ReadFull(w.file, pathLenBuf); err != nil {
		return nil, err
	}
	pathLen := binary.BigEndian.Uint32(pathLenBuf)

	// Read Path
	pathBytes := make([]byte, pathLen)
	if _, err := io.ReadFull(w.file, pathBytes); err != nil {
		return nil, err
	}

	info := &FileInfo{
		Path: string(pathBytes),
	}

	// Read ContentHash (32 bytes)
	if _, err := io.ReadFull(w.file, info.ContentHash[:]); err != nil {
		return nil, err
	}

	// Read remaining fixed fields: Size(8) + ModTime(8) + Permissions(4) + Indexed(1) = 21 bytes
	fixedBuf := make([]byte, 21)
	if _, err := io.ReadFull(w.file, fixedBuf); err != nil {
		return nil, err
	}

	offset := 0

	// Size
	info.Size = int64(binary.BigEndian.Uint64(fixedBuf[offset:]))
	offset += 8

	// ModTime
	modTimeNano := int64(binary.BigEndian.Uint64(fixedBuf[offset:]))
	info.ModTime = time.Unix(0, modTimeNano)
	offset += 8

	// Permissions
	info.Permissions = binary.BigEndian.Uint32(fixedBuf[offset:])
	offset += 4

	// Indexed
	info.Indexed = fixedBuf[offset] == 1

	return info, nil
}

// verifyEntryChecksum verifies the CRC32 checksum of an entry.
func (w *WAL) verifyEntryChecksum(entry *WALEntry, header []byte, keyLen uint32) bool {
	// Reconstruct the data that was checksummed
	data := encodeEntryForChecksum(entry, header, keyLen)

	computed := crc32.ChecksumIEEE(data)
	return computed == entry.Checksum
}

// encodeEntryForChecksum re-encodes entry data for checksum verification.
func encodeEntryForChecksum(entry *WALEntry, header []byte, keyLen uint32) []byte {
	size := 13 + int(keyLen)

	if entry.Op == WALOpInsert && entry.Info != nil {
		size += encodedFileInfoSize(entry.Info)
	}

	buf := make([]byte, size)

	// Copy header
	copy(buf, header)
	offset := 13

	// Copy key
	if keyLen > 0 {
		copy(buf[offset:], []byte(entry.Key))
		offset += int(keyLen)
	}

	// Encode FileInfo if present
	if entry.Op == WALOpInsert && entry.Info != nil {
		encodeFileInfo(buf, offset, entry.Info)
	}

	return buf
}
