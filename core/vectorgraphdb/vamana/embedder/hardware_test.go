package embedder

import (
	"runtime"
	"testing"
)

func TestDetectHardware(t *testing.T) {
	ResetHardwareCache()

	caps := DetectHardware()

	if caps.CPUCores <= 0 {
		t.Errorf("Expected positive CPU cores, got %d", caps.CPUCores)
	}

	if caps.CPUCores != runtime.NumCPU() {
		t.Errorf("Expected %d CPU cores, got %d", runtime.NumCPU(), caps.CPUCores)
	}

	if caps.SystemRAMGB <= 0 {
		t.Errorf("Expected positive RAM, got %f GB", caps.SystemRAMGB)
	}
}

func TestDetectHardware_Cached(t *testing.T) {
	ResetHardwareCache()

	caps1 := DetectHardware()
	caps2 := DetectHardware()

	if caps1.CPUCores != caps2.CPUCores {
		t.Error("Cached results should be identical")
	}

	if caps1.SystemRAMGB != caps2.SystemRAMGB {
		t.Error("Cached results should be identical")
	}
}

func TestHardwareCapabilities_SelectModelTier(t *testing.T) {
	tests := []struct {
		name     string
		caps     HardwareCapabilities
		expected ModelTier
	}{
		{
			name: "high-end GPU",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: true,
				VRAMGB:       8.0,
				SystemRAMGB:  32.0,
				CPUCores:     16,
			},
			expected: TierQwen3,
		},
		{
			name: "low-end GPU",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: true,
				VRAMGB:       1.5,
				SystemRAMGB:  8.0,
				CPUCores:     4,
			},
			expected: TierGTELarge,
		},
		{
			name: "no GPU, good RAM",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: false,
				VRAMGB:       0,
				SystemRAMGB:  8.0,
				CPUCores:     4,
			},
			expected: TierGTELarge,
		},
		{
			name: "no GPU, low RAM",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: false,
				VRAMGB:       0,
				SystemRAMGB:  1.5,
				CPUCores:     2,
			},
			expected: TierHybridLocal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier := tt.caps.SelectModelTier()
			if tier != tt.expected {
				t.Errorf("Expected tier %v, got %v", tt.expected, tier)
			}
		})
	}
}

func TestModelTier_String(t *testing.T) {
	tests := []struct {
		tier     ModelTier
		expected string
	}{
		{TierQwen3, "Qwen3-Embedding-0.6B"},
		{TierGTELarge, "gte-large-en-v1.5"},
		{TierHybridLocal, "HybridLocal"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.tier.String() != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, tt.tier.String())
			}
		})
	}
}

func TestModelTier_Spec(t *testing.T) {
	for tier, expectedSpec := range ModelRegistry {
		spec := tier.Spec()
		if spec.Name != expectedSpec.Name {
			t.Errorf("Tier %v: expected name %s, got %s", tier, expectedSpec.Name, spec.Name)
		}
		if spec.Dimension != EmbeddingDimension {
			t.Errorf("Tier %v: expected dimension %d, got %d", tier, EmbeddingDimension, spec.Dimension)
		}
	}
}
