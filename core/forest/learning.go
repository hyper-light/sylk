package forest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/viterin/vek/vek32"
)

// LearningObjective identifies the supervised target for a learned model.
type LearningObjective string

const (
	ObjectiveUtility LearningObjective = "utility"
	ObjectiveRisk    LearningObjective = "risk"
)

type boosterConfig struct {
	LinearEpochs     int
	MaxTrees         int
	MaxBins          int
	LearningRate     float64
	L2               float64
	Gamma            float64
	MinChildExamples int
	MinExamples      int
}

type trainingExample struct {
	ID               int64
	RetrievalID      string
	BranchID         string
	RootID           string
	SessionID        string
	CallerAgentType  string
	BranchAgentType  string
	RankPosition     int
	BaseScore        float64
	PredictedUtility float64
	PredictedRisk    float64
	UtilityLabel     sql.NullFloat64
	RiskLabel        sql.NullFloat64
	Features         []float32
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type labeledExample struct {
	Features []float32
	Label    float64
}

type decisionStump struct {
	FeatureIndex int     `json:"feature_index"`
	Threshold    float32 `json:"threshold"`
	LeftWeight   float32 `json:"left_weight"`
	RightWeight  float32 `json:"right_weight"`
	Gain         float32 `json:"gain"`
	LeftCount    int     `json:"left_count"`
	RightCount   int     `json:"right_count"`
}

type learnedModelMetrics struct {
	ExampleCount int     `json:"example_count"`
	PositiveRate float64 `json:"positive_rate"`
	LogLoss      float64 `json:"log_loss"`
	Accuracy     float64 `json:"accuracy"`
	Brier        float64 `json:"brier"`
}

type boosterModel struct {
	Key           string              `json:"key"`
	Objective     LearningObjective   `json:"objective"`
	AgentType     string              `json:"agent_type"`
	Version       int                 `json:"version"`
	TrainedAt     time.Time           `json:"trained_at"`
	Bias          float32             `json:"bias"`
	LinearWeights []float32           `json:"linear_weights"`
	Trees         []decisionStump     `json:"trees"`
	Metrics       learnedModelMetrics `json:"metrics"`
	Confidence    float64             `json:"confidence"`
	FeatureNames  []string            `json:"feature_names"`
}

func (m *boosterModel) Predict(features []float32) (float64, float64) {
	if m == nil {
		return 0, 0
	}
	raw := float64(m.Bias)
	if len(m.LinearWeights) > 0 && len(features) >= len(m.LinearWeights) {
		raw += float64(vek32.Dot(features[:len(m.LinearWeights)], m.LinearWeights))
	}
	for _, tree := range m.Trees {
		if tree.FeatureIndex < 0 || tree.FeatureIndex >= len(features) {
			continue
		}
		if features[tree.FeatureIndex] <= tree.Threshold {
			raw += float64(tree.LeftWeight)
		} else {
			raw += float64(tree.RightWeight)
		}
	}
	return raw, sigmoid(raw)
}

func (m *boosterModel) clone() *boosterModel {
	if m == nil {
		return nil
	}
	cloned := *m
	cloned.LinearWeights = append([]float32(nil), m.LinearWeights...)
	cloned.Trees = append([]decisionStump(nil), m.Trees...)
	cloned.FeatureNames = append([]string(nil), m.FeatureNames...)
	return &cloned
}

type learnedModelStore struct {
	db     *sql.DB
	mu     sync.RWMutex
	models map[string]*boosterModel
}

func newLearnedModelStore(db *sql.DB) (*learnedModelStore, error) {
	store := &learnedModelStore{
		db:     db,
		models: make(map[string]*boosterModel),
	}
	if err := store.loadActive(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *learnedModelStore) loadActive(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT model_key, payload
		FROM forest_models
		WHERE active = 1
	`)
	if err != nil {
		return fmt.Errorf("query active forest models: %w", err)
	}
	defer rows.Close()

	models := make(map[string]*boosterModel)
	for rows.Next() {
		var (
			key     string
			payload []byte
			model   boosterModel
		)
		if err := rows.Scan(&key, &payload); err != nil {
			return fmt.Errorf("scan forest model: %w", err)
		}
		if err := json.Unmarshal(payload, &model); err != nil {
			return fmt.Errorf("decode forest model %s: %w", key, err)
		}
		models[key] = model.clone()
	}
	if err := rows.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	s.models = models
	s.mu.Unlock()
	return nil
}

func (s *learnedModelStore) resolve(objective LearningObjective, agentType string) *boosterModel {
	normalized := normalizeRequesterAgentType(agentType)
	keys := []string{
		modelKey(objective, normalized),
		modelKey(objective, "global"),
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, key := range keys {
		if model, ok := s.models[key]; ok {
			return model.clone()
		}
	}
	return nil
}

func (s *learnedModelStore) save(ctx context.Context, model *boosterModel) error {
	if model == nil {
		return nil
	}
	payload, err := json.Marshal(model)
	if err != nil {
		return fmt.Errorf("marshal forest model: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin forest model tx: %w", err)
	}
	defer tx.Rollback()

	var nextVersion int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM forest_models
		WHERE model_key = ?
	`, model.Key).Scan(&nextVersion); err != nil {
		return fmt.Errorf("load forest model version: %w", err)
	}
	model.Version = nextVersion
	payload, err = json.Marshal(model)
	if err != nil {
		return fmt.Errorf("marshal forest model: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE forest_models
		SET active = 0
		WHERE model_key = ?
	`, model.Key)
	if err != nil {
		return fmt.Errorf("deactivate forest model: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO forest_models
		(model_key, version, objective, agent_type, trained_at, example_count, feature_count, confidence, active, metrics, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`, model.Key, model.Version, string(model.Objective), model.AgentType, model.TrainedAt.Unix(),
		model.Metrics.ExampleCount, len(model.FeatureNames), model.Confidence, marshalJSON(model.Metrics), payload)
	if err != nil {
		return fmt.Errorf("insert forest model: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit forest model tx: %w", err)
	}

	s.mu.Lock()
	s.models[model.Key] = model.clone()
	s.mu.Unlock()
	return nil
}

func defaultBoosterConfig() boosterConfig {
	return boosterConfig{
		LinearEpochs:     14,
		MaxTrees:         24,
		MaxBins:          8,
		LearningRate:     0.18,
		L2:               1.0,
		Gamma:            1e-4,
		MinChildExamples: 4,
		MinExamples:      16,
	}
}

func modelKey(objective LearningObjective, agentType string) string {
	return string(objective) + ":" + normalizeRequesterAgentType(agentType)
}

func normalizeRequesterAgentType(agentType string) string {
	normalized := strings.ToLower(strings.TrimSpace(agentType))
	switch {
	case normalized == "":
		return "global"
	case strings.HasPrefix(normalized, "scribe"):
		return "scribe"
	default:
		return normalized
	}
}

func (m *MemoryForest) TrainModels(ctx context.Context, maxExamples int) (TrainingResult, error) {
	if m.models == nil {
		return TrainingResult{}, nil
	}
	examples, err := m.loadTrainingExamples(ctx, maxExamples)
	if err != nil {
		return TrainingResult{}, err
	}
	groups := buildTrainingGroups(examples, m.booster)
	if len(groups) == 0 {
		return TrainingResult{}, nil
	}

	type trainResult struct {
		model *boosterModel
		err   error
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []trainResult
	)
	for _, group := range groups {
		group := group
		wg.Add(1)
		go func() {
			defer wg.Done()
			model, err := trainBoosterModel(group.objective, group.agentType, group.examples, m.booster)
			mu.Lock()
			results = append(results, trainResult{model: model, err: err})
			mu.Unlock()
		}()
	}
	wg.Wait()

	var output TrainingResult
	for _, result := range results {
		if result.err != nil {
			return output, result.err
		}
		if result.model == nil {
			continue
		}
		if err := m.models.save(ctx, result.model); err != nil {
			return output, err
		}
		output.Trained++
		output.Keys = append(output.Keys, result.model.Key)
	}
	sort.Strings(output.Keys)
	return output, nil
}

func (m *MemoryForest) loadTrainingExamples(ctx context.Context, limit int) ([]trainingExample, error) {
	if limit <= 0 {
		limit = 1024
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, retrieval_id, branch_id, root_id, session_id, caller_agent_type, branch_agent_type,
		       rank_position, base_score, predicted_utility, predicted_risk,
		       utility_label, risk_label, features, created_at, updated_at
		FROM forest_training_examples
		WHERE utility_label IS NOT NULL OR risk_label IS NOT NULL
		ORDER BY updated_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query training examples: %w", err)
	}
	defer rows.Close()

	var examples []trainingExample
	for rows.Next() {
		var (
			example   trainingExample
			features  []byte
			createdAt int64
			updatedAt int64
		)
		if err := rows.Scan(
			&example.ID,
			&example.RetrievalID,
			&example.BranchID,
			&example.RootID,
			&example.SessionID,
			&example.CallerAgentType,
			&example.BranchAgentType,
			&example.RankPosition,
			&example.BaseScore,
			&example.PredictedUtility,
			&example.PredictedRisk,
			&example.UtilityLabel,
			&example.RiskLabel,
			&features,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan training example: %w", err)
		}
		example.Features = unpackFloat32s(features)
		example.CreatedAt = time.Unix(createdAt, 0).UTC()
		example.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		if len(example.Features) == featureCount {
			examples = append(examples, example)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return examples, nil
}

type trainingGroup struct {
	objective LearningObjective
	agentType string
	examples  []labeledExample
}

func buildTrainingGroups(examples []trainingExample, cfg boosterConfig) []trainingGroup {
	globalUtility := make([]labeledExample, 0, len(examples))
	globalRisk := make([]labeledExample, 0, len(examples))
	utilityByAgent := make(map[string][]labeledExample)
	riskByAgent := make(map[string][]labeledExample)

	for _, example := range examples {
		agentType := normalizeRequesterAgentType(example.CallerAgentType)
		if example.UtilityLabel.Valid {
			item := labeledExample{Features: example.Features, Label: clamp01(example.UtilityLabel.Float64)}
			globalUtility = append(globalUtility, item)
			utilityByAgent[agentType] = append(utilityByAgent[agentType], item)
		}
		if example.RiskLabel.Valid {
			item := labeledExample{Features: example.Features, Label: clamp01(example.RiskLabel.Float64)}
			globalRisk = append(globalRisk, item)
			riskByAgent[agentType] = append(riskByAgent[agentType], item)
		}
	}

	var groups []trainingGroup
	if len(globalUtility) >= cfg.MinExamples {
		groups = append(groups, trainingGroup{objective: ObjectiveUtility, agentType: "global", examples: trimExamples(globalUtility, 512)})
	}
	if len(globalRisk) >= cfg.MinExamples {
		groups = append(groups, trainingGroup{objective: ObjectiveRisk, agentType: "global", examples: trimExamples(globalRisk, 512)})
	}
	for agentType, group := range utilityByAgent {
		if agentType == "global" || len(group) < cfg.MinExamples {
			continue
		}
		groups = append(groups, trainingGroup{objective: ObjectiveUtility, agentType: agentType, examples: trimExamples(group, 256)})
	}
	for agentType, group := range riskByAgent {
		if agentType == "global" || len(group) < cfg.MinExamples {
			continue
		}
		groups = append(groups, trainingGroup{objective: ObjectiveRisk, agentType: agentType, examples: trimExamples(group, 256)})
	}
	return groups
}

func trimExamples(examples []labeledExample, limit int) []labeledExample {
	if len(examples) <= limit {
		return examples
	}
	return examples[:limit]
}

func trainBoosterModel(objective LearningObjective, agentType string, examples []labeledExample, cfg boosterConfig) (*boosterModel, error) {
	if len(examples) < cfg.MinExamples {
		return nil, nil
	}
	if len(examples[0].Features) != featureCount {
		return nil, nil
	}

	labels := make([]float64, len(examples))
	for i, example := range examples {
		labels[i] = clamp01(example.Label)
	}

	positiveRate := mean(labels)
	bias := float32(logit(clampProbability(positiveRate)))
	weights := make([]float32, featureCount)
	if objective == ObjectiveUtility {
		copy(weights, seededLinearWeights())
	}

	for epoch := 0; epoch < cfg.LinearEpochs; epoch++ {
		var biasGrad float64
		grad := make([]float32, featureCount)
		for i, example := range examples {
			pred := sigmoid(float64(bias) + float64(vek32.Dot(example.Features, weights)))
			diff := pred - labels[i]
			biasGrad += diff
			for j, value := range example.Features {
				grad[j] += float32(diff) * value
			}
		}
		scale := float32(cfg.LearningRate / float64(len(examples)))
		for j := range weights {
			weights[j] -= scale * (grad[j] + float32(cfg.L2)*weights[j])
		}
		bias -= float32((cfg.LearningRate / float64(len(examples))) * biasGrad)
	}

	rawPreds := make([]float64, len(examples))
	for i, example := range examples {
		rawPreds[i] = float64(bias) + float64(vek32.Dot(example.Features, weights))
	}

	trees := make([]decisionStump, 0, cfg.MaxTrees)
	for round := 0; round < cfg.MaxTrees; round++ {
		grads := make([]float64, len(examples))
		hess := make([]float64, len(examples))
		for i := range examples {
			prob := sigmoid(rawPreds[i])
			grads[i] = prob - labels[i]
			hess[i] = math.Max(prob*(1-prob), 1e-6)
		}
		stump := fitBestStump(examples, grads, hess, cfg)
		if stump.Gain <= 0 {
			break
		}
		trees = append(trees, stump)
		for i, example := range examples {
			if example.Features[stump.FeatureIndex] <= stump.Threshold {
				rawPreds[i] += float64(stump.LeftWeight)
			} else {
				rawPreds[i] += float64(stump.RightWeight)
			}
		}
	}

	metrics := evaluateModel(labels, rawPreds)
	model := &boosterModel{
		Key:           modelKey(objective, agentType),
		Objective:     objective,
		AgentType:     normalizeRequesterAgentType(agentType),
		Version:       1,
		TrainedAt:     time.Now().UTC(),
		Bias:          bias,
		LinearWeights: weights,
		Trees:         trees,
		Metrics:       metrics,
		Confidence:    modelConfidence(metrics),
		FeatureNames:  append([]string(nil), forestFeatureNames[:]...),
	}
	return model, nil
}

func seededLinearWeights() []float32 {
	weights := make([]float32, featureCount)
	copy(weights[:len(defaultScoreWeights)], defaultScoreWeights)
	weights[featureBaseScore] = 0.35
	weights[featureAgentFamilyAffinity] = 0.08
	weights[featureSessionAffinity] = 0.05
	return weights
}

func fitBestStump(examples []labeledExample, grads, hess []float64, cfg boosterConfig) decisionStump {
	best := decisionStump{FeatureIndex: -1}
	totalGrad := sumFloat64(grads)
	totalHess := sumFloat64(hess)
	if totalHess <= 0 {
		return best
	}

	for featureIndex := 0; featureIndex < featureCount; featureIndex++ {
		thresholds := candidateThresholds(examples, featureIndex, cfg.MaxBins)
		for _, threshold := range thresholds {
			var leftGrad, leftHess float64
			var leftCount int
			for idx, example := range examples {
				if example.Features[featureIndex] <= threshold {
					leftGrad += grads[idx]
					leftHess += hess[idx]
					leftCount++
				}
			}
			rightCount := len(examples) - leftCount
			if leftCount < cfg.MinChildExamples || rightCount < cfg.MinChildExamples {
				continue
			}
			rightGrad := totalGrad - leftGrad
			rightHess := totalHess - leftHess
			gain := (leftGrad*leftGrad)/(leftHess+cfg.L2) +
				(rightGrad*rightGrad)/(rightHess+cfg.L2) -
				(totalGrad*totalGrad)/(totalHess+cfg.L2) - cfg.Gamma
			if gain <= float64(best.Gain) {
				continue
			}
			best = decisionStump{
				FeatureIndex: featureIndex,
				Threshold:    threshold,
				LeftWeight:   float32((-leftGrad / (leftHess + cfg.L2)) * cfg.LearningRate),
				RightWeight:  float32((-rightGrad / (rightHess + cfg.L2)) * cfg.LearningRate),
				Gain:         float32(gain),
				LeftCount:    leftCount,
				RightCount:   rightCount,
			}
		}
	}
	return best
}

func candidateThresholds(examples []labeledExample, featureIndex int, maxBins int) []float32 {
	values := make([]float32, 0, len(examples))
	for _, example := range examples {
		values = append(values, example.Features[featureIndex])
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if len(values) <= 1 {
		return nil
	}

	maxBins = minInt(maxBins, len(values)-1)
	thresholds := make([]float32, 0, maxBins)
	last := float32(math.NaN())
	for bin := 1; bin <= maxBins; bin++ {
		idx := int(math.Round((float64(bin) / float64(maxBins+1)) * float64(len(values)-1)))
		if idx <= 0 || idx >= len(values) {
			continue
		}
		threshold := (values[idx-1] + values[idx]) / 2
		if threshold == values[idx] && threshold == values[idx-1] {
			continue
		}
		if !math.IsNaN(float64(last)) && threshold == last {
			continue
		}
		last = threshold
		thresholds = append(thresholds, threshold)
	}
	return thresholds
}

func evaluateModel(labels, rawPreds []float64) learnedModelMetrics {
	var (
		logLoss float64
		brier   float64
		correct int
	)
	for i, label := range labels {
		prob := clampProbability(sigmoid(rawPreds[i]))
		logLoss += -(label*math.Log(prob) + (1-label)*math.Log(1-prob))
		diff := prob - label
		brier += diff * diff
		predicted := 0.0
		if prob >= 0.5 {
			predicted = 1.0
		}
		if predicted == math.Round(label) {
			correct++
		}
	}
	count := len(labels)
	if count == 0 {
		return learnedModelMetrics{}
	}
	return learnedModelMetrics{
		ExampleCount: count,
		PositiveRate: mean(labels),
		LogLoss:      logLoss / float64(count),
		Accuracy:     float64(correct) / float64(count),
		Brier:        brier / float64(count),
	}
}

func modelConfidence(metrics learnedModelMetrics) float64 {
	sampleSignal := clamp01(float64(metrics.ExampleCount) / 96.0)
	lossSignal := clamp01(1 - (metrics.LogLoss / math.Ln2))
	accuracySignal := clamp01((metrics.Accuracy - 0.5) * 2)
	return clamp01((sampleSignal * 0.45) + (lossSignal * 0.35) + (accuracySignal * 0.20))
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	return sumFloat64(values) / float64(len(values))
}

func sumFloat64(values []float64) float64 {
	var total float64
	for _, value := range values {
		total += value
	}
	return total
}

func clampProbability(value float64) float64 {
	switch {
	case value < 1e-4:
		return 1e-4
	case value > 1-1e-4:
		return 1 - 1e-4
	default:
		return value
	}
}

func logit(probability float64) float64 {
	probability = clampProbability(probability)
	return math.Log(probability / (1 - probability))
}

func (m *MemoryForest) applyLearnedPredictions(query Query, packets []*BranchPacket, featureByBranch map[string][]float32) {
	if m.models == nil || len(packets) == 0 {
		return
	}

	utilityModel := m.models.resolve(ObjectiveUtility, query.AgentType)
	riskModel := m.models.resolve(ObjectiveRisk, query.AgentType)
	if utilityModel == nil && riskModel == nil {
		return
	}

	for _, packet := range packets {
		if packet == nil || packet.Branch == nil {
			continue
		}
		features := featureByBranch[packet.Branch.ID]
		if len(features) != featureCount {
			continue
		}
		prediction := &LearnedPrediction{
			Signals: summarizeFeatureSignals(features, 6),
		}
		baseScore := clamp01(packet.Score.Total)
		if utilityModel != nil {
			_, probability := utilityModel.Predict(features)
			prediction.Utility = clamp01(probability)
			prediction.UtilityModel = utilityModel.Key
			prediction.UtilityVersion = utilityModel.Version
			prediction.Confidence = math.Max(prediction.Confidence, utilityModel.Confidence)
		} else {
			prediction.Utility = baseScore
		}
		if riskModel != nil {
			_, probability := riskModel.Predict(features)
			prediction.Risk = clamp01(probability)
			prediction.RiskModel = riskModel.Key
			prediction.RiskVersion = riskModel.Version
			prediction.Confidence = math.Max(prediction.Confidence, riskModel.Confidence)
		}
		prediction.Replay = clamp01((prediction.Utility + float64(features[featureSalience]) + float64(features[featureWarmth])) / 3)
		prediction.Clarification = clamp01(math.Abs(baseScore-prediction.Utility) + prediction.Risk*0.5)

		alpha := 0.45 * clamp01(prediction.Confidence)
		learnedScore := clamp01((baseScore * (1 - alpha)) + (prediction.Utility * alpha))
		total := clamp01(learnedScore - (prediction.Risk * 0.08) - (prediction.Clarification * 0.03))

		packet.Prediction = prediction
		packet.Score.Base = baseScore
		packet.Score.Learned = learnedScore
		packet.Score.RiskPenalty = prediction.Risk
		packet.Score.ModelConfidence = prediction.Confidence
		packet.Score.Total = total
	}
}

func (m *MemoryForest) recordRetrievalExamples(
	ctx context.Context,
	query Query,
	packets []*BranchPacket,
	featureByBranch map[string][]float32,
) error {
	if len(packets) == 0 {
		return nil
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin retrieval example tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO forest_training_examples
		(retrieval_id, branch_id, root_id, session_id, caller_agent_type, branch_agent_type,
		 rank_position, base_score, predicted_utility, predicted_risk, features, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(retrieval_id, branch_id) DO UPDATE SET
			rank_position = excluded.rank_position,
			base_score = excluded.base_score,
			predicted_utility = excluded.predicted_utility,
			predicted_risk = excluded.predicted_risk,
			features = excluded.features,
			metadata = excluded.metadata,
			updated_at = excluded.updated_at
	`)
	if err != nil {
		return fmt.Errorf("prepare retrieval example insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC()
	retrievalID := stableID(
		"retrieval",
		query.SessionID,
		query.AgentType,
		query.IntentID,
		normalizeText(query.Query),
		now.Format(time.RFC3339Nano),
	)

	limit := maxInt(query.Limit*2, 8)
	if len(packets) < limit {
		limit = len(packets)
	}
	for i := 0; i < limit; i++ {
		packet := packets[i]
		if packet == nil || packet.Branch == nil {
			continue
		}
		features := featureByBranch[packet.Branch.ID]
		if len(features) != featureCount {
			continue
		}
		predictedUtility := packet.Score.Learned
		predictedRisk := packet.Score.RiskPenalty
		if packet.Prediction != nil {
			predictedUtility = packet.Prediction.Utility
			predictedRisk = packet.Prediction.Risk
		}
		metadata := map[string]any{
			"query":      query.Query,
			"families":   query.Families,
			"intent_id":  query.IntentID,
			"horizon":    query.Horizon,
			"packet":     packet.Score,
			"prediction": packet.Prediction,
		}
		if _, err := stmt.ExecContext(
			ctx,
			retrievalID,
			packet.Branch.ID,
			packet.Branch.RootID,
			firstNonEmpty(query.SessionID, packet.Branch.SessionID, "global"),
			normalizeRequesterAgentType(query.AgentType),
			strings.ToLower(strings.TrimSpace(packet.Branch.AgentType)),
			i,
			packet.Score.Base,
			clamp01(predictedUtility),
			clamp01(predictedRisk),
			packFloat32s(features),
			marshalJSON(metadata),
			now.Unix(),
			now.Unix(),
		); err != nil {
			return fmt.Errorf("insert retrieval example: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retrieval example tx: %w", err)
	}
	return nil
}

func (m *MemoryForest) labelExamplesForOutcome(ctx context.Context, branchID, sessionID string, status OutcomeStatus) error {
	utilityLabel, riskLabel := outcomeLabels(status)
	if branchID == "" {
		return nil
	}
	if sessionID == "" {
		sessionID = "global"
	}
	_, err := m.db.ExecContext(ctx, `
		UPDATE forest_training_examples
		SET utility_label = COALESCE(utility_label, ?),
		    risk_label = COALESCE(risk_label, ?),
		    updated_at = ?
		WHERE branch_id = ? AND session_id = ?
	`, utilityLabel, riskLabel, time.Now().UTC().Unix(), branchID, sessionID)
	if err != nil {
		return fmt.Errorf("label forest training examples: %w", err)
	}
	return nil
}

func outcomeLabels(status OutcomeStatus) (float64, float64) {
	switch status {
	case OutcomeStatusSucceeded:
		return 1.0, 0.0
	case OutcomeStatusFailed:
		return 0.0, 1.0
	default:
		return 0.5, 0.5
	}
}

func (m *MemoryForest) loadRelayMass(ctx context.Context, branchIDs []string) (map[string]float64, error) {
	branchIDs = dedupeStrings(branchIDs)
	result := make(map[string]float64, len(branchIDs))
	if len(branchIDs) == 0 {
		return result, nil
	}

	placeholders := make([]string, 0, len(branchIDs))
	args := make([]any, 0, len(branchIDs)*2)
	for _, branchID := range branchIDs {
		placeholders = append(placeholders, "?")
		args = append(args, branchID)
	}
	for _, branchID := range branchIDs {
		args = append(args, branchID)
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT branch_id, SUM(weight)
		FROM (
			SELECT source_branch_id AS branch_id, weight
			FROM forest_relay_edges
			WHERE source_branch_id IN (`+strings.Join(placeholders, ",")+`)
			UNION ALL
			SELECT target_branch_id AS branch_id, weight
			FROM forest_relay_edges
			WHERE target_branch_id IN (`+strings.Join(placeholders, ",")+`)
		)
		GROUP BY branch_id
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query relay mass: %w", err)
	}
	defer rows.Close()

	var maxMass float64
	for rows.Next() {
		var (
			branchID string
			mass     float64
		)
		if err := rows.Scan(&branchID, &mass); err != nil {
			return nil, fmt.Errorf("scan relay mass: %w", err)
		}
		result[branchID] = mass
		if mass > maxMass {
			maxMass = mass
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if maxMass <= 0 {
		return result, nil
	}
	for branchID, mass := range result {
		result[branchID] = clamp01(mass / maxMass)
	}
	return result, nil
}
