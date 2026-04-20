package blob

import "testing"

func TestComputeShardCount_PowerOfTwo(t *testing.T) {
	cases := []int{1, 2, 4, 8, 16, 32}
	for _, cpus := range cases {
		got := computeShardCount(cpus)
		if got&(got-1) != 0 {
			t.Errorf("computeShardCount(%d) = %d is not a power of two", cpus, got)
		}
	}
}

func TestComputeShardCount_AtLeastDesired(t *testing.T) {
	cpus := 8
	got := computeShardCount(cpus)
	want := cpus * shardOversubscribeFactor
	if got < want {
		t.Errorf("computeShardCount(%d) = %d, want ≥ %d (oversubscription floor)", cpus, got, want)
	}
}

func TestComputeShardCount_CapsAtMax(t *testing.T) {
	got := computeShardCount(10000)
	if got > maxShards {
		t.Errorf("computeShardCount(10000) = %d, should be capped at %d", got, maxShards)
	}
}

func TestComputeShardCount_FloorAtMin(t *testing.T) {
	got := computeShardCount(0)
	if got < minShards {
		t.Errorf("computeShardCount(0) = %d, should be ≥ %d (minimum)", got, minShards)
	}
}

func TestRoundUpPow2(t *testing.T) {
	cases := map[int]int{
		1:  1,
		2:  2,
		3:  4,
		5:  8,
		17: 32,
	}
	for in, want := range cases {
		if got := roundUpPow2(in); got != want {
			t.Errorf("roundUpPow2(%d) = %d, want %d", in, got, want)
		}
	}
}
