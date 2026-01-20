package quantization

import (
	"context"
	"errors"
	"fmt"

	"gonum.org/v1/gonum/blas"
	"gonum.org/v1/gonum/blas/blas64"
	"gonum.org/v1/gonum/mat"
)

// =============================================================================
// Optimized Product Quantization (OPQ)
// =============================================================================
//
// OPQ improves PQ recall by learning an orthogonal rotation matrix R that
// minimizes quantization error. Vectors are rotated before quantization:
//
//   Standard PQ: code = encode(x)
//   OPQ:         code = encode(R @ x)
//
// The rotation R is learned by alternating optimization:
//   1. Fix R, train PQ codebooks on rotated vectors
//   2. Fix codebooks, solve Procrustes problem for optimal R
//
// This typically improves recall by 10-30% with minimal performance impact.

// OptimizedProductQuantizer wraps ProductQuantizer with learned rotation.
type OptimizedProductQuantizer struct {
	// Underlying product quantizer
	pq *ProductQuantizer

	// Rotation matrix R: [vectorDim × vectorDim], stored row-major
	// Rotated vector = R @ original vector
	rotation []float64

	// Inverse rotation (R^T for orthogonal matrices)
	rotationT []float64

	// Vector dimension
	vectorDim int

	// Whether OPQ training has been performed
	trained bool

	// Number of OPQ iterations (alternating optimization rounds)
	opqIterations int
}

// OPQConfig configures Optimized Product Quantization training.
type OPQConfig struct {
	// PQConfig for the underlying product quantizer
	PQConfig ProductQuantizerConfig

	// TrainConfig for k-means training
	TrainConfig TrainConfig

	// OPQIterations is the number of alternating optimization rounds.
	// More iterations = better rotation but longer training.
	// Typical values: 10-20. Default: 10.
	OPQIterations int
}

// DefaultOPQConfig returns default OPQ configuration.
func DefaultOPQConfig() OPQConfig {
	return OPQConfig{
		PQConfig:      DefaultProductQuantizerConfig(),
		TrainConfig:   TrainConfig{}, // Will be derived
		OPQIterations: 10,
	}
}

// NewOptimizedProductQuantizer creates a new OPQ instance.
func NewOptimizedProductQuantizer(vectorDim int, config OPQConfig) (*OptimizedProductQuantizer, error) {
	pq, err := NewProductQuantizer(vectorDim, config.PQConfig)
	if err != nil {
		return nil, fmt.Errorf("create PQ: %w", err)
	}

	if config.OPQIterations <= 0 {
		config.OPQIterations = 10
	}

	// Initialize rotation as identity matrix
	rotation := make([]float64, vectorDim*vectorDim)
	for i := 0; i < vectorDim; i++ {
		rotation[i*vectorDim+i] = 1.0
	}

	return &OptimizedProductQuantizer{
		pq:            pq,
		rotation:      rotation,
		rotationT:     make([]float64, vectorDim*vectorDim),
		vectorDim:     vectorDim,
		trained:       false,
		opqIterations: config.OPQIterations,
	}, nil
}

// Train learns both the rotation matrix and PQ codebooks.
func (opq *OptimizedProductQuantizer) Train(ctx context.Context, vectors [][]float32, config OPQConfig) error {
	if len(vectors) == 0 {
		return errors.New("no training vectors")
	}

	n := len(vectors)
	dim := opq.vectorDim

	// Validate dimensions
	for i, v := range vectors {
		if len(v) != dim {
			return fmt.Errorf("vector %d has dimension %d, expected %d", i, len(v), dim)
		}
	}

	// Convert to float64 for rotation learning
	X := make([]float64, n*dim)
	for i, v := range vectors {
		for j, val := range v {
			X[i*dim+j] = float64(val)
		}
	}

	// Initialize R as identity
	R := make([]float64, dim*dim)
	for i := 0; i < dim; i++ {
		R[i*dim+i] = 1.0
	}

	// Rotated vectors buffer
	XR := make([]float64, n*dim)

	// Alternating optimization
	for iter := 0; iter < opq.opqIterations; iter++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Step 1: Rotate vectors using current R
		// XR = X @ R^T (each row of X multiplied by R^T)
		blas64.Gemm(
			blas.NoTrans,
			blas.Trans,
			1.0,
			blas64.General{Rows: n, Cols: dim, Stride: dim, Data: X},
			blas64.General{Rows: dim, Cols: dim, Stride: dim, Data: R},
			0.0,
			blas64.General{Rows: n, Cols: dim, Stride: dim, Data: XR},
		)

		// Convert rotated vectors to float32 for PQ training
		rotatedVectors := make([][]float32, n)
		for i := 0; i < n; i++ {
			rotatedVectors[i] = make([]float32, dim)
			for j := 0; j < dim; j++ {
				rotatedVectors[i][j] = float32(XR[i*dim+j])
			}
		}

		// Step 2: Train PQ on rotated vectors
		trainConfig := config.TrainConfig
		if trainConfig.NumRestarts == 0 {
			trainConfig = DeriveTrainConfig(opq.pq.centroidsPerSubspace, opq.pq.subspaceDim)
		}

		// Reset PQ trained state to allow retraining
		opq.pq.trained = false
		if err := opq.pq.TrainParallelWithConfig(ctx, rotatedVectors, trainConfig, 0); err != nil {
			return fmt.Errorf("PQ training iteration %d: %w", iter, err)
		}

		// Step 3: Compute reconstructed vectors from PQ codes
		// For each vector, encode then decode
		reconstructed := make([]float64, n*dim)
		for i := 0; i < n; i++ {
			code, _ := opq.pq.Encode(rotatedVectors[i])

			// Decode: sum of centroids for each subspace
			for m := 0; m < opq.pq.numSubspaces; m++ {
				centroid := opq.pq.centroids[m][code[m]]
				subStart := m * opq.pq.subspaceDim
				for d, val := range centroid {
					reconstructed[i*dim+subStart+d] = float64(val)
				}
			}
		}

		// Step 4: Update R via Procrustes problem
		// Find R that minimizes ||X @ R^T - reconstructed||²
		// Solution: R = V @ U^T where reconstructed^T @ X = U @ S @ V^T (SVD)
		//
		// Compute M = reconstructed^T @ X [dim × dim]
		M := make([]float64, dim*dim)
		blas64.Gemm(
			blas.Trans,
			blas.NoTrans,
			1.0,
			blas64.General{Rows: n, Cols: dim, Stride: dim, Data: reconstructed},
			blas64.General{Rows: n, Cols: dim, Stride: dim, Data: X},
			0.0,
			blas64.General{Rows: dim, Cols: dim, Stride: dim, Data: M},
		)

		// SVD of M
		svdU, svdVT, err := computeSVD(M, dim)
		if err != nil {
			// If SVD fails, keep current R
			continue
		}

		// R = V @ U^T = (U @ V^T)^T
		// Since we have V^T, we compute U @ V^T then transpose
		// Actually: R^T = U @ V^T, so R = V @ U^T
		// Let's compute R = V @ U^T directly
		// V = (V^T)^T, U^T = transpose of U
		newR := make([]float64, dim*dim)
		for i := 0; i < dim; i++ {
			for j := 0; j < dim; j++ {
				var sum float64
				for k := 0; k < dim; k++ {
					// V[i,k] * U^T[k,j] = V^T[k,i] * U[j,k]
					sum += svdVT[k*dim+i] * svdU[j*dim+k]
				}
				newR[i*dim+j] = sum
			}
		}
		copy(R, newR)
	}

	// Store final rotation
	copy(opq.rotation, R)

	// Compute R^T for inverse rotation
	for i := 0; i < dim; i++ {
		for j := 0; j < dim; j++ {
			opq.rotationT[i*dim+j] = R[j*dim+i]
		}
	}

	opq.trained = true
	return nil
}

// computeSVD computes the SVD of a square matrix using gonum.
// Returns U, V^T matrices.
func computeSVD(M []float64, dim int) ([]float64, []float64, error) {
	// Create dense matrix from flat array
	mMat := mat.NewDense(dim, dim, M)

	// Compute SVD
	var svd mat.SVD
	ok := svd.Factorize(mMat, mat.SVDFull)
	if !ok {
		return nil, nil, errors.New("SVD factorization failed")
	}

	// Extract U and V^T
	var u, vt mat.Dense
	svd.UTo(&u)
	svd.VTo(&vt)

	// Convert to flat arrays
	uData := make([]float64, dim*dim)
	vtData := make([]float64, dim*dim)

	for i := 0; i < dim; i++ {
		for j := 0; j < dim; j++ {
			uData[i*dim+j] = u.At(i, j)
			vtData[i*dim+j] = vt.At(j, i) // V^T stored as V transposed
		}
	}

	return uData, vtData, nil
}

// Encode compresses a vector using OPQ (rotate then PQ encode).
func (opq *OptimizedProductQuantizer) Encode(vector []float32) (PQCode, error) {
	if !opq.trained {
		return nil, errors.New("OPQ not trained")
	}
	if len(vector) != opq.vectorDim {
		return nil, fmt.Errorf("vector dimension %d != expected %d", len(vector), opq.vectorDim)
	}

	// Rotate vector: rotated = R @ vector
	rotated := opq.rotateVector(vector)

	// Encode with PQ
	return opq.pq.Encode(rotated)
}

// rotateVector applies the learned rotation to a vector.
func (opq *OptimizedProductQuantizer) rotateVector(vector []float32) []float32 {
	dim := opq.vectorDim
	rotated := make([]float32, dim)

	// Convert to float64, rotate, convert back
	v64 := make([]float64, dim)
	for i, val := range vector {
		v64[i] = float64(val)
	}

	result := make([]float64, dim)
	blas64.Gemv(
		blas.NoTrans,
		1.0,
		blas64.General{Rows: dim, Cols: dim, Stride: dim, Data: opq.rotation},
		blas64.Vector{N: dim, Inc: 1, Data: v64},
		0.0,
		blas64.Vector{N: dim, Inc: 1, Data: result},
	)

	for i, val := range result {
		rotated[i] = float32(val)
	}
	return rotated
}

// ComputeDistanceTable computes the distance table for a query.
// The query is rotated before computing distances.
func (opq *OptimizedProductQuantizer) ComputeDistanceTable(query []float32) (*DistanceTable, error) {
	if !opq.trained {
		return nil, errors.New("OPQ not trained")
	}

	// Rotate query
	rotatedQuery := opq.rotateVector(query)

	// Compute distance table on rotated query
	return opq.pq.ComputeDistanceTable(rotatedQuery)
}

// AsymmetricDistance computes distance using precomputed table.
func (opq *OptimizedProductQuantizer) AsymmetricDistance(table *DistanceTable, code PQCode) float32 {
	return opq.pq.AsymmetricDistance(table, code)
}

// EncodeBatch encodes multiple vectors.
func (opq *OptimizedProductQuantizer) EncodeBatch(vectors [][]float32, workers int) ([]PQCode, error) {
	if !opq.trained {
		return nil, errors.New("OPQ not trained")
	}

	// Rotate all vectors first
	rotated := make([][]float32, len(vectors))
	for i, v := range vectors {
		rotated[i] = opq.rotateVector(v)
	}

	return opq.pq.EncodeBatch(rotated, workers)
}

// IsTrained returns whether OPQ has been trained.
func (opq *OptimizedProductQuantizer) IsTrained() bool {
	return opq.trained
}

// DeriveOPQConfig derives optimal OPQ configuration from data characteristics.
// All parameters are computed from first principles based on the data.
//
// Parameters:
//   - vectorDim: dimensionality of vectors
//   - numVectors: number of training vectors available
//   - targetBytesPerVector: desired compressed size (0 = auto-derive)
func DeriveOPQConfig(vectorDim, numVectors, targetBytesPerVector int) OPQConfig {
	// Number of subspaces: balance between compression and accuracy
	// Rule: subspace dimension should be 8-32 for good k-means performance
	// Prefer power-of-2 subspace counts for memory alignment
	var numSubspaces int
	subspaceDim := 24 // Target subspace dimension

	if vectorDim <= 32 {
		numSubspaces = vectorDim / 4
		if numSubspaces < 1 {
			numSubspaces = 1
		}
	} else if vectorDim <= 128 {
		numSubspaces = vectorDim / 8
	} else if vectorDim <= 512 {
		numSubspaces = vectorDim / 16
	} else {
		// For high dimensions, use ~32 subspaces
		numSubspaces = vectorDim / subspaceDim
	}

	// Ensure vectorDim is divisible by numSubspaces
	for vectorDim%numSubspaces != 0 && numSubspaces > 1 {
		numSubspaces--
	}

	// Centroids per subspace: more is better for accuracy
	// Limited by: training data, memory, uint8 encoding (max 256)
	// Rule: need at least 10 samples per centroid for stable k-means
	actualSubspaceDim := vectorDim / numSubspaces
	minSamplesPerCentroid := max(10, actualSubspaceDim)
	maxCentroids := numVectors / minSamplesPerCentroid

	var centroidsPerSubspace int
	if maxCentroids >= 256 {
		centroidsPerSubspace = 256 // Maximum for uint8 encoding
	} else if maxCentroids >= 128 {
		centroidsPerSubspace = 128
	} else if maxCentroids >= 64 {
		centroidsPerSubspace = 64
	} else if maxCentroids >= 32 {
		centroidsPerSubspace = 32
	} else {
		centroidsPerSubspace = max(maxCentroids, 8) // Minimum viable
	}

	// OPQ iterations: more iterations = better rotation
	// Diminishing returns after ~20 iterations
	// Scale with data complexity (higher dim = more iterations)
	opqIterations := 10
	if vectorDim >= 256 {
		opqIterations = 15
	}
	if vectorDim >= 512 {
		opqIterations = 20
	}

	return OPQConfig{
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         numSubspaces,
			CentroidsPerSubspace: centroidsPerSubspace,
		},
		TrainConfig:   DeriveTrainConfig(centroidsPerSubspace, actualSubspaceDim),
		OPQIterations: opqIterations,
	}
}

// NumSubspaces returns the number of subspaces.
func (opq *OptimizedProductQuantizer) NumSubspaces() int {
	return opq.pq.numSubspaces
}

// CentroidsPerSubspace returns centroids per subspace.
func (opq *OptimizedProductQuantizer) CentroidsPerSubspace() int {
	return opq.pq.centroidsPerSubspace
}

// VectorDim returns the vector dimension.
func (opq *OptimizedProductQuantizer) VectorDim() int {
	return opq.vectorDim
}
