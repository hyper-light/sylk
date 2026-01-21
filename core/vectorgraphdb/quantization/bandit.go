package quantization

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

type QuantizationBandit struct {
	definitions map[ArmID]ArmDefinition
	posteriors  map[string]map[ArmID][]BetaPosterior
	store       BanditStore
	rng         *rand.Rand
	mu          sync.Mutex
}

type BanditStore interface {
	LoadPosteriors(codebaseID string) (map[ArmID][]BetaPosterior, error)
	SavePosteriors(codebaseID string, posteriors map[ArmID][]BetaPosterior) error
}

func NewQuantizationBandit(store BanditStore) *QuantizationBandit {
	return &QuantizationBandit{
		definitions: DefaultArmDefinitions(),
		posteriors:  make(map[string]map[ArmID][]BetaPosterior),
		store:       store,
		rng:         rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (b *QuantizationBandit) Sample(codebaseID string) BanditConfig {
	b.mu.Lock()
	defer b.mu.Unlock()

	posteriors := b.getOrLoadPosteriorsLocked(codebaseID)
	config := BanditConfig{SampledChoiceIndices: make(map[ArmID]int)}

	for armID, def := range b.definitions {
		armPosteriors := posteriors[armID]
		if armPosteriors == nil {
			armPosteriors = initUniformPriors(len(def.Choices))
		}

		choiceIdx := b.sampleBestArmLocked(armPosteriors)
		config.SampledChoiceIndices[armID] = choiceIdx
		applyChoice(&config, armID, def.Choices[choiceIdx])
	}

	return config
}

func (b *QuantizationBandit) Update(codebaseID string, armID ArmID, choiceIdx int, reward float64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.updateLocked(codebaseID, armID, choiceIdx, reward)
}

func (b *QuantizationBandit) updateLocked(codebaseID string, armID ArmID, choiceIdx int, reward float64) {
	posteriors := b.getOrLoadPosteriorsLocked(codebaseID)
	armPosteriors := posteriors[armID]
	def := b.definitions[armID]

	if armPosteriors == nil {
		armPosteriors = initUniformPriors(len(def.Choices))
		posteriors[armID] = armPosteriors
	}

	if choiceIdx < 0 || choiceIdx >= len(armPosteriors) {
		return
	}

	if reward >= 0.5 {
		armPosteriors[choiceIdx].Alpha++
	} else {
		armPosteriors[choiceIdx].Beta++
	}
}

func (b *QuantizationBandit) UpdateFromOutcome(outcome RewardOutcome) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for armID, choiceIdx := range outcome.Config.SampledChoiceIndices {
		b.updateLocked(outcome.CodebaseID, armID, choiceIdx, outcome.Reward)
	}

	if b.store != nil {
		posteriors := b.posteriors[outcome.CodebaseID]
		if posteriors != nil {
			b.store.SavePosteriors(outcome.CodebaseID, posteriors)
		}
	}
}

func (b *QuantizationBandit) updateLockedContinuous(codebaseID string, armID ArmID, choiceIdx int, reward float64) {
	posteriors := b.getOrLoadPosteriorsLocked(codebaseID)
	armPosteriors := posteriors[armID]
	def := b.definitions[armID]

	if armPosteriors == nil {
		armPosteriors = initUniformPriors(len(def.Choices))
		posteriors[armID] = armPosteriors
	}

	if choiceIdx < 0 || choiceIdx >= len(armPosteriors) {
		return
	}

	if reward < 0 {
		reward = 0
	}
	if reward > 1 {
		reward = 1
	}

	armPosteriors[choiceIdx].Alpha += reward
	armPosteriors[choiceIdx].Beta += (1.0 - reward)
}

func (b *QuantizationBandit) UpdateFromContinuousOutcome(outcome ContinuousRewardOutcome) {
	b.mu.Lock()
	defer b.mu.Unlock()

	recallWeight := outcome.RecallWeight
	latencyWeight := outcome.LatencyWeight
	if recallWeight == 0 && latencyWeight == 0 {
		recallWeight, latencyWeight = DefaultContinuousRewardWeights()
	}

	reward := ComputeReward(outcome.Continuous, recallWeight, latencyWeight)

	for armID, choiceIdx := range outcome.Config.SampledChoiceIndices {
		b.updateLockedContinuous(outcome.CodebaseID, armID, choiceIdx, reward)
	}

	if b.store != nil {
		posteriors := b.posteriors[outcome.CodebaseID]
		if posteriors != nil {
			b.store.SavePosteriors(outcome.CodebaseID, posteriors)
		}
	}
}

func (b *QuantizationBandit) GetConfig(codebaseID string) BanditConfig {
	return b.Sample(codebaseID)
}

func (b *QuantizationBandit) GetDeterministicConfig() BanditConfig {
	return DefaultBanditConfig()
}

func (b *QuantizationBandit) getOrLoadPosteriorsLocked(codebaseID string) map[ArmID][]BetaPosterior {
	if posteriors, exists := b.posteriors[codebaseID]; exists {
		return posteriors
	}

	var posteriors map[ArmID][]BetaPosterior
	if b.store != nil {
		loaded, err := b.store.LoadPosteriors(codebaseID)
		if err == nil && loaded != nil {
			posteriors = loaded
		}
	}

	if posteriors == nil {
		posteriors = make(map[ArmID][]BetaPosterior)
	}

	b.posteriors[codebaseID] = posteriors
	return posteriors
}

func initUniformPriors(numChoices int) []BetaPosterior {
	priors := make([]BetaPosterior, numChoices)
	for i := range priors {
		priors[i] = NewUniformPrior()
	}
	return priors
}

func (b *QuantizationBandit) sampleBestArmLocked(posteriors []BetaPosterior) int {
	bestIdx := 0
	bestSample := float64(-1)

	for i, p := range posteriors {
		sample := b.sampleBetaLocked(p.Alpha, p.Beta)
		if sample > bestSample {
			bestSample = sample
			bestIdx = i
		}
	}

	return bestIdx
}

func (b *QuantizationBandit) sampleBetaLocked(alpha, beta float64) float64 {
	x := b.sampleGammaLocked(alpha)
	y := b.sampleGammaLocked(beta)
	if x+y < 1e-30 {
		return 0.5
	}
	return x / (x + y)
}

const maxGammaIterations = 1000

func (b *QuantizationBandit) sampleGammaLocked(shape float64) float64 {
	if shape <= 0 {
		return 0
	}

	if shape < 1 {
		u := b.rng.Float64()
		if u == 0 {
			u = 1e-10
		}
		return b.sampleGammaLocked(1+shape) * math.Pow(u, 1/shape)
	}

	d := shape - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)

	for iter := 0; iter < maxGammaIterations; iter++ {
		x := b.rng.NormFloat64()
		v := 1.0 + c*x
		if v <= 0 {
			continue
		}

		v = v * v * v
		u := b.rng.Float64()

		if u < 1.0-0.0331*(x*x)*(x*x) {
			return d * v
		}

		if u > 0 && math.Log(u) < 0.5*x*x+d*(1.0-v+math.Log(v)) {
			return d * v
		}
	}

	return shape
}

func applyChoice(config *BanditConfig, armID ArmID, value interface{}) {
	switch armID {
	case ArmCandidateMultiplier:
		config.CandidateMultiplier = value.(int)
	case ArmSubspaceDimTarget:
		config.SubspaceDimTarget = value.(int)
	case ArmMinSubspaces:
		config.MinSubspaces = value.(int)
	case ArmCentroidsPerSubspace:
		config.CentroidsPerSubspace = value.(int)
	case ArmSplitPerturbation:
		config.SplitPerturbation = value.(float32)
	case ArmReencodeTimeoutMinutes:
		config.ReencodeTimeout = time.Duration(value.(int)) * time.Minute
	case ArmLOPQCodeSizeBytes:
		config.LOPQCodeSizeBytes = value.(int)
	case ArmStrategyExactThreshold:
		config.StrategyExactThreshold = value.(int)
	case ArmStrategyCoarseThreshold:
		config.StrategyCoarseThreshold = value.(int)
	case ArmStrategyMediumThreshold:
		config.StrategyMediumThreshold = value.(int)
	}
}

func (b *QuantizationBandit) GetPosteriorStats(codebaseID string, armID ArmID) []BetaPosterior {
	b.mu.Lock()
	defer b.mu.Unlock()

	posteriors := b.posteriors[codebaseID]
	if posteriors == nil {
		return nil
	}

	armPosteriors := posteriors[armID]
	if armPosteriors == nil {
		return nil
	}

	result := make([]BetaPosterior, len(armPosteriors))
	copy(result, armPosteriors)
	return result
}

func (b *QuantizationBandit) GetTotalObservations(codebaseID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	posteriors := b.posteriors[codebaseID]
	if posteriors == nil {
		return 0
	}

	total := 0
	for _, armPosteriors := range posteriors {
		for _, p := range armPosteriors {
			total += p.TotalObservations()
		}
	}
	return total
}
