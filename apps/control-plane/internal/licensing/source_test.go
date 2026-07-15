package licensing_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/licensing"
)

func TestFileSource_ReadsAndVerifiesFromDisk(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	payload := licensing.LicensePayload{Tier: "enterprise", Seats: 25, IssuedAt: now, ExpiresAt: now.AddDate(1, 0, 0)}
	fileBytes, err := licensing.Sign(payload, priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	path := filepath.Join(t.TempDir(), "license.json")
	if err := os.WriteFile(path, fileBytes, 0o600); err != nil {
		t.Fatalf("write license file: %v", err)
	}

	src := licensing.FileSource{
		Path:         path,
		PublicKeyB64: base64.StdEncoding.EncodeToString(pub),
		Now:          func() time.Time { return now },
	}
	e, err := src.Current(context.Background())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if !e.Valid || e.Tier != "enterprise" || e.Seats != 25 {
		t.Fatalf("unexpected entitlement: %+v", e)
	}
}

func TestFileSource_MissingFileReturnsError(t *testing.T) {
	src := licensing.FileSource{Path: "/nonexistent/path/license.json", PublicKeyB64: "irrelevant"}
	if _, err := src.Current(context.Background()); err == nil {
		t.Fatalf("expected error for missing license file")
	}
}

func TestCloudSource_ReturnsConfiguredEntitlement(t *testing.T) {
	want := licensing.DefaultCloudEntitlement(time.Now())
	src := licensing.CloudSource{Entitlement: want}
	got, err := src.Current(context.Background())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if got != want {
		t.Fatalf("cloud source did not return configured entitlement: got %+v want %+v", got, want)
	}
}

func TestDefaultCloudEntitlement_IsValidAndUnrestricted(t *testing.T) {
	now := time.Now()
	e := licensing.DefaultCloudEntitlement(now)
	if !e.Valid {
		t.Fatalf("expected default cloud entitlement to be valid")
	}
	if !e.ExpiresAt.After(now.AddDate(50, 0, 0)) {
		t.Fatalf("expected far-future expiry, got %v", e.ExpiresAt)
	}
}

type fakeSource struct {
	fn func() (licensing.Entitlement, error)
}

func (f fakeSource) Current(context.Context) (licensing.Entitlement, error) { return f.fn() }

func TestScheduledSource_CachesUntilIntervalElapses(t *testing.T) {
	calls := 0
	fake := fakeSource{fn: func() (licensing.Entitlement, error) {
		calls++
		return licensing.Entitlement{Tier: "enterprise", Seats: calls}, nil
	}}
	clock := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	sched := &licensing.ScheduledSource{
		Inner:    fake,
		Interval: time.Minute,
		Now:      func() time.Time { return clock },
	}

	e1, err := sched.Current(context.Background())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	e2, err := sched.Current(context.Background())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 underlying call before interval elapses, got %d", calls)
	}
	if e1.Seats != e2.Seats {
		t.Fatalf("expected cached value to be reused: %+v vs %+v", e1, e2)
	}

	clock = clock.Add(2 * time.Minute)
	e3, err := sched.Current(context.Background())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected re-validation after interval elapses, got %d calls", calls)
	}
	if e3.Seats == e1.Seats {
		t.Fatalf("expected a fresh value after interval elapses")
	}
}

func TestScheduledSource_CachesErrorsToo(t *testing.T) {
	calls := 0
	fake := fakeSource{fn: func() (licensing.Entitlement, error) {
		calls++
		return licensing.Entitlement{}, os.ErrNotExist
	}}
	clock := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	sched := &licensing.ScheduledSource{
		Inner:    fake,
		Interval: time.Minute,
		Now:      func() time.Time { return clock },
	}
	if _, err := sched.Current(context.Background()); err == nil {
		t.Fatalf("expected cached error to be returned")
	}
	if _, err := sched.Current(context.Background()); err == nil {
		t.Fatalf("expected cached error to be returned on second call")
	}
	if calls != 1 {
		t.Fatalf("expected error to be cached (1 underlying call), got %d", calls)
	}
}

func TestNoOpGateKeyPolicy_NeverRestricts(t *testing.T) {
	var p licensing.GateKeyPolicy = licensing.NoOpGateKeyPolicy{}
	keys, ok := p.AllowedGateKeys("enterprise")
	if ok {
		t.Fatalf("expected NoOpGateKeyPolicy to report unrestricted (ok=false)")
	}
	if keys != nil {
		t.Fatalf("expected nil keys, got %v", keys)
	}
}
