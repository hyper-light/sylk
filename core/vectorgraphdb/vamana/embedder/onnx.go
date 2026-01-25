package embedder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/options"
	"github.com/knights-analytics/hugot/pipelines"
)

type ONNXEmbedder struct {
	tier           ModelTier
	spec           ModelSpec
	cacheDir       string
	modelPath      string
	ortLibraryPath string
	fallback       *HybridLocalEmbedder
	session        *hugot.Session
	pipeline       *pipelines.FeatureExtractionPipeline
	mu             sync.RWMutex
	loaded         bool
	useGPU         bool
}

type ONNXConfig struct {
	Tier           ModelTier
	CacheDir       string
	UseGPU         bool
	OrtLibraryPath string
}

func NewONNXEmbedder(cfg ONNXConfig) (*ONNXEmbedder, error) {
	spec := cfg.Tier.Spec()

	if cfg.CacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		cfg.CacheDir = filepath.Join(home, ".sylk", "models")
	}

	if err := os.MkdirAll(cfg.CacheDir, 0755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	return &ONNXEmbedder{
		tier:           cfg.Tier,
		spec:           spec,
		cacheDir:       cfg.CacheDir,
		modelPath:      filepath.Join(cfg.CacheDir, spec.Name),
		ortLibraryPath: cfg.OrtLibraryPath,
		fallback:       NewHybridLocalEmbedder(),
		useGPU:         cfg.UseGPU,
	}, nil
}

func (o *ONNXEmbedder) Dimension() int {
	return o.spec.Dimension
}

func (o *ONNXEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if !o.isLoaded() {
		return o.fallback.Embed(ctx, text)
	}

	results, err := o.runInference(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return results[0], nil
}

func (o *ONNXEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if !o.isLoaded() {
		return o.fallback.EmbedBatch(ctx, texts)
	}

	return o.runInference(ctx, texts)
}

func (o *ONNXEmbedder) isLoaded() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.loaded
}

func (o *ONNXEmbedder) runInference(_ context.Context, texts []string) ([][]float32, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if o.pipeline == nil {
		return nil, fmt.Errorf("pipeline not initialized")
	}

	output, err := o.pipeline.RunPipeline(texts)
	if err != nil {
		return nil, fmt.Errorf("inference failed: %w", err)
	}

	return output.Embeddings, nil
}

func (o *ONNXEmbedder) EnsureModel(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.loaded {
		return nil
	}

	if _, err := os.Stat(o.modelPath); os.IsNotExist(err) {
		if err := o.downloadModel(ctx); err != nil {
			return fmt.Errorf("download model: %w", err)
		}
	}

	if err := o.loadModel(); err != nil {
		return fmt.Errorf("load model: %w", err)
	}

	o.loaded = true
	return nil
}

func (o *ONNXEmbedder) downloadModel(_ context.Context) error {
	if o.spec.HFRepo == "" {
		return fmt.Errorf("no HuggingFace repo specified for model %s", o.spec.Name)
	}

	downloadOpts := hugot.NewDownloadOptions()
	modelPath, err := hugot.DownloadModel(o.spec.HFRepo, o.cacheDir, downloadOpts)
	if err != nil {
		return fmt.Errorf("download from HuggingFace: %w", err)
	}

	o.modelPath = modelPath
	return nil
}

func (o *ONNXEmbedder) loadModel() error {
	sessionOpts := []options.WithOption{
		options.WithIntraOpNumThreads(runtime.NumCPU()),
	}

	if o.ortLibraryPath != "" {
		sessionOpts = append(sessionOpts, options.WithOnnxLibraryPath(o.ortLibraryPath))
	}

	if o.useGPU {
		sessionOpts = append(sessionOpts, options.WithCuda(nil))
	}

	session, err := hugot.NewORTSession(sessionOpts...)
	if err != nil {
		return fmt.Errorf("create ORT session: %w", err)
	}

	pipelineConfig := hugot.FeatureExtractionConfig{
		ModelPath: o.modelPath,
		Name:      o.spec.Name,
	}

	pipeline, err := hugot.NewPipeline(session, pipelineConfig)
	if err != nil {
		session.Destroy()
		return fmt.Errorf("create pipeline: %w", err)
	}

	o.session = session
	o.pipeline = pipeline
	return nil
}

func (o *ONNXEmbedder) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.session != nil {
		o.session.Destroy()
		o.session = nil
	}
	o.pipeline = nil
	o.loaded = false
	return nil
}

func (o *ONNXEmbedder) ModelSpec() ModelSpec {
	return o.spec
}

func (o *ONNXEmbedder) IsReady() bool {
	return o.isLoaded()
}

func (o *ONNXEmbedder) Fallback() Embedder {
	return o.fallback
}
