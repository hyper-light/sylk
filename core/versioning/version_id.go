package versioning

import (
	"crypto/sha256"
	"encoding/hex"
)

const HashSize = 32

type VersionID [HashSize]byte

type ContentHash [HashSize]byte

func ComputeVersionID(content []byte, metadata []byte) VersionID {
	h := sha256.New()
	h.Write(content)
	h.Write(metadata)
	var id VersionID
	copy(id[:], h.Sum(nil))
	return id
}

func ComputeContentHash(content []byte) ContentHash {
	return ContentHash(sha256.Sum256(content))
}

func (v VersionID) String() string {
	return hex.EncodeToString(v[:])
}

func (v VersionID) Short() string {
	return hex.EncodeToString(v[:4])
}

func (v VersionID) IsZero() bool {
	for _, b := range v {
		if b != 0 {
			return false
		}
	}
	return true
}

func (v VersionID) Bytes() []byte {
	result := make([]byte, HashSize)
	copy(result, v[:])
	return result
}

func ParseVersionID(s string) (VersionID, error) {
	bytes, err := hex.DecodeString(s)
	if err != nil {
		return VersionID{}, err
	}
	var id VersionID
	copy(id[:], bytes)
	return id, nil
}

func (c ContentHash) String() string {
	return hex.EncodeToString(c[:])
}

func (c ContentHash) Short() string {
	return hex.EncodeToString(c[:4])
}

func (c ContentHash) IsZero() bool {
	for _, b := range c {
		if b != 0 {
			return false
		}
	}
	return true
}

func (c ContentHash) Bytes() []byte {
	result := make([]byte, HashSize)
	copy(result, c[:])
	return result
}

func ParseContentHash(s string) (ContentHash, error) {
	bytes, err := hex.DecodeString(s)
	if err != nil {
		return ContentHash{}, err
	}
	var hash ContentHash
	copy(hash[:], bytes)
	return hash, nil
}

func (v VersionID) Equal(other VersionID) bool {
	return v == other
}

func (c ContentHash) Equal(other ContentHash) bool {
	return c == other
}
