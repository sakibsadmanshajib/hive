package batchstore

// TypeBatchPoll is the Asynq task type name for batch polling tasks.
const TypeBatchPoll = "batch:poll"

// BatchPollPayload is the JSON-serialized payload of a TypeBatchPoll task.
type BatchPollPayload struct {
	BatchID          string `json:"batch_id"`
	AccountID        string `json:"account_id"`
	ReservationID    string `json:"reservation_id"`
	UpstreamBatchID  string `json:"upstream_batch_id"`
	Provider         string `json:"provider"`
	InputFileID      string `json:"input_file_id"`
	Endpoint         string `json:"endpoint"`
	APIKeyID         string `json:"api_key_id,omitempty"`
	ModelAlias       string `json:"model_alias,omitempty"`
	EstimatedCredits int64  `json:"estimated_credits,omitempty"`
	ActualCredits    int64  `json:"actual_credits,omitempty"`
}
