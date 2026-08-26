package matrix

import "strings"

// EndpointStatus represents the support classification of an API endpoint.
type EndpointStatus string

const (
	// StatusSupportedNow indicates the endpoint is fully implemented and available.
	StatusSupportedNow EndpointStatus = "supported_now"

	// StatusPlannedForLaunch indicates the endpoint will be implemented before launch.
	StatusPlannedForLaunch EndpointStatus = "planned_for_launch"

	// StatusExplicitlyUnsupported indicates the endpoint is not planned for launch.
	StatusExplicitlyUnsupported EndpointStatus = "explicitly_unsupported_at_launch"

	// StatusOutOfScope indicates organization/admin endpoints not part of Hive.
	StatusOutOfScope EndpointStatus = "out_of_scope"

	// StatusUnknown indicates the endpoint is not in the support matrix.
	StatusUnknown EndpointStatus = "unknown"
)

// MatrixEntry represents a single endpoint in the support matrix.
type MatrixEntry struct {
	Method string         `json:"method"`
	Path   string         `json:"path"`
	Status EndpointStatus `json:"status"`
	Phase  *int           `json:"phase"`
	Notes  string         `json:"notes"`
}

// SupportMatrix holds the full endpoint classification and provides lookup.
type SupportMatrix struct {
	Version   string        `json:"version"`
	Generated string        `json:"generated"`
	Endpoints []MatrixEntry `json:"endpoints"`
	lookup    map[string]EndpointStatus
}

// Lookup returns the support status for a given method and path combination.
// Returns StatusUnknown if the endpoint is not in the matrix.
func (m *SupportMatrix) Lookup(method, path string) EndpointStatus {
	key := method + " " + path
	if status, ok := m.lookup[key]; ok {
		return status
	}
	for _, ep := range m.Endpoints {
		if ep.Method != method {
			continue
		}
		if pathMatchesTemplate(ep.Path, path) {
			return ep.Status
		}
	}
	return StatusUnknown
}

// HasCoverage reports whether m has any entry at all for a raw mux
// registration pattern: an exact path match, or, for a trailing-slash
// subtree pattern such as "/v1/agent/schedules/", any entry whose path
// falls under that subtree. It is deliberately coarser than Lookup, which
// answers "is this exact request allowed" for one method+path; HasCoverage
// answers "does support-matrix.json know this route exists at all",
// regardless of method or status, which is what a boot-time drift guard
// over registered mux patterns can meaningfully ask (see
// assertMatrixCoverage in apps/edge-api/cmd/server). It cannot see past a
// registered prefix into a handler's own internal path-suffix dispatch
// (routeItem/routeTaskByID-style switches), so a new suffix added under an
// already-covered prefix still needs its own matrix entry added by hand.
func (m *SupportMatrix) HasCoverage(pattern string) bool {
	prefix := strings.TrimSuffix(pattern, "/")
	childPrefix := prefix + "/"
	for _, ep := range m.Endpoints {
		if ep.Path == pattern || ep.Path == prefix || strings.HasPrefix(ep.Path, childPrefix) {
			return true
		}
	}
	return false
}

// buildLookup constructs the internal lookup map from the endpoints slice.
func (m *SupportMatrix) buildLookup() {
	m.lookup = make(map[string]EndpointStatus, len(m.Endpoints))
	for _, ep := range m.Endpoints {
		key := ep.Method + " " + ep.Path
		m.lookup[key] = ep.Status
	}
}

func pathMatchesTemplate(template, path string) bool {
	if template == path {
		return true
	}

	templateParts := splitPath(template)
	pathParts := splitPath(path)
	if len(templateParts) != len(pathParts) {
		return false
	}

	for i := range templateParts {
		part := templateParts[i]
		if isTemplateSegment(part) {
			if pathParts[i] == "" {
				return false
			}
			continue
		}
		if part != pathParts[i] {
			return false
		}
	}

	return true
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func isTemplateSegment(part string) bool {
	return strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")
}
