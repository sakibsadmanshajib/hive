package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/docs"
	edgeagentsched "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/agentsched"
	edgeagenttask "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/agenttask"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/artifacts"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/audio"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/batches"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/catalog"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/featuregate"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/files"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/images"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/limits"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/matrix"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/middleware"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/proxy"
	edgerag "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/rag"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/sovereign"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/stt"
	"github.com/sakibsadmanshajib/hive/packages/embedmodel"
	"github.com/sakibsadmanshajib/hive/packages/storage"
)

// jwtAuthEnv collects the Supabase JWT validator configuration sourced
// from the runtime environment. All three values are required so the
// edge-api fails fast when JWT routing is mis-deployed.
type jwtAuthEnv struct {
	Issuer   string
	Audience string
	JWKSURL  string
	// CAFile is optional and names the PEM certificate authority to trust
	// for the JWKS fetch. When set it REPLACES the system roots for that
	// one fetch, so it must not be set on a JWKS host whose certificate is
	// publicly trusted: that would break the fetch rather than harden it.
	// The self-hosted (enterprise) profile serves its JWKS through an
	// in-stack TLS terminator using a private authority, which is the case
	// this exists for.
	CAFile string
}

type storageConfig struct {
	Endpoint     string
	AccessKey    string
	SecretKey    string
	Region       string
	FilesBucket  string
	ImagesBucket string
}

func main() {
	// Sovereign-mode guard: fail fast before any service wiring when external
	// provider keys are present. See apps/edge-api/internal/sovereign for tests.
	if err := sovereign.Check(os.Getenv); err != nil {
		log.Fatal(err)
	}

	// GOMEMLIMIT and the container's own memory limit are set in two different
	// places and nothing keeps them consistent. Say so at boot when they cannot
	// do their job together (issue #1299).
	logMemoryLimit(log.Printf)

	// Root context cancels on SIGINT/SIGTERM so background goroutines
	// rooted here (notably the jwx JWKS auto-refresher) exit cleanly
	// instead of leaking through process shutdown — passing
	// context.Background() to NewSupabaseJWTValidator would orphan
	// the refresh loop.
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	matrixPath := resolveMatrixPath()
	specPath := resolveSpecPath()

	// Load support matrix
	m, err := matrix.LoadMatrix(matrixPath)
	if err != nil {
		log.Fatalf("failed to load support matrix: %v", err)
	}
	log.Printf("Loaded support matrix: %d endpoints", len(m.Endpoints))

	cpBaseURL := resolveControlPlaneBaseURL()
	catalogClient := catalog.NewClient(cpBaseURL)

	// Issue #238 — per-tenant feature gate. Lazy-resolves flags from control-plane
	// with a 30 s in-memory cache per tenant (end-to-end revocation < 60 s).
	// RAG/voice/relay/cowork route handlers (issues #232-235) call featureGate.Require(...)
	// to wrap their http.Handler before registration on the mux.
	// ponytail: featureGate used by RAG/voice/relay/cowork handlers (#232-235).
	// Kept here so the gate is constructed once and shared across all feature routes.
	featureGate := featuregate.New(featuregate.Config{
		ControlPlaneURL: cpBaseURL,
		TTL:             30 * time.Second,
	})

	dbPool := openOptionalDBPool(rootCtx)
	if dbPool != nil {
		defer dbPool.Close()
	}

	// Initialize authz
	authzClient, err := authz.NewClient(resolveControlPlaneBaseURL(), resolveRedisURL())
	if err != nil {
		log.Fatalf("failed to initialize authz client: %v", err)
	}
	limiter, err := authz.NewLimiter(resolveRedisURL())
	if err != nil {
		log.Fatalf("failed to initialize authz limiter: %v", err)
	}
	failOpen := resolveRateLimitFailOpen()
	if failOpen {
		log.Printf("authz: WARNING rate limiter is in FAIL-OPEN mode (RATE_LIMIT_FAIL_OPEN) — Redis outages will NOT enforce limits; do not use in production")
	}
	authorizer := authz.NewAuthorizer(authzClient, limiter, authz.WithFailOpen(failOpen))

	// Open WebUI authenticates its own upstream calls with OWUI_SHIM_KEY alone
	// (model listing, document-RAG embeddings, text-to-speech). All three fail
	// silently or with a misleading invalid-key error when that key does not
	// resolve, so probe it here and keep probing. See watchOWUIShimKey.
	go watchOWUIShimKey(rootCtx, authzClient, os.Getenv("OWUI_SHIM_KEY"), owuiShimKeyProbeInterval)

	// Initialize Prometheus metrics registry for edge-api.
	edgeMetrics, promRegistry := proxy.NewEdgeMetrics()

	// Create the main mux. routeRecorder (route_recorder.go) records every
	// pattern registered through it, so the boot-time assertMatrixCoverage
	// call below can catch a route shipped with zero support-matrix.json
	// coverage, without a hand-kept parallel list of routes.
	mux := newRouteRecorder()

	// Infrastructure routes (no unsupported middleware). /metrics is not among
	// them; it is served on metricsListenAddr instead.
	registerInfraRoutes(mux, specPath, authzClient.Degraded)

	// Inference routes
	routingClient := inference.NewRoutingClient(resolveControlPlaneBaseURL())
	accountingClient := inference.NewAccountingClient(resolveControlPlaneBaseURL())
	litellmClient := inference.NewLiteLLMClient(resolveLiteLLMBaseURL(), resolveLiteLLMMasterKey())
	orchestrator := inference.NewOrchestrator(authorizer, routingClient, accountingClient, litellmClient).
		WithStageMetrics(inference.NewStageMetrics(promRegistry))
	inferenceHandler := inference.NewHandler(orchestrator)
	chatDispatchHandler := chat.NewDispatch(chat.Deps{
		Pool:    dbPool,
		Routing: routingClient,
		// Session chat settles through the same control-plane accounting the
		// API-key path uses (#746). Without these two the handler refuses
		// every request: serving inference that cannot be charged is the
		// defect this wiring closes, not a degraded mode to fall back to.
		Accounting: accountingClient,
		Billing:    &metering.PGBillingAccountResolver{Pool: dbPool},
		// Cross-chat recall (#172, D-020): reads public.user_memories under
		// RLS. Injection degrades to no block until the migration is applied.
		Memories:   chat.NewMemorySource(dbPool),
		LiteLLMURL: resolveLiteLLMBaseURL(),
		LiteLLMKey: resolveLiteLLMMasterKey(),
		DeploySHA:  os.Getenv("DEPLOY_SHA"),
		Env:        os.Getenv("HIVE_ENV"),
	})

	openAIChatHandler := jwtAwareChatHandler(chatDispatchHandler, inferenceHandler)
	mux.Handle("/v1/chat/completions", openAIChatHandler)
	mux.Handle("/v1/completions", inferenceHandler)
	mux.Handle("/v1/responses", inferenceHandler)
	mux.Handle("/v1/embeddings", inferenceHandler)

	// Anthropic Messages surface: POST /v1/messages and POST /v1/messages/count_tokens.
	// The APIKeyNormalizer rewrites x-api-key to Authorization: Bearer so the
	// standard auth.Selector routes hk_ keys to the API-key path and JWTs to
	// the JWT path, reusing the same auth wrappers as /v1/chat/completions.
	//
	// The handler translates and then delegates to the very handler registered
	// for /v1/chat/completions above, so alias resolution (per-tenant model
	// entitlement, the API-key alias allowlist, capability matching), credit
	// reservation and settlement, upstream retry, tracing and audit are shared
	// with that surface rather than reimplemented. It previously POSTed straight
	// to LiteLLM, which let a caller address a raw route id and skip all of it.
	anthropicHandler := anthropic.NewHandler(anthropic.Deps{
		OpenAIChat: openAIChatHandler,
		// count_tokens is the one route on this surface that does not delegate,
		// so it is the one route that needs its own API-key authority. Without
		// it the handler could only see a JWT session user and refused every
		// Anthropic SDK caller, which authenticates with an API key (#1261).
		// Zero cost arguments: the estimate is computed locally and bills
		// nothing, so this resolves and rate-limits the key without reserving
		// credit against it.
		AuthorizeAPIKey: func(ctx context.Context, authHeader string) (*apierrors.OpenAIError, map[string]string) {
			_, headers, authErr := authorizer.Authorize(ctx, authHeader, "", 0, 0, 0)
			return authErr, headers
		},
	})
	mux.Handle("/v1/messages", anthropic.APIKeyNormalizer(anthropicHandler))
	mux.Handle("/v1/messages/", anthropic.APIKeyNormalizer(anthropicHandler))

	storageCfg, err := loadStorageConfigFromEnv()
	if err != nil {
		log.Fatalf("storage unavailable: %v", err)
	}
	storageClient, err := storage.NewS3Client(storage.Config{
		Endpoint:  storageCfg.Endpoint,
		AccessKey: storageCfg.AccessKey,
		SecretKey: storageCfg.SecretKey,
		Region:    storageCfg.Region,
	})
	if err != nil {
		log.Fatalf("storage unavailable: %v", err)
	}

	imagesAuthorizer := images.NewAuthorizerAdapter(authorizer)
	imagesRouting := images.NewRoutingAdapter(routingClient)
	imagesAccounting := images.NewAccountingAdapter(accountingClient)
	imagesHandler := images.NewHandler(
		imagesAuthorizer,
		imagesRouting,
		imagesAccounting,
		resolveLiteLLMBaseURL(),
		resolveLiteLLMMasterKey(),
		storageClient,
		storageCfg.ImagesBucket,
	)

	audioAuthorizer := audio.NewAuthorizerAdapter(authorizer)
	audioRouting := audio.NewRoutingAdapter(routingClient)
	audioAccounting := audio.NewAccountingAdapter(accountingClient)
	audioHandler := audio.NewHandler(
		audioAuthorizer,
		audioRouting,
		audioAccounting,
		resolveLiteLLMBaseURL(),
		resolveLiteLLMMasterKey(),
	)
	if parakeetURL, fwURL := os.Getenv("PARAKEET_BASE_URL"), os.Getenv("FASTER_WHISPER_BASE_URL"); parakeetURL != "" || fwURL != "" {
		audioHandler.WithSTT(stt.NewTieredClient(stt.Config{
			ParakeetBaseURL:      parakeetURL,
			ParakeetAPIKey:       os.Getenv("PARAKEET_API_KEY"),
			FasterWhisperBaseURL: fwURL,
			FasterWhisperAPIKey:  os.Getenv("FASTER_WHISPER_API_KEY"),
		}))
		log.Printf("voice STT enabled: parakeet=%q faster-whisper=%q", parakeetURL, fwURL)
	}

	filestoreClient := files.NewFilestoreClient(resolveControlPlaneBaseURL())
	filesAuthorizer := files.NewAuthorizerAdapter(authorizer)
	filesHandler := files.NewHandler(filesAuthorizer, storageClient, filestoreClient, storageCfg.FilesBucket)

	batchClient := batches.NewBatchClient(resolveControlPlaneBaseURL())
	batchesAuthorizer := batches.NewAuthorizerAdapter(authorizer)
	batchesFileClient := batches.NewFilestoreAdapter(filestoreClient)
	batchesAccounting := batches.NewAccountingAdapter(accountingClient)
	batchesHandler := batches.NewHandler(batchesAuthorizer, batchClient, batchesFileClient, storageClient, batchesAccounting, storageCfg.FilesBucket)

	// Issue #293: Voice previously had a gate constant with no enforcing route
	// check. Wire it here, at the one real route it protects.
	//
	// voiceGateForAPIKeys below narrows that gate to JWT-session callers only.
	// Unlike RAG and Cowork (deliberately JWT-only sovereign-workspace
	// features per the comments on their wiring above), /v1/audio/* is core
	// OpenAI-contract surface -- the same category as /v1/chat/completions,
	// /v1/embeddings, and /v1/images/*, none of which carry a tenant feature
	// gate. featuregate.Gate.Require reads auth.UserFrom, which is only ever
	// populated by the JWT middleware (auth.Selector sends "Bearer hk_..."
	// requests straight past it, see internal/auth/selector.go); wrapping an
	// hk_-authenticated route in Require therefore denied every API-key
	// caller unconditionally, regardless of routing/catalog config or the
	// tenant's actual ENABLE_VOICE setting. The audio.Handler already does
	// its own API-key authorization (audio.Authorizer), so hk_ callers are
	// let through here and JWT/OWUI callers alone are still subject to the
	// tenant flag.
	voiceMW := voiceGateForAPIKeys(featureGate.Require(featuregate.FeatureVoice))
	registerMediaFileBatchRoutes(mux, imagesHandler, audioHandler, filesHandler, batchesHandler, voiceMW)

	// Issue #997: Open WebUI's voice dropdowns fetch GET /v1/audio/voices with
	// no Authorization header at all, so this route is registered without the
	// hk_-key authorizer or the tenant voice gate. Serving the provider's real
	// roster here is what keeps Open WebUI's get_available_voices from falling
	// back to its hardcoded alloy-style list (#996); gating it would silently
	// reinstate that fallback. See audio.VoicesHandler for the full rationale.
	registerAudioVoicesRoute(mux)

	log.Printf("S3 storage enabled: images=%s, files=%s", storageCfg.ImagesBucket, storageCfg.FilesBucket)

	// RAG routes (#232): always registered behind FeatureRAG so the gate returns
	// 403 (not 404) regardless of whether the embedding backend is configured.
	// When EMBEDDING_BASE_URL is unset the handler itself returns a provider-blind 503.
	{
		ragEmbedBaseURL := strings.TrimSpace(os.Getenv("EMBEDDING_BASE_URL"))
		ragEmbedModel := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL"))
		if ragEmbedModel == "" {
			ragEmbedModel = "bge-m3"
		}
		// EMBEDDING_DIM: the vector width the admin-selected model produces.
		// Overwrites the rag package's default (1024, matching today's
		// rag_chunks.embedding column) so the dimension checks in embed.go
		// compare against config, not a compile-time constant.
		ragEmbedDimRaw := strings.TrimSpace(os.Getenv("EMBEDDING_DIM"))
		ragEmbedDim := edgerag.EmbeddingDimension
		if ragEmbedDimRaw != "" {
			if v, err := strconv.Atoi(ragEmbedDimRaw); err != nil {
				log.Printf("WARNING: EMBEDDING_DIM=%q is not a valid integer; falling back to default %d", ragEmbedDimRaw, ragEmbedDim)
			} else {
				ragEmbedDim = v
			}
		}
		edgerag.EmbeddingDimension = ragEmbedDim

		// EMBEDDING_ALLOW_UNINDEXED opts a >4000-dim configuration into an
		// unindexed brute-force column (small-corpus only). Off by default.
		ragAllowUnindexedRaw := strings.TrimSpace(os.Getenv("EMBEDDING_ALLOW_UNINDEXED"))
		ragEmbedAllowUnindexed := ragAllowUnindexedRaw == "1" || strings.EqualFold(ragAllowUnindexedRaw, "true")

		// embedmodel.Resolve is the single source of truth (#368): it enforces
		// the selectable policy (no naive truncation of a non-MRL model, no dim
		// wider than native, dimension -> pgvector (type, opclass)) and derives
		// the MRL reduction target (plan.ReduceTo). There is no independent
		// EMBEDDING_TRUNCATE_TO knob any more: whether and how far to reduce is
		// derived from (model MRL?, chosen dim vs native). An unknown model
		// still resolves its pgvector mapping by dimension. A rejected
		// combination disables the embedding backend the same way an unset
		// EMBEDDING_BASE_URL does below (route stays registered, provider-blind
		// 503), rather than serving vectors from a misconfigured width.
		ragEmbedReduceTo := 0
		// pgvector column type the query vector must be cast to in SearchChunks
		// ("vector"/"halfvec"). Derived from the resolved Plan (dim -> type via
		// embedmodel.ResolvePgvector), matching what provisioning built the
		// column as. Defaults to "vector" (shipped column) when RAG is disabled.
		ragPgType := "vector"
		if ragEmbedBaseURL != "" {
			plan, err := embedmodel.Resolve(ragEmbedModel, ragEmbedDim, ragEmbedAllowUnindexed)
			if err != nil {
				log.Printf("ERROR: RAG embedding configuration is inconsistent, disabling /v1/rag/* embedding: %v", err)
				ragEmbedBaseURL = ""
			} else {
				ragEmbedReduceTo = plan.ReduceTo
				ragPgType = plan.PgType
				// Cross-service reconcile: compare against the active
				// rag_embedding_config row the live column was provisioned to.
				// A mismatch (different model or dim) means our query vectors
				// would hit a column built for a different embedding space, so
				// disable RAG rather than serve cross-space results.
				if dbPool != nil {
					if rowModel, rowDim, found, cerr := edgerag.LoadActiveEmbeddingConfig(context.Background(), dbPool); cerr != nil {
						log.Printf("WARNING: could not read rag_embedding_config, proceeding on env config only: %v", cerr)
					} else if found && !embedmodel.SameConfig(ragEmbedModel, ragEmbedDim, rowModel, rowDim) {
						log.Printf("ERROR: RAG embedding config mismatch (env model=%s dim=%d vs provisioned model=%s dim=%d), disabling /v1/rag/* embedding; provision + re-embed to switch models", ragEmbedModel, ragEmbedDim, rowModel, rowDim)
						ragEmbedBaseURL = ""
					}
				}
			}
		}

		var ragRepo edgerag.Store
		if dbPool != nil {
			ragRepo = edgerag.NewRepo(dbPool, ragPgType)
		}
		var ragEmbedder edgerag.Embedder
		if ragEmbedBaseURL != "" {
			ragEmbedder = edgerag.NewHTTPEmbedder(ragEmbedBaseURL, ragEmbedModel, ragEmbedReduceTo, resolveLiteLLMMasterKey())
		}
		ragAudit := func(ctx context.Context, action, resourceType, resourceID, severity string,
			tenantID, actorID uuid.UUID, userAgent string, after any) {
			if dbPool == nil {
				return
			}
			if err := chat.InsertAuditEvent(ctx, dbPool, chat.AuditEvent{
				TenantID:     tenantID,
				ActorID:      actorID,
				Action:       action,
				Severity:     severity,
				ResourceType: resourceType,
				ResourceID:   resourceID,
				UserAgent:    userAgent,
				After:        after,
				DeploySHA:    os.Getenv("DEPLOY_SHA"),
				Environment:  os.Getenv("HIVE_ENV"),
			}); err != nil {
				log.Printf("rag audit write failed: %v", err)
			}
		}
		// Ingestion (chunk + embed + store) runs in control-plane, not here --
		// edge-api calls the internal service-to-service endpoint the same way
		// filestoreClient calls control-plane for file metadata (issue #232).
		ragIngestClient := edgerag.NewIngestClient(resolveControlPlaneBaseURL())
		ragIngest := ragIngestClient.AsIngestFunc(log.Printf)

		// Grounded generation (#325): POST /v1/rag/chat reuses the same
		// routingClient + litellmClient already wired above for
		// /v1/chat/completions. ragSelectRoute adapts inference.RoutingClient
		// to the rag package's decoupled RouteSelectFunc so rag does not need
		// to import inference's Orchestrator/billing types; litellmClient's
		// ChatCompletion method value satisfies ChatDispatchFunc directly.
		//
		// Deliberately NOT wired through inference.Orchestrator: this route
		// is JWT-session authenticated (auth.UserFrom, same as every other
		// /v1/rag/* endpoint), and Orchestrator.Authorize only resolves
		// "hk_..." API keys (internal/authz/authorizer.go) — calling it here
		// would 401 every legitimate RAG request. The BudgetGate wrapped
		// around this whole mux below has the identical limitation today
		// (see the "Phase 19"/Plan 03 comment further down: JWT traffic is
		// pre-billing by design, tracked separately, and affects every
		// JWT-session inference route, not just this one). RAG chat records
		// its own usage-accounting signal instead (RAG_CHAT_COMPLETED audit
		// event in chat_handler.go), matching what internal/chat/dispatch.go
		// already does (an llm_traces row) for the equivalent JWT-session
		// /v1/chat/completions path.
		ragSelectRoute := func(ctx context.Context, aliasID string) (string, error) {
			route, err := routingClient.SelectRoute(ctx, inference.SelectRouteInput{
				AliasID:             aliasID,
				NeedChatCompletions: true,
			})
			if err != nil {
				if errors.Is(err, inference.ErrRouteNotFound) {
					return "", edgerag.ErrRouteNotFound
				}
				if errors.Is(err, inference.ErrModelNotEntitled) {
					return "", edgerag.ErrModelNotEntitled
				}
				return "", err
			}
			return route.LiteLLMModelName, nil
		}

		ragHandler := edgerag.NewHandler(ragRepo, ragEmbedder, ragAudit, ragIngest, rootCtx).
			WithChat(ragSelectRoute, litellmClient.ChatCompletion).
			// Binary document ingest: convert PDF/DOCX/etc to markdown via the
			// pinned markitdown sidecar before chunk + embed. MARKITDOWN_URL
			// defaults to the compose service DNS name; without the sidecar
			// reachable, binary uploads fail loud (503/422) while raw-text
			// uploads are unaffected.
			WithConverter(edgerag.NewMarkitdownClient(resolveMarkitdownURL()),
				resolveRAGMaxUploadBytes())
		if ragRepo != nil {
			// Fail RAG search closed for a tenant whose stored documents were
			// embedded under a different model/dim than this process is
			// configured for right now (see EmbeddingMismatch / checkEmbeddingGuard).
			ragHandler = ragHandler.WithEmbeddingGuard(ragEmbedModel, ragEmbedDim)
		}
		ragMux := http.NewServeMux()
		ragHandler.Register(ragMux)
		registerRAGRoutes(mux, featureGate, ragMux)
	}

	// Agent task lifecycle routes (#311, agent-subsystem blueprint Step 3.4):
	// always registered behind FeatureCowork so the gate returns 403 (not
	// 404) regardless of whether control-plane's agent-task store is
	// reachable. Persistence lives in control-plane
	// (apps/control-plane/internal/agenttask); this is the customer-facing
	// auth boundary and wire-shape translator that calls into it, mirroring
	// the RAG block above.
	{
		agentTaskClient := edgeagenttask.NewClient(resolveControlPlaneBaseURL())
		agentTaskHandler := edgeagenttask.NewHandler(agentTaskClient)
		agentTaskMux := http.NewServeMux()
		agentTaskHandler.Register(agentTaskMux)
		registerAgentTaskRoutes(mux, featureGate, agentTaskMux)
	}

	// Scheduled agent tasks ("routines"): customer-facing CRUD for the rows
	// control-plane's scheduler turns into real tasks. Same trust shape as
	// the block above: persistence lives in control-plane
	// (apps/control-plane/internal/agentsched), this is only the auth
	// boundary, registered behind FeatureCowork like /v1/agent/tasks.
	{
		agentSchedClient := edgeagentsched.NewClient(resolveControlPlaneBaseURL())
		agentSchedHandler := edgeagentsched.NewHandler(agentSchedClient)
		agentSchedMux := http.NewServeMux()
		agentSchedHandler.Register(agentSchedMux)
		registerAgentScheduleRoutes(mux, featureGate, agentSchedMux)
	}

	// API routes
	mux.Handle("/v1/models", modelsHandler(catalogClient, authorizer))
	mux.Handle("/catalog/models", handleCatalogModels(catalogClient))

	// Feature-gate read seam for Open WebUI (issue #293). OWUI has no in-repo
	// fork; its only extendable surface is a native Function that talks to
	// edge-api. This endpoint lets that Function (or any authenticated client)
	// read the tenant's live gate map at session start and gate its own UI,
	// reusing the same per-tenant Gate and request auth as Require().
	mux.Handle("/v1/featuregate", featuregate.NewStateHandler(featureGate))

	// Apply middleware: CompatHeaders (outermost) -> Metrics -> BudgetGate -> UnsupportedEndpoint (inner)
	//
	// Phase 14 — BudgetGate sits between metrics and unsupported-endpoint detection.
	// It pulls workspace identity by hashing the bearer token through the authz
	// resolver, then enforces the hard-cap stored in Redis (key written by the
	// control-plane budgets service on every Set/DeleteBudget call). Soft-cap
	// crossings are non-blocking but emit `budget_soft_cap_crossed_total`.
	budgetGate, err := buildBudgetGate(authzClient)
	if err != nil {
		log.Fatalf("failed to initialize budget gate: %v", err)
	}

	// Phase 19 — Supabase JWT validator + Authorization selector.
	//
	// The selector inspects the Authorization header: requests bearing the
	// canonical "Bearer hk_" API-key prefix flow to the existing API-key
	// path (unchanged); everything else is routed through the Supabase JWT
	// middleware which validates the token, populates the request context
	// via auth.WithUser, and emits OpenAI-shaped UNAUTHORIZED errors on
	// failure. The API-key handler remains responsible for its own
	// per-route authz (`handleModels`, `authorizeAliasRequest`, etc.).
	// Phase 19 JWT validation is opt-in: when the Supabase env vars are
	// absent (CI smoke runs, single-tenant API-key-only deployments) we
	// log and skip the selector + JWT middleware wiring so non-hk_ bearer
	// tokens continue to be rejected by the existing API-key handler
	// rather than crashing the process. Production deployments are
	// expected to provide every variable; the warning here is loud enough
	// for operators to catch.
	jwtCfg, jwtCfgErr := loadJWTAuthEnv()
	var jwtMW func(http.Handler) http.Handler
	// Hoisted out of the else-branch (rather than :=-scoped to it) so the
	// artifacts wiring below can reuse the same validator instance to
	// optionally resolve a viewer's tenant on its anonymous-reachable
	// serving routes. Stays nil when JWT wiring is skipped.
	var jwtValidator *auth.SupabaseJWTValidator
	if jwtCfgErr != nil {
		log.Printf("WARNING: phase-19 JWT auth wiring skipped (%v)", jwtCfgErr)
	} else {
		var err error
		jwtValidator, err = auth.NewSupabaseJWTValidator(rootCtx, auth.SupabaseJWTConfig{
			Issuer:      jwtCfg.Issuer,
			JWKSURL:     jwtCfg.JWKSURL,
			JWTAudience: jwtCfg.Audience,
			CAFile:      jwtCfg.CAFile,
		})
		if err != nil {
			log.Fatalf("failed to initialize Supabase JWT validator: %v", err)
		}
		jwtMW = auth.JWTMiddleware(jwtValidator, jwtAuditLogger(), auth.NewTenantFallback(dbPool))
	}

	// Artifacts hosting (#312, agent-subsystem blueprint Step 3.3).
	// Management routes (/v1/artifacts/*) sit inside the /v1/ prefix, so
	// they go through the JWT selector above exactly like RAG. The serving
	// routes (/artifacts/{id}, /artifacts/{id}/v/{n}) sit outside it by
	// design: a shared artifact must be reachable with no Authorization
	// header at all, so the handler resolves an optional viewer tenant
	// itself from the same jwtValidator instance and falls back to
	// anonymous-only (public artifacts) when Supabase JWT env vars are
	// absent, mirroring the jwtCfgErr graceful-degradation path.
	{
		var artifactsStore artifacts.Store
		if dbPool != nil {
			artifactsStore = artifacts.NewRepo(dbPool)
		}
		var artifactsClaimsParser artifacts.ClaimsParser
		if jwtValidator != nil {
			artifactsClaimsParser = jwtValidator
		}
		artifactsAudit := func(ctx context.Context, action, resourceType, resourceID, severity string,
			tenantID, actorID uuid.UUID, userAgent string, after any) {
			if dbPool == nil {
				return
			}
			if err := chat.InsertAuditEvent(ctx, dbPool, chat.AuditEvent{
				TenantID:     tenantID,
				ActorID:      actorID,
				Action:       action,
				Severity:     severity,
				ResourceType: resourceType,
				ResourceID:   resourceID,
				UserAgent:    userAgent,
				After:        after,
				DeploySHA:    os.Getenv("DEPLOY_SHA"),
				Environment:  os.Getenv("HIVE_ENV"),
			}); err != nil {
				log.Printf("artifacts audit write failed: %v", err)
			}
		}
		artifactsHandler := artifacts.NewHandler(artifactsStore, storageClient, storageCfg.FilesBucket,
			artifactsClaimsParser, os.Getenv("ARTIFACTS_FRAME_ANCESTOR"), artifactsAudit)
		artifactsHandler.Register(mux)
	}

	// Boot-time route/matrix drift guard (route_recorder.go). Refuses to
	// start rather than silently 404 a shipped route: see
	// assertMatrixCoverage's doc comment for exactly what this does and does
	// not catch.
	if err := assertMatrixCoverage(mux.Patterns(), m); err != nil {
		log.Fatal(err)
	}

	var handler http.Handler = mux
	handler = middleware.UnsupportedEndpointMiddleware(m)(handler)
	// budgetGate resolves the workspace identity from the API-key bearer
	// token via authzClient.Resolve, so it stays inert for JWT-authenticated
	// traffic. That is no longer a hole: session chat takes a real credit
	// hold before dispatch (chat.startSettlement), so an account with no
	// credits is refused by the reservation itself rather than by this
	// middleware. A ctx-aware budget resolver would move that refusal
	// earlier, it would not add one that is missing.
	handler = budgetGate.Wrap(handler)
	if jwtMW != nil {
		// Auth selector sits inside metrics/CompatHeaders so 401s are still
		// observed and CORS headers still apply, but outside budget/route
		// middleware so unauthenticated traffic never reaches accounting.
		handler = authSelectorMiddleware(jwtMW, handler)
	}
	handler = proxy.InstrumentHandler(edgeMetrics, handler)
	handler = middleware.CompatHeaders()(handler)

	// Global request-body cap (outermost) sized for the largest legitimate
	// body: a /v1/files multipart upload carrying a files.MaxFileSize (512 MiB)
	// payload plus multipart boundaries + form fields (globalMaxBody adds
	// multipartOverhead headroom). Smaller media endpoints (/v1/images/*,
	// /v1/audio/*) are wrapped with tighter per-route caps in
	// registerMediaFileBatchRoutes — http.MaxBytesHandler nests and the smaller
	// inner limit takes effect first. Chat keeps its own 4 MiB internal limit.
	// MaxBytesHandler only bounds the inbound request body, so SSE streaming
	// responses are unaffected.
	// Telemetry listener. Prometheus scrapes this port over the container
	// network; it is deliberately not published to the host and not routed
	// through the public ingress.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", proxy.MetricsHandler(promRegistry))
	go func() {
		metricsSrv := &http.Server{
			Addr:              metricsListenAddr,
			Handler:           metricsMux,
			ReadHeaderTimeout: 10 * time.Second,
		}
		log.Printf("edge-api metrics listening on %s", metricsListenAddr)
		if err := metricsSrv.ListenAndServe(); err != nil {
			log.Fatalf("metrics server failed: %v", err)
		}
	}()

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: http.MaxBytesHandler(handler, globalMaxBody),
		// ReadHeaderTimeout is the slowloris defence — a client dribbling
		// request headers is cut after this deadline. Safe for every route.
		ReadHeaderTimeout: 10 * time.Second,
		// IdleTimeout bounds keep-alive connections sitting idle between
		// requests so they cannot accumulate and exhaust file descriptors.
		IdleTimeout: 120 * time.Second,
		// ReadTimeout / WriteTimeout are intentionally left at zero:
		// WriteTimeout would abort long-lived SSE chat streams, and
		// ReadTimeout would cut slow large multipart uploads to
		// /v1/uploads. Slowloris is covered by ReadHeaderTimeout; body
		// size is bounded by MaxBytesHandler above.
	}
	log.Printf("edge-api listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func openOptionalDBPool(ctx context.Context) *pgxpool.Pool {
	dsn := strings.TrimSpace(os.Getenv("SUPABASE_DB_URL"))
	if dsn == "" {
		log.Printf("WARNING: edge-api DB pool unavailable (SUPABASE_DB_URL missing); JWT chat trace/audit writes disabled")
		return nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("WARNING: edge-api DB pool unavailable: %v", err)
		return nil
	}
	return pool
}

// metricsListenAddr is the address of the telemetry-only listener that serves
// /metrics. It is separate from the public listener because the public port is
// the one published to the host and routed through the ingress tunnel, so
// anything mounted on it is internet-reachable. The edge-api series include
// hive_upstream_requests_total{provider=...}, which names upstream providers
// and must never reach a customer, plus the full endpoint inventory and traffic
// volumes. Prometheus reaches this port over the container network only.
const metricsListenAddr = ":9102"

// registerInfraRoutes registers the unauthenticated infrastructure endpoints on
// the public mux. /metrics is deliberately not registered here; see
// metricsListenAddr.
//
// degraded is called on every /health request; it must not block or touch
// the network, so /health cannot become another consumer of a pool it exists
// to report on. It is typically authz.Client.Degraded itself, fed by real
// traffic on the authorize/budget-gate hot paths rather than a synthetic
// probe: /health used to be a hardcoded 200 regardless of whether edge-api
// could actually reach control-plane to resolve a key, so a pool-contention
// window that made every real request fail still reported this endpoint
// healthy throughout.
//
// The parameter is named and read as "degraded", not "healthy", on purpose
// (PR #975 CodeRabbit review): authz.Client.Degraded already returns true
// when unhealthy, and a "healthy" name checked as !healthy() silently
// inverted that polarity, so a fully healthy edge-api reported 503 and an
// actual control-plane outage reported 200 -- a second lie in the exact fix
// meant to stop this endpoint from lying.
func registerInfraRoutes(mux httpMux, specPath string, degraded func() bool) {
	mux.HandleFunc("/health", handleHealth(degraded))
	mux.Handle("/docs/", docs.SwaggerHandler(specPath))
}

func handleHealth(degraded func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if degraded != nil && degraded() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "degraded",
				// Fixed string, and deliberately service-agnostic. This
				// endpoint is unauthenticated on the public gateway, so the
				// degraded body follows the same discipline control-plane's
				// /health already enforces with a test: name the missing
				// capability, never the internal component, the host or the
				// upstream error. "authorization dependency unavailable" says
				// what a caller can act on; the internal topology is not the
				// caller's business.
				"reason": "authorization dependency unavailable",
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func loadStorageConfigFromEnv() (storageConfig, error) {
	endpoint, err := requireStorageEnv("S3_ENDPOINT")
	if err != nil {
		return storageConfig{}, err
	}
	accessKey, err := requireStorageEnv("S3_ACCESS_KEY")
	if err != nil {
		return storageConfig{}, err
	}
	secretKey, err := requireStorageEnv("S3_SECRET_KEY")
	if err != nil {
		return storageConfig{}, err
	}
	region, err := requireStorageEnv("S3_REGION")
	if err != nil {
		return storageConfig{}, err
	}
	filesBucket, err := requireStorageEnv("S3_BUCKET_FILES")
	if err != nil {
		return storageConfig{}, err
	}
	imagesBucket, err := requireStorageEnv("S3_BUCKET_IMAGES")
	if err != nil {
		return storageConfig{}, err
	}

	return storageConfig{
		Endpoint:     endpoint,
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		Region:       region,
		FilesBucket:  filesBucket,
		ImagesBucket: imagesBucket,
	}, nil
}

func requireStorageEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

// Per-route request-body caps. The global cap (globalMaxBody) is sized for
// 512 MiB file uploads, but the image/audio handlers forward multipart parts
// to LiteLLM without their own size check, so without tighter caps an
// authenticated caller could push hundreds of MiB through them. These mirror
// the providers' documented limits with a little headroom.
const (
	// multipartOverhead leaves room for multipart boundaries + form fields
	// (e.g. the "purpose" field) on top of a maximal files.MaxFileSize file
	// payload, so a valid near-512 MiB upload is not rejected at the body cap.
	multipartOverhead = 16 << 20
	globalMaxBody     = files.MaxFileSize + multipartOverhead // ~528 MiB
	imagesMaxBody     = 50 << 20                              // image edits/variations uploads
	audioMaxBody      = 26 << 20                              // transcription/translation audio (~25 MiB)
)

// voiceMW gates every /v1/audio/* route on featuregate.FeatureVoice (issue
// #293: Voice had a gate constant but no route ever called it). Images,
// files, and batches are ungated here by design — their own gate keys, if
// any, are out of this step's scope.
func registerMediaFileBatchRoutes(mux httpMux, imagesHandler, audioHandler, filesHandler, batchesHandler http.Handler, voiceMW func(http.Handler) http.Handler) {
	images := http.MaxBytesHandler(imagesHandler, imagesMaxBody)
	audio := voiceMW(http.MaxBytesHandler(audioHandler, audioMaxBody))
	mux.Handle("/v1/images/generations", images)
	mux.Handle("/v1/images/edits", images)
	mux.Handle("/v1/images/variations", images)
	mux.Handle("/v1/audio/speech", audio)
	mux.Handle("/v1/audio/transcriptions", audio)
	mux.Handle("/v1/audio/translations", audio)
	mux.Handle("/v1/files", filesHandler)
	mux.Handle("/v1/files/", filesHandler)
	mux.Handle("/v1/uploads", filesHandler)
	mux.Handle("/v1/uploads/", filesHandler)
	mux.Handle("/v1/batches", batchesHandler)
	mux.Handle("/v1/batches/", batchesHandler)
}

// registerAudioVoicesRoute attaches GET /v1/audio/voices. Extracted (issue
// #1079 shipped this as a bare inline mux.Handle call, which is exactly what
// left it with no support-matrix.json entry) so route_matrix_guard_test.go
// can register it in isolation and exercise assertMatrixCoverage against it.
// See the call site in main() for why this route deliberately sits outside
// every auth gate.
func registerAudioVoicesRoute(mux httpMux) {
	mux.Handle("/v1/audio/voices", audio.VoicesHandler())
}

func jwtAwareChatHandler(jwtHandler, apiKeyHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, ok := auth.UserFrom(r.Context()); ok && user != nil {
			jwtHandler.ServeHTTP(w, r)
			return
		}
		apiKeyHandler.ServeHTTP(w, r)
	})
}

// voiceGateForAPIKeys narrows a featuregate middleware to JWT-session callers
// only. hk_ API-key requests never populate auth.UserFrom (see the comment
// above the voiceMW construction in main()), so an hk_ caller skips the gate
// and reaches next directly; the gated path still applies in full to
// JWT-authenticated (OWUI/web-console) requests.
func voiceGateForAPIKeys(gate func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		gated := gate(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user, ok := auth.UserFrom(r.Context()); ok && user != nil {
				gated.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// modelsHandler is what GET /v1/models is actually registered as: the
// OpenAI-shaped handler below, wrapped so a real Anthropic SDK client works
// against the same route (issue #1259).
//
// APIKeyNormalizer is applied here at the leaf as well as in
// authSelectorMiddleware, and that is not redundant. The selector wrapper only
// exists when JWT auth is wired (jwtMW != nil); on a deployment where Supabase
// JWT config is absent, edge-api logs "JWT auth wiring skipped" and mounts no
// selector at all, so nothing normalizes x-api-key and handleModels reads an
// empty Authorization header for every Anthropic SDK caller. POST /v1/messages
// has always carried the same leaf wrapper for the same reason, which is
// precisely why it kept working on that deployment while this route 401'd.
//
// ModelsCompat then re-shapes the answer, but only for a caller that
// identified itself as Anthropic-shaped; an OpenAI-shaped caller, Open WebUI
// included, still gets the byte-identical OpenAI list it always did.
func modelsHandler(client *catalog.Client, authorizer *authz.Authorizer) http.Handler {
	return anthropic.APIKeyNormalizer(anthropic.ModelsCompat(handleModels(client, authorizer)))
}

// handleModels serves the OpenAI-compatible model list.
//
// Every caller needs a credential this service can resolve: a signed-in
// user's JWT (tenant-filtered list) or a registered API key (account-scoped
// list). There is no exception for the Open WebUI shim key. Open WebUI
// reaches this route with `OWUI_SHIM_KEY` on the Authorization header and
// no request body, so the OWUI unwrap middleware has nothing to lift a
// per-user JWT out of; that credential is expected to be a real minted
// Hive API key, and when it is, this handler resolves it like any other.
// An empty model picker in Open WebUI therefore means the configured shim
// key does not resolve, which is a deployment fault and is reported as
// such by the startup and periodic shim-key probe (see watchOWUIShimKey).
func handleModels(client *catalog.Client, authorizer *authz.Authorizer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// JWT-session caller: the JWT middleware already authenticated the
		// request and resolved the tenant, so serve the tenant-filtered list.
		// It is built from the same visibility predicate the admin toggle
		// writes, which keeps the listed models and the invokable models equal.
		if tenantID := auth.TenantID(r.Context()); tenantID != uuid.Nil {
			snapshot, err := client.FetchSnapshotForTenant(r.Context(), tenantID)
			if err != nil {
				writeCatalogUnavailable(w)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"object": "list",
				"data":   snapshot.Models,
			})
			return
		}

		// API-key caller: valid API key required to list models, even if not
		// binding to a specific alias. D-030 resolves a tenant for every
		// API-key account (public.tenant_billing_accounts), so this now
		// serves the same tenant-filtered list the JWT-session branch above
		// does, built from the same visibility predicate the admin toggle
		// writes -- rather than the unfiltered catalog every API key saw
		// before this. The key policy allowlist still governs what the key
		// may actually invoke on top of that.
		//
		// The OWUI shim key lands here too and is resolved exactly like any
		// other API key.
		authSnap, ok := authorizeAliasRequest(w, r, authorizer, "", 0, 0, 0)
		if !ok {
			return
		}

		tenantID, err := authSnap.TenantUUID()
		if err != nil {
			// Fail closed: an API key whose account has no resolvable tenant
			// cannot be filtered by entitlement at all, so it gets nothing
			// rather than the pre-D-030 unfiltered catalog (mirrors
			// inference.Orchestrator.selectRoute's ErrAccountNotProvisioned).
			// The predicate lives on the snapshot (authz.AuthSnapshot.TenantUUID)
			// so the OWUI shim-key probe checks this same requirement instead of
			// a weaker one, which is what made issue #717 invisible.
			code := "account_not_provisioned"
			apierrors.WriteError(w, http.StatusForbidden, "invalid_request_error",
				"This API key's account is not yet linked to a workspace. Contact support to complete account setup.", &code)
			return
		}

		snapshot, err := client.FetchSnapshotForTenant(r.Context(), tenantID)
		if err != nil {
			writeCatalogUnavailable(w)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data":   snapshot.Models,
		})
	})
}

func handleCatalogModels(client *catalog.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := client.FetchSnapshot(r.Context())
		if err != nil {
			writeCatalogUnavailable(w)
			return
		}

		writeJSON(w, http.StatusOK, snapshot.Catalog)
	})
}

func writeCatalogUnavailable(w http.ResponseWriter) {
	code := "catalog_unavailable"
	apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error", "The Hive model catalog is temporarily unavailable.", &code)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func resolveMatrixPath() string {
	matrixPath := os.Getenv("SUPPORT_MATRIX_PATH")
	if matrixPath != "" {
		return matrixPath
	}

	return "/app/packages/openai-contract/matrix/support-matrix.json"
}

func resolveSpecPath() string {
	specPath := os.Getenv("OPENAPI_SPEC_PATH")
	if specPath != "" {
		return specPath
	}

	return "/app/packages/openai-contract/generated/hive-openapi.yaml"
}

func resolveControlPlaneBaseURL() string {
	baseURL := os.Getenv("EDGE_CONTROL_PLANE_BASE_URL")
	if baseURL != "" {
		return baseURL
	}

	return "http://control-plane:8081"
}

// resolveMarkitdownURL returns the markitdown sidecar base URL. Defaults to
// the compose service DNS name; the sidecar runs on the default profile
// wherever edge-api runs.
func resolveMarkitdownURL() string {
	if u := strings.TrimSpace(os.Getenv("MARKITDOWN_URL")); u != "" {
		return u
	}
	return "http://markitdown:8700"
}

// parseRAGMaxUploadBytes reads the one upload ceiling this deployment sets.
//
// It is not only edge-api's: compose interpolates the same
// ${RAG_MAX_UPLOAD_BYTES} expression into the markitdown sidecar as
// MAX_UPLOAD_BYTES and into Open WebUI, where
// deploy/docker/owui-patches/hive_rag_env_config.py floors it into whole
// megabytes for the chat composer's cap. One variable, three consumers, so it
// needs one parse rule and one failure mode (issue #1428).
//
// Empty falls back to the package default, which is the documented contract
// for a deployment that never sets it and is what compose's own default
// produces anyway. A value that is present but malformed does NOT fall back.
// It used to warn and carry on, which silently moved the product's document
// ceiling to 25 MB while the deployment's own configuration said otherwise,
// and this repository has a long record of warnings nobody reads. The Python
// half now refuses to start on the same input, so falling back here would
// leave the two halves of one variable disagreeing about what malformed means.
func parseRAGMaxUploadBytes(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return edgerag.DefaultMaxUploadBytes, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf(
			"RAG_MAX_UPLOAD_BYTES=%q is not a positive whole number of bytes; "+
				"refusing to start rather than falling back to %d, which would cap "+
				"uploads at a size this deployment never asked for (26214400 is 25MB)",
			raw, edgerag.DefaultMaxUploadBytes)
	}
	return n, nil
}

// resolveRAGMaxUploadBytes caps the binary/base64 RAG upload path, and fails
// the process rather than the request when the ceiling is unreadable.
func resolveRAGMaxUploadBytes() int64 {
	n, err := parseRAGMaxUploadBytes(os.Getenv("RAG_MAX_UPLOAD_BYTES"))
	if err != nil {
		log.Fatalf("%v", err)
	}
	return n
}

func resolveRedisURL() string {
	url := os.Getenv("REDIS_URL")
	if url != "" {
		return url
	}
	return "redis://redis:6379/0"
}

// resolveRateLimitFailOpen reports whether the edge limiter should fail OPEN
// (admit traffic) when the Redis backend cannot be evaluated. Default is fail
// CLOSED (#51): a backend outage must not silently disable abuse controls.
// Set RATE_LIMIT_FAIL_OPEN=true only in dev/local where availability beats
// metering.
func resolveRateLimitFailOpen() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("RATE_LIMIT_FAIL_OPEN"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func resolveLiteLLMBaseURL() string {
	if u := os.Getenv("LITELLM_BASE_URL"); u != "" {
		return u
	}
	return "http://litellm:4000"
}

func resolveLiteLLMMasterKey() string {
	k := strings.TrimSpace(os.Getenv("LITELLM_MASTER_KEY"))
	if k == "" {
		// No dev fallback: a blank master key would let anyone who can
		// reach LiteLLM call upstream models unbilled. Fail fast at
		// startup instead of silently shipping a free-inference path.
		log.Fatal("LITELLM_MASTER_KEY is required and must be non-empty")
	}
	return k
}

// buildBudgetGate constructs the Phase 14 BudgetGate middleware. The gate
// resolves the workspace by hashing the bearer token through the authz client,
// then enforces the hard cap from Redis (key written by the control-plane
// budgets service on every Set/DeleteBudget). Soft-cap crossings increment
// the `budget_soft_cap_crossed_total` counter without blocking the request.
//
// Cache invalidation strategy: the control-plane PUSHES the latest hard_cap
// to Redis on every upsert; the gate READS with a brief TTL so missed pushes
// heal on the next read. The MTD spend counter is INCRed inline by the
// control-plane settlement path keyed by `budget:mtd_spend:{ws}:YYYY-MM`.
func buildBudgetGate(authzClient *authz.Client) (*limits.BudgetGate, error) {
	opt, err := redis.ParseURL(resolveRedisURL())
	if err != nil {
		return nil, fmt.Errorf("budget gate: parse redis URL: %w", err)
	}
	redisClient := redis.NewClient(opt)
	cache := limits.NewRedisCacheReader(redisClient)

	resolver := func(r *http.Request) (string, bool) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			return "", false
		}
		// Resolve is best-effort here — auth failures will be re-rejected by
		// the per-route authz path. We only need the workspace identity.
		snap, rerr := authzClient.Resolve(r.Context(), authHeader)
		if rerr != nil {
			return "", false
		}
		if snap.AccountID == "" {
			return "", false
		}
		return snap.AccountID, true
	}

	return limits.New(limits.Config{
		Cache:                cache,
		WorkspaceFromRequest: resolver,
		// SoftCapResolver intentionally nil — soft-cap evaluation lives in the
		// control-plane spendalerts cron. Phase 18 may surface a thin
		// internal endpoint for inline soft-cap checks if hot-path needs it.
		SoftCapResolver: nil,
	})
}

// loadJWTAuthEnv reads the Supabase JWT validator configuration from the
// environment. Returns a non-nil error when any required variable is
// missing; callers decide whether to fatal or skip the JWT path. Phase
// 19 deployments that serve chat-app traffic MUST set every variable —
// the caller's warning + skip is intended only for CI smoke runs and
// single-tenant API-key-only deployments where JWT validation is moot.
func loadJWTAuthEnv() (jwtAuthEnv, error) {
	issuer := strings.TrimSpace(os.Getenv("SUPABASE_JWT_ISSUER"))
	audience := strings.TrimSpace(os.Getenv("SUPABASE_JWT_AUDIENCE"))
	jwksURL := strings.TrimSpace(os.Getenv("SUPABASE_JWKS_URL"))

	var missing []string
	if issuer == "" {
		missing = append(missing, "SUPABASE_JWT_ISSUER")
	}
	if audience == "" {
		missing = append(missing, "SUPABASE_JWT_AUDIENCE")
	}
	if jwksURL == "" {
		missing = append(missing, "SUPABASE_JWKS_URL")
	}
	if len(missing) > 0 {
		return jwtAuthEnv{}, fmt.Errorf("Supabase JWT config missing required env vars: %s", strings.Join(missing, ", "))
	}

	// Enforce HTTPS for JWKS. An http:// URL would let an on-path
	// attacker substitute the JWKS document and forge arbitrary JWTs
	// that the validator would accept as legitimate Supabase tokens.
	if !strings.HasPrefix(strings.ToLower(jwksURL), "https://") {
		return jwtAuthEnv{}, fmt.Errorf("SUPABASE_JWKS_URL must be https (got %q)", jwksURL)
	}

	// Optional. The https requirement above still holds, and chain and
	// hostname verification still happen: this narrows WHICH authority is
	// acceptable for that one fetch, replacing the system roots rather
	// than extending them. It never turns verification off.
	caFile := strings.TrimSpace(os.Getenv("SUPABASE_JWKS_CA_FILE"))

	return jwtAuthEnv{Issuer: issuer, Audience: audience, JWKSURL: jwksURL, CAFile: caFile}, nil
}

// jwtAuditLogger returns the audit hook handed to the JWT middleware. For
// now this is a thin log.Printf shim — the dedicated edge-api audit.Logger
// is wired in a follow-up so we do not introduce that import here. The
// shape (`action, reason, ip`) matches the canonical control-plane audit
// signature so swapping in the real logger is mechanical.
func jwtAuditLogger() auth.AuditFailFunc {
	return func(action, reason, ip string) {
		log.Printf("auth.jwt.failure action=%s ip=%s reason=%s", action, ip, reason)
	}
}

// authSelectorMiddleware routes only Hive-versioned `/v1/*` traffic through
// the auth Selector. Infrastructure endpoints (/health, /docs/,
// /catalog/models) bypass authentication so probes and the Swagger UI keep
// working. Within /v1, an OWUI body-metadata unwrap runs first so requests
// arriving from Open WebUI (which sets the static shim key in Authorization
// because OWUI does not let pipelines override that header) are rewritten
// to carry the signed-in user's JWT before the Selector picks JWT vs
// API-key. Then the Selector forwards "Bearer hk_" credentials to the
// existing API-key path and everything else through the JWT middleware.
//
// The OWUI shim key is read from `OWUI_SHIM_KEY`. If unset the unwrap
// middleware is a no-op, preserving existing API-key-only deployments.
func authSelectorMiddleware(jwtMW func(http.Handler) http.Handler, next http.Handler) http.Handler {
	jwtPath := jwtMW(next)
	selector := auth.Selector(jwtPath, next)
	owuiUnwrap := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{
		ShimKey: strings.TrimSpace(os.Getenv("OWUI_SHIM_KEY")),
	})
	selector = owuiUnwrap(selector)
	// A real Anthropic SDK client (Anthropic(api_key=...), the default and
	// documented construction) sends the credential on x-api-key, never
	// Authorization. auth.Selector only inspects Authorization, and
	// anthropic.APIKeyNormalizer used to be wired solely at the mux leaf
	// (mux.Handle("/v1/messages", anthropic.APIKeyNormalizer(...))), which
	// sits inside this middleware, not outside it: the selector above always
	// ran first and never saw the header. Every x-api-key-only request fell
	// through to the JWT path and 401'd regardless of key validity. Applying
	// the same normalizer here, before the selector, fixes that for every
	// /v1/* route (a no-op wherever Authorization is already set).
	selector = anthropic.APIKeyNormalizer(selector)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		selector.ServeHTTP(w, r)
	})
}

// owuiShimKeyProbeInterval is how often the OWUI shim-key health probe
// re-resolves OWUI_SHIM_KEY after its boot check. The key can stop resolving
// long after startup (revoked, or rotated on one side only), so a boot-only
// check would miss the failure mode that actually happens.
const owuiShimKeyProbeInterval = 5 * time.Minute

// owuiShimKeyProbeTimeout bounds a single probe so a stalled control-plane
// cannot wedge the watch loop.
const owuiShimKeyProbeTimeout = 10 * time.Second

// shimKeyResolver is the slice of authz.Client the OWUI shim-key probe needs.
type shimKeyResolver interface {
	Resolve(ctx context.Context, rawToken string) (authz.AuthSnapshot, error)
}

// checkOWUIShimKey reports why the configured OWUI shim key cannot serve Open
// WebUI's own upstream calls, or nil when it can.
//
// OWUI_SHIM_KEY is expected to be a real minted Hive API key. Open WebUI
// presents it, and nothing else, on the upstream calls that carry no
// signed-in user: the bodyless GET /v1/models the model picker is built from,
// document-RAG embeddings, and text-to-speech. A key that does not resolve
// breaks all three, and every one of those failures is either invisible (an
// empty picker issues no request at all) or reported to the customer as a
// generic invalid-key error that names no cause. This probe is the signal
// that names it.
//
// Its predicate must stay at least as strict as the request path's, or the
// probe becomes a false green. It was exactly that in issue #717: it read
// Status and the model allowlist but never TenantID, so a shim key whose
// account had no public.tenant_billing_accounts row was logged as able to
// authenticate all three features, and then 403'd all three for every demo
// user. The tenant requirement is therefore not restated here; it is the same
// authz.AuthSnapshot.TenantUUID call handleModels and
// inference.Orchestrator.selectRoute make, so the two cannot drift again.
func checkOWUIShimKey(ctx context.Context, resolver shimKeyResolver, shimKey string) error {
	snapshot, err := resolver.Resolve(ctx, shimKey)
	if err != nil {
		// A transport failure, timeout, or control-plane 5xx says nothing
		// about the key itself -- it never reached a verdict. Keep that
		// distinguishable from "the key does not resolve" so
		// watchOWUIShimKey below does not tell an operator to mint a
		// replacement over a transient outage (nearly happened live on
		// 2026-08-14: a cold control-plane container timed out this same
		// probe, and rotating the key it names would have broken a working,
		// long-lived deployment for no reason).
		if errors.Is(err, authz.ErrUpstreamUnavailable) {
			return fmt.Errorf("%w: the control plane could not be reached to resolve it", err)
		}
		return fmt.Errorf("it does not resolve to a Hive API key: %w", err)
	}
	if snapshot.Status != "active" {
		return fmt.Errorf("the key it names has status %q, not active", snapshot.Status)
	}
	// A key that resolves no tenant is refused by the request path with 403
	// account_not_provisioned, so minting a replacement key cannot help: the
	// account itself has to be mapped to a tenant.
	if _, err := snapshot.TenantUUID(); err != nil {
		return fmt.Errorf("%w, so /v1/models, document RAG embeddings and text-to-speech all answer "+
			"403 account_not_provisioned for it. A new key will NOT help: map the account by running "+
			"apps/control-plane/cmd/backfill-tenants against the same database, or re-run "+
			"scripts/seed-owui-e2e-user.py, which provisions the mapping for the account it mints on", err)
	}
	// A key with neither allow_all_models nor any allowlisted alias resolves
	// fine and then denies every model, which reaches the operator as the same
	// silent outage.
	if !snapshot.AllowAllModels && len(snapshot.AllowedAliases) == 0 {
		return errors.New("the key it names is allowed no models (allow_all_models is false and its alias allowlist is empty)")
	}
	return nil
}

// watchOWUIShimKey probes the configured OWUI shim key at boot and every
// interval after that, logging every transition between usable and unusable.
// Logging only on change keeps a healthy deployment quiet while making a
// broken one loud, and makes a mid-life revocation visible within one
// interval rather than never.
//
// Deliberately not fatal, and that stayed the decision when the tenant check
// was added for issue #717: edge-api serves API-key and JWT traffic that has
// nothing to do with Open WebUI, and refusing to boot over a chat-surface
// credential would turn a degraded chat surface into a total outage for every
// other customer. The new failure mode makes that worse, not better, to be
// fatal on: an unprovisioned account is a missing database row that an operator
// fixes without redeploying, and a container that will not start is a strictly
// harder thing to diagnose from than one that logs the row it needs. What
// actually failed in #717 was the verdict, not its severity, so the fix is that
// the verdict is now true. A no-op when OWUI_SHIM_KEY is unset, which is the
// normal state for a deployment with no Open WebUI front-end.
// owuiShimKeyState is the probe's tri-state verdict. A plain boolean
// ("healthy") collapsed "transiently unreachable" and "genuinely unusable"
// into the same "unhealthy" value, so watchOWUIShimKey's change-detection
// (compare against the last logged state) never fired on a transition
// between the two: a transient control-plane timeout would log once, and a
// subsequent genuinely revoked/dead key would log nothing at all, because
// the boolean never changed. That reintroduced exactly the silence issue
// #717 exists to prevent, on the branch this PR added (PR #903 security
// review MEDIUM finding).
type owuiShimKeyState int

const (
	owuiShimKeyHealthy owuiShimKeyState = iota
	owuiShimKeyTransient
	owuiShimKeyDead
)

func watchOWUIShimKey(ctx context.Context, resolver shimKeyResolver, shimKey string, interval time.Duration) {
	shimKey = strings.TrimSpace(shimKey)
	if shimKey == "" {
		return
	}
	reported := false
	var lastState owuiShimKeyState
	for {
		probeCtx, cancel := context.WithTimeout(ctx, owuiShimKeyProbeTimeout)
		err := checkOWUIShimKey(probeCtx, resolver, shimKey)
		cancel()

		state := owuiShimKeyDead
		switch {
		case err == nil:
			state = owuiShimKeyHealthy
		case errors.Is(err, authz.ErrUpstreamUnavailable):
			state = owuiShimKeyTransient
		}

		if !reported || state != lastState {
			switch state {
			case owuiShimKeyHealthy:
				log.Printf("owui: OWUI_SHIM_KEY resolves to an active Hive API key on a tenant-provisioned account; Open WebUI model listing, document RAG embeddings, and text-to-speech can authenticate")
			case owuiShimKeyTransient:
				// Transient: the control plane could not be reached in time,
				// not a verdict that the key is bad. No "mint a replacement"
				// advice here -- rotating a working key over a timeout is
				// the failure this branch exists to prevent.
				log.Printf("owui: WARN OWUI_SHIM_KEY probe could not reach the control plane (transient, will retry next interval): %v. This is NOT a sign the key is invalid -- do not rotate it over this alone", err)
			default:
				log.Printf("owui: ERROR OWUI_SHIM_KEY is unusable: %v. Open WebUI's model picker will be empty and its document RAG embeddings and text-to-speech will fail with a generic invalid-key error. Mint a replacement with scripts/seed-owui-e2e-user.py, which updates .env and Open WebUI's persisted config together, then restart open-webui", err)
			}
			reported = true
			lastState = state
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// authorizeAliasRequest performs hot-path authorization.
// It writes the OpenAI-compatible error response itself if unauthorized.
// Returns the snapshot and a boolean indicating whether authorized.
func authorizeAliasRequest(w http.ResponseWriter, r *http.Request, authorizer *authz.Authorizer, aliasID string, estimatedCredits, billableTokens, freeTokens int64) (authz.AuthSnapshot, bool) {
	authHeader := r.Header.Get("Authorization")
	snapshot, headers, authErr := authorizer.Authorize(r.Context(), authHeader, aliasID, estimatedCredits, billableTokens, freeTokens)
	if authErr != nil {
		apierrors.WriteAuthFailure(w, authErr, headers)
		return snapshot, false
	}
	return snapshot, true
}
