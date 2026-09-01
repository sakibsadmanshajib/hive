package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// The model list is where the chat surface learns whether it may attach a tools
// block, so these hold the field end to end through the service rather than
// only over the pure predicate.

func snapshotFixture(routeCaps []RouteToolCapability) *stubRepository {
	return &stubRepository{
		aliases: []ModelAlias{
			{AliasID: "hive-free", Visibility: "public", OwnedBy: "hive"},
			{AliasID: "hive-embed", Visibility: "public", OwnedBy: "hive"},
		},
		routeCapabilities: routeCaps,
	}
}

func TestSnapshotPublishesToolCapabilityPerAlias(t *testing.T) {
	repo := snapshotFixture([]RouteToolCapability{
		{AliasID: "hive-free", HealthState: "healthy", ToolsSupported: true},
		{AliasID: "hive-free", HealthState: "healthy", ToolsSupported: true},
		{AliasID: "hive-embed", HealthState: "healthy", ToolsSupported: false},
	})

	snapshot, err := NewService(repo).GetSnapshot(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}

	got := map[string]bool{}
	for _, model := range snapshot.Models {
		got[model.ID] = model.HiveCapabilities.Tools
	}
	if !got["hive-free"] {
		t.Error("hive-free reported hive_capabilities.tools = false; the chat surface would advertise no tools at all on the pool the demo runs on")
	}
	if got["hive-embed"] {
		t.Error("hive-embed reported hive_capabilities.tools = true on a route that does not support tools; advertising there would narrow route selection on every turn")
	}
}

// An alias whose only route is disabled has nothing that can serve a tool call,
// and the empty-pool case must read false rather than inheriting a stale true.
func TestSnapshotReportsNoToolsWhenEveryRouteIsDisabled(t *testing.T) {
	repo := snapshotFixture([]RouteToolCapability{
		{AliasID: "hive-free", HealthState: "disabled", ToolsSupported: true},
	})

	snapshot, err := NewService(repo).GetSnapshot(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	for _, model := range snapshot.Models {
		if model.ID == "hive-free" && model.HiveCapabilities.Tools {
			t.Fatal("an alias with no enabled route at all reported tool capable")
		}
	}
}

// Loud failure over a plausible-looking one: if the capability read fails, the
// list fails. Serving every alias as tools = false would silently switch web
// access off product-wide with nothing saying why.
func TestSnapshotFailsWhenToolCapabilityCannotBeRead(t *testing.T) {
	repo := snapshotFixture(nil)
	repo.routeCapabilitiesErr = errors.New("capability read failed")

	if _, err := NewService(repo).GetSnapshot(context.Background()); err == nil {
		t.Fatal("GetSnapshot succeeded with an unreadable capability source; every alias would have been published as tools = false")
	}
	if _, err := NewService(repo).GetSnapshotForTenant(context.Background(), uuid.MustParse("aaaaaaaa-0000-0000-0000-0000000000f3")); err == nil {
		t.Fatal("GetSnapshotForTenant succeeded with an unreadable capability source")
	}
}
