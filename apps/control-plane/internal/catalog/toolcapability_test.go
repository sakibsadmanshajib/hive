package catalog

import "testing"

// TestToolCapableAliases pins the advertisement rule row by row. Each case is
// one way the rule can be got wrong.
func TestToolCapableAliases(t *testing.T) {
	cases := []struct {
		name string
		rows []RouteToolCapability
		want map[string]bool
	}{
		{
			name: "every enabled route capable",
			rows: []RouteToolCapability{
				{AliasID: "hive-free", HealthState: "healthy", ToolsSupported: true},
				{AliasID: "hive-free", HealthState: "healthy", ToolsSupported: true},
			},
			want: map[string]bool{"hive-free": true},
		},
		{
			name: "one incapable enabled route sinks the alias",
			rows: []RouteToolCapability{
				{AliasID: "hive-free", HealthState: "healthy", ToolsSupported: true},
				{AliasID: "hive-free", HealthState: "healthy", ToolsSupported: false},
			},
			want: map[string]bool{"hive-free": false},
		},
		{
			name: "an incapable route seen after a capable one still sinks it",
			rows: []RouteToolCapability{
				{AliasID: "hive-free", HealthState: "healthy", ToolsSupported: false},
				{AliasID: "hive-free", HealthState: "healthy", ToolsSupported: true},
			},
			want: map[string]bool{"hive-free": false},
		},
		{
			name: "a disabled incapable route cannot veto: the config sync never emits it",
			rows: []RouteToolCapability{
				{AliasID: "hive-free", HealthState: "healthy", ToolsSupported: true},
				{AliasID: "hive-free", HealthState: "disabled", ToolsSupported: false},
			},
			want: map[string]bool{"hive-free": true},
		},
		{
			name: "case and padding in health_state are not a way past the veto",
			rows: []RouteToolCapability{
				{AliasID: "hive-free", HealthState: "healthy", ToolsSupported: true},
				{AliasID: "hive-free", HealthState: " DISABLED ", ToolsSupported: false},
			},
			want: map[string]bool{"hive-free": true},
		},
		{
			name: "an alias with nothing enabled is false, never absent",
			rows: []RouteToolCapability{
				{AliasID: "hive-retired", HealthState: "disabled", ToolsSupported: true},
			},
			want: map[string]bool{"hive-retired": false},
		},
		{
			name: "aliases are independent",
			rows: []RouteToolCapability{
				{AliasID: "hive-free", HealthState: "healthy", ToolsSupported: true},
				{AliasID: "hive-embed", HealthState: "healthy", ToolsSupported: false},
			},
			want: map[string]bool{"hive-free": true, "hive-embed": false},
		},
		{
			name: "no rows at all is an empty map, not a capable one",
			rows: nil,
			want: map[string]bool{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ToolCapableAliases(tc.rows)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for aliasID, want := range tc.want {
				if got[aliasID] != want {
					t.Errorf("alias %s = %v, want %v", aliasID, got[aliasID], want)
				}
			}
		})
	}
}

// TestToolCapableAliasesDoesNotInventCapabilityForAnUnknownAlias is the
// negative half of the false-never-absent rule: a caller asking about an alias
// this catalog has never routed must read false, because the zero value for a
// missing key is the honest answer and nothing may depend on presence alone.
func TestToolCapableAliasesDoesNotInventCapabilityForAnUnknownAlias(t *testing.T) {
	capable := ToolCapableAliases([]RouteToolCapability{
		{AliasID: "hive-free", HealthState: "healthy", ToolsSupported: true},
	})
	if capable["hive-does-not-exist"] {
		t.Fatal("an alias with no routes at all reported tool capable")
	}
}
