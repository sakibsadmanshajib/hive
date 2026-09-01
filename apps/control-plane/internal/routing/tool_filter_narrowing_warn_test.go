package routing

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
)

// The runtime half of the identity argument.
//
// TestAdvertisingToolsNeverNarrowsTheCandidateSet proves the tool filter is the
// identity on every advertised alias in the MIGRATION CHAIN. It cannot prove it
// for a catalog mutated after deploy, and routes plus health_state do move at
// runtime while the model list is cached. So the one thing that must not happen
// is that a narrowing happens and nobody can tell. These two hold the WARN.

func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func narrowingCandidates() []RouteCandidate {
	return []RouteCandidate{
		{
			RouteID:                 "route-warn-plain",
			AliasID:                 "warn-alias",
			Provider:                "provider-a",
			LiteLLMModelName:        "group-plain",
			PriceClass:              "standard",
			HealthState:             "healthy",
			Priority:                10,
			SupportsChatCompletions: true,
			SupportsTools:           false,
		},
		{
			RouteID:                 "route-warn-capable",
			AliasID:                 "warn-alias",
			Provider:                "provider-b",
			LiteLLMModelName:        "group-capable",
			PriceClass:              "standard",
			HealthState:             "healthy",
			Priority:                20,
			SupportsChatCompletions: true,
			SupportsTools:           true,
		},
	}
}

func selectWithTools(t *testing.T, candidates []RouteCandidate, aliasID string) {
	t.Helper()

	repo := &stubRepository{
		policy:     catalog.AliasPolicySnapshot{AliasID: aliasID, PolicyMode: "pinned"},
		candidates: candidates,
	}
	if _, err := NewService(repo, &stubEntitlements{visible: true}).SelectRoute(
		context.Background(),
		SelectionInput{
			AliasID:             aliasID,
			NeedChatCompletions: true,
			RequireToolCapable:  true,
		},
	); err != nil {
		t.Fatalf("SelectRoute on %s: %v", aliasID, err)
	}
}

// The alias the catalog should never have advertised, reached anyway because a
// route was added or degraded after the chat surface read the model list.
func TestSelectRouteWarnsWhenTheToolFilterNarrowsTheCandidateSet(t *testing.T) {
	buf := captureWarnings(t)

	selectWithTools(t, narrowingCandidates(), "warn-alias")

	logged := buf.String()
	if !strings.Contains(logged, "tool capability filter narrowed the candidate set") {
		t.Fatalf("a narrowing produced no warning at all; traffic concentrates onto fewer routes with nothing to see it. Captured: %q", logged)
	}
	if !strings.Contains(logged, "warn-alias") {
		t.Errorf("the warning does not name the alias, so it cannot be acted on. Captured: %q", logged)
	}
}

// And the other direction, which is every request on this deployment today: on
// a uniformly capable alias the filter is the identity and must stay silent. A
// warning that fires on the healthy path is one nobody reads.
func TestSelectRouteIsSilentWhenTheToolFilterRemovesNothing(t *testing.T) {
	buf := captureWarnings(t)

	candidates := narrowingCandidates()
	candidates[0].SupportsTools = true

	selectWithTools(t, candidates, "warn-alias")

	if strings.Contains(buf.String(), "narrowed the candidate set") {
		t.Fatalf("a uniformly tool-capable alias produced a narrowing warning: %q", buf.String())
	}
}

// A disabled incapable route is not a narrowing: the config sync never emits it,
// so it cannot receive a dispatch and its removal concentrates nothing. This is
// the same exclusion the group veto and the health filter already make, held
// here so the counters cannot drift apart from them.
func TestSelectRouteDoesNotWarnAboutDisabledRoutes(t *testing.T) {
	buf := captureWarnings(t)

	candidates := narrowingCandidates()
	candidates[0].HealthState = "disabled"

	selectWithTools(t, candidates, "warn-alias")

	if strings.Contains(buf.String(), "narrowed the candidate set") {
		t.Fatalf("a disabled incapable route was counted as a narrowing: %q", buf.String())
	}
}
