package scann

import (
	"math"
	"math/bits"
	"math/rand/v2"
	"sync"

	"github.com/viterin/vek/vek32"
)

type FlashCoder struct {
	numSubspaces int
	subspaceDim  int
	numCentroids int
	codebooks    [][]float32
	sdt          [][]float32
	codes        [][]uint8
	codeMags     [][]float32
}

func (b *BatchBuilder) deriveFlashParams(n, dim, R int) (numSubspaces, numCentroids int) {
	numSubspaces = bits.Len(uint(dim))
	if numSubspaces > dim {
		numSubspaces = dim
	}

	numCentroids = 1 << bits.Len(uint(R))

	return numSubspaces, numCentroids
}

func (b *BatchBuilder) NewFlashCoder(vectors [][]float32, R int) *FlashCoder {
	n := len(vectors)
	if n == 0 {
		return nil
	}
	dim := len(vectors[0])

	numSubspaces, numCentroids := b.deriveFlashParams(n, dim, R)
	subspaceDim := dim / numSubspaces

	if subspaceDim*numSubspaces != dim {
		remainder := dim - subspaceDim*numSubspaces
		if remainder > 0 {
			subspaceDim = (dim + numSubspaces - 1) / numSubspaces
		}
	}

	fc := &FlashCoder{
		numSubspaces: numSubspaces,
		subspaceDim:  subspaceDim,
		numCentroids: numCentroids,
		codebooks:    make([][]float32, numSubspaces),
		codes:        make([][]uint8, n),
	}

	codeStorage := make([]uint8, n*numSubspaces)
	for i := range n {
		fc.codes[i] = codeStorage[i*numSubspaces : (i+1)*numSubspaces : (i+1)*numSubspaces]
	}

	fc.trainCodebooks(vectors, b.config.NumWorkers)
	fc.encodeVectors(vectors, b.config.NumWorkers)
	fc.buildSDT()

	return fc
}

func (fc *FlashCoder) trainCodebooks(vectors [][]float32, numWorkers int) {
	n := len(vectors)
	dim := len(vectors[0])

	sampleSize := fc.numCentroids * bits.Len(uint(fc.numCentroids)) * fc.numSubspaces
	if sampleSize > n {
		sampleSize = n
	}

	indices := rand.Perm(n)[:sampleSize]

	var wg sync.WaitGroup
	for m := range fc.numSubspaces {
		wg.Add(1)
		go func(subspace int) {
			defer wg.Done()
			fc.trainSubspaceCodebook(subspace, vectors, indices, dim)
		}(m)
	}
	wg.Wait()
}

func (fc *FlashCoder) trainSubspaceCodebook(subspace int, vectors [][]float32, sampleIndices []int, fullDim int) {
	subStart := subspace * fc.subspaceDim
	subEnd := subStart + fc.subspaceDim
	if subEnd > fullDim {
		subEnd = fullDim
	}
	actualSubDim := subEnd - subStart

	sampleCount := len(sampleIndices)
	subVecs := make([][]float32, sampleCount)
	for i, idx := range sampleIndices {
		subVecs[i] = vectors[idx][subStart:subEnd]
	}

	centroids := make([][]float32, fc.numCentroids)
	for k := range fc.numCentroids {
		centroids[k] = make([]float32, actualSubDim)
		srcIdx := sampleIndices[rand.IntN(sampleCount)]
		copy(centroids[k], vectors[srcIdx][subStart:subEnd])
	}

	assignments := make([]int, sampleCount)
	counts := make([]int, fc.numCentroids)
	sums := make([][]float32, fc.numCentroids)
	for k := range fc.numCentroids {
		sums[k] = make([]float32, actualSubDim)
	}

	numIters := bits.Len(uint(fc.numCentroids))
	for iter := range numIters {
		for i, sv := range subVecs {
			bestK := 0
			bestDist := float32(math.MaxFloat32)
			for k, c := range centroids {
				dist := vek32.Distance(sv, c)
				if dist < bestDist {
					bestDist = dist
					bestK = k
				}
			}
			assignments[i] = bestK
		}

		for k := range fc.numCentroids {
			counts[k] = 0
			for d := range actualSubDim {
				sums[k][d] = 0
			}
		}

		for i, sv := range subVecs {
			k := assignments[i]
			counts[k]++
			for d, v := range sv {
				sums[k][d] += v
			}
		}

		for k := range fc.numCentroids {
			if counts[k] > 0 {
				for d := range actualSubDim {
					centroids[k][d] = sums[k][d] / float32(counts[k])
				}
			} else if iter == 0 {
				srcIdx := rand.IntN(sampleCount)
				copy(centroids[k], subVecs[srcIdx])
			}
		}
	}

	fc.codebooks[subspace] = make([]float32, fc.numCentroids*actualSubDim)
	for k, c := range centroids {
		copy(fc.codebooks[subspace][k*actualSubDim:(k+1)*actualSubDim], c)
	}
}

func (fc *FlashCoder) encodeVectors(vectors [][]float32, numWorkers int) {
	n := len(vectors)
	dim := len(vectors[0])

	chunkSize := (n + numWorkers - 1) / numWorkers
	var wg sync.WaitGroup

	for w := range numWorkers {
		start := w * chunkSize
		end := start + chunkSize
		if end > n {
			end = n
		}
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			for i := start; i < end; i++ {
				fc.encodeVector(vectors[i], fc.codes[i], dim)
			}
		}(start, end)
	}
	wg.Wait()
}

func (fc *FlashCoder) encodeVector(vec []float32, code []uint8, fullDim int) {
	for m := range fc.numSubspaces {
		subStart := m * fc.subspaceDim
		subEnd := subStart + fc.subspaceDim
		if subEnd > fullDim {
			subEnd = fullDim
		}
		actualSubDim := subEnd - subStart
		subVec := vec[subStart:subEnd]

		bestK := uint8(0)
		bestDist := float32(math.MaxFloat32)

		codebook := fc.codebooks[m]
		for k := range fc.numCentroids {
			centroid := codebook[k*actualSubDim : (k+1)*actualSubDim]
			dist := vek32.Distance(subVec, centroid)
			if dist < bestDist {
				bestDist = dist
				bestK = uint8(k)
			}
		}
		code[m] = bestK
	}
}

func (fc *FlashCoder) buildSDT() {
	fc.sdt = make([][]float32, fc.numSubspaces)
	fc.codeMags = make([][]float32, fc.numSubspaces)

	for m := range fc.numSubspaces {
		fc.sdt[m] = make([]float32, fc.numCentroids*fc.numCentroids)
		fc.codeMags[m] = make([]float32, fc.numCentroids)

		subStart := m * fc.subspaceDim
		actualSubDim := fc.subspaceDim
		if m == fc.numSubspaces-1 {
			codebookLen := len(fc.codebooks[m])
			actualSubDim = codebookLen / fc.numCentroids
		}

		codebook := fc.codebooks[m]

		for k := range fc.numCentroids {
			centroid := codebook[k*actualSubDim : (k+1)*actualSubDim]
			fc.codeMags[m][k] = float32(math.Sqrt(float64(vek32.Dot(centroid, centroid))))
		}

		for k1 := range fc.numCentroids {
			c1 := codebook[k1*actualSubDim : (k1+1)*actualSubDim]
			for k2 := range fc.numCentroids {
				c2 := codebook[k2*actualSubDim : (k2+1)*actualSubDim]
				dot := vek32.Dot(c1, c2)
				fc.sdt[m][k1*fc.numCentroids+k2] = dot
			}
		}
		_ = subStart
	}
}

type ADT struct {
	tables   [][]float32
	queryMag float32
}

func (fc *FlashCoder) BuildADT(queryVec []float32) *ADT {
	fullDim := len(queryVec)
	adt := &ADT{
		tables: make([][]float32, fc.numSubspaces),
	}

	var magSum float32
	for m := range fc.numSubspaces {
		subStart := m * fc.subspaceDim
		subEnd := subStart + fc.subspaceDim
		if subEnd > fullDim {
			subEnd = fullDim
		}
		subQ := queryVec[subStart:subEnd]
		magSum += vek32.Dot(subQ, subQ)

		actualSubDim := subEnd - subStart
		codebook := fc.codebooks[m]

		adt.tables[m] = make([]float32, fc.numCentroids)
		for k := range fc.numCentroids {
			centroid := codebook[k*actualSubDim : (k+1)*actualSubDim]
			adt.tables[m][k] = vek32.Dot(subQ, centroid)
		}
	}
	adt.queryMag = float32(math.Sqrt(float64(magSum)))

	return adt
}

func (fc *FlashCoder) ApproxDotADT(adt *ADT, code []uint8) float32 {
	var dot float32
	for m, c := range code {
		dot += adt.tables[m][c]
	}
	return dot
}

func (fc *FlashCoder) ApproxMag(code []uint8) float32 {
	var magSq float32
	for m1, c1 := range code {
		magSq += fc.sdt[m1][int(c1)*fc.numCentroids+int(c1)]

		for m2 := m1 + 1; m2 < len(code); m2++ {
			c2 := code[m2]
			dot1 := fc.codeMags[m1][c1] * fc.codeMags[m2][c2]
			if dot1 > 0 {
			}
		}
	}
	return float32(math.Sqrt(float64(magSq)))
}

func (fc *FlashCoder) ApproxMagFromSDT(code []uint8) float32 {
	var magSq float32
	for m, c := range code {
		magSq += fc.sdt[m][int(c)*fc.numCentroids+int(c)]
	}
	return float32(math.Sqrt(float64(magSq)))
}

func (fc *FlashCoder) ApproxDotSDT(codeA, codeB []uint8) float32 {
	var dot float32
	for m, cA := range codeA {
		cB := codeB[m]
		dot += fc.sdt[m][int(cA)*fc.numCentroids+int(cB)]
	}
	return dot
}

func (fc *FlashCoder) ApproxCosineDistADT(adt *ADT, code []uint8, codeMag float32) float64 {
	if adt.queryMag == 0 || codeMag == 0 {
		return 2.0
	}
	dot := fc.ApproxDotADT(adt, code)
	return 1.0 - float64(dot)/(float64(adt.queryMag)*float64(codeMag))
}

func (fc *FlashCoder) ApproxCosineDistSDT(codeA, codeB []uint8, magA, magB float32) float64 {
	if magA == 0 || magB == 0 {
		return 2.0
	}
	dot := fc.ApproxDotSDT(codeA, codeB)
	return 1.0 - float64(dot)/(float64(magA)*float64(magB))
}

func (fc *FlashCoder) Code(id uint32) []uint8 {
	return fc.codes[id]
}

func (fc *FlashCoder) NumCodes() int {
	return len(fc.codes)
}

type FlashMags struct {
	mags []float32
}

func (fc *FlashCoder) PrecomputeMags() *FlashMags {
	n := len(fc.codes)
	fm := &FlashMags{
		mags: make([]float32, n),
	}
	for i, code := range fc.codes {
		fm.mags[i] = fc.ApproxMagFromSDT(code)
	}
	return fm
}

func (fm *FlashMags) Get(id uint32) float32 {
	return fm.mags[id]
}

type FlashPruneBuffers struct {
	adt     *ADT
	scored  []flashCandidate
	toScore []uint32
}

type flashCandidate struct {
	id   uint32
	dist float64
}

func NewFlashPruneBuffers(maxCandidates int) *FlashPruneBuffers {
	return &FlashPruneBuffers{
		scored:  make([]flashCandidate, maxCandidates),
		toScore: make([]uint32, maxCandidates),
	}
}

func (fc *FlashCoder) RobustPruneFlash(
	p uint32,
	candidates []uint32,
	alpha float64,
	R int,
	vectors [][]float32,
	trueMags []float64,
	flashMags *FlashMags,
	buf *FlashPruneBuffers,
) []uint32 {
	if len(candidates) == 0 {
		return nil
	}

	if len(candidates) <= R {
		result := make([]uint32, len(candidates))
		copy(result, candidates)
		return result
	}

	pCode := fc.codes[p]
	pMag := flashMags.Get(p)

	maxExamine := min(R*2, len(candidates))
	preserveCount := min(R, len(candidates))
	ratio := max(1, len(candidates)/maxExamine)
	totalSamples := maxExamine * bits.Len(uint(ratio))
	secondHopSamples := min(len(candidates)-preserveCount, totalSamples-preserveCount)
	maxScore := preserveCount + max(0, secondHopSamples)

	toScore := candidates
	if len(candidates) > maxScore && secondHopSamples > 0 {
		toScore = buf.toScore[:maxScore]
		copy(toScore, candidates[:preserveCount])
		copy(toScore[preserveCount:], candidates[preserveCount:maxScore])
		for i, c := range candidates[maxScore:] {
			j := rand.IntN(secondHopSamples + i + 1)
			if j < secondHopSamples {
				toScore[preserveCount+j] = c
			}
		}
	}

	scored := buf.scored[:len(toScore)]
	for i, c := range toScore {
		cCode := fc.codes[c]
		cMag := flashMags.Get(c)
		dist := fc.ApproxCosineDistSDT(pCode, cCode, pMag, cMag)
		scored[i] = flashCandidate{id: c, dist: dist}
	}

	for i := 1; i < len(scored); i++ {
		j := i
		for j > 0 && scored[j].dist < scored[j-1].dist {
			scored[j], scored[j-1] = scored[j-1], scored[j]
			j--
		}
	}

	examineCount := min(maxExamine, len(scored))
	maxCheck := bits.Len(uint(R * R))

	selected := make([]uint32, 0, R)
	consecutiveRejections := 0

	for i := range examineCount {
		if len(selected) >= R || consecutiveRejections >= R {
			break
		}

		c := scored[i]
		cCode := fc.codes[c.id]
		cMag := flashMags.Get(c.id)

		keep := true
		checkStart := max(0, len(selected)-maxCheck)
		for j := checkStart; j < len(selected); j++ {
			s := selected[j]
			sCode := fc.codes[s]
			sMag := flashMags.Get(s)

			distCS := fc.ApproxCosineDistSDT(cCode, sCode, cMag, sMag)
			if c.dist > alpha*distCS {
				keep = false
				break
			}
		}

		if keep {
			selected = append(selected, c.id)
			consecutiveRejections = 0
		} else {
			consecutiveRejections++
		}
	}

	return selected
}

func (fc *FlashCoder) rebuildADT(adt *ADT, queryVec []float32) {
	fullDim := len(queryVec)
	var magSum float32

	for m := range fc.numSubspaces {
		subStart := m * fc.subspaceDim
		subEnd := subStart + fc.subspaceDim
		if subEnd > fullDim {
			subEnd = fullDim
		}
		subQ := queryVec[subStart:subEnd]
		magSum += vek32.Dot(subQ, subQ)

		actualSubDim := subEnd - subStart
		codebook := fc.codebooks[m]

		for k := range fc.numCentroids {
			centroid := codebook[k*actualSubDim : (k+1)*actualSubDim]
			adt.tables[m][k] = vek32.Dot(subQ, centroid)
		}
	}
	adt.queryMag = float32(math.Sqrt(float64(magSum)))
}
