package batchstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

type AsynqQueue struct {
	client *asynq.Client
}

func NewAsynqQueue(client *asynq.Client) *AsynqQueue {
	return &AsynqQueue{client: client}
}

func (q *AsynqQueue) Enqueue(ctx context.Context, payload BatchPollPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal batch poll payload: %w", err)
	}

	_, err = q.client.EnqueueContext(ctx, asynq.NewTask(TypeBatchPoll, body), asynq.Queue("batch"))
	if err != nil {
		return fmt.Errorf("enqueue batch poll task: %w", err)
	}
	return nil
}

// EnqueueExecute pushes a TypeBatchExecute task for the local executor.
func (q *AsynqQueue) EnqueueExecute(ctx context.Context, payload BatchExecutePayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal batch execute payload: %w", err)
	}

	_, err = q.client.EnqueueContext(ctx, asynq.NewTask(TypeBatchExecute, body), asynq.Queue("batch"))
	if err != nil {
		return fmt.Errorf("enqueue batch execute task: %w", err)
	}
	return nil
}
