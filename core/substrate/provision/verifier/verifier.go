// Package verifier checks fetched bytes against the integrity policy
// declared in a recipe's VerifySpec.
//
// The verifier is the substrate's supply-chain trust anchor. Every byte
// flowing through the runtime passes through verification before
// extraction; refusing on mismatch is non-negotiable. A registry that
// served different bytes than the lockfile pin advertised would, in any
// sane substrate, find its bytes refused and a supply_chain_violation
// activity emitted to the Activity Fabric.
package verifier

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"errors"
	"fmt"
	"hash"
	"strings"

	"github.com/adalundhe/sylk/core/substrate/recipe"
	"lukechampine.com/blake3"
)

// Verifier validates fetched bytes against an expected hash.
type Verifier struct {
	policy recipe.VerifyPolicy
}

// New returns a Verifier with the given policy. Empty policy defaults
// to recipe.VerifyStrict.
func New(policy recipe.VerifyPolicy) *Verifier {
	return &Verifier{policy: defaultPolicy(policy)}
}

func defaultPolicy(p recipe.VerifyPolicy) recipe.VerifyPolicy {
	if p == "" {
		return recipe.VerifyStrict
	}
	return p
}

// Verify computes the digest of data using the algorithm named by
// expected.Algorithm and compares it to expected.Digest in
// constant time.
//
// Behavior depends on the policy:
//   - strict     — any mismatch returns ErrHashMismatch
//   - advisory   — mismatch returns a wrapped advisory error; callers
//     can inspect with errors.Is(err, ErrAdvisoryMismatch)
//   - permissive — mismatch returns nil (the caller chose to skip)
//
// Empty expected.Digest is always an error regardless of policy: a
// recipe with no expected hash is malformed and validation should have
// caught it earlier; the verifier refuses to provide a "verified by
// default" path.
func (v *Verifier) Verify(data []byte, expected recipe.Hash) error {
	if expected.IsEmpty() {
		return ErrEmptyExpectation
	}
	actual, err := computeHash(expected.Algorithm, data)
	if err != nil {
		return err
	}
	return v.compare(actual, expected.Digest, expected.Algorithm)
}

// VerifyStrict computes and compares using strict policy regardless of
// the verifier's configured policy. Used by callers that must enforce
// strict policy locally (e.g. supply-chain-critical fetches like
// bootstrap toolchains, where advisory mode is never appropriate).
func VerifyStrict(data []byte, expected recipe.Hash) error {
	return New(recipe.VerifyStrict).Verify(data, expected)
}

func (v *Verifier) compare(actual, expected []byte, algorithm string) error {
	if subtle.ConstantTimeCompare(actual, expected) == 1 {
		return nil
	}
	return v.classifyMismatch(actual, expected, algorithm)
}

func (v *Verifier) classifyMismatch(actual, expected []byte, algorithm string) error {
	mismatchInfo := newMismatchError(algorithm, expected, actual)
	switch v.policy {
	case recipe.VerifyPermissive:
		return nil
	case recipe.VerifyAdvisory:
		return fmt.Errorf("%w: %s", ErrAdvisoryMismatch, mismatchInfo)
	}
	return fmt.Errorf("%w: %s", ErrHashMismatch, mismatchInfo)
}

func newMismatchError(algorithm string, expected, actual []byte) string {
	return fmt.Sprintf("algorithm=%s expected=%x actual=%x", algorithm, expected, actual)
}

// ComputeHash returns the digest of data using the named algorithm.
// Exposed so callers can compute a hash to record in a lockfile or
// emit as a supply_chain_violation activity payload.
func ComputeHash(algorithm string, data []byte) ([]byte, error) {
	return computeHash(algorithm, data)
}

func computeHash(algorithm string, data []byte) ([]byte, error) {
	h, err := newHasher(algorithm)
	if err != nil {
		return nil, err
	}
	_, _ = h.Write(data)
	return h.Sum(nil), nil
}

func newHasher(algorithm string) (hash.Hash, error) {
	switch strings.ToLower(algorithm) {
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	case "blake3":
		return blake3.New(blake3DigestSize, nil), nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, algorithm)
}

// blake3DigestSize matches the substrate-internal Blob and Manifest
// digest width (32 bytes). The blake3 package supports arbitrary
// output widths; we pin to 32 for symmetry with the rest of the system.
const blake3DigestSize = 32

// Errors.
var (
	ErrUnknownAlgorithm  = errors.New("verifier: unknown hash algorithm")
	ErrEmptyExpectation  = errors.New("verifier: expected hash is empty")
	ErrHashMismatch      = errors.New("verifier: hash mismatch (strict policy)")
	ErrAdvisoryMismatch  = errors.New("verifier: hash mismatch (advisory policy — proceeding)")
)
