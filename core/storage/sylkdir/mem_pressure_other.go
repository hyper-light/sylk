//go:build !linux && !darwin

package sylkdir

// measureSystemPressure returns 0 on unsupported platforms.
// Only Go runtime pressure is available via MeasureMemoryPressure.
func measureSystemPressure() float64 {
	return 0
}
