package sinks

import (
	"context"
	"errors"
	"net/http"
)

type SplunkConfig struct {
	URL   string
	Token string
	HTTP  *http.Client
}

type Splunk struct {
	cfg SplunkConfig
}

func NewSplunk(cfg SplunkConfig) *Splunk {
	cfg.HTTP = httpClient(cfg.HTTP)
	return &Splunk{cfg: cfg}
}

func (s *Splunk) Name() string { return "splunk" }

func (s *Splunk) Send(ctx context.Context, row map[string]any) error {
	if s.cfg.URL == "" {
		return errors.New("splunk: url required")
	}
	body := map[string]any{
		"event":      row,
		"sourcetype": "hive:audit",
	}
	return postJSON(ctx, s.cfg.HTTP, s.cfg.URL, body, map[string]string{
		"Authorization": "Splunk " + s.cfg.Token,
	})
}
