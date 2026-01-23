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

	// NumPartitions: sqrt(N) partitions gives O(sqrt(N)) vectors per partition
	// This balances partition overhead vs scan cost
	// partitionBits = ceil(log2(sqrt(N))) = ceil(log2(N)/2)
	logN := bits.Len(uint(n))
	partitionBits := (logN + 1) / 2
	numPartitions := 1 << partitionBits

	// NProbe: sqrt(numPartitions) for multi-probe coverage
	// With P partitions, probing sqrt(P) gives good recall/latency tradeoff
	// nprobe = sqrt(P) = sqrt(sqrt(N)) = N^0.25
	nprobe := 1 << ((partitionBits + 1) / 2)
	if nprobe > numPartitions {
		nprobe = numPartitions
	}

	// QuantBits: bits needed to represent log2(dim) distinct levels
	// More dimensions need fewer bits per dimension (information spreads)
	// quantBits = ceil(log2(log2(dim)))
	logDim := bits.Len(uint(dim))
	quantBits := bits.Len(uint(logDim))
	if quantBits < 2 {
		quantBits = 2
	}

	// Oversample: compensate for quantization ranking errors
	// With q-bit quantization, max rank displacement ~ 2^q
	// oversample = 2^quantBits ensures we capture displaced true neighbors
	oversample := 1 << quantBits

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
	PartitionNanos int64
	BinaryNanos    int64
	Uint4Nanos     int64
	TotalNanos     int64
}
