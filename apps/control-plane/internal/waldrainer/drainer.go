package waldrainer

import (
	"context"
	"log/slog"
	"time"
)

type WAL interface {
	Drain(ctx context.Context) (int, error)
}

func Run(ctx context.Context, wal WAL, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			n, err := wal.Drain(ctx)
			if err != nil {
				slog.Warn("waldrainer error", "err", err)
				continue
			}
			if n > 0 {
				slog.Info("waldrainer drained", "count", n)
			}
		}
	}
}
