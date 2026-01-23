package ivf

import (
	"container/heap"
	"math"
	"math/bits"
	"math/rand/v2"
	"runtime"
	"sync"
	"time"

	"github.com/viterin/vek/vek32"
)

const (
	// Knuth MMIX LCG parameters from TAOCP Volume 2, Section 3.3.4
	// These provide full period 2^64 and pass the spectral test
	knuthLCGMultiplier uint64 = 6364136223846793005
	knuthLCGIncrement  uint64 = 1442695040888963407

	// Golden ratio fractional bits (2^32 / phi) for seed mixing
	// phi = (1 + sqrt(5)) / 2, this spreads seed values uniformly
	goldenRatioFrac uint64 = 0x9E3779B9

	// LCG output conversion: use top 31 bits centered around 0
	// Top bits have better randomness properties in LCG
	lcgShift  = 33
	lcgCenter = 1 << 30
)

type Index struct {
	config        Config
	dim           int
	partitionBits int
	projections   [][]float32
	postingLists  []PostingList
	vectorsFlat   []float32
	vectorNorms   []float64
	uint4Mins     []float32
	uint4Scales   []float32
	uint4Flat     []byte
	idToLocation  []idLocation
	numVectors    int

	bbq        *BBQ
	bbqCodes   []byte
	bbqCodeLen int

	centroids     [][]float32
	centroidNorms []float64
	partitionIDs  [][]uint32

	graph *VamanaGraph
}

type idLocation struct {
	partition uint32
	localIdx  uint32
}

func NewIndex(config Config, dim int) *Index {
	partitionBits := bits.Len(uint(config.NumPartitions)) - 1
	if partitionBits < 1 {
		partitionBits = 1
	}

	return &Index{
		config:        config,
		dim:           dim,
		partitionBits: partitionBits,
	}
}

func (idx *Index) Build(vectors [][]float32) (*BuildResult, *BuildStats) {
	stats := &BuildStats{}
	totalStart := time.Now()
	n := len(vectors)
	idx.numVectors = n

	if n == 0 {
		return &BuildResult{}, stats
	}

	idx.dim = len(vectors[0])
	numWorkers := idx.config.NumWorkers
	if numWorkers == 0 {
		numWorkers = runtime.GOMAXPROCS(0)
	}

	t0 := time.Now()
	idx.storeVectorsContiguous(vectors, numWorkers)
	t1 := time.Now()
	idx.buildBBQ(vectors, numWorkers)
	t2 := time.Now()
	idx.buildIVFPartitions(vectors, numWorkers)
	t3 := time.Now()
	idx.graph = idx.BuildVamanaGraph()
	t4 := time.Now()

	stats.TotalNanos = time.Since(totalStart).Nanoseconds()
	stats.StoreNanos = t1.Sub(t0).Nanoseconds()
	stats.BBQNanos = t2.Sub(t1).Nanoseconds()
	stats.PartitionNanos = t3.Sub(t2).Nanoseconds()
	stats.GraphNanos = t4.Sub(t3).Nanoseconds()

	result := &BuildResult{
		NumVectors:       n,
		NumPartitions:    idx.config.NumPartitions,
		AvgPartitionSize: float64(n) / float64(idx.config.NumPartitions),
		MinPartitionSize: n / idx.config.NumPartitions,
		MaxPartitionSize: (n + idx.config.NumPartitions - 1) / idx.config.NumPartitions,
	}
	return result, stats
}

func (idx *Index) buildBBQ(vectors [][]float32, numWorkers int) {
	idx.bbq = NewBBQ(idx.dim)
	idx.bbq.Train(vectors)
	idx.bbqCodeLen = idx.bbq.CodeLen()
	idx.bbqCodes = make([]byte, len(vectors)*idx.bbqCodeLen)

	n := len(vectors)
	chunkSize := (n + numWorkers - 1) / numWorkers
	var wg sync.WaitGroup

	for w := range numWorkers {
		start := w * chunkSize
		end := min(start+chunkSize, n)
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			for i := start; i < end; i++ {
				offset := i * idx.bbqCodeLen
				idx.bbq.EncodeDB(vectors[i], idx.bbqCodes[offset:offset+idx.bbqCodeLen])
			}
		}(start, end)
	}
	wg.Wait()
}

func (idx *Index) buildIVFPartitions(vectors [][]float32, numWorkers int) {
	n := len(vectors)
	k := idx.config.NumPartitions
	dim := idx.dim

	if k > n {
		k = n
	}

	sampleSize := k * bits.Len(uint(k))
	if sampleSize > n {
		sampleSize = n
	}

	perm := rand.Perm(n)

	idx.centroids = make([][]float32, k)
	centroidsFlat := make([]float32, k*dim)
	centroidNormsSq := make([]float64, k)

	for i := range k {
		idx.centroids[i] = make([]float32, dim)
		copy(idx.centroids[i], vectors[perm[i%sampleSize]])
		copy(centroidsFlat[i*dim:], idx.centroids[i])
		centroidNormsSq[i] = float64(vek32.Dot(idx.centroids[i], idx.centroids[i]))
	}

	sample := make([][]float32, sampleSize)
	sampleNormsSq := make([]float64, sampleSize)
	for i := range sampleSize {
		sample[i] = vectors[perm[i]]
		sampleNormsSq[i] = float64(vek32.Dot(sample[i], sample[i]))
	}

	sampleAssign := make([]uint32, sampleSize)
	prevAssign := make([]uint32, sampleSize)

	for iter := 0; ; iter++ {
		copy(prevAssign, sampleAssign)
		idx.assignToCentroidsFast(sample, sampleNormsSq, centroidsFlat, centroidNormsSq, sampleAssign, numWorkers)

		reassignments := 0
		if iter > 0 {
			for i := range sampleAssign {
				if sampleAssign[i] != prevAssign[i] {
					reassignments++
				}
			}
			if reassignments == 0 {
				break
			}
		}

		counts := make([]int, k)
		for _, p := range sampleAssign {
			counts[p]++
		}

		for i := range k * dim {
			centroidsFlat[i] = 0
		}

		for i, vec := range sample {
			p := sampleAssign[i]
			offset := int(p) * dim
			for j, v := range vec {
				centroidsFlat[offset+j] += v
			}
		}

		emptyCount := 0
		for i := range k {
			if counts[i] == 0 {
				emptyCount++
				copy(centroidsFlat[i*dim:], vectors[perm[(iter*k+i)%sampleSize]])
			} else {
				invCount := 1.0 / float32(counts[i])
				for j := range dim {
					centroidsFlat[i*dim+j] *= invCount
				}
			}
			centroidNormsSq[i] = float64(vek32.Dot(centroidsFlat[i*dim:(i+1)*dim], centroidsFlat[i*dim:(i+1)*dim]))
		}

		if emptyCount == 0 && reassignments == 0 && iter > 0 {
			break
		}
	}

	for i := range k {
		copy(idx.centroids[i], centroidsFlat[i*dim:(i+1)*dim])
	}

	idx.centroidNorms = make([]float64, k)
	for i := range k {
		idx.centroidNorms[i] = math.Sqrt(centroidNormsSq[i])
	}

	sqrtK := 1
	for sqrtK*sqrtK < k {
		sqrtK++
	}

	superCentroids := make([]float32, sqrtK*dim)
	superNormsSq := make([]float64, sqrtK)
	centroidToSuper := make([]int, k)

	for i := range sqrtK {
		groupStart := i * (k / sqrtK)
		groupEnd := groupStart + k/sqrtK
		if i == sqrtK-1 {
			groupEnd = k
		}
		for j := range dim {
			var sum float32
			for c := groupStart; c < groupEnd; c++ {
				sum += centroidsFlat[c*dim+j]
			}
			superCentroids[i*dim+j] = sum / float32(groupEnd-groupStart)
		}
		superNormsSq[i] = float64(vek32.Dot(superCentroids[i*dim:(i+1)*dim], superCentroids[i*dim:(i+1)*dim]))
		for c := groupStart; c < groupEnd; c++ {
			centroidToSuper[c] = i
		}
	}

	groupStarts := make([]int, sqrtK)
	groupEnds := make([]int, sqrtK)
	for i := range sqrtK {
		groupStarts[i] = i * (k / sqrtK)
		groupEnds[i] = groupStarts[i] + k/sqrtK
		if i == sqrtK-1 {
			groupEnds[i] = k
		}
	}

	assignments := make([]uint32, n)
	chunkSize := (n + numWorkers - 1) / numWorkers
	var wg sync.WaitGroup

	for w := range numWorkers {
		start := w * chunkSize
		end := min(start+chunkSize, n)
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			superDists := make([]float64, sqrtK)
			for i := start; i < end; i++ {
				vec := vectors[i]
				vecNormSq := float64(vek32.Dot(vec, vec))

				for s := range sqrtK {
					dot := vek32.Dot(vec, superCentroids[s*dim:(s+1)*dim])
					superDists[s] = vecNormSq + superNormsSq[s] - 2*float64(dot)
				}

				best1, best2 := 0, 1
				if superDists[1] < superDists[0] {
					best1, best2 = 1, 0
				}
				for s := 2; s < sqrtK; s++ {
					if superDists[s] < superDists[best1] {
						best2 = best1
						best1 = s
					} else if superDists[s] < superDists[best2] {
						best2 = s
					}
				}

				bestCentroid := uint32(groupStarts[best1])
				bestDist := math.MaxFloat64

				for c := groupStarts[best1]; c < groupEnds[best1]; c++ {
					dot := vek32.Dot(vec, centroidsFlat[c*dim:(c+1)*dim])
					d := vecNormSq + centroidNormsSq[c] - 2*float64(dot)
					if d < bestDist {
						bestDist = d
						bestCentroid = uint32(c)
					}
				}
				for c := groupStarts[best2]; c < groupEnds[best2]; c++ {
					dot := vek32.Dot(vec, centroidsFlat[c*dim:(c+1)*dim])
					d := vecNormSq + centroidNormsSq[c] - 2*float64(dot)
					if d < bestDist {
						bestDist = d
						bestCentroid = uint32(c)
					}
				}
				assignments[i] = bestCentroid
			}
		}(start, end)
	}
	wg.Wait()

	idx.partitionIDs = make([][]uint32, k)
	for p := range k {
		idx.partitionIDs[p] = make([]uint32, 0, n/k+1)
	}
	for i, p := range assignments {
		idx.partitionIDs[p] = append(idx.partitionIDs[p], uint32(i))
	}
}

func (idx *Index) assignToCentroidsFast(vectors [][]float32, vectorNormsSq []float64, centroidsFlat []float32, centroidNormsSq []float64, assignments []uint32, numWorkers int) {
	n := len(vectors)
	k := len(centroidNormsSq)
	dim := idx.dim

	chunkSize := (n + numWorkers - 1) / numWorkers
	var wg sync.WaitGroup

	for w := range numWorkers {
		start := w * chunkSize
		end := min(start+chunkSize, n)
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			for i := start; i < end; i++ {
				vec := vectors[i]
				vecNormSq := vectorNormsSq[i]
				bestIdx := uint32(0)
				bestDist := math.MaxFloat64

				for j := range k {
					centroid := centroidsFlat[j*dim : (j+1)*dim]
					dot := vek32.Dot(vec, centroid)
					d := vecNormSq + centroidNormsSq[j] - 2*float64(dot)
					if d < bestDist {
						bestDist = d
						bestIdx = uint32(j)
					}
				}
				assignments[i] = bestIdx
			}
		}(start, end)
	}
	wg.Wait()
}

func (idx *Index) storeVectorsContiguous(vectors [][]float32, numWorkers int) {
	n := len(vectors)
	dim := idx.dim
	idx.vectorsFlat = make([]float32, n*dim)
	idx.vectorNorms = make([]float64, n)

	chunkSize := (n + numWorkers - 1) / numWorkers
	var wg sync.WaitGroup

	for w := range numWorkers {
		start := w * chunkSize
		end := min(start+chunkSize, n)
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			for i := start; i < end; i++ {
				offset := i * dim
				copy(idx.vectorsFlat[offset:offset+dim], vectors[i])
				idx.vectorNorms[i] = math.Sqrt(float64(vek32.Dot(vectors[i], vectors[i])))
			}
		}(start, end)
	}
	wg.Wait()
}

func (idx *Index) initProjections() {
	idx.projections = make([][]float32, idx.partitionBits)
	for i := range idx.partitionBits {
		proj := make([]float32, idx.dim)
		seed := uint64(i+1) * goldenRatioFrac
		for j := range idx.dim {
			seed = seed*knuthLCGMultiplier + knuthLCGIncrement
			centered := int64(seed>>lcgShift) - lcgCenter
			proj[j] = float32(centered) / float32(lcgCenter)
		}
		scale := float32(1.0 / math.Sqrt(float64(idx.dim)))
		vek32.MulNumber_Inplace(proj, scale)
		idx.projections[i] = proj
	}
}

func (idx *Index) computePartitionID(vec []float32) uint32 {
	var id uint32
	for i, proj := range idx.projections {
		dot := vek32.Dot(vec, proj)
		if dot >= 0 {
			id |= 1 << i
		}
	}
	return id
}

func (idx *Index) assignPartitions(vectors [][]float32, numWorkers int) [][]uint32 {
	n := len(vectors)
	numParts := idx.config.NumPartitions

	idx.postingLists = make([]PostingList, numParts)
	idx.idToLocation = make([]idLocation, n)
	partitionMembers := make([][]uint32, numParts)

	partitionSize := (n + numParts - 1) / numParts
	for p := range numParts {
		partitionMembers[p] = make([]uint32, 0, partitionSize)
	}

	for i := range n {
		p := uint32(i % numParts)
		idx.idToLocation[i] = idLocation{
			partition: p,
			localIdx:  uint32(len(partitionMembers[p])),
		}
		partitionMembers[p] = append(partitionMembers[p], uint32(i))
	}

	for p := range numParts {
		idx.postingLists[p] = PostingList{
			IDs:         partitionMembers[p],
			BinaryCodes: make([]uint64, len(partitionMembers[p])),
		}
	}

	return partitionMembers
}

func (idx *Index) encodeBinary(vectors [][]float32, partitionMembers [][]uint32, numWorkers int) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, numWorkers)

	for p, members := range partitionMembers {
		if len(members) == 0 {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(p int, members []uint32) {
			defer wg.Done()
			defer func() { <-sem }()

			for i, id := range members {
				vec := vectors[id]
				var code uint64
				bitsToEncode := min(64, idx.dim)
				for b := range bitsToEncode {
					if vec[b] >= 0 {
						code |= 1 << b
					}
				}
				idx.postingLists[p].BinaryCodes[i] = code
			}
		}(p, members)
	}
	wg.Wait()
}

func (idx *Index) computeUint4Params(vectors [][]float32, numWorkers int) {
	n := len(vectors)
	dim := idx.dim
	idx.uint4Mins = make([]float32, dim)
	idx.uint4Scales = make([]float32, dim)

	type minMax struct {
		mins []float32
		maxs []float32
	}

	results := make([]minMax, numWorkers)
	chunkSize := (n + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup
	for w := range numWorkers {
		start := w * chunkSize
		end := min(start+chunkSize, n)
		if start >= end {
			continue
		}

		results[w] = minMax{
			mins: make([]float32, dim),
			maxs: make([]float32, dim),
		}
		for d := range dim {
			results[w].mins[d] = math.MaxFloat32
			results[w].maxs[d] = -math.MaxFloat32
		}

		wg.Add(1)
		go func(w, start, end int) {
			defer wg.Done()
			localMins := results[w].mins
			localMaxs := results[w].maxs

			for i := start; i < end; i++ {
				vec := vectors[i]
				for d, v := range vec {
					if v < localMins[d] {
						localMins[d] = v
					}
					if v > localMaxs[d] {
						localMaxs[d] = v
					}
				}
			}
		}(w, start, end)
	}
	wg.Wait()

	for d := range dim {
		idx.uint4Mins[d] = math.MaxFloat32
		idx.uint4Scales[d] = -math.MaxFloat32
	}
	for _, r := range results {
		if r.mins == nil {
			continue
		}
		for d := range dim {
			if r.mins[d] < idx.uint4Mins[d] {
				idx.uint4Mins[d] = r.mins[d]
			}
			if r.maxs[d] > idx.uint4Scales[d] {
				idx.uint4Scales[d] = r.maxs[d]
			}
		}
	}

	quantMax := float32(idx.config.QuantMax())
	for d := range dim {
		rng := idx.uint4Scales[d] - idx.uint4Mins[d]
		if rng > 0 {
			idx.uint4Scales[d] = quantMax / rng
		} else {
			idx.uint4Scales[d] = 0
		}
	}
}

func (idx *Index) encodeUint4Flat(vectors [][]float32, numWorkers int) {
	n := len(vectors)
	dim := idx.dim
	codeLen := idx.config.CodeLen(dim)
	valsPerByte := idx.config.ValsPerByte()
	quantBits := idx.config.QuantBits
	quantMax := uint8(idx.config.QuantMax())

	idx.uint4Flat = make([]byte, n*codeLen)

	chunkSize := (n + numWorkers - 1) / numWorkers
	var wg sync.WaitGroup

	for w := range numWorkers {
		start := w * chunkSize
		end := min(start+chunkSize, n)
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			for i := start; i < end; i++ {
				vec := vectors[i]
				offset := i * codeLen

				for d := 0; d < dim; d += valsPerByte {
					var packed byte
					for v := range valsPerByte {
						if d+v >= dim {
							break
						}
						q := uint8((vec[d+v] - idx.uint4Mins[d+v]) * idx.uint4Scales[d+v])
						if q > quantMax {
							q = quantMax
						}
						packed |= q << (v * quantBits)
					}
					idx.uint4Flat[offset+d/valsPerByte] = packed
				}
			}
		}(start, end)
	}
	wg.Wait()
}

func (idx *Index) computeBuildResult(partitionMembers [][]uint32) *BuildResult {
	result := &BuildResult{
		NumVectors:    idx.numVectors,
		NumPartitions: idx.config.NumPartitions,
	}

	if idx.numVectors == 0 {
		return result
	}

	result.MinPartitionSize = idx.numVectors
	var total int64
	for _, members := range partitionMembers {
		size := len(members)
		total += int64(size)
		if size > result.MaxPartitionSize {
			result.MaxPartitionSize = size
		}
		if size > 0 && size < result.MinPartitionSize {
			result.MinPartitionSize = size
		}
	}

	result.AvgPartitionSize = float64(total) / float64(idx.config.NumPartitions)
	return result
}

func (idx *Index) Search(query []float32, k int) []SearchResult {
	if idx.numVectors == 0 || k <= 0 {
		return nil
	}

	queryNorm := math.Sqrt(float64(vek32.Dot(query, query)))
	if queryNorm == 0 {
		return nil
	}

	return idx.parallelExactSearch(query, queryNorm, k)
}

func (idx *Index) SearchBBQ(query []float32, k int) []SearchResult {
	if idx.numVectors == 0 || k <= 0 || idx.bbq == nil {
		return nil
	}

	queryNorm := math.Sqrt(float64(vek32.Dot(query, query)))
	if queryNorm == 0 {
		return nil
	}

	return idx.parallelBBQSearch(query, queryNorm, k)
}

func (idx *Index) parallelBBQSearch(query []float32, queryNorm float64, k int) []SearchResult {
	numWorkers := runtime.GOMAXPROCS(0)

	oversample := bits.Len(uint(idx.numVectors))
	candidateCount := k * oversample
	if candidateCount > idx.numVectors {
		candidateCount = idx.numVectors
	}

	qEnc := idx.bbq.EncodeQuery(query)
	workerKeep := candidateCount

	type candidate struct {
		id   uint32
		dist int32
	}

	type workerResult struct {
		candidates []candidate
	}

	results := make([]workerResult, numWorkers)
	chunkSize := (idx.numVectors + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup
	for w := range numWorkers {
		start := w * chunkSize
		end := min(start+chunkSize, idx.numVectors)
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(w, start, end int) {
			defer wg.Done()

			localCandidates := make([]candidate, 0, workerKeep)
			minDist := int32(math.MinInt32)

			codeLen := idx.bbqCodeLen
			for i := start; i < end; i++ {
				offset := i * codeLen
				code := idx.bbqCodes[offset : offset+codeLen]
				dist := qEnc.Distance(code)

				if len(localCandidates) < workerKeep {
					localCandidates = append(localCandidates, candidate{uint32(i), dist})
					if dist < minDist || len(localCandidates) == 1 {
						minDist = dist
					}
				} else if dist > minDist {
					minIdx := 0
					for j, c := range localCandidates {
						if c.dist < localCandidates[minIdx].dist {
							minIdx = j
						}
					}
					localCandidates[minIdx] = candidate{uint32(i), dist}
					minDist = localCandidates[0].dist
					for _, c := range localCandidates {
						if c.dist < minDist {
							minDist = c.dist
						}
					}
				}
			}

			results[w] = workerResult{candidates: localCandidates}
		}(w, start, end)
	}
	wg.Wait()

	allCandidates := make([]candidate, 0, numWorkers*workerKeep)
	for _, r := range results {
		allCandidates = append(allCandidates, r.candidates...)
	}

	for i := 1; i < len(allCandidates); i++ {
		j := i
		for j > 0 && allCandidates[j].dist > allCandidates[j-1].dist {
			allCandidates[j], allCandidates[j-1] = allCandidates[j-1], allCandidates[j]
			j--
		}
	}

	if len(allCandidates) > candidateCount {
		allCandidates = allCandidates[:candidateCount]
	}

	dim := idx.dim
	h := &resultHeap{}
	heap.Init(h)

	for _, c := range allCandidates {
		vecStart := int(c.id) * dim
		vec := idx.vectorsFlat[vecStart : vecStart+dim]
		vecNorm := idx.vectorNorms[c.id]

		if vecNorm == 0 {
			continue
		}

		dot := vek32.Dot(query, vec)
		similarity := float64(dot) / (queryNorm * vecNorm)

		if h.Len() < k {
			heap.Push(h, SearchResult{c.id, similarity})
		} else if similarity > (*h)[0].Similarity {
			(*h)[0] = SearchResult{c.id, similarity}
			heap.Fix(h, 0)
		}
	}

	result := make([]SearchResult, h.Len())
	for i := h.Len() - 1; i >= 0; i-- {
		result[i] = heap.Pop(h).(SearchResult)
	}
	return result
}

func (idx *Index) SearchIVF(query []float32, k int) []SearchResult {
	if idx.numVectors == 0 || k <= 0 || idx.centroids == nil {
		return nil
	}

	queryNorm := math.Sqrt(float64(vek32.Dot(query, query)))
	if queryNorm == 0 {
		return nil
	}

	topPartitions := idx.findTopPartitions(query, queryNorm)
	return idx.searchPartitions(query, queryNorm, topPartitions, k)
}

func (idx *Index) SearchVamana(query []float32, k int) []SearchResult {
	if idx.graph == nil {
		return idx.SearchIVF(query, k)
	}
	logN := bits.Len(uint(idx.numVectors))
	beamWidth := logN * logN
	return idx.graph.BeamSearchBBQ(query, k, beamWidth)
}

func (idx *Index) findTopPartitions(query []float32, queryNorm float64) []int {
	nprobe := idx.config.NProbe
	numParts := len(idx.centroids)

	type partScore struct {
		idx int
		sim float64
	}

	scores := make([]partScore, numParts)
	for p := range numParts {
		centroid := idx.centroids[p]
		centroidNorm := idx.centroidNorms[p]
		if centroidNorm == 0 {
			scores[p] = partScore{p, -1}
			continue
		}
		dot := vek32.Dot(query, centroid)
		sim := float64(dot) / (queryNorm * centroidNorm)
		scores[p] = partScore{p, sim}
	}

	for i := 1; i < len(scores); i++ {
		j := i
		for j > 0 && scores[j].sim > scores[j-1].sim {
			scores[j], scores[j-1] = scores[j-1], scores[j]
			j--
		}
	}

	topN := min(nprobe, numParts)
	result := make([]int, topN)
	for i := range topN {
		result[i] = scores[i].idx
	}
	return result
}

func (idx *Index) searchPartitions(query []float32, queryNorm float64, partitions []int, k int) []SearchResult {
	dim := idx.dim
	h := &resultHeap{}
	heap.Init(h)

	for _, p := range partitions {
		ids := idx.partitionIDs[p]
		for _, id := range ids {
			vecStart := int(id) * dim
			vec := idx.vectorsFlat[vecStart : vecStart+dim]
			vecNorm := idx.vectorNorms[id]

			if vecNorm == 0 {
				continue
			}

			dot := vek32.Dot(query, vec)
			similarity := float64(dot) / (queryNorm * vecNorm)

			if h.Len() < k {
				heap.Push(h, SearchResult{id, similarity})
			} else if similarity > (*h)[0].Similarity {
				(*h)[0] = SearchResult{id, similarity}
				heap.Fix(h, 0)
			}
		}
	}

	result := make([]SearchResult, h.Len())
	for i := h.Len() - 1; i >= 0; i-- {
		result[i] = heap.Pop(h).(SearchResult)
	}
	return result
}

func (idx *Index) exactSearch(query []float32, queryNorm float64, k int) []SearchResult {
	dim := idx.dim
	h := &resultHeap{}
	heap.Init(h)

	for i := range idx.numVectors {
		vecStart := i * dim
		vec := idx.vectorsFlat[vecStart : vecStart+dim]
		vecNorm := idx.vectorNorms[i]

		if vecNorm == 0 {
			continue
		}

		dot := vek32.Dot(query, vec)
		similarity := float64(dot) / (queryNorm * vecNorm)

		if h.Len() < k {
			heap.Push(h, SearchResult{uint32(i), similarity})
		} else if similarity > (*h)[0].Similarity {
			(*h)[0] = SearchResult{uint32(i), similarity}
			heap.Fix(h, 0)
		}
	}

	result := make([]SearchResult, h.Len())
	for i := h.Len() - 1; i >= 0; i-- {
		result[i] = heap.Pop(h).(SearchResult)
	}
	return result
}

func (idx *Index) parallelExactSearch(query []float32, queryNorm float64, k int) []SearchResult {
	numWorkers := runtime.GOMAXPROCS(0)
	dim := idx.dim

	// Per-worker keep count: k * numWorkers ensures global top-k survives merge
	// Worst case: all top-k in one worker's chunk requires that worker keeps k
	// With variance across workers, k * W guarantees capture with high probability
	workerKeep := k * numWorkers

	type workerResult struct {
		results []SearchResult
	}

	results := make([]workerResult, numWorkers)
	chunkSize := (idx.numVectors + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup
	for w := range numWorkers {
		start := w * chunkSize
		end := min(start+chunkSize, idx.numVectors)
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(w, start, end, localKeep int) {
			defer wg.Done()
			h := &resultHeap{}
			heap.Init(h)

			for i := start; i < end; i++ {
				vecStart := i * dim
				vec := idx.vectorsFlat[vecStart : vecStart+dim]
				vecNorm := idx.vectorNorms[i]

				if vecNorm == 0 {
					continue
				}

				dot := vek32.Dot(query, vec)
				similarity := float64(dot) / (queryNorm * vecNorm)

				if h.Len() < localKeep {
					heap.Push(h, SearchResult{uint32(i), similarity})
				} else if similarity > (*h)[0].Similarity {
					(*h)[0] = SearchResult{uint32(i), similarity}
					heap.Fix(h, 0)
				}
			}

			topK := make([]SearchResult, h.Len())
			for i := h.Len() - 1; i >= 0; i-- {
				topK[i] = heap.Pop(h).(SearchResult)
			}
			results[w] = workerResult{results: topK}
		}(w, start, end, workerKeep)
	}
	wg.Wait()

	finalHeap := &resultHeap{}
	heap.Init(finalHeap)
	for _, r := range results {
		for _, s := range r.results {
			if finalHeap.Len() < k {
				heap.Push(finalHeap, s)
			} else if s.Similarity > (*finalHeap)[0].Similarity {
				(*finalHeap)[0] = s
				heap.Fix(finalHeap, 0)
			}
		}
	}

	result := make([]SearchResult, finalHeap.Len())
	for i := finalHeap.Len() - 1; i >= 0; i-- {
		result[i] = heap.Pop(finalHeap).(SearchResult)
	}
	return result
}

func (idx *Index) computeBinaryCode(vec []float32) uint64 {
	var code uint64
	bitsToEncode := min(64, idx.dim)
	for b := range bitsToEncode {
		if vec[b] >= 0 {
			code |= 1 << b
		}
	}
	return code
}

func (idx *Index) computeUint4Code(vec []float32) []byte {
	dim := idx.dim
	codeLen := idx.config.CodeLen(dim)
	valsPerByte := idx.config.ValsPerByte()
	quantBits := idx.config.QuantBits
	quantMax := uint8(idx.config.QuantMax())

	code := make([]byte, codeLen)

	for d := 0; d < dim; d += valsPerByte {
		var packed byte
		for v := range valsPerByte {
			if d+v >= dim {
				break
			}
			q := uint8((vec[d+v] - idx.uint4Mins[d+v]) * idx.uint4Scales[d+v])
			if q > quantMax {
				q = quantMax
			}
			packed |= q << (v * quantBits)
		}
		code[d/valsPerByte] = packed
	}
	return code
}

func (idx *Index) selectPartitions(queryPartition uint32) []uint32 {
	nprobe := idx.config.NProbe
	numParts := idx.config.NumPartitions

	if nprobe >= numParts {
		result := make([]uint32, numParts)
		for i := range numParts {
			result[i] = uint32(i)
		}
		return result
	}

	type partDist struct {
		id   uint32
		dist int
	}

	candidates := make([]partDist, numParts)
	for i := range numParts {
		hamming := bits.OnesCount32(queryPartition ^ uint32(i))
		candidates[i] = partDist{uint32(i), hamming}
	}

	for i := range nprobe {
		minIdx := i
		for j := i + 1; j < numParts; j++ {
			if candidates[j].dist < candidates[minIdx].dist {
				minIdx = j
			}
		}
		candidates[i], candidates[minIdx] = candidates[minIdx], candidates[i]
	}

	result := make([]uint32, nprobe)
	for i := range nprobe {
		result[i] = candidates[i].id
	}
	return result
}

func (idx *Index) binaryFilter(partitions []uint32, queryBinary uint64, targetSize int) []uint32 {
	h := &binaryHeap{}
	heap.Init(h)

	for _, p := range partitions {
		posting := &idx.postingLists[p]
		for i, id := range posting.IDs {
			code := posting.BinaryCodes[i]
			dist := bits.OnesCount64(queryBinary ^ code)

			if h.Len() < targetSize {
				heap.Push(h, binaryScored{id, dist})
			} else if dist < (*h)[0].dist {
				(*h)[0] = binaryScored{id, dist}
				heap.Fix(h, 0)
			}
		}
	}

	result := make([]uint32, h.Len())
	for i := h.Len() - 1; i >= 0; i-- {
		result[i] = heap.Pop(h).(binaryScored).id
	}
	return result
}

func (idx *Index) uint4ScorePartitions(partitions []uint32, queryCode []byte, targetSize int) []uint32 {
	codeLen := idx.config.CodeLen(idx.dim)
	numWorkers := runtime.GOMAXPROCS(0)

	type partResult struct {
		scores []uint4Scored
	}

	results := make([]partResult, len(partitions))
	var wg sync.WaitGroup

	partCh := make(chan int, len(partitions))
	for i := range partitions {
		partCh <- i
	}
	close(partCh)

	for range min(numWorkers, len(partitions)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localHeap := &uint4Heap{}

			for pi := range partCh {
				p := partitions[pi]
				posting := &idx.postingLists[p]
				heap.Init(localHeap)

				for _, id := range posting.IDs {
					offset := int(id) * codeLen
					code := idx.uint4Flat[offset : offset+codeLen]
					score := idx.uint4DotProduct(queryCode, code)

					if localHeap.Len() < targetSize {
						heap.Push(localHeap, uint4Scored{id, score})
					} else if score > (*localHeap)[0].score {
						(*localHeap)[0] = uint4Scored{id, score}
						heap.Fix(localHeap, 0)
					}
				}

				topK := make([]uint4Scored, localHeap.Len())
				for i := localHeap.Len() - 1; i >= 0; i-- {
					topK[i] = heap.Pop(localHeap).(uint4Scored)
				}
				results[pi] = partResult{scores: topK}
			}
		}()
	}
	wg.Wait()

	finalHeap := &uint4Heap{}
	heap.Init(finalHeap)
	for _, r := range results {
		for _, s := range r.scores {
			if finalHeap.Len() < targetSize {
				heap.Push(finalHeap, s)
			} else if s.score > (*finalHeap)[0].score {
				(*finalHeap)[0] = s
				heap.Fix(finalHeap, 0)
			}
		}
	}

	result := make([]uint32, finalHeap.Len())
	for i := finalHeap.Len() - 1; i >= 0; i-- {
		result[i] = heap.Pop(finalHeap).(uint4Scored).id
	}
	return result
}

func (idx *Index) uint4DotProduct(a, b []byte) int {
	valsPerByte := idx.config.ValsPerByte()
	quantBits := idx.config.QuantBits
	mask := byte((1 << quantBits) - 1)

	var sum int
	for i := range a {
		for v := range valsPerByte {
			shift := v * quantBits
			av := int((a[i] >> shift) & mask)
			bv := int((b[i] >> shift) & mask)
			sum += av * bv
		}
	}
	return sum
}

func (idx *Index) exactRerank(candidates []uint32, query []float32, queryNorm float32, k int) []SearchResult {
	dim := idx.dim

	h := &resultHeap{}
	heap.Init(h)

	for _, id := range candidates {
		vecStart := int(id) * dim
		vec := idx.vectorsFlat[vecStart : vecStart+dim]
		vecNorm := idx.vectorNorms[id]

		if vecNorm == 0 {
			continue
		}

		dot := vek32.Dot(query, vec)
		similarity := float64(dot) / (float64(queryNorm) * float64(vecNorm))

		if h.Len() < k {
			heap.Push(h, SearchResult{id, similarity})
		} else if similarity > (*h)[0].Similarity {
			(*h)[0] = SearchResult{id, similarity}
			heap.Fix(h, 0)
		}
	}

	n := h.Len()
	results := make([]SearchResult, n)
	for i := n - 1; i >= 0; i-- {
		results[i] = heap.Pop(h).(SearchResult)
	}
	return results
}

type binaryScored struct {
	id   uint32
	dist int
}

type binaryHeap []binaryScored

func (h binaryHeap) Len() int           { return len(h) }
func (h binaryHeap) Less(i, j int) bool { return h[i].dist > h[j].dist }
func (h binaryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *binaryHeap) Push(x any)        { *h = append(*h, x.(binaryScored)) }
func (h *binaryHeap) Pop() any          { old := *h; x := old[len(old)-1]; *h = old[:len(old)-1]; return x }

type uint4Scored struct {
	id    uint32
	score int
}

type uint4Heap []uint4Scored

func (h uint4Heap) Len() int           { return len(h) }
func (h uint4Heap) Less(i, j int) bool { return h[i].score < h[j].score }
func (h uint4Heap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *uint4Heap) Push(x any)        { *h = append(*h, x.(uint4Scored)) }
func (h *uint4Heap) Pop() any          { old := *h; x := old[len(old)-1]; *h = old[:len(old)-1]; return x }

type resultHeap []SearchResult

func (h resultHeap) Len() int { return len(h) }
func (h resultHeap) Less(i, j int) bool {
	if h[i].Similarity != h[j].Similarity {
		return h[i].Similarity < h[j].Similarity
	}
	return h[i].ID > h[j].ID
}
func (h resultHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *resultHeap) Push(x any)   { *h = append(*h, x.(SearchResult)) }
func (h *resultHeap) Pop() any     { old := *h; x := old[len(old)-1]; *h = old[:len(old)-1]; return x }

func (idx *Index) PartitionIDs() [][]uint32 { return idx.partitionIDs }

func (idx *Index) GraphAdjacency() [][]uint32 {
	if idx.graph == nil {
		return nil
	}
	return idx.graph.adjacency
}
