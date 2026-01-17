package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/treesitter"
)

func TestNewDesignerSkills(t *testing.T) {
	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewDesignerSkills(tool)
	defer skills.Close()

	if skills.tool == nil {
		t.Error("tool should not be nil")
	}
}

func TestTsExtractComponents(t *testing.T) {
	skipIfNoGrammar(t, "tsx")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewDesignerSkills(tool)
	defer skills.Close()

	path := createDesignerTestFile(t, "component.tsx", `
function Button() {
	return <button>Click</button>
}

function Card() {
	return <div>Card</div>
}
`)

	ctx := context.Background()
	result, err := skills.TsExtractComponents(ctx, []string{path}, ExtractComponentsOptions{})
	if err != nil {
		t.Fatalf("TsExtractComponents: %v", err)
	}

	if result.Total != 2 {
		t.Errorf("expected 2 components, got %d", result.Total)
	}
}

func TestTsExtractComponentsNoComponents(t *testing.T) {
	skipIfNoGrammar(t, "tsx")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewDesignerSkills(tool)
	defer skills.Close()

	path := createDesignerTestFile(t, "util.tsx", `
function helper() {
	return 1
}
`)

	ctx := context.Background()
	result, err := skills.TsExtractComponents(ctx, []string{path}, ExtractComponentsOptions{})
	if err != nil {
		t.Fatalf("TsExtractComponents: %v", err)
	}

	if result.Total != 0 {
		t.Errorf("expected 0 components, got %d", result.Total)
	}
}

func TestTsAnalyzeJSX(t *testing.T) {
	skipIfNoGrammar(t, "tsx")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewDesignerSkills(tool)
	defer skills.Close()

	path := createDesignerTestFile(t, "component.tsx", `
function Component() {
	return <div><span>Text</span></div>
}
`)

	ctx := context.Background()
	result, err := skills.TsAnalyzeJSX(ctx, path, AnalyzeJSXOptions{})
	if err != nil {
		t.Fatalf("TsAnalyzeJSX: %v", err)
	}

	if len(result.Elements) == 0 {
		t.Error("expected at least 1 JSX element")
	}
}

func TestTsFindStyles(t *testing.T) {
	skipIfNoGrammar(t, "tsx")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewDesignerSkills(tool)
	defer skills.Close()

	path := createDesignerTestFile(t, "styled.tsx", "const Button = styled('button');\nconst box = css({});\n")

	ctx := context.Background()
	result, err := skills.TsFindStyles(ctx, []string{path}, FindStylesOptions{})
	if err != nil {
		t.Fatalf("TsFindStyles: %v", err)
	}

	if result.Total < 1 {
		t.Errorf("expected at least 1 style, got %d", result.Total)
	}
}

func TestTsExtractProps(t *testing.T) {
	skipIfNoGrammar(t, "tsx")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewDesignerSkills(tool)
	defer skills.Close()

	path := createDesignerTestFile(t, "component.tsx", `
interface ButtonProps {
	label: string
	onClick: () => void
}

function Button(props: ButtonProps) {
	return <button>{props.label}</button>
}
`)

	ctx := context.Background()
	result, err := skills.TsExtractProps(ctx, path, "")
	if err != nil {
		t.Fatalf("TsExtractProps: %v", err)
	}

	if len(result.Components) != 1 {
		t.Errorf("expected 1 component props, got %d", len(result.Components))
	}
}

func TestTsExtractPropsFiltered(t *testing.T) {
	skipIfNoGrammar(t, "tsx")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewDesignerSkills(tool)
	defer skills.Close()

	path := createDesignerTestFile(t, "component.tsx", `
interface ButtonProps {}
interface CardProps {}
`)

	ctx := context.Background()
	result, err := skills.TsExtractProps(ctx, path, "Button")
	if err != nil {
		t.Fatalf("TsExtractProps: %v", err)
	}

	for _, comp := range result.Components {
		if comp.ComponentName != "Button" {
			t.Errorf("expected only Button, got %q", comp.ComponentName)
		}
	}
}

func TestTsFindHooks(t *testing.T) {
	skipIfNoGrammar(t, "tsx")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewDesignerSkills(tool)
	defer skills.Close()

	path := createDesignerTestFile(t, "component.tsx", `
function Component() {
	const [state, setState] = useState(0)
	useEffect(() => {}, [])
	return <div>{state}</div>
}
`)

	ctx := context.Background()
	result, err := skills.TsFindHooks(ctx, []string{path}, FindHooksOptions{IncludeCustom: true})
	if err != nil {
		t.Fatalf("TsFindHooks: %v", err)
	}

	if result.Total < 2 {
		t.Errorf("expected at least 2 hooks, got %d", result.Total)
	}
}

func TestTsFindHooksCustomOnly(t *testing.T) {
	skipIfNoGrammar(t, "tsx")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewDesignerSkills(tool)
	defer skills.Close()

	path := createDesignerTestFile(t, "component.tsx", `
function Component() {
	const data = useCustomHook()
	const [state] = useState(0)
	return <div>{data}</div>
}
`)

	ctx := context.Background()
	result, err := skills.TsFindHooks(ctx, []string{path}, FindHooksOptions{IncludeCustom: false})
	if err != nil {
		t.Fatalf("TsFindHooks: %v", err)
	}

	for _, file := range result.Files {
		for _, hook := range file.Hooks {
			if hook.IsCustom {
				t.Errorf("found custom hook %q when IncludeCustom=false", hook.Name)
			}
		}
	}
}

func TestTsAnalyzeAccessibility(t *testing.T) {
	skipIfNoGrammar(t, "tsx")

	tool := treesitter.NewTreeSitterTool()
	defer tool.Close()

	skills := NewDesignerSkills(tool)
	defer skills.Close()

	path := createDesignerTestFile(t, "component.tsx", `
function Component() {
	return <div><img src="test.png" /></div>
}
`)

	ctx := context.Background()
	result, err := skills.TsAnalyzeAccessibility(ctx, []string{path})
	if err != nil {
		t.Fatalf("TsAnalyzeAccessibility: %v", err)
	}

	if result.Issues < 1 {
		t.Errorf("expected at least 1 accessibility issue, got %d", result.Issues)
	}
}

func TestIsComponentFunction(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"Button", true},
		{"Card", true},
		{"button", false},
		{"helper", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isComponentFunction(tt.name)
		if got != tt.expected {
			t.Errorf("isComponentFunction(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestIsStyleFunction(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"styled", true},
		{"css", true},
		{"makeStyles", true},
		{"createStyles", true},
		{"sx", true},
		{"regular", false},
	}

	for _, tt := range tests {
		got := isStyleFunction(tt.name)
		if got != tt.expected {
			t.Errorf("isStyleFunction(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestDetectStyleType(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"styled", "styled-components"},
		{"css", "css-in-js"},
		{"makeStyles", "material-ui"},
		{"createStyles", "material-ui"},
		{"sx", "emotion"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		got := detectStyleType(tt.name)
		if got != tt.expected {
			t.Errorf("detectStyleType(%q) = %q, want %q", tt.name, got, tt.expected)
		}
	}
}

func TestIsHookCall(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"useState", true},
		{"useEffect", true},
		{"useCustom", true},
		{"use", false},
		{"helper", false},
	}

	for _, tt := range tests {
		got := isHookCall(tt.name)
		if got != tt.expected {
			t.Errorf("isHookCall(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestIsCustomHook(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"useState", false},
		{"useEffect", false},
		{"useCallback", false},
		{"useCustom", true},
		{"useMyHook", true},
	}

	for _, tt := range tests {
		got := isCustomHook(tt.name)
		if got != tt.expected {
			t.Errorf("isCustomHook(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestMatchesComponent(t *testing.T) {
	tests := []struct {
		typeName      string
		componentName string
		expected      bool
	}{
		{"ButtonProps", "", true},
		{"ButtonProps", "Button", true},
		{"ButtonProps", "Card", false},
		{"CardProps", "Card", true},
	}

	for _, tt := range tests {
		got := matchesComponent(tt.typeName, tt.componentName)
		if got != tt.expected {
			t.Errorf("matchesComponent(%q, %q) = %v, want %v",
				tt.typeName, tt.componentName, got, tt.expected)
		}
	}
}

func createDesignerTestFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
