package ivf

import (
	"math/rand"
	"testing"
)

func generateTestData(n, dim, codeLen int) ([][]float32, []byte) {
	vectors := make([][]float32, n)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rand.Float32()*2 - 1
		}
	}

	codes := make([]byte, n*codeLen)
	for i := range codes {
		codes[i] = byte(rand.Intn(256))
	}

	return vectors, codes
}

func BenchmarkHammingDistance(b *testing.B) {
	sizes := []int{12, 24, 48, 96}
	for _, codeLen := range sizes {
		b.Run(sprintf("codeLen=%d", codeLen), func(b *testing.B) {
			code1 := make([]byte, codeLen)
			code2 := make([]byte, codeLen)
			for i := range code1 {
				code1[i] = byte(rand.Intn(256))
				code2[i] = byte(rand.Intn(256))
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = HammingDistance(code1, code2)
			}
		})
	}
}

func BenchmarkPQInsert(b *testing.B) {
	capacities := []int{50, 100, 200}
	for _, cap := range capacities {
		b.Run(sprintf("capacity=%d", cap), func(b *testing.B) {
			pq := &neighborPQ{
				data:     make([]vamanaCandidate, cap+1),
				capacity: cap,
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pq.clear()
				for j := 0; j < cap*2; j++ {
					pq.insert(uint32(j), rand.Intn(1000))
				}
			}
		})
	}
}

func BenchmarkGreedySearchLocal(b *testing.B) {
	sizes := []int{100, 300, 500}
	for _, n := range sizes {
		b.Run(sprintf("n=%d", n), func(b *testing.B) {
			codeLen := 96
			_, codes := generateTestData(n, 768, codeLen)

			members := make([]uint32, n)
			for i := range members {
				members[i] = uint32(i)
			}

			localAdj := make([][]uint32, n)
			R := 19
			for i := range localAdj {
				localAdj[i] = make([]uint32, R)
				for j := 0; j < R; j++ {
					localAdj[i][j] = uint32(rand.Intn(n))
				}
			}

			w := newGraphBuildWorker(n, R, 100)
			g := &VamanaGraph{}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = g.greedySearchLocal(0, n/2, localAdj, members, codes, codeLen, 100, w)
			}
		})
	}
}

func BenchmarkRobustPruneLocal(b *testing.B) {
	candidateCounts := []int{50, 100, 200}
	for _, numCandidates := range candidateCounts {
		b.Run(sprintf("candidates=%d", numCandidates), func(b *testing.B) {
			n := 300
			codeLen := 96
			_, codes := generateTestData(n, 768, codeLen)

			members := make([]uint32, n)
			for i := range members {
				members[i] = uint32(i)
			}

			localAdj := make([][]uint32, n)
			R := 19
			for i := range localAdj {
				localAdj[i] = make([]uint32, 0, R)
			}

			candidates := make([]vamanaCandidate, numCandidates)
			for i := range candidates {
				candidates[i] = vamanaCandidate{uint32(i + 1), rand.Intn(500)}
			}

			w := newGraphBuildWorker(n, R, 100)
			g := &VamanaGraph{}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				candidatesCopy := make([]vamanaCandidate, len(candidates))
				copy(candidatesCopy, candidates)
				_ = g.robustPruneLocal(0, candidatesCopy, localAdj, members, codes, codeLen, 1.0, R, w)
			}
		})
	}
}

func BenchmarkBuildPartitionGraph(b *testing.B) {
	sizes := []int{100, 300, 500}
	for _, n := range sizes {
		b.Run(sprintf("n=%d", n), func(b *testing.B) {
			dim := 768
			vectors, _ := generateTestData(n, dim, 96)

			config := ConfigForN(n, dim)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				idx := NewIndex(config, dim)
				idx.Build(vectors)
			}
		})
	}
}

func BenchmarkFullBuild(b *testing.B) {
	sizes := []int{1000, 5000, 10000}
	for _, n := range sizes {
		b.Run(sprintf("n=%d", n), func(b *testing.B) {
			dim := 768
			vectors, _ := generateTestData(n, dim, 96)

			config := ConfigForN(n, dim)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				idx := NewIndex(config, dim)
				idx.Build(vectors)
			}
		})
	}
}

func sprintf(format string, args ...any) string {
	result := format
	for _, arg := range args {
		switch v := arg.(type) {
		case int:
			result = replaceFirst(result, "%d", itoa(v))
		case string:
			result = replaceFirst(result, "%s", v)
		}
	}
	return result
}

func replaceFirst(s, old, new string) string {
	for i := 0; i <= len(s)-len(old); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	if negative {
		digits = append(digits, '-')
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}
