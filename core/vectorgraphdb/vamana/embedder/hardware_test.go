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

func TestHardwareCapabilities_SelectHighQualityTier(t *testing.T) {
	tests := []struct {
		name     string
		caps     HardwareCapabilities
		expected ModelTier
	}{
		{
			name: "high RAM selects 4B",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: true,
				VRAMGB:       8.0,
				SystemRAMGB:  32.0,
				CPUCores:     16,
			},
			expected: TierQwen3_4B,
		},
		{
			name: "medium RAM selects 4B",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: false,
				VRAMGB:       0,
				SystemRAMGB:  8.0,
				CPUCores:     4,
			},
			expected: TierQwen3_4B,
		},
		{
			name: "6GB RAM threshold selects 4B",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: false,
				VRAMGB:       0,
				SystemRAMGB:  6.0,
				CPUCores:     4,
			},
			expected: TierQwen3_4B,
		},
		{
			name: "low RAM selects 0.6B",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: false,
				VRAMGB:       0,
				SystemRAMGB:  4.0,
				CPUCores:     2,
			},
			expected: TierQwen3_0_6B,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier := tt.caps.SelectHighQualityTier()
			if tier != tt.expected {
				t.Errorf("Expected tier %v, got %v", tt.expected, tier)
			}
		})
	}
}

func TestHardwareCapabilities_CanUseGPU(t *testing.T) {
	tests := []struct {
		name     string
		caps     HardwareCapabilities
		tier     ModelTier
		expected bool
	}{
		{
			name: "no GPU",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: false,
				VRAMGB:       0,
			},
			tier:     TierQwen3_4B,
			expected: false,
		},
		{
			name: "GPU with sufficient VRAM for 4B",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: true,
				VRAMGB:       4.0,
			},
			tier:     TierQwen3_4B,
			expected: true,
		},
		{
			name: "GPU with insufficient VRAM for 4B",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: true,
				VRAMGB:       1.0,
			},
			tier:     TierQwen3_4B,
			expected: false,
		},
		{
			name: "GPU with sufficient VRAM for 0.6B",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: true,
				VRAMGB:       1.0,
			},
			tier:     TierQwen3_0_6B,
			expected: true,
		},
		{
			name: "HybridLocal never uses GPU",
			caps: HardwareCapabilities{
				HasNVIDIAGPU: true,
				VRAMGB:       8.0,
			},
			tier:     TierHybridLocal,
			expected: true, // MinVRAMGB is 0 for HybridLocal
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.caps.CanUseGPU(tt.tier)
			if result != tt.expected {
				t.Errorf("Expected CanUseGPU=%v, got %v", tt.expected, result)
			}
		})
	}
}

func TestHardwareCapabilities_CanRunTier(t *testing.T) {
	tests := []struct {
		name     string
		caps     HardwareCapabilities
		tier     ModelTier
		expected bool
	}{
		{
			name: "sufficient RAM for 4B",
			caps: HardwareCapabilities{
				SystemRAMGB: 8.0,
			},
			tier:     TierQwen3_4B,
			expected: true,
		},
		{
			name: "insufficient RAM for 4B",
			caps: HardwareCapabilities{
				SystemRAMGB: 3.0,
			},
			tier:     TierQwen3_4B,
			expected: false,
		},
		{
			name: "sufficient RAM for 0.6B",
			caps: HardwareCapabilities{
				SystemRAMGB: 2.0,
			},
			tier:     TierQwen3_0_6B,
			expected: true,
		},
		{
			name: "HybridLocal always runs",
			caps: HardwareCapabilities{
				SystemRAMGB: 0.5,
			},
			tier:     TierHybridLocal,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.caps.CanRunTier(tt.tier)
			if result != tt.expected {
				t.Errorf("Expected CanRunTier=%v, got %v", tt.expected, result)
			}
		})
	}
}

func TestModelTier_String(t *testing.T) {
	tests := []struct {
		tier     ModelTier
		expected string
	}{
		{TierHybridLocal, "HybridLocal"},
		{TierQwen3_0_6B, "Qwen3-Embedding-0.6B"},
		{TierQwen3_4B, "Qwen3-Embedding-4B"},
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
