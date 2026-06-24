// Package vectorquant implements TurboQuant-style scalar quantization for
// approximate nearest-neighbor vector search. It applies a deterministic
// random rotation (via Hadamard transform with random sign flips) followed
// by per-block scalar quantization to 2, 3, or 4 bits. The package is
// standalone, CGO-free, and designed as an internal benchmark/evaluation tool.
//
// Design decisions vs. the full TurboQuant paper (arXiv 2504.19874v1):
//
//   - We use a Walsh-Hadamard transform with random sign flips instead of a
//     full random orthogonal matrix. This is O(d log d) and produces a good
//     rotation for spreading energy uniformly, which is the paper's key goal.
//     A full Gaussian random orthogonal matrix would be O(d^3) to construct.
//
//   - We use per-block scalar quantization with uniform bins (min-max scaling)
//     rather than the paper's product quantization (PQ) codebooks. This is
//     simpler to implement and reason about, and for 2-4 bits the difference
//     in recall is modest. The trade-off is documented.
//
//   - We do not implement the paper's asymmetric distance computation (ADC)
//     or the exact reranking pipeline beyond the inner-product estimator.
//     The benchmark includes an exact-rerank mode for comparison.
//
//   - Optional 2.5-bit and 3.5-bit modes use channel splitting (half the
//     dimensions at one bit-width, half at the next) rather than true
//     fractional bits, keeping the implementation clear and testable.
package vectorquant

import (
	"math"
	"math/rand"
)

// HadamardTransform applies the in-place Walsh-Hadamard transform to vec.
// The length must be a power of 2. This is O(n log n) via the butterfly algorithm.
func HadamardTransform(vec []float64) {
	n := len(vec)
	if n == 0 || (n&(n-1)) != 0 {
		return
	}
	for step := 1; step < n; step <<= 1 {
		for i := 0; i < n; i += step << 1 {
			for j := i; j < i+step; j++ {
				u := vec[j]
				v := vec[j+step]
				vec[j] = u + v
				vec[j+step] = u - v
			}
		}
	}
}

// nextPowerOf2 returns the smallest power of 2 >= n.
func nextPowerOf2(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// RandomRotation holds the precomputed random signs for a Hadamard-based
// random rotation. Apply via ApplyRotation and InverseApplyRotation.
type RandomRotation struct {
	dim   int
	padded int   // next power of 2 >= dim
	signs []float64 // random +1/-1 per padded dimension
}

// NewRandomRotation creates a deterministic random rotation for dim-dimensional
// vectors using the given seed.
func NewRandomRotation(dim, seed int) *RandomRotation {
	padded := nextPowerOf2(dim)
	rng := rand.New(rand.NewSource(int64(seed)))
	signs := make([]float64, padded)
	for i := range signs {
		if rng.Intn(2) == 0 {
			signs[i] = 1.0
		} else {
			signs[i] = -1.0
		}
	}
	return &RandomRotation{dim: dim, padded: padded, signs: signs}
}

// ApplyRotation applies the random rotation to input, writing the result
// to output (which must be len >= padded). The input is zero-padded to
// the padded dimension.
func (rr *RandomRotation) ApplyRotation(input []float64, output []float64) {
	n := rr.padded
	// Zero-pad and apply random sign flips
	for i := 0; i < n; i++ {
		if i < len(input) {
			output[i] = input[i] * rr.signs[i]
		} else {
			output[i] = 0
		}
	}
	// Walsh-Hadamard transform
	HadamardTransform(output)
	// Normalize by 1/sqrt(padded) to preserve L2 norm
	factor := 1.0 / math.Sqrt(float64(n))
	for i := 0; i < n; i++ {
		output[i] *= factor
	}
}

// InverseApplyRotation applies the inverse rotation. For the construction
// y = H * D * x / sqrt(n), the inverse is x = D * H * y / sqrt(n).
// Note the order difference: forward applies D then H; inverse applies H then D.
func (rr *RandomRotation) InverseApplyRotation(input []float64, output []float64) {
	n := rr.padded
	// First: copy input (we need to transform in-place but can't alias)
	copy(output, input)
	// Apply Hadamard transform first (reverse of forward order)
	HadamardTransform(output)
	// Then apply random sign flips
	for i := 0; i < n; i++ {
		output[i] *= rr.signs[i]
	}
	// Normalize
	factor := 1.0 / math.Sqrt(float64(n))
	for i := 0; i < n; i++ {
		output[i] *= factor
	}
}
