package ivf

import (
	"math"
	"math/bits"
	"runtime"
	"slices"
	"sync"

	"github.com/viterin/vek/vek32"
)

const (
	lcgMultiplier    = 6364136223846793005
	lcgIncrement     = 1442695040888963407
	bitsPerWord      = 64
	bitsPerWordShift = 6
	bitsPerWordMask  = bitsPerWord - 1
)

type VamanaGraph struct {
	adjacency     [][]uint32
	R             int
	medoid        uint32
	partitionReps []uint32
	idx           *Index
	searchBufPool sync.Pool
}

type GraphConfig struct {
	R         int     // Max out-degree per node
	L         int     // Search list size during graph build
	Alpha     float32 // RobustPrune diversity: 1.0 = greedy nearest, >1.0 = sparser/more diverse (DiskANN default: 1.2)
	BeamWidth int     // Beam width for graph traversal
	BridgeK   int     // Number of cross-partition bridges
}

func ConfigForGraph(n, numPartitions int) GraphConfig {
	if n <= 0 {
		return GraphConfig{}
	}

	logN := bits.Len(uint(n))

	R := logN
	L := deriveL(R, 0.95)

	beamWidth := logN

	bridgeK := bits.Len(uint(numPartitions))

	return GraphConfig{
		R:         R,
		L:         L,
		Alpha:     1.0, // 1.0 optimal for small partitions; DiskANN's 1.2 is for large single-graph builds
		BeamWidth: beamWidth,
		BridgeK:   bridgeK,
	}
}

func deriveL(R int, targetRecall float64) int {
	targetRecall = max(0.0, min(targetRecall, 1.0-1.0/float64(R*R)))

	logR := bits.Len(uint(R))
	if logR < 1 {
		logR = 1
	}

	pathFactor := float64(logR)
	expansionFactor := 1.0 + float64(logR)/float64(R)
	recallBits := -math.Log2(1.0 - targetRecall)

	L := float64(R) * pathFactor * expansionFactor * recallBits / float64(logR)

	maxL := R * logR * logR
	return max(R, min(int(math.Ceil(L)), maxL))
}

type graphBuildWorker struct {
	hammingDists  []int
	indices       []int
	visited       []uint64
	candidates    []vamanaCandidate
	prunedList    []uint32
	reverseBuffer []uint32
	occludeFactor []float32
	rng           uint64
	pq            neighborPQ
	idScratch     []uint32
	distScratch   []int
}

type vamanaCandidate struct {
	id   uint32
	dist int
}

type neighborPQ struct {
	data     []vamanaCandidate
	size     int
	capacity int
	cursor   int
}

func newGraphBuildWorker(maxMembers, R, L int) *graphBuildWorker {
	visitedWords := (maxMembers + bitsPerWordMask) / bitsPerWord
	return &graphBuildWorker{
		hammingDists:  make([]int, maxMembers),
		indices:       make([]int, maxMembers),
		visited:       make([]uint64, visitedWords),
		candidates:    make([]vamanaCandidate, 0, maxMembers),
		prunedList:    make([]uint32, 0, R),
		reverseBuffer: make([]uint32, 0, R+1),
		occludeFactor: make([]float32, 0, maxMembers),
		rng:           uint64(maxMembers) | 1,
		pq: neighborPQ{
			data:     make([]vamanaCandidate, L+1),
			capacity: L,
		},
		idScratch:   make([]uint32, 0, R),
		distScratch: make([]int, 0, R),
	}
}

func (w *graphBuildWorker) clearVisited(n int) {
	words := (n + bitsPerWordMask) / bitsPerWord
	for i := 0; i < words; i++ {
		w.visited[i] = 0
	}
}

func (w *graphBuildWorker) setVisited(localID uint32) {
	w.visited[localID>>bitsPerWordShift] |= 1 << (localID & bitsPerWordMask)
}

func (w *graphBuildWorker) isVisited(localID uint32) bool {
	return w.visited[localID>>bitsPerWordShift]&(1<<(localID&bitsPerWordMask)) != 0
}

func (w *graphBuildWorker) nextRandom(n int) int {
	w.rng = w.rng*lcgMultiplier + lcgIncrement
	return int(w.rng>>33) % n
}

func (pq *neighborPQ) clear() {
	pq.size = 0
	pq.cursor = 0
}

func (pq *neighborPQ) insert(id uint32, dist int) {
	if pq.size == pq.capacity && pq.data[pq.size-1].dist <= dist {
		return
	}

	lo, hi := 0, pq.size
	for lo < hi {
		mid := (lo + hi) >> 1
		if dist < pq.data[mid].dist {
			hi = mid
		} else if pq.data[mid].id == id {
			return
		} else {
			lo = mid + 1
		}
	}

	if lo < pq.capacity {
		copy(pq.data[lo+1:pq.size+1], pq.data[lo:pq.size])
	}
	pq.data[lo] = vamanaCandidate{id: id, dist: dist}
	if pq.size < pq.capacity {
		pq.size++
	}
	if lo < pq.cursor {
		pq.cursor = lo
	}
}

func (pq *neighborPQ) closestUnexpanded() (uint32, int, bool) {
	if pq.cursor >= pq.size {
		return 0, 0, false
	}
	c := pq.data[pq.cursor]
	pq.cursor++
	return c.id, c.dist, true
}

func (pq *neighborPQ) hasUnexpanded() bool {
	return pq.cursor < pq.size
}

func (idx *Index) BuildVamanaGraph() *VamanaGraph {
	n := idx.numVectors
	if n == 0 {
		return nil
	}

	numParts := len(idx.partitionIDs)
	cfg := ConfigForGraph(n, numParts)
	visitedWords := (n + bitsPerWordMask) / bitsPerWord

	g := &VamanaGraph{
		adjacency: make([][]uint32, n),
		R:         cfg.R,
		idx:       idx,
		searchBufPool: sync.Pool{
			New: func() any {
				logN := bits.Len(uint(n))
				L := cfg.BeamWidth * logN
				return &searchBuf{
					visited:       make([]uint64, visitedWords),
					beam:          make([]graphCandidate, 0, cfg.BeamWidth*2),
					candidatePool: make([]graphCandidate, 0, cfg.BeamWidth*cfg.R),
					allCandidates: make([]graphCandidate, 0, L),
					exactScored:   make([]exactCandidate, 0, L),
				}
			},
		},
	}

	g.medoid = idx.computeMedoid()
	g.buildLocalGraphsHamming(cfg)

	if numParts > 1 {
		g.addCrossPartitionBridges(cfg)
	}

	return g
}

func (g *VamanaGraph) buildLocalGraphsHamming(cfg GraphConfig) {
	idx := g.idx
	numParts := len(idx.partitionIDs)
	numWorkers := runtime.GOMAXPROCS(0)

	maxMembersPerPartition := 0
	for p := 0; p < numParts; p++ {
		if len(idx.partitionIDs[p]) > maxMembersPerPartition {
			maxMembersPerPartition = len(idx.partitionIDs[p])
		}
	}

	workers := make([]*graphBuildWorker, numWorkers)
	for w := 0; w < numWorkers; w++ {
		workers[w] = newGraphBuildWorker(maxMembersPerPartition, cfg.R, cfg.L)
	}

	var wg sync.WaitGroup
	partCh := make(chan int, numParts)

	for p := 0; p < numParts; p++ {
		partCh <- p
	}
	close(partCh)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(worker *graphBuildWorker) {
			defer wg.Done()
			for p := range partCh {
				g.buildPartitionGraphHamming(p, cfg, worker)
			}
		}(workers[w])
	}
	wg.Wait()
}

func (g *VamanaGraph) buildPartitionGraphHamming(partIdx int, cfg GraphConfig, w *graphBuildWorker) {
	idx := g.idx
	members := idx.partitionIDs[partIdx]
	n := len(members)

	if n == 0 {
		return
	}

	if n == 1 {
		g.adjacency[members[0]] = nil
		return
	}

	codeLen := idx.bbqCodeLen
	codes := idx.bbqCodes

	localR := cfg.R
	if localR > n-1 {
		localR = n - 1
	}

	localL := cfg.L
	if localL > n-1 {
		localL = n - 1
	}

	localAdj := make([][]uint32, n)
	for i := 0; i < n; i++ {
		localAdj[i] = make([]uint32, 0, localR)
	}

	medoidLocal := g.findLocalMedoid(members, codes, codeLen)

	for localID := 0; localID < n; localID++ {
		candidates := g.greedySearchLocal(localID, medoidLocal, localAdj, members, codes, codeLen, localL, w)

		pruned := g.robustPruneLocal(localID, candidates, localAdj, members, codes, codeLen, cfg.Alpha, localR, w)
		localAdj[localID] = pruned

		g.addReverseEdgesLocal(localID, pruned, localAdj, members, codes, codeLen, cfg.Alpha, localR, w)
	}

	for localID := 0; localID < n; localID++ {
		if len(localAdj[localID]) > localR {
			nbrs := make([]vamanaCandidate, len(localAdj[localID]))
			nodeCode := codes[int(members[localID])*codeLen : int(members[localID])*codeLen+codeLen]
			for i, nb := range localAdj[localID] {
				nbCode := codes[int(members[nb])*codeLen : int(members[nb])*codeLen+codeLen]
				nbrs[i] = vamanaCandidate{nb, HammingDistance(nodeCode, nbCode)}
			}
			localAdj[localID] = g.robustPruneLocal(localID, nbrs, localAdj, members, codes, codeLen, cfg.Alpha, localR, w)
		}
	}

	for i := 0; i < n; i++ {
		g.adjacency[members[i]] = make([]uint32, len(localAdj[i]))
		for j, localNb := range localAdj[i] {
			g.adjacency[members[i]][j] = members[localNb]
		}
	}
}

func (g *VamanaGraph) findLocalMedoid(members []uint32, codes []byte, codeLen int) int {
	n := len(members)
	if n == 0 {
		return 0
	}

	bestMedoid := 0
	bestTotalDist := int(^uint(0) >> 1)

	sampleSize := n
	if sampleSize > 100 {
		sampleSize = 100
	}

	for i := 0; i < sampleSize; i++ {
		totalDist := 0
		iCode := codes[int(members[i])*codeLen : int(members[i])*codeLen+codeLen]
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			jCode := codes[int(members[j])*codeLen : int(members[j])*codeLen+codeLen]
			totalDist += HammingDistance(iCode, jCode)
		}
		if totalDist < bestTotalDist {
			bestTotalDist = totalDist
			bestMedoid = i
		}
	}
	return bestMedoid
}

func (g *VamanaGraph) greedySearchLocal(queryLocal, startLocal int, localAdj [][]uint32, members []uint32, codes []byte, codeLen int, L int, w *graphBuildWorker) []vamanaCandidate {
	n := len(members)
	w.clearVisited(n)
	w.pq.clear()
	w.candidates = w.candidates[:0]

	queryCode := codes[int(members[queryLocal])*codeLen : int(members[queryLocal])*codeLen+codeLen]

	w.setVisited(uint32(startLocal))
	startCode := codes[int(members[startLocal])*codeLen : int(members[startLocal])*codeLen+codeLen]
	startDist := HammingDistance(queryCode, startCode)
	w.pq.insert(uint32(startLocal), startDist)

	for w.pq.hasUnexpanded() {
		currentID, currentDist, ok := w.pq.closestUnexpanded()
		if !ok {
			break
		}

		w.candidates = append(w.candidates, vamanaCandidate{currentID, currentDist})

		w.idScratch = w.idScratch[:0]
		for _, nbLocal := range localAdj[currentID] {
			if !w.isVisited(nbLocal) {
				w.setVisited(nbLocal)
				w.idScratch = append(w.idScratch, nbLocal)
			}
		}

		if len(w.idScratch) == 0 {
			continue
		}

		w.distScratch = w.distScratch[:0]
		for _, nbLocal := range w.idScratch {
			nbCode := codes[int(members[nbLocal])*codeLen : int(members[nbLocal])*codeLen+codeLen]
			w.distScratch = append(w.distScratch, HammingDistance(queryCode, nbCode))
		}

		for i, nbLocal := range w.idScratch {
			w.pq.insert(nbLocal, w.distScratch[i])
		}
	}

	w.setVisited(uint32(queryLocal))

	writeIdx := 0
	for _, c := range w.candidates {
		if c.id != uint32(queryLocal) {
			w.candidates[writeIdx] = c
			writeIdx++
		}
	}
	return w.candidates[:writeIdx]
}

func (g *VamanaGraph) robustPruneLocal(sourceLocal int, candidates []vamanaCandidate, localAdj [][]uint32, members []uint32, codes []byte, codeLen int, alpha float32, R int, w *graphBuildWorker) []uint32 {
	if len(candidates) == 0 {
		return localAdj[sourceLocal]
	}

	for _, nb := range localAdj[sourceLocal] {
		found := false
		for _, c := range candidates {
			if c.id == nb {
				found = true
				break
			}
		}
		if !found {
			nbCode := codes[int(members[nb])*codeLen : int(members[nb])*codeLen+codeLen]
			sourceCode := codes[int(members[sourceLocal])*codeLen : int(members[sourceLocal])*codeLen+codeLen]
			dist := HammingDistance(sourceCode, nbCode)
			candidates = append(candidates, vamanaCandidate{nb, dist})
		}
	}

	slices.SortFunc(candidates, func(a, b vamanaCandidate) int {
		return a.dist - b.dist
	})

	maxCandidates := w.pq.capacity + R
	if len(candidates) > maxCandidates {
		candidates = candidates[:maxCandidates]
	}

	if cap(w.occludeFactor) < len(candidates) {
		w.occludeFactor = make([]float32, len(candidates))
	}
	occludeFactor := w.occludeFactor[:len(candidates)]
	for i := range occludeFactor {
		occludeFactor[i] = 0
	}

	w.prunedList = w.prunedList[:0]

	curAlpha := float32(1.0)
	for curAlpha <= alpha && len(w.prunedList) < R {
		for i, candidate := range candidates {
			if len(w.prunedList) >= R {
				break
			}
			if occludeFactor[i] > curAlpha {
				continue
			}
			if candidate.id == uint32(sourceLocal) {
				occludeFactor[i] = float32(math.MaxFloat32)
				continue
			}

			occludeFactor[i] = float32(math.MaxFloat32)
			w.prunedList = append(w.prunedList, candidate.id)

			candidateCode := codes[int(members[candidate.id])*codeLen : int(members[candidate.id])*codeLen+codeLen]

			for j := i + 1; j < len(candidates); j++ {
				if occludeFactor[j] > alpha {
					continue
				}
				otherCode := codes[int(members[candidates[j].id])*codeLen : int(members[candidates[j].id])*codeLen+codeLen]
				djk := HammingDistance(candidateCode, otherCode)
				if djk == 0 {
					occludeFactor[j] = float32(math.MaxFloat32)
				} else {
					ratio := float32(candidates[j].dist) / float32(djk)
					if ratio > occludeFactor[j] {
						occludeFactor[j] = ratio
					}
				}
			}
		}
		curAlpha *= 1.2
	}

	result := make([]uint32, len(w.prunedList))
	copy(result, w.prunedList)
	return result
}

func (g *VamanaGraph) addReverseEdgesLocal(sourceLocal int, neighbors []uint32, localAdj [][]uint32, members []uint32, codes []byte, codeLen int, alpha float32, R int, w *graphBuildWorker) {
	sourceID := uint32(sourceLocal)

	for _, nbLocal := range neighbors {
		hasEdge := false
		for _, existing := range localAdj[nbLocal] {
			if existing == sourceID {
				hasEdge = true
				break
			}
		}
		if hasEdge {
			continue
		}

		if len(localAdj[nbLocal]) < R {
			localAdj[nbLocal] = append(localAdj[nbLocal], sourceID)
			continue
		}

		w.reverseBuffer = w.reverseBuffer[:0]
		for _, existing := range localAdj[nbLocal] {
			w.reverseBuffer = append(w.reverseBuffer, existing)
		}
		w.reverseBuffer = append(w.reverseBuffer, sourceID)

		nbCode := codes[int(members[nbLocal])*codeLen : int(members[nbLocal])*codeLen+codeLen]
		candidates := make([]vamanaCandidate, len(w.reverseBuffer))
		for i, cand := range w.reverseBuffer {
			candCode := codes[int(members[cand])*codeLen : int(members[cand])*codeLen+codeLen]
			candidates[i] = vamanaCandidate{cand, HammingDistance(nbCode, candCode)}
		}

		pruned := g.robustPruneLocal(int(nbLocal), candidates, localAdj, members, codes, codeLen, alpha, R, w)
		localAdj[nbLocal] = pruned
	}
}

func quickselectTopKIntMin(dist []int, indices []int, n, k int) {
	if k >= n {
		return
	}

	left, right := 0, n-1

	for left < right {
		pivotIdx := left + (right-left)/2
		pivotVal := dist[pivotIdx]

		dist[pivotIdx], dist[right] = dist[right], dist[pivotIdx]
		indices[pivotIdx], indices[right] = indices[right], indices[pivotIdx]

		storeIdx := left
		for i := left; i < right; i++ {
			if dist[i] < pivotVal {
				dist[i], dist[storeIdx] = dist[storeIdx], dist[i]
				indices[i], indices[storeIdx] = indices[storeIdx], indices[i]
				storeIdx++
			}
		}

		dist[storeIdx], dist[right] = dist[right], dist[storeIdx]
		indices[storeIdx], indices[right] = indices[right], indices[storeIdx]

		if storeIdx == k {
			return
		} else if storeIdx < k {
			left = storeIdx + 1
		} else {
			right = storeIdx - 1
		}
	}
}

func (idx *Index) computeMedoid() uint32 {
	n := idx.numVectors
	dim := idx.dim
	flat := idx.vectorsFlat
	norms := idx.vectorNorms

	centroid := make([]float64, dim)
	for i := 0; i < n; i++ {
		off := i * dim
		vec := flat[off : off+dim]
		for j, v := range vec {
			centroid[j] += float64(v)
		}
	}

	invN := 1.0 / float64(n)
	centroidF32 := make([]float32, dim)
	for j := range centroid {
		centroidF32[j] = float32(centroid[j] * invN)
	}
	centroidNorm := math.Sqrt(float64(vek32.Dot(centroidF32, centroidF32)))

	var best uint32
	bestSim := -2.0

	for i := 0; i < n; i++ {
		off := i * dim
		vec := flat[off : off+dim]
		vecNorm := norms[i]
		if vecNorm == 0 {
			continue
		}
		dot := vek32.Dot(centroidF32, vec)
		sim := float64(dot) / (centroidNorm * vecNorm)
		if sim > bestSim {
			bestSim = sim
			best = uint32(i)
		}
	}

	return best
}

func (g *VamanaGraph) addCrossPartitionBridges(cfg GraphConfig) {
	idx := g.idx
	numParts := len(idx.centroids)
	if numParts <= 1 {
		return
	}

	dim := idx.dim
	flat := idx.vectorsFlat
	norms := idx.vectorNorms

	g.partitionReps = make([]uint32, numParts)
	for p := 0; p < numParts; p++ {
		members := idx.partitionIDs[p]
		if len(members) == 0 {
			continue
		}

		centroid := idx.centroids[p]
		centroidNorm := idx.centroidNorms[p]

		var bestNode uint32
		bestSim := -2.0

		for _, nodeID := range members {
			off := int(nodeID) * dim
			vec := flat[off : off+dim]
			vecNorm := norms[nodeID]
			if vecNorm == 0 || centroidNorm == 0 {
				continue
			}
			dot := vek32.Dot(centroid, vec)
			sim := float64(dot) / (centroidNorm * vecNorm)
			if sim > bestSim {
				bestSim = sim
				bestNode = nodeID
			}
		}
		g.partitionReps[p] = bestNode
	}
	partReps := g.partitionReps

	bridgeK := cfg.BridgeK

	type scored struct {
		part int
		sim  float64
	}
	scores := make([]scored, numParts)

	for p := 0; p < numParts; p++ {
		if len(idx.partitionIDs[p]) == 0 {
			continue
		}

		pRep := partReps[p]
		pOff := int(pRep) * dim
		pVec := flat[pOff : pOff+dim]
		pNorm := norms[pRep]

		scoreCount := 0
		for q := 0; q < numParts; q++ {
			if q == p || len(idx.partitionIDs[q]) == 0 {
				continue
			}
			qRep := partReps[q]
			qOff := int(qRep) * dim
			qVec := flat[qOff : qOff+dim]
			qNorm := norms[qRep]

			var sim float64
			if pNorm == 0 || qNorm == 0 {
				sim = -2.0
			} else {
				dot := vek32.Dot(pVec, qVec)
				sim = float64(dot) / (pNorm * qNorm)
			}
			scores[scoreCount] = scored{q, sim}
			scoreCount++
		}

		validScores := scores[:scoreCount]
		slices.SortFunc(validScores, func(a, b scored) int {
			if a.sim > b.sim {
				return -1
			}
			if a.sim < b.sim {
				return 1
			}
			return 0
		})

		bridgeCount := min(bridgeK, scoreCount)
		for i := 0; i < bridgeCount; i++ {
			qRep := partReps[validScores[i].part]

			hasEdge := false
			for _, nb := range g.adjacency[pRep] {
				if nb == qRep {
					hasEdge = true
					break
				}
			}
			if !hasEdge {
				g.adjacency[pRep] = append(g.adjacency[pRep], qRep)
			}

			hasEdge = false
			for _, nb := range g.adjacency[qRep] {
				if nb == pRep {
					hasEdge = true
					break
				}
			}
			if !hasEdge {
				g.adjacency[qRep] = append(g.adjacency[qRep], pRep)
			}
		}
	}
}

type searchBuf struct {
	visited       []uint64
	beam          []graphCandidate
	candidatePool []graphCandidate
	allCandidates []graphCandidate
	exactScored   []exactCandidate
}

type exactCandidate struct {
	id         uint32
	similarity float64
}

type graphCandidate struct {
	id      uint32
	bbqDist int32
}

func bitsetSet(visited []uint64, id uint32) {
	visited[id>>bitsPerWordShift] |= 1 << (id & bitsPerWordMask)
}

func bitsetTest(visited []uint64, id uint32) bool {
	return visited[id>>bitsPerWordShift]&(1<<(id&bitsPerWordMask)) != 0
}

func bitsetClear(visited []uint64) {
	for i := range visited {
		visited[i] = 0
	}
}

func (g *VamanaGraph) BeamSearchBBQ(query []float32, k int, beamWidth int) []SearchResult {
	idx := g.idx
	n := idx.numVectors
	dim := idx.dim
	codeLen := idx.bbqCodeLen
	codes := idx.bbqCodes
	flat := idx.vectorsFlat
	norms := idx.vectorNorms

	if n == 0 || k <= 0 {
		return nil
	}

	queryNorm := math.Sqrt(float64(vek32.Dot(query, query)))
	if queryNorm == 0 {
		return nil
	}

	buf := g.searchBufPool.Get().(*searchBuf)
	defer g.searchBufPool.Put(buf)

	bitsetClear(buf.visited)
	visited := buf.visited

	qEnc := idx.bbq.EncodeQuery(query)

	beam := buf.beam[:0]

	numStartPoints := idx.config.NProbe
	if numStartPoints > len(g.partitionReps) {
		numStartPoints = len(g.partitionReps)
	}

	if numStartPoints > 0 && len(g.partitionReps) > 0 {
		type partSim struct {
			partIdx int
			sim     float64
		}
		partSims := make([]partSim, len(idx.centroids))
		for p := range idx.centroids {
			centroid := idx.centroids[p]
			centroidNorm := idx.centroidNorms[p]
			if centroidNorm == 0 {
				partSims[p] = partSim{p, -2.0}
				continue
			}
			dot := vek32.Dot(query, centroid)
			partSims[p] = partSim{p, float64(dot) / (queryNorm * centroidNorm)}
		}

		slices.SortFunc(partSims, func(a, b partSim) int {
			if a.sim > b.sim {
				return -1
			}
			if a.sim < b.sim {
				return 1
			}
			return 0
		})

		for i := 0; i < numStartPoints; i++ {
			rep := g.partitionReps[partSims[i].partIdx]
			if !bitsetTest(visited, rep) {
				bitsetSet(visited, rep)
				dist := qEnc.Distance(codes[int(rep)*codeLen : (int(rep)+1)*codeLen])
				beam = append(beam, graphCandidate{rep, dist})
			}
		}
	}

	if len(beam) == 0 {
		bitsetSet(visited, g.medoid)
		medoidDist := qEnc.Distance(codes[int(g.medoid)*codeLen : (int(g.medoid)+1)*codeLen])
		beam = append(beam, graphCandidate{g.medoid, medoidDist})
	}

	candidatePool := buf.candidatePool[:0]

	logN := bits.Len(uint(n))
	L := k * numStartPoints * logN

	allCandidates := buf.allCandidates[:0]
	for _, b := range beam {
		allCandidates = append(allCandidates, b)
	}

	for len(beam) > 0 && len(allCandidates) < L {
		candidatePool = candidatePool[:0]

		for _, current := range beam {
			neighbors := g.adjacency[current.id]
			for _, nb := range neighbors {
				if bitsetTest(visited, nb) {
					continue
				}
				bitsetSet(visited, nb)

				nbDist := qEnc.Distance(codes[int(nb)*codeLen : (int(nb)+1)*codeLen])

				candidatePool = append(candidatePool, graphCandidate{nb, nbDist})
				allCandidates = append(allCandidates, graphCandidate{nb, nbDist})
			}
		}

		if len(candidatePool) == 0 {
			break
		}

		slices.SortFunc(candidatePool, func(a, b graphCandidate) int {
			return int(b.bbqDist - a.bbqDist)
		})

		nextBeamSize := min(beamWidth, len(candidatePool))
		beam = beam[:nextBeamSize]
		copy(beam, candidatePool[:nextBeamSize])
	}

	if cap(buf.exactScored) < len(allCandidates) {
		buf.exactScored = make([]exactCandidate, len(allCandidates))
	}
	exactScored := buf.exactScored[:len(allCandidates)]

	for i, c := range allCandidates {
		off := int(c.id) * dim
		vec := flat[off : off+dim]
		vecNorm := norms[c.id]

		var sim float64
		if vecNorm == 0 {
			sim = -2.0
		} else {
			dot := vek32.Dot(query, vec)
			sim = float64(dot) / (queryNorm * vecNorm)
		}
		exactScored[i] = exactCandidate{c.id, sim}
	}

	slices.SortFunc(exactScored, func(a, b exactCandidate) int {
		if a.similarity > b.similarity {
			return -1
		}
		if a.similarity < b.similarity {
			return 1
		}
		return 0
	})

	resultCount := min(k, len(exactScored))
	results := make([]SearchResult, resultCount)
	for i := 0; i < resultCount; i++ {
		results[i] = SearchResult{
			ID:         exactScored[i].id,
			Similarity: exactScored[i].similarity,
		}
	}

	return results
}

type GraphStats struct {
	NumNodes         int
	NumEdges         int
	MinDegree        int
	MaxDegree        int
	AvgDegree        float64
	ZeroDegreeNodes  int
	NumPartitions    int
	NumPartitionReps int
	Medoid           uint32
}

func (g *VamanaGraph) Stats() GraphStats {
	if g == nil {
		return GraphStats{}
	}

	stats := GraphStats{
		NumNodes:         len(g.adjacency),
		MinDegree:        int(^uint(0) >> 1),
		Medoid:           g.medoid,
		NumPartitionReps: len(g.partitionReps),
		NumPartitions:    len(g.idx.partitionIDs),
	}

	for _, neighbors := range g.adjacency {
		deg := len(neighbors)
		stats.NumEdges += deg
		if deg < stats.MinDegree {
			stats.MinDegree = deg
		}
		if deg > stats.MaxDegree {
			stats.MaxDegree = deg
		}
		if deg == 0 {
			stats.ZeroDegreeNodes++
		}
	}

	if stats.NumNodes > 0 {
		stats.AvgDegree = float64(stats.NumEdges) / float64(stats.NumNodes)
	}
	if stats.NumNodes == 0 {
		stats.MinDegree = 0
	}

	return stats
}

func (idx *Index) GraphStats() GraphStats {
	if idx.graph == nil {
		return GraphStats{}
	}
	return idx.graph.Stats()
}
