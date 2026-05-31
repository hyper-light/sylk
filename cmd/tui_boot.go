package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/boot"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/ui"
	uitheme "github.com/adalundhe/sylk/ui/theme"
)

var bootstrapProgressStageLabels = []string{
	"infrastructure",
	"system participants",
	"agent wiring",
	"services",
	"surfaces",
}

const bootstrapProgressEventsPerStage = 8

type bootstrapProgressKind uint8

const (
	bootstrapProgressStage bootstrapProgressKind = iota + 1
	bootstrapProgressClaims
	bootstrapProgressKnowledge
	bootstrapProgressError
	bootstrapProgressComplete
)

type bootstrapProgressEvent struct {
	kind      bootstrapProgressKind
	label     string
	phase     string
	current   int64
	total     int64
	health    boot.BootHealth
	err       error
	result    bootstrapRunResult
	timestamp time.Time
}

type bootstrapRunResult struct {
	deps    ui.Deps
	cleanup func() error
	err     error
}

type bootstrapProgressReporter struct {
	ctx       context.Context
	events    chan bootstrapProgressEvent
	resultMu  sync.Mutex
	result    bootstrapRunResult
	hasResult bool
	done      atomic.Bool
}

func newBootstrapProgressReporter(ctx context.Context) *bootstrapProgressReporter {
	capacity := len(bootstrapProgressStageLabels) * bootstrapProgressEventsPerStage
	return &bootstrapProgressReporter{
		ctx:    ctx,
		events: make(chan bootstrapProgressEvent, capacity),
	}
}

func (r *bootstrapProgressReporter) ReportStage(label string, current int64) {
	r.send(bootstrapProgressEvent{
		kind:    bootstrapProgressStage,
		label:   label,
		current: current,
		total:   int64(len(bootstrapProgressStageLabels)),
	})
}

func (r *bootstrapProgressReporter) ReportClaimsHealth(health boot.BootHealth) {
	r.send(bootstrapProgressEvent{kind: bootstrapProgressClaims, health: health})
}

func (r *bootstrapProgressReporter) ReportKnowledgeProgress(phase string, current, total int64) {
	r.send(bootstrapProgressEvent{
		kind:    bootstrapProgressKnowledge,
		phase:   phase,
		current: current,
		total:   total,
	})
}

func (r *bootstrapProgressReporter) ReportError(label string, err error) {
	if err == nil {
		return
	}
	r.send(bootstrapProgressEvent{
		kind:  bootstrapProgressError,
		label: label,
		err:   err,
	})
}

func (r *bootstrapProgressReporter) Complete(result bootstrapRunResult) {
	if r == nil {
		return
	}
	r.resultMu.Lock()
	r.result = result
	r.hasResult = true
	r.resultMu.Unlock()
	r.sendComplete(bootstrapProgressEvent{kind: bootstrapProgressComplete, result: result})
}

func (r *bootstrapProgressReporter) Result() (bootstrapRunResult, bool) {
	if r == nil {
		return bootstrapRunResult{}, false
	}
	r.resultMu.Lock()
	defer r.resultMu.Unlock()
	return r.result, r.hasResult
}

func (r *bootstrapProgressReporter) send(event bootstrapProgressEvent) {
	if r == nil || r.done.Load() {
		return
	}
	event.timestamp = time.Now()
	select {
	case r.events <- event:
	case <-r.ctx.Done():
	}
}

func (r *bootstrapProgressReporter) sendComplete(event bootstrapProgressEvent) {
	event.timestamp = time.Now()
	r.events <- event
	r.done.Store(true)
}

func reportBootstrapClaimsHealth(phase1 *bootstrapPhase1) {
	if phase1 == nil || phase1.progress == nil || phase1.bootOps == nil {
		return
	}
	phase1.progress.ReportClaimsHealth(phase1.bootOps.BootHealth())
}

func runBootstrapWithBootUI(
	ctx context.Context,
	mockMode bool,
	projectRoot string,
	stop context.CancelFunc,
) (ui.Deps, func() error, error) {
	reporter := newBootstrapProgressReporter(ctx)
	scope := concurrency.NewGoroutineScope(ctx, "tui-bootstrap", nil)
	scope.SetMaxLifetime(bootstrapWorkerMaxLifetime())

	if err := scope.Go("bootstrap-deps", 0, func(context.Context) error {
		defer completeBootstrapAfterPanic(reporter)
		deps, cleanup, err := bootstrapDepsWithProgress(ctx, mockMode, projectRoot, reporter)
		reporter.Complete(bootstrapRunResult{deps: deps, cleanup: cleanup, err: err})
		return nil
	}); err != nil {
		return ui.Deps{}, nil, err
	}

	model := newBootstrapBootModel(ctx, reporter.events, stop)
	program := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		if stop != nil {
			stop()
		}
		scope.SignalShutdown()
		_ = scope.Shutdown(shutdownGrace, shutdownHard)
		return ui.Deps{}, nil, err
	}

	scope.SignalShutdown()
	if err := scope.Shutdown(shutdownGrace, shutdownHard); err != nil && !isBootstrapScopeDrainError(err) {
		return ui.Deps{}, nil, err
	}

	result, ok := reporter.Result()
	if !ok {
		return ui.Deps{}, nil, fmt.Errorf("bootstrap did not produce a result")
	}
	return result.deps, result.cleanup, result.err
}

func completeBootstrapAfterPanic(reporter *bootstrapProgressReporter) {
	if recovered := recover(); recovered != nil {
		reporter.Complete(bootstrapRunResult{err: fmt.Errorf("bootstrap panic: %v", recovered)})
		panic(recovered)
	}
}

func bootstrapWorkerMaxLifetime() time.Duration {
	return time.Hour
}

func isBootstrapScopeDrainError(err error) bool {
	var leakErr *concurrency.GoroutineLeakError
	return errors.As(err, &leakErr)
}

type bootstrapBootModel struct {
	ctx         context.Context
	events      <-chan bootstrapProgressEvent
	cancel      context.CancelFunc
	width       int
	height      int
	theme       *uitheme.Theme
	titleGrad   *uitheme.Gradient
	borderGrad  *uitheme.Gradient
	spinnerGrad *uitheme.Gradient
	frame       int
	stageLabel  string
	stageCur    int64
	stageTotal  int64
	knowledge   string
	health      boot.BootHealth
	err         error
	startedAt   time.Time
	completedAt time.Time
}

func newBootstrapBootModel(ctx context.Context, events <-chan bootstrapProgressEvent, cancel context.CancelFunc) bootstrapBootModel {
	th := bootstrapTheme()
	return bootstrapBootModel{
		ctx:         ctx,
		events:      events,
		cancel:      cancel,
		width:       bootstrapDefaultWidth,
		height:      bootstrapDefaultHeight,
		theme:       th,
		titleGrad:   th.Palette.GroupGradient(),
		borderGrad:  th.Palette.FocusRingGradient(),
		spinnerGrad: th.Palette.ThinkingGradient(),
		stageLabel:  "starting",
		stageTotal:  int64(len(bootstrapProgressStageLabels)),
		startedAt:   time.Now(),
	}
}

const (
	bootstrapDefaultWidth         = 80
	bootstrapDefaultHeight        = 24
	bootstrapMinViewportHeight    = 12
	bootstrapViewportMargin       = 4
	bootstrapPanelMinWidth        = 44
	bootstrapPanelMaxWidth        = 88
	bootstrapPanelPaddingX        = 2
	bootstrapPanelPaddingY        = 1
	bootstrapPanelFrameWidth      = 2
	bootstrapMinBarWidth          = 12
	bootstrapMaxBarWidth          = 48
	bootstrapAnimationFramePeriod = 100 * time.Millisecond
)

type bootstrapProgressMsg struct {
	event bootstrapProgressEvent
}

type bootstrapTickMsg time.Time

func (m bootstrapBootModel) Init() tea.Cmd {
	return tea.Batch(waitBootstrapProgress(m.events), waitBootstrapTick())
}

func (m bootstrapBootModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = max(msg.Width, bootstrapMinBarWidth)
		m.height = max(msg.Height, bootstrapMinViewportHeight)
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case bootstrapProgressMsg:
		m.applyEvent(msg.event)
		return m.nextCommand(msg.event)
	case bootstrapTickMsg:
		m.frame++
		if !m.completedAt.IsZero() {
			return m, nil
		}
		return m, waitBootstrapTick()
	default:
		return m, nil
	}
}

func (m bootstrapBootModel) View() string {
	width := max(m.width, bootstrapMinBarWidth)
	height := max(m.height, bootstrapMinViewportHeight)
	bg := m.theme.Palette.Background
	panelBg := m.theme.Palette.PopupBg
	panelWidth := bootstrapPanelWidth(width)
	frameWidth := bootstrapFrameInnerWidth(panelWidth)
	contentWidth := bootstrapContentWidth(panelWidth)
	barWidth := bootstrapBarWidth(contentWidth)
	done, total := m.progressCounts()
	elapsed := m.elapsed()
	rows := []string{
		m.titleLine(contentWidth, elapsed),
		m.statusLine(done, total, contentWidth, elapsed),
		m.progressBar(done, total, barWidth, elapsed),
		"",
	}
	rows = append(rows, m.phaseRows(contentWidth)...)
	rows = append(rows, m.footerRows(contentWidth)...)
	content := lipgloss.NewStyle().
		Width(contentWidth).
		Background(panelBg).
		Padding(bootstrapPanelPaddingY, bootstrapPanelPaddingX).
		Render(strings.Join(rows, "\n"))
	panel := uitheme.RenderGradientBorder(content, m.borderGrad, elapsed, frameWidth, lipgloss.Height(content), 0)
	placed := lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel, lipgloss.WithWhitespaceBackground(bg))
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Background(bg).
		Render(placed)
}

func (m bootstrapBootModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc", "q":
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	default:
		return m, nil
	}
}

func (m *bootstrapBootModel) applyEvent(event bootstrapProgressEvent) {
	switch event.kind {
	case bootstrapProgressStage:
		m.stageLabel = event.label
		m.stageCur = event.current
		m.stageTotal = event.total
	case bootstrapProgressClaims:
		m.health = event.health
	case bootstrapProgressKnowledge:
		m.knowledge = knowledgeProgressLabel(event)
	case bootstrapProgressError:
		m.err = event.err
		m.stageLabel = event.label
	case bootstrapProgressComplete:
		m.completedAt = event.timestamp
		m.err = event.result.err
		if event.result.err == nil {
			m.stageCur = int64(len(bootstrapProgressStageLabels))
			m.stageTotal = int64(len(bootstrapProgressStageLabels))
			m.stageLabel = "complete"
		}
	}
}

func (m bootstrapBootModel) nextCommand(event bootstrapProgressEvent) (tea.Model, tea.Cmd) {
	if event.kind == bootstrapProgressComplete {
		return m, tea.Quit
	}
	return m, waitBootstrapProgress(m.events)
}

func (m bootstrapBootModel) progressCounts() (int64, int64) {
	done, total := bootHealthProgress(m.health)
	if total != 0 {
		return done, total
	}
	return m.stageCur, max(m.stageTotal, int64(len(bootstrapProgressStageLabels)))
}

func (m bootstrapBootModel) stageLine(done, total int64) string {
	elapsed := m.elapsed().Round(time.Millisecond)
	if m.completedAt.IsZero() {
		return fmt.Sprintf("%s  %d/%d  %s", m.stageLabel, done, total, elapsed)
	}
	return fmt.Sprintf("%s  %d/%d  %s", m.stageLabel, done, total, m.completedAt.Sub(m.startedAt).Round(time.Millisecond))
}

func (m bootstrapBootModel) phaseRows(width int) []string {
	if len(m.health.Phases) == 0 {
		return []string{m.dimStyle().Render(fitBootstrapText("claims boot projection pending", width))}
	}
	rows := make([]string, 0, len(m.health.Phases))
	for _, phase := range m.health.Phases {
		rows = append(rows, m.phaseRow(phase, width))
	}
	return rows
}

func (m bootstrapBootModel) footerRows(width int) []string {
	rows := []string{""}
	if m.knowledge != "" {
		rows = append(rows, m.mutedStyle().Render(fitBootstrapText(m.knowledge, width)))
	}
	if m.err != nil {
		rows = append(rows, m.errorStyle().Render(fitBootstrapText(m.err.Error(), width)))
	}
	if m.ctx.Err() != nil && m.err == nil {
		rows = append(rows, m.errorStyle().Render(fitBootstrapText(m.ctx.Err().Error(), width)))
	}
	return rows
}

func (m bootstrapBootModel) titleLine(width int, elapsed time.Duration) string {
	spinner := uitheme.RenderGradientGlyph(m.spinner(), m.spinnerGrad, elapsed)
	title := uitheme.RenderRippleText("Sylk boot", elapsed, m.titleGrad, 0)
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, spinner+" "+title)
}

func (m bootstrapBootModel) statusLine(done, total int64, width int, elapsed time.Duration) string {
	color := m.titleGrad.Sample(elapsed)
	return lipgloss.NewStyle().
		Foreground(color).
		Render(fitBootstrapText(m.stageLine(done, total), width))
}

func (m bootstrapBootModel) phaseRow(phase boot.BootPhaseHealth, width int) string {
	label := strings.ReplaceAll(string(phase.Phase), "_", " ")
	status := bootstrapOutcomeLabel(phase)
	labelWidth := bootstrapPhaseLabelWidth(m.health)
	text := fmt.Sprintf("%-*s %s", labelWidth, label, status)
	style := m.phaseStatusStyle(status)
	return style.Render(fitBootstrapText(text, width))
}

func (m bootstrapBootModel) progressBar(done, total int64, width int, elapsed time.Duration) string {
	width = max(width, bootstrapMinBarWidth)
	if total <= 0 {
		return m.dimStyle().Render(strings.Repeat("─", width))
	}
	filled := min(width, int((done*int64(width))/total))
	active := lipgloss.NewStyle().Foreground(m.titleGrad.Sample(elapsed)).Render(strings.Repeat("━", filled))
	empty := m.dimStyle().Render(strings.Repeat("─", width-filled))
	return lipgloss.PlaceHorizontal(bootstrapContentWidth(bootstrapPanelWidth(m.width)), lipgloss.Center, active+empty)
}

func (m bootstrapBootModel) spinner() string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[m.frame%len(frames)]
}

func (m bootstrapBootModel) elapsed() time.Duration {
	if !m.completedAt.IsZero() {
		return m.completedAt.Sub(m.startedAt)
	}
	return time.Since(m.startedAt)
}

func waitBootstrapProgress(events <-chan bootstrapProgressEvent) tea.Cmd {
	return func() tea.Msg {
		event := <-events
		return bootstrapProgressMsg{event: event}
	}
}

func waitBootstrapTick() tea.Cmd {
	return tea.Tick(bootstrapAnimationFramePeriod, func(t time.Time) tea.Msg {
		return bootstrapTickMsg(t)
	})
}

func bootHealthProgress(health boot.BootHealth) (int64, int64) {
	if len(health.Phases) == 0 {
		return 0, 0
	}
	var done int64
	for _, phase := range health.Phases {
		if phase.Outcome == string(claims.ClaimLifecycleSatisfied) {
			done++
		}
	}
	return done, int64(len(health.Phases))
}

func bootstrapOutcomeLabel(phase boot.BootPhaseHealth) string {
	switch claims.ClaimLifecycleStatus(phase.Outcome) {
	case claims.ClaimLifecycleSatisfied:
		return "done"
	case claims.ClaimLifecycleValidationFailed, claims.ClaimLifecycleValidationErrored, claims.ClaimLifecycleValidationIncomplete:
		return "failed"
	default:
		return bootstrapPendingOutcomeLabel(phase.Outcome)
	}
}

func bootstrapPendingOutcomeLabel(outcome string) string {
	switch strings.TrimSpace(outcome) {
	case "", "missing", "pending":
		return "pending"
	default:
		return outcome
	}
}

func knowledgeProgressLabel(event bootstrapProgressEvent) string {
	phase := strings.TrimSpace(event.phase)
	if phase == "" {
		phase = "knowledge"
	}
	if event.total > 0 {
		return fmt.Sprintf("knowledge %s %d/%d", phase, event.current, event.total)
	}
	return "knowledge " + phase
}

func bootstrapBarWidth(width int) int {
	derived := width - bootstrapViewportMargin
	if derived < bootstrapMinBarWidth {
		return bootstrapMinBarWidth
	}
	if derived > bootstrapMaxBarWidth {
		return bootstrapMaxBarWidth
	}
	return derived
}

func bootstrapTheme() *uitheme.Theme {
	if parseThemeMode(tuiTheme) == ui.ThemeLight {
		return uitheme.DefaultLight()
	}
	return uitheme.DefaultDark()
}

func bootstrapPanelWidth(width int) int {
	available := max(bootstrapMinBarWidth, width-bootstrapViewportMargin)
	target := (width * 2) / 3
	return min(max(target, bootstrapPanelMinWidth), min(available, bootstrapPanelMaxWidth))
}

func bootstrapContentWidth(panelWidth int) int {
	return max(bootstrapMinBarWidth, bootstrapFrameInnerWidth(panelWidth)-(bootstrapPanelPaddingX*2))
}

func bootstrapFrameInnerWidth(panelWidth int) int {
	return max(bootstrapMinBarWidth, panelWidth-bootstrapPanelFrameWidth)
}

func bootstrapPhaseLabelWidth(health boot.BootHealth) int {
	width := 0
	for _, phase := range health.Phases {
		width = max(width, utf8.RuneCountInString(strings.ReplaceAll(string(phase.Phase), "_", " ")))
	}
	return max(width, len("system participants"))
}

func fitBootstrapText(text string, width int) string {
	if width <= 0 || utf8.RuneCountInString(text) <= width {
		return text
	}
	runes := []rune(text)
	if width == 1 {
		return string(runes[:1])
	}
	return string(runes[:width-1]) + "."
}

func (m bootstrapBootModel) phaseStatusStyle(status string) lipgloss.Style {
	switch status {
	case "done":
		return lipgloss.NewStyle().Foreground(m.theme.Palette.Success)
	case "failed":
		return lipgloss.NewStyle().Foreground(m.theme.Palette.Error)
	default:
		return m.mutedStyle()
	}
}

func (m bootstrapBootModel) mutedStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.theme.Palette.Subtext)
}

func (m bootstrapBootModel) dimStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
}

func (m bootstrapBootModel) errorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.theme.Palette.Error)
}
