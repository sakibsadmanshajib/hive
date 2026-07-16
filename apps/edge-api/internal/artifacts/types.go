// Package artifacts implements the /v1/artifacts/* management endpoints and
// the /artifacts/* hosting endpoints (issue #312, agent-subsystem blueprint
// Step 3.3). A self-contained HTML blob is stored keyed by artifact id and
// version, served at a stable URL that survives redeploys, and stays
// private until the owning tenant explicitly shares it.
package artifacts

import (
	"errors"
	"time"
)

// ErrNotFound is returned when an artifact or version does not exist, is
// not visible to the caller under RLS, or (for management writes) is not
// owned by the caller's tenant. Handlers map it to HTTP 404 uniformly so a
// private artifact never distinguishes "does not exist" from "exists but
// you cannot see it" for an unauthorized caller.
var ErrNotFound = errors.New("not found")

// MaxHTMLSize bounds the accepted artifact body (5 MiB). Artifacts are
// self-contained HTML documents, not general file uploads (that is
// apps/edge-api/internal/files); this cap is far below files.MaxFileSize
// and exists to bound request-body decoding cost, not storage cost.
const MaxHTMLSize = 5 << 20

// CreateRequest is the JSON body for POST /v1/artifacts.
type CreateRequest struct {
	Name string `json:"name"`
	HTML string `json:"html"`
}

// AddVersionRequest is the JSON body for POST /v1/artifacts/{id}/versions.
type AddVersionRequest struct {
	HTML string `json:"html"`
}

// ShareRequest is the JSON body for POST /v1/artifacts/{id}/share.
type ShareRequest struct {
	Public bool `json:"public"`
}

// VersionResponse is returned by create and add-version.
type VersionResponse struct {
	ID           string    `json:"id"`
	Version      int       `json:"version"`
	URL          string    `json:"url"`           // latest-version URL, stable across redeploys
	VersionedURL string    `json:"versioned_url"` // this specific version's URL
	CreatedAt    time.Time `json:"created_at"`
}

// ShareResponse is returned by the share endpoint.
type ShareResponse struct {
	ID     string `json:"id"`
	Public bool   `json:"public"`
}
