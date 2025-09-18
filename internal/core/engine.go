// Package core implements a deadline-based Pomodoro state machine.
package core

import (
	"context"
	"sync"
	"time"
)

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

type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type Config struct {
	Work      time.Duration
	ShortBrk  time.Duration
	LongBrk   time.Duration
	LongEvery int
}

type State struct {
	Phase        Phase
	StartedAt    time.Time
	EndsAt       time.Time
	PomodoroDone int
	Paused       bool
}

type PomodoroEngine struct {
	mu     sync.RWMutex
	cfg    Config
	state  State
	clock  Clock
	cancel context.CancelFunc

	// optional subscribers (e.g., TUI refresh)
	onAdvance    func(State)
	pausedRemain time.Duration
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

func (p *PomodoroEngine) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
	// reset to idle work phase
	p.state = State{Phase: PhaseWork}
	if p.onAdvance != nil {
		go p.onAdvance(p.state) // unblocked notification
	}
}

func (p *PomodoroEngine) spawnLocked() {
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	end := p.state.EndsAt
	go func() {
		// wait until deadline with monotonic time
		select {
		case <-p.clock.After(time.Until(end)):
			p.advance()
		case <-ctx.Done():
			return
		}
	}()
}

func (p *PomodoroEngine) stopLocked() {
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}

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
		// notify subscriber (e.g., to trigger notification)
		// execute outside the lock
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
