package batches

// BatchRequest is the JSON body for POST /v1/batches.
type BatchRequest struct {
	InputFileID      string            `json:"input_file_id"`
	Endpoint         string            `json:"endpoint"`
	CompletionWindow string            `json:"completion_window"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// BatchObject is the OpenAI-compatible representation of a batch job.
type BatchObject struct {
	ID               string              `json:"id"`
	Object           string              `json:"object"` // always "batch"
	Endpoint         string              `json:"endpoint"`
	Errors           *BatchErrors        `json:"errors,omitempty"`
	InputFileID      string              `json:"input_file_id"`
	CompletionWindow string              `json:"completion_window"`
	Status           string              `json:"status"`
	OutputFileID     *string             `json:"output_file_id,omitempty"`
	ErrorFileID      *string             `json:"error_file_id,omitempty"`
	CreatedAt        int64               `json:"created_at"`
	InProgressAt     *int64              `json:"in_progress_at,omitempty"`
	ExpiresAt        *int64              `json:"expires_at,omitempty"`
	FinalizingAt     *int64              `json:"finalizing_at,omitempty"`
	CompletedAt      *int64              `json:"completed_at,omitempty"`
	FailedAt         *int64              `json:"failed_at,omitempty"`
	ExpiredAt        *int64              `json:"expired_at,omitempty"`
	CancellingAt     *int64              `json:"cancelling_at,omitempty"`
	CancelledAt      *int64              `json:"cancelled_at,omitempty"`
	RequestCounts    *BatchRequestCounts `json:"request_counts,omitempty"`
	Metadata         map[string]string   `json:"metadata,omitempty"`
}

// BatchErrors is the error list embedded in a BatchObject.
type BatchErrors struct {
	Object string       `json:"object"` // "list"
	Data   []BatchError `json:"data"`
}

// BatchError is a single error detail within a batch.
type BatchError struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Param   *string `json:"param,omitempty"`
	Line    *int    `json:"line,omitempty"`
}

// BatchRequestCounts tracks completion progress of a batch job.
type BatchRequestCounts struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

// BatchListResponse is the OpenAI-compatible list response for batches.
type BatchListResponse struct {
	Object  string        `json:"object"` // "list"
	Data    []BatchObject `json:"data"`
	FirstID *string       `json:"first_id,omitempty"`
	LastID  *string       `json:"last_id,omitempty"`
	HasMore bool          `json:"has_more"`
}

// ValidBatchEndpoints lists the allowed endpoint values for batch creation.
var ValidBatchEndpoints = map[string]bool{
	"/v1/chat/completions": true,
	"/v1/completions":      true,
	"/v1/embeddings":       true,
}

// BatchInputLine is a single line of a JSONL batch input file.
type BatchInputLine struct {
	CustomID string                 `json:"custom_id"`
	Method   string                 `json:"method"`
	URL      string                 `json:"url"`
	Body     map[string]interface{} `json:"body"`
}
