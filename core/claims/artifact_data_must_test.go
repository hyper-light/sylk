package claims

// MustArtifactData is intentionally test-only. Production callers must
// handle typed artifact decode failures explicitly.
func MustArtifactData[T any](artifact *Artifact) T {
	value, err := ArtifactData[T](artifact)
	if err != nil {
		panic(err)
	}
	return value
}
