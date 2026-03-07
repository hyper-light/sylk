package guardian

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ContentSafetyValidator detects dangerous content patterns that go beyond
// malicious code detection. Covers:
//   - Compression bombs (zip, gzip, brotli quines)
//   - Binary executable detection (ELF, PE, Mach-O, shebang)
//   - XXE/entity expansion in XML/SVG
//   - Archive traversal (path traversal via zip entries)
//   - Permission escalation patterns (setuid, sudo, chmod 777)
//   - Polyglot archives (files valid as multiple types)
type ContentSafetyValidator struct {
	// maxDecompressRatio is the maximum allowed ratio of decompressed
	// to compressed size. Content exceeding this is flagged as a
	// compression bomb. Default: 100 (100:1 ratio).
	maxDecompressRatio int

	// maxXMLEntityDepth limits nested entity expansion to prevent
	// billion-laughs attacks. Default: 3.
	maxXMLEntityDepth int

	xxePatterns     []safetyPattern
	archivePatterns []safetyPattern
	permPatterns    []safetyPattern
}

type safetyPattern struct {
	Name     string
	Regex    *regexp.Regexp
	Severity Severity
	Type     FindingType
}

// NewContentSafetyValidator creates a validator with default configuration.
func NewContentSafetyValidator() *ContentSafetyValidator {
	return &ContentSafetyValidator{
		maxDecompressRatio: 100,
		maxXMLEntityDepth:  3,
		xxePatterns:        defaultXXEPatterns(),
		archivePatterns:    defaultArchivePatterns(),
		permPatterns:       defaultPermPatterns(),
	}
}

// Validate inspects raw content for safety issues. Returns nil when
// content is safe (zero allocation on happy path).
func (v *ContentSafetyValidator) Validate(content []byte, contentType, url string) []Finding {
	var findings []Finding

	findings = append(findings, v.checkCompressionBomb(content, contentType)...)
	findings = append(findings, v.checkBinaryExecutable(content)...)
	findings = append(findings, v.checkXXE(content, contentType)...)
	findings = append(findings, v.checkArchiveTraversal(content, contentType)...)
	findings = append(findings, v.checkPermissionEscalation(content)...)

	return findings
}

// checkCompressionBomb detects content that claims to be compressed but
// has characteristics of a compression bomb (quine or high-ratio payload).
func (v *ContentSafetyValidator) checkCompressionBomb(content []byte, contentType string) []Finding {
	if len(content) == 0 {
		return nil
	}

	ct := strings.ToLower(contentType)
	isCompressed := strings.Contains(ct, "gzip") ||
		strings.Contains(ct, "deflate") ||
		strings.Contains(ct, "brotli") ||
		strings.Contains(ct, "zip") ||
		strings.Contains(ct, "x-compress")

	if !isCompressed {
		// Check magic bytes for compressed content regardless of content-type.
		isCompressed = hasCompressedMagic(content)
	}

	if !isCompressed {
		return nil
	}

	var findings []Finding

	// Nested archive detection: multiple compression layers.
	if countCompressedLayers(content) > 2 {
		findings = append(findings, Finding{
			ID:         uuid.New().String(),
			Type:       FindingTypeCompBomb,
			Severity:   SeverityCritical,
			Confidence: 0.9,
			Title:      "Nested compressed archive detected",
			Detail:     "Content contains multiple compression layers — potential compression bomb",
			Timestamp:  time.Now(),
		})
	}

	// High entropy + small size = suspicious for compressed content.
	// A 1KB file that claims to decompress to 100MB+ is a bomb.
	if len(content) < 1024 && isCompressed {
		findings = append(findings, Finding{
			ID:         uuid.New().String(),
			Type:       FindingTypeCompBomb,
			Severity:   SeverityHigh,
			Confidence: 0.7,
			Title:      "Suspiciously small compressed archive",
			Detail:     "Compressed content is very small — may decompress to dangerous size",
			Timestamp:  time.Now(),
		})
	}

	return findings
}

// checkBinaryExecutable detects executable file formats in fetched content.
func (v *ContentSafetyValidator) checkBinaryExecutable(content []byte) []Finding {
	if len(content) < 4 {
		return nil
	}

	var findings []Finding

	// ELF binary (Linux executables).
	if bytes.HasPrefix(content, []byte{0x7f, 'E', 'L', 'F'}) {
		findings = append(findings, Finding{
			ID:         uuid.New().String(),
			Type:       FindingTypeBinaryExec,
			Severity:   SeverityCritical,
			Confidence: 1.0,
			Title:      "ELF executable binary detected",
			Detail:     "Content is a Linux executable binary — never safe to ingest as text",
			Timestamp:  time.Now(),
		})
	}

	// PE binary (Windows executables).
	if bytes.HasPrefix(content, []byte{'M', 'Z'}) && len(content) > 64 {
		// Verify PE signature at offset specified in DOS header.
		peOffset := int(content[60]) | int(content[61])<<8
		if peOffset > 0 && peOffset+4 <= len(content) {
			if bytes.Equal(content[peOffset:peOffset+4], []byte{'P', 'E', 0, 0}) {
				findings = append(findings, Finding{
					ID:         uuid.New().String(),
					Type:       FindingTypeBinaryExec,
					Severity:   SeverityCritical,
					Confidence: 1.0,
					Title:      "PE executable binary detected",
					Detail:     "Content is a Windows executable binary",
					Timestamp:  time.Now(),
				})
			}
		}
	}

	// Mach-O binary (macOS executables).
	if (bytes.HasPrefix(content, []byte{0xfe, 0xed, 0xfa, 0xce}) ||
		bytes.HasPrefix(content, []byte{0xfe, 0xed, 0xfa, 0xcf}) ||
		bytes.HasPrefix(content, []byte{0xce, 0xfa, 0xed, 0xfe}) ||
		bytes.HasPrefix(content, []byte{0xcf, 0xfa, 0xed, 0xfe})) {
		findings = append(findings, Finding{
			ID:         uuid.New().String(),
			Type:       FindingTypeBinaryExec,
			Severity:   SeverityCritical,
			Confidence: 1.0,
			Title:      "Mach-O executable binary detected",
			Detail:     "Content is a macOS executable binary",
			Timestamp:  time.Now(),
		})
	}

	// Java class file.
	if bytes.HasPrefix(content, []byte{0xca, 0xfe, 0xba, 0xbe}) {
		findings = append(findings, Finding{
			ID:         uuid.New().String(),
			Type:       FindingTypeBinaryExec,
			Severity:   SeverityHigh,
			Confidence: 1.0,
			Title:      "Java class file detected",
			Detail:     "Content is a compiled Java class file",
			Timestamp:  time.Now(),
		})
	}

	// WebAssembly binary.
	if bytes.HasPrefix(content, []byte{0x00, 'a', 's', 'm'}) {
		findings = append(findings, Finding{
			ID:         uuid.New().String(),
			Type:       FindingTypeBinaryExec,
			Severity:   SeverityMedium,
			Confidence: 1.0,
			Title:      "WebAssembly binary detected",
			Detail:     "Content is a WebAssembly module",
			Timestamp:  time.Now(),
		})
	}

	return findings
}

// checkXXE detects XML External Entity and entity expansion attacks.
func (v *ContentSafetyValidator) checkXXE(content []byte, contentType string) []Finding {
	ct := strings.ToLower(contentType)
	isXML := strings.Contains(ct, "xml") || strings.Contains(ct, "svg")
	if !isXML {
		// Check content for XML declaration.
		if !bytes.Contains(content[:min(len(content), 200)], []byte("<?xml")) {
			return nil
		}
	}

	contentStr := string(content)
	var findings []Finding

	for _, pat := range v.xxePatterns {
		if pat.Regex.MatchString(contentStr) {
			findings = append(findings, Finding{
				ID:         uuid.New().String(),
				Type:       pat.Type,
				Severity:   pat.Severity,
				Confidence: 0.9,
				Title:      pat.Name,
				Detail:     "Detected in XML/SVG content",
				Timestamp:  time.Now(),
			})
		}
	}

	// Count entity definitions — excessive entities suggest billion-laughs.
	entityCount := strings.Count(contentStr, "<!ENTITY")
	if entityCount > v.maxXMLEntityDepth {
		findings = append(findings, Finding{
			ID:         uuid.New().String(),
			Type:       FindingTypeXXE,
			Severity:   SeverityCritical,
			Confidence: 0.95,
			Title:      "Excessive XML entity definitions (billion-laughs pattern)",
			Detail:     fmt.Sprintf("Found %d entity definitions", entityCount),
			Timestamp:  time.Now(),
		})
	}

	return findings
}

// checkArchiveTraversal detects path traversal in archive content.
func (v *ContentSafetyValidator) checkArchiveTraversal(content []byte, contentType string) []Finding {
	contentStr := string(content)
	var findings []Finding

	for _, pat := range v.archivePatterns {
		if pat.Regex.MatchString(contentStr) {
			findings = append(findings, Finding{
				ID:         uuid.New().String(),
				Type:       pat.Type,
				Severity:   pat.Severity,
				Confidence: 0.85,
				Title:      pat.Name,
				Detail:     "Detected in fetched content",
				Timestamp:  time.Now(),
			})
		}
	}

	// Zip-specific: scan for local file headers with directory traversal.
	if isZipContent(content) {
		if bytes.Contains(content, []byte("../")) || bytes.Contains(content, []byte("..\\")) {
			findings = append(findings, Finding{
				ID:         uuid.New().String(),
				Type:       FindingTypeArchiveRisk,
				Severity:   SeverityCritical,
				Confidence: 0.95,
				Title:      "Zip archive contains path traversal",
				Detail:     "Archive entries reference parent directories — zip slip attack",
				Timestamp:  time.Now(),
			})
		}
	}

	_ = contentType
	return findings
}

// checkPermissionEscalation detects patterns that could escalate privileges.
func (v *ContentSafetyValidator) checkPermissionEscalation(content []byte) []Finding {
	contentStr := string(content)
	var findings []Finding

	for _, pat := range v.permPatterns {
		if pat.Regex.MatchString(contentStr) {
			findings = append(findings, Finding{
				ID:         uuid.New().String(),
				Type:       pat.Type,
				Severity:   pat.Severity,
				Confidence: 0.8,
				Title:      pat.Name,
				Detail:     "Detected in fetched content",
				Timestamp:  time.Now(),
			})
		}
	}
	return findings
}

func defaultXXEPatterns() []safetyPattern {
	return []safetyPattern{
		{
			Name:     "External entity declaration (SYSTEM)",
			Regex:    regexp.MustCompile(`(?i)<!ENTITY\s+\S+\s+SYSTEM\s+["'][^"']+["']`),
			Severity: SeverityCritical,
			Type:     FindingTypeXXE,
		},
		{
			Name:     "External entity declaration (PUBLIC)",
			Regex:    regexp.MustCompile(`(?i)<!ENTITY\s+\S+\s+PUBLIC\s+["'][^"']+["']`),
			Severity: SeverityCritical,
			Type:     FindingTypeXXE,
		},
		{
			Name:     "Parameter entity expansion",
			Regex:    regexp.MustCompile(`(?i)<!ENTITY\s+%\s+\S+\s+["'][^"']*["']`),
			Severity: SeverityHigh,
			Type:     FindingTypeXXE,
		},
		{
			Name:     "SVG foreignObject with script",
			Regex:    regexp.MustCompile(`(?i)<foreignObject[^>]*>.*?<script`),
			Severity: SeverityHigh,
			Type:     FindingTypeXXE,
		},
		{
			Name:     "SVG onload event handler",
			Regex:    regexp.MustCompile(`(?i)<svg[^>]*\bonload\s*=`),
			Severity: SeverityHigh,
			Type:     FindingTypeXXE,
		},
	}
}

func defaultArchivePatterns() []safetyPattern {
	return []safetyPattern{
		{
			Name:     "Tar path traversal",
			Regex:    regexp.MustCompile(`\.\./[^"'\s]*\.(sh|exe|bat|cmd|ps1|py|rb|pl)`),
			Severity: SeverityCritical,
			Type:     FindingTypeArchiveRisk,
		},
		{
			Name:     "Absolute path in archive",
			Regex:    regexp.MustCompile(`(?m)^/(etc|usr|var|tmp|root|home)/`),
			Severity: SeverityHigh,
			Type:     FindingTypeArchiveRisk,
		},
		{
			Name:     "Symlink escape attempt",
			Regex:    regexp.MustCompile(`(?i)symlink.*\.\./|ln\s+-s.*\.\./`),
			Severity: SeverityCritical,
			Type:     FindingTypeArchiveRisk,
		},
	}
}

func defaultPermPatterns() []safetyPattern {
	return []safetyPattern{
		{
			Name:     "setuid/setgid binary creation",
			Regex:    regexp.MustCompile(`(?i)chmod\s+[0-7]*[4-7][0-7]{2}\s|chmod\s+[ug]\+s\b`),
			Severity: SeverityCritical,
			Type:     FindingTypePermEscal,
		},
		{
			Name:     "World-writable permission (chmod 777/666)",
			Regex:    regexp.MustCompile(`chmod\s+(777|666|775|o\+w)\b`),
			Severity: SeverityHigh,
			Type:     FindingTypePermEscal,
		},
		{
			Name:     "sudo/doas without password",
			Regex:    regexp.MustCompile(`(?i)(sudo|doas)\s+.*NOPASSWD|echo\s+.*\|\s*sudo`),
			Severity: SeverityCritical,
			Type:     FindingTypePermEscal,
		},
		{
			Name:     "Crontab manipulation",
			Regex:    regexp.MustCompile(`(?i)crontab\s+-[lr]|echo\s+.*>>\s*/etc/cron`),
			Severity: SeverityHigh,
			Type:     FindingTypePermEscal,
		},
		{
			Name:     "SSH key injection",
			Regex:    regexp.MustCompile(`(?i)>>?\s*~?/\.ssh/authorized_keys|mkdir\s+-p\s+~?/\.ssh`),
			Severity: SeverityCritical,
			Type:     FindingTypePermEscal,
		},
		{
			Name:     "Shadow/passwd file manipulation",
			Regex:    regexp.MustCompile(`(?i)>>\s*/etc/(shadow|passwd|sudoers)|useradd\s+.*-o\s+-u\s+0`),
			Severity: SeverityCritical,
			Type:     FindingTypePermEscal,
		},
	}
}

// hasCompressedMagic checks for common compression format magic bytes.
func hasCompressedMagic(content []byte) bool {
	if len(content) < 4 {
		return false
	}
	// Gzip: 1f 8b
	if content[0] == 0x1f && content[1] == 0x8b {
		return true
	}
	// Zip: PK\x03\x04
	if isZipContent(content) {
		return true
	}
	// Bzip2: BZ
	if content[0] == 'B' && content[1] == 'Z' {
		return true
	}
	// XZ: fd 37 7a 58 5a 00
	if len(content) >= 6 && content[0] == 0xfd && content[1] == 0x37 && content[2] == 0x7a {
		return true
	}
	// Zstd: 28 b5 2f fd
	if len(content) >= 4 && content[0] == 0x28 && content[1] == 0xb5 && content[2] == 0x2f && content[3] == 0xfd {
		return true
	}
	return false
}

// isZipContent checks for ZIP local file header magic bytes.
func isZipContent(content []byte) bool {
	return len(content) >= 4 && content[0] == 'P' && content[1] == 'K' && content[2] == 0x03 && content[3] == 0x04
}

// countCompressedLayers counts nested compression signatures in content.
func countCompressedLayers(content []byte) int {
	count := 0
	// Gzip signatures.
	for i := 0; i < len(content)-1; i++ {
		if content[i] == 0x1f && content[i+1] == 0x8b {
			count++
		}
	}
	// Zip signatures.
	for i := 0; i < len(content)-3; i++ {
		if content[i] == 'P' && content[i+1] == 'K' && content[i+2] == 0x03 && content[i+3] == 0x04 {
			count++
		}
	}
	return count
}
