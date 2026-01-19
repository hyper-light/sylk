package handoff

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
)

// =============================================================================
// HO.2.1 Hierarchical Parameter Blending
// =============================================================================
//
// Implements hierarchical blending of learned parameters across levels:
//   Instance -> AgentModel -> Model -> Global priors
//
// Low confidence at any level causes delegation to higher levels.
// This enables cold start handling - new instances with no data
// gracefully fall back to model-level or global priors.

// BlendLevel represents a level in the parameter hierarchy.
// Lower numeric values are more specific (instance-level),
// higher values are more general (global priors).
type BlendLevel int

const (
	// LevelInstance is the most specific level - parameters learned
	// for a specific instance (e.g., a particular conversation session).
	LevelInstance BlendLevel = iota

	// LevelAgentModel represents parameters learned for a specific
	// combination of agent type and model (e.g., "coder" agent using "gpt-4").
	LevelAgentModel

	// LevelModel represents parameters learned for a specific model
	// across all agent types (e.g., all "gpt-4" usage patterns).
	LevelModel

	// LevelGlobal represents global prior parameters that apply
	// when no more specific data is available.
	LevelGlobal
)

// String returns a human-readable name for the blend level.
func (bl BlendLevel) String() string {
	switch bl {
	case LevelInstance:
		return "Instance"
	case LevelAgentModel:
		return "AgentModel"
	case LevelModel:
		return "Model"
	case LevelGlobal:
		return "Global"
	default:
		return fmt.Sprintf("Unknown(%d)", int(bl))
	}
}

// =============================================================================
// Leveled Parameter Wrapper
// =============================================================================

// leveledParam wraps a parameter with its confidence level.
// This is used internally for blending calculations.
type leveledParam struct {
	// Param is the learned parameter at this level.
	// Can be *LearnedWeight, *LearnedContextSize, or *LearnedCount.
	Param interface{}

	// Confidence is the confidence in this parameter (0.0 to 1.0).
	// Higher confidence means more weight in blending.
	Confidence float64

	// Level identifies which hierarchy level this parameter is from.
	Level BlendLevel
}

// =============================================================================
// Blended Parameters Result
// =============================================================================

// BlendedParams contains the result of hierarchical parameter blending.
// It tracks the contribution weight from each level for transparency
// and debugging purposes.
type BlendedParams struct {
	// Value is the final blended value.
	Value float64 `json:"value"`

	// InstanceWeight is the weight contribution from instance level.
	InstanceWeight float64 `json:"instance_weight"`

	// AgentModelWeight is the weight contribution from agent-model level.
	AgentModelWeight float64 `json:"agent_model_weight"`

	// ModelWeight is the weight contribution from model level.
	ModelWeight float64 `json:"model_weight"`

	// GlobalWeight is the weight contribution from global level.
	GlobalWeight float64 `json:"global_weight"`

	// EffectiveConfidence is the combined confidence in the blended value.
	EffectiveConfidence float64 `json:"effective_confidence"`

	// DominantLevel indicates which level contributed the most weight.
	DominantLevel BlendLevel `json:"dominant_level"`
}

// MarshalJSON implements json.Marshaler.
func (bp *BlendedParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"value":                bp.Value,
		"instance_weight":      bp.InstanceWeight,
		"agent_model_weight":   bp.AgentModelWeight,
		"model_weight":         bp.ModelWeight,
		"global_weight":        bp.GlobalWeight,
		"effective_confidence": bp.EffectiveConfidence,
		"dominant_level":       bp.DominantLevel.String(),
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (bp *BlendedParams) UnmarshalJSON(data []byte) error {
	var temp map[string]interface{}
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	if v, ok := temp["value"].(float64); ok {
		bp.Value = v
	}
	if v, ok := temp["instance_weight"].(float64); ok {
		bp.InstanceWeight = v
	}
	if v, ok := temp["agent_model_weight"].(float64); ok {
		bp.AgentModelWeight = v
	}
	if v, ok := temp["model_weight"].(float64); ok {
		bp.ModelWeight = v
	}
	if v, ok := temp["global_weight"].(float64); ok {
		bp.GlobalWeight = v
	}
	if v, ok := temp["effective_confidence"].(float64); ok {
		bp.EffectiveConfidence = v
	}
	if level, ok := temp["dominant_level"].(string); ok {
		bp.DominantLevel = parseLevelString(level)
	}

	return nil
}

// parseLevelString converts a string to BlendLevel.
func parseLevelString(s string) BlendLevel {
	switch s {
	case "Instance":
		return LevelInstance
	case "AgentModel":
		return LevelAgentModel
	case "Model":
		return LevelModel
	case "Global":
		return LevelGlobal
	default:
		return LevelGlobal
	}
}

// =============================================================================
// Parameter Key Types
// =============================================================================

// instanceKey uniquely identifies an instance-level parameter.
type instanceKey struct {
	AgentType  string
	ModelID    string
	InstanceID string
	ParamName  string
}

// agentModelKey uniquely identifies an agent-model level parameter.
type agentModelKey struct {
	AgentType string
	ModelID   string
	ParamName string
}

// modelKey uniquely identifies a model-level parameter.
type modelKey struct {
	ModelID   string
	ParamName string
}

// globalKey uniquely identifies a global-level parameter.
type globalKey struct {
	ParamName string
}

// =============================================================================
// Hierarchical Parameter Blender
// =============================================================================

// HierarchicalParamBlender manages learned parameters across hierarchy levels
// and provides blended values based on confidence-weighted combinations.
//
// The blending algorithm:
// 1. Collects parameters from all available levels
// 2. Computes blend weight for each level based on confidence
// 3. Normalizes weights to sum to 1.0
// 4. Returns weighted combination of parameter values
//
// Cold start behavior: When instance-level has low confidence,
// the blender automatically delegates to higher levels with more data.
type HierarchicalParamBlender struct {
	mu sync.RWMutex

	// instanceParams stores instance-level parameters.
	// Key: instanceKey, Value: *LearnedWeight
	instanceParams map[instanceKey]*LearnedWeight

	// agentModelParams stores agent-model level parameters.
	// Key: agentModelKey, Value: *LearnedWeight
	agentModelParams map[agentModelKey]*LearnedWeight

	// modelParams stores model-level parameters.
	// Key: modelKey, Value: *LearnedWeight
	modelParams map[modelKey]*LearnedWeight

	// globalParams stores global prior parameters.
	// Key: globalKey, Value: *LearnedWeight
	globalParams map[globalKey]*LearnedWeight

	// config controls blending behavior.
	config *BlenderConfig
}

// BlenderConfig controls hierarchical blending behavior.
type BlenderConfig struct {
	// MinConfidenceForWeight is the minimum confidence required
	// for a level to contribute any weight. Default: 0.1
	MinConfidenceForWeight float64 `json:"min_confidence_for_weight"`

	// ConfidenceExponent controls how strongly confidence affects weight.
	// Higher values make high-confidence levels dominate more.
	// Default: 2.0
	ConfidenceExponent float64 `json:"confidence_exponent"`

	// ExplorationBonus adds extra weight to lower levels to encourage
	// learning from more specific data. Default: 0.0
	ExplorationBonus float64 `json:"exploration_bonus"`
}

// DefaultBlenderConfig returns sensible defaults for the blender.
func DefaultBlenderConfig() *BlenderConfig {
	return &BlenderConfig{
		MinConfidenceForWeight: 0.1,
		ConfidenceExponent:     2.0,
		ExplorationBonus:       0.0,
	}
}

// NewHierarchicalParamBlender creates a new blender with the given config.
// If config is nil, uses DefaultBlenderConfig.
func NewHierarchicalParamBlender(config *BlenderConfig) *HierarchicalParamBlender {
	if config == nil {
		config = DefaultBlenderConfig()
	}

	return &HierarchicalParamBlender{
		instanceParams:   make(map[instanceKey]*LearnedWeight),
		agentModelParams: make(map[agentModelKey]*LearnedWeight),
		modelParams:      make(map[modelKey]*LearnedWeight),
		globalParams:     make(map[globalKey]*LearnedWeight),
		config:           config,
	}
}

// =============================================================================
// Registration Methods
// =============================================================================

// RegisterInstance registers a learned parameter at the instance level.
// This is the most specific level, for a particular session/conversation.
func (hpb *HierarchicalParamBlender) RegisterInstance(
	agentType, modelID, instanceID, paramName string,
	param *LearnedWeight,
) {
	if param == nil {
		return
	}

	hpb.mu.Lock()
	defer hpb.mu.Unlock()

	key := instanceKey{
		AgentType:  agentType,
		ModelID:    modelID,
		InstanceID: instanceID,
		ParamName:  paramName,
	}
	hpb.instanceParams[key] = param
}

// RegisterAgentModel registers a learned parameter at the agent-model level.
// This level aggregates data from all instances of a specific agent+model combination.
func (hpb *HierarchicalParamBlender) RegisterAgentModel(
	agentType, modelID, paramName string,
	param *LearnedWeight,
) {
	if param == nil {
		return
	}

	hpb.mu.Lock()
	defer hpb.mu.Unlock()

	key := agentModelKey{
		AgentType: agentType,
		ModelID:   modelID,
		ParamName: paramName,
	}
	hpb.agentModelParams[key] = param
}

// RegisterModel registers a learned parameter at the model level.
// This level aggregates data from all agent types using a specific model.
func (hpb *HierarchicalParamBlender) RegisterModel(
	modelID, paramName string,
	param *LearnedWeight,
) {
	if param == nil {
		return
	}

	hpb.mu.Lock()
	defer hpb.mu.Unlock()

	key := modelKey{
		ModelID:   modelID,
		ParamName: paramName,
	}
	hpb.modelParams[key] = param
}

// RegisterGlobal registers a learned parameter at the global level.
// This is the fallback when no more specific data is available.
func (hpb *HierarchicalParamBlender) RegisterGlobal(
	paramName string,
	param *LearnedWeight,
) {
	if param == nil {
		return
	}

	hpb.mu.Lock()
	defer hpb.mu.Unlock()

	key := globalKey{ParamName: paramName}
	hpb.globalParams[key] = param
}

// =============================================================================
// Lookup Methods
// =============================================================================

// GetInstance returns the instance-level parameter if it exists.
func (hpb *HierarchicalParamBlender) GetInstance(
	agentType, modelID, instanceID, paramName string,
) (*LearnedWeight, bool) {
	hpb.mu.RLock()
	defer hpb.mu.RUnlock()

	key := instanceKey{
		AgentType:  agentType,
		ModelID:    modelID,
		InstanceID: instanceID,
		ParamName:  paramName,
	}
	param, ok := hpb.instanceParams[key]
	return param, ok
}

// GetAgentModel returns the agent-model level parameter if it exists.
func (hpb *HierarchicalParamBlender) GetAgentModel(
	agentType, modelID, paramName string,
) (*LearnedWeight, bool) {
	hpb.mu.RLock()
	defer hpb.mu.RUnlock()

	key := agentModelKey{
		AgentType: agentType,
		ModelID:   modelID,
		ParamName: paramName,
	}
	param, ok := hpb.agentModelParams[key]
	return param, ok
}

// GetModel returns the model-level parameter if it exists.
func (hpb *HierarchicalParamBlender) GetModel(
	modelID, paramName string,
) (*LearnedWeight, bool) {
	hpb.mu.RLock()
	defer hpb.mu.RUnlock()

	key := modelKey{
		ModelID:   modelID,
		ParamName: paramName,
	}
	param, ok := hpb.modelParams[key]
	return param, ok
}

// GetGlobal returns the global-level parameter if it exists.
func (hpb *HierarchicalParamBlender) GetGlobal(
	paramName string,
) (*LearnedWeight, bool) {
	hpb.mu.RLock()
	defer hpb.mu.RUnlock()

	key := globalKey{ParamName: paramName}
	param, ok := hpb.globalParams[key]
	return param, ok
}

// =============================================================================
// Blending Methods
// =============================================================================

// GetBlended returns a blended LearnedWeight by combining parameters
// from all available hierarchy levels based on their confidence.
//
// The blending process:
// 1. Collect parameters from instance -> agent-model -> model -> global
// 2. Compute weight for each level based on confidence
// 3. Blend the means using normalized weights
// 4. Return combined result with transparency about weight sources
//
// If no parameters exist at any level, returns nil and false.
func (hpb *HierarchicalParamBlender) GetBlended(
	agentType, modelID, instanceID, paramName string,
) (*BlendedParams, bool) {
	hpb.mu.RLock()
	defer hpb.mu.RUnlock()

	// Collect available parameters at each level
	levels := hpb.collectLeveledParams(agentType, modelID, instanceID, paramName)
	if len(levels) == 0 {
		return nil, false
	}

	// Compute blend weights based on confidence
	return hpb.blendParams(levels), true
}

// GetBlendedWeight returns a new LearnedWeight representing the blended
// distribution across hierarchy levels.
//
// This creates a synthetic LearnedWeight with:
// - Mean equal to the blended value
// - Confidence based on the effective confidence
// - Alpha/Beta set to produce the target mean with appropriate precision
func (hpb *HierarchicalParamBlender) GetBlendedWeight(
	agentType, modelID, instanceID, paramName string,
) (*LearnedWeight, bool) {
	blended, ok := hpb.GetBlended(agentType, modelID, instanceID, paramName)
	if !ok {
		return nil, false
	}

	// Create a LearnedWeight with the blended mean and confidence
	// Use confidence to determine the precision (alpha + beta)
	// Higher confidence = higher precision = tighter distribution
	precision := 2.0 + blended.EffectiveConfidence*50.0 // Range: 2 to 52

	alpha := blended.Value * precision
	beta := (1.0 - blended.Value) * precision

	lw := &LearnedWeight{
		Alpha:            alpha,
		Beta:             beta,
		EffectiveSamples: precision,
		PriorAlpha:       alpha,
		PriorBeta:        beta,
	}

	return lw, true
}

// collectLeveledParams gathers parameters from all available hierarchy levels.
func (hpb *HierarchicalParamBlender) collectLeveledParams(
	agentType, modelID, instanceID, paramName string,
) []leveledParam {
	var levels []leveledParam

	// Check instance level
	instanceK := instanceKey{
		AgentType:  agentType,
		ModelID:    modelID,
		InstanceID: instanceID,
		ParamName:  paramName,
	}
	if param, ok := hpb.instanceParams[instanceK]; ok {
		levels = append(levels, leveledParam{
			Param:      param,
			Confidence: param.Confidence(),
			Level:      LevelInstance,
		})
	}

	// Check agent-model level
	agentModelK := agentModelKey{
		AgentType: agentType,
		ModelID:   modelID,
		ParamName: paramName,
	}
	if param, ok := hpb.agentModelParams[agentModelK]; ok {
		levels = append(levels, leveledParam{
			Param:      param,
			Confidence: param.Confidence(),
			Level:      LevelAgentModel,
		})
	}

	// Check model level
	modelK := modelKey{
		ModelID:   modelID,
		ParamName: paramName,
	}
	if param, ok := hpb.modelParams[modelK]; ok {
		levels = append(levels, leveledParam{
			Param:      param,
			Confidence: param.Confidence(),
			Level:      LevelModel,
		})
	}

	// Check global level
	globalK := globalKey{ParamName: paramName}
	if param, ok := hpb.globalParams[globalK]; ok {
		levels = append(levels, leveledParam{
			Param:      param,
			Confidence: param.Confidence(),
			Level:      LevelGlobal,
		})
	}

	return levels
}

// blendParams computes the weighted blend of parameters across levels.
func (hpb *HierarchicalParamBlender) blendParams(levels []leveledParam) *BlendedParams {
	result := &BlendedParams{}

	// Compute raw weights for each level
	weights := make([]float64, len(levels))
	totalWeight := 0.0

	for i, lp := range levels {
		w := hpb.computeBlendWeight(lp.Level, lp.Confidence)
		weights[i] = w
		totalWeight += w
	}

	// Normalize weights and compute blended value
	if totalWeight < 1e-10 {
		// All weights essentially zero, use uniform weighting
		totalWeight = float64(len(levels))
		for i := range weights {
			weights[i] = 1.0
		}
	}

	blendedValue := 0.0
	effectiveConfidence := 0.0
	maxWeight := 0.0
	dominantLevel := LevelGlobal

	for i, lp := range levels {
		normalizedWeight := weights[i] / totalWeight
		param := lp.Param.(*LearnedWeight)
		paramValue := param.Mean()

		blendedValue += normalizedWeight * paramValue
		effectiveConfidence += normalizedWeight * lp.Confidence

		// Track which level contributes most
		if normalizedWeight > maxWeight {
			maxWeight = normalizedWeight
			dominantLevel = lp.Level
		}

		// Store weights by level
		switch lp.Level {
		case LevelInstance:
			result.InstanceWeight = normalizedWeight
		case LevelAgentModel:
			result.AgentModelWeight = normalizedWeight
		case LevelModel:
			result.ModelWeight = normalizedWeight
		case LevelGlobal:
			result.GlobalWeight = normalizedWeight
		}
	}

	result.Value = blendedValue
	result.EffectiveConfidence = effectiveConfidence
	result.DominantLevel = dominantLevel

	return result
}

// computeBlendWeight determines the weight contribution for a level
// based on its confidence. Higher confidence = higher weight.
//
// The formula is: weight = (confidence ^ exponent) * levelMultiplier
//
// Where levelMultiplier provides slight preference for more specific levels
// when confidence is similar, encouraging learning at finer granularity.
func (hpb *HierarchicalParamBlender) computeBlendWeight(level BlendLevel, confidence float64) float64 {
	if confidence < hpb.config.MinConfidenceForWeight {
		return 0.0
	}

	// Apply confidence exponent
	weight := math.Pow(confidence, hpb.config.ConfidenceExponent)

	// Add exploration bonus for more specific levels
	// This encourages using instance-level data when available
	levelBonus := 0.0
	switch level {
	case LevelInstance:
		levelBonus = hpb.config.ExplorationBonus * 3.0
	case LevelAgentModel:
		levelBonus = hpb.config.ExplorationBonus * 2.0
	case LevelModel:
		levelBonus = hpb.config.ExplorationBonus * 1.0
	case LevelGlobal:
		levelBonus = 0.0
	}

	return weight + levelBonus
}

// =============================================================================
// Specialized Blending Functions
// =============================================================================

// blendWeight computes a blended weight value from leveled parameters.
// Used for parameters in [0,1] range like thresholds and ratios.
//
// If explore is true, adds exploration bonus to encourage learning
// from more specific levels even with lower confidence.
func blendWeight(levels []leveledParam, explore bool) float64 {
	if len(levels) == 0 {
		return 0.5 // Default neutral value
	}

	config := DefaultBlenderConfig()
	if explore {
		config.ExplorationBonus = 0.1
	}

	blender := &HierarchicalParamBlender{config: config}
	result := blender.blendParams(levels)
	return result.Value
}

// blendContextSize computes a blended context size from leveled parameters.
// Used for positive integer parameters like buffer sizes and token counts.
//
// If explore is true, adds exploration bonus to prefer specific levels.
func blendContextSize(levels []leveledParam, explore bool) int {
	if len(levels) == 0 {
		return 100 // Default context size
	}

	config := DefaultBlenderConfig()
	if explore {
		config.ExplorationBonus = 0.1
	}

	blender := &HierarchicalParamBlender{config: config}
	result := blender.blendParams(levels)

	// Round to nearest integer, ensure positive
	size := int(math.Round(result.Value))
	if size < 1 {
		size = 1
	}
	return size
}

// blendCount computes a blended count from leveled parameters.
// Used for non-negative integer parameters like number of items.
//
// If explore is true, adds exploration bonus to prefer specific levels.
func blendCount(levels []leveledParam, explore bool) int {
	if len(levels) == 0 {
		return 5 // Default count
	}

	config := DefaultBlenderConfig()
	if explore {
		config.ExplorationBonus = 0.1
	}

	blender := &HierarchicalParamBlender{config: config}
	result := blender.blendParams(levels)

	// Round to nearest integer, ensure non-negative
	count := int(math.Round(result.Value))
	if count < 0 {
		count = 0
	}
	return count
}

// =============================================================================
// Removal Methods
// =============================================================================

// RemoveInstance removes an instance-level parameter.
func (hpb *HierarchicalParamBlender) RemoveInstance(
	agentType, modelID, instanceID, paramName string,
) bool {
	hpb.mu.Lock()
	defer hpb.mu.Unlock()

	key := instanceKey{
		AgentType:  agentType,
		ModelID:    modelID,
		InstanceID: instanceID,
		ParamName:  paramName,
	}
	if _, ok := hpb.instanceParams[key]; ok {
		delete(hpb.instanceParams, key)
		return true
	}
	return false
}

// RemoveAgentModel removes an agent-model level parameter.
func (hpb *HierarchicalParamBlender) RemoveAgentModel(
	agentType, modelID, paramName string,
) bool {
	hpb.mu.Lock()
	defer hpb.mu.Unlock()

	key := agentModelKey{
		AgentType: agentType,
		ModelID:   modelID,
		ParamName: paramName,
	}
	if _, ok := hpb.agentModelParams[key]; ok {
		delete(hpb.agentModelParams, key)
		return true
	}
	return false
}

// RemoveModel removes a model-level parameter.
func (hpb *HierarchicalParamBlender) RemoveModel(
	modelID, paramName string,
) bool {
	hpb.mu.Lock()
	defer hpb.mu.Unlock()

	key := modelKey{
		ModelID:   modelID,
		ParamName: paramName,
	}
	if _, ok := hpb.modelParams[key]; ok {
		delete(hpb.modelParams, key)
		return true
	}
	return false
}

// RemoveGlobal removes a global-level parameter.
func (hpb *HierarchicalParamBlender) RemoveGlobal(paramName string) bool {
	hpb.mu.Lock()
	defer hpb.mu.Unlock()

	key := globalKey{ParamName: paramName}
	if _, ok := hpb.globalParams[key]; ok {
		delete(hpb.globalParams, key)
		return true
	}
	return false
}

// =============================================================================
// Statistics and Debugging
// =============================================================================

// Stats returns statistics about the blender's registered parameters.
type BlenderStats struct {
	InstanceCount   int `json:"instance_count"`
	AgentModelCount int `json:"agent_model_count"`
	ModelCount      int `json:"model_count"`
	GlobalCount     int `json:"global_count"`
	TotalCount      int `json:"total_count"`
}

// GetStats returns statistics about registered parameters.
func (hpb *HierarchicalParamBlender) GetStats() BlenderStats {
	hpb.mu.RLock()
	defer hpb.mu.RUnlock()

	stats := BlenderStats{
		InstanceCount:   len(hpb.instanceParams),
		AgentModelCount: len(hpb.agentModelParams),
		ModelCount:      len(hpb.modelParams),
		GlobalCount:     len(hpb.globalParams),
	}
	stats.TotalCount = stats.InstanceCount + stats.AgentModelCount +
		stats.ModelCount + stats.GlobalCount

	return stats
}

// Clear removes all registered parameters.
func (hpb *HierarchicalParamBlender) Clear() {
	hpb.mu.Lock()
	defer hpb.mu.Unlock()

	hpb.instanceParams = make(map[instanceKey]*LearnedWeight)
	hpb.agentModelParams = make(map[agentModelKey]*LearnedWeight)
	hpb.modelParams = make(map[modelKey]*LearnedWeight)
	hpb.globalParams = make(map[globalKey]*LearnedWeight)
}

// =============================================================================
// JSON Serialization
// =============================================================================

// blenderJSON is used for JSON marshaling/unmarshaling.
type blenderJSON struct {
	Config           *BlenderConfig                      `json:"config"`
	InstanceParams   map[string]*LearnedWeight           `json:"instance_params"`
	AgentModelParams map[string]*LearnedWeight           `json:"agent_model_params"`
	ModelParams      map[string]*LearnedWeight           `json:"model_params"`
	GlobalParams     map[string]*LearnedWeight           `json:"global_params"`
}

// MarshalJSON implements json.Marshaler.
func (hpb *HierarchicalParamBlender) MarshalJSON() ([]byte, error) {
	hpb.mu.RLock()
	defer hpb.mu.RUnlock()

	// Convert map keys to strings for JSON serialization
	instanceJSON := make(map[string]*LearnedWeight)
	for k, v := range hpb.instanceParams {
		key := fmt.Sprintf("%s|%s|%s|%s", k.AgentType, k.ModelID, k.InstanceID, k.ParamName)
		instanceJSON[key] = v
	}

	agentModelJSON := make(map[string]*LearnedWeight)
	for k, v := range hpb.agentModelParams {
		key := fmt.Sprintf("%s|%s|%s", k.AgentType, k.ModelID, k.ParamName)
		agentModelJSON[key] = v
	}

	modelJSON := make(map[string]*LearnedWeight)
	for k, v := range hpb.modelParams {
		key := fmt.Sprintf("%s|%s", k.ModelID, k.ParamName)
		modelJSON[key] = v
	}

	globalJSON := make(map[string]*LearnedWeight)
	for k, v := range hpb.globalParams {
		globalJSON[k.ParamName] = v
	}

	return json.Marshal(blenderJSON{
		Config:           hpb.config,
		InstanceParams:   instanceJSON,
		AgentModelParams: agentModelJSON,
		ModelParams:      modelJSON,
		GlobalParams:     globalJSON,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (hpb *HierarchicalParamBlender) UnmarshalJSON(data []byte) error {
	var temp blenderJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	hpb.mu.Lock()
	defer hpb.mu.Unlock()

	if temp.Config != nil {
		hpb.config = temp.Config
	} else {
		hpb.config = DefaultBlenderConfig()
	}

	// Parse instance params
	hpb.instanceParams = make(map[instanceKey]*LearnedWeight)
	for keyStr, v := range temp.InstanceParams {
		key, err := parseInstanceKey(keyStr)
		if err == nil {
			hpb.instanceParams[key] = v
		}
	}

	// Parse agent-model params
	hpb.agentModelParams = make(map[agentModelKey]*LearnedWeight)
	for keyStr, v := range temp.AgentModelParams {
		key, err := parseAgentModelKey(keyStr)
		if err == nil {
			hpb.agentModelParams[key] = v
		}
	}

	// Parse model params
	hpb.modelParams = make(map[modelKey]*LearnedWeight)
	for keyStr, v := range temp.ModelParams {
		key, err := parseModelKey(keyStr)
		if err == nil {
			hpb.modelParams[key] = v
		}
	}

	// Parse global params
	hpb.globalParams = make(map[globalKey]*LearnedWeight)
	for keyStr, v := range temp.GlobalParams {
		hpb.globalParams[globalKey{ParamName: keyStr}] = v
	}

	return nil
}

// parseInstanceKey parses a pipe-separated instance key string.
func parseInstanceKey(s string) (instanceKey, error) {
	var key instanceKey
	n, _ := fmt.Sscanf(s, "%s|%s|%s|%s",
		&key.AgentType, &key.ModelID, &key.InstanceID, &key.ParamName)
	if n != 4 {
		// Try splitting manually for keys with special characters
		parts := splitKey(s, 4)
		if len(parts) == 4 {
			key.AgentType = parts[0]
			key.ModelID = parts[1]
			key.InstanceID = parts[2]
			key.ParamName = parts[3]
			return key, nil
		}
		return key, fmt.Errorf("invalid instance key: %s", s)
	}
	return key, nil
}

// parseAgentModelKey parses a pipe-separated agent-model key string.
func parseAgentModelKey(s string) (agentModelKey, error) {
	var key agentModelKey
	parts := splitKey(s, 3)
	if len(parts) == 3 {
		key.AgentType = parts[0]
		key.ModelID = parts[1]
		key.ParamName = parts[2]
		return key, nil
	}
	return key, fmt.Errorf("invalid agent-model key: %s", s)
}

// parseModelKey parses a pipe-separated model key string.
func parseModelKey(s string) (modelKey, error) {
	var key modelKey
	parts := splitKey(s, 2)
	if len(parts) == 2 {
		key.ModelID = parts[0]
		key.ParamName = parts[1]
		return key, nil
	}
	return key, fmt.Errorf("invalid model key: %s", s)
}

// splitKey splits a string by '|' into expected number of parts.
func splitKey(s string, expected int) []string {
	result := make([]string, 0, expected)
	start := 0
	count := 0

	for i := 0; i < len(s); i++ {
		if s[i] == '|' {
			result = append(result, s[start:i])
			start = i + 1
			count++
			if count >= expected-1 {
				break
			}
		}
	}

	// Add the last part
	if start < len(s) {
		result = append(result, s[start:])
	}

	return result
}
