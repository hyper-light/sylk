package verifier

import (
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/recipe"
	"lukechampine.com/blake3"
)

func TestVerify_SHA256_Match(t *testing.T) {
	data := []byte("hello world")
	digest := sha256.Sum256(data)

	v := New(recipe.VerifyStrict)
	err := v.Verify(data, recipe.Hash{Algorithm: "sha256", Digest: digest[:]})
	if err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

func TestVerify_SHA512_Match(t *testing.T) {
	data := []byte("hello world")
	digest := sha512.Sum512(data)

	v := New(recipe.VerifyStrict)
	err := v.Verify(data, recipe.Hash{Algorithm: "sha512", Digest: digest[:]})
	if err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

func TestVerify_BLAKE3_Match(t *testing.T) {
	data := []byte("hello world")
	digest := blake3.Sum256(data)

	v := New(recipe.VerifyStrict)
	err := v.Verify(data, recipe.Hash{Algorithm: "blake3", Digest: digest[:]})
	if err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

func TestVerify_StrictRejectsMismatch(t *testing.T) {
	v := New(recipe.VerifyStrict)
	err := v.Verify([]byte("wrong content"), recipe.Hash{
		Algorithm: "sha256",
		Digest:    sha256Sum([]byte("expected content")),
	})
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("expected ErrHashMismatch, got %v", err)
	}
}

func TestVerify_AdvisorySurfacesAdvisoryError(t *testing.T) {
	v := New(recipe.VerifyAdvisory)
	err := v.Verify([]byte("wrong"), recipe.Hash{
		Algorithm: "sha256",
		Digest:    sha256Sum([]byte("expected")),
	})
	if !errors.Is(err, ErrAdvisoryMismatch) {
		t.Fatalf("expected ErrAdvisoryMismatch, got %v", err)
	}
	if errors.Is(err, ErrHashMismatch) {
		t.Fatal("advisory mismatch should not be flagged as a strict mismatch")
	}
}

func TestVerify_PermissiveSilentlyAccepts(t *testing.T) {
	v := New(recipe.VerifyPermissive)
	err := v.Verify([]byte("wrong"), recipe.Hash{
		Algorithm: "sha256",
		Digest:    sha256Sum([]byte("expected")),
	})
	if err != nil {
		t.Fatalf("permissive should accept any mismatch, got %v", err)
	}
}

func TestVerify_EmptyExpectationIsAlwaysError(t *testing.T) {
	for _, policy := range []recipe.VerifyPolicy{recipe.VerifyStrict, recipe.VerifyAdvisory, recipe.VerifyPermissive} {
		v := New(policy)
		err := v.Verify([]byte("anything"), recipe.Hash{})
		if !errors.Is(err, ErrEmptyExpectation) {
			t.Errorf("policy=%s: expected ErrEmptyExpectation, got %v", policy, err)
		}
	}
}

func TestVerify_UnknownAlgorithm(t *testing.T) {
	v := New(recipe.VerifyStrict)
	err := v.Verify([]byte("data"), recipe.Hash{Algorithm: "md5", Digest: []byte{0x00}})
	if !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("expected ErrUnknownAlgorithm for md5, got %v", err)
	}
}

func TestVerify_AlgorithmIsCaseInsensitive(t *testing.T) {
	data := []byte("hello")
	digest := sha256.Sum256(data)
	v := New(recipe.VerifyStrict)
	err := v.Verify(data, recipe.Hash{Algorithm: "SHA256", Digest: digest[:]})
	if err != nil {
		t.Fatalf("uppercase algorithm name should be accepted, got %v", err)
	}
}

func TestVerifyStrict_PackageLevel(t *testing.T) {
	data := []byte("data")
	digest := sha256.Sum256(data)
	if err := VerifyStrict(data, recipe.Hash{Algorithm: "sha256", Digest: digest[:]}); err != nil {
		t.Fatalf("VerifyStrict (match): %v", err)
	}
	if err := VerifyStrict(data, recipe.Hash{Algorithm: "sha256", Digest: sha256Sum([]byte("other"))}); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("VerifyStrict (mismatch): got %v, want ErrHashMismatch", err)
	}
}

func TestComputeHash_AcceptsEachAlgorithm(t *testing.T) {
	data := []byte("data")
	for _, algo := range []string{"sha256", "sha512", "blake3"} {
		out, err := ComputeHash(algo, data)
		if err != nil {
			t.Errorf("ComputeHash(%s): %v", algo, err)
			continue
		}
		if len(out) == 0 {
			t.Errorf("ComputeHash(%s) returned empty digest", algo)
		}
	}
}

func sha256Sum(data []byte) []byte {
	d := sha256.Sum256(data)
	return d[:]
}
