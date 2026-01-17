package versioning

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
)

func TestComputeVersionID(t *testing.T) {
	t.Run("deterministic for same input", func(t *testing.T) {
		content := []byte("test content")
		metadata := []byte("test metadata")

		id1 := ComputeVersionID(content, metadata)
		id2 := ComputeVersionID(content, metadata)

		if id1 != id2 {
			t.Errorf("expected same IDs, got %s and %s", id1.String(), id2.String())
		}
	})

	t.Run("different for different content", func(t *testing.T) {
		metadata := []byte("metadata")
		id1 := ComputeVersionID([]byte("content1"), metadata)
		id2 := ComputeVersionID([]byte("content2"), metadata)

		if id1 == id2 {
			t.Error("expected different IDs for different content")
		}
	})

	t.Run("different for different metadata", func(t *testing.T) {
		content := []byte("content")
		id1 := ComputeVersionID(content, []byte("meta1"))
		id2 := ComputeVersionID(content, []byte("meta2"))

		if id1 == id2 {
			t.Error("expected different IDs for different metadata")
		}
	})

	t.Run("empty input produces valid hash", func(t *testing.T) {
		id := ComputeVersionID(nil, nil)
		if id.IsZero() {
			t.Error("expected non-zero hash for empty input")
		}
	})
}

func TestComputeContentHash(t *testing.T) {
	t.Run("matches sha256", func(t *testing.T) {
		content := []byte("test content")
		hash := ComputeContentHash(content)
		expected := sha256.Sum256(content)

		if hash != ContentHash(expected) {
			t.Errorf("hash mismatch: got %s, expected %x", hash.String(), expected)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		content := []byte("test")
		h1 := ComputeContentHash(content)
		h2 := ComputeContentHash(content)

		if h1 != h2 {
			t.Error("expected same hashes")
		}
	})
}

func TestVersionID_String(t *testing.T) {
	t.Run("returns hex string", func(t *testing.T) {
		var id VersionID
		for i := range id {
			id[i] = byte(i)
		}

		s := id.String()
		if len(s) != 64 {
			t.Errorf("expected 64 char hex string, got %d chars", len(s))
		}

		decoded, err := hex.DecodeString(s)
		if err != nil {
			t.Fatalf("invalid hex: %v", err)
		}
		if len(decoded) != HashSize {
			t.Errorf("expected %d bytes, got %d", HashSize, len(decoded))
		}
	})
}

func TestVersionID_Short(t *testing.T) {
	t.Run("returns first 4 bytes as hex", func(t *testing.T) {
		var id VersionID
		id[0], id[1], id[2], id[3] = 0xab, 0xcd, 0xef, 0x12

		short := id.Short()
		if short != "abcdef12" {
			t.Errorf("expected abcdef12, got %s", short)
		}
	})
}

func TestVersionID_IsZero(t *testing.T) {
	t.Run("zero ID", func(t *testing.T) {
		var id VersionID
		if !id.IsZero() {
			t.Error("expected IsZero to return true for zero ID")
		}
	})

	t.Run("non-zero ID", func(t *testing.T) {
		id := ComputeVersionID([]byte("test"), nil)
		if id.IsZero() {
			t.Error("expected IsZero to return false for non-zero ID")
		}
	})

	t.Run("single non-zero byte", func(t *testing.T) {
		var id VersionID
		id[HashSize-1] = 1
		if id.IsZero() {
			t.Error("expected IsZero to return false")
		}
	})
}

func TestVersionID_Bytes(t *testing.T) {
	t.Run("returns copy", func(t *testing.T) {
		id := ComputeVersionID([]byte("test"), nil)
		bytes := id.Bytes()

		bytes[0] = 0xff
		if id[0] == 0xff {
			t.Error("Bytes should return a copy, not reference")
		}
	})

	t.Run("correct length", func(t *testing.T) {
		var id VersionID
		if len(id.Bytes()) != HashSize {
			t.Errorf("expected %d bytes, got %d", HashSize, len(id.Bytes()))
		}
	})
}

func TestParseVersionID(t *testing.T) {
	t.Run("valid hex string", func(t *testing.T) {
		original := ComputeVersionID([]byte("test"), nil)
		parsed, err := ParseVersionID(original.String())

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if parsed != original {
			t.Error("parsed ID does not match original")
		}
	})

	t.Run("invalid hex string", func(t *testing.T) {
		_, err := ParseVersionID("not-hex")
		if err == nil {
			t.Error("expected error for invalid hex")
		}
	})

	t.Run("empty string", func(t *testing.T) {
		id, err := ParseVersionID("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !id.IsZero() {
			t.Error("expected zero ID for empty string")
		}
	})

	t.Run("short hex string", func(t *testing.T) {
		id, err := ParseVersionID("abcd")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id[0] != 0xab || id[1] != 0xcd {
			t.Error("first bytes not set correctly")
		}
	})
}

func TestContentHash_String(t *testing.T) {
	t.Run("returns hex string", func(t *testing.T) {
		hash := ComputeContentHash([]byte("test"))
		s := hash.String()

		if len(s) != 64 {
			t.Errorf("expected 64 char hex string, got %d", len(s))
		}
	})
}

func TestContentHash_Short(t *testing.T) {
	t.Run("returns first 4 bytes", func(t *testing.T) {
		var hash ContentHash
		hash[0], hash[1], hash[2], hash[3] = 0x12, 0x34, 0x56, 0x78

		if hash.Short() != "12345678" {
			t.Errorf("expected 12345678, got %s", hash.Short())
		}
	})
}

func TestContentHash_IsZero(t *testing.T) {
	t.Run("zero hash", func(t *testing.T) {
		var hash ContentHash
		if !hash.IsZero() {
			t.Error("expected IsZero true")
		}
	})

	t.Run("non-zero hash", func(t *testing.T) {
		hash := ComputeContentHash([]byte("test"))
		if hash.IsZero() {
			t.Error("expected IsZero false")
		}
	})
}

func TestContentHash_Bytes(t *testing.T) {
	t.Run("returns copy", func(t *testing.T) {
		hash := ComputeContentHash([]byte("test"))
		bytes := hash.Bytes()

		bytes[0] = 0xff
		if hash[0] == 0xff {
			t.Error("Bytes should return a copy")
		}
	})
}

func TestParseContentHash(t *testing.T) {
	t.Run("valid hex", func(t *testing.T) {
		original := ComputeContentHash([]byte("test"))
		parsed, err := ParseContentHash(original.String())

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if parsed != original {
			t.Error("parsed hash does not match")
		}
	})

	t.Run("invalid hex", func(t *testing.T) {
		_, err := ParseContentHash("xyz")
		if err == nil {
			t.Error("expected error for invalid hex")
		}
	})
}

func TestVersionID_Equal(t *testing.T) {
	t.Run("equal IDs", func(t *testing.T) {
		id1 := ComputeVersionID([]byte("test"), nil)
		id2 := ComputeVersionID([]byte("test"), nil)

		if !id1.Equal(id2) {
			t.Error("expected Equal to return true")
		}
	})

	t.Run("different IDs", func(t *testing.T) {
		id1 := ComputeVersionID([]byte("test1"), nil)
		id2 := ComputeVersionID([]byte("test2"), nil)

		if id1.Equal(id2) {
			t.Error("expected Equal to return false")
		}
	})
}

func TestContentHash_Equal(t *testing.T) {
	t.Run("equal hashes", func(t *testing.T) {
		h1 := ComputeContentHash([]byte("test"))
		h2 := ComputeContentHash([]byte("test"))

		if !h1.Equal(h2) {
			t.Error("expected Equal to return true")
		}
	})

	t.Run("different hashes", func(t *testing.T) {
		h1 := ComputeContentHash([]byte("test1"))
		h2 := ComputeContentHash([]byte("test2"))

		if h1.Equal(h2) {
			t.Error("expected Equal to return false")
		}
	})
}

func TestVersionID_ConcurrentAccess(t *testing.T) {
	var wg sync.WaitGroup
	iterations := 1000

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			content := []byte{byte(n)}
			id := ComputeVersionID(content, nil)
			_ = id.String()
			_ = id.Short()
			_ = id.IsZero()
			_ = id.Bytes()
		}(i)
	}

	wg.Wait()
}

func TestContentHash_ConcurrentAccess(t *testing.T) {
	var wg sync.WaitGroup
	iterations := 1000

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			content := []byte{byte(n)}
			hash := ComputeContentHash(content)
			_ = hash.String()
			_ = hash.Short()
			_ = hash.IsZero()
			_ = hash.Bytes()
		}(i)
	}

	wg.Wait()
}

func TestHashSize(t *testing.T) {
	if HashSize != 32 {
		t.Errorf("expected HashSize to be 32, got %d", HashSize)
	}
}
