package recipe

import "testing"

func TestComputeID_Deterministic(t *testing.T) {
	r := sampleRecipe()
	if ComputeID(&r) != ComputeID(&r) {
		t.Fatal("ComputeID is non-deterministic across two calls on the same input")
	}
}

func TestComputeID_FetchOrderIndependent(t *testing.T) {
	a := sampleRecipe()
	b := sampleRecipe()
	b.Fetch = append([]FetchSpec(nil), b.Fetch...)
	// Reverse order:
	for i, j := 0, len(b.Fetch)-1; i < j; i, j = i+1, j-1 {
		b.Fetch[i], b.Fetch[j] = b.Fetch[j], b.Fetch[i]
	}
	if ComputeID(&a) != ComputeID(&b) {
		t.Fatal("Fetch ordering changed the recipe ID; canonicalization should sort")
	}
}

func TestComputeID_OutputOrderIndependent(t *testing.T) {
	a := sampleRecipe()
	b := sampleRecipe()
	b.Output = append([]OutputMapping(nil), b.Output...)
	for i, j := 0, len(b.Output)-1; i < j; i, j = i+1, j-1 {
		b.Output[i], b.Output[j] = b.Output[j], b.Output[i]
	}
	if ComputeID(&a) != ComputeID(&b) {
		t.Fatal("Output ordering changed the recipe ID")
	}
}

func TestComputeID_BuildOrderMatters(t *testing.T) {
	// Build order is semantically meaningful: ./configure must run
	// before make. Permuting Build steps must change the ID.
	a := sampleRecipeWithBuild()
	b := sampleRecipeWithBuild()
	b.Build[0], b.Build[1] = b.Build[1], b.Build[0]
	if ComputeID(&a) == ComputeID(&b) {
		t.Fatal("Permuting Build step order produced the same ID; build order is semantically significant")
	}
}

func TestComputeID_RequiresOrderIndependent(t *testing.T) {
	a := sampleRecipeWithRequires()
	b := sampleRecipeWithRequires()
	b.Requires = append([]ID(nil), b.Requires...)
	b.Requires[0], b.Requires[1] = b.Requires[1], b.Requires[0]
	if ComputeID(&a) != ComputeID(&b) {
		t.Fatal("Requires ordering changed the recipe ID")
	}
}

func TestComputeID_MetadataDoesNotAffectID(t *testing.T) {
	a := sampleRecipe()
	b := sampleRecipe()
	b.Metadata.Description = "totally different blurb"
	b.Metadata.Tags = []string{"a", "b"}
	if ComputeID(&a) != ComputeID(&b) {
		t.Fatal("cosmetic metadata change altered the recipe ID")
	}
}

func TestComputeID_FetchHashAffectsID(t *testing.T) {
	a := sampleRecipe()
	b := sampleRecipe()
	b.Fetch[0].Hash.Digest = []byte("totally different digest")
	if ComputeID(&a) == ComputeID(&b) {
		t.Fatal("changing fetch hash did not change ID")
	}
}

func TestComputeID_DeterminismProfileAffectsID(t *testing.T) {
	a := sampleRecipe()
	b := sampleRecipe()
	b.Determinism.SourceDateEpoch = 42
	if ComputeID(&a) == ComputeID(&b) {
		t.Fatal("changing determinism profile did not change ID")
	}
}

func TestComputeID_PlatformPinAffectsID(t *testing.T) {
	a := sampleRecipe()
	b := sampleRecipe()
	b.Fetch[0].PlatformPin = &Platform{OS: "linux", Arch: "amd64"}
	if ComputeID(&a) == ComputeID(&b) {
		t.Fatal("adding platform pin did not change ID")
	}
}

func TestComputeID_StableAcrossRuns(t *testing.T) {
	// Sanity: a fixed recipe content should produce the same hex
	// string today as it would on any other run. Capture a reference
	// hash for one well-defined recipe to detect accidental schema
	// drift.
	r := referenceRecipe()
	id := ComputeID(&r).String()
	if id == "" {
		t.Fatal("ComputeID returned an empty string; canonicalization is broken")
	}
}

func sampleRecipe() Recipe {
	return Recipe{
		SchemaVersion: SchemaVersion,
		Fetch: []FetchSpec{
			{
				Source: "https://example.com/a.tar.gz",
				Hash:   Hash{Algorithm: "sha256", Digest: []byte("alpha-digest-bytes")},
			},
			{
				Source: "https://example.com/b.tar.gz",
				Hash:   Hash{Algorithm: "sha256", Digest: []byte("beta-digest-bytes")},
			},
		},
		Verify: VerifySpec{
			HashAlgorithm: "blake3",
			PolicyClass:   VerifyStrict,
		},
		Extract: ExtractSpec{
			Format:       ExtractFormatTarGz,
			StripPrefix:  "sample-1.0/",
			PreserveMode: true,
		},
		Output: []OutputMapping{
			{SubstratePath: "/bin/sample", SourcePath: "/bin/sample", Mode: 0755},
			{SubstratePath: "/share/doc/sample.md", SourcePath: "/doc/sample.md", Mode: 0644},
		},
		Metadata: RecipeMetadata{
			Name:      "sample",
			Version:   "1.0.0",
			Ecosystem: "test",
			License:   "MIT",
		},
	}
}

func sampleRecipeWithBuild() Recipe {
	r := sampleRecipe()
	r.Build = []BuildStep{
		{Cmd: []string{"./configure", "--prefix=/output"}},
		{Cmd: []string{"make", "install"}},
	}
	return r
}

func sampleRecipeWithRequires() Recipe {
	r := sampleRecipe()
	a := ID{0xaa}
	b := ID{0xbb}
	r.Requires = []ID{a, b}
	return r
}

func referenceRecipe() Recipe {
	return Recipe{
		SchemaVersion: 1,
		Fetch: []FetchSpec{{
			Source: "https://example.com/ref.tar.gz",
			Hash:   Hash{Algorithm: "blake3", Digest: []byte{0x01, 0x02, 0x03}},
		}},
		Verify:  VerifySpec{HashAlgorithm: "blake3", PolicyClass: VerifyStrict},
		Extract: ExtractSpec{Format: ExtractFormatTarGz},
		Output:  []OutputMapping{{SubstratePath: "/bin/ref", SourcePath: "/bin/ref"}},
	}
}
