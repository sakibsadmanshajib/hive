package metrics

import (
	"fmt"
	"net/http"
	"regexp"
	"time"
)

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// InstrumentHandler returns a middleware that records HTTP request metrics.
// It wraps the next handler with counters and histograms for request count,
// duration, method, and status class. Low-cardinality endpoint labels are
// enforced via normalizeEndpoint.
func InstrumentHandler(reg *Registry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		statusClass := fmt.Sprintf("%dxx", recorder.status/100)
		endpoint := normalizeEndpoint(r.URL.Path)
		reg.HTTPRequestsTotal.WithLabelValues(endpoint, r.Method, statusClass).Inc()
		reg.HTTPRequestDuration.WithLabelValues(endpoint).Observe(time.Since(start).Seconds())
	})
}

// uuidPattern matches UUID-like segments in URL paths.
var uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// normalizeEndpoint replaces UUID segments in URL paths with ":id" to prevent
// cardinality explosion in Prometheus label values.
// CRITICAL: Never emit raw UUID values, user IDs, or API key IDs as label values.
func normalizeEndpoint(path string) string {
	return uuidPattern.ReplaceAllString(path, ":id")
}
