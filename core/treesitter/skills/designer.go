package skills

import (
	"context"
	"os"
	"strings"

	"github.com/adalundhe/sylk/core/treesitter"
)

type DesignerSkills struct {
	tool *treesitter.TreeSitterTool
}

func NewDesignerSkills(tool *treesitter.TreeSitterTool) *DesignerSkills {
	return &DesignerSkills{tool: tool}
}

type ComponentsResult struct {
	Files []FileComponents `json:"files"`
	Total int              `json:"total"`
}

type FileComponents struct {
	FilePath   string          `json:"file_path"`
	Components []ComponentInfo `json:"components"`
}

type ComponentInfo struct {
	Name      string   `json:"name"`
	StartLine uint32   `json:"start_line"`
	EndLine   uint32   `json:"end_line"`
	Props     []string `json:"props,omitempty"`
	IsDefault bool     `json:"is_default"`
}

type ExtractComponentsOptions struct {
	Framework string
}

func (d *DesignerSkills) TsExtractComponents(ctx context.Context, files []string, opts ExtractComponentsOptions) (*ComponentsResult, error) {
	result := &ComponentsResult{
		Files: make([]FileComponents, 0, len(files)),
	}

	for _, filePath := range files {
		fc, err := d.extractFileComponents(ctx, filePath, opts)
		if err != nil {
			continue
		}
		if len(fc.Components) > 0 {
			result.Files = append(result.Files, fc)
			result.Total += len(fc.Components)
		}
	}

	return result, nil
}

func (d *DesignerSkills) extractFileComponents(ctx context.Context, filePath string, opts ExtractComponentsOptions) (FileComponents, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return FileComponents{}, err
	}

	parseResult, err := d.tool.Parse(ctx, filePath, content)
	if err != nil {
		return FileComponents{}, err
	}

	fc := FileComponents{
		FilePath:   filePath,
		Components: make([]ComponentInfo, 0),
	}

	for _, f := range parseResult.Functions {
		if isComponentFunction(f.Name) {
			fc.Components = append(fc.Components, ComponentInfo{
				Name:      f.Name,
				StartLine: f.StartLine,
				EndLine:   f.EndLine,
			})
		}
	}

	return fc, nil
}

func isComponentFunction(name string) bool {
	if len(name) == 0 {
		return false
	}
	first := name[0]
	return first >= 'A' && first <= 'Z'
}

type JSXAnalysisResult struct {
	FilePath  string       `json:"file_path"`
	Elements  []JSXElement `json:"elements"`
	Hierarchy []string     `json:"hierarchy"`
}

type JSXElement struct {
	TagName   string            `json:"tag_name"`
	StartLine uint32            `json:"start_line"`
	Props     map[string]string `json:"props,omitempty"`
	Children  int               `json:"children_count"`
}

type AnalyzeJSXOptions struct {
	IncludeProps bool
}

func (d *DesignerSkills) TsAnalyzeJSX(ctx context.Context, filePath string, opts AnalyzeJSXOptions) (*JSXAnalysisResult, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	matches, err := d.tool.Query(ctx, filePath, content, `(jsx_element) @element`)
	if err != nil {
		return nil, err
	}

	result := &JSXAnalysisResult{
		FilePath:  filePath,
		Elements:  make([]JSXElement, 0),
		Hierarchy: make([]string, 0),
	}

	for _, m := range matches {
		for _, c := range m.Captures {
			result.Elements = append(result.Elements, JSXElement{
				TagName:   "element",
				StartLine: c.Node.StartLine,
			})
		}
	}

	return result, nil
}

type StylesResult struct {
	Files []FileStyles `json:"files"`
	Total int          `json:"total"`
}

type FileStyles struct {
	FilePath string      `json:"file_path"`
	Styles   []StyleInfo `json:"styles"`
}

type StyleInfo struct {
	Type      string `json:"type"`
	Name      string `json:"name,omitempty"`
	StartLine uint32 `json:"start_line"`
	Content   string `json:"content,omitempty"`
}

type FindStylesOptions struct {
	StyleType string
}

func (d *DesignerSkills) TsFindStyles(ctx context.Context, files []string, opts FindStylesOptions) (*StylesResult, error) {
	result := &StylesResult{
		Files: make([]FileStyles, 0, len(files)),
	}

	for _, filePath := range files {
		fs, err := d.findFileStyles(ctx, filePath, opts)
		if err != nil {
			continue
		}
		if len(fs.Styles) > 0 {
			result.Files = append(result.Files, fs)
			result.Total += len(fs.Styles)
		}
	}

	return result, nil
}

func (d *DesignerSkills) findFileStyles(ctx context.Context, filePath string, opts FindStylesOptions) (FileStyles, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return FileStyles{}, err
	}

	fs := FileStyles{
		FilePath: filePath,
		Styles:   make([]StyleInfo, 0),
	}

	matches, err := d.tool.Query(ctx, filePath, content, `(call_expression function: (identifier) @func)`)
	if err != nil {
		return fs, nil
	}

	for _, m := range matches {
		for _, c := range m.Captures {
			if isStyleFunction(c.Content) {
				fs.Styles = append(fs.Styles, StyleInfo{
					Type:      detectStyleType(c.Content),
					Name:      c.Content,
					StartLine: c.Node.StartLine,
				})
			}
		}
	}

	return fs, nil
}

func isStyleFunction(name string) bool {
	styleFuncs := []string{"styled", "css", "createStyles", "makeStyles", "sx"}
	for _, sf := range styleFuncs {
		if name == sf {
			return true
		}
	}
	return false
}

func detectStyleType(name string) string {
	switch name {
	case "styled":
		return "styled-components"
	case "css":
		return "css-in-js"
	case "createStyles", "makeStyles":
		return "material-ui"
	case "sx":
		return "emotion"
	default:
		return "unknown"
	}
}

type PropsResult struct {
	FilePath   string     `json:"file_path"`
	Components []PropInfo `json:"components"`
}

type PropInfo struct {
	ComponentName string    `json:"component_name"`
	Props         []PropDef `json:"props"`
}

type PropDef struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Required bool   `json:"required"`
	Default  string `json:"default,omitempty"`
}

func (d *DesignerSkills) TsExtractProps(ctx context.Context, filePath string, componentName string) (*PropsResult, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	parseResult, err := d.tool.Parse(ctx, filePath, content)
	if err != nil {
		return nil, err
	}

	result := &PropsResult{
		FilePath:   filePath,
		Components: make([]PropInfo, 0),
	}

	for _, t := range parseResult.Types {
		if strings.HasSuffix(t.Name, "Props") {
			if componentName != "" && !strings.Contains(t.Name, componentName) {
				continue
			}
			result.Components = append(result.Components, PropInfo{
				ComponentName: strings.TrimSuffix(t.Name, "Props"),
				Props:         make([]PropDef, 0),
			})
		}
	}

	return result, nil
}

type HooksResult struct {
	Files []FileHooks `json:"files"`
	Total int         `json:"total"`
}

type FileHooks struct {
	FilePath string     `json:"file_path"`
	Hooks    []HookInfo `json:"hooks"`
}

type HookInfo struct {
	Name      string `json:"name"`
	StartLine uint32 `json:"start_line"`
	IsCustom  bool   `json:"is_custom"`
}

type FindHooksOptions struct {
	IncludeCustom bool
}

func (d *DesignerSkills) TsFindHooks(ctx context.Context, files []string, opts FindHooksOptions) (*HooksResult, error) {
	result := &HooksResult{
		Files: make([]FileHooks, 0, len(files)),
	}

	for _, filePath := range files {
		fh, err := d.findFileHooks(ctx, filePath, opts)
		if err != nil {
			continue
		}
		if len(fh.Hooks) > 0 {
			result.Files = append(result.Files, fh)
			result.Total += len(fh.Hooks)
		}
	}

	return result, nil
}

func (d *DesignerSkills) findFileHooks(ctx context.Context, filePath string, opts FindHooksOptions) (FileHooks, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return FileHooks{}, err
	}

	matches, err := d.tool.Query(ctx, filePath, content, `(call_expression function: (identifier) @func)`)
	if err != nil {
		return FileHooks{}, err
	}

	fh := FileHooks{
		FilePath: filePath,
		Hooks:    make([]HookInfo, 0),
	}

	for _, m := range matches {
		collectHooksFromCaptures(m.Captures, opts.IncludeCustom, &fh.Hooks)
	}

	return fh, nil
}

func collectHooksFromCaptures(captures []treesitter.ToolCapture, includeCustom bool, hooks *[]HookInfo) {
	for _, c := range captures {
		if !isHookCall(c.Content) {
			continue
		}
		isCustom := isCustomHook(c.Content)
		if isCustom && !includeCustom {
			continue
		}
		*hooks = append(*hooks, HookInfo{
			Name:      c.Content,
			StartLine: c.Node.StartLine,
			IsCustom:  isCustom,
		})
	}
}

func isHookCall(name string) bool {
	return strings.HasPrefix(name, "use") && len(name) > 3
}

func isCustomHook(name string) bool {
	standardHooks := []string{
		"useState", "useEffect", "useContext", "useReducer",
		"useCallback", "useMemo", "useRef", "useLayoutEffect",
		"useImperativeHandle", "useDebugValue",
	}
	for _, h := range standardHooks {
		if name == h {
			return false
		}
	}
	return true
}

type AccessibilityResult struct {
	Files  []FileAccessibility `json:"files"`
	Issues int                 `json:"total_issues"`
}

type FileAccessibility struct {
	FilePath  string               `json:"file_path"`
	Issues    []AccessibilityIssue `json:"issues"`
	AriaUsage []AriaUsage          `json:"aria_usage"`
}

type AccessibilityIssue struct {
	Type      string `json:"type"`
	StartLine uint32 `json:"start_line"`
	Message   string `json:"message"`
}

type AriaUsage struct {
	Attribute string `json:"attribute"`
	StartLine uint32 `json:"start_line"`
}

func (d *DesignerSkills) TsAnalyzeAccessibility(ctx context.Context, files []string) (*AccessibilityResult, error) {
	result := &AccessibilityResult{
		Files: make([]FileAccessibility, 0, len(files)),
	}

	for _, filePath := range files {
		fa, err := d.analyzeFileAccessibility(ctx, filePath)
		if err != nil {
			continue
		}
		result.Files = append(result.Files, fa)
		result.Issues += len(fa.Issues)
	}

	return result, nil
}

func (d *DesignerSkills) analyzeFileAccessibility(ctx context.Context, filePath string) (FileAccessibility, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return FileAccessibility{}, err
	}

	fa := FileAccessibility{
		FilePath:  filePath,
		Issues:    make([]AccessibilityIssue, 0),
		AriaUsage: make([]AriaUsage, 0),
	}

	imgMatches, _ := d.tool.Query(ctx, filePath, content, `(jsx_element (jsx_opening_element name: (identifier) @tag))`)
	for _, m := range imgMatches {
		for _, c := range m.Captures {
			if c.Content == "img" {
				fa.Issues = append(fa.Issues, AccessibilityIssue{
					Type:      "missing_alt",
					StartLine: c.Node.StartLine,
					Message:   "img element may need alt attribute",
				})
			}
		}
	}

	return fa, nil
}

func (d *DesignerSkills) Close() {
}
