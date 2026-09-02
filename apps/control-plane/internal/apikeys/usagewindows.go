package apikeys

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/sakibsadmanshajib/hive/packages/ratewindows"
)

// UsageWindow is one window as a customer may see it.
//
// There is no absolute figure on this struct, and that is the point. The
// allowance is a credit score, credits convert to dollars by a constant the
// console publishes, and the internal credit value of a subscription is
// confidential (D-068). A percentage and a reset time carry everything the
// customer needs and disclose none of it (D-070).
type UsageWindow struct {
	Window string `json:"window"`
	// Configured false means no limit is set on this window, so it is
	// unlimited. Reported explicitly rather than as a zero limit, which a
	// display would render as a full bar for a limit that does not exist.
	Configured    bool   `json:"configured"`
	UsedPercent   int    `json:"used_percent"`
	ResetsAt      string `json:"resets_at,omitempty"`
	WindowSeconds int    `json:"window_seconds"`
	// Anchored reports how the window resets: an anchored window restores in
	// full at ResetsAt, a sliding one drains continuously. The copy beside the
	// bar has to match the behaviour, so the behaviour is on the wire.
	Anchored bool `json:"anchored"`
}

// UsageWindows is the account's consumption across both windows.
type UsageWindows struct {
	AccountID uuid.UUID     `json:"account_id"`
	Windows   []UsageWindow `json:"windows"`
	ReadAt    string        `json:"read_at"`
}

// ErrUsageWindowsUnavailable reports that the counters could not be read. It is
// surfaced rather than swallowed: a consumption display that silently shows
// zero when its backing store is unreachable tells the customer they have used
// nothing, which is worse than telling them the figure is unavailable.
var ErrUsageWindowsUnavailable = errors.New("apikeys: usage window counters unavailable")

// UsageWindowReader reads back the counters edge-api's limiter writes.
//
// The key shapes come from packages/ratewindows, shared with the limiter,
// because a private second copy of a key format is how a display quietly starts
// reporting a window nothing is counting.
type UsageWindowReader struct {
	client *redis.Client
	now    func() time.Time
}

// NewUsageWindowReader returns a reader over the same Redis the edge limiter
// writes to. A nil client yields a nil reader, and the handler answers
// "unavailable" rather than inventing zeroes.
func NewUsageWindowReader(client *redis.Client) *UsageWindowReader {
	if client == nil {
		return nil
	}
	return &UsageWindowReader{client: client, now: time.Now}
}

// Read returns both windows for one account.
func (r *UsageWindowReader) Read(ctx context.Context, accountID uuid.UUID, limits AccountRateLimits) (UsageWindows, error) {
	if r == nil || r.client == nil {
		return UsageWindows{}, ErrUsageWindowsUnavailable
	}
	now := r.now().UTC()
	anchor := limits.WeeklyAnchorAt
	if anchor.IsZero() {
		anchor = time.Unix(0, 0).UTC()
	}

	out := UsageWindows{AccountID: accountID, ReadAt: now.Format(time.RFC3339)}
	for _, w := range []struct {
		shape ratewindows.Shape
		limit *int64
	}{
		{ratewindows.SessionShape(), limits.SessionLimit},
		{ratewindows.WeeklyShape(), limits.WeeklyLimit},
	} {
		window := UsageWindow{
			Window:        w.shape.Name,
			WindowSeconds: int(time.Duration(w.shape.Buckets) * w.shape.BucketSize / time.Second),
			Anchored:      w.shape.Anchored,
		}
		if w.limit == nil || *w.limit <= 0 {
			out.Windows = append(out.Windows, window)
			continue
		}

		used, err := r.sum(ctx, ratewindows.AccountPrefix(accountID.String()), w.shape, anchor, now)
		if err != nil {
			return UsageWindows{}, err
		}
		window.Configured = true
		window.UsedPercent = percentOf(used, *w.limit)
		window.ResetsAt = ratewindows.ResetAt(now, w.shape.EffectiveAnchor(anchor), w.shape.BucketSize, w.shape.Buckets).UTC().Format(time.RFC3339)
		out.Windows = append(out.Windows, window)
	}
	return out, nil
}

func (r *UsageWindowReader) sum(ctx context.Context, scopePrefix string, shape ratewindows.Shape, anchor, now time.Time) (int64, error) {
	keys := shape.BucketKeys(scopePrefix, now, anchor)
	values, err := r.client.MGet(ctx, keys...).Result()
	if err != nil {
		return 0, errors.Join(ErrUsageWindowsUnavailable, err)
	}
	var total int64
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		parsed, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			continue
		}
		total += parsed
	}
	return total, nil
}

// percentOf clamps to 0..100. A window past its limit reads as 100, never 143:
// the bar is a proportion of an allowance, and there is no such thing as more
// than all of it.
func percentOf(used, limit int64) int {
	if limit <= 0 || used <= 0 {
		return 0
	}
	pct := used * 100 / limit
	if pct > 100 {
		return 100
	}
	return int(pct)
}
