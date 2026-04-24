package files

import (
	"errors"

	sharedstorage "github.com/hivegpt/hive/packages/storage"
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
