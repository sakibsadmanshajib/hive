package catalog

import "strings"

// Tool advertisement, decided from the catalog rather than from route selection.
//
// WHAT THIS ANSWERS
//
// "May Hive attach the web_search and web_fetch specs to a chat request on this
// alias, knowing that doing so will not change which routes the request can
// land on?" It is published as hive_capabilities.tools on GET /v1/models, and
// the chat surface attaches a tools block only for an alias reporting true.
//
// WHY THE QUESTION HAS TO BE ANSWERED HERE AND NOT AT DISPATCH
//
// edge-api folds tools, tool_choice, response_format and functions into one
// RequireToolCapable bool (inference.firstToolParam), and routing.SelectRoute
// then narrows the candidate set to tool-capable routes. So advertising tools
// by default would change route selection on EVERY chat turn, not only the
// turns that call a tool. On a free pool of two Groq routes there is no room
// for that: narrowing it would concentrate all traffic on fewer members.
//
// The containment is that filtering an all-capable set is the identity
// function. Advertisement is therefore made conditional on the alias already
// being uniformly tool-capable, which is what this predicate decides, and
// RequireToolCapable can then be set honestly on the chat path without
// removing a single candidate. TestAdvertisingToolsNeverNarrowsTheCandidateSet
// in the routing package holds that equality against the folded migration
// chain, through the real SelectRoute.
//
// EVERY ENABLED ROUTE, NOT EVERY GROUP
//
// SelectRoute's own filter works at gateway-group granularity: a group is
// capable when every enabled member sharing its litellm_model_name is. That is
// the right rule for "can this request be served", but it is NOT the rule for
// "does advertising narrow anything": an alias with one all-capable group and
// one incapable group passes the group rule and still loses the second group's
// routes the moment the flag is set. Requiring every enabled route of the alias
// to be capable is the stricter condition, it implies group uniformity, and it
// is exactly the condition under which the filter is the identity.
//
// DISABLED ROUTES ARE EXCLUDED, for the same reason SelectRoute excludes them
// from its group veto: the LiteLLM config sync does not emit a disabled route,
// so it cannot receive a dispatch and cannot make a live alias incapable.

// RouteToolCapability is one public.provider_routes row joined to its
// public.provider_capabilities row, reduced to the three columns this decision
// reads. A route with no capabilities row is absent rather than false, which
// mirrors the inner join in routing.ListRouteCandidates: such a route cannot be
// selected at all, so it must not veto an alias it can never serve.
type RouteToolCapability struct {
	AliasID        string
	HealthState    string
	ToolsSupported bool
}

// ToolCapableAliases reports, per alias id, whether tools may be advertised on
// that alias. An alias is capable when it has at least one enabled route and
// every one of its enabled routes supports tools.
//
// An alias with no enabled route at all is reported false rather than omitted
// from the map, so a reader that only checks presence cannot mistake "nothing
// left to serve this" for "capable". An alias absent from rows entirely is
// absent from the map, and Go's zero value for the missing key is false, which
// is the same honest answer.
func ToolCapableAliases(rows []RouteToolCapability) map[string]bool {
	seen := make(map[string]bool, len(rows))
	enabled := make(map[string]int, len(rows))
	toolCapable := make(map[string]int, len(rows))

	for _, row := range rows {
		// Trimmed, while SelectRoute and buildCatalogSnapshot key on the
		// untrimmed alias_id. A padded alias_id would therefore land here under
		// a key neither of them looks up and read false, which refuses to
		// advertise rather than advertising wrongly. Noted rather than
		// reconciled: the column has no padded row and the failure direction is
		// the safe one.
		aliasID := strings.TrimSpace(row.AliasID)
		if aliasID == "" {
			continue
		}
		seen[aliasID] = true
		if strings.EqualFold(strings.TrimSpace(row.HealthState), "disabled") {
			continue
		}
		enabled[aliasID]++
		if row.ToolsSupported {
			toolCapable[aliasID]++
		}
	}

	capable := make(map[string]bool, len(seen))
	for aliasID := range seen {
		capable[aliasID] = enabled[aliasID] > 0 && enabled[aliasID] == toolCapable[aliasID]
	}

	return capable
}
