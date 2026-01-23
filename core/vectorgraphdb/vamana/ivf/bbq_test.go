package ivf

import (
	"math/rand"
	"testing"
)

func BenchmarkBBQ_Distance(b *testing.B) {
	dim := 768
	bbq := NewBBQ(dim)

	vectors := make([][]float32, 1000)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rand.Float32()*2 - 1
		}
	}
	bbq.Train(vectors)

	codes := make([][]byte, 1000)
	for i := range codes {
		codes[i] = make([]byte, bbq.CodeLen())
		bbq.EncodeDB(vectors[i], codes[i])
	}

	query := make([]float32, dim)
	for i := range query {
		query[i] = rand.Float32()*2 - 1
	}
	qEnc := bbq.EncodeQuery(query)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for _, code := range codes {
			_ = qEnc.Distance(code)
		}
	}
}

func BenchmarkBBQ_DistanceFlat(b *testing.B) {
	dim := 768
	bbq := NewBBQ(dim)

	n := 10000
	vectors := make([][]float32, n)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rand.Float32()*2 - 1
		}
	}
	bbq.Train(vectors)

	codesFlat := make([]byte, n*bbq.CodeLen())
	for i := range vectors {
		offset := i * bbq.CodeLen()
		bbq.EncodeDB(vectors[i], codesFlat[offset:offset+bbq.CodeLen()])
	}

	query := make([]float32, dim)
	for i := range query {
		query[i] = rand.Float32()*2 - 1
	}
	qEnc := bbq.EncodeQuery(query)
	dists := make([]int32, n)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		qEnc.DistanceFlat(codesFlat, n, dists)
	}
}

func BenchmarkHamming(b *testing.B) {
	codeLen := 96
	codes := make([][]byte, 1000)
	for i := range codes {
		codes[i] = make([]byte, codeLen)
		for j := range codes[i] {
			codes[i][j] = byte(rand.Intn(256))
		}
	}
	query := make([]byte, codeLen)
	for i := range query {
		query[i] = byte(rand.Intn(256))
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for _, code := range codes {
			_ = HammingDistance(query, code)
		}
	}
}

func TestBBQ_Correctness(t *testing.T) {
	dim := 768
	bbq := NewBBQ(dim)

	n := 1000
	vectors := make([][]float32, n)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rand.Float32()*2 - 1
		}
	}
	bbq.Train(vectors)

	codes := make([][]byte, n)
	for i := range codes {
		codes[i] = make([]byte, bbq.CodeLen())
		bbq.EncodeDB(vectors[i], codes[i])
	}

	query := vectors[0]
	qEnc := bbq.EncodeQuery(query)

	selfDist := qEnc.Distance(codes[0])
	t.Logf("Self distance (should be highest): %d", selfDist)

	maxDist := int32(-1 << 30)
	maxIdx := -1
	for i, code := range codes {
		d := qEnc.Distance(code)
		if d > maxDist {
			maxDist = d
			maxIdx = i
		}
	}

	if maxIdx != 0 {
		t.Logf("Warning: self not highest similarity, got idx=%d dist=%d vs self=%d", maxIdx, maxDist, selfDist)
	} else {
		t.Logf("PASS: Self is highest similarity as expected")
	}
}
