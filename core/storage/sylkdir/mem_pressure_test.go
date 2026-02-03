package sylkdir

import "testing"

func TestMemoryPressure_Range(t *testing.T) {
	p := MeasureMemoryPressure()

	if p.GoPressure < 0 || p.GoPressure > 1 {
		t.Errorf("GoPressure = %f, want [0, 1]", p.GoPressure)
	}
	if p.SysPressure < 0 || p.SysPressure > 1 {
		t.Errorf("SysPressure = %f, want [0, 1]", p.SysPressure)
	}
	if p.Combined < 0 || p.Combined > 1 {
		t.Errorf("Combined = %f, want [0, 1]", p.Combined)
	}
}

func TestMemoryPressure_CombinedIsMax(t *testing.T) {
	p := MeasureMemoryPressure()

	// Combined should be >= both components (it's max, then capped).
	if p.Combined < p.GoPressure && p.GoPressure <= 1 {
		t.Errorf("Combined (%f) < GoPressure (%f)", p.Combined, p.GoPressure)
	}
	if p.Combined < p.SysPressure && p.SysPressure <= 1 {
		t.Errorf("Combined (%f) < SysPressure (%f)", p.Combined, p.SysPressure)
	}
}

func TestMemoryPressure_GoHeapNonZero(t *testing.T) {
	p := MeasureMemoryPressure()

	// Go runtime should always report some heap usage during a test.
	if p.GoPressure == 0 {
		t.Error("GoPressure = 0, expected nonzero during test execution")
	}
}
