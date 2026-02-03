package sylkdir

import (
	"math"
	"runtime"
)

// MemoryPressure reports memory utilization from Go runtime and OS.
type MemoryPressure struct {
	GoPressure  float64 // HeapInuse / HeapSys, range [0, 1]
	SysPressure float64 // OS-level pressure, range [0, 1]
	Combined    float64 // min(max(GoPressure, SysPressure), 1.0)
}

// MeasureMemoryPressure returns the current memory pressure from Go runtime
// and system-level metrics. Combined is capped at 1.0.
func MeasureMemoryPressure() MemoryPressure {
	goPressure := measureGoPressure()
	sysPressure := measureSystemPressure()
	return MemoryPressure{
		GoPressure:  goPressure,
		SysPressure: sysPressure,
		Combined:    math.Min(math.Max(goPressure, sysPressure), 1.0),
	}
}

// measureGoPressure returns HeapInuse/HeapSys from the Go runtime.
func measureGoPressure() float64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	if stats.HeapSys == 0 {
		return 0
	}
	return float64(stats.HeapInuse) / float64(stats.HeapSys)
}
