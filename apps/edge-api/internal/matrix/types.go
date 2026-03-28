package matrix

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
	return StatusUnknown
}

// buildLookup constructs the internal lookup map from the endpoints slice.
func (m *SupportMatrix) buildLookup() {
	m.lookup = make(map[string]EndpointStatus, len(m.Endpoints))
	for _, ep := range m.Endpoints {
		key := ep.Method + " " + ep.Path
		m.lookup[key] = ep.Status
	}
}
