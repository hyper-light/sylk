package embedder

type ModelTier int

const (
	TierQwen3 ModelTier = iota
	TierGTELarge
	TierHybridLocal
)

type ModelSpec struct {
	Tier      ModelTier
	Name      string
	HFRepo    string
	Dimension int
	SizeBytes int64
	MTEBScore float64
	MinVRAMGB float64
	MinRAMGB  float64
	IsEncoder bool
	Quantized bool
}

const EmbeddingDimension = 1024

var ModelRegistry = map[ModelTier]ModelSpec{
	TierQwen3: {
		Tier:      TierQwen3,
		Name:      "Qwen3-Embedding-0.6B",
		HFRepo:    "onnx-community/Qwen3-Embedding-0.6B-ONNX",
		Dimension: EmbeddingDimension,
		SizeBytes: 1_200_000_000,
		MTEBScore: 70.70,
		MinVRAMGB: 2.0,
		MinRAMGB:  4.0,
		IsEncoder: false,
		Quantized: false,
	},
	TierGTELarge: {
		Tier:      TierGTELarge,
		Name:      "gte-large-en-v1.5",
		HFRepo:    "keisuke-miyako/gte-large-en-v1.5-onnx",
		Dimension: EmbeddingDimension,
		SizeBytes: 424_000_000,
		MTEBScore: 65.39,
		MinVRAMGB: 1.0,
		MinRAMGB:  2.0,
		IsEncoder: true,
		Quantized: true,
	},
	TierHybridLocal: {
		Tier:      TierHybridLocal,
		Name:      "HybridLocal",
		HFRepo:    "",
		Dimension: EmbeddingDimension,
		SizeBytes: 50_000_000,
		MTEBScore: 45.0,
		MinVRAMGB: 0,
		MinRAMGB:  0.5,
		IsEncoder: true,
		Quantized: false,
	},
}

func (t ModelTier) String() string {
	switch t {
	case TierQwen3:
		return "Qwen3-Embedding-0.6B"
	case TierGTELarge:
		return "gte-large-en-v1.5"
	case TierHybridLocal:
		return "HybridLocal"
	default:
		return "unknown"
	}
}

func (t ModelTier) Spec() ModelSpec {
	if spec, ok := ModelRegistry[t]; ok {
		return spec
	}
	return ModelRegistry[TierHybridLocal]
}
