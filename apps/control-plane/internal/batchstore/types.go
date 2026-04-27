package batchstore

// TypeBatchPoll is the Asynq task type name for batch polling tasks (upstream
// LiteLLM-supported provider path).
const TypeBatchPoll = "batch:poll"

// TypeBatchExecute is the Asynq task type name for the local executor path —
// the batch is processed line-by-line by control-plane against LiteLLM's
// chat-completions endpoint instead of LiteLLM's batch upload API. See
// .planning/phases/15-batch-local-executor/PLAN.md.
const TypeBatchExecute = "batch:execute"

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

// BatchExecutePayload carries the minimal context needed for the local
// executor to claim a batch and process it. Account/reservation lookup is
// performed by the executor against the persisted batch row.
type BatchExecutePayload struct {
	BatchID   string `json:"batch_id"`
	AccountID string `json:"account_id"`
}
