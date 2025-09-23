package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
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
	quit     bool
}

func NewModel(engine *core.PomodoroEngine, notifier notify.Notifier) (*Model, error) {
	m := &Model{
		engine:   engine,
		notifier: notifier,
		progress: progress.New(progress.WithDefaultGradient()),
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
	_, err := p.Run()
	return err
}

func (m *Model) Init() tea.Cmd {
	return tickCmd()
}

type tickMsg time.Time

// tickCmd returns a command that sends a tickMsg after one second.
// It uses tea.Tick (not time.Ticker), which schedules a one-time event
// without leaving behind a running goroutine. Each tick must be
// explicitly rescheduled in the update loop, giving precise control
// over timing and throttling.
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
					// Idle -> Start
					m.engine.Start()
				} else if st.Paused {
					// Paused -> Resume
					m.engine.Resume()
				}
			}
		case "p":
			m.engine.Pause()
		case "r":
			// Reset/Stop to idle
			m.engine.Stop()
		}

	case tickMsg:
		// Schedule the next tick
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

	phaseLabel := st.Phase.String()
	if st.StartedAt.IsZero() {
		phaseLabel = "IDLE"
	}
	phase := lipgloss.NewStyle().Bold(true).Render(phaseLabel)

	info := fmt.Sprintf("Remaining: %s\nCompleted: %d\nPaused: %v\n",
		remain, st.PomodoroDone, st.Paused)

	// progress bar based on phase duration
	total := m.engine.PhaseDuration(st.Phase)
	if phaseLabel == "IDLE" {
		// The idle phase is always represented by a new State object.
		// We can detect the idle phase by checking whether State.StartedAt is zero.
		total = 0
	}
	var ratio float64
	if total > 0 {
		done := min(max(total-m.engine.Remaining(), 0), total)
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
