package quantization

import (
	"sync"
)

type QueryAdaptiveDistance struct {
	config      QueryAdaptiveConfig
	pq          *ProductQuantizer
	profilePool sync.Pool
}

func NewQueryAdaptiveDistance(config QueryAdaptiveConfig, pq *ProductQuantizer) (*QueryAdaptiveDistance, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	qad := &QueryAdaptiveDistance{
		config: config,
		pq:     pq,
	}

	qad.profilePool = sync.Pool{
		New: func() interface{} {
			return NewQueryProfile(pq.VectorDim(), pq.NumSubspaces())
		},
	}

	return qad, nil
}

func (qad *QueryAdaptiveDistance) ComputeWeightedDistanceTable(query []float32) (*WeightedDistanceTable, error) {
	distTable, err := qad.pq.ComputeDistanceTable(query)
	if err != nil {
		return nil, err
	}

	weights := qad.computeWeights(query)
	return &WeightedDistanceTable{Table: distTable, Weights: weights}, nil
}

func (qad *QueryAdaptiveDistance) computeWeights(query []float32) SubspaceWeights {
	if !qad.config.Enabled {
		return NewSubspaceWeights(qad.pq.NumSubspaces())
	}

	profile := qad.profilePool.Get().(*QueryProfile)
	defer qad.profilePool.Put(profile)

	if err := profile.ComputeFromQuery(query, qad.config.Strategy); err != nil {
		return NewSubspaceWeights(qad.pq.NumSubspaces())
	}

	if !qad.shouldApplyWeights(profile) {
		return NewSubspaceWeights(qad.pq.NumSubspaces())
	}

	return profile.Weights.Clone()
}

func (qad *QueryAdaptiveDistance) shouldApplyWeights(profile *QueryProfile) bool {
	if profile == nil || len(profile.SubspaceVariances) == 0 {
		return false
	}

	var minVar, maxVar float32
	minVar = profile.SubspaceVariances[0]
	maxVar = profile.SubspaceVariances[0]

	for _, v := range profile.SubspaceVariances[1:] {
		if v < minVar {
			minVar = v
		}
		if v > maxVar {
			maxVar = v
		}
	}

	if minVar == 0 {
		return maxVar > 0
	}

	return (maxVar / minVar) >= qad.config.MinVarianceRatio
}

func (qad *QueryAdaptiveDistance) WeightedAsymmetricDistance(table *WeightedDistanceTable, code PQCode) float32 {
	if table == nil || table.Table == nil || len(code) == 0 {
		return float32(1e30)
	}

	numSubspaces := len(code)
	if numSubspaces != table.Table.NumSubspaces {
		return float32(1e30)
	}

	var totalDist float32
	useWeights := len(table.Weights) == numSubspaces

	for m := 0; m < numSubspaces; m++ {
		centroidIdx := int(code[m])
		if centroidIdx >= len(table.Table.Table[m]) {
			continue
		}

		dist := table.Table.Table[m][centroidIdx]
		if useWeights {
			dist *= table.Weights[m] * float32(numSubspaces)
		}
		totalDist += dist
	}

	return totalDist
}

func (qad *QueryAdaptiveDistance) ComputeProfile(query []float32) (*QueryProfile, error) {
	profile := NewQueryProfile(qad.pq.VectorDim(), qad.pq.NumSubspaces())
	if profile == nil {
		return nil, ErrQueryAdaptiveNilProfile
	}

	if err := profile.ComputeFromQuery(query, qad.config.Strategy); err != nil {
		return nil, err
	}

	return profile, nil
}

type WeightedDistanceTable struct {
	Table   *DistanceTable
	Weights SubspaceWeights
}

func (w *WeightedDistanceTable) ComputeWeightedDistance(code PQCode) float32 {
	if w == nil || w.Table == nil || len(code) == 0 {
		return float32(1e30)
	}

	numSubspaces := len(code)
	if numSubspaces != w.Table.NumSubspaces {
		return float32(1e30)
	}

	var totalDist float32
	useWeights := len(w.Weights) == numSubspaces

	for m := 0; m < numSubspaces; m++ {
		centroidIdx := int(code[m])
		if centroidIdx >= len(w.Table.Table[m]) {
			continue
		}

		dist := w.Table.Table[m][centroidIdx]
		if useWeights {
			dist *= w.Weights[m] * float32(numSubspaces)
		}
		totalDist += dist
	}

	return totalDist
}

type QueryAdaptiveStats struct {
	QueriesProcessed     int64
	WeightedQueries      int64
	UniformQueries       int64
	AverageVarianceRatio float64
}
