package sinks

import "context"

type Sink interface {
	Name() string
	Send(ctx context.Context, row map[string]any) error
}
