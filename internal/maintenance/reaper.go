// Package maintenance runs periodic background cleanup tasks, such as deleting
// expired idempotency keys and published outbox messages, so tables that grow
// over time stay bounded.
package maintenance

import (
	"context"
	"log/slog"
	"time"
)

// Task is a named cleanup function returning how many rows it removed.
type Task struct {
	Name string
	Run  func(context.Context) (int64, error)
}

// Reaper runs its tasks on a fixed interval until the context is cancelled.
type Reaper struct {
	logger   *slog.Logger
	interval time.Duration
	tasks    []Task
}

func NewReaper(logger *slog.Logger, interval time.Duration, tasks ...Task) *Reaper {
	return &Reaper{logger: logger, interval: interval, tasks: tasks}
}

// Run blocks until ctx is cancelled, running every task each interval.
func (r *Reaper) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for _, task := range r.tasks {
				removed, err := task.Run(ctx)
				switch {
				case err != nil:
					r.logger.ErrorContext(ctx, "reaper task failed",
						slog.String("task", task.Name), slog.Any("error", err))
				case removed > 0:
					r.logger.DebugContext(ctx, "reaper removed rows",
						slog.String("task", task.Name), slog.Int64("count", removed))
				}
			}
		}
	}
}
