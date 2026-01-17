// Package detect provides utilities for detecting dependencies in project manifest files.
package detect

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type packageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// HasDependency checks if a package.json file in the given root directory
// contains the specified package as a dependency or devDependency.
func HasDependency(root string, pkg string) (bool, error) {
	data, err := readFileIfExists(filepath.Join(root, "package.json"))
	if err != nil || data == nil {
		return false, err
	}

	var pkgJSON packageJSON
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return false, err
	}

	return hasKeyInMaps(pkg, pkgJSON.Dependencies, pkgJSON.DevDependencies), nil
}

func hasKeyInMaps(key string, maps ...map[string]string) bool {
	for _, m := range maps {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}

// HasGemDependency checks if a Gemfile in the given root directory
// contains the specified gem as a dependency.
func HasGemDependency(root string, gem string) (bool, error) {
	return scanFileForMatch(
		filepath.Join(root, "Gemfile"),
		func(line string) bool {
			return isGemDeclaration(line) && containsGemName(line, gem)
		},
	)
}

func isGemDeclaration(line string) bool {
	return strings.HasPrefix(line, "gem ") || strings.HasPrefix(line, "gem(")
}

func containsGemName(line, gem string) bool {
	return strings.Contains(line, "'"+gem+"'") || strings.Contains(line, `"`+gem+`"`)
}

// HasPythonDependency checks if a Python project in the given root directory
// contains the specified package as a dependency.
func HasPythonDependency(root string, pkg string) (bool, error) {
	found, err := checkRequirementsTxt(root, pkg)
	if err != nil || found {
		return found, err
	}
	return checkPyprojectToml(root, pkg)
}

func checkRequirementsTxt(root string, pkg string) (bool, error) {
	normalizedPkg := normalizePythonPackageName(pkg)
	return scanFileForMatch(
		filepath.Join(root, "requirements.txt"),
		func(line string) bool {
			return !strings.HasPrefix(line, "-") &&
				normalizePythonPackageName(extractPythonPackageName(line)) == normalizedPkg
		},
	)
}

func checkPyprojectToml(root string, pkg string) (bool, error) {
	data, err := readFileIfExists(filepath.Join(root, "pyproject.toml"))
	if err != nil || data == nil {
		return false, err
	}
	return searchPyprojectForPackage(string(data), normalizePythonPackageName(pkg)), nil
}

func searchPyprojectForPackage(content, normalizedPkg string) bool {
	lines := strings.Split(content, "\n")
	inDepsSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "[") {
			inDepsSection = strings.Contains(strings.ToLower(trimmed), "dependencies")
			continue
		}

		if matchesPyprojectDep(trimmed, normalizedPkg, inDepsSection) {
			return true
		}
	}
	return false
}

func matchesPyprojectDep(line, normalizedPkg string, inDepsSection bool) bool {
	if inDepsSection && strings.Contains(line, "=") {
		parts := strings.SplitN(line, "=", 2)
		if normalizePythonPackageName(strings.TrimSpace(parts[0])) == normalizedPkg {
			return true
		}
	}
	return containsPythonPackageInArray(line, normalizedPkg)
}

func normalizePythonPackageName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "-", "_")
	return strings.ReplaceAll(name, ".", "_")
}

func extractPythonPackageName(line string) string {
	if idx := strings.Index(line, "["); idx != -1 {
		line = line[:idx]
	}
	return strings.TrimSpace(line[:findFirstSpecifier(line)])
}

func findFirstSpecifier(line string) int {
	specifiers := []string{"==", ">=", "<=", "~=", "!=", "<", ">", "@", ";"}
	minIdx := len(line)
	for _, spec := range specifiers {
		if idx := strings.Index(line, spec); idx != -1 && idx < minIdx {
			minIdx = idx
		}
	}
	return minIdx
}

func containsPythonPackageInArray(line, normalizedPkg string) bool {
	if !hasQuotes(line) {
		return false
	}
	return matchPythonPackageInParts(line, normalizedPkg)
}

func hasQuotes(line string) bool {
	return strings.Contains(line, `"`) || strings.Contains(line, "'")
}

func matchPythonPackageInParts(line, normalizedPkg string) bool {
	parts := strings.FieldsFunc(line, isPythonArrayDelimiter)
	for _, part := range parts {
		if matchesPythonPart(part, normalizedPkg) {
			return true
		}
	}
	return false
}

func matchesPythonPart(part, normalizedPkg string) bool {
	part = strings.TrimSpace(part)
	return part != "" && normalizePythonPackageName(extractPythonPackageName(part)) == normalizedPkg
}

var pythonArrayDelimiters = map[rune]bool{
	'"':  true,
	'\'': true,
	',':  true,
	'[':  true,
	']':  true,
}

func isPythonArrayDelimiter(r rune) bool {
	return pythonArrayDelimiters[r]
}

// HasCargoDependency checks if a Cargo.toml file in the given root directory
// contains the specified crate as a dependency.
func HasCargoDependency(root string, crate string) (bool, error) {
	data, err := readFileIfExists(filepath.Join(root, "Cargo.toml"))
	if err != nil || data == nil {
		return false, err
	}
	return searchCargoForCrate(string(data), normalizeCrateName(crate)), nil
}

func searchCargoForCrate(content, normalizedCrate string) bool {
	lines := strings.Split(content, "\n")
	inDepsSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isCargoSkippableLine(trimmed) {
			continue
		}

		found, newInDeps := processCargoLine(trimmed, normalizedCrate, inDepsSection)
		if found {
			return true
		}
		inDepsSection = newInDeps
	}
	return false
}

func isCargoSkippableLine(line string) bool {
	return line == "" || strings.HasPrefix(line, "#")
}

func processCargoLine(line, normalizedCrate string, inDepsSection bool) (found bool, newInDeps bool) {
	if strings.HasPrefix(line, "[") {
		return processCargoSection(line, normalizedCrate)
	}
	if inDepsSection && matchesCargoDep(line, normalizedCrate) {
		return true, inDepsSection
	}
	return false, inDepsSection
}

func processCargoSection(line, normalizedCrate string) (inDeps bool, found bool) {
	section := strings.ToLower(line)
	inDeps = strings.Contains(section, "dependencies")

	if !isDetailedCargoSection(section) {
		return inDeps, false
	}

	inner := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
	parts := strings.Split(inner, ".")
	if len(parts) >= 2 && normalizeCrateName(parts[len(parts)-1]) == normalizedCrate {
		return inDeps, true
	}
	return inDeps, false
}

func isDetailedCargoSection(section string) bool {
	return strings.HasPrefix(section, "[dependencies.") ||
		strings.HasPrefix(section, "[dev-dependencies.") ||
		strings.HasPrefix(section, "[build-dependencies.")
}

func matchesCargoDep(line, normalizedCrate string) bool {
	if !strings.Contains(line, "=") {
		return false
	}
	parts := strings.SplitN(line, "=", 2)
	return normalizeCrateName(strings.TrimSpace(parts[0])) == normalizedCrate
}

func normalizeCrateName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "-", "_"))
}

// HasGoDependency checks if a go.mod file in the given root directory
// contains the specified package as a dependency.
func HasGoDependency(root string, pkg string) (bool, error) {
	return scanGoMod(filepath.Join(root, "go.mod"), pkg)
}

func scanGoMod(path, pkg string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer file.Close()

	return parseGoModRequires(bufio.NewScanner(file), pkg)
}

func parseGoModRequires(scanner *bufio.Scanner, pkg string) (bool, error) {
	inRequireBlock := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if isGoModComment(line) {
			continue
		}

		found, nowInBlock := processGoModLine(line, pkg, inRequireBlock)
		if found {
			return true, nil
		}
		inRequireBlock = nowInBlock
	}
	return false, scanner.Err()
}

func isGoModComment(line string) bool {
	return line == "" || strings.HasPrefix(line, "//")
}

func processGoModLine(line, pkg string, inBlock bool) (found bool, nowInBlock bool) {
	if isRequireBlockStart(line) {
		return false, true
	}
	if isRequireBlockEnd(line, inBlock) {
		return false, false
	}
	return checkGoModRequire(line, pkg, inBlock), inBlock
}

func isRequireBlockStart(line string) bool {
	return strings.HasPrefix(line, "require (") || line == "require ("
}

func isRequireBlockEnd(line string, inBlock bool) bool {
	return inBlock && line == ")"
}

func checkGoModRequire(line, pkg string, inBlock bool) bool {
	if isSingleRequireLine(line) {
		return checkSingleRequire(line, pkg)
	}
	if inBlock {
		return checkBlockRequire(line, pkg)
	}
	return false
}

func isSingleRequireLine(line string) bool {
	return strings.HasPrefix(line, "require ") && !strings.Contains(line, "(")
}

func checkSingleRequire(line, pkg string) bool {
	parts := strings.Fields(line)
	return len(parts) >= 2 && matchGoModule(parts[1], pkg)
}

func checkBlockRequire(line, pkg string) bool {
	parts := strings.Fields(line)
	return len(parts) >= 1 && matchGoModule(parts[0], pkg)
}

func matchGoModule(modPath, pkg string) bool {
	return modPath == pkg ||
		strings.HasPrefix(pkg, modPath+"/") ||
		strings.HasPrefix(modPath, pkg+"/")
}

// readFileIfExists reads a file and returns nil data if file doesn't exist.
func readFileIfExists(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// scanFileForMatch scans a file line by line and returns true if matcher returns true.
func scanFileForMatch(path string, matcher func(string) bool) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return handleFileOpenError(err)
	}
	defer file.Close()

	return scanLines(bufio.NewScanner(file), matcher)
}

func handleFileOpenError(err error) (bool, error) {
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func scanLines(scanner *bufio.Scanner, matcher func(string) bool) (bool, error) {
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if isSkippableLine(line) {
			continue
		}
		if matcher(line) {
			return true, nil
		}
	}
	return false, scanner.Err()
}

func isSkippableLine(line string) bool {
	return line == "" || strings.HasPrefix(line, "#")
}
