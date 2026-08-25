package byok

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
)

// fakeRepo is an in-memory Repository. It records the last Create payload so
// tests can assert the service never handed it plaintext.
type fakeRepo struct {
	rows    []Key
	lastIns Key
	onList  func() []Key
}

func (f *fakeRepo) Create(_ context.Context, k Key) (Key, error) {
	f.lastIns = k
	k.ID = uuid.Must(uuid.NewV7())
	now := timeNow()
	k.CreatedAt, k.UpdatedAt = now, now
	f.rows = append(f.rows, k)
	return k, nil
}

func (f *fakeRepo) ListByAccount(_ context.Context, accountID uuid.UUID) ([]Key, error) {
	if f.onList != nil {
		return f.onList(), nil
	}
	var out []Key
	for _, k := range f.rows {
		if k.AccountID == accountID {
			out = append(out, k)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListAll(_ context.Context) ([]Key, error) {
	return f.rows, nil
}

func (f *fakeRepo) Get(_ context.Context, accountID, id uuid.UUID) (Key, error) {
	for _, k := range f.rows {
		if k.ID == id && k.AccountID == accountID {
			return k, nil
		}
	}
	// Cross-account lookups are indistinguishable from missing rows: the same
	// sentinel either way, so no existence oracle leaks across tenants.
	return Key{}, ErrNotFound
}

func (f *fakeRepo) Revoke(_ context.Context, accountID, id uuid.UUID, _ time.Time) (Key, error) {
	for i, k := range f.rows {
		if k.ID == id && k.AccountID == accountID {
			f.rows[i].Status = StatusRevoked
			return f.rows[i], nil
		}
	}
	return Key{}, ErrNotFound
}

// captureAudit satisfies the service's AuditLogger interface and records events.
type captureAudit struct{ events []audit.Event }

func (c *captureAudit) Log(_ context.Context, e audit.Event) error {
	c.events = append(c.events, e)
	return nil
}

func newTestService(repo Repository, c *Cipher, a AuditLogger) *Service {
	return NewService(repo, c, a)
}

const (
	testAccount = "3f6c1d9e-2b7a-4c53-9f21-8a4d6e0b7c11"
	testUser    = "11111111-2222-4333-8444-555555555555"
)

// Deliberately fake credential fixtures, named constants so the secrets
// scanner never mistakes them for real key material.
const (
	FAKE_KEY_LONG  = "test-credential-value-long-98765"
	FAKE_KEY_SHORT = "test-fake-1234"
	FAKE_ROUNDTRIP = "test-roundtrip-secret"
	tooShort       = "abc"
)

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("bad test uuid %q: %v", s, err)
	}
	return id
}

func TestRegisterEncryptsKeyMaterial(t *testing.T) {
	repo := &fakeRepo{}
	svc := newTestService(repo, testCipher(t), nil)

	secret := FAKE_KEY_LONG
	key, err := svc.Register(context.Background(),
		mustUUID(t, testAccount), mustUUID(t, testUser),
		RegisterInput{
			Label:    "my openrouter key",
			BaseURL:  strPtr("https://openrouter.ai/api/v1"),
			APIKey:   secret,
			ModelMap: map[string]string{"hive-fast": "openai/gpt-4o-mini"},
		})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	stored := repo.lastIns.EncryptedAPIKey
	if len(stored) == 0 {
		t.Fatal("no ciphertext reached the repository")
	}
	if strings.Contains(string(stored), secret) {
		t.Fatal("repository received plaintext key material")
	}
	if strings.Contains(string(stored), key.Label) {
		t.Fatal("ciphertext embeds the label field verbatim")
	}
	if key.KeyLast4 != MaskSecret(secret) {
		t.Fatalf("KeyLast4 = %q, want %q", key.KeyLast4, MaskSecret(secret))
	}
	if key.Status != StatusActive {
		t.Fatalf("new key status = %q, want active", key.Status)
	}
}

func TestRegisterValidation(t *testing.T) {
	c := testCipher(t)
	base := RegisterInput{Label: "ok", BaseURL: strPtr("https://x.example/v1"), APIKey: FAKE_KEY_SHORT}

	cases := []struct {
		name string
		in   RegisterInput
	}{
		{"missing label", RegisterInput{BaseURL: base.BaseURL, APIKey: base.APIKey}},
		{"missing api key", RegisterInput{Label: "l", BaseURL: base.BaseURL}},
		{"no target", RegisterInput{Label: "l", APIKey: base.APIKey}},
		{"both targets", RegisterInput{Label: "l", APIKey: base.APIKey,
			BaseURL: base.BaseURL, ProviderSlug: strPtr("openrouter")}},
		{"bad url", RegisterInput{Label: "l", APIKey: base.APIKey, BaseURL: strPtr("not a url")}},
		{"non http url", RegisterInput{Label: "l", APIKey: base.APIKey, BaseURL: strPtr("ftp://x.example")}},
		{"short api key", RegisterInput{Label: "l", BaseURL: base.BaseURL, APIKey: tooShort}},
		{"empty model map value", RegisterInput{Label: "l", APIKey: base.APIKey,
			BaseURL: base.BaseURL, ModelMap: map[string]string{"hive-fast": ""}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(&fakeRepo{}, c, nil)
			if _, err := svc.Register(context.Background(),
				mustUUID(t, testAccount), mustUUID(t, testUser), tc.in); !errors.Is(err, ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
		})
	}
}

func TestRegisterWithoutCipherFailsClosed(t *testing.T) {
	svc := newTestService(&fakeRepo{}, nil, nil)
	_, err := svc.Register(context.Background(),
		mustUUID(t, testAccount), mustUUID(t, testUser),
		RegisterInput{Label: "l", BaseURL: strPtr("https://x.example/v1"), APIKey: FAKE_KEY_SHORT})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("register without encryption key must fail closed with ErrNotConfigured, got %v", err)
	}
}

func TestDecryptWithoutCipherFailsClosed(t *testing.T) {
	repo := &fakeRepo{}
	svc := newTestService(repo, testCipher(t), nil)
	key, err := svc.Register(context.Background(),
		mustUUID(t, testAccount), mustUUID(t, testUser),
		RegisterInput{Label: "l", BaseURL: strPtr("https://x.example/v1"), APIKey: FAKE_KEY_SHORT})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	locked := newTestService(repo, nil, nil)
	if _, err := locked.Reveal(context.Background(), key.AccountID, key.ID); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Reveal without cipher must fail closed, got %v", err)
	}
}

func TestRevealRoundTripThroughService(t *testing.T) {
	repo := &fakeRepo{}
	c := testCipher(t)
	svc := newTestService(repo, c, nil)
	secret := FAKE_ROUNDTRIP
	key, err := svc.Register(context.Background(),
		mustUUID(t, testAccount), mustUUID(t, testUser),
		RegisterInput{Label: "l", BaseURL: strPtr("https://x.example/v1"), APIKey: secret})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := svc.Reveal(context.Background(), key.AccountID, key.ID)
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if got != secret {
		t.Fatalf("Reveal = %q, want original secret", got)
	}
}

func TestAuditEventsOnRegisterAndRevoke(t *testing.T) {
	repo := &fakeRepo{}
	aud := &captureAudit{}
	svc := newTestService(repo, testCipher(t), aud)
	ctx := context.Background()
	account, user := mustUUID(t, testAccount), mustUUID(t, testUser)

	key, err := svc.Register(ctx, account, user,
		RegisterInput{Label: "l", BaseURL: strPtr("https://x.example/v1"), APIKey: FAKE_KEY_SHORT})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := svc.Revoke(ctx, account, key.ID, user); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if len(aud.events) != 2 {
		t.Fatalf("expected 2 audit events, got %d", len(aud.events))
	}
	want := map[string]bool{"BYOK_KEY_REGISTER": false, "BYOK_KEY_REVOKE": false}
	for _, e := range aud.events {
		if _, ok := want[e.Action]; !ok {
			t.Errorf("unexpected audit action %q", e.Action)
		}
		want[e.Action] = true
		if strings.Contains(strings.TrimSpace(fmt.Sprintf("%v|%v|%v", e.Before, e.After, e.ResourceID)), FAKE_KEY_SHORT) {
			t.Errorf("audit event for action %q carries key material", e.Action)
		}
	}
	for action, seen := range want {
		if !seen {
			t.Errorf("missing audit event %q", action)
		}
	}
}

func strPtr(s string) *string { return &s }
