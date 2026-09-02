// Package ratewindows holds the Redis key shapes and bucket arithmetic for the
// two long usage windows Hive enforces on an account: a sliding five hour
// session window and a weekly window anchored per account.
//
// It exists for the same reason packages/budgetkeys does. edge-api writes these
// counters on the hot path and control-plane reads them back to show a customer
// how much of the window is gone; both live under an internal/ tree rooted at
// their own app, so neither can import the other, and two private copies of a
// key format is how a display silently starts reporting a window nothing is
// counting.
package ratewindows

import (
	"fmt"
	"time"
)

// Window names, used as the family segment of a Redis key, as the reason-code
// prefix on a refusal, and as the field name on every customer-facing surface.
const (
	Session = "session"
	Weekly  = "weekly"
)

// Session window shape: five hours of five minute buckets, summed on every
// check, so the window slides continuously rather than emptying on a boundary.
const (
	SessionBucketSize  = 5 * time.Minute
	SessionBucketCount = 60
)

// Weekly window shape: ONE bucket, seven days wide, indexed from the account's
// own anchor.
//
// One bucket rather than seven daily ones is the whole difference between a
// rolling week and the anchored week the owner ruled for (D-069, issue #1684).
// With a single bucket the counter key itself changes when the anchor's week
// rolls over, so the allowance is fully restored at the anchor instead of
// leaking back a day at a time.
const (
	WeeklyBucketSize  = 7 * 24 * time.Hour
	WeeklyBucketCount = 1
)

// AccountPrefix and KeyPrefix return the hash-tagged Redis key prefix for one
// scope. The braces keep every bucket of one scope on one Redis Cluster slot,
// which the Lua scripts require because they read sibling buckets by name.
func AccountPrefix(accountID string) string { return fmt.Sprintf("rlwin:{acct:%s}", accountID) }

// KeyPrefix is AccountPrefix for a single API key.
func KeyPrefix(keyID string) string { return fmt.Sprintf("rlwin:{key:%s}", keyID) }

// FamilyPrefix appends the window name to a scope prefix.
func FamilyPrefix(scopePrefix, window string) string {
	return fmt.Sprintf("%s:%s", scopePrefix, window)
}

// BucketKey is the Redis key holding one bucket's accumulated score.
func BucketKey(familyPrefix string, bucket int64) string {
	return fmt.Sprintf("%s:%d", familyPrefix, bucket)
}

// Bucket returns the index of the bucket containing now, measured from anchor.
//
// Floor division, not Go's truncating division: an anchor later than now (a
// clock skew, or a freshly written future anchor) must land in the bucket
// BEFORE the anchor rather than share bucket zero with the first real one.
func Bucket(now, anchor time.Time, size time.Duration) int64 {
	if size <= 0 {
		return 0
	}
	delta := now.UnixMilli() - anchor.UnixMilli()
	step := size.Milliseconds()
	q := delta / step
	if delta%step != 0 && delta < 0 {
		q--
	}
	return q
}

// BucketStart returns the instant bucket begins.
func BucketStart(bucket int64, anchor time.Time, size time.Duration) time.Time {
	return anchor.Add(time.Duration(bucket) * size)
}

// ResetAt returns the instant the window is empty again if no further usage is
// recorded: the moment the CURRENT bucket ages out of the last slot.
//
// For the anchored weekly window (one bucket) that is the end of the anchor's
// current week, which is exactly the full restore the owner ruled for. For the
// sliding session window it is five hours after the current bucket started,
// which is when the newest usage recorded right now finally leaves the window.
func ResetAt(now, anchor time.Time, size time.Duration, count int) time.Time {
	bucket := Bucket(now, anchor, size)
	return BucketStart(bucket+int64(count), anchor, size)
}

// Shape describes one window's bucket geometry, so a caller can iterate both
// windows without repeating the constants.
type Shape struct {
	Name       string
	BucketSize time.Duration
	Buckets    int
	// Anchored reports whether the window indexes from the account anchor
	// (weekly) or from the Unix epoch (session).
	Anchored bool
}

// SessionShape and WeeklyShape are the two shapes Hive enforces.
func SessionShape() Shape {
	return Shape{Name: Session, BucketSize: SessionBucketSize, Buckets: SessionBucketCount}
}

// WeeklyShape is the anchored week.
func WeeklyShape() Shape {
	return Shape{Name: Weekly, BucketSize: WeeklyBucketSize, Buckets: WeeklyBucketCount, Anchored: true}
}

// EffectiveAnchor is the anchor a shape indexes from. The session window is
// epoch aligned deliberately: it slides, so its bucket boundaries are never
// customer visible, and pinning it to the account anchor would buy nothing but
// a second thing to keep in sync.
func (s Shape) EffectiveAnchor(accountAnchor time.Time) time.Time {
	if !s.Anchored {
		return time.Unix(0, 0).UTC()
	}
	return accountAnchor
}

// BucketKeys returns every bucket key in the window ending at now, newest
// first. Read side only: the Lua script builds its own keys so the whole check
// stays one round trip.
func (s Shape) BucketKeys(scopePrefix string, now, accountAnchor time.Time) []string {
	anchor := s.EffectiveAnchor(accountAnchor)
	family := FamilyPrefix(scopePrefix, s.Name)
	current := Bucket(now, anchor, s.BucketSize)
	keys := make([]string, 0, s.Buckets)
	for i := 0; i < s.Buckets; i++ {
		keys = append(keys, BucketKey(family, current-int64(i)))
	}
	return keys
}
