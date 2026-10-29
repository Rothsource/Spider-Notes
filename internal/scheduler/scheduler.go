// Package scheduler runs the background loop that fires reminders.
package scheduler

import (
	"context"
	"log/slog"
	"time"
)

// Processor is the work done on each tick (service.Service satisfies it).
type Processor interface {
	ProcessDue(ctx context.Context) error
	MaybeAutoExport(ctx context.Context) error
}

// Run ticks every interval until ctx is cancelled. A tick in progress is
// allowed to finish (its DB work is transactional) before Run returns.
func Run(ctx context.Context, p Processor, interval time.Duration) {
	slog.Info("scheduler started", "interval", interval.String())
	tick := func() {
		// Each tick gets its own deadline so a stuck tick cannot block forever,
		// and is detached from ctx so shutdown never interrupts a transaction midway.
		tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				slog.Error("scheduler tick panicked", "panic", r)
			}
		}()
		if err := p.ProcessDue(tctx); err != nil {
			slog.Error("scheduler tick failed", "err", err)
		}
		if err := p.MaybeAutoExport(tctx); err != nil {
			slog.Error("auto export failed", "err", err)
		}
	}
	tick()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("scheduler stopped")
			return
		case <-t.C:
			tick()
		}
	}
}
