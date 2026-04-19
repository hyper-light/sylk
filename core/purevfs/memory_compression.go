package purevfs

import (
	"bytes"
	"compress/flate"
	"io"
	"sort"
	"time"
)

// memory_compression.go implements transparent compression of cold in-memory
// files so the broker's memory namespaces (/tmp, /cache, /home, /out inside
// the FUSE sandbox) can hold more data without spilling to real disk. This
// preserves the strict-disk invariant: never write to a host filesystem path
// to make room for a sandbox file. When the budget fills up, cold files get
// compressed in place (DEFLATE level 1 — fast) and the freed bytes satisfy
// the pending reservation. Hot files stay uncompressed.
//
// Algorithm:
//  1. Reservation request arrives for N bytes.
//  2. Budget reports pressure.
//  3. Walk memory files sorted by access time (oldest first). For each
//     uncompressed file, compress and release the delta from the budget.
//     Stop when total released ≥ N or all files are compressed.
//  4. Retry the reservation. If still short, return ErrMemoryPressure —
//     the user's workload genuinely exceeds the allocated budget and they
//     must raise it explicitly rather than silently spill to disk.
//
// Reads decompress on demand. Re-reserving the decompression delta may
// trigger another compression pass; if even that can't make room, the
// read returns a transient buffer without updating stored state, so the
// caller still gets the correct bytes.

// compressColdFilesLocked compresses cold files in the namespace until at
// least targetFreed bytes have been returned to the budget, or until no
// compressible files remain. Returns the actual number of bytes released.
// Caller must hold n.mu as a write lock.
func (n *memoryNamespace) compressColdFilesLocked(targetFreed int64) int64 {
	candidates := n.compressionCandidatesLocked()
	var freed int64
	for _, name := range candidates {
		if freed >= targetFreed {
			return freed
		}
		file := n.files[name]
		if file == nil || file.compressed {
			continue
		}
		rawSize := len(file.content)
		if rawSize == 0 {
			continue
		}
		compressed, err := flateCompress(file.content)
		if err != nil || len(compressed) >= rawSize {
			// Compression failed or produced a larger result (already
			// dense, random, or tiny). Skip — nothing to gain here.
			continue
		}
		delta := int64(rawSize - len(compressed))
		file.content = compressed
		file.compressed = true
		file.rawSize = rawSize
		n.releaseBytesLocked(delta)
		freed += delta
	}
	return freed
}

// compressionCandidatesLocked returns file paths sorted by ascending access
// time — the coldest files come first. Files that are already compressed
// or empty are still included (filtered at compression time) to keep the
// sort stable and simple.
func (n *memoryNamespace) compressionCandidatesLocked() []string {
	names := make([]string, 0, len(n.files))
	for name := range n.files {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		ai := n.files[names[i]].accessTime
		aj := n.files[names[j]].accessTime
		if ai.Equal(aj) {
			return names[i] < names[j]
		}
		return ai.Before(aj)
	})
	return names
}

// decompressFileContent returns the raw bytes of a possibly-compressed
// file. Pure function over a memoryFile; does not mutate namespace state
// or budget. Callers decide whether to keep the raw bytes in memory (and
// pay the reservation) or discard them after serving.
func decompressFileContent(file *memoryFile) ([]byte, error) {
	if !file.compressed {
		return cloneBytes(file.content), nil
	}
	return flateDecompress(file.content)
}

// maybeInflateInPlaceLocked tries to replace a compressed file's content
// with its uncompressed bytes, reserving the delta against the budget. If
// the reservation fails (even after a compression pass of colder files),
// the function leaves the file compressed and returns the decompressed
// bytes as a transient buffer. Caller must hold n.mu as a write lock.
func (n *memoryNamespace) maybeInflateInPlaceLocked(name string) ([]byte, error) {
	file := n.files[name]
	if file == nil {
		return nil, nil
	}
	if !file.compressed {
		return cloneBytes(file.content), nil
	}
	raw, err := flateDecompress(file.content)
	if err != nil {
		return nil, err
	}
	delta := int64(len(raw) - len(file.content))
	if delta <= 0 {
		// Should not happen (compressed size > raw size means we stored a
		// bad compression result), but defensively restore in place.
		file.content = raw
		file.compressed = false
		file.rawSize = 0
		return cloneBytes(raw), nil
	}
	if err := n.reserveBytesLocked(delta); err != nil {
		// Try compressing colder files to make room for the decompression.
		if freed := n.compressColdFilesLocked(delta); freed >= delta {
			if retryErr := n.reserveBytesLocked(delta); retryErr == nil {
				file.content = raw
				file.compressed = false
				file.rawSize = 0
				return cloneBytes(raw), nil
			}
		}
		// Still no room — serve the raw bytes as transient; file stays
		// compressed in storage.
		return raw, nil
	}
	file.content = raw
	file.compressed = false
	file.rawSize = 0
	return cloneBytes(raw), nil
}

// flateCompress encodes content with DEFLATE level 1 (fast). Level 1 hits
// ~2-3x compression on typical text and binaries (pytest cache, npm
// logs, source files) at ~500 MB/s on commodity CPUs — fast enough that
// compression latency does not dominate broker throughput.
func flateCompress(content []byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(content) / 2)
	writer, err := flate.NewWriter(&buf, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(content); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// flateDecompress reverses flateCompress. Used both by in-place inflation
// and by transient reads when the namespace is under memory pressure.
func flateDecompress(compressed []byte) ([]byte, error) {
	reader := flate.NewReader(bytes.NewReader(compressed))
	defer reader.Close()
	return io.ReadAll(reader)
}

// touchAccessLocked bumps a file's access time to the current moment.
// Access time drives the cold-file sort order during compression passes;
// a recently-touched file is hot and should stay uncompressed.
func (n *memoryNamespace) touchAccessLocked(file *memoryFile) {
	file.accessTime = time.Now().UTC()
}
