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

	fmt.Fprintf(r.out, "run: started interval=%s database=%s\n", interval, r.cfg.DatabasePath)
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

func (r *Runner) runTick(ctx context.Context) {
	result, err := r.Tick(ctx)
	if err != nil {
		fmt.Fprintf(r.err, "tick: error: %v\n", err)
		return
	}
	if result.HasWork() {
		fmt.Fprintf(r.out, "tick: %s\n", result.Summary())
	}
}
