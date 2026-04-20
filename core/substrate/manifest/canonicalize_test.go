package manifest

import (
	"testing"

	"github.com/adalundhe/sylk/core/substrate/blob"
)

func TestComputeID_Deterministic(t *testing.T) {
	m := sampleManifest()
	id1 := ComputeID(&m)
	id2 := ComputeID(&m)
	if id1 != id2 {
		t.Fatal("ComputeID is non-deterministic across two calls on the same input")
	}
}

func TestComputeID_OrderIndependentForFiles(t *testing.T) {
	a := sampleManifest()
	b := sampleManifest()
	// Permute file order.
	b.Files[0], b.Files[1] = b.Files[1], b.Files[0]

	if ComputeID(&a) != ComputeID(&b) {
		t.Fatal("file ordering changes ID; ComputeID should sort files for canonicalization")
	}
}

func TestComputeID_PackageDifferenceChangesID(t *testing.T) {
	a := sampleManifest()
	b := sampleManifest()
	b.Package.Version = "9.9.9"

	if ComputeID(&a) == ComputeID(&b) {
		t.Fatal("differing version produced the same ID")
	}
}

func TestComputeID_FileContentDifferenceChangesID(t *testing.T) {
	a := sampleManifest()
	b := sampleManifest()
	b.Files[0].BlobHash = blob.Sum([]byte("totally different content"))

	if ComputeID(&a) == ComputeID(&b) {
		t.Fatal("differing blob hash on a file produced the same manifest ID")
	}
}

func TestComputeID_ModeDifferenceChangesID(t *testing.T) {
	a := sampleManifest()
	b := sampleManifest()
	b.Files[0].Mode = 0644

	if ComputeID(&a) == ComputeID(&b) {
		t.Fatal("differing mode produced the same ID")
	}
}

func TestComputeID_MetadataDoesNotAffectID(t *testing.T) {
	a := sampleManifest()
	b := sampleManifest()
	b.Metadata.Description = "completely different description"
	b.Metadata.Tags = []string{"different", "tags"}

	if ComputeID(&a) != ComputeID(&b) {
		t.Fatal("cosmetic metadata difference changed the ID; only load-bearing fields should affect identity")
	}
}

func TestComputeID_SourceDifferenceChangesID(t *testing.T) {
	a := sampleManifest()
	b := sampleManifest()
	b.Source.RecipeID = "different-recipe"

	if ComputeID(&a) == ComputeID(&b) {
		t.Fatal("differing source recipe ID did not change manifest ID")
	}
}

func sampleManifest() Manifest {
	return Manifest{
		Package: PackageID{Ecosystem: "test", Name: "sample", Version: "1.0.0"},
		Files: []FileEntry{
			{Path: "/bin/sample", BlobHash: blob.Sum([]byte("binary content")), Mode: 0755},
			{Path: "/share/doc/sample.md", BlobHash: blob.Sum([]byte("doc content")), Mode: 0644},
		},
		Source: SourceMetadata{
			RecipeID:               "test-recipe-id",
			FetchURL:               "https://example.com/sample-1.0.0.tar.gz",
			FetchHash:              blob.Sum([]byte("archive bytes")),
			EcosystemHash:          "deadbeef",
			EcosystemHashAlgorithm: "sha256",
		},
	}
}
