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

// detectNVIDIAGPU attempts to detect NVIDIA GPU using nvidia-smi.
// Returns VRAM in GB, GPU name, and success flag.
func detectNVIDIAGPU() (float64, string, bool) {
	// Try nvidia-smi first (most reliable)
	if vram, name, ok := detectViaNvidiaSMI(); ok {
		return vram, name, true
	}

	// Fallback: check sysfs for NVIDIA devices
	if hasNVIDIADevice() {
		// Can't determine VRAM without nvidia-smi, assume minimal
		return 0, "NVIDIA GPU (unknown model)", true
	}

	return 0, "", false
}

// detectViaNvidiaSMI uses nvidia-smi to query GPU information.
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

// hasNVIDIADevice checks sysfs for NVIDIA PCI devices.
func hasNVIDIADevice() bool {
	if runtime.GOOS != "linux" {
		return false
	}

	// NVIDIA vendor ID is 10de
	pciDevices := "/sys/bus/pci/devices"
	entries, err := os.ReadDir(pciDevices)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		vendorPath := filepath.Join(pciDevices, entry.Name(), "vendor")
		data, err := os.ReadFile(vendorPath)
		if err != nil {
			continue
		}

		vendor := strings.TrimSpace(string(data))
		// NVIDIA vendor ID
		if vendor == "0x10de" {
			// Check device class is display controller (03xx)
			classPath := filepath.Join(pciDevices, entry.Name(), "class")
			classData, err := os.ReadFile(classPath)
			if err != nil {
				continue
			}
			class := strings.TrimSpace(string(classData))
			// Display controller class starts with 0x03
			if strings.HasPrefix(class, "0x03") {
				return true
			}
		}
	}

	return false
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

// SelectModelTier determines the appropriate model tier based on hardware.
func (h HardwareCapabilities) SelectModelTier() ModelTier {
	// Tier 1: GPU with ≥2GB VRAM
	if h.HasNVIDIAGPU && h.VRAMGB >= 2.0 {
		return TierQwen3
	}

	// Tier 2: GPU with <2GB VRAM or CPU with ≥2GB RAM
	if h.HasNVIDIAGPU || h.SystemRAMGB >= 2.0 {
		return TierGTELarge
	}

	// Tier 3: Limited resources
	return TierHybridLocal
}
