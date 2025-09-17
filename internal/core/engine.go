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
	onAdvance func(State)
}

func New(cfg Config) *PomodoroEngine {
	return &PomodoroEngine{
		cfg:   cfg,
		clock: realClock{},
		state: State{Phase: PhaseWork},
	}
}

func (p *PomodoroEngine) SetOnAdvance(fn func(State)) {
	p.onAdvance = fn
}

// Snapshot of current state (thread-safe)
func (p *PomodoroEngine) State() State {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

func (p *PomodoroEngine) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock.Now()
	p.state.Phase = PhaseWork
	p.state.StartedAt = now
	p.state.EndsAt = now.Add(p.cfg.Work)
	p.state.Paused = false
	p.spawnLocked()
}

func (p *PomodoroEngine) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.Paused {
		return
	}
	remain := time.Until(p.state.EndsAt)
	if remain < 0 {
		remain = 0
	}
	p.stopLocked()
	// keep remaining by moving EndsAt to Now+remain after resume
	p.state.Paused = true
	p.state.StartedAt = p.clock.Now()
	p.state.EndsAt = p.state.StartedAt.Add(remain)
}

func (p *PomodoroEngine) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.state.Paused {
		return
	}
	remain := time.Until(p.state.EndsAt)
	if remain < 0 {
		remain = 0
	}
	p.state.Paused = false
	p.state.StartedAt = p.clock.Now()
	p.state.EndsAt = p.state.StartedAt.Add(remain)
	p.spawnLocked()
}

func (p *PomodoroEngine) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
	// reset to idle work phase
	p.state = State{Phase: PhaseWork}
	if p.onAdvance != nil {
		p.onAdvance(p.state)
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
		go p.onAdvance(p.state)
	}
}

// Helper: Remaining time (non-negative)
func (p *PomodoroEngine) Remaining() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	rem := time.Until(p.state.EndsAt)
	if rem < 0 {
		return 0
	}
	return rem
}
