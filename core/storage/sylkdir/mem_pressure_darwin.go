//go:build darwin

package sylkdir

import (
	"os/exec"
	"strconv"
	"strings"
)

// measureSystemPressure uses sysctl to estimate memory pressure on macOS.
// Returns a value in [0, 1] based on hw.memsize and vm_stat output.
func measureSystemPressure() float64 {
	total := readSysctlUint64("hw.memsize")
	if total == 0 {
		return 0
	}

	// vm_stat reports pages; page size is typically 16384 on Apple Silicon, 4096 on Intel.
	pageSize := readSysctlUint64("hw.pagesize")
	if pageSize == 0 {
		pageSize = 4096
	}

	freePages := readVMStatField("Pages free")
	inactivePages := readVMStatField("Pages inactive")

	available := (freePages + inactivePages) * pageSize
	if available >= total {
		return 0
	}

	used := float64(total-available) / float64(total)
	return clampDarwinPressure(used)
}

// clampDarwinPressure ensures the value is in [0, 1].
func clampDarwinPressure(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// readSysctlUint64 reads a numeric sysctl value.
func readSysctlUint64(key string) uint64 {
	out, err := exec.Command("sysctl", "-n", key).Output()
	if err != nil {
		return 0
	}
	val, _ := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	return val
}

// readVMStatField reads a named field from vm_stat output.
func readVMStatField(field string) uint64 {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, field) {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 2 {
			continue
		}
		val, _ := strconv.ParseUint(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[1]), ".")), 10, 64)
		return val
	}
	return 0
}
