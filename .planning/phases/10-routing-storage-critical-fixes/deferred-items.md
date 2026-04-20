# Deferred Items

## Plan 10-03

- `apps/control-plane/internal/filestore/http_test.go` still has expected RED failures for internal `storage_path`, `s3_upload_id`, `output_file_id`, and timestamp response fields. These are owned by Plan 10-06 per `10-02-SUMMARY.md`.
- `apps/control-plane/internal/filestore/repository_test.go::TestUpdateBatchStatusPersistsAllowedFields` still has the expected RED failure for persisting allowed batch update fields. This is owned by Plan 10-06 per `10-02-SUMMARY.md`.
