package cache

import (
	"errors"
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// DictionaryStore manages per-ecosystem zstd compression dictionaries.
//
// A trained zstd dictionary improves compression ratio dramatically on
// homogeneous content: Python source files compress 5–8× with a
// Python-trained dict vs ~3× with no dict. The dictionary itself is
// ~64KB and is amortized across thousands of blobs, so its memory
// cost is negligible.
//
// Dictionary lifecycle: the substrate generates dictionaries offline
// from a corpus sample and stores them in the substrate WAL. At cache
// startup, the WAL replay populates the DictionaryStore. Recipes that
// declare an ecosystem the store has a dictionary for use the dict
// when the cache compresses their blobs to Warm; the rest fall back
// to dict-less zstd (still ~3× compression on text).
//
// Safe for concurrent use.
type DictionaryStore struct {
	mu      sync.RWMutex
	dicts   map[string][]byte // ecosystem name → dictionary bytes
	encoders map[string]*zstd.Encoder
	decoders map[string]*zstd.Decoder
}

// NewDictionaryStore constructs an empty store. Call Install for each
// ecosystem dictionary the substrate has trained.
func NewDictionaryStore() *DictionaryStore {
	return &DictionaryStore{
		dicts:    make(map[string][]byte),
		encoders: make(map[string]*zstd.Encoder),
		decoders: make(map[string]*zstd.Decoder),
	}
}

// Install registers a dictionary for the named ecosystem. Re-installing
// replaces the prior registration and recreates the encoder/decoder
// pair.
func (d *DictionaryStore) Install(ecosystem string, dict []byte) error {
	if ecosystem == "" {
		return ErrEmptyEcosystem
	}
	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderDict(dict),
		zstd.WithEncoderLevel(zstd.SpeedDefault),
	)
	if err != nil {
		return fmt.Errorf("zstd encoder: %w", err)
	}
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderDicts(dict))
	if err != nil {
		_ = encoder.Close()
		return fmt.Errorf("zstd decoder: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if old, ok := d.encoders[ecosystem]; ok {
		_ = old.Close()
	}
	if old, ok := d.decoders[ecosystem]; ok {
		old.Close()
	}
	d.dicts[ecosystem] = dict
	d.encoders[ecosystem] = encoder
	d.decoders[ecosystem] = decoder
	return nil
}

// Compress returns the zstd-compressed form of data, using the named
// ecosystem's dictionary when available. Empty ecosystem (or unknown
// ecosystem) compresses with the default no-dict encoder.
func (d *DictionaryStore) Compress(ecosystem string, data []byte) ([]byte, error) {
	encoder := d.encoderFor(ecosystem)
	if encoder == nil {
		return defaultCompress(data)
	}
	return encoder.EncodeAll(data, nil), nil
}

// Decompress returns the uncompressed form of compressed bytes, using
// the named ecosystem's dictionary when available.
func (d *DictionaryStore) Decompress(ecosystem string, compressed []byte) ([]byte, error) {
	decoder := d.decoderFor(ecosystem)
	if decoder == nil {
		return defaultDecompress(compressed)
	}
	return decoder.DecodeAll(compressed, nil)
}

// Has reports whether a dictionary is installed for the ecosystem.
func (d *DictionaryStore) Has(ecosystem string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.dicts[ecosystem]
	return ok
}

// Ecosystems returns the names of all installed dictionaries.
func (d *DictionaryStore) Ecosystems() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, 0, len(d.dicts))
	for k := range d.dicts {
		out = append(out, k)
	}
	return out
}

// Close releases all encoders and decoders. After Close the store is
// not usable.
func (d *DictionaryStore) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, encoder := range d.encoders {
		_ = encoder.Close()
	}
	for _, decoder := range d.decoders {
		decoder.Close()
	}
	d.encoders = nil
	d.decoders = nil
}

func (d *DictionaryStore) encoderFor(ecosystem string) *zstd.Encoder {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.encoders[ecosystem]
}

func (d *DictionaryStore) decoderFor(ecosystem string) *zstd.Decoder {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.decoders[ecosystem]
}

// defaultEncoder/defaultDecoder are package-level singletons used when
// no ecosystem dictionary is available. Lazy-initialized to defer the
// allocation until first use.
var (
	defaultEncoderOnce sync.Once
	defaultEncoderErr  error
	defaultEncoder     *zstd.Encoder

	defaultDecoderOnce sync.Once
	defaultDecoderErr  error
	defaultDecoder     *zstd.Decoder
)

func defaultCompress(data []byte) ([]byte, error) {
	defaultEncoderOnce.Do(func() {
		defaultEncoder, defaultEncoderErr = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	})
	if defaultEncoderErr != nil {
		return nil, defaultEncoderErr
	}
	return defaultEncoder.EncodeAll(data, nil), nil
}

func defaultDecompress(compressed []byte) ([]byte, error) {
	defaultDecoderOnce.Do(func() {
		defaultDecoder, defaultDecoderErr = zstd.NewReader(nil)
	})
	if defaultDecoderErr != nil {
		return nil, defaultDecoderErr
	}
	return defaultDecoder.DecodeAll(compressed, nil)
}

// ErrEmptyEcosystem is returned when an empty ecosystem name is passed
// to Install.
var ErrEmptyEcosystem = errors.New("cache: ecosystem name is empty")
