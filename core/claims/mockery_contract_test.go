package claims

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestClaimsInfrastructureMockeryConfigNamesAllCrossPackageInterfaces(t *testing.T) {
	root := claimsRepoRoot(t)
	cfg := readMockeryConfig(t, filepath.Join(root, ".mockery.yaml"))
	for _, spec := range claimsInfrastructureMockerySpecs() {
		pkg, ok := cfg.Packages[spec.Package]
		if !ok {
			t.Fatalf(".mockery.yaml missing package %s", spec.Package)
		}
		if pkg.Config.Dir != spec.MockDir || pkg.Config.Outpkg != "mocks" {
			t.Fatalf("%s package config = %#v, want dir=%s outpkg=mocks", spec.Package, pkg.Config, spec.MockDir)
		}
		iface, ok := pkg.Interfaces[spec.Interface]
		if !ok {
			t.Fatalf(".mockery.yaml missing interface %s", spec.Interface)
		}
		if iface.Config.Mockname != spec.MockName {
			t.Fatalf("%s mockname = %q, want %q", spec.Interface, iface.Config.Mockname, spec.MockName)
		}
	}
}

func TestClaimsInfrastructureGeneratedMocksMatchSourceInterfaces(t *testing.T) {
	root := claimsRepoRoot(t)
	for _, spec := range claimsInfrastructureMockerySpecs() {
		sourceMethods := interfaceMethodNames(t, filepath.Join(root, spec.SourceDir), spec.Interface)
		mockFile := generatedMockFileForTypePath(t, filepath.Join(root, spec.MockDir), spec.MockName)
		mockMethods := mockMethodNames(t, mockFile, spec.MockName)
		assertGeneratedMockHeader(t, mockFile)
		if missing := missingMockMethods(sourceMethods, mockMethods); len(missing) != 0 {
			t.Fatalf("%s missing methods %s required by %s", mockFile, strings.Join(missing, ","), spec.Interface)
		}
	}
}

func TestClaimsInfrastructureMockeryDriftHelperNamesMissingMethod(t *testing.T) {
	missing := missingMockMethods([]string{"PublishDelta", "SubscribeDelta"}, map[string]bool{"PublishDelta": true})
	if len(missing) != 1 || missing[0] != "SubscribeDelta" {
		t.Fatalf("missing methods = %#v, want SubscribeDelta", missing)
	}
}

func TestClaimsInfrastructureGoGenerateHintsMatchMockeryConfig(t *testing.T) {
	root := claimsRepoRoot(t)
	for _, spec := range claimsInfrastructureMockerySpecs() {
		line, ok := mockeryGoGenerateLine(t, filepath.Join(root, spec.SourceDir), spec.Interface)
		if !ok {
			continue
		}
		for _, part := range []string{"mockery", "--name=" + spec.Interface, "--output=./mocks", "--outpkg=mocks"} {
			if !strings.Contains(line, part) {
				t.Fatalf("%s go:generate line %q missing %q", spec.Interface, line, part)
			}
		}
	}
}

type claimsMockerySpec struct {
	Package   string
	SourceDir string
	MockDir   string
	Interface string
	MockName  string
}

type mockeryConfig struct {
	Packages map[string]mockeryPackage `yaml:"packages"`
}

type mockeryPackage struct {
	Config     mockeryPackageConfig              `yaml:"config"`
	Interfaces map[string]mockeryInterfaceConfig `yaml:"interfaces"`
}

type mockeryPackageConfig struct {
	Dir    string `yaml:"dir"`
	Outpkg string `yaml:"outpkg"`
}

type mockeryInterfaceConfig struct {
	Config mockeryMockConfig `yaml:"config"`
}

type mockeryMockConfig struct {
	Mockname string `yaml:"mockname"`
}

func claimsInfrastructureMockerySpecs() []claimsMockerySpec {
	const claimsPackage = "github.com/adalundhe/sylk/core/claims"
	const bridgePackage = "github.com/adalundhe/sylk/ui/bridge"
	specs := []claimsMockerySpec{
		{claimsPackage, "core/claims", "core/claims/mocks", "ValidationDispatcher", "ValidationDispatcher"},
		{claimsPackage, "core/claims", "core/claims/mocks", "ValidatorHandler", "ValidatorHandler"},
		{claimsPackage, "core/claims", "core/claims/mocks", "AgentTurnEvaluator", "AgentTurnEvaluator"},
		{claimsPackage, "core/claims", "core/claims/mocks", "ArtifactLifecycleBusSink", "ArtifactLifecycleBusSink"},
		{claimsPackage, "core/claims", "core/claims/mocks", "ValidationLifecycleBusSink", "ValidationLifecycleBusSink"},
		{claimsPackage, "core/claims", "core/claims/mocks", "ClaimsClock", "ClaimsClock"},
		{claimsPackage, "core/claims", "core/claims/mocks", "TestamentGenerator", "TestamentGenerator"},
		{claimsPackage, "core/claims", "core/claims/mocks", "DeltaPublisher", "DeltaPublisher"},
		{claimsPackage, "core/claims", "core/claims/mocks", "DeltaSubscriber", "DeltaSubscriber"},
		{claimsPackage, "core/claims", "core/claims/mocks", "DeltaSubscription", "DeltaSubscription"},
		{claimsPackage, "core/claims", "core/claims/mocks", "DeltaBus", "DeltaBus"},
		{claimsPackage, "core/claims", "core/claims/mocks", "ClaimsProjector", "ClaimsProjector"},
		{claimsPackage, "core/claims", "core/claims/mocks", "AgentRefResolver", "AgentRefResolver"},
		{claimsPackage, "core/claims", "core/claims/mocks", "ClaimPostPolicy", "ClaimPostPolicy"},
		{claimsPackage, "core/claims", "core/claims/mocks", "ScopeProvider", "ScopeProvider"},
		{claimsPackage, "core/claims", "core/claims/mocks", "ServiceHandler", "ServiceHandler"},
		{claimsPackage, "core/claims", "core/claims/mocks", "DAGProcessorBackend", "DAGProcessorBackend"},
		{claimsPackage, "core/claims", "core/claims/mocks", "VFSProvisionerBackend", "VFSProvisionerBackend"},
		{claimsPackage, "core/claims", "core/claims/mocks", "ToolRuntimeBackend", "ToolRuntimeBackend"},
		{claimsPackage, "core/claims", "core/claims/mocks", "KnowledgeBackend", "KnowledgeBackend"},
		{claimsPackage, "core/claims", "core/claims/mocks", "MemoryContinuityBackend", "MemoryContinuityBackend"},
		{claimsPackage, "core/claims", "core/claims/mocks", "DocumentBackend", "DocumentBackend"},
		{claimsPackage, "core/claims", "core/claims/mocks", "GuardianBackend", "GuardianBackend"},
		{claimsPackage, "core/claims", "core/claims/mocks", "ProviderGatewayBackend", "ProviderGatewayBackend"},
		{claimsPackage, "core/claims", "core/claims/mocks", "ExternalAdapterBackend", "ExternalAdapterBackend"},
		{bridgePackage, "ui/bridge", "ui/bridge/mocks", "ClaimPresentationSink", "ClaimPresentationSink"},
		{bridgePackage, "ui/bridge", "ui/bridge/mocks", "ClaimArtifactSink", "ClaimArtifactSink"},
	}
	return specs
}

func readMockeryConfig(t *testing.T, path string) mockeryConfig {
	t.Helper()
	var cfg mockeryConfig
	if err := yaml.Unmarshal([]byte(readClaimsFile(t, path)), &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return cfg
}

func interfaceMethodNames(t *testing.T, dir, name string) []string {
	t.Helper()
	iface := findInterfaceType(t, dir, name)
	methods := make([]string, 0, len(iface.Methods.List))
	for _, field := range iface.Methods.List {
		for _, fieldName := range field.Names {
			methods = append(methods, fieldName.Name)
		}
	}
	return methods
}

func missingMockMethods(sourceMethods []string, mockMethods map[string]bool) []string {
	missing := make([]string, 0)
	for _, method := range sourceMethods {
		if !mockMethods[method] {
			missing = append(missing, method)
		}
	}
	return missing
}

func findInterfaceType(t *testing.T, dir, name string) *ast.InterfaceType {
	t.Helper()
	for _, file := range parseGoFilesInDir(t, dir) {
		for _, decl := range file.Decls {
			if iface := interfaceTypeFromDecl(decl, name); iface != nil {
				return iface
			}
		}
	}
	t.Fatalf("interface %s not found in %s", name, dir)
	return nil
}

func interfaceTypeFromDecl(decl ast.Decl, name string) *ast.InterfaceType {
	gen, ok := decl.(*ast.GenDecl)
	if !ok {
		return nil
	}
	for _, spec := range gen.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != name {
			continue
		}
		iface, _ := typeSpec.Type.(*ast.InterfaceType)
		return iface
	}
	return nil
}

func mockMethodNames(t *testing.T, encodedFile, mockName string) map[string]bool {
	t.Helper()
	path := mockFilePath(encodedFile)
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := make(map[string]bool)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && receiverTypeName(fn.Recv) == mockName {
			out[fn.Name.Name] = true
		}
	}
	return out
}

func generatedMockFileForTypePath(t *testing.T, dir, mockName string) string {
	t.Helper()
	for _, entry := range readGoDirEntries(t, dir) {
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if fileDeclaresType(file, mockName) {
			return path
		}
	}
	t.Fatalf("generated mock type %s not found in %s", mockName, dir)
	return ""
}

func fileDeclaresType(file *ast.File, name string) bool {
	for _, decl := range file.Decls {
		if typeSpecFromDecl(decl, name) != nil {
			return true
		}
	}
	return false
}

func typeSpecFromDecl(decl ast.Decl, name string) *ast.TypeSpec {
	gen, ok := decl.(*ast.GenDecl)
	if !ok {
		return nil
	}
	for _, spec := range gen.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if ok && typeSpec.Name.Name == name {
			return typeSpec
		}
	}
	return nil
}

func receiverTypeName(fields *ast.FieldList) string {
	if fields == nil || len(fields.List) == 0 {
		return ""
	}
	return exprTypeName(fields.List[0].Type)
}

func exprTypeName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return exprTypeName(typed.X)
	default:
		return ""
	}
}

func assertGeneratedMockHeader(t *testing.T, encodedFile string) {
	t.Helper()
	path := mockFilePath(encodedFile)
	body := readClaimsFile(t, path)
	if !strings.HasPrefix(body, "// Code generated by mockery. DO NOT EDIT.") {
		t.Fatalf("%s is missing mockery generated header", path)
	}
}

func mockFilePath(encodedFile string) string {
	parts := strings.SplitN(encodedFile, ":", 2)
	if len(parts) == 0 {
		return encodedFile
	}
	return parts[0]
}

func parseGoFilesInDir(t *testing.T, dir string) []*ast.File {
	t.Helper()
	files := make([]*ast.File, 0)
	for _, entry := range readGoDirEntries(t, dir) {
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		files = append(files, file)
	}
	return files
}

func readGoDirEntries(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	out := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if includeGoSourceEntry(entry) {
			out = append(out, entry)
		}
	}
	return out
}

func includeGoSourceEntry(entry os.DirEntry) bool {
	name := entry.Name()
	return !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

func mockeryGoGenerateLine(t *testing.T, dir, interfaceName string) (string, bool) {
	t.Helper()
	needle := "mockery --name=" + interfaceName
	for _, entry := range readGoDirEntries(t, dir) {
		for _, line := range strings.Split(readClaimsFile(t, filepath.Join(dir, entry.Name())), "\n") {
			if strings.Contains(line, needle) {
				return line, true
			}
		}
	}
	return "", false
}

func claimsRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readClaimsFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
