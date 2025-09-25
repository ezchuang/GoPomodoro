package core

import (
	"sync"
	"testing"
	"time"
)

/*********** fakes for deterministic testing ***********/

type fakeTimer struct {
	ch      chan time.Time
	stopped bool
}

func newFakeTimer() *fakeTimer {
	return &fakeTimer{ch: make(chan time.Time, 1)}
}

func (ft *fakeTimer) C() <-chan time.Time { return ft.ch }
func (ft *fakeTimer) Stop() bool {
	ft.stopped = true
	return true
}

// fire pushes a single event if not stopped.
func (ft *fakeTimer) fire(now time.Time) {
	if !ft.stopped {
		select {
		case ft.ch <- now:
		default:
		}
	}
}

type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	last  *fakeTimer
	dlist []*fakeTimer
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d) // advance logical time deterministically
	ft := newFakeTimer()
	f.last = ft
	f.dlist = append(f.dlist, ft)
	return ft
}

// helper: fire the most recently created timer
func (f *fakeClock) fireLast() {
	f.mu.Lock()
	last := f.last
	now := f.now
	f.mu.Unlock()
	if last != nil {
		last.fire(now)
	}
}

/*********** tests ***********/

func newTestEngine(cfg Config) (*PomodoroEngine, *fakeClock) {
	eng := New(cfg)
	fc := &fakeClock{now: time.Unix(0, 0)}
	eng.clock = fc
	return eng, fc
}

// waitAdvance is used instead of directly polling eng.State().
// Reason:
//   - Direct polling requires time.Sleep and is racy (you may read old state).
//   - With waitAdvance we subscribe to onAdvance and block until the engine
//     notifies us. This makes tests deterministic and event-driven.
func waitAdvance(t *testing.T, set func(func(State))) chan State {
	t.Helper()
	ch := make(chan State, 1)
	set(func(s State) {
		ch <- s
	})
	return ch
}

func TestStart_AdvanceToShortBreak(t *testing.T) {
	cfg := Config{
		Work:      1 * time.Second,
		ShortBrk:  2 * time.Second,
		LongBrk:   3 * time.Second,
		LongEvery: 4,
	}
	eng, fc := newTestEngine(cfg)

	ch := waitAdvance(t, eng.SetOnAdvance)
	eng.Start()

	// fire work timer -> should advance to ShortBreak
	fc.fireLast()

	select {
	case st := <-ch:
		if st.Phase != PhaseShortBreak {
			t.Fatalf("expected SHORT_BREAK, got %v", st.Phase)
		}
		if st.PomodoroDone != 1 {
			t.Fatalf("expected PomodoroDone=1, got %d", st.PomodoroDone)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for phase advance")
	}
}

func TestPauseResume_FreezesRemaining(t *testing.T) {
	cfg := Config{
		Work:      10 * time.Second,
		ShortBrk:  1 * time.Second,
		LongBrk:   1 * time.Second,
		LongEvery: 4,
	}
	eng, _ := newTestEngine(cfg)
	eng.Start()

	// simulate some time has "passed" by moving EndsAt earlier via Pause:
	eng.Pause()
	rem1 := eng.Remaining()
	time.Sleep(50 * time.Millisecond)
	rem2 := eng.Remaining()

	if rem1 != rem2 {
		t.Fatalf("remaining changed while paused: %v -> %v", rem1, rem2)
	}

	eng.Resume()
	rem3 := eng.Remaining()

	if rem3 > rem1 || rem1-rem3 > time.Millisecond {
		t.Fatalf("resume did not restore remaining correctly: paused=%v resumed=%v", rem1, rem3)
	}
}

func TestStop_CancelsRunner_NoAdvanceAfterStop(t *testing.T) {
	cfg := Config{
		Work:      1 * time.Second,
		ShortBrk:  1 * time.Second,
		LongBrk:   1 * time.Second,
		LongEvery: 4,
	}
	eng, fc := newTestEngine(cfg)
	eng.Start()

	gotAdvance := make(chan struct{}, 1)
	eng.SetOnAdvance(func(State) { gotAdvance <- struct{}{} })

	// stop should cancel current runner
	eng.Stop()

	// even if timer fires later, we should see no advance callback
	fc.fireLast()

	select {
	case <-gotAdvance:
		t.Fatal("advance should NOT be called after Stop()")
	case <-time.After(100 * time.Millisecond):
		// ok
	}
}

func TestLongEvery_TriggersLongBreak(t *testing.T) {
	cfg := Config{
		Work:      1 * time.Second,
		ShortBrk:  1 * time.Second,
		LongBrk:   1 * time.Second,
		LongEvery: 2, // every 2 work sessions -> LongBreak
	}
	eng, fc := newTestEngine(cfg)
	ch := waitAdvance(t, eng.SetOnAdvance)

	eng.Start()

	// 1) Work -> ShortBreak
	fc.fireLast()
	<-ch

	// 2) ShortBreak -> Work
	fc.fireLast()
	<-ch

	// 3) Work -> LongBreak  (PomodoroDone==2)
	fc.fireLast()
	st := <-ch
	if st.Phase != PhaseLongBreak {
		t.Fatalf("expected LONG_BREAK, got %v", st.Phase)
	}
	if st.PomodoroDone != 2 {
		t.Fatalf("expected PomodoroDone=2, got %d", st.PomodoroDone)
	}
}
