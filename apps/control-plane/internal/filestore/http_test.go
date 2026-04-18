package filestore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestInternalFileResponseIncludesStoragePath(t *testing.T) {
	resp := fileToResponse(File{
		ID:          "file-1",
		AccountID:   "acct-1",
		Purpose:     "batch",
		Filename:    "input.jsonl",
		Bytes:       128,
		Status:      "uploaded",
		StoragePath: "accounts/acct-1/files/file-1/input.jsonl",
		CreatedAt:   time.Unix(1700000000, 0),
	})

	assertJSONField(t, resp, "storage_path")
}

func TestInternalUploadResponseIncludesMultipartFields(t *testing.T) {
	s3UploadID := "s3-upload-1"
	resp := uploadToResponse(Upload{
		ID:          "upload-1",
		AccountID:   "acct-1",
		Filename:    "input.jsonl",
		Bytes:       128,
		MimeType:    "application/jsonl",
		Purpose:     "batch",
		Status:      "pending",
		S3UploadID:  &s3UploadID,
		StoragePath: "accounts/acct-1/uploads/upload-1/input.jsonl",
		CreatedAt:   time.Unix(1700000000, 0),
		ExpiresAt:   time.Unix(1700003600, 0),
	}, nil)

	for _, field := range []string{"s3_upload_id", "storage_path"} {
		assertJSONField(t, resp, field)
	}
}

func TestInternalBatchResponseIncludesOutputFieldsAndTimestamps(t *testing.T) {
	outputFileID := "file-output"
	errorFileID := "file-error"
	inProgressAt := time.Unix(1700000100, 0)
	completedAt := time.Unix(1700000200, 0)
	failedAt := time.Unix(1700000300, 0)
	cancelledAt := time.Unix(1700000400, 0)

	resp := batchToResponse(Batch{
		ID:                     "batch-1",
		AccountID:              "acct-1",
		InputFileID:            "file-input",
		OutputFileID:           &outputFileID,
		ErrorFileID:            &errorFileID,
		Endpoint:               "/v1/chat/completions",
		CompletionWindow:       "24h",
		Status:                 "completed",
		RequestCountsTotal:     3,
		RequestCountsCompleted: 2,
		RequestCountsFailed:    1,
		CreatedAt:              time.Unix(1700000000, 0),
		InProgressAt:           &inProgressAt,
		CompletedAt:            &completedAt,
		FailedAt:               &failedAt,
		CancelledAt:            &cancelledAt,
		ExpiresAt:              time.Unix(1700086400, 0),
	})

	for _, field := range []string{
		"output_file_id",
		"error_file_id",
		"in_progress_at",
		"completed_at",
		"failed_at",
		"cancelled_at",
	} {
		assertJSONField(t, resp, field)
	}
}

func assertJSONField(t *testing.T, value interface{}, field string) {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	needle := `"` + field + `"`
	if !strings.Contains(string(body), needle) {
		t.Fatalf("expected internal response JSON to include %s; got %s", field, string(body))
	}
}
