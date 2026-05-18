package sinks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type LokiConfig struct {
	URL  string
	HTTP *http.Client
}

type Loki struct {
	cfg LokiConfig
}

func NewLoki(cfg LokiConfig) *Loki {
	cfg.HTTP = httpClient(cfg.HTTP)
	return &Loki{cfg: cfg}
}

func (s *Loki) Name() string { return "loki" }

func (s *Loki) Send(ctx context.Context, row map[string]any) error {
	if s.cfg.URL == "" {
		return errors.New("loki: url required")
	}
	action, _ := row["action"].(string)
	severity, _ := row["severity"].(string)
	line, err := json.Marshal(row)
	if err != nil {
		return err
	}
	body := map[string]any{
		"streams": []map[string]any{
			{
				"stream": map[string]string{
					"action":   action,
					"severity": severity,
					"service":  "hive",
				},
				"values": [][]string{
					{time.Now().Format("20060102150405"), string(line)},
				},
			},
		},
	}
	return postJSON(ctx, s.cfg.HTTP, s.cfg.URL, body, nil)
}
