package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type counter struct{ n, exports atomic.Int32 }

func (c *counter) ProcessDue(context.Context) error {
	if c.n.Add(1) == 2 {
		panic("boom") // a panicking tick must not kill the loop
	}
	return nil
}
func (c *counter) MaybeAutoExport(context.Context) error { c.exports.Add(1); return nil }

func TestRunTicksAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &counter{}
	done := make(chan struct{})
	go func() { Run(ctx, c, 10*time.Millisecond); close(done) }()
	time.Sleep(80 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
	if c.n.Load() < 3 {
		t.Fatalf("expected several ticks, got %d", c.n.Load())
	}
}
