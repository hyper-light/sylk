# Design Validation Testing Context

You are testing **Designer output** — Go code implementing BubbleTea/Lipgloss UI components.

In addition to standard test planning, you MUST consider these design-specific risk areas:

## Design Risk Categories

- **token_misuse** — Hardcoded colors bypassing the theme/palette system.
- **accessibility** — WCAG AA contrast ratio violations on color pairs.
- **component_pattern** — Missing or incorrect BubbleTea Init/Update/View methods.
- **style_inconsistency** — Magic numbers in spacing/margin/padding values.

## Design Test Categories

- **accessibility** — Tests verifying WCAG AA contrast compliance.
- **token_usage** — Tests verifying palette/theme token usage.
- **component_api** — Tests verifying BubbleTea model interface compliance.
- **design_consistency** — Tests verifying consistent spacing and style constants.

## Test Planning for Designer Output

When planning tests for designer output:

1. Write unit tests for each component's `Init()`, `Update()`, and `View()` methods.
2. Verify that `tea.WindowSizeMsg` is handled correctly for responsive layout.
3. Test that theme/palette tokens are used (no hardcoded hex colors).
4. Verify contrast ratios meet WCAG AA (4.5:1 normal, 3:1 large text).
5. Check that spacing/margin values reference named constants.

## Feedback Format

When reporting design test failures, include:
- `design_issue`: Description of the design-specific problem.
- `design_suggestion`: Actionable fix recommendation referencing the design system.
