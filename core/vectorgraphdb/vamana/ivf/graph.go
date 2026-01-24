package ivf

import (
	"container/heap"
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
	adjacency         [][]uint32
	R                 int
	medoid            uint32
	partitionReps     []uint32
	nodeToPartition   []uint16
	clusteringQuality float64
	idx               *Index
	searchBufPool     sync.Pool
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
	L := deriveL(R)

	beamWidth := logN

	logP := bits.Len(uint(numPartitions))
	bridgeK := logP * logP

	return GraphConfig{
		R:         R,
		L:         L,
		Alpha:     1.0,
		BeamWidth: beamWidth,
		BridgeK:   bridgeK,
	}
}

func maxStableRecall(R int) float64 {
	if R <= 1 {
		return 0.5
	}
	return 1.0 - 1.0/float64(R*R)
}

func deriveL(R int) int {
	targetRecall := maxStableRecall(R)

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

	logN := bits.Len(uint(n))
	defaultK := logN
	L := defaultK * logN * logN * logN

	beamCap := logN * logN * logN
	poolCap := beamCap * cfg.R

	g := &VamanaGraph{
		adjacency: make([][]uint32, n),
		R:         cfg.R,
		idx:       idx,
		searchBufPool: sync.Pool{
			New: func() any {
				return &searchBuf{
					visited:       make([]uint64, visitedWords),
					beam:          make([]graphCandidate, 0, beamCap),
					candidatePool: make([]graphCandidate, 0, poolCap),
					allCandidates: make([]graphCandidate, 0, L),
					exactScored:   make([]exactCandidate, 0, L),
				}
			},
		},
	}

	g.medoid = idx.computeMedoid()
	g.buildLocalGraphsHamming(cfg)

	g.nodeToPartition = make([]uint16, n)
	for p, members := range idx.partitionIDs {
		for _, nodeID := range members {
			g.nodeToPartition[nodeID] = uint16(p)
		}
	}

	g.clusteringQuality = g.computeClusteringQuality()

	if numParts > 1 {
		g.computePartitionReps()

		logP := bits.Len(uint(numParts))
		stitchThreshold := math.Sqrt(float64(logP))

		g.buildOverlayVamana()

		if g.clusteringQuality < stitchThreshold {
			g.stitchAdjacentPartitions(cfg)
		}
	}

	g.ensureConnectivity(cfg)

	return g
}

func (g *VamanaGraph) computeClusteringQuality() float64 {
	idx := g.idx
	n := idx.numVectors
	numParts := len(idx.centroids)
	if n == 0 || numParts < 2 {
		return 1.0
	}

	dim := idx.dim
	flat := idx.vectorsFlat
	norms := idx.vectorNorms

	logN := bits.Len(uint(n))
	sampleSize := logN * logN
	if sampleSize > n {
		sampleSize = n
	}
	step := n / sampleSize
	if step < 1 {
		step = 1
	}

	var sumRatio float64
	var count int

	for i := 0; i < n; i += step {
		vecOff := i * dim
		vec := flat[vecOff : vecOff+dim]
		vecNorm := norms[i]
		if vecNorm == 0 {
			continue
		}

		ownPart := int(g.nodeToPartition[i])
		ownCentroid := idx.centroids[ownPart]
		ownCentroidNorm := idx.centroidNorms[ownPart]
		if ownCentroidNorm == 0 {
			continue
		}

		ownDot := vek32.Dot(vec, ownCentroid)
		ownSim := float64(ownDot) / (vecNorm * ownCentroidNorm)

		bestOtherSim := -2.0
		for p := 0; p < numParts; p++ {
			if p == ownPart {
				continue
			}
			centroid := idx.centroids[p]
			centroidNorm := idx.centroidNorms[p]
			if centroidNorm == 0 {
				continue
			}
			dot := vek32.Dot(vec, centroid)
			sim := float64(dot) / (vecNorm * centroidNorm)
			if sim > bestOtherSim {
				bestOtherSim = sim
			}
		}

		if bestOtherSim > 0 && ownSim > 0 {
			ratio := ownSim / bestOtherSim
			sumRatio += ratio
			count++
		}
	}

	if count == 0 {
		return 1.0
	}

	return sumRatio / float64(count)
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

	logR := bits.Len(uint(localR))
	localL := localR * logR
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

	logN := bits.Len(uint(n))
	staleLimit := logN
	staleCount := 0
	bestDist := startDist

	for w.pq.hasUnexpanded() {
		currentID, currentDist, ok := w.pq.closestUnexpanded()
		if !ok {
			break
		}

		if currentDist < bestDist {
			bestDist = currentDist
			staleCount = 0
		} else {
			staleCount++
			if staleCount >= staleLimit {
				break
			}
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

func (g *VamanaGraph) computePartitionReps() {
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
}

func (g *VamanaGraph) buildOverlayVamana() {
	idx := g.idx
	numParts := len(g.partitionReps)
	if numParts <= 1 {
		return
	}

	dim := idx.dim
	flat := idx.vectorsFlat
	norms := idx.vectorNorms

	logP := bits.Len(uint(numParts))
	R := logP
	L := deriveL(R)

	overlayAdj := make([][]int, numParts)
	for i := range overlayAdj {
		overlayAdj[i] = make([]int, 0, R)
	}

	medoid := g.findOverlayMedoid()

	visited := make([]bool, numParts)
	candidates := make([]overlayCand, 0, L)

	for p := 0; p < numParts; p++ {
		for i := range visited {
			visited[i] = false
		}
		candidates = candidates[:0]

		pRep := g.partitionReps[p]
		pOff := int(pRep) * dim
		pVec := flat[pOff : pOff+dim]
		pNorm := norms[pRep]

		visited[medoid] = true
		medoidRep := g.partitionReps[medoid]
		medoidOff := int(medoidRep) * dim
		medoidVec := flat[medoidOff : medoidOff+dim]
		medoidNorm := norms[medoidRep]
		medoidSim := g.cosineSim(pVec, pNorm, medoidVec, medoidNorm)
		candidates = append(candidates, overlayCand{medoid, medoidSim})

		cursor := 0
		for cursor < len(candidates) && len(candidates) < L {
			current := candidates[cursor]
			cursor++

			for _, nb := range overlayAdj[current.partIdx] {
				if visited[nb] {
					continue
				}
				visited[nb] = true

				nbRep := g.partitionReps[nb]
				nbOff := int(nbRep) * dim
				nbVec := flat[nbOff : nbOff+dim]
				nbNorm := norms[nbRep]
				sim := g.cosineSim(pVec, pNorm, nbVec, nbNorm)

				pos := len(candidates)
				for i, c := range candidates {
					if sim > c.sim {
						pos = i
						break
					}
				}
				if pos < L {
					if len(candidates) < L {
						candidates = append(candidates, overlayCand{})
					}
					copy(candidates[pos+1:], candidates[pos:])
					candidates[pos] = overlayCand{nb, sim}
				}
			}
		}

		visited[p] = true
		writeIdx := 0
		for _, c := range candidates {
			if c.partIdx != p {
				candidates[writeIdx] = c
				writeIdx++
			}
		}
		candidates = candidates[:writeIdx]

		pruned := g.robustPruneOverlay(p, candidates, overlayAdj, R)
		overlayAdj[p] = pruned

		for _, nb := range pruned {
			hasEdge := false
			for _, existing := range overlayAdj[nb] {
				if existing == p {
					hasEdge = true
					break
				}
			}
			if !hasEdge {
				if len(overlayAdj[nb]) < R {
					overlayAdj[nb] = append(overlayAdj[nb], p)
				} else {
					nbRep := g.partitionReps[nb]
					nbOff := int(nbRep) * dim
					nbVec := flat[nbOff : nbOff+dim]
					nbNorm := norms[nbRep]

					nbCandidates := make([]overlayCand, len(overlayAdj[nb])+1)
					for i, existing := range overlayAdj[nb] {
						existingRep := g.partitionReps[existing]
						existingOff := int(existingRep) * dim
						existingVec := flat[existingOff : existingOff+dim]
						existingNorm := norms[existingRep]
						nbCandidates[i] = overlayCand{existing, g.cosineSim(nbVec, nbNorm, existingVec, existingNorm)}
					}
					pRepVec := flat[int(g.partitionReps[p])*dim : int(g.partitionReps[p])*dim+dim]
					pRepNorm := norms[g.partitionReps[p]]
					nbCandidates[len(overlayAdj[nb])] = overlayCand{p, g.cosineSim(nbVec, nbNorm, pRepVec, pRepNorm)}

					overlayAdj[nb] = g.robustPruneOverlay(nb, nbCandidates, overlayAdj, R)
				}
			}
		}
	}

	for p := 0; p < numParts; p++ {
		pRep := g.partitionReps[p]
		for _, nb := range overlayAdj[p] {
			nbRep := g.partitionReps[nb]
			hasEdge := false
			for _, existing := range g.adjacency[pRep] {
				if existing == nbRep {
					hasEdge = true
					break
				}
			}
			if !hasEdge {
				g.adjacency[pRep] = append(g.adjacency[pRep], nbRep)
			}
		}
	}
}

type overlayCand struct {
	partIdx int
	sim     float64
}

func (g *VamanaGraph) stitchAdjacentPartitions(cfg GraphConfig) {
	idx := g.idx
	numParts := len(idx.centroids)
	if numParts <= 1 {
		return
	}

	dim := idx.dim
	flat := idx.vectorsFlat
	norms := idx.vectorNorms
	codeLen := idx.bbqCodeLen
	codes := idx.bbqCodes

	repToPartition := make(map[uint32]int, numParts)
	for p, rep := range g.partitionReps {
		repToPartition[rep] = p
	}

	logP := bits.Len(uint(numParts))
	boundaryNodesPerPartition := logP * logP

	adjacentPartitions := make([][]int, numParts)
	for p := 0; p < numParts; p++ {
		pRep := g.partitionReps[p]
		for _, neighborID := range g.adjacency[pRep] {
			adjP, isRep := repToPartition[neighborID]
			if isRep && adjP != p {
				adjacentPartitions[p] = append(adjacentPartitions[p], adjP)
			}
		}
	}

	boundaryNodes := make([][]uint32, numParts)
	for p := 0; p < numParts; p++ {
		members := idx.partitionIDs[p]
		if len(members) == 0 || len(adjacentPartitions[p]) == 0 {
			continue
		}

		ownCentroid := idx.centroids[p]
		ownCentroidNorm := idx.centroidNorms[p]

		type nodeScore struct {
			nodeID uint32
			ratio  float64
		}
		scores := make([]nodeScore, 0, len(members))

		for _, nodeID := range members {
			off := int(nodeID) * dim
			vec := flat[off : off+dim]
			vecNorm := norms[nodeID]
			if vecNorm == 0 || ownCentroidNorm == 0 {
				continue
			}

			ownDot := vek32.Dot(vec, ownCentroid)
			ownSim := float64(ownDot) / (vecNorm * ownCentroidNorm)

			bestAdjSim := -2.0
			for _, adjP := range adjacentPartitions[p] {
				adjCentroid := idx.centroids[adjP]
				adjCentroidNorm := idx.centroidNorms[adjP]
				if adjCentroidNorm == 0 {
					continue
				}
				adjDot := vek32.Dot(vec, adjCentroid)
				adjSim := float64(adjDot) / (vecNorm * adjCentroidNorm)
				if adjSim > bestAdjSim {
					bestAdjSim = adjSim
				}
			}

			if bestAdjSim > -2.0 && ownSim > 0 {
				ratio := bestAdjSim / ownSim
				scores = append(scores, nodeScore{nodeID, ratio})
			}
		}

		slices.SortFunc(scores, func(a, b nodeScore) int {
			if a.ratio > b.ratio {
				return -1
			}
			if a.ratio < b.ratio {
				return 1
			}
			return 0
		})

		numBoundary := min(boundaryNodesPerPartition, len(scores))
		boundaryNodes[p] = make([]uint32, numBoundary)
		for i := 0; i < numBoundary; i++ {
			boundaryNodes[p][i] = scores[i].nodeID
		}
	}

	edgesPerBoundaryNode := logP
	maxDegree := cfg.R * 2

	for p := 0; p < numParts; p++ {
		for _, adjP := range adjacentPartitions[p] {
			if adjP <= p {
				continue
			}

			pBoundary := boundaryNodes[p]
			adjBoundary := boundaryNodes[adjP]

			for _, pNode := range pBoundary {
				if len(g.adjacency[pNode]) >= maxDegree {
					continue
				}

				pCode := codes[int(pNode)*codeLen : int(pNode)*codeLen+codeLen]

				type targetDist struct {
					nodeID uint32
					dist   int
				}
				targets := make([]targetDist, 0, len(adjBoundary))

				for _, adjNode := range adjBoundary {
					adjCode := codes[int(adjNode)*codeLen : int(adjNode)*codeLen+codeLen]
					dist := HammingDistance(pCode, adjCode)
					targets = append(targets, targetDist{adjNode, dist})
				}

				slices.SortFunc(targets, func(a, b targetDist) int {
					return a.dist - b.dist
				})

				edgesAdded := 0
				for _, t := range targets {
					if edgesAdded >= edgesPerBoundaryNode {
						break
					}
					if len(g.adjacency[pNode]) >= maxDegree {
						break
					}

					hasEdge := false
					for _, existing := range g.adjacency[pNode] {
						if existing == t.nodeID {
							hasEdge = true
							break
						}
					}
					if hasEdge {
						continue
					}

					g.adjacency[pNode] = append(g.adjacency[pNode], t.nodeID)
					if len(g.adjacency[t.nodeID]) < maxDegree {
						g.adjacency[t.nodeID] = append(g.adjacency[t.nodeID], pNode)
					}
					edgesAdded++
				}
			}
		}
	}
}

func (g *VamanaGraph) ensureConnectivity(cfg GraphConfig) {
	n := len(g.adjacency)
	if n == 0 {
		return
	}

	idx := g.idx
	codeLen := idx.bbqCodeLen
	codes := idx.bbqCodes

	visited := make([]bool, n)
	queue := make([]uint32, 0, n)

	for _, rep := range g.partitionReps {
		if !visited[rep] {
			visited[rep] = true
			queue = append(queue, rep)
		}
	}

	if !visited[g.medoid] {
		visited[g.medoid] = true
		queue = append(queue, g.medoid)
	}

	head := 0
	for head < len(queue) {
		current := queue[head]
		head++

		for _, neighbor := range g.adjacency[current] {
			if !visited[neighbor] {
				visited[neighbor] = true
				queue = append(queue, neighbor)
			}
		}
	}

	reachable := queue
	unreachable := make([]uint32, 0)
	for i := 0; i < n; i++ {
		if !visited[i] {
			unreachable = append(unreachable, uint32(i))
		}
	}

	if len(unreachable) == 0 {
		return
	}

	maxDegree := cfg.R * 2

	for _, nodeID := range unreachable {
		nodeCode := codes[int(nodeID)*codeLen : int(nodeID)*codeLen+codeLen]

		bestDist := int(^uint(0) >> 1)
		var bestTarget uint32

		sampleSize := min(len(reachable), 1000)
		step := len(reachable) / sampleSize
		if step < 1 {
			step = 1
		}

		for i := 0; i < len(reachable); i += step {
			targetID := reachable[i]
			targetCode := codes[int(targetID)*codeLen : int(targetID)*codeLen+codeLen]
			dist := HammingDistance(nodeCode, targetCode)
			if dist < bestDist {
				bestDist = dist
				bestTarget = targetID
			}
		}

		if len(g.adjacency[nodeID]) < maxDegree {
			g.adjacency[nodeID] = append(g.adjacency[nodeID], bestTarget)
		}
		if len(g.adjacency[bestTarget]) < maxDegree {
			g.adjacency[bestTarget] = append(g.adjacency[bestTarget], nodeID)
		}

		visited[nodeID] = true
		reachable = append(reachable, nodeID)
	}
}

func (g *VamanaGraph) findOverlayMedoid() int {
	idx := g.idx
	numParts := len(g.partitionReps)
	dim := idx.dim
	flat := idx.vectorsFlat
	norms := idx.vectorNorms

	centroid := make([]float64, dim)
	validCount := 0
	for p := 0; p < numParts; p++ {
		rep := g.partitionReps[p]
		off := int(rep) * dim
		vec := flat[off : off+dim]
		if norms[rep] == 0 {
			continue
		}
		validCount++
		for j, v := range vec {
			centroid[j] += float64(v)
		}
	}

	if validCount == 0 {
		return 0
	}

	invN := 1.0 / float64(validCount)
	centroidF32 := make([]float32, dim)
	for j := range centroid {
		centroidF32[j] = float32(centroid[j] * invN)
	}
	centroidNorm := math.Sqrt(float64(vek32.Dot(centroidF32, centroidF32)))

	bestMedoid := 0
	bestSim := -2.0
	for p := 0; p < numParts; p++ {
		rep := g.partitionReps[p]
		off := int(rep) * dim
		vec := flat[off : off+dim]
		vecNorm := norms[rep]
		if vecNorm == 0 {
			continue
		}
		dot := vek32.Dot(centroidF32, vec)
		sim := float64(dot) / (centroidNorm * vecNorm)
		if sim > bestSim {
			bestSim = sim
			bestMedoid = p
		}
	}
	return bestMedoid
}

func (g *VamanaGraph) cosineSim(v1 []float32, n1 float64, v2 []float32, n2 float64) float64 {
	if n1 == 0 || n2 == 0 {
		return -2.0
	}
	dot := vek32.Dot(v1, v2)
	return float64(dot) / (n1 * n2)
}

func (g *VamanaGraph) robustPruneOverlay(source int, candidates []overlayCand, adj [][]int, R int) []int {
	if len(candidates) == 0 {
		return adj[source]
	}

	for _, nb := range adj[source] {
		found := false
		for _, c := range candidates {
			if c.partIdx == nb {
				found = true
				break
			}
		}
		if !found {
			idx := g.idx
			dim := idx.dim
			flat := idx.vectorsFlat
			norms := idx.vectorNorms

			srcRep := g.partitionReps[source]
			srcOff := int(srcRep) * dim
			srcVec := flat[srcOff : srcOff+dim]
			srcNorm := norms[srcRep]

			nbRep := g.partitionReps[nb]
			nbOff := int(nbRep) * dim
			nbVec := flat[nbOff : nbOff+dim]
			nbNorm := norms[nbRep]

			sim := g.cosineSim(srcVec, srcNorm, nbVec, nbNorm)
			candidates = append(candidates, overlayCand{nb, sim})
		}
	}

	slices.SortFunc(candidates, func(a, b overlayCand) int {
		if a.sim > b.sim {
			return -1
		}
		if a.sim < b.sim {
			return 1
		}
		return 0
	})

	pruned := make([]int, 0, R)
	occluded := make([]bool, len(candidates))

	idx := g.idx
	dim := idx.dim
	flat := idx.vectorsFlat
	norms := idx.vectorNorms

	for i, cand := range candidates {
		if len(pruned) >= R {
			break
		}
		if occluded[i] || cand.partIdx == source {
			continue
		}

		pruned = append(pruned, cand.partIdx)

		candRep := g.partitionReps[cand.partIdx]
		candOff := int(candRep) * dim
		candVec := flat[candOff : candOff+dim]
		candNorm := norms[candRep]

		for j := i + 1; j < len(candidates); j++ {
			if occluded[j] {
				continue
			}
			otherRep := g.partitionReps[candidates[j].partIdx]
			otherOff := int(otherRep) * dim
			otherVec := flat[otherOff : otherOff+dim]
			otherNorm := norms[otherRep]

			simCandOther := g.cosineSim(candVec, candNorm, otherVec, otherNorm)
			if simCandOther >= candidates[j].sim {
				occluded[j] = true
			}
		}
	}

	return pruned
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
	flat := idx.vectorsFlat
	norms := idx.vectorNorms

	if n == 0 || k <= 0 {
		return nil
	}

	queryNorm := math.Sqrt(float64(vek32.Dot(query, query)))
	if queryNorm == 0 {
		return nil
	}

	numParts := len(idx.centroids)
	logK := bits.Len(uint(k))
	if logK < 1 {
		logK = 1
	}
	logP := bits.Len(uint(numParts))

	missRate := 1.0 / float64(k*logK)
	sqrtK := math.Sqrt(float64(k))
	coverageFactor := 1.0 - math.Pow(missRate, 1.0/sqrtK)
	nprobe := int(float64(numParts) * coverageFactor)

	minProbe := k * logP
	if nprobe < minProbe {
		nprobe = minProbe
	}

	if g.clusteringQuality > 0 {
		spreadFactor := 1.0 / g.clusteringQuality
		nprobe = int(float64(nprobe) * spreadFactor)
		if nprobe < minProbe {
			nprobe = minProbe
		}
	}

	if nprobe > numParts {
		nprobe = numParts
	}

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
		scores[p] = partScore{p, float64(dot) / (queryNorm * centroidNorm)}
	}

	for i := 1; i < len(scores); i++ {
		j := i
		for j > 0 && scores[j].sim > scores[j-1].sim {
			scores[j], scores[j-1] = scores[j-1], scores[j]
			j--
		}
	}

	h := &resultHeap{}
	heap.Init(h)

	for pi := 0; pi < nprobe; pi++ {
		p := scores[pi].idx
		ids := idx.partitionIDs[p]
		for _, id := range ids {
			vecStart := int(id) * dim
			vec := flat[vecStart : vecStart+dim]
			vecNorm := norms[id]

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

func (g *VamanaGraph) searchGraphBBQ(queryID uint32, k int, beamWidth int) []uint32 {
	idx := g.idx
	n := idx.numVectors
	if n == 0 || k <= 0 {
		return nil
	}

	codeLen := idx.bbqCodeLen
	codes := idx.bbqCodes
	queryCode := codes[int(queryID)*codeLen : int(queryID)*codeLen+codeLen]

	visited := make(map[uint32]struct{}, beamWidth*4)
	beam := make([]graphCandidate, 0, beamWidth)
	allCandidates := make([]graphCandidate, 0, beamWidth*4)

	startNode := g.medoid
	if g.nodeToPartition != nil && queryID < uint32(len(g.nodeToPartition)) {
		p := g.nodeToPartition[queryID]
		if int(p) < len(g.partitionReps) {
			startNode = g.partitionReps[p]
		}
	}

	visited[startNode] = struct{}{}
	visited[queryID] = struct{}{}
	startCode := codes[int(startNode)*codeLen : int(startNode)*codeLen+codeLen]
	startDist := HammingDistance(queryCode, startCode)
	beam = append(beam, graphCandidate{startNode, int32(startDist)})
	allCandidates = append(allCandidates, graphCandidate{startNode, int32(startDist)})

	logN := bits.Len(uint(n))
	staleLimit := logN
	staleCount := 0
	bestDist := int32(startDist)

	for len(beam) > 0 {
		bestIdx := 0
		for i := 1; i < len(beam); i++ {
			if beam[i].bbqDist < beam[bestIdx].bbqDist {
				bestIdx = i
			}
		}

		current := beam[bestIdx]
		beam[bestIdx] = beam[len(beam)-1]
		beam = beam[:len(beam)-1]

		if current.bbqDist < bestDist {
			bestDist = current.bbqDist
			staleCount = 0
		} else {
			staleCount++
			if staleCount >= staleLimit {
				break
			}
		}

		for _, nbID := range g.adjacency[current.id] {
			if _, seen := visited[nbID]; seen {
				continue
			}
			visited[nbID] = struct{}{}

			nbCode := codes[int(nbID)*codeLen : int(nbID)*codeLen+codeLen]
			nbDist := int32(HammingDistance(queryCode, nbCode))

			candidate := graphCandidate{nbID, nbDist}
			allCandidates = append(allCandidates, candidate)

			if len(beam) < beamWidth {
				beam = append(beam, candidate)
			} else {
				worstIdx := 0
				for i := 1; i < len(beam); i++ {
					if beam[i].bbqDist > beam[worstIdx].bbqDist {
						worstIdx = i
					}
				}
				if nbDist < beam[worstIdx].bbqDist {
					beam[worstIdx] = candidate
				}
			}
		}
	}

	slices.SortFunc(allCandidates, func(a, b graphCandidate) int {
		return int(a.bbqDist) - int(b.bbqDist)
	})

	resultLen := min(k, len(allCandidates))
	result := make([]uint32, resultLen)
	for i := range resultLen {
		result[i] = allCandidates[i].id
	}
	return result
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

func (g *VamanaGraph) ClusteringQuality() float64 {
	return g.clusteringQuality
}
