package ivf

import (
	"encoding/binary"
	"fmt"
	"hash/crc64"
	"unsafe"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

var partitionMagic = [4]byte{'P', 'T', 'N', '1'}

const (
	partitionHeaderSize   = 12
	partitionChecksumSize = 8
)

type PartitionIndex struct {
	region  *storage.MmapRegion
	K       int
	N       int
	offsets []uint32
	ids     []uint32
}

func partitionFileSize(k, n int) int64 {
	return int64(partitionHeaderSize + (k+1)*4 + n*4 + partitionChecksumSize)
}

func SavePartitionIndex(path string, partitionIDs [][]uint32) error {
	if len(partitionIDs) == 0 {
		return fmt.Errorf("partition: no partitions to save")
	}

	k := len(partitionIDs)
	n := 0
	for _, p := range partitionIDs {
		n += len(p)
	}

	size := partitionFileSize(k, n)
	region, err := storage.MapFile(path, size, false)
	if err != nil {
		return fmt.Errorf("partition: map file: %w", err)
	}
	defer region.Close()

	data := region.Data()

	copy(data[0:4], partitionMagic[:])
	binary.LittleEndian.PutUint32(data[4:8], uint32(k))
	binary.LittleEndian.PutUint32(data[8:12], uint32(n))

	offsetsStart := partitionHeaderSize
	idsStart := partitionHeaderSize + (k+1)*4

	offset := uint32(0)
	for i, p := range partitionIDs {
		binary.LittleEndian.PutUint32(data[offsetsStart+i*4:], offset)
		offset += uint32(len(p))
	}
	binary.LittleEndian.PutUint32(data[offsetsStart+k*4:], offset)

	idOffset := idsStart
	for _, p := range partitionIDs {
		for _, id := range p {
			binary.LittleEndian.PutUint32(data[idOffset:], id)
			idOffset += 4
		}
	}

	checksumOffset := idsStart + n*4
	checksum := crc64.Checksum(data[:checksumOffset], crc64.MakeTable(crc64.ECMA))
	binary.LittleEndian.PutUint64(data[checksumOffset:], checksum)

	if err := region.Sync(); err != nil {
		return fmt.Errorf("partition: sync: %w", err)
	}

	return nil
}

func LoadPartitionIndex(path string) (*PartitionIndex, error) {
	region, err := storage.MapFile(path, partitionHeaderSize, true)
	if err != nil {
		return nil, fmt.Errorf("partition: open for header: %w", err)
	}

	data := region.Data()

	if data[0] != partitionMagic[0] || data[1] != partitionMagic[1] ||
		data[2] != partitionMagic[2] || data[3] != partitionMagic[3] {
		region.Close()
		return nil, fmt.Errorf("partition: invalid magic")
	}

	k := int(binary.LittleEndian.Uint32(data[4:8]))
	n := int(binary.LittleEndian.Uint32(data[8:12]))

	region.Close()

	fullSize := partitionFileSize(k, n)
	region, err = storage.MapFile(path, fullSize, true)
	if err != nil {
		return nil, fmt.Errorf("partition: map full file: %w", err)
	}

	data = region.Data()

	checksumOffset := partitionHeaderSize + (k+1)*4 + n*4
	storedChecksum := binary.LittleEndian.Uint64(data[checksumOffset:])
	computedChecksum := crc64.Checksum(data[:checksumOffset], crc64.MakeTable(crc64.ECMA))

	if storedChecksum != computedChecksum {
		region.Close()
		return nil, fmt.Errorf("partition: checksum mismatch: stored=%x computed=%x", storedChecksum, computedChecksum)
	}

	offsetsStart := partitionHeaderSize
	offsetsEnd := partitionHeaderSize + (k+1)*4
	offsetBytes := data[offsetsStart:offsetsEnd]
	offsets := unsafe.Slice((*uint32)(unsafe.Pointer(&offsetBytes[0])), k+1)

	idsStart := offsetsEnd
	idsEnd := idsStart + n*4
	idBytes := data[idsStart:idsEnd]
	ids := unsafe.Slice((*uint32)(unsafe.Pointer(&idBytes[0])), n)

	return &PartitionIndex{
		region:  region,
		K:       k,
		N:       n,
		offsets: offsets,
		ids:     ids,
	}, nil
}

func (pi *PartitionIndex) Partition(p int) []uint32 {
	if p < 0 || p >= pi.K {
		return nil
	}
	start := pi.offsets[p]
	end := pi.offsets[p+1]
	return pi.ids[start:end]
}

func (pi *PartitionIndex) PartitionSize(p int) int {
	if p < 0 || p >= pi.K {
		return 0
	}
	return int(pi.offsets[p+1] - pi.offsets[p])
}

func (pi *PartitionIndex) Close() error {
	if pi.region != nil {
		return pi.region.Close()
	}
	return nil
}
