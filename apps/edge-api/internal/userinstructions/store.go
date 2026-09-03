// Package userinstructions implements per-user custom instructions (issue
// #1363): the standing "how should the assistant respond" text a person
// writes once, which chat dispatch then prepends to the system messages of
// every one of their requests.
//
// The slice lives in edge-api rather than control-plane, and that is a
// deliberate departure from the sibling usermemories slice
// (apps/control-plane/internal/usermemories) rather than an oversight.
// usermemories is a service-to-service surface: it takes tenant_id and
// user_id as URL path segments behind the shared-secret gate, so a browser
// session cannot address it and no caller-facing route exists to resolve one
// into the other. edge-api already resolves exactly this principal for every
// chat request, through the same OWUIUnwrap plus JWT selector that gates
// /v1/chat/completions, so mounting the surface here costs no new identity
// path and gives it the same authentication as the traffic it shapes. The
// state is still Hive-owned Postgres read and written only by Hive code,
// which is what .wolf/decisions.md D-044 requires; what it forbids is Open
// WebUI's own database being the source of truth, and it is not.
//
// Storage lives in public.user_instructions, one row per (tenant, user),
// enforced by that table's primary key. See
// supabase/migrations/20260902_03_user_instructions.sql for why this is a
// table of its own and not a discriminator column on public.user_memories.
package userinstructions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxContentLen caps stored instructions at the write boundary, in runes.
// The CHECK on public.user_instructions enforces the same number at storage
// level; this one exists so an over-long body is refused with a sentence the
// user can act on rather than a constraint violation.
const MaxContentLen = 4000

// ErrTooLong is returned when content exceeds MaxContentLen after
// sanitization. Over-length is the one input problem worth naming back to
// the caller: everything else this package rejects, it rejects by folding to
// empty, which means "clear my instructions" and is a legal request.
var ErrTooLong = errors.New("userinstructions: content is too long")

// The two format characters Sanitize keeps. Both are category Cf and both
// change rendered glyphs in Indic and Arabic scripts and in emoji sequences,
// so stripping them corrupts real text rather than removing decoration.
const (
	zeroWidthNonJoiner = '‌'
	zeroWidthJoiner    = '‍'
)

// Store is the narrow data-access port for public.user_instructions. Every
// method is scoped by the (tenantID, userID) pair the caller resolved from
// the authenticated principal: tenant scope additionally comes from RLS
// (app.current_tenant_id), user scope from the explicit WHERE clause, the
// same split public.user_memories and public.agent_tasks use.
type Store interface {
	// Instructions returns the user's instructions, or the empty string when
	// they have none. Absence is not an error: a person who has never written
	// instructions is the common case, not a failure.
	// The name matches chat.InstructionSource in
	// apps/edge-api/internal/chat/memory.go, so this store satisfies that
	// consumer interface directly with no adapter in between.
	Instructions(ctx context.Context, tenantID, userID uuid.UUID) (string, error)
	// Put stores content, replacing whatever was there. Empty content
	// deletes the row, so "I cleared the box" and "I never wrote anything"
	// are the same state rather than two states that render differently.
	Put(ctx context.Context, tenantID, userID uuid.UUID, content string) error
}

type pgxStore struct {
	pool *pgxpool.Pool
}

// NewStore constructs the pgxpool-backed Store.
func NewStore(pool *pgxpool.Pool) Store {
	return &pgxStore{pool: pool}
}

// withTenantTx mirrors apps/edge-api/internal/chat/memory.go's helper of the
// same name. See that doc for why a bare set_config outside a transaction
// does not survive connection pooling.
func (s *pgxStore) withTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("userinstructions: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("userinstructions: set tenant: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *pgxStore) Instructions(ctx context.Context, tenantID, userID uuid.UUID) (string, error) {
	var content string
	err := s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT content
			  FROM public.user_instructions
			 WHERE tenant_id = $1 AND user_id = $2
		`, tenantID, userID)
		switch scanErr := row.Scan(&content); {
		case errors.Is(scanErr, pgx.ErrNoRows):
			content = ""
			return nil
		case scanErr != nil:
			return fmt.Errorf("userinstructions: get scan: %w", scanErr)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return content, nil
}

func (s *pgxStore) Put(ctx context.Context, tenantID, userID uuid.UUID, content string) error {
	return s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if content == "" {
			if _, err := tx.Exec(ctx, `
				DELETE FROM public.user_instructions
				 WHERE tenant_id = $1 AND user_id = $2
			`, tenantID, userID); err != nil {
				return fmt.Errorf("userinstructions: delete: %w", err)
			}
			return nil
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO public.user_instructions (tenant_id, user_id, content)
			VALUES ($1, $2, $3)
			ON CONFLICT (tenant_id, user_id)
			DO UPDATE SET content = EXCLUDED.content, updated_at = now()
		`, tenantID, userID, content); err != nil {
			return fmt.Errorf("userinstructions: upsert: %w", err)
		}
		return nil
	})
}

// Sanitize folds one submitted instruction body into what is safe to store
// and to place in a system message.
//
// Unlike usermemories.SanitizeContent, newlines and tabs SURVIVE. A memory is
// one extracted fact and renders as one line of a bulleted block, so folding
// its newlines away protects that block's framing. Instructions are prose the
// person wrote, often a list, and flattening them into a single line would
// destroy the thing they typed. The block this text lands in is delimited by
// its own heading and carries no per-line structure for a newline to forge,
// so keeping them costs nothing.
//
// Everything else is still removed: carriage returns fold into the newline
// they precede, U+2028 and U+2029 fold to newlines (they are line separators
// that many renderers treat as breaks and many do not, so normalising them
// removes the disagreement), and every remaining control character plus the
// Unicode replacement character is dropped outright.
//
// FORMAT characters go too, with two named exceptions. unicode.IsControl
// covers category Cc only, so before this the bidi overrides U+202A to U+202E
// and U+2066 to U+2069 survived, along with zero-width spaces and joiners.
// Those are invisible in the echo-back textarea, which is the whole problem:
// text that reads one way to the person approving it and another way to
// whatever renders it later. An override pair can reverse the apparent
// meaning of a line while the box shows nothing at all.
//
// The exceptions are U+200C ZERO WIDTH NON-JOINER and U+200D ZERO WIDTH
// JOINER, which are Cf and are kept deliberately. They are not decoration in
// Bengali, Hindi or Arabic, they change which glyph is rendered, and Hive's
// first market is Bangladesh. They also hold multi-codepoint emoji together.
// Dropping them would silently corrupt legitimate text, which is a worse
// failure than the invisibility they share with the overrides, and neither
// can reorder a line the way a bidi override can.
//
// Empty output is a legal, meaningful answer, not an error: it means the
// person cleared the box, which Put turns into a deleted row.
func Sanitize(raw string) (string, error) {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\r' || r == ' ' || r == ' ':
			return '\n'
		case r == zeroWidthNonJoiner || r == zeroWidthJoiner:
			return r
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == unicode.ReplacementChar:
			return -1
		default:
			return r
		}
	}, normalized)

	// Trailing whitespace on each line, plus leading and trailing blank
	// lines, are typing residue rather than content. Interior blank lines
	// stay: they are paragraph breaks the person meant.
	lines := strings.Split(cleaned, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	content := strings.Trim(strings.Join(lines, "\n"), "\n \t")

	// Counted in runes, matching the CHECK on public.user_instructions, which
	// uses char_length rather than octet_length. Refused rather than
	// truncated: silently dropping the end of someone's instructions would
	// change what they asked for without telling them.
	if len([]rune(content)) > MaxContentLen {
		return "", ErrTooLong
	}
	return content, nil
}
