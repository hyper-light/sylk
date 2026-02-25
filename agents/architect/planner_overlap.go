package architect

import (
	"hash/fnv"
	"strings"
)

// overlapResult holds the outcome of overlap detection between the tail of
// accumulated text and the head of a continuation.
type overlapResult struct {
	overlapLen int    // bytes of overlap removed
	netNew     string // continuation with overlap prefix stripped
}

// detectOverlap finds the longest suffix of accumulated that matches a prefix
// of continuation using the Z-algorithm (O(n) worst case). The searchLimit
// bounds how many bytes of accumulated's tail to examine.
func detectOverlap(accumulated, continuation string, searchLimit int) overlapResult {
	tail := suffixBounded(accumulated, searchLimit)
	if tail == "" || continuation == "" {
		return overlapResult{netNew: continuation}
	}
	overlap := longestSuffixPrefixOverlap(tail, continuation)
	return overlapResult{
		overlapLen: overlap,
		netNew:     continuation[overlap:],
	}
}

// detectModeOverlap applies byte-level overlap detection first, then falls back
// to line-level detection for text mode when no byte-level overlap is found.
func detectModeOverlap(mode continuationMode, accumulated, continuation string, searchLimit int) overlapResult {
	result := detectOverlap(accumulated, continuation, searchLimit)
	if result.overlapLen > 0 || mode == continuationModeJSON {
		return result
	}
	return detectLineOverlap(accumulated, continuation, searchLimit)
}

// longestSuffixPrefixOverlap finds the length of the longest string that is
// both a suffix of a and a prefix of b. Uses the Z-algorithm in O(|a|+|b|).
func longestSuffixPrefixOverlap(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	// Construct S = B + sentinel + A. Z-values in the A portion where
	// z[i]+i == len(S) identify suffix-prefix overlaps.
	s := b + "\x00" + a
	z := computeZArray(s)
	return maxTerminalZValue(z, len(b)+1)
}

// maxTerminalZValue finds the largest z[i] where i+z[i] == len(z) and i >= start.
// These positions correspond to suffixes of the tail matching prefixes of the
// continuation in the concatenated Z-array string.
func maxTerminalZValue(z []int, start int) int {
	best := 0
	for i := start; i < len(z); i++ {
		best = maxTerminalCandidate(best, z[i], i, len(z))
	}
	return best
}

// maxTerminalCandidate returns zi if it represents a terminal overlap longer
// than the current best, otherwise returns best unchanged.
func maxTerminalCandidate(best, zi, i, n int) int {
	if i+zi == n && zi > best {
		return zi
	}
	return best
}

// computeZArray returns the Z-array where z[i] is the length of the longest
// substring starting at s[i] matching a prefix of s. O(n) time and space.
func computeZArray(s string) []int {
	n := len(s)
	z := make([]int, n)
	if n == 0 {
		return z
	}
	var box zBox
	for i := 1; i < n; i++ {
		z[i] = box.seed(z, i)
		z[i] = extendZMatch(s, i, z[i])
		box.extend(i, z[i])
	}
	return z
}

// zBox tracks the rightmost Z-box [l, r) during Z-array computation.
type zBox struct {
	l, r int
}

// seed returns the initial Z[i] value from the current Z-box. If i falls
// within the box, the previously computed Z value gives a lower bound.
func (b *zBox) seed(z []int, i int) int {
	if i >= b.r {
		return 0
	}
	return min(b.r-i, z[i-b.l])
}

// extend updates the Z-box if position i's match extends past the current
// right boundary.
func (b *zBox) extend(i, zi int) {
	if i+zi > b.r {
		b.l = i
		b.r = i + zi
	}
}

// extendZMatch extends a Z-value by comparing characters starting at offset k
// in the string against characters starting at i+k.
func extendZMatch(s string, i, k int) int {
	for i+k < len(s) && s[k] == s[i+k] {
		k++
	}
	return k
}

// suffixBounded returns the last maxBytes bytes of s, or s if shorter.
func suffixBounded(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[len(s)-maxBytes:]
}

// --- Line-level overlap (markdown fallback) ---

// detectLineOverlap finds overlap at line granularity. Lines are compared with
// trailing whitespace trimmed, making it robust to minor reformatting.
func detectLineOverlap(accumulated, continuation string, maxBytes int) overlapResult {
	accLines := tailLinesBounded(accumulated, maxBytes)
	contLines := headLinesBounded(continuation, maxBytes)
	overlap := lineSuffixPrefixOverlap(accLines, contLines)
	return buildLineOverlapResult(continuation, overlap, contLines)
}

// lineSuffixPrefixOverlap returns the number of matching lines between the
// suffix of accLines and prefix of contLines. Returns 0 if either is empty.
func lineSuffixPrefixOverlap(accLines, contLines []string) int {
	if len(accLines) == 0 || len(contLines) == 0 {
		return 0
	}
	return longestLineSuffixPrefix(accLines, contLines)
}

// longestLineSuffixPrefix finds the longest k such that the last k lines of a
// match the first k lines of b (with trailing whitespace trimmed).
// Uses FNV-1a hashing + Z-algorithm for O(n) performance.
func longestLineSuffixPrefix(a, b []string) int {
	trimmedA := trimLines(a)
	trimmedB := trimLines(b)
	aHashes := hashLines(trimmedA)
	bHashes := hashLines(trimmedB)

	// Build S = bHashes + sentinel + aHashes.
	// Z-values in the aHashes portion where z[i]+i == len(S) identify
	// suffixes of a that match prefixes of b (via hash equality).
	// Sentinel uses 1 (FNV-1a never produces 1 for non-pathological input;
	// verified by collision guard below).
	n := len(bHashes) + 1 + len(aHashes)
	s := make([]uint64, n)
	copy(s, bHashes)
	s[len(bHashes)] = 1 // sentinel
	copy(s[len(bHashes)+1:], aHashes)

	z := computeUint64ZArray(s)
	best := maxTerminalUint64ZValue(z, len(bHashes)+1)
	if best == 0 {
		return 0
	}

	// Verify the hash match against actual trimmed lines (collision guard).
	if linesEqual(trimmedA[len(trimmedA)-best:], trimmedB[:best]) {
		return best
	}
	// Hash collision — fall back to linear scan (astronomically unlikely).
	return longestLineSuffixPrefixLinear(trimmedA, trimmedB)
}

// longestLineSuffixPrefixLinear is the O(n^2) fallback used only on hash collision.
func longestLineSuffixPrefixLinear(a, b []string) int {
	maxK := min(len(a), len(b))
	for k := maxK; k > 0; k-- {
		if linesEqual(a[len(a)-k:], b[:k]) {
			return k
		}
	}
	return 0
}

// trimLines returns a new slice where each line has trailing whitespace stripped.
func trimLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(line, " \t\r")
	}
	return out
}

// hashLines computes an FNV-1a hash for each trimmed line.
func hashLines(lines []string) []uint64 {
	out := make([]uint64, len(lines))
	for i, line := range lines {
		out[i] = fnv1aString(line)
	}
	return out
}

// fnv1aString returns the FNV-1a 64-bit hash of s.
func fnv1aString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// computeUint64ZArray returns the Z-array for a uint64 slice. z[i] is the
// length of the longest prefix of s that matches s[i:]. O(n) time and space.
func computeUint64ZArray(s []uint64) []int {
	n := len(s)
	z := make([]int, n)
	if n == 0 {
		return z
	}
	var l, r int
	for i := 1; i < n; i++ {
		if i < r {
			z[i] = min(r-i, z[i-l])
		}
		for i+z[i] < n && s[z[i]] == s[i+z[i]] {
			z[i]++
		}
		if i+z[i] > r {
			l = i
			r = i + z[i]
		}
	}
	return z
}

// maxTerminalUint64ZValue finds the largest z[i] where i+z[i] == len(z)
// and i >= start.
func maxTerminalUint64ZValue(z []int, start int) int {
	best := 0
	for i := start; i < len(z); i++ {
		if i+z[i] == len(z) && z[i] > best {
			best = z[i]
		}
	}
	return best
}

// linesEqual returns true if corresponding lines in a and b are equal.
// Caller must ensure len(a) == len(b).
func linesEqual(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// trimLine strips trailing spaces, tabs, and carriage returns from a line.
func trimLine(s string) string {
	return strings.TrimRight(s, " \t\r")
}

// buildLineOverlapResult constructs an overlapResult from a line-level overlap
// count, computing the byte offset into the continuation string.
func buildLineOverlapResult(continuation string, overlapLines int, contLines []string) overlapResult {
	if overlapLines == 0 {
		return overlapResult{netNew: continuation}
	}
	byteLen := joinedByteLength(contLines[:overlapLines])
	return overlapResult{
		overlapLen: byteLen,
		netNew:     continuation[byteLen:],
	}
}

// joinedByteLength returns the total byte length of lines joined by newlines.
func joinedByteLength(lines []string) int {
	if len(lines) == 0 {
		return 0
	}
	total := len(lines) - 1 // newlines between lines
	for _, line := range lines {
		total += len(line)
	}
	return total
}

// tailLinesBounded returns the lines from the last maxBytes of s.
func tailLinesBounded(s string, maxBytes int) []string {
	return strings.Split(suffixBounded(s, maxBytes), "\n")
}

// headLinesBounded returns the lines from the first maxBytes of s.
func headLinesBounded(s string, maxBytes int) []string {
	if len(s) > maxBytes {
		s = s[:maxBytes]
	}
	return strings.Split(s, "\n")
}
