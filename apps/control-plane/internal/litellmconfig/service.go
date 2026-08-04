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

// routeRow is a join of provider_routes, custom_providers and
// provider_capabilities for active routes.
type routeRow struct {
	ModelName   string
	LiteLLMName string // provider_routes.provider_model — already carries its provider prefix, e.g. "openrouter/openai/gpt-4o-mini"
	BaseURL     string
	APIKeyEnv   string
	// SupportsEmbeddings selects the LiteLLM adapter for this route; see
	// litellmModel in generator.go.
	SupportsEmbeddings bool
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
// WriteAndRestart. Active routes are those with health_state in ('healthy',
// 'degraded'). Rows with health_state 'disabled' or 'eol' are excluded so
// retired routes never receive live LiteLLM traffic.
func (s *SyncService) Sync(ctx context.Context) error {
	// LEFT JOIN on provider_capabilities: a route with no capabilities row is
	// treated as non-embedding, which is the same adapter choice the generator
	// made before supports_embeddings was read at all.
	rows, err := s.pool.Query(ctx, `
		SELECT
			pr.route_id       AS model_name,
			pr.provider_model AS litellm_name,
			cp.base_url       AS base_url,
			cp.api_key_env    AS api_key_env,
			COALESCE(pc.supports_embeddings, false) AS supports_embeddings
		FROM public.provider_routes pr
		JOIN public.custom_providers cp ON cp.slug = pr.provider
		LEFT JOIN public.provider_capabilities pc ON pc.route_id = pr.route_id
		WHERE pr.health_state NOT IN ('disabled', 'eol')
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
		if err := rows.Scan(&r.ModelName, &r.LiteLLMName, &r.BaseURL, &r.APIKeyEnv, &r.SupportsEmbeddings); err != nil {
			return fmt.Errorf("litellmconfig: sync: scan route: %w", err)
		}

		// provider_routes.provider_model is stored pre-prefixed for every
		// route seeded so far (e.g. "openrouter/openai/gpt-4o-mini",
		// "groq/llama-3.3-70b-versatile" — see supabase/migrations/
		// 20260331_02_routing_policy.sql and siblings). custom_providers.
		// litellm_prefix is NOT concatenated here: doing so would double the
		// prefix ("openrouter/openrouter/..."), since every provider_model
		// value already carries it.
		entries = append(entries, ModelEntry{
			ModelName:          r.ModelName,
			LiteLLMName:        r.LiteLLMName,
			APIBase:            r.BaseURL,
			APIKeyEnv:          r.APIKeyEnv,
			SupportsEmbeddings: r.SupportsEmbeddings,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("litellmconfig: sync: rows error: %w", err)
	}

	slog.Info("litellmconfig: sync: active routes loaded", "count", len(entries))

	// ponytail: a zero-row result almost always means a query/schema defect
	// (issue #701) or every provider disabled at once, not an intentional
	// empty gateway. Refuse rather than write model_list: [] and restart
	// LiteLLM into serving nothing.
	if len(entries) == 0 {
		return fmt.Errorf("litellmconfig: sync: zero active routes; refusing to write an empty model_list")
	}

	cfg := Config{
		Models: entries,
		GeneralSettings: GeneralSettings{
			MasterKey: s.masterKey,
		},
		ExistingConfigPath: s.configPath,
	}

	return WriteAndRestart(ctx, s.configPath, cfg, s.restarter)
}
