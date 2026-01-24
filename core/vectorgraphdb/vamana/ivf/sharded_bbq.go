// Package ivf provides IVF (Inverted File) index with BBQ quantization and Vamana graph.
package ivf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc64"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
	"gopkg.in/yaml.v3"
)

// BBQ storage constants.
const (
	// BBQPerShard matches VectorsPerShard for alignment.
	// 65536 codes per shard keeps files manageable.
	BBQPerShard = 65536

	bbqShardShift = 16            // log2(65536)
	bbqShardMask  = (1 << 16) - 1 // 0xFFFF

	// bbqHeaderSize is the fixed header size for BBQ shard files.
	// Layout: [Magic:4][CodeLen:4][Count:4][Reserved:4] = 16 bytes
	bbqHeaderSize = 16

	// bbqMagic identifies BBQ shard files.
	bbqMagic = 0x31514242 // "BBQ1" in little-endian
)

// crc64Table is the ECMA polynomial table for checksums.
var crc64Table = crc64.MakeTable(crc64.ECMA)

// BBQ storage errors.
var (
	ErrBBQCodeLenMismatch  = errors.New("bbq code length mismatch")
	ErrBBQCapacityExceeded = errors.New("bbq shard capacity exceeded")
	ErrBBQInvalidMagic     = errors.New("bbq invalid magic bytes")
	ErrBBQChecksumMismatch = errors.New("bbq checksum mismatch: shard corrupted")
	ErrBBQStoreClosed      = errors.New("bbq store is closed")
)

// shardedBBQMeta is the YAML metadata for ShardedBBQStore.
type shardedBBQMeta struct {
	ShardCapacity int `yaml:"shard_capacity"`
	CodeLen       int `yaml:"code_len"`
	Total         int `yaml:"total"`
}

// bbqShard wraps a single BBQ shard with its lock and checksum.
type bbqShard struct {
	region   *storage.MmapRegion
	codeLen  int
	capacity int
	count    uint32 // current count in this shard
	checksum uint64 // CRC64 of data section (computed on seal)
	sealed   bool   // true if shard is full and sealed
	mu       sync.RWMutex
}

// ShardedBBQStore provides mmap-backed storage for BBQ codes with sharding.
//
// Each shard holds up to 65536 BBQ codes. Shards are sealed when full,
// and new shards are created as needed.
//
// File format per shard:
//
//	[Header: 16B][Code0: codeLen][Code1: codeLen]...[Checksum: 8B]
//
// Thread safety: concurrent reads are safe; writes use per-shard locking.
type ShardedBBQStore struct {
	dir           string
	codeLen       int
	shardCapacity int

	shards  atomic.Value // []*bbqShard
	shardMu sync.Mutex   // for shard creation only

	total atomic.Uint64
}

// CreateShardedBBQStore creates a new sharded BBQ store.
func CreateShardedBBQStore(dir string, codeLen int) (*ShardedBBQStore, error) {
	if codeLen <= 0 {
		return nil, fmt.Errorf("bbq: invalid code length: %d", codeLen)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("bbq: create dir: %w", err)
	}

	s := &ShardedBBQStore{
		dir:           dir,
		codeLen:       codeLen,
		shardCapacity: BBQPerShard,
	}
	s.shards.Store(make([]*bbqShard, 0, 8))

	if err := s.createShard(); err != nil {
		return nil, err
	}

	if err := s.saveMeta(); err != nil {
		return nil, err
	}

	return s, nil
}

// OpenShardedBBQStore opens an existing sharded BBQ store.
// Returns the store and a list of corrupted shard indices (if any).
func OpenShardedBBQStore(dir string) (*ShardedBBQStore, []int, error) {
	metaData, err := os.ReadFile(filepath.Join(dir, "meta.yaml"))
	if err != nil {
		return nil, nil, fmt.Errorf("bbq: read meta: %w", err)
	}

	var meta shardedBBQMeta
	if err := yaml.Unmarshal(metaData, &meta); err != nil {
		return nil, nil, fmt.Errorf("bbq: parse meta: %w", err)
	}

	numShards := (meta.Total + meta.ShardCapacity - 1) / meta.ShardCapacity
	if numShards == 0 {
		numShards = 1
	}

	shards := make([]*bbqShard, numShards)
	var corrupted []int

	for i := range numShards {
		path := filepath.Join(dir, fmt.Sprintf("shard_%04d.bin", i))
		shard, err := openBBQShard(path, meta.CodeLen)
		if err != nil {
			// Close already opened shards
			for j := range i {
				if shards[j] != nil {
					shards[j].Close()
				}
			}
			return nil, nil, fmt.Errorf("bbq: open shard %d: %w", i, err)
		}

		// Verify checksum for sealed shards
		if shard.sealed && !shard.VerifyChecksum() {
			corrupted = append(corrupted, i)
		}

		shards[i] = shard
	}

	s := &ShardedBBQStore{
		dir:           dir,
		codeLen:       meta.CodeLen,
		shardCapacity: meta.ShardCapacity,
	}
	s.shards.Store(shards)
	s.total.Store(uint64(meta.Total))

	return s, corrupted, nil
}

// openBBQShard opens a single BBQ shard file.
func openBBQShard(path string, expectedCodeLen int) (*bbqShard, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}

	size := info.Size()
	if size < bbqHeaderSize {
		return nil, fmt.Errorf("file too small: %d bytes", size)
	}

	region, err := storage.MapFile(path, size, false)
	if err != nil {
		return nil, fmt.Errorf("mmap: %w", err)
	}

	data := region.Data()

	// Parse header
	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != bbqMagic {
		region.Close()
		return nil, ErrBBQInvalidMagic
	}

	codeLen := int(binary.LittleEndian.Uint32(data[4:8]))
	if codeLen != expectedCodeLen {
		region.Close()
		return nil, fmt.Errorf("%w: expected %d, got %d", ErrBBQCodeLenMismatch, expectedCodeLen, codeLen)
	}

	count := binary.LittleEndian.Uint32(data[8:12])
	// data[12:16] is reserved

	// Calculate if shard is sealed (full)
	capacity := BBQPerShard
	sealed := int(count) >= capacity

	// Read stored checksum if sealed
	var checksum uint64
	if sealed {
		checksumOffset := bbqHeaderSize + int64(capacity)*int64(codeLen)
		if size >= checksumOffset+8 {
			checksum = binary.LittleEndian.Uint64(data[checksumOffset : checksumOffset+8])
		}
	}

	return &bbqShard{
		region:   region,
		codeLen:  codeLen,
		capacity: capacity,
		count:    count,
		checksum: checksum,
		sealed:   sealed,
	}, nil
}

// createBBQShard creates a new BBQ shard file with preallocated space.
func createBBQShard(path string, codeLen, capacity int) (*bbqShard, error) {
	// Calculate total size: header + codes + checksum
	totalSize := int64(bbqHeaderSize) + int64(capacity)*int64(codeLen) + 8

	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}

	if err := f.Truncate(totalSize); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("truncate: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("close: %w", err)
	}

	region, err := storage.MapFile(path, totalSize, false)
	if err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("mmap: %w", err)
	}

	// Write header
	data := region.Data()
	binary.LittleEndian.PutUint32(data[0:4], bbqMagic)
	binary.LittleEndian.PutUint32(data[4:8], uint32(codeLen))
	binary.LittleEndian.PutUint32(data[8:12], 0)  // count = 0
	binary.LittleEndian.PutUint32(data[12:16], 0) // reserved

	return &bbqShard{
		region:   region,
		codeLen:  codeLen,
		capacity: capacity,
		count:    0,
		sealed:   false,
	}, nil
}

// VerifyChecksum verifies the CRC64 checksum of a sealed shard.
func (s *bbqShard) VerifyChecksum() bool {
	if !s.sealed || s.region == nil {
		return true // unsealed shards don't have checksums yet
	}

	data := s.region.Data()
	dataStart := bbqHeaderSize
	dataEnd := bbqHeaderSize + int(s.count)*s.codeLen

	computed := crc64.Checksum(data[dataStart:dataEnd], crc64Table)
	return computed == s.checksum
}

// computeAndWriteChecksum computes CRC64 and writes it to the shard.
func (s *bbqShard) computeAndWriteChecksum() {
	data := s.region.Data()
	dataStart := bbqHeaderSize
	dataEnd := bbqHeaderSize + int(s.count)*s.codeLen

	s.checksum = crc64.Checksum(data[dataStart:dataEnd], crc64Table)

	// Write checksum at end of data
	checksumOffset := bbqHeaderSize + s.capacity*s.codeLen
	binary.LittleEndian.PutUint64(data[checksumOffset:checksumOffset+8], s.checksum)
}

// Get returns the BBQ code at the given local index (zero-copy).
func (s *bbqShard) Get(localID uint32) []byte {
	if s.region == nil || localID >= s.count {
		return nil
	}

	data := s.region.Data()
	offset := bbqHeaderSize + int(localID)*s.codeLen
	return data[offset : offset+s.codeLen]
}

// Append adds a BBQ code to the shard.
func (s *bbqShard) Append(code []byte) error {
	if len(code) != s.codeLen {
		return ErrBBQCodeLenMismatch
	}

	if int(s.count) >= s.capacity {
		return ErrBBQCapacityExceeded
	}

	data := s.region.Data()
	offset := bbqHeaderSize + int(s.count)*s.codeLen
	copy(data[offset:offset+s.codeLen], code)

	s.count++

	// Update count in header
	binary.LittleEndian.PutUint32(data[8:12], s.count)

	return nil
}

// Seal marks the shard as full and computes the checksum.
func (s *bbqShard) Seal() {
	if s.sealed {
		return
	}
	s.computeAndWriteChecksum()
	s.sealed = true
}

// Close releases the mmap region.
func (s *bbqShard) Close() error {
	if s.region == nil {
		return nil
	}
	err := s.region.Close()
	s.region = nil
	return err
}

// loadShards returns the current shard slice.
func (s *ShardedBBQStore) loadShards() []*bbqShard {
	return s.shards.Load().([]*bbqShard)
}

// createShard creates a new BBQ shard.
func (s *ShardedBBQStore) createShard() error {
	shards := s.loadShards()
	shardIdx := len(shards)
	path := filepath.Join(s.dir, fmt.Sprintf("shard_%04d.bin", shardIdx))

	shard, err := createBBQShard(path, s.codeLen, s.shardCapacity)
	if err != nil {
		return fmt.Errorf("bbq: create shard %d: %w", shardIdx, err)
	}

	newShards := make([]*bbqShard, len(shards)+1)
	copy(newShards, shards)
	newShards[shardIdx] = shard
	s.shards.Store(newShards)
	return nil
}

// saveMeta writes the metadata file.
func (s *ShardedBBQStore) saveMeta() error {
	meta := shardedBBQMeta{
		ShardCapacity: s.shardCapacity,
		CodeLen:       s.codeLen,
		Total:         int(s.total.Load()),
	}
	data, err := yaml.Marshal(&meta)
	if err != nil {
		return fmt.Errorf("bbq: marshal meta: %w", err)
	}
	return os.WriteFile(filepath.Join(s.dir, "meta.yaml"), data, 0644)
}

// getShard returns the shard and local ID for a global ID.
func (s *ShardedBBQStore) getShard(id uint32) (*bbqShard, uint32) {
	shardIdx := int(id >> bbqShardShift)
	localID := id & bbqShardMask

	shards := s.loadShards()
	if shardIdx >= len(shards) {
		return nil, 0
	}

	return shards[shardIdx], localID
}

// Get returns the BBQ code at the given global ID (zero-copy).
func (s *ShardedBBQStore) Get(id uint32) []byte {
	shard, localID := s.getShard(id)
	if shard == nil {
		return nil
	}

	shard.mu.RLock()
	code := shard.Get(localID)
	shard.mu.RUnlock()
	return code
}

// Append adds a BBQ code and returns its global ID.
func (s *ShardedBBQStore) Append(code []byte) (uint32, error) {
	if len(code) != s.codeLen {
		return 0, ErrBBQCodeLenMismatch
	}

	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	total := int(s.total.Load())
	shardIdx := total >> bbqShardShift

	// Create new shards as needed
	for shardIdx >= len(s.loadShards()) {
		// Seal previous shard if exists
		shards := s.loadShards()
		if len(shards) > 0 {
			prevShard := shards[len(shards)-1]
			prevShard.mu.Lock()
			prevShard.Seal()
			prevShard.mu.Unlock()
		}

		if err := s.createShard(); err != nil {
			return 0, err
		}
	}

	currentShard := s.loadShards()[shardIdx]
	currentShard.mu.Lock()
	err := currentShard.Append(code)
	currentShard.mu.Unlock()

	if err != nil {
		return 0, err
	}

	s.total.Add(1)
	return uint32(total), nil
}

// AppendBatch adds multiple BBQ codes and returns the starting global ID.
func (s *ShardedBBQStore) AppendBatch(codes [][]byte) (uint32, error) {
	if len(codes) == 0 {
		return 0, nil
	}

	for _, code := range codes {
		if len(code) != s.codeLen {
			return 0, ErrBBQCodeLenMismatch
		}
	}

	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	total := int(s.total.Load())
	startID := uint32(total)
	remaining := codes

	for len(remaining) > 0 {
		shardIdx := total >> bbqShardShift
		localID := total & bbqShardMask
		space := BBQPerShard - localID

		// Create new shards as needed
		for shardIdx >= len(s.loadShards()) {
			shards := s.loadShards()
			if len(shards) > 0 {
				prevShard := shards[len(shards)-1]
				prevShard.mu.Lock()
				prevShard.Seal()
				prevShard.mu.Unlock()
			}

			if err := s.createShard(); err != nil {
				return 0, err
			}
		}

		take := min(space, len(remaining))
		batch := remaining[:take]
		remaining = remaining[take:]

		currentShard := s.loadShards()[shardIdx]
		currentShard.mu.Lock()
		for _, code := range batch {
			if err := currentShard.Append(code); err != nil {
				currentShard.mu.Unlock()
				return 0, err
			}
		}
		currentShard.mu.Unlock()

		total += take
	}

	s.total.Store(uint64(total))
	return startID, nil
}

// Count returns the total number of BBQ codes stored.
func (s *ShardedBBQStore) Count() uint64 {
	return s.total.Load()
}

// CodeLen returns the BBQ code length.
func (s *ShardedBBQStore) CodeLen() int {
	return s.codeLen
}

// ShardCount returns the number of shards.
func (s *ShardedBBQStore) ShardCount() int {
	return len(s.loadShards())
}

// VerifyChecksum verifies the checksum of a specific shard.
// Returns true if the shard is valid or unsealed.
func (s *ShardedBBQStore) VerifyChecksum(shardIdx int) bool {
	shards := s.loadShards()
	if shardIdx >= len(shards) {
		return false
	}

	shard := shards[shardIdx]
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	return shard.VerifyChecksum()
}

// RebuildShard rebuilds a corrupted shard from provided codes.
func (s *ShardedBBQStore) RebuildShard(shardIdx int, codes [][]byte) error {
	shards := s.loadShards()
	if shardIdx >= len(shards) {
		return fmt.Errorf("bbq: shard %d does not exist", shardIdx)
	}

	shard := shards[shardIdx]
	shard.mu.Lock()

	if shard.region != nil {
		shard.region.Close()
	}

	path := filepath.Join(s.dir, fmt.Sprintf("shard_%04d.bin", shardIdx))
	newShard, err := createBBQShard(path, s.codeLen, s.shardCapacity)
	if err != nil {
		shard.mu.Unlock()
		return fmt.Errorf("bbq: recreate shard %d: %w", shardIdx, err)
	}

	for _, code := range codes {
		if err := newShard.Append(code); err != nil {
			newShard.Close()
			shard.mu.Unlock()
			return fmt.Errorf("bbq: write code to shard %d: %w", shardIdx, err)
		}
	}

	if len(codes) >= s.shardCapacity {
		newShard.Seal()
	}

	shard.region = newShard.region
	shard.codeLen = newShard.codeLen
	shard.capacity = newShard.capacity
	shard.count = newShard.count
	shard.checksum = newShard.checksum
	shard.sealed = newShard.sealed

	shard.mu.Unlock()
	return nil
}

// Sync flushes all shards to disk.
func (s *ShardedBBQStore) Sync() error {
	shards := s.loadShards()
	for i, shard := range shards {
		shard.mu.Lock()
		if shard.region != nil {
			if err := shard.region.Sync(); err != nil {
				shard.mu.Unlock()
				return fmt.Errorf("bbq: sync shard %d: %w", i, err)
			}
		}
		shard.mu.Unlock()
	}
	return s.saveMeta()
}

// Close closes all shards and saves metadata.
func (s *ShardedBBQStore) Close() error {
	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	// Save metadata first
	if err := s.saveMeta(); err != nil {
		return err
	}

	shards := s.loadShards()
	var firstErr error

	for i, shard := range shards {
		shard.mu.Lock()
		err := shard.Close()
		shard.mu.Unlock()

		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("bbq: close shard %d: %w", i, err)
		}
	}

	s.shards.Store([]*bbqShard(nil))
	return firstErr
}

// GetSlice returns a slice of BBQ codes as a contiguous byte slice.
// This is useful for bulk operations. Returns nil if range is invalid.
func (s *ShardedBBQStore) GetSlice(start, end uint32) []byte {
	if end <= start || uint64(end) > s.total.Load() {
		return nil
	}

	startShard := int(start >> bbqShardShift)
	endShard := int((end - 1) >> bbqShardShift)

	// Fast path: all codes in same shard
	if startShard == endShard {
		shard := s.loadShards()[startShard]
		shard.mu.RLock()
		defer shard.mu.RUnlock()

		if shard.region == nil {
			return nil
		}

		data := shard.region.Data()
		localStart := int(start & bbqShardMask)
		localEnd := int(end & bbqShardMask)
		if localEnd == 0 {
			localEnd = BBQPerShard
		}

		offsetStart := bbqHeaderSize + localStart*s.codeLen
		offsetEnd := bbqHeaderSize + localEnd*s.codeLen
		return data[offsetStart:offsetEnd]
	}

	// Slow path: codes span multiple shards, need to copy
	count := int(end - start)
	result := make([]byte, count*s.codeLen)
	offset := 0

	for i := start; i < end; i++ {
		code := s.Get(i)
		if code == nil {
			return nil
		}
		copy(result[offset:offset+s.codeLen], code)
		offset += s.codeLen
	}

	return result
}

// UnsafeSlice returns a zero-copy slice for a single shard's codes.
// The returned slice points directly into mmap memory.
// Only valid while the shard is open. Use with caution.
func (s *ShardedBBQStore) UnsafeSlice(shardIdx int) []byte {
	shards := s.loadShards()
	if shardIdx >= len(shards) {
		return nil
	}

	shard := shards[shardIdx]
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	if shard.region == nil {
		return nil
	}

	data := shard.region.Data()
	dataStart := bbqHeaderSize
	dataEnd := bbqHeaderSize + int(shard.count)*shard.codeLen
	return data[dataStart:dataEnd]
}

// Ensure unused import for unsafe (used in doc comment)
var _ = unsafe.Sizeof(0)
