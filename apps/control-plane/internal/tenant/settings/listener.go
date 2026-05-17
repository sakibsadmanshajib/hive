package settings

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// StartListener subscribes to the tenant_settings_changed Postgres NOTIFY
// channel and invalidates cache entries on receipt. Blocks until ctx is
// cancelled. Callers run it in a goroutine.
func (r *Resolver) StartListener(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := r.pool.Acquire(ctx)
		if err != nil {
			slog.Warn("tenant_settings listener: acquire failed", "err", err)
			time.Sleep(time.Second)
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN tenant_settings_changed"); err != nil {
			conn.Release()
			slog.Warn("tenant_settings listener: LISTEN failed", "err", err)
			time.Sleep(time.Second)
			continue
		}
		for {
			n, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				conn.Release()
				if ctx.Err() != nil {
					return
				}
				slog.Warn("tenant_settings listener: wait failed", "err", err)
				break
			}
			r.handle(n)
		}
	}
}

func (r *Resolver) handle(n *pgconn.Notification) {
	var payload struct {
		TenantID uuid.UUID `json:"tenant_id"`
		Key      Key       `json:"key"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil {
		slog.Warn("tenant_settings listener: bad payload", "err", err, "payload", n.Payload)
		return
	}
	r.Invalidate(payload.TenantID, payload.Key)
}
