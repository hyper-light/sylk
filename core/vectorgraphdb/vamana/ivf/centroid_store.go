package ivf

import (
	"encoding/binary"
	"fmt"
	"hash/crc64"
	"unsafe"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

var centroidMagic = [4]byte{'C', 'T', 'R', '1'}

const (
	centroidVersion      = 1
	centroidHeaderSize   = 16
	centroidChecksumSize = 8
)

type CentroidStore struct {
	region    *storage.MmapRegion
	K         int
	Dim       int
	Centroids []float32
	Norms     []float64
}

func centroidFileSize(k, dim int) int64 {
	return int64(centroidHeaderSize + k*dim*4 + k*8 + centroidChecksumSize)
}

func SaveCentroids(path string, centroids [][]float32, norms []float64) error {
	if len(centroids) == 0 {
		return fmt.Errorf("centroid: no centroids to save")
	}

	k := len(centroids)
	dim := len(centroids[0])

	if len(norms) != k {
		return fmt.Errorf("centroid: norms length %d != centroids length %d", len(norms), k)
	}

	for i, c := range centroids {
		if len(c) != dim {
			return fmt.Errorf("centroid: centroid %d has dim %d, expected %d", i, len(c), dim)
		}
	}

	size := centroidFileSize(k, dim)
	region, err := storage.MapFile(path, size, false)
	if err != nil {
		return fmt.Errorf("centroid: map file: %w", err)
	}
	defer region.Close()

	data := region.Data()

	copy(data[0:4], centroidMagic[:])
	binary.LittleEndian.PutUint32(data[4:8], centroidVersion)
	binary.LittleEndian.PutUint32(data[8:12], uint32(k))
	binary.LittleEndian.PutUint32(data[12:16], uint32(dim))

	centroidOffset := centroidHeaderSize
	for _, c := range centroids {
		for _, v := range c {
			binary.LittleEndian.PutUint32(data[centroidOffset:], uint32FromFloat32(v))
			centroidOffset += 4
		}
	}

	normOffset := centroidHeaderSize + k*dim*4
	for _, n := range norms {
		binary.LittleEndian.PutUint64(data[normOffset:], uint64FromFloat64(n))
		normOffset += 8
	}

	checksumOffset := centroidHeaderSize + k*dim*4 + k*8
	checksum := crc64.Checksum(data[:checksumOffset], crc64.MakeTable(crc64.ECMA))
	binary.LittleEndian.PutUint64(data[checksumOffset:], checksum)

	if err := region.Sync(); err != nil {
		return fmt.Errorf("centroid: sync: %w", err)
	}

	return nil
}

func LoadCentroids(path string) (*CentroidStore, error) {
	region, err := storage.MapFile(path, centroidHeaderSize, true)
	if err != nil {
		return nil, fmt.Errorf("centroid: open for header: %w", err)
	}
	region.Close()

	region, err = storage.MapFile(path, centroidHeaderSize, true)
	if err != nil {
		return nil, fmt.Errorf("centroid: reopen: %w", err)
	}

	data := region.Data()

	if data[0] != centroidMagic[0] || data[1] != centroidMagic[1] ||
		data[2] != centroidMagic[2] || data[3] != centroidMagic[3] {
		region.Close()
		return nil, fmt.Errorf("centroid: invalid magic")
	}

	version := binary.LittleEndian.Uint32(data[4:8])
	if version != centroidVersion {
		region.Close()
		return nil, fmt.Errorf("centroid: unsupported version %d", version)
	}

	k := int(binary.LittleEndian.Uint32(data[8:12]))
	dim := int(binary.LittleEndian.Uint32(data[12:16]))

	region.Close()

	fullSize := centroidFileSize(k, dim)
	region, err = storage.MapFile(path, fullSize, true)
	if err != nil {
		return nil, fmt.Errorf("centroid: map full file: %w", err)
	}

	data = region.Data()

	checksumOffset := centroidHeaderSize + k*dim*4 + k*8
	storedChecksum := binary.LittleEndian.Uint64(data[checksumOffset:])
	computedChecksum := crc64.Checksum(data[:checksumOffset], crc64.MakeTable(crc64.ECMA))

	if storedChecksum != computedChecksum {
		region.Close()
		return nil, fmt.Errorf("centroid: checksum mismatch: stored=%x computed=%x", storedChecksum, computedChecksum)
	}

	centroidStart := centroidHeaderSize
	centroidEnd := centroidHeaderSize + k*dim*4

	centroidBytes := data[centroidStart:centroidEnd]
	centroids := unsafe.Slice((*float32)(unsafe.Pointer(&centroidBytes[0])), k*dim)

	normStart := centroidEnd
	normEnd := normStart + k*8
	normBytes := data[normStart:normEnd]
	norms := unsafe.Slice((*float64)(unsafe.Pointer(&normBytes[0])), k)

	return &CentroidStore{
		region:    region,
		K:         k,
		Dim:       dim,
		Centroids: centroids,
		Norms:     norms,
	}, nil
}

func (cs *CentroidStore) GetCentroid(idx int) []float32 {
	if idx < 0 || idx >= cs.K {
		return nil
	}
	start := idx * cs.Dim
	return cs.Centroids[start : start+cs.Dim]
}

func (cs *CentroidStore) GetNorm(idx int) float64 {
	if idx < 0 || idx >= cs.K {
		return 0
	}
	return cs.Norms[idx]
}

func (cs *CentroidStore) Close() error {
	if cs.region != nil {
		return cs.region.Close()
	}
	return nil
}

func uint32FromFloat32(f float32) uint32 {
	return *(*uint32)(unsafe.Pointer(&f))
}

func uint64FromFloat64(f float64) uint64 {
	return *(*uint64)(unsafe.Pointer(&f))
}
