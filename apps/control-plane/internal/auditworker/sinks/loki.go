package sinks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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
					{lokiTimestamp(row), string(line)},
				},
			},
		},
	}
	return postJSON(ctx, s.cfg.HTTP, s.cfg.URL, body, nil)
}

// lokiTimestamp returns a Unix-nanosecond decimal string — the only timestamp
// format the Loki push API accepts. Prefer the event ts (column on
// audit_log) over time.Now() so disk-buffered drains preserve the original
// event time rather than the sink-receive time.
func lokiTimestamp(row map[string]any) string {
	if rawTs, ok := row["ts"]; ok {
		switch typed := rawTs.(type) {
		case string:
			if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
				return strconv.FormatInt(parsed.UnixNano(), 10)
			}
		case time.Time:
			return strconv.FormatInt(typed.UnixNano(), 10)
		}
	}
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}
