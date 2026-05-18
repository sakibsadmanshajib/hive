package sinks

import (
	"context"
	"errors"
	"net/http"
)

type ELKConfig struct {
	URL    string
	APIKey string
	HTTP   *http.Client
}

type ELK struct {
	cfg ELKConfig
}

func NewELK(cfg ELKConfig) *ELK {
	cfg.HTTP = httpClient(cfg.HTTP)
	return &ELK{cfg: cfg}
}

func (s *ELK) Name() string { return "elk" }

func (s *ELK) Send(ctx context.Context, row map[string]any) error {
	if s.cfg.URL == "" {
		return errors.New("elk: url required")
	}
	return postJSON(ctx, s.cfg.HTTP, s.cfg.URL, row, map[string]string{
		"Authorization": "ApiKey " + s.cfg.APIKey,
	})
}
