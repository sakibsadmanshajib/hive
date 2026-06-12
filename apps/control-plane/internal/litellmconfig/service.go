package litellmconfig

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SyncRunner is the interface consumed by the HTTP handler.
type SyncRunner interface {
	Sync(ctx context.Context) error
}

// routeRow is a join of provider_routes and custom_providers for active routes.
type routeRow struct {
	ModelName     string
	LiteLLMPrefix string
	LiteLLMName   string // provider_routes.model_id — the upstream model identifier
	BaseURL       string
	APIKeyEnv     string
}

// SyncService queries the DB for active provider routes, generates LiteLLM
// YAML, writes it atomically, and triggers a container restart.
type SyncService struct {
	pool       *pgxpool.Pool
	configPath string
	masterKey  string
	restarter  Restarter
}

// NewSyncService returns a SyncService wired with the given dependencies.
func NewSyncService(pool *pgxpool.Pool, configPath, masterKey string, restarter Restarter) *SyncService {
	return &SyncService{
		pool:       pool,
		configPath: configPath,
		masterKey:  masterKey,
		restarter:  restarter,
	}
}

// Sync queries active provider routes, builds model entries, and calls
// WriteAndRestart. Active routes are those with health_state NOT equal to
// 'disabled'. Routes with 'healthy' or 'degraded' health_state are included.
func (s *SyncService) Sync(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `
		SELECT
			pr.route_id        AS model_name,
			cp.litellm_prefix  AS litellm_prefix,
			pr.model_id        AS litellm_name,
			cp.base_url        AS base_url,
			cp.api_key_env     AS api_key_env
		FROM public.provider_routes pr
		JOIN public.custom_providers cp ON cp.id = pr.provider_id
		WHERE pr.health_state != 'disabled'
		  AND cp.enabled = true
		ORDER BY pr.route_id ASC
	`)
	if err != nil {
		return fmt.Errorf("litellmconfig: sync: query routes: %w", err)
	}
	defer rows.Close()

	var entries []ModelEntry
	for rows.Next() {
		var r routeRow
		if err := rows.Scan(&r.ModelName, &r.LiteLLMPrefix, &r.LiteLLMName, &r.BaseURL, &r.APIKeyEnv); err != nil {
			return fmt.Errorf("litellmconfig: sync: scan route: %w", err)
		}

		litellmModel := r.LiteLLMName
		if r.LiteLLMPrefix != "" {
			litellmModel = r.LiteLLMPrefix + r.LiteLLMName
		}

		entries = append(entries, ModelEntry{
			ModelName:   r.ModelName,
			LiteLLMName: litellmModel,
			APIBase:     r.BaseURL,
			APIKeyEnv:   r.APIKeyEnv,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("litellmconfig: sync: rows error: %w", err)
	}

	slog.Info("litellmconfig: sync: active routes loaded", "count", len(entries))

	cfg := Config{
		Models: entries,
		GeneralSettings: GeneralSettings{
			MasterKey: s.masterKey,
		},
		ExistingConfigPath: s.configPath,
	}

	return WriteAndRestart(ctx, s.configPath, cfg, s.restarter)
}
