package filestore

import "time"

// File represents a stored file's metadata in Postgres.
type File struct {
	ID          string
	AccountID   string
	Purpose     string
	Filename    string
	Bytes       int64
	Status      string // "uploaded", "processed", "error"
	StoragePath string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
}

// Upload represents an in-progress multipart upload's metadata in Postgres.
type Upload struct {
	ID          string
	AccountID   string
	Filename    string
	Bytes       int64
	MimeType    string
	Purpose     string
	Status      string // "pending", "completed", "cancelled"
	S3UploadID  *string
	StoragePath string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// UploadPart represents a single part of a multipart upload.
type UploadPart struct {
	ID        string
	UploadID  string
	PartNum   int
	ETag      string
	CreatedAt time.Time
}

// Batch represents an async batch job in Postgres.
type Batch struct {
	ID                      string
	AccountID               string
	InputFileID             string
	OutputFileID            *string
	ErrorFileID             *string
	Endpoint                string
	CompletionWindow        string
	Status                  string // "validating","in_progress","finalizing","completed","failed","cancelled","expired"
	Provider                string
	UpstreamBatchID         *string
	ReservationID           *string
	RequestCountsTotal      int
	RequestCountsCompleted  int
	RequestCountsFailed     int
	CreatedAt               time.Time
	InProgressAt            *time.Time
	CompletedAt             *time.Time
	FailedAt                *time.Time
	CancelledAt             *time.Time
	ExpiresAt               time.Time
}
