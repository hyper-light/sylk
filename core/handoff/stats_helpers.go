package handoff

import (
	"math"
	"math/rand"
)

// =============================================================================
// Statistical Helper Functions - HO.1.5
// =============================================================================

// betaSample generates a random sample from Beta(alpha, beta) distribution.
// Returns a value in [0,1]. Uses the ratio of gamma variates method.
//
// The Beta distribution is commonly used for modeling probabilities and
// proportions. It is parameterized by two shape parameters:
//   - alpha (α): controls the shape toward 1.0
//   - beta (β): controls the shape toward 0.0
//
// Special cases:
//   - Returns 0.5 if either parameter is non-positive
//   - Returns 0.5 if both gamma samples are zero (unlikely but possible)
func betaSample(alpha, beta float64) float64 {
	if alpha <= 0 || beta <= 0 {
		return 0.5
	}

	x := gammaSample(alpha, 1.0)
	y := gammaSample(beta, 1.0)

	if x+y == 0 {
		return 0.5
	}

	return x / (x + y)
}

// betaQuantile computes the p-th quantile of the Beta(alpha, beta) distribution.
// Returns the value x such that P(X ≤ x) = p for X ~ Beta(alpha, beta).
//
// Uses Newton-Raphson method with incomplete beta function approximation.
// The quantile is the inverse of the CDF.
//
// Parameters:
//   - p: probability in (0,1)
//   - alpha, beta: shape parameters (must be positive)
//
// Returns:
//   - The quantile value in [0,1]
func betaQuantile(p, alpha, beta float64) float64 {
	if !isValidQuantileInput(p, alpha, beta) {
		return defaultQuantileValue(p, alpha, beta)
	}

	return newtonRaphsonQuantile(p, alpha, beta)
}

// gammaSample generates a random sample from Gamma(alpha, beta) distribution.
// Returns a positive value.
//
// The Gamma distribution is used for modeling waiting times and positive
// continuous values. It is parameterized by:
//   - alpha (shape): controls the shape of the distribution
//   - beta (rate): controls the scale (inverse of scale parameter)
//
// Uses Marsaglia and Tsang's method for shape >= 1.
// For shape < 1, uses the transformation method.
func gammaSample(alpha, beta float64) float64 {
	if alpha <= 0 || beta <= 0 {
		return 1.0
	}

	return gammaSampleValid(alpha, beta)
}

// poissonSample generates a random sample from Poisson(lambda) distribution.
// Returns a non-negative integer.
//
// The Poisson distribution models the number of events occurring in a fixed
// interval when events occur independently at a constant average rate.
//
// Uses inverse transform method for lambda < 10, and normal approximation
// for larger lambda values.
//
// Parameters:
//   - lambda: the average rate (must be positive)
//
// Returns:
//   - A non-negative integer count
func poissonSample(lambda float64) int {
	if lambda <= 0 {
		return 0
	}

	if lambda < 10 {
		return poissonInverse(lambda)
	}

	return poissonNormal(lambda)
}

// dotProduct computes the dot product of two vectors.
// Returns the sum of element-wise products: Σ(a[i] * b[i]).
//
// If vectors have different lengths, uses the shorter length.
// Returns 0.0 for empty vectors or nil inputs.
//
// The dot product is fundamental to many statistical operations including
// linear regression, correlation, and vector similarity measures.
func dotProduct(a, b []float64) float64 {
	n := validVectorLength(a, b)
	if n == 0 {
		return 0.0
	}

	return sumProducts(a, b, n)
}

// =============================================================================
// Internal Helper Functions
// =============================================================================

// isValidQuantileInput checks if quantile parameters are valid.
func isValidQuantileInput(p, alpha, beta float64) bool {
	return p > 0 && p < 1 && alpha > 0 && beta > 0
}

// defaultQuantileValue returns the appropriate default for invalid quantile input.
func defaultQuantileValue(p, alpha, beta float64) float64 {
	if p <= 0 {
		return 0.0
	}
	if p >= 1 {
		return 1.0
	}
	return 0.5
}

// newtonRaphsonQuantile uses Newton-Raphson iteration for quantile calculation.
func newtonRaphsonQuantile(p, alpha, beta float64) float64 {
	x := alpha / (alpha + beta)

	for i := 0; i < 10; i++ {
		cdf := incompleteBeta(x, alpha, beta)
		if math.Abs(cdf-p) < 1e-6 {
			break
		}
		x = updateQuantileEstimate(x, cdf, p, alpha, beta)
	}

	return x
}

// updateQuantileEstimate performs one Newton-Raphson update step.
func updateQuantileEstimate(x, cdf, p, alpha, beta float64) float64 {
	pdf := betaPDF(x, alpha, beta)
	if pdf > 1e-10 {
		x = x - (cdf-p)/pdf
		x = math.Max(0.0, math.Min(1.0, x))
	}
	return x
}

// gammaSampleValid generates gamma sample with valid parameters.
func gammaSampleValid(alpha, beta float64) float64 {
	if alpha < 1.0 {
		return gammaSample(alpha+1.0, beta) * math.Pow(rand.Float64(), 1.0/alpha)
	}
	return gammaSampleMarsaglia(alpha, beta)
}

// gammaSampleMarsaglia uses Marsaglia and Tsang's method for alpha >= 1.
func gammaSampleMarsaglia(alpha, beta float64) float64 {
	d := alpha - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)

	for {
		x, v := gammaCandidate(c)
		if gammaAccept(x, v, d) {
			return d * v / beta
		}
	}
}

// validVectorLength returns the valid length for dot product calculation.
func validVectorLength(a, b []float64) int {
	if a == nil || b == nil {
		return 0
	}

	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	return n
}

// sumProducts computes the sum of element-wise products.
func sumProducts(a, b []float64, n int) float64 {
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += a[i] * b[i]
	}
	return sum
}

// initContinuedFraction initializes the continued fraction calculation.
func initContinuedFraction(qab, qap, x float64) (float64, float64, float64) {
	c := 1.0
	d := 1.0 - qab*x/qap
	if math.Abs(d) < 1e-30 {
		d = 1e-30
	}
	d = 1.0 / d
	h := d
	return c, d, h
}

// cfIteration performs one iteration of the continued fraction.
func cfIteration(m int, x, alpha, beta, qab, qap, qam, c, d, h float64) (float64, float64, float64) {
	m2 := 2 * m
	c, d, h = cfStep1(m, m2, x, alpha, beta, qam, c, d, h)
	c, d, h = cfStep2(m, m2, x, alpha, qab, qap, c, d, h)
	return c, d, h
}

// cfStep1 performs the first step of a continued fraction iteration.
func cfStep1(m, m2 int, x, alpha, beta, qam, c, d, h float64) (float64, float64, float64) {
	aa := float64(m) * (beta - float64(m)) * x / ((qam + float64(m2)) * (alpha + float64(m2)))
	d = safeInverse(1.0 + aa*d)
	c = safeValue(1.0 + aa/c)
	h *= d * c
	return c, d, h
}

// cfStep2 performs the second step of a continued fraction iteration.
func cfStep2(m, m2 int, x, alpha, qab, qap, c, d, h float64) (float64, float64, float64) {
	aa := -(alpha + float64(m)) * (qab + float64(m)) * x / ((alpha + float64(m2)) * (qap + float64(m2)))
	d = safeInverse(1.0 + aa*d)
	c = safeValue(1.0 + aa/c)
	del := d * c
	h *= del
	return c, d, h
}

// safeInverse safely inverts a value with underflow protection.
func safeInverse(v float64) float64 {
	if math.Abs(v) < 1e-30 {
		v = 1e-30
	}
	return 1.0 / v
}

// safeValue ensures a value doesn't underflow.
func safeValue(v float64) float64 {
	if math.Abs(v) < 1e-30 {
		return 1e-30
	}
	return v
}

// cfConverged checks if continued fraction has converged.
func cfConverged(d, c, epsilon float64) bool {
	del := d * c
	return math.Abs(del-1.0) < epsilon
}

// poissonNormalParams holds parameters for normal approximation of Poisson.
type poissonNormalParams struct {
	c     float64
	beta  float64
	alpha float64
	k     float64
}

// computePoissonNormalParams computes parameters for normal approximation.
func computePoissonNormalParams(lambda float64) poissonNormalParams {
	c := 0.767 - 3.36/lambda
	beta := math.Pi / math.Sqrt(3.0*lambda)
	alpha := beta * lambda
	k := math.Log(c) - lambda - math.Log(beta)
	return poissonNormalParams{c, beta, alpha, k}
}

// poissonNormalCandidate generates a candidate Poisson sample.
func poissonNormalCandidate(params poissonNormalParams, lambda float64) int {
	u := rand.Float64()
	if u == 0 {
		return -1
	}

	x := (math.Log(u/(1.0-u)) + params.alpha) / params.beta
	return int(math.Floor(x + 0.5))
}

// poissonNormalAccept determines if a candidate should be accepted.
func poissonNormalAccept(n int, params poissonNormalParams, lambda float64) bool {
	v := rand.Float64()
	x := (float64(n) - params.alpha) * params.beta
	y := params.alpha - params.beta*x
	lhs := y + math.Log(v/(1.0+math.Exp(y))*(1.0+math.Exp(y)))
	rhs := params.k + float64(n)*math.Log(lambda) - logGamma(float64(n)+1.0)
	return lhs <= rhs
}

// incompleteBeta approximates the regularized incomplete beta function.
// This is the CDF of the Beta distribution: I_x(a,b) = P(X ≤ x).
func incompleteBeta(x, alpha, beta float64) float64 {
	if x <= 0 {
		return 0.0
	}
	if x >= 1 {
		return 1.0
	}

	bt := math.Exp(
		alpha*math.Log(x) +
			beta*math.Log(1.0-x) -
			logBeta(alpha, beta),
	)

	if x < (alpha+1.0)/(alpha+beta+2.0) {
		return bt * betaContinuedFraction(x, alpha, beta) / alpha
	}

	return 1.0 - bt*betaContinuedFraction(1.0-x, beta, alpha)/beta
}

// betaPDF computes the probability density function of Beta(alpha, beta).
func betaPDF(x, alpha, beta float64) float64 {
	if x <= 0 || x >= 1 {
		return 0.0
	}

	return math.Exp(
		(alpha-1.0)*math.Log(x) +
			(beta-1.0)*math.Log(1.0-x) -
			logBeta(alpha, beta),
	)
}

// logBeta computes the natural logarithm of the beta function.
func logBeta(alpha, beta float64) float64 {
	return logGamma(alpha) + logGamma(beta) - logGamma(alpha+beta)
}

// logGamma approximates the natural logarithm of the gamma function.
func logGamma(x float64) float64 {
	if x <= 0 {
		return math.Inf(1)
	}

	coefficients := []float64{
		76.18009172947146,
		-86.50532032941677,
		24.01409824083091,
		-1.231739572450155,
		0.1208650973866179e-2,
		-0.5395239384953e-5,
	}

	y := x
	tmp := x + 5.5
	tmp = (x+0.5)*math.Log(tmp) - tmp

	ser := 1.000000000190015
	for i, c := range coefficients {
		y++
		ser += c / y
		if i >= 5 {
			break
		}
	}

	return tmp + math.Log(2.5066282746310005*ser/x)
}

// betaContinuedFraction evaluates the continued fraction for incomplete beta.
func betaContinuedFraction(x, alpha, beta float64) float64 {
	const maxIter = 100
	const epsilon = 1e-10

	qab := alpha + beta
	qap := alpha + 1.0
	qam := alpha - 1.0

	c, d, h := initContinuedFraction(qab, qap, x)

	for m := 1; m <= maxIter; m++ {
		c, d, h = cfIteration(m, x, alpha, beta, qab, qap, qam, c, d, h)
		if cfConverged(d, c, epsilon) {
			break
		}
	}

	return h
}

// gammaCandidate generates a candidate sample for gamma distribution.
func gammaCandidate(c float64) (float64, float64) {
	for {
		x := rand.NormFloat64()
		v := 1.0 + c*x
		if v > 0 {
			return x, v * v * v
		}
	}
}

// gammaAccept determines if a gamma sample should be accepted.
func gammaAccept(x, v, d float64) bool {
	u := rand.Float64()
	if u < 1.0-0.0331*x*x*x*x {
		return true
	}
	return math.Log(u) < 0.5*x*x+d*(1.0-v+math.Log(v))
}

// poissonInverse generates Poisson sample using inverse transform (lambda < 10).
func poissonInverse(lambda float64) int {
	l := math.Exp(-lambda)
	k := 0
	p := 1.0

	for p > l {
		k++
		p *= rand.Float64()
	}

	return k - 1
}

// poissonNormal generates Poisson sample using normal approximation (lambda >= 10).
func poissonNormal(lambda float64) int {
	params := computePoissonNormalParams(lambda)

	for {
		n := poissonNormalCandidate(params, lambda)
		if n >= 0 && poissonNormalAccept(n, params, lambda) {
			return n
		}
	}
}
