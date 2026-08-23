package usermemories_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usermemories"
)

func TestSanitizeContent_StripsControlCharsAndCollapsesWhitespace(t *testing.T) {
	got, err := usermemories.SanitizeContent("  prefers\tconcise\nanswers  ")
	require.NoError(t, err)
	require.Equal(t, "prefers concise answers", got)
}

func TestSanitizeContent_CapsAt500Runes(t *testing.T) {
	raw := strings.Repeat("x", 700)
	got, err := usermemories.SanitizeContent(raw)
	require.NoError(t, err)
	require.Len(t, []rune(got), 500)
}

func TestSanitizeContent_EmptyAfterStripFails(t *testing.T) {
	_, err := usermemories.SanitizeContent("\x00\x01\x1b")
	require.ErrorIs(t, err, usermemories.ErrEmptyContent)
}

func TestSanitizeContent_FoldsUnicodeLineSeparators(t *testing.T) {
	got, err := usermemories.SanitizeContent("a b c")
	require.NoError(t, err)
	require.Equal(t, "a b c", got)
}

func TestSanitizeSourceChatID(t *testing.T) {
	long := strings.Repeat("x", 201)
	require.Nil(t, usermemories.SanitizeSourceChatID(nil))
	require.NotNil(t, usermemories.SanitizeSourceChatID(&[]string{"chat-1"}[0]))
	require.Nil(t, usermemories.SanitizeSourceChatID(&long))
	dirty := "chat\x00-1"
	got := usermemories.SanitizeSourceChatID(&dirty)
	require.Equal(t, "chat-1", *got)
}
