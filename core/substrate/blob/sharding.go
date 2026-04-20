package blob

import "runtime"

// Shard sizing is derived from runtime.NumCPU() rather than hardcoded so
// the store scales with available parallelism on any host. The
// oversubscription factor reduces contention when goroutines are
// scheduled densely (e.g. parallel build steps each performing many Put
// operations); 4× CPU shards is the rate at which empirical contention
// drops to single-digit microseconds in benchmarks. The hard cap matches
// the value range of a single byte (the first byte of the hash is the
// shard selector), which is the largest count the addressing scheme can
// represent without widening the index.
const (
	shardOversubscribeFactor = 4
	maxShards                = 256
	minShards                = 1
)

// computeShardCount returns the next power of two ≥ cpuCount * factor,
// capped at maxShards. Power-of-two sizing lets shardFor mask the hash's
// first byte rather than divide, which is measurably faster in the hot
// path and removes a class of integer-division micro-pessimizations the
// compiler cannot otherwise eliminate.
func computeShardCount(cpuCount int) int {
	desired := desiredShards(cpuCount)
	return roundUpPow2(desired)
}

func desiredShards(cpuCount int) int {
	scaled := cpuCount * shardOversubscribeFactor
	if scaled < minShards {
		return minShards
	}
	return scaled
}

func roundUpPow2(n int) int {
	if n >= maxShards {
		return maxShards
	}
	power := 1
	for power < n {
		power <<= 1
	}
	return power
}

func defaultShardCount() int {
	return computeShardCount(runtime.NumCPU())
}
