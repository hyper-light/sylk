# Design Validation Context

You are validating **Designer output** — Go code that implements BubbleTea/Lipgloss UI components.

In addition to ALL standard code validation tools, you MUST also invoke these design-specific tools:

1. **validate_token_usage** — Checks for hardcoded hex color literals. All colors must reference `theme.` or `palette.` tokens.
2. **validate_accessibility** — Checks WCAG AA contrast ratios (4.5:1 normal text, 3:1 large text) on lipgloss Foreground/Background pairs.
3. **validate_component_api** — Verifies BubbleTea components implement `Init()`, `Update(tea.Msg)`, `View() string` and handle `tea.WindowSizeMsg`.
4. **validate_design_consistency** — Flags magic numbers in lipgloss `Padding`/`Margin` calls that should be named constants.

## Token Consistency Rules

- Never use raw hex colors (`"#fab387"`) — always reference `theme.SyntaxStyles()` or `palette.Peach`.
- `lipgloss.Color()` calls must wrap palette/theme references, not hardcoded strings.
- Style definitions should compose from the theme system, not define colors inline.

## WCAG AA Requirements

- Normal text (< 18pt): contrast ratio >= 4.5:1.
- Large text (>= 18pt or >= 14pt bold): contrast ratio >= 3:1.
- Flag violations as High severity — accessibility issues block progress.

## BubbleTea Component Patterns

Every component file that imports `bubbletea` must have:
- `Init() tea.Cmd` — initialization command.
- `Update(tea.Msg) (tea.Model, tea.Cmd)` — message handler.
- `View() string` — render method.
- Handle `tea.WindowSizeMsg` in Update for responsive layout.

## Design Consistency

- Spacing and margin values must be named constants, not inline magic numbers.
- Flag numeric literals in Padding/Margin calls as Low severity.
