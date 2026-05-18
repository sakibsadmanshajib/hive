package sinks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type DatadogConfig struct {
	URL    string
	APIKey string
	Site   string
	HTTP   *http.Client
}

type Datadog struct {
	cfg DatadogConfig
}

func NewDatadog(cfg DatadogConfig) *Datadog {
	cfg.HTTP = httpClient(cfg.HTTP)
	if cfg.Site == "" {
		cfg.Site = "datadoghq.com"
	}
	if cfg.URL == "" && cfg.APIKey != "" {
		cfg.URL = fmt.Sprintf("https://http-intake.logs.%s/api/v2/logs", cfg.Site)
	}
	return &Datadog{cfg: cfg}
}

func (s *Datadog) Name() string { return "datadog" }

func (s *Datadog) Send(ctx context.Context, row map[string]any) error {
	if s.cfg.URL == "" {
		return errors.New("datadog: url required")
	}
	return postJSON(ctx, s.cfg.HTTP, s.cfg.URL, []map[string]any{row}, map[string]string{
		"DD-API-KEY": s.cfg.APIKey,
	})
}
