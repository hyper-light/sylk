package quantization

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"
)

func lopqMakeClusteredVectors(n, dim, k int, seed int64) [][]float32 {
	rng := rand.New(rand.NewSource(seed))
	vectors := make([][]float32, n)

	centers := make([][]float32, k)
	for i := 0; i < k; i++ {
		centers[i] = make([]float32, dim)
		for d := 0; d < dim; d++ {
			centers[i][d] = rng.Float32()*20 - 10
		}
	}

	for i := 0; i < n; i++ {
		cluster := i % k
		vectors[i] = make([]float32, dim)
		for d := 0; d < dim; d++ {
			noise := float32(rng.NormFloat64() * 0.5)
			vectors[i][d] = centers[cluster][d] + noise
		}
	}

	return vectors
}

func lopqMakeRandomVectors(n, dim int, seed int64) [][]float32 {
	rng := rand.New(rand.NewSource(seed))
	vectors := make([][]float32, n)
	for i := 0; i < n; i++ {
		vectors[i] = make([]float32, dim)
		for d := 0; d < dim; d++ {
			vectors[i][d] = rng.Float32()*2 - 1
		}
	}
	return vectors
}

func TestLOPQCode(t *testing.T) {
	t.Run("HasLocalCode", func(t *testing.T) {
		tests := []struct {
			name     string
			code     LOPQCode
			expected bool
		}{
			{
				name:     "with local code",
				code:     LOPQCode{Partition: 1, LocalCode: PQCode{1, 2, 3}},
				expected: true,
			},
			{
				name:     "without local code",
				code:     LOPQCode{Partition: 1, LocalCode: nil},
				expected: false,
			},
			{
				name:     "empty local code",
				code:     LOPQCode{Partition: 1, LocalCode: PQCode{}},
				expected: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := tt.code.HasLocalCode(); got != tt.expected {
					t.Errorf("HasLocalCode() = %v, want %v", got, tt.expected)
				}
			})
		}
	})

	t.Run("HasFallbackCode", func(t *testing.T) {
		tests := []struct {
			name     string
			code     LOPQCode
			expected bool
		}{
			{
				name:     "with fallback code",
				code:     LOPQCode{Partition: 1, FallbackCode: RaBitQCode{0xFF}},
				expected: true,
			},
			{
				name:     "without fallback code",
				code:     LOPQCode{Partition: 1, FallbackCode: nil},
				expected: false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := tt.code.HasFallbackCode(); got != tt.expected {
					t.Errorf("HasFallbackCode() = %v, want %v", got, tt.expected)
				}
			})
		}
	})

	t.Run("Clone", func(t *testing.T) {
		original := LOPQCode{
			Partition:    PartitionID(42),
			LocalCode:    PQCode{1, 2, 3, 4},
			FallbackCode: RaBitQCode{0xAB, 0xCD},
		}

		clone := original.Clone()

		if !clone.Equal(original) {
			t.Error("Clone() is not equal to original")
		}

		if original.LocalCode != nil && clone.LocalCode != nil {
			clone.LocalCode[0] = 255
			if original.LocalCode[0] == 255 {
				t.Error("Clone() did not deep copy LocalCode")
			}
		}
	})

	t.Run("Equal", func(t *testing.T) {
		tests := []struct {
			name     string
			a        LOPQCode
			b        LOPQCode
			expected bool
		}{
			{
				name:     "identical codes",
				a:        LOPQCode{Partition: 1, LocalCode: PQCode{1, 2, 3}},
				b:        LOPQCode{Partition: 1, LocalCode: PQCode{1, 2, 3}},
				expected: true,
			},
			{
				name:     "different partition",
				a:        LOPQCode{Partition: 1, LocalCode: PQCode{1, 2, 3}},
				b:        LOPQCode{Partition: 2, LocalCode: PQCode{1, 2, 3}},
				expected: false,
			},
			{
				name:     "different local code",
				a:        LOPQCode{Partition: 1, LocalCode: PQCode{1, 2, 3}},
				b:        LOPQCode{Partition: 1, LocalCode: PQCode{1, 2, 4}},
				expected: false,
			},
			{
				name:     "both nil local codes",
				a:        LOPQCode{Partition: 1, LocalCode: nil},
				b:        LOPQCode{Partition: 1, LocalCode: nil},
				expected: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := tt.a.Equal(tt.b); got != tt.expected {
					t.Errorf("Equal() = %v, want %v", got, tt.expected)
				}
			})
		}
	})

	t.Run("String", func(t *testing.T) {
		tests := []struct {
			name string
			code LOPQCode
		}{
			{
				name: "with local code",
				code: LOPQCode{Partition: 5, LocalCode: PQCode{1, 2}},
			},
			{
				name: "with fallback code",
				code: LOPQCode{Partition: 3, FallbackCode: RaBitQCode{0xFF}},
			},
			{
				name: "no code",
				code: LOPQCode{Partition: 7},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				str := tt.code.String()
				if len(str) == 0 {
					t.Error("String() returned empty string")
				}
			})
		}
	})
}

func TestPartitionID(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		tests := []struct {
			name     string
			id       PartitionID
			expected bool
		}{
			{"valid zero", PartitionID(0), true},
			{"valid 100", PartitionID(100), true},
			{"valid max-1", PartitionID(0xFFFE), true},
			{"invalid", InvalidPartitionID, false},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := tt.id.Valid(); got != tt.expected {
					t.Errorf("Valid() = %v, want %v", got, tt.expected)
				}
			})
		}
	})

	t.Run("String", func(t *testing.T) {
		tests := []struct {
			id PartitionID
		}{
			{PartitionID(0)},
			{PartitionID(42)},
			{InvalidPartitionID},
		}

		for _, tt := range tests {
			str := tt.id.String()
			if len(str) == 0 {
				t.Errorf("String() for %v returned empty", tt.id)
			}
		}
	})
}

func TestLOPQConfig(t *testing.T) {
	t.Run("DefaultLOPQConfig", func(t *testing.T) {
		config := DefaultLOPQConfig()

		if config.NumPartitions != 1024 {
			t.Errorf("NumPartitions = %d, want 1024", config.NumPartitions)
		}
		if config.TrainingThreshold != 100 {
			t.Errorf("TrainingThreshold = %d, want 100", config.TrainingThreshold)
		}
		if config.FallbackSeed != 42 {
			t.Errorf("FallbackSeed = %d, want 42", config.FallbackSeed)
		}
		if config.VectorDimension != 0 {
			t.Errorf("VectorDimension = %d, want 0", config.VectorDimension)
		}
	})

	t.Run("Validate", func(t *testing.T) {
		tests := []struct {
			name        string
			config      LOPQConfig
			expectError error
		}{
			{
				name: "valid config",
				config: LOPQConfig{
					VectorDimension:   128,
					NumPartitions:     16,
					TrainingThreshold: 10,
				},
				expectError: nil,
			},
			{
				name: "zero dimension",
				config: LOPQConfig{
					VectorDimension:   0,
					NumPartitions:     16,
					TrainingThreshold: 10,
				},
				expectError: ErrLOPQInvalidDimension,
			},
			{
				name: "negative dimension",
				config: LOPQConfig{
					VectorDimension:   -1,
					NumPartitions:     16,
					TrainingThreshold: 10,
				},
				expectError: ErrLOPQInvalidDimension,
			},
			{
				name: "zero partitions",
				config: LOPQConfig{
					VectorDimension:   128,
					NumPartitions:     0,
					TrainingThreshold: 10,
				},
				expectError: ErrLOPQInvalidPartitionCount,
			},
			{
				name: "negative partitions",
				config: LOPQConfig{
					VectorDimension:   128,
					NumPartitions:     -1,
					TrainingThreshold: 10,
				},
				expectError: ErrLOPQInvalidPartitionCount,
			},
			{
				name: "zero threshold",
				config: LOPQConfig{
					VectorDimension:   128,
					NumPartitions:     16,
					TrainingThreshold: 0,
				},
				expectError: ErrLOPQInvalidThreshold,
			},
			{
				name: "negative threshold",
				config: LOPQConfig{
					VectorDimension:   128,
					NumPartitions:     16,
					TrainingThreshold: -1,
				},
				expectError: ErrLOPQInvalidThreshold,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				err := tt.config.Validate()
				if tt.expectError == nil {
					if err != nil {
						t.Errorf("Validate() = %v, want nil", err)
					}
				} else {
					if err != tt.expectError {
						t.Errorf("Validate() = %v, want %v", err, tt.expectError)
					}
				}
			})
		}
	})

	t.Run("NumPartitions bounds", func(t *testing.T) {
		partitionCounts := []int{1, 2, 8, 16, 64, 256, 1024, 4096}

		for _, count := range partitionCounts {
			config := LOPQConfig{
				VectorDimension:   64,
				NumPartitions:     count,
				TrainingThreshold: 10,
			}
			if err := config.Validate(); err != nil {
				t.Errorf("Validate() with %d partitions = %v, want nil", count, err)
			}
		}
	})

	t.Run("String", func(t *testing.T) {
		config := LOPQConfig{
			VectorDimension:   128,
			NumPartitions:     64,
			TrainingThreshold: 50,
		}
		str := config.String()
		if len(str) == 0 {
			t.Error("String() returned empty")
		}
	})
}

func TestLOPQCoarseQuantizer(t *testing.T) {
	t.Run("NewCoarseQuantizer", func(t *testing.T) {
		config := LOPQConfig{
			VectorDimension:   32,
			NumPartitions:     8,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, err := NewCoarseQuantizer(config)
		if err != nil {
			t.Fatalf("NewCoarseQuantizer() error = %v", err)
		}
		if cq == nil {
			t.Fatal("NewCoarseQuantizer() returned nil")
		}
		if cq.IsTrained() {
			t.Error("IsTrained() = true for new quantizer")
		}
	})

	t.Run("NewCoarseQuantizer with invalid config", func(t *testing.T) {
		config := LOPQConfig{
			VectorDimension:   0,
			NumPartitions:     8,
			TrainingThreshold: 10,
		}

		_, err := NewCoarseQuantizer(config)
		if err == nil {
			t.Error("NewCoarseQuantizer() with invalid config should return error")
		}
	})

	t.Run("Train produces expected centroids", func(t *testing.T) {
		dim := 16
		numPartitions := 4
		numVectors := 200

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, err := NewCoarseQuantizer(config)
		if err != nil {
			t.Fatalf("NewCoarseQuantizer() error = %v", err)
		}

		vectors := lopqMakeClusteredVectors(numVectors, dim, numPartitions, 42)

		err = cq.Train(context.Background(), vectors)
		if err != nil {
			t.Fatalf("Train() error = %v", err)
		}

		if !cq.IsTrained() {
			t.Error("IsTrained() = false after training")
		}

		if cq.NumPartitions() != numPartitions {
			t.Errorf("NumPartitions() = %d, want %d", cq.NumPartitions(), numPartitions)
		}

		if cq.TrainedAt().IsZero() {
			t.Error("TrainedAt() is zero")
		}

		centroids := cq.Centroids()
		if len(centroids) != numPartitions {
			t.Errorf("len(Centroids()) = %d, want %d", len(centroids), numPartitions)
		}
	})

	t.Run("Train with empty vectors", func(t *testing.T) {
		config := LOPQConfig{
			VectorDimension:   16,
			NumPartitions:     4,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		err := cq.Train(context.Background(), [][]float32{})
		if err == nil {
			t.Error("Train() with empty vectors should return error")
		}
	})

	t.Run("Train with dimension mismatch", func(t *testing.T) {
		config := LOPQConfig{
			VectorDimension:   16,
			NumPartitions:     4,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeRandomVectors(100, 32, 42)

		err := cq.Train(context.Background(), vectors)
		if err == nil {
			t.Error("Train() with wrong dimension should return error")
		}
	})

	t.Run("GetPartition returns valid IDs", func(t *testing.T) {
		dim := 16
		numPartitions := 4

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		for i := 0; i < 10; i++ {
			partition := cq.GetPartition(vectors[i])
			if !partition.Valid() {
				t.Errorf("GetPartition() returned invalid partition for vector %d", i)
			}
			if int(partition) >= numPartitions {
				t.Errorf("GetPartition() = %d, want < %d", partition, numPartitions)
			}
		}
	})

	t.Run("GetPartition deterministic", func(t *testing.T) {
		dim := 16
		numPartitions := 4

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		testVec := vectors[0]
		partition1 := cq.GetPartition(testVec)
		partition2 := cq.GetPartition(testVec)

		if partition1 != partition2 {
			t.Errorf("GetPartition() not deterministic: %d != %d", partition1, partition2)
		}
	})

	t.Run("GetPartition before training", func(t *testing.T) {
		config := LOPQConfig{
			VectorDimension:   16,
			NumPartitions:     4,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vec := make([]float32, 16)

		partition := cq.GetPartition(vec)
		if partition != InvalidPartitionID {
			t.Errorf("GetPartition() before training = %d, want InvalidPartitionID", partition)
		}
	})

	t.Run("GetPartitionBatch", func(t *testing.T) {
		dim := 16
		numPartitions := 4

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		partitions := cq.GetPartitionBatch(vectors[:10])
		if len(partitions) != 10 {
			t.Errorf("GetPartitionBatch() returned %d partitions, want 10", len(partitions))
		}

		for i := 0; i < 10; i++ {
			expected := cq.GetPartition(vectors[i])
			if partitions[i] != expected {
				t.Errorf("GetPartitionBatch()[%d] = %d, GetPartition() = %d", i, partitions[i], expected)
			}
		}
	})

	t.Run("GetCentroid", func(t *testing.T) {
		dim := 16
		numPartitions := 4

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		centroid, err := cq.GetCentroid(PartitionID(0))
		if err != nil {
			t.Fatalf("GetCentroid() error = %v", err)
		}
		if len(centroid) != dim {
			t.Errorf("len(centroid) = %d, want %d", len(centroid), dim)
		}
	})

	t.Run("GetCentroid invalid partition", func(t *testing.T) {
		dim := 16
		numPartitions := 4

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		_, err := cq.GetCentroid(InvalidPartitionID)
		if err == nil {
			t.Error("GetCentroid(InvalidPartitionID) should return error")
		}

		_, err = cq.GetCentroid(PartitionID(100))
		if err == nil {
			t.Error("GetCentroid() with out of range partition should return error")
		}
	})

	t.Run("ComputeResidual", func(t *testing.T) {
		dim := 16
		numPartitions := 4

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		vec := vectors[0]
		partition := cq.GetPartition(vec)

		residual, err := cq.ComputeResidual(vec, partition)
		if err != nil {
			t.Fatalf("ComputeResidual() error = %v", err)
		}
		if len(residual) != dim {
			t.Errorf("len(residual) = %d, want %d", len(residual), dim)
		}

		centroid, _ := cq.GetCentroid(partition)
		for d := 0; d < dim; d++ {
			expected := vec[d] - centroid[d]
			if math.Abs(float64(residual[d]-expected)) > 1e-6 {
				t.Errorf("residual[%d] = %f, want %f", d, residual[d], expected)
			}
		}
	})

	t.Run("GetNearestPartitions", func(t *testing.T) {
		dim := 16
		numPartitions := 8

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(200, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		vec := vectors[0]
		nearest, err := cq.GetNearestPartitions(vec, 3)
		if err != nil {
			t.Fatalf("GetNearestPartitions() error = %v", err)
		}
		if len(nearest) != 3 {
			t.Errorf("len(nearest) = %d, want 3", len(nearest))
		}

		expected := cq.GetPartition(vec)
		if nearest[0] != expected {
			t.Errorf("nearest[0] = %d, want %d (same as GetPartition)", nearest[0], expected)
		}
	})
}

func TestLOPQTrainer(t *testing.T) {
	t.Run("NewLocalCodebookTrainer", func(t *testing.T) {
		dim := 16
		numPartitions := 4

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		trainer, err := NewLocalCodebookTrainer(config, cq)
		if err != nil {
			t.Fatalf("NewLocalCodebookTrainer() error = %v", err)
		}
		if trainer == nil {
			t.Fatal("NewLocalCodebookTrainer() returned nil")
		}
	})

	t.Run("NewLocalCodebookTrainer without trained coarse", func(t *testing.T) {
		config := LOPQConfig{
			VectorDimension:   16,
			NumPartitions:     4,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)

		_, err := NewLocalCodebookTrainer(config, cq)
		if err == nil {
			t.Error("NewLocalCodebookTrainer() with untrained coarse should return error")
		}
	})

	t.Run("AddVectors and GetPartitionVectorCount", func(t *testing.T) {
		dim := 16
		numPartitions := 4

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 10,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		trainer, _ := NewLocalCodebookTrainer(config, cq)
		trainer.AddVectors(vectors)

		totalCount := 0
		for i := 0; i < numPartitions; i++ {
			count := trainer.GetPartitionVectorCount(PartitionID(i))
			totalCount += count
		}

		if totalCount != len(vectors) {
			t.Errorf("total vector count = %d, want %d", totalCount, len(vectors))
		}
	})

	t.Run("IsPartitionReadyForTraining", func(t *testing.T) {
		dim := 16
		numPartitions := 4
		threshold := 20

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: threshold,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		trainer, _ := NewLocalCodebookTrainer(config, cq)
		trainer.AddVectors(vectors)

		readyCount := 0
		for i := 0; i < numPartitions; i++ {
			if trainer.IsPartitionReadyForTraining(PartitionID(i)) {
				readyCount++
			}
		}

		if readyCount == 0 {
			t.Error("no partitions ready for training, expected at least some")
		}
	})

	t.Run("TrainPartition", func(t *testing.T) {
		dim := 16
		numPartitions := 4
		threshold := 10

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: threshold,
			FallbackSeed:      42,
			LocalPQConfig: &ProductQuantizerConfig{
				NumSubspaces:         4,
				CentroidsPerSubspace: 8,
			},
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(400, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		trainer, _ := NewLocalCodebookTrainer(config, cq)
		trainer.AddVectors(vectors)

		readyPartitions := trainer.GetPartitionsReadyForTraining()
		if len(readyPartitions) == 0 {
			t.Skip("no partitions ready for training")
		}

		partition := readyPartitions[0]
		codebook, err := trainer.TrainPartition(context.Background(), partition)
		if err != nil {
			t.Fatalf("TrainPartition() error = %v", err)
		}
		if codebook == nil {
			t.Fatal("TrainPartition() returned nil codebook")
		}

		if !codebook.IsReady() {
			t.Error("codebook.IsReady() = false after training")
		}
		if codebook.PartitionID != partition {
			t.Errorf("codebook.PartitionID = %d, want %d", codebook.PartitionID, partition)
		}
		if codebook.Quantizer == nil {
			t.Error("codebook.Quantizer is nil")
		}
		if codebook.TrainedAt.IsZero() {
			t.Error("codebook.TrainedAt is zero")
		}
		if codebook.VectorCount == 0 {
			t.Error("codebook.VectorCount is zero")
		}
	})

	t.Run("TrainPartition insufficient data", func(t *testing.T) {
		dim := 16
		numPartitions := 4
		threshold := 1000

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: threshold,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		trainer, _ := NewLocalCodebookTrainer(config, cq)
		trainer.AddVectors(vectors)

		_, err := trainer.TrainPartition(context.Background(), PartitionID(0))
		if err == nil {
			t.Error("TrainPartition() with insufficient data should return error")
		}
	})

	t.Run("EncodeVector after training", func(t *testing.T) {
		dim := 16
		numPartitions := 4
		threshold := 10

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: threshold,
			FallbackSeed:      42,
			LocalPQConfig: &ProductQuantizerConfig{
				NumSubspaces:         4,
				CentroidsPerSubspace: 8,
			},
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(400, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		trainer, _ := NewLocalCodebookTrainer(config, cq)
		trainer.AddVectors(vectors)

		readyPartitions := trainer.GetPartitionsReadyForTraining()
		if len(readyPartitions) == 0 {
			t.Skip("no partitions ready")
		}
		trainedPartition := readyPartitions[0]
		_, _ = trainer.TrainPartition(context.Background(), trainedPartition)

		var testVec []float32
		for _, v := range vectors {
			if cq.GetPartition(v) == trainedPartition {
				testVec = v
				break
			}
		}

		if testVec == nil {
			t.Skip("no test vector found in trained partition")
		}

		code, err := trainer.EncodeVector(testVec)
		if err != nil {
			t.Fatalf("EncodeVector() error = %v", err)
		}
		if code.Partition != trainedPartition {
			t.Errorf("code.Partition = %d, want %d", code.Partition, trainedPartition)
		}
		if !code.HasLocalCode() {
			t.Error("code should have local code after partition training")
		}
	})

	t.Run("EncodeVector before training (fallback)", func(t *testing.T) {
		dim := 16
		numPartitions := 4
		threshold := 1000

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: threshold,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		trainer, _ := NewLocalCodebookTrainer(config, cq)
		trainer.AddVectors(vectors)

		code, err := trainer.EncodeVector(vectors[0])
		if err != nil {
			t.Fatalf("EncodeVector() error = %v", err)
		}

		if !code.Partition.Valid() {
			t.Error("code.Partition should be valid")
		}
		if code.HasLocalCode() {
			t.Error("code should not have local code before training")
		}
	})

	t.Run("DecodeVector", func(t *testing.T) {
		dim := 16
		numPartitions := 4
		threshold := 10

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: threshold,
			FallbackSeed:      42,
			LocalPQConfig: &ProductQuantizerConfig{
				NumSubspaces:         4,
				CentroidsPerSubspace: 8,
			},
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(400, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		trainer, _ := NewLocalCodebookTrainer(config, cq)
		trainer.AddVectors(vectors)

		for _, p := range trainer.GetPartitionsReadyForTraining() {
			_, _ = trainer.TrainPartition(context.Background(), p)
		}

		testVec := vectors[0]
		code, _ := trainer.EncodeVector(testVec)

		decoded, err := trainer.DecodeVector(code)
		if err != nil {
			t.Fatalf("DecodeVector() error = %v", err)
		}
		if len(decoded) != dim {
			t.Errorf("len(decoded) = %d, want %d", len(decoded), dim)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		dim := 16
		numPartitions := 4
		threshold := 10

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: threshold,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(200, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		trainer, _ := NewLocalCodebookTrainer(config, cq)
		trainer.AddVectors(vectors)

		stats := trainer.Stats()

		if stats.TotalVectorsStored != int64(len(vectors)) {
			t.Errorf("TotalVectorsStored = %d, want %d", stats.TotalVectorsStored, len(vectors))
		}
		if stats.TrainingThreshold != threshold {
			t.Errorf("TrainingThreshold = %d, want %d", stats.TrainingThreshold, threshold)
		}
	})
}

func TestLOPQStats(t *testing.T) {
	t.Run("ReadyRatio", func(t *testing.T) {
		tests := []struct {
			name     string
			stats    LOPQStats
			expected float64
		}{
			{
				name:     "all ready",
				stats:    LOPQStats{TotalPartitions: 100, ReadyPartitions: 100},
				expected: 1.0,
			},
			{
				name:     "half ready",
				stats:    LOPQStats{TotalPartitions: 100, ReadyPartitions: 50},
				expected: 0.5,
			},
			{
				name:     "none ready",
				stats:    LOPQStats{TotalPartitions: 100, ReadyPartitions: 0},
				expected: 0.0,
			},
			{
				name:     "zero total",
				stats:    LOPQStats{TotalPartitions: 0, ReadyPartitions: 0},
				expected: 0.0,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := tt.stats.ReadyRatio()
				if math.Abs(got-tt.expected) > 1e-9 {
					t.Errorf("ReadyRatio() = %f, want %f", got, tt.expected)
				}
			})
		}
	})

	t.Run("String", func(t *testing.T) {
		stats := LOPQStats{
			TotalPartitions:         100,
			ReadyPartitions:         75,
			PendingTraining:         10,
			TotalVectors:            50000,
			FallbackRatio:           0.25,
			AverageLocalCodebookAge: 24 * time.Hour,
		}

		str := stats.String()
		if len(str) == 0 {
			t.Error("String() returned empty")
		}
	})
}

func TestLOPQFallback(t *testing.T) {
	t.Run("Encoding works before training", func(t *testing.T) {
		dim := 16
		numPartitions := 4

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 1000,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)
		_ = cq.Train(context.Background(), vectors)

		trainer, _ := NewLocalCodebookTrainer(config, cq)
		trainer.AddVectors(vectors)

		code, err := trainer.EncodeVector(vectors[0])
		if err != nil {
			t.Fatalf("EncodeVector() error = %v", err)
		}
		if !code.Partition.Valid() {
			t.Error("code should have valid partition")
		}
	})

	t.Run("Mixed trained and untrained partitions", func(t *testing.T) {
		dim := 16
		numPartitions := 8
		threshold := 15

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: threshold,
			FallbackSeed:      42,
			LocalPQConfig: &ProductQuantizerConfig{
				NumSubspaces:         4,
				CentroidsPerSubspace: 8,
			},
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(400, dim, 4, 42)
		_ = cq.Train(context.Background(), vectors)

		trainer, _ := NewLocalCodebookTrainer(config, cq)
		trainer.AddVectors(vectors)

		readyPartitions := trainer.GetPartitionsReadyForTraining()
		successfullyTrained := make(map[PartitionID]bool)
		for _, p := range readyPartitions {
			codebook, err := trainer.TrainPartition(context.Background(), p)
			if err == nil && codebook != nil && codebook.IsReady() {
				successfullyTrained[p] = true
			}
		}

		encodedWithLocal := 0
		encodedFallback := 0

		for _, vec := range vectors {
			code, err := trainer.EncodeVector(vec)
			if err != nil {
				t.Fatalf("EncodeVector() error = %v", err)
			}
			if !code.Partition.Valid() {
				t.Error("code should have valid partition")
			}
			if code.HasLocalCode() {
				encodedWithLocal++
			} else {
				encodedFallback++
			}
		}

		t.Logf("Successfully trained partitions: %d, encoded with local: %d, fallback: %d",
			len(successfullyTrained), encodedWithLocal, encodedFallback)
	})
}

func TestLOPQDeterminism(t *testing.T) {
	t.Run("Same seed produces same coarse centroids", func(t *testing.T) {
		dim := 16
		numPartitions := 4
		seed := int64(12345)

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 10,
			FallbackSeed:      seed,
		}

		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)

		cq1, _ := NewCoarseQuantizer(config)
		_ = cq1.Train(context.Background(), vectors)

		cq2, _ := NewCoarseQuantizer(config)
		_ = cq2.Train(context.Background(), vectors)

		centroids1 := cq1.Centroids()
		centroids2 := cq2.Centroids()

		if len(centroids1) != len(centroids2) {
			t.Fatalf("centroid counts differ: %d vs %d", len(centroids1), len(centroids2))
		}

		matchCount := 0
		for i := range centroids1 {
			for j := range centroids2 {
				allMatch := true
				for d := range centroids1[i] {
					if math.Abs(float64(centroids1[i][d]-centroids2[j][d])) > 0.01 {
						allMatch = false
						break
					}
				}
				if allMatch {
					matchCount++
					break
				}
			}
		}
		if matchCount != len(centroids1) {
			t.Errorf("only %d/%d centroids found matching (order may differ)", matchCount, len(centroids1))
		}
	})

	t.Run("Same quantizer produces consistent assignments", func(t *testing.T) {
		dim := 16
		numPartitions := 4
		seed := int64(12345)

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 10,
			FallbackSeed:      seed,
		}

		vectors := lopqMakeClusteredVectors(100, dim, numPartitions, 42)

		cq, _ := NewCoarseQuantizer(config)
		_ = cq.Train(context.Background(), vectors)

		for i, vec := range vectors {
			p1 := cq.GetPartition(vec)
			p2 := cq.GetPartition(vec)
			if p1 != p2 {
				t.Errorf("vector %d: partition differs on repeat call %d vs %d", i, p1, p2)
			}
		}
	})
}

func TestLOPQEdgeCases(t *testing.T) {
	t.Run("Single partition", func(t *testing.T) {
		dim := 16
		numPartitions := 1

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 5,
			FallbackSeed:      42,
		}

		cq, err := NewCoarseQuantizer(config)
		if err != nil {
			t.Fatalf("NewCoarseQuantizer() error = %v", err)
		}

		vectors := lopqMakeRandomVectors(50, dim, 42)
		err = cq.Train(context.Background(), vectors)
		if err != nil {
			t.Fatalf("Train() error = %v", err)
		}

		for i, vec := range vectors {
			partition := cq.GetPartition(vec)
			if partition != PartitionID(0) {
				t.Errorf("vector %d: partition = %d, want 0", i, partition)
			}
		}
	})

	t.Run("More partitions than vectors", func(t *testing.T) {
		dim := 16
		numPartitions := 100
		numVectors := 20

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 5,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeRandomVectors(numVectors, dim, 42)

		err := cq.Train(context.Background(), vectors)
		if err != nil {
			t.Fatalf("Train() error = %v", err)
		}

		if cq.NumPartitions() > numVectors {
			t.Errorf("NumPartitions() = %d, should be <= %d", cq.NumPartitions(), numVectors)
		}
	})

	t.Run("Empty partition handling", func(t *testing.T) {
		dim := 16
		numPartitions := 4

		config := LOPQConfig{
			VectorDimension:   dim,
			NumPartitions:     numPartitions,
			TrainingThreshold: 1000,
			FallbackSeed:      42,
		}

		cq, _ := NewCoarseQuantizer(config)
		vectors := lopqMakeClusteredVectors(50, dim, 2, 42)
		_ = cq.Train(context.Background(), vectors)

		trainer, _ := NewLocalCodebookTrainer(config, cq)
		trainer.AddVectors(vectors)

		emptyCount := 0
		for i := 0; i < numPartitions; i++ {
			if trainer.GetPartitionVectorCount(PartitionID(i)) == 0 {
				emptyCount++
			}
		}

		t.Logf("Empty partitions: %d/%d", emptyCount, numPartitions)
	})

	t.Run("LocalCodebook nil handling", func(t *testing.T) {
		var cb *LocalCodebook
		if cb.IsReady() {
			t.Error("nil LocalCodebook.IsReady() should return false")
		}
		str := cb.String()
		if str == "" {
			t.Error("nil LocalCodebook.String() should return non-empty")
		}
	})
}

func BenchmarkLOPQAssign(b *testing.B) {
	dim := 128
	numPartitions := 64

	config := LOPQConfig{
		VectorDimension:   dim,
		NumPartitions:     numPartitions,
		TrainingThreshold: 10,
		FallbackSeed:      42,
	}

	cq, _ := NewCoarseQuantizer(config)
	vectors := lopqMakeClusteredVectors(1000, dim, numPartitions, 42)
	_ = cq.Train(context.Background(), vectors)

	testVec := vectors[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cq.GetPartition(testVec)
	}
}

func BenchmarkLOPQAssignBatch(b *testing.B) {
	dim := 128
	numPartitions := 64
	batchSize := 100

	config := LOPQConfig{
		VectorDimension:   dim,
		NumPartitions:     numPartitions,
		TrainingThreshold: 10,
		FallbackSeed:      42,
	}

	cq, _ := NewCoarseQuantizer(config)
	vectors := lopqMakeClusteredVectors(1000, dim, numPartitions, 42)
	_ = cq.Train(context.Background(), vectors)

	batch := vectors[:batchSize]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cq.GetPartitionBatch(batch)
	}
}

func BenchmarkLOPQEncode(b *testing.B) {
	dim := 64
	numPartitions := 8

	config := LOPQConfig{
		VectorDimension:   dim,
		NumPartitions:     numPartitions,
		TrainingThreshold: 10,
		FallbackSeed:      42,
	}

	cq, _ := NewCoarseQuantizer(config)
	vectors := lopqMakeClusteredVectors(200, dim, numPartitions, 42)
	_ = cq.Train(context.Background(), vectors)

	trainer, _ := NewLocalCodebookTrainer(config, cq)
	trainer.AddVectors(vectors)

	for _, p := range trainer.GetPartitionsReadyForTraining() {
		_, _ = trainer.TrainPartition(context.Background(), p)
	}

	testVec := vectors[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = trainer.EncodeVector(testVec)
	}
}

func BenchmarkLOPQCoarseTraining(b *testing.B) {
	dim := 64
	numPartitions := 16
	numVectors := 500

	config := LOPQConfig{
		VectorDimension:   dim,
		NumPartitions:     numPartitions,
		TrainingThreshold: 10,
		FallbackSeed:      42,
	}

	vectors := lopqMakeClusteredVectors(numVectors, dim, numPartitions, 42)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cq, _ := NewCoarseQuantizer(config)
		_ = cq.Train(context.Background(), vectors)
	}
}
