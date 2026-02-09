package ivf

import (
	"math/bits"
)

type Config struct {
	NumPartitions int
	NProbe        int
	Oversample    int
	QuantBits     int
	NumWorkers    int
}

func ConfigForN(n, dim int) Config {
	if n <= 0 || dim <= 0 {
		return Config{}
	}

	logN := bits.Len(uint(n))
	logDim := bits.Len(uint(dim))

	numPartitions := 1 << (logN / 2)

	nprobe := logN * logN

	quantBits := bits.Len(uint(logDim))
	if quantBits < 1 {
		quantBits = 1
	}

	oversample := logN

	return Config{
		NumPartitions: numPartitions,
		NProbe:        nprobe,
		Oversample:    oversample,
		QuantBits:     quantBits,
		NumWorkers:    0,
	}
}

func (c Config) QuantMax() int       { return (1 << c.QuantBits) - 1 }
func (c Config) ValsPerByte() int    { return 8 / c.QuantBits }
func (c Config) CodeLen(dim int) int { return (dim + c.ValsPerByte() - 1) / c.ValsPerByte() }

type PostingList struct {
	IDs         []uint32
	BinaryCodes []uint64
}

type BuildResult struct {
	NumVectors       int
	NumPartitions    int
	AvgPartitionSize float64
	MaxPartitionSize int
	MinPartitionSize int
}

type SearchResult struct {
	ID         uint32
	Similarity float64
}

type BuildStats struct {
	StoreNanos     int64
	BBQNanos       int64
	PartitionNanos int64
	GraphNanos     int64
	TotalNanos     int64
}
