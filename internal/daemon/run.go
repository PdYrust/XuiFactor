package daemon

import (
	"context"
	"fmt"
	"time"
)

func (r *Runner) Run(ctx context.Context) error {
	interval := r.cfg.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	fmt.Fprintf(r.out, "run: started interval=%s database=%s\n", interval, r.cfg.DatabaseLabel())
	r.runTick(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(r.out, "run: stopped")
			return nil
		case <-ticker.C:
			r.runTick(ctx)
		}
	}
}

// runTick logs a repeating error once, so a condition that persists for hours
// does not flood the journal every poll interval.
func (r *Runner) runTick(ctx context.Context) {
	result, err := r.Tick(ctx)
	if err != nil {
		if message := err.Error(); message != r.lastTickError {
			fmt.Fprintf(r.err, "tick: error: %s\n", message)
			r.lastTickError = message
		}
		return
	}
	if r.lastTickError != "" {
		fmt.Fprintln(r.out, "tick: recovered")
		r.lastTickError = ""
	}
	if result.HasWork() {
		fmt.Fprintf(r.out, "tick: %s\n", result.Summary())
	}
}
