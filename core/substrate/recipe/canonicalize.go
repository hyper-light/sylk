package recipe

import (
	"sort"

	"lukechampine.com/blake3"
)

// ComputeID derives the RecipeID from the load-bearing fields of r.
//
// Hashed fields:
//   - SchemaVersion
//   - Fetch (sorted by Source for determinism)
//   - Verify
//   - Extract
//   - Requires (sorted by ID)
//   - Build (in declaration order — ordering is semantically meaningful)
//   - Determinism
//   - Output (sorted by SubstratePath)
//
// Excluded: ID itself, CreatedAt, Metadata. Cosmetic metadata
// differences should not change a recipe's identity. The recipe's ID is
// computed *from* the fields, so including it would be circular.
//
// All variable-length fields are length-prefixed before hashing to
// prevent boundary-injection attacks where one field's content could
// masquerade as another's separator.
func ComputeID(r *Recipe) ID {
	h := blake3.New(IDSize, nil)
	hashSchemaVersion(h, r.SchemaVersion)
	hashFetchList(h, r.Fetch)
	hashVerify(h, &r.Verify)
	hashExtract(h, &r.Extract)
	hashRequiresList(h, r.Requires)
	hashBuildList(h, r.Build)
	hashDeterminism(h, &r.Determinism)
	hashOutputList(h, r.Output)
	var out ID
	copy(out[:], h.Sum(nil))
	return out
}

type writer interface {
	Write([]byte) (int, error)
}

func hashSchemaVersion(h writer, v int) {
	hashU32(h, uint32(v))
}

func hashFetchList(h writer, fetches []FetchSpec) {
	sorted := sortedFetches(fetches)
	hashU32(h, uint32(len(sorted)))
	for i := range sorted {
		hashFetch(h, &sorted[i])
	}
}

func sortedFetches(fetches []FetchSpec) []FetchSpec {
	out := make([]FetchSpec, len(fetches))
	copy(out, fetches)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Source < out[j].Source
	})
	return out
}

func hashFetch(h writer, f *FetchSpec) {
	hashStr(h, f.Source)
	hashHash(h, f.Hash)
	hashStringMap(h, f.Headers)
	hashPlatformPtr(h, f.PlatformPin)
	hashSignaturePtr(h, f.Signature)
}

func hashHash(h writer, hash Hash) {
	hashStr(h, hash.Algorithm)
	hashBytes(h, hash.Digest)
}

func hashSignaturePtr(h writer, s *Signature) {
	if s == nil {
		hashBool(h, false)
		return
	}
	hashBool(h, true)
	hashStr(h, s.Format)
	hashBytes(h, s.Payload)
}

func hashPlatformPtr(h writer, p *Platform) {
	if p == nil {
		hashBool(h, false)
		return
	}
	hashBool(h, true)
	hashPlatform(h, *p)
}

func hashPlatform(h writer, p Platform) {
	hashStr(h, p.OS)
	hashStr(h, p.Arch)
	hashStr(h, p.Libc)
}

func hashVerify(h writer, v *VerifySpec) {
	hashStr(h, v.HashAlgorithm)
	hashStr(h, string(v.PolicyClass))
}

func hashExtract(h writer, e *ExtractSpec) {
	hashStr(h, string(e.Format))
	hashStr(h, e.StripPrefix)
	hashBool(h, e.PreserveMode)
	hashFilterPtr(h, e.Filter)
}

func hashFilterPtr(h writer, f *Filter) {
	if f == nil {
		hashBool(h, false)
		return
	}
	hashBool(h, true)
	hashStringSlice(h, f.Include)
	hashStringSlice(h, f.Exclude)
}

func hashRequiresList(h writer, ids []ID) {
	sorted := make([]ID, len(ids))
	copy(sorted, ids)
	sort.SliceStable(sorted, func(i, j int) bool {
		return compareIDs(sorted[i], sorted[j]) < 0
	})
	hashU32(h, uint32(len(sorted)))
	for i := range sorted {
		hashBytes(h, sorted[i][:])
	}
}

func compareIDs(a, b ID) int {
	for i := range a {
		if a[i] != b[i] {
			return int(a[i]) - int(b[i])
		}
	}
	return 0
}

func hashBuildList(h writer, steps []BuildStep) {
	hashU32(h, uint32(len(steps)))
	for i := range steps {
		hashBuildStep(h, &steps[i])
	}
}

func hashBuildStep(h writer, b *BuildStep) {
	hashStringSlice(h, b.Cmd)
	hashStringMap(h, b.Env)
	hashStr(h, b.Cwd)
	hashU64(h, uint64(b.Timeout))
	hashSandboxOverridePtr(h, b.SandboxOverride)
}

func hashSandboxOverridePtr(h writer, o *SandboxStepOverride) {
	if o == nil {
		hashBool(h, false)
		return
	}
	hashBool(h, true)
	hashBool(h, o.AllowNetwork)
}

func hashDeterminism(h writer, d *DeterminismProfile) {
	hashU64(h, uint64(d.SourceDateEpoch))
	hashStr(h, d.Locale)
	hashStr(h, d.Hostname)
	hashStr(h, d.User)
	hashStr(h, d.Home)
	hashStr(h, string(d.MtimePolicy))
	hashStringSlice(h, d.CompilerFlags)
	hashStringSlice(h, d.LinkerFlags)
	hashStringSlice(h, d.EnvScrub)
	hashStringMap(h, d.EnvAdd)
}

func hashOutputList(h writer, outputs []OutputMapping) {
	sorted := make([]OutputMapping, len(outputs))
	copy(sorted, outputs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].SubstratePath < sorted[j].SubstratePath
	})
	hashU32(h, uint32(len(sorted)))
	for i := range sorted {
		hashOutput(h, &sorted[i])
	}
}

func hashOutput(h writer, o *OutputMapping) {
	hashStr(h, o.SubstratePath)
	hashStr(h, o.SourcePath)
	hashU32(h, uint32(o.Mode))
	hashBool(h, o.Symlink)
}

// Length-prefixed primitive writers.

func hashStr(h writer, s string) {
	hashBytes(h, []byte(s))
}

func hashBytes(h writer, b []byte) {
	hashU32(h, uint32(len(b)))
	_, _ = h.Write(b)
}

func hashStringSlice(h writer, ss []string) {
	hashU32(h, uint32(len(ss)))
	for _, s := range ss {
		hashStr(h, s)
	}
}

func hashStringMap(h writer, m map[string]string) {
	keys := sortedKeys(m)
	hashU32(h, uint32(len(keys)))
	for _, k := range keys {
		hashStr(h, k)
		hashStr(h, m[k])
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func hashBool(h writer, b bool) {
	flag := byte(0)
	if b {
		flag = 1
	}
	_, _ = h.Write([]byte{flag})
}

func hashU32(h writer, n uint32) {
	buf := []byte{
		byte(n >> 24),
		byte(n >> 16),
		byte(n >> 8),
		byte(n),
	}
	_, _ = h.Write(buf)
}

func hashU64(h writer, n uint64) {
	buf := []byte{
		byte(n >> 56),
		byte(n >> 48),
		byte(n >> 40),
		byte(n >> 32),
		byte(n >> 24),
		byte(n >> 16),
		byte(n >> 8),
		byte(n),
	}
	_, _ = h.Write(buf)
}
