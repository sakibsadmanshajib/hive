package main

import (
	"net/http"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/featuregate"
)

// Feature-gate attachment for the route families that are only reachable when
// the tenant holds the matching gate.
//
// These three lines used to sit inline in main(). Every test in the repository
// built its own gate middleware and called it directly, so the attachment (the
// fact that this exact gate wraps this exact path prefix) was covered by
// nothing: deleting the wrapper left the whole Go suite green while the RAG and
// Cowork surfaces served every tenant. Issue #793. Keeping the attachment in a
// named function lets gated_routes_test.go build the same mux the process
// serves and assert the denial, so removing or swapping a gate turns red.

// registerRAGRoutes attaches the RAG surface to mux behind FeatureRAG.
func registerRAGRoutes(mux *http.ServeMux, gate *featuregate.Gate, ragHandler http.Handler) {
	mux.Handle("/v1/rag/", gate.Require(featuregate.FeatureRAG)(ragHandler))
}

// registerAgentTaskRoutes attaches the agent-task lifecycle surface to mux
// behind FeatureCowork. Both the exact path and the subtree share one gated
// handler so a request to either is denied identically.
func registerAgentTaskRoutes(mux *http.ServeMux, gate *featuregate.Gate, taskHandler http.Handler) {
	gated := gate.Require(featuregate.FeatureCowork)(taskHandler)
	mux.Handle("/v1/agent/tasks", gated)
	mux.Handle("/v1/agent/tasks/", gated)
}

// registerAgentScheduleRoutes attaches the scheduled-agent-task ("routines")
// CRUD surface to mux behind FeatureCowork: deployments (tenants) without
// Cowork enabled get the same 403 the task routes return, never a 404, and
// removing or swapping this gate turns gated_routes_test.go red.
func registerAgentScheduleRoutes(mux *http.ServeMux, gate *featuregate.Gate, scheduleHandler http.Handler) {
	gated := gate.Require(featuregate.FeatureCowork)(scheduleHandler)
	mux.Handle("/v1/agent/schedules", gated)
	mux.Handle("/v1/agent/schedules/", gated)
}
