package embedder

type ModelTier int

const (
	TierHybridLocal ModelTier = iota
	TierQwen3_0_6B
	TierQwen3_4B
)

type ModelFormat int

const (
	FormatNone ModelFormat = iota
	FormatGGUF
)

type ModelSpec struct {
	Tier        ModelTier
	Name        string
	Format      ModelFormat
	HFRepo      string
	GGUFFile    string
	Dimension   int
	MaxDim      int
	SizeBytes   int64
	MTEBScore   float64
	MinVRAMGB   float64
	MinRAMGB    float64
	SupportsGPU bool
	SupportsMRL bool
	Languages   int
	CodeSupport bool
}

const EmbeddingDimension = 1024

var ModelRegistry = map[ModelTier]ModelSpec{
	TierHybridLocal: {
		Tier:        TierHybridLocal,
		Name:        "HybridLocal",
		Format:      FormatNone,
		Dimension:   EmbeddingDimension,
		MaxDim:      EmbeddingDimension,
		SizeBytes:   0,
		MTEBScore:   50.0, // Enhanced hybrid with stemming, phonetics, minhash, etc.
		MinVRAMGB:   0,
		MinRAMGB:    0.5,
		SupportsGPU: false,
		SupportsMRL: false,
		Languages:   0,
		CodeSupport: true,
	},
	TierQwen3_0_6B: {
		Tier:        TierQwen3_0_6B,
		Name:        "Qwen3-Embedding-0.6B",
		Format:      FormatGGUF,
		HFRepo:      "Qwen/Qwen3-Embedding-0.6B-GGUF",
		GGUFFile:    "qwen3-embedding-0.6b-q8_0.gguf",
		Dimension:   EmbeddingDimension,
		MaxDim:      1024,
		SizeBytes:   639_000_000,
		MTEBScore:   70.70,
		MinVRAMGB:   1.0,
		MinRAMGB:    1.5,
		SupportsGPU: true,
		SupportsMRL: true,
		Languages:   100,
		CodeSupport: true,
	},
	TierQwen3_4B: {
		Tier:        TierQwen3_4B,
		Name:        "Qwen3-Embedding-4B",
		Format:      FormatGGUF,
		HFRepo:      "Qwen/Qwen3-Embedding-4B-GGUF",
		GGUFFile:    "qwen3-embedding-4b-q4_k_m.gguf",
		Dimension:   EmbeddingDimension,
		MaxDim:      2560,
		SizeBytes:   2_500_000_000,
		MTEBScore:   74.60,
		MinVRAMGB:   4.0,
		MinRAMGB:    6.0,
		SupportsGPU: true,
		SupportsMRL: true,
		Languages:   100,
		CodeSupport: true,
	},
}

func (t ModelTier) String() string {
	switch t {
	case TierHybridLocal:
		return "HybridLocal"
	case TierQwen3_0_6B:
		return "Qwen3-Embedding-0.6B"
	case TierQwen3_4B:
		return "Qwen3-Embedding-4B"
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

func (t ModelTier) IsHighQuality() bool {
	return t == TierQwen3_0_6B || t == TierQwen3_4B
}

func (t ModelTier) RequiresDownload() bool {
	spec := t.Spec()
	return spec.Format == FormatGGUF && spec.HFRepo != ""
}
