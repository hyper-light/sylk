//go:build linux

package sylkdir

import (
	"os"
	"strconv"
	"strings"
)

// measureSystemPressure reads /proc/meminfo to calculate memory pressure.
// Returns (MemTotal - MemAvailable) / MemTotal, capped at [0, 1].
func measureSystemPressure() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	return parseProcMeminfo(data)
}

// parseProcMeminfo extracts MemTotal and MemAvailable from /proc/meminfo content.
func parseProcMeminfo(data []byte) float64 {
	var total, available uint64
	for _, line := range strings.Split(string(data), "\n") {
		total, available = parseMemLine(line, total, available)
	}
	if total == 0 {
		return 0
	}
	used := float64(total-available) / float64(total)
	return clampPressure(used)
}

// parseMemLine extracts a memory value from a single /proc/meminfo line.
func parseMemLine(line string, total, available uint64) (uint64, uint64) {
	switch {
	case strings.HasPrefix(line, "MemTotal:"):
		total = parseMeminfoKB(line)
	case strings.HasPrefix(line, "MemAvailable:"):
		available = parseMeminfoKB(line)
	}
	return total, available
}

// parseMeminfoKB extracts the kB value from a /proc/meminfo line like "MemTotal:  1234 kB".
func parseMeminfoKB(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	val, _ := strconv.ParseUint(fields[1], 10, 64)
	return val
}

// clampPressure ensures the value is in [0, 1].
func clampPressure(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}
