package embedder

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// HardwareCapabilities describes the available compute resources.
type HardwareCapabilities struct {
	HasNVIDIAGPU bool
	GPUName      string
	VRAMGB       float64
	SystemRAMGB  float64
	CPUCores     int
}

var (
	cachedCapabilities *HardwareCapabilities
	capabilitiesMu     sync.Mutex
)

// DetectHardware probes the system for GPU and memory information.
// Results are cached after first call.
func DetectHardware() HardwareCapabilities {
	capabilitiesMu.Lock()
	defer capabilitiesMu.Unlock()

	if cachedCapabilities != nil {
		return *cachedCapabilities
	}

	caps := HardwareCapabilities{
		CPUCores:    runtime.NumCPU(),
		SystemRAMGB: detectSystemRAM(),
	}

	// Try NVIDIA GPU detection
	if vram, name, ok := detectNVIDIAGPU(); ok {
		caps.HasNVIDIAGPU = true
		caps.GPUName = name
		caps.VRAMGB = vram
	}

	cachedCapabilities = &caps
	return caps
}

// ResetHardwareCache clears the cached hardware detection results.
// Useful for testing.
func ResetHardwareCache() {
	capabilitiesMu.Lock()
	cachedCapabilities = nil
	capabilitiesMu.Unlock()
}

// detectNVIDIAGPU attempts to detect NVIDIA GPU.
// Returns VRAM in GB, GPU name, and success flag.
func detectNVIDIAGPU() (float64, string, bool) {
	// Try sysfs first (instant, no driver cold-start)
	if vram, name, ok := detectViaSysfs(); ok {
		return vram, name, true
	}

	// Fallback to nvidia-smi (may have 1-2s cold-start on first call)
	if vram, name, ok := detectViaNvidiaSMI(); ok {
		return vram, name, true
	}

	return 0, "", false
}

// detectViaSysfs reads GPU info from Linux sysfs (instant, no driver init).
// VRAM is extracted from PCI BAR1 which maps GPU memory.
func detectViaSysfs() (float64, string, bool) {
	if runtime.GOOS != "linux" {
		return 0, "", false
	}

	pciDevices := "/sys/bus/pci/devices"
	entries, err := os.ReadDir(pciDevices)
	if err != nil {
		return 0, "", false
	}

	for _, entry := range entries {
		devicePath := filepath.Join(pciDevices, entry.Name())

		// Check vendor ID (NVIDIA = 0x10de)
		vendorData, err := os.ReadFile(filepath.Join(devicePath, "vendor"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(vendorData)) != "0x10de" {
			continue
		}

		// Check device class (display controller = 0x03xxxx)
		classData, err := os.ReadFile(filepath.Join(devicePath, "class"))
		if err != nil {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(string(classData)), "0x03") {
			continue
		}

		// Found NVIDIA GPU - extract VRAM from PCI resource file
		vram := parseVRAMFromResource(filepath.Join(devicePath, "resource"))

		// Try to get device name from device ID
		name := "NVIDIA GPU"
		if deviceData, err := os.ReadFile(filepath.Join(devicePath, "device")); err == nil {
			deviceID := strings.TrimSpace(string(deviceData))
			name = lookupNVIDIADeviceName(deviceID)
		}

		return vram, name, true
	}

	return 0, "", false
}

// parseVRAMFromResource extracts VRAM size from PCI resource file.
// BAR1 (second line) contains the GPU memory mapping.
func parseVRAMFromResource(path string) float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 {
		return 0
	}

	// BAR1 is the second line (index 1), contains VRAM mapping
	// Format: "start_addr end_addr flags"
	fields := strings.Fields(lines[1])
	if len(fields) < 2 {
		return 0
	}

	start, err := strconv.ParseUint(strings.TrimPrefix(fields[0], "0x"), 16, 64)
	if err != nil {
		return 0
	}
	end, err := strconv.ParseUint(strings.TrimPrefix(fields[1], "0x"), 16, 64)
	if err != nil {
		return 0
	}

	if end <= start {
		return 0
	}

	sizeBytes := end - start + 1
	return float64(sizeBytes) / (1024 * 1024 * 1024) // Convert to GB
}

// lookupNVIDIADeviceName returns a human-readable name for common NVIDIA GPUs.
func lookupNVIDIADeviceName(deviceID string) string {
	// Common consumer/workstation GPUs (can be extended)
	names := map[string]string{
		"0x2684": "GeForce RTX 4090",
		"0x2704": "GeForce RTX 4080",
		"0x2782": "GeForce RTX 4070 Ti",
		"0x2786": "GeForce RTX 4070",
		"0x28e0": "GeForce RTX 4070 Mobile",
		"0x28e1": "GeForce RTX 4070 Mobile",
		"0x2820": "GeForce RTX 4070 Laptop GPU",
		"0x2860": "GeForce RTX 4070 Max-Q",
		"0x2717": "GeForce RTX 4080 Super",
		"0x2757": "GeForce RTX 4070 Super",
		"0x2882": "GeForce RTX 4060",
		"0x2420": "GeForce RTX 3090",
		"0x2204": "GeForce RTX 3090",
		"0x2206": "GeForce RTX 3080",
		"0x2216": "GeForce RTX 3080 Mobile",
		"0x2208": "GeForce RTX 3080 Ti",
		"0x2484": "GeForce RTX 3070",
		"0x2488": "GeForce RTX 3070 Ti",
		"0x2503": "GeForce RTX 3060",
		"0x2504": "GeForce RTX 3060 Ti",
	}
	if name, ok := names[deviceID]; ok {
		return name
	}
	return "NVIDIA GPU"
}

// detectViaNvidiaSMI uses nvidia-smi to query GPU information.
// Note: nvidia-smi has ~1.8s cold-start on first invocation due to driver init.
func detectViaNvidiaSMI() (float64, string, bool) {
	cmd := exec.Command("nvidia-smi",
		"--query-gpu=memory.total,name",
		"--format=csv,noheader,nounits")

	output, err := cmd.Output()
	if err != nil {
		return 0, "", false
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return 0, "", false
	}

	// Parse first GPU (use primary GPU for capability assessment)
	parts := strings.Split(lines[0], ", ")
	if len(parts) < 2 {
		return 0, "", false
	}

	// Memory is in MiB from nvidia-smi
	memMiB, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0, "", false
	}

	name := strings.TrimSpace(parts[1])
	vramGB := memMiB / 1024.0

	return vramGB, name, true
}

// detectSystemRAM returns system RAM in GB.
func detectSystemRAM() float64 {
	switch runtime.GOOS {
	case "linux":
		return detectRAMLinux()
	case "darwin":
		return detectRAMDarwin()
	case "windows":
		return detectRAMWindows()
	default:
		// Fallback: assume 4GB minimum
		return 4.0
	}
}

// detectRAMLinux reads /proc/meminfo.
func detectRAMLinux() float64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 4.0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseFloat(fields[1], 64)
				if err == nil {
					return kb / (1024 * 1024) // KB to GB
				}
			}
			break
		}
	}

	return 4.0
}

// detectRAMDarwin uses sysctl on macOS.
func detectRAMDarwin() float64 {
	cmd := exec.Command("sysctl", "-n", "hw.memsize")
	output, err := cmd.Output()
	if err != nil {
		return 4.0
	}

	bytes, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		return 4.0
	}

	return bytes / (1024 * 1024 * 1024) // Bytes to GB
}

// detectRAMWindows uses wmic on Windows.
func detectRAMWindows() float64 {
	cmd := exec.Command("wmic", "ComputerSystem", "get", "TotalPhysicalMemory")
	output, err := cmd.Output()
	if err != nil {
		return 4.0
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == "TotalPhysicalMemory" {
			continue
		}
		bytes, err := strconv.ParseFloat(line, 64)
		if err == nil {
			return bytes / (1024 * 1024 * 1024) // Bytes to GB
		}
	}

	return 4.0
}

func (h HardwareCapabilities) SelectHighQualityTier() ModelTier {
	if h.SystemRAMGB >= 6.0 {
		return TierQwen3_4B
	}
	return TierQwen3_0_6B
}

func (h HardwareCapabilities) CanUseGPU(tier ModelTier) bool {
	if !h.HasNVIDIAGPU {
		return false
	}
	spec := tier.Spec()
	return h.VRAMGB >= spec.MinVRAMGB
}

func (h HardwareCapabilities) CanRunTier(tier ModelTier) bool {
	spec := tier.Spec()
	return h.SystemRAMGB >= spec.MinRAMGB
}
