//go:build !ORT && !ALL

package embedder

func isORTEnabled() bool {
	return false
}
