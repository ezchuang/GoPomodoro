// Package core implements a deadline-based Pomodoro state machine.
// It manages work/break cycles and provides thread-safe state access.
package core

import (
	"context"
	"sync"
	"time"
)

// Phase defines the type of a Pomodoro phase.
type Phase int

const (
	PhaseWork Phase = iota
	PhaseShortBreak
	PhaseLongBreak
)

func (p Phase) String() string {
	switch p {
	case PhaseWork:
		return "WORK"
	case PhaseShortBreak:
		return "SHORT_BREAK"
	case PhaseLongBreak:
		return "LONG_BREAK"
	default:
		return "UNKNOWN"
	}
}

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// Clock abstracts time functions for testability.
// A fake clock can be injected to avoid nondeterministic tests.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) NewTimer(d time.Duration) Timer {
	d = max(d, 0)
	return &realTimer{t: time.NewTimer(d)}
}

type realTimer struct{ t *time.Timer }

func (rt *realTimer) C() <-chan time.Time { return rt.t.C }
func (rt *realTimer) Stop() bool          { return rt.t.Stop() }

// Config specifies Pomodoro timings and recurrence rules.
type Config struct {
	Work      time.Duration
	ShortBrk  time.Duration
	LongBrk   time.Duration
	LongEvery int // long break after N work sessions
}

// State represents the current snapshot of the engine.
type State struct {
	Phase        Phase
	StartedAt    time.Time
	EndsAt       time.Time
	PomodoroDone int
	Paused       bool
}

// PomodoroEngine manages the lifecycle of Pomodoro phases.
// It is safe for concurrent access.
type PomodoroEngine struct {
	mu           sync.RWMutex
	cfg          Config
	state        State
	clock        Clock
	cancel       context.CancelFunc
	pausedRemain time.Duration

	// optional subscribers (e.g., TUI refresh)
	// Invoked on every phase change
	onAdvance func(State)
}

// New creates a PomodoroEngine with the given config.
func New(cfg Config) *PomodoroEngine {
	return &PomodoroEngine{
		cfg:   cfg,
		clock: realClock{},
		state: State{Phase: PhaseWork},
	}
}

// SetOnAdvance sets a callback invoked whenever the phase changes.
// The callback receives a snapshot State.
func (p *PomodoroEngine) SetOnAdvance(fn func(State)) {
	p.onAdvance = fn
}

// Snapshot of current state (thread-safe)
func (p *PomodoroEngine) State() State {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

// optional subscriber invoked on every phase change.
// For idle state (StartedAt zero), it returns 0.
func (p *PomodoroEngine) PhaseDuration(ph Phase) time.Duration {
	switch ph {
	case PhaseWork:
		return p.cfg.Work
	case PhaseShortBreak:
		return p.cfg.ShortBrk
	case PhaseLongBreak:
		return p.cfg.LongBrk
	default:
		return 0
	}
}

func (p *PomodoroEngine) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock.Now()
	p.state.Phase = PhaseWork
	p.state.StartedAt = now
	p.state.EndsAt = now.Add(p.cfg.Work)
	p.state.Paused = false
	p.pausedRemain = 0
	p.spawnLocked()
}

// Pause freezes the current phase, recording remaining time.
func (p *PomodoroEngine) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.Paused {
		return
	}
	// Freeze remain into pausedRemain
	rem := max(time.Until(p.state.EndsAt), 0)
	p.pausedRemain = rem
	p.state.Paused = true
	p.stopLocked()
}

func (p *PomodoroEngine) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.state.Paused {
		return
	}
	now := p.clock.Now()
	p.state.StartedAt = now
	p.pausedRemain = max(p.pausedRemain, 0)
	p.state.EndsAt = now.Add(p.pausedRemain)
	p.state.Paused = false
	p.pausedRemain = 0
	p.spawnLocked()
}

// Stop cancels the current phase and resets to idle work state.
// A snapshot notification is sent asynchronously if onAdvance is set.
func (p *PomodoroEngine) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
	// reset to idle work phase
	p.state = State{Phase: PhaseWork}
	if p.onAdvance != nil {
		go p.onAdvance(p.state)
	}
}

// spawnLocked schedules a goroutine that waits until the current
// phase deadline, then triggers advance(). Cancelable via stopLocked().
func (p *PomodoroEngine) spawnLocked() {
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	d := max(time.Until(p.state.EndsAt), 0)
	t := p.clock.NewTimer(d)

	go func() {
		// always stop & (if fired) drain the timer to free resources
		defer func() {
			if !t.Stop() {
				select {
				case <-t.C():
				default:
				}
			}
		}()

		// wait until deadline with monotonic time
		select {
		case <-t.C():
			p.advance()
		case <-ctx.Done():
			return
		}
	}()
}

// stopLocked cancels the current deadline goroutine if any.
func (p *PomodoroEngine) stopLocked() {
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}

// advance transitions the engine to the next phase based on rules.
// It spawns a new deadline watcher and notifies subscribers.
func (p *PomodoroEngine) advance() {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch p.state.Phase {
	case PhaseWork:
		p.state.PomodoroDone++
		p.state.StartedAt = p.clock.Now()
		if p.state.PomodoroDone%p.cfg.LongEvery == 0 {
			p.state.Phase = PhaseLongBreak
			p.state.EndsAt = p.state.StartedAt.Add(p.cfg.LongBrk)
		} else {
			p.state.Phase = PhaseShortBreak
			p.state.EndsAt = p.state.StartedAt.Add(p.cfg.ShortBrk)
		}
		p.spawnLocked()
	case PhaseShortBreak, PhaseLongBreak:
		p.state.Phase = PhaseWork
		p.state.StartedAt = p.clock.Now()
		p.state.EndsAt = p.state.StartedAt.Add(p.cfg.Work)
		p.spawnLocked()
	}

	if p.onAdvance != nil {
		// notify subscriber (e.g., UI refresh or system notification)
		// execute outside the lock to prevent blocking
		go p.onAdvance(p.state)
	}
}

// Helper: Remaining time (non-negative)
func (p *PomodoroEngine) Remaining() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.state.Paused {
		return max(p.pausedRemain, 0)
	}
	rem := time.Until(p.state.EndsAt)
	return max(rem, 0)
}
