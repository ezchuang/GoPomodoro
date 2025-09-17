package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/ezchuang/GoPomodoro/internal/core"
	"github.com/ezchuang/GoPomodoro/internal/notify"
	"github.com/ezchuang/GoPomodoro/internal/ui"
)

func main() {
	work := flag.Duration("work", 25*time.Minute, "work duration")
	short := flag.Duration("short", 5*time.Minute, "short break duration")
	long := flag.Duration("long", 15*time.Minute, "long break duration")
	longEvery := flag.Int("long-every", 4, "take a long break every N pomodoros")
	flag.Parse()

	cfg := core.Config{
		Work:      *work,
		ShortBrk:  *short,
		LongBrk:   *long,
		LongEvery: *longEvery,
	}

	engine := core.New(cfg)
	notifier := notify.New()

	m, err := ui.NewModel(engine, notifier)
	if err != nil {
		log.Fatal(err)
	}
	if err := ui.Run(m); err != nil {
		fmt.Println("error:", err)
	}
}
