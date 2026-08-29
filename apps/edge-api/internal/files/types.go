package files

import (
	"errors"
	"net/http"

	sharedstorage "github.com/sakibsadmanshajib/hive/packages/storage"
)

// ErrNotFound is returned when a file or upload resource does not exist for the given account.
var ErrNotFound = errors.New("not found")

// FileObject is the OpenAI-compatible representation of an uploaded file.
type FileObject struct {
	ID          string `json:"id"`
	Object      string `json:"object"` // always "file"
	Bytes       int64  `json:"bytes"`
	CreatedAt   int64  `json:"created_at"`
	Filename    string `json:"filename"`
	Purpose     string `json:"purpose"`
	Status      string `json:"status"`
	StoragePath string `json:"-"` // internal; not serialized to clients
}

// UploadObject is the OpenAI-compatible representation of a multipart upload.
type UploadObject struct {
	ID          string      `json:"id"`
	Object      string      `json:"object"` // always "upload"
	Bytes       int64       `json:"bytes"`
	CreatedAt   int64       `json:"created_at"`
	Filename    string      `json:"filename"`
	Purpose     string      `json:"purpose"`
	Status      string      `json:"status"`
	ExpiresAt   int64       `json:"expires_at"`
	File        *FileObject `json:"file,omitempty"`
	S3UploadID  *string     `json:"-"` // internal; not serialized to clients
	StoragePath string      `json:"-"` // internal; not serialized to clients
}

// UploadPartObject is the OpenAI-compatible representation of an upload part.
type UploadPartObject struct {
	ID        string `json:"id"`
	Object    string `json:"object"` // always "upload.part"
	CreatedAt int64  `json:"created_at"`
	UploadID  string `json:"upload_id"`
}

// FileListResponse is the OpenAI-compatible list response for files.
type FileListResponse struct {
	Object string       `json:"object"` // always "list"
	Data   []FileObject `json:"data"`
}

// DeletedFileResponse is the OpenAI-compatible response for a file deletion.
type DeletedFileResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"` // always "file"
	Deleted bool   `json:"deleted"`
}

// CompletePart holds the part number and ETag needed to finalize a multipart upload.
type CompletePart = sharedstorage.CompletePart

// ValidPurposes lists the accepted values for file purpose.
var ValidPurposes = map[string]bool{
	"batch":      true,
	"assistants": true,
	"fine-tune":  true,
	"vision":     true,
}

// MaxFileSize is the maximum allowed file upload size (512 MB).
const MaxFileSize = 512 << 20

// multipartMemoryBudget is what an upload may hold in RAM while being parsed.
// Everything past it spills to a temporary file, which net/http removes when
// the request ends.
//
// This is r.ParseMultipartForm's maxMemory argument, which is a BUFFERING
// THRESHOLD and not an acceptance limit. Passing MaxFileSize here (as this
// package used to) did not raise the size of an accepted upload by one byte:
// the size limit is enforced separately, against the part's own length, and
// is unchanged. What it did was let a single upload pin 512 MiB of live heap,
// which no GOMEMLIMIT can reclaim because it is reachable, so two concurrent
// uploads exceeded edge-api's container memory limit on their own and got the
// whole service OOM-killed, dropping every in-flight SSE stream with it.
//
// 32 MiB matches what the images surface already passes, and is well above
// any realistic non-file form field.
//
// The trade this makes is heap for disk: the spill lands on the container's
// filesystem, so a burst of concurrent large uploads consumes disk instead of
// memory. That is the better failure by a wide margin. Disk exhaustion
// surfaces as a parse error on the one request that hit it, which this
// handler already answers with a 400, while the same bytes on the heap take
// the whole container past its memory limit and the kernel kills it, dropping
// every in-flight SSE stream in the process. These routes also authorize
// before reading, so the concurrency behind that disk usage is authenticated
// traffic, not anonymous.
const multipartMemoryBudget = 32 << 20

// parseUploadForm parses a multipart upload under multipartMemoryBudget.
// Every upload handler in this package goes through it so the budget cannot
// drift back to a per-call-site argument, and so the test that pins the
// budget's meaning exercises the same call the handlers make.
func parseUploadForm(r *http.Request) error {
	return r.ParseMultipartForm(multipartMemoryBudget)
}
