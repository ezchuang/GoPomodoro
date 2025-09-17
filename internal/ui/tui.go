package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ezchuang/GoPomodoro/internal/core"
	"github.com/ezchuang/GoPomodoro/internal/notify"
)

type Model struct {
	engine   *core.PomodoroEngine
	notifier notify.Notifier

	width  int
	height int

	progress progress.Model
	ticker   *time.Ticker
	quit     bool
}

func NewModel(engine *core.PomodoroEngine, notifier notify.Notifier) (*Model, error) {
	m := &Model{
		engine:   engine,
		notifier: notifier,
		progress: progress.New(progress.WithDefaultGradient()),
		ticker:   time.NewTicker(time.Second),
	}
	// subscribe to phase changes to send notifications
	engine.SetOnAdvance(func(st core.State) {
		title := "GoPomodoro"
		body := fmt.Sprintf("Phase: %s", st.Phase.String())
		_ = notifier.Notify(title, body)
	})
	return m, nil
}

func Run(m *Model) error {
	p := tea.NewProgram(m, tea.WithAltScreen())
	return p.Start()
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), tea.EnterAltScreen)
}

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.quit = true
			return m, tea.Quit
		case "s":
			st := m.engine.State()
			if st.Paused || st.StartedAt.IsZero() {
				if st.StartedAt.IsZero() {
					m.engine.Start()
				} else {
					m.engine.Resume()
				}
			} else {
				// already running; no-op
			}
		case "p":
			m.engine.Pause()
		case "r":
			m.engine.Stop()
		}
	case tickMsg:
		return m, tickCmd()

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	}
	return m, nil
}

func (m *Model) View() string {
	st := m.engine.State()
	remain := m.engine.Remaining().Truncate(time.Second)

	title := lipgloss.NewStyle().Bold(true).Underline(true).Render("GoPomodoro")
	phase := lipgloss.NewStyle().Bold(true).Render(st.Phase.String())

	info := fmt.Sprintf("Remaining: %s\nCompleted: %d\nPaused: %v\n",
		remain, st.PomodoroDone, st.Paused)

	// progress bar based on phase duration
	var total time.Duration
	switch st.Phase {
	case core.PhaseWork:
		total = m.engine.State().EndsAt.Sub(m.engine.State().StartedAt)
	case core.PhaseShortBreak:
		total = m.engine.State().EndsAt.Sub(m.engine.State().StartedAt)
	case core.PhaseLongBreak:
		total = m.engine.State().EndsAt.Sub(m.engine.State().StartedAt)
	default:
		total = time.Second
	}
	var ratio float64
	if total > 0 {
		done := total - m.engine.Remaining()
		if done < 0 {
			done = 0
		}
		if done > total {
			done = total
		}
		ratio = float64(done) / float64(total)
	}

	bar := m.progress.ViewAs(ratio)

	help := lipgloss.NewStyle().Faint(true).Render("[s] start/resume  [p] pause  [r] reset  [q] quit")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(max(32, m.width-4)).
		Render(fmt.Sprintf("%s\n\nPhase: %s\n%s\n%s\n\n%s", title, phase, info, bar, help))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
