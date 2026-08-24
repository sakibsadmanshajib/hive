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
	RouteID string
	// ModelName is provider_routes.litellm_model_name, the model_name key the
	// generator emits into config.yaml and the only string LiteLLM is
	// addressed by. Today nearly every row keeps it equal to route_id; rows
	// that diverge form route groups: N rows sharing one litellm_model_name
	// emit N deployments under one model_name, which LiteLLM's router
	// load-balances across with per-deployment cooldown.
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

// Sync queries provider routes, builds model entries for the active ones, and
// calls WriteAndRestart. Active routes are those with health_state in
// ('healthy', 'degraded') on an enabled provider. Rows with health_state
// 'disabled' or 'eol' produce no model_list entry so retired routes never
// receive live LiteLLM traffic, but their route_id is still reported to the
// merge via Config.KnownRouteIDs so an entry left over from when they were
// active can be removed.
func (s *SyncService) Sync(ctx context.Context) error {
	// Every provider_routes row, with activity computed rather than filtered in
	// the WHERE clause. One query, not two, so the active set and the full
	// route_id set cannot disagree about a row written between them.
	//
	// LEFT JOIN on provider_capabilities: a route with no capabilities row is
	// treated as non-embedding, which is the same adapter choice the generator
	// made before supports_embeddings was read at all.
	rows, err := s.pool.Query(ctx, `
		SELECT
			pr.route_id       AS route_id,
			pr.litellm_model_name AS model_name,
			pr.provider_model AS litellm_name,
			cp.base_url       AS base_url,
			cp.api_key_env    AS api_key_env,
			COALESCE(pc.supports_embeddings, false) AS supports_embeddings,
			(pr.health_state NOT IN ('disabled', 'eol') AND cp.enabled) AS active
		FROM public.provider_routes pr
		JOIN public.custom_providers cp ON cp.slug = pr.provider
		LEFT JOIN public.provider_capabilities pc ON pc.route_id = pr.route_id
		ORDER BY pr.route_id ASC
	`)
	if err != nil {
		return fmt.Errorf("litellmconfig: sync: query routes: %w", err)
	}
	defer rows.Close()

	var entries []ModelEntry
	var knownRouteIDs []string
	var knownGroupNames []string
	for rows.Next() {
		var r routeRow
		var active bool
		if err := rows.Scan(&r.RouteID, &r.ModelName, &r.LiteLLMName, &r.BaseURL, &r.APIKeyEnv, &r.SupportsEmbeddings, &active); err != nil {
			return fmt.Errorf("litellmconfig: sync: scan route: %w", err)
		}

		knownRouteIDs = append(knownRouteIDs, r.RouteID)
		// Every litellm_model_name in the table is a DB-owned gateway name even
		// when its row is inactive; without this set, a route GROUP whose
		// members all go inactive would leave its shared model_list entry
		// unreclaimable (generatedNames never contains it and neither does
		// KnownRouteIDs, which holds route_ids).
		if !knownGroupNamesContains(knownGroupNames, r.ModelName) {
			knownGroupNames = append(knownGroupNames, r.ModelName)
		}
		if !active {
			continue
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

	slog.Info("litellmconfig: sync: routes loaded", "active", len(entries), "total", len(knownRouteIDs))

	// ponytail: a zero-row result almost always means a query/schema defect
	// (issue #701) or every provider disabled at once, not an intentional
	// empty gateway. Refuse rather than write model_list: [] and restart
	// LiteLLM into serving nothing.
	if len(entries) == 0 {
		return fmt.Errorf("litellmconfig: sync: zero active routes; refusing to write an empty model_list")
	}

	cfg := Config{
		Models:          entries,
		KnownRouteIDs:   knownRouteIDs,
		KnownGroupNames: knownGroupNames,
		GeneralSettings: GeneralSettings{
			MasterKey: s.masterKey,
		},
		ExistingConfigPath: s.configPath,
	}

	return WriteAndRestart(ctx, s.configPath, cfg, s.restarter)
}

// knownGroupNamesContains reports whether names already holds name. Group
// members repeat one shared litellm_model_name across rows, so the list stays
// tiny; a linear scan beats a map for single-digit sizes.
func knownGroupNamesContains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
