package sinks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

// NewSentry derives the ingest URL and public key from a Sentry DSN of the
// form `<scheme>://<public_key>@<host>[:port]/<project_id>` when URL and
// Key are not provided explicitly. The DSN is the only value Sentry hands
// out in its UI; posting JSON directly to the DSN is not a valid ingest
// endpoint and would silently fail. Explicit URL/Key still take
// precedence for installations that front Sentry with a relay.
func NewSentry(cfg SentryConfig) *Sentry {
	cfg.HTTP = httpClient(cfg.HTTP)
	if (cfg.URL == "" || cfg.Key == "") && cfg.DSN != "" {
		if u, key, err := parseSentryDSN(cfg.DSN); err == nil {
			if cfg.URL == "" {
				cfg.URL = u
			}
			if cfg.Key == "" {
				cfg.Key = key
			}
		}
	}
	return &Sentry{cfg: cfg}
}

func parseSentryDSN(dsn string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil {
		return "", "", err
	}
	if u.Scheme == "" || u.Host == "" || u.User == nil {
		return "", "", fmt.Errorf("sentry: dsn missing scheme/host/userinfo")
	}
	key := u.User.Username()
	if key == "" {
		return "", "", fmt.Errorf("sentry: dsn missing public key")
	}
	projectID := strings.Trim(u.Path, "/")
	if projectID == "" {
		return "", "", fmt.Errorf("sentry: dsn missing project id")
	}
	ingest := fmt.Sprintf("%s://%s/api/%s/store/", u.Scheme, u.Host, projectID)
	return ingest, key, nil
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
	if s.cfg.Key == "" {
		return errors.New("sentry: auth key required (provide explicitly or via DSN)")
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
		"X-Sentry-Auth": fmt.Sprintf(
			"Sentry sentry_version=7, sentry_key=%s, sentry_client=hive-auditworker/1.0",
			s.cfg.Key,
		),
	})
}
