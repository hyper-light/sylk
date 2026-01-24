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

const (
	NormsPerShard = 65536

	normShardShift = 16
	normShardMask  = (1 << 16) - 1

	normHeaderSize = 16
	normMagic      = 0x314D524E // "NRM1" in little-endian
	normEntrySize  = 8          // float64
)

var (
	ErrNormCapacityExceeded = errors.New("norm shard capacity exceeded")
	ErrNormInvalidMagic     = errors.New("norm invalid magic bytes")
	ErrNormChecksumMismatch = errors.New("norm checksum mismatch: shard corrupted")
)

type shardedNormMeta struct {
	ShardCapacity int `yaml:"shard_capacity"`
	Total         int `yaml:"total"`
}

type normShard struct {
	region   *storage.MmapRegion
	capacity int
	count    uint32
	checksum uint64
	sealed   bool
	mu       sync.RWMutex
}

type ShardedNormStore struct {
	dir           string
	shardCapacity int

	shards  atomic.Value
	shardMu sync.Mutex

	total atomic.Uint64
}

func CreateShardedNormStore(dir string) (*ShardedNormStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("norms: create dir: %w", err)
	}

	s := &ShardedNormStore{
		dir:           dir,
		shardCapacity: NormsPerShard,
	}
	s.shards.Store(make([]*normShard, 0, 8))

	if err := s.createShard(); err != nil {
		return nil, err
	}

	if err := s.saveMeta(); err != nil {
		return nil, err
	}

	return s, nil
}

func OpenShardedNormStore(dir string) (*ShardedNormStore, []int, error) {
	metaData, err := os.ReadFile(filepath.Join(dir, "meta.yaml"))
	if err != nil {
		return nil, nil, fmt.Errorf("norms: read meta: %w", err)
	}

	var meta shardedNormMeta
	if err := yaml.Unmarshal(metaData, &meta); err != nil {
		return nil, nil, fmt.Errorf("norms: parse meta: %w", err)
	}

	numShards := (meta.Total + meta.ShardCapacity - 1) / meta.ShardCapacity
	if numShards == 0 {
		numShards = 1
	}

	shards := make([]*normShard, numShards)
	var corrupted []int

	for i := range numShards {
		path := filepath.Join(dir, fmt.Sprintf("shard_%04d.bin", i))
		shard, err := openNormShard(path)
		if err != nil {
			for j := range i {
				if shards[j] != nil {
					shards[j].Close()
				}
			}
			return nil, nil, fmt.Errorf("norms: open shard %d: %w", i, err)
		}

		if shard.sealed && !shard.VerifyChecksum() {
			corrupted = append(corrupted, i)
		}

		shards[i] = shard
	}

	s := &ShardedNormStore{
		dir:           dir,
		shardCapacity: meta.ShardCapacity,
	}
	s.shards.Store(shards)
	s.total.Store(uint64(meta.Total))

	return s, corrupted, nil
}

func openNormShard(path string) (*normShard, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}

	size := info.Size()
	if size < normHeaderSize {
		return nil, fmt.Errorf("file too small: %d bytes", size)
	}

	region, err := storage.MapFile(path, size, false)
	if err != nil {
		return nil, fmt.Errorf("mmap: %w", err)
	}

	data := region.Data()

	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != normMagic {
		region.Close()
		return nil, ErrNormInvalidMagic
	}

	count := binary.LittleEndian.Uint32(data[4:8])

	capacity := NormsPerShard
	sealed := int(count) >= capacity

	var checksum uint64
	if sealed {
		checksumOffset := normHeaderSize + int64(capacity)*normEntrySize
		if size >= checksumOffset+8 {
			checksum = binary.LittleEndian.Uint64(data[checksumOffset : checksumOffset+8])
		}
	}

	return &normShard{
		region:   region,
		capacity: capacity,
		count:    count,
		checksum: checksum,
		sealed:   sealed,
	}, nil
}

func createNormShard(path string, capacity int) (*normShard, error) {
	totalSize := int64(normHeaderSize) + int64(capacity)*normEntrySize + 8

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

	data := region.Data()
	binary.LittleEndian.PutUint32(data[0:4], normMagic)
	binary.LittleEndian.PutUint32(data[4:8], 0)
	binary.LittleEndian.PutUint64(data[8:16], 0)

	return &normShard{
		region:   region,
		capacity: capacity,
		count:    0,
		sealed:   false,
	}, nil
}

func (s *normShard) VerifyChecksum() bool {
	if !s.sealed || s.region == nil {
		return true
	}

	data := s.region.Data()
	dataStart := normHeaderSize
	dataEnd := normHeaderSize + int(s.count)*normEntrySize

	computed := crc64.Checksum(data[dataStart:dataEnd], crc64Table)
	return computed == s.checksum
}

func (s *normShard) computeAndWriteChecksum() {
	data := s.region.Data()
	dataStart := normHeaderSize
	dataEnd := normHeaderSize + int(s.count)*normEntrySize

	s.checksum = crc64.Checksum(data[dataStart:dataEnd], crc64Table)

	checksumOffset := normHeaderSize + s.capacity*normEntrySize
	binary.LittleEndian.PutUint64(data[checksumOffset:checksumOffset+8], s.checksum)
}

func (s *normShard) Get(localID uint32) float64 {
	if s.region == nil || localID >= s.count {
		return 0
	}

	data := s.region.Data()
	offset := normHeaderSize + int(localID)*normEntrySize
	bits := binary.LittleEndian.Uint64(data[offset : offset+8])
	return *(*float64)(unsafe.Pointer(&bits))
}

func (s *normShard) GetSlice(start, end uint32) []float64 {
	if s.region == nil || end > s.count || start >= end {
		return nil
	}

	data := s.region.Data()
	offsetStart := normHeaderSize + int(start)*normEntrySize
	offsetEnd := normHeaderSize + int(end)*normEntrySize

	byteSlice := data[offsetStart:offsetEnd]
	return unsafe.Slice((*float64)(unsafe.Pointer(&byteSlice[0])), end-start)
}

func (s *normShard) Append(norm float64) error {
	if int(s.count) >= s.capacity {
		return ErrNormCapacityExceeded
	}

	data := s.region.Data()
	offset := normHeaderSize + int(s.count)*normEntrySize
	bits := *(*uint64)(unsafe.Pointer(&norm))
	binary.LittleEndian.PutUint64(data[offset:offset+8], bits)

	s.count++
	binary.LittleEndian.PutUint32(data[4:8], s.count)

	return nil
}

func (s *normShard) Seal() {
	if s.sealed {
		return
	}
	s.computeAndWriteChecksum()
	s.sealed = true
}

func (s *normShard) Close() error {
	if s.region == nil {
		return nil
	}
	err := s.region.Close()
	s.region = nil
	return err
}

func (s *ShardedNormStore) loadShards() []*normShard {
	return s.shards.Load().([]*normShard)
}

func (s *ShardedNormStore) createShard() error {
	shards := s.loadShards()
	shardIdx := len(shards)
	path := filepath.Join(s.dir, fmt.Sprintf("shard_%04d.bin", shardIdx))

	shard, err := createNormShard(path, s.shardCapacity)
	if err != nil {
		return fmt.Errorf("norms: create shard %d: %w", shardIdx, err)
	}

	newShards := make([]*normShard, len(shards)+1)
	copy(newShards, shards)
	newShards[shardIdx] = shard
	s.shards.Store(newShards)
	return nil
}

func (s *ShardedNormStore) saveMeta() error {
	meta := shardedNormMeta{
		ShardCapacity: s.shardCapacity,
		Total:         int(s.total.Load()),
	}
	data, err := yaml.Marshal(&meta)
	if err != nil {
		return fmt.Errorf("norms: marshal meta: %w", err)
	}
	return os.WriteFile(filepath.Join(s.dir, "meta.yaml"), data, 0644)
}

func (s *ShardedNormStore) getShard(id uint32) (*normShard, uint32) {
	shardIdx := int(id >> normShardShift)
	localID := id & normShardMask

	shards := s.loadShards()
	if shardIdx >= len(shards) {
		return nil, 0
	}

	return shards[shardIdx], localID
}

func (s *ShardedNormStore) Get(id uint32) float64 {
	shard, localID := s.getShard(id)
	if shard == nil {
		return 0
	}

	shard.mu.RLock()
	norm := shard.Get(localID)
	shard.mu.RUnlock()
	return norm
}

func (s *ShardedNormStore) GetSlice(start, end uint32) []float64 {
	if end <= start || uint64(end) > s.total.Load() {
		return nil
	}

	startShard := int(start >> normShardShift)
	endShard := int((end - 1) >> normShardShift)

	if startShard == endShard {
		shard := s.loadShards()[startShard]
		shard.mu.RLock()
		defer shard.mu.RUnlock()

		localStart := start & normShardMask
		localEnd := end & normShardMask
		if localEnd == 0 {
			localEnd = NormsPerShard
		}

		return shard.GetSlice(localStart, localEnd)
	}

	count := int(end - start)
	result := make([]float64, count)
	offset := 0

	for i := start; i < end; i++ {
		result[offset] = s.Get(i)
		offset++
	}

	return result
}

func (s *ShardedNormStore) Append(norm float64) (uint32, error) {
	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	total := int(s.total.Load())
	shardIdx := total >> normShardShift

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

	currentShard := s.loadShards()[shardIdx]
	currentShard.mu.Lock()
	err := currentShard.Append(norm)
	currentShard.mu.Unlock()

	if err != nil {
		return 0, err
	}

	s.total.Add(1)
	return uint32(total), nil
}

func (s *ShardedNormStore) AppendBatch(norms []float64) (uint32, error) {
	if len(norms) == 0 {
		return 0, nil
	}

	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	total := int(s.total.Load())
	startID := uint32(total)
	remaining := norms

	for len(remaining) > 0 {
		shardIdx := total >> normShardShift
		localID := total & normShardMask
		space := NormsPerShard - localID

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
		for _, norm := range batch {
			if err := currentShard.Append(norm); err != nil {
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

func (s *ShardedNormStore) Count() uint64 {
	return s.total.Load()
}

func (s *ShardedNormStore) ShardCount() int {
	return len(s.loadShards())
}

func (s *ShardedNormStore) VerifyChecksum(shardIdx int) bool {
	shards := s.loadShards()
	if shardIdx >= len(shards) {
		return false
	}

	shard := shards[shardIdx]
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	return shard.VerifyChecksum()
}

func (s *ShardedNormStore) RebuildShard(shardIdx int, norms []float64) error {
	shards := s.loadShards()
	if shardIdx >= len(shards) {
		return fmt.Errorf("norms: shard %d does not exist", shardIdx)
	}

	shard := shards[shardIdx]
	shard.mu.Lock()

	if shard.region != nil {
		shard.region.Close()
	}

	path := filepath.Join(s.dir, fmt.Sprintf("shard_%04d.bin", shardIdx))
	newShard, err := createNormShard(path, s.shardCapacity)
	if err != nil {
		shard.mu.Unlock()
		return fmt.Errorf("norms: recreate shard %d: %w", shardIdx, err)
	}

	for _, norm := range norms {
		if err := newShard.Append(norm); err != nil {
			newShard.Close()
			shard.mu.Unlock()
			return fmt.Errorf("norms: write to shard %d: %w", shardIdx, err)
		}
	}

	if len(norms) >= s.shardCapacity {
		newShard.Seal()
	}

	shard.region = newShard.region
	shard.capacity = newShard.capacity
	shard.count = newShard.count
	shard.checksum = newShard.checksum
	shard.sealed = newShard.sealed

	shard.mu.Unlock()
	return nil
}

func (s *ShardedNormStore) Sync() error {
	shards := s.loadShards()
	for i, shard := range shards {
		shard.mu.Lock()
		if shard.region != nil {
			if err := shard.region.Sync(); err != nil {
				shard.mu.Unlock()
				return fmt.Errorf("norms: sync shard %d: %w", i, err)
			}
		}
		shard.mu.Unlock()
	}
	return s.saveMeta()
}

func (s *ShardedNormStore) Close() error {
	s.shardMu.Lock()
	defer s.shardMu.Unlock()

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
			firstErr = fmt.Errorf("norms: close shard %d: %w", i, err)
		}
	}

	s.shards.Store([]*normShard(nil))
	return firstErr
}
