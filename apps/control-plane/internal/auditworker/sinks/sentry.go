package sinks

import (
	"context"
	"errors"
	"net/http"
)

type SentryConfig struct {
	URL  string
	DSN  string
	Key  string
	HTTP *http.Client
}

type Sentry struct {
	cfg SentryConfig
}

func NewSentry(cfg SentryConfig) *Sentry {
	cfg.HTTP = httpClient(cfg.HTTP)
	if cfg.URL == "" {
		cfg.URL = cfg.DSN
	}
	return &Sentry{cfg: cfg}
}

func (s *Sentry) Name() string { return "sentry" }

func (s *Sentry) Send(ctx context.Context, row map[string]any) error {
	severity, _ := row["severity"].(string)
	if severity != "ERROR" && severity != "CRITICAL" {
		return nil
	}
	if s.cfg.URL == "" {
		return errors.New("sentry: url required")
	}
	// Allow-list strictly. The full audit row contains user_agent, actor_id,
	// source_ip, and after_json, which Sentry would surface to every project
	// member through its event-search UI. We forward only fields needed to
	// correlate to logs.
	extra := map[string]any{}
	for _, key := range []string{"request_id", "action", "severity", "env", "deploy_sha"} {
		if v, ok := row[key]; ok {
			extra[key] = v
		}
	}
	body := map[string]any{
		"level":   severity,
		"message": row["action"],
		"extra":   extra,
	}
	return postJSON(ctx, s.cfg.HTTP, s.cfg.URL, body, map[string]string{
		"Authorization": "Sentry sentry_key=" + s.cfg.Key,
	})
}
