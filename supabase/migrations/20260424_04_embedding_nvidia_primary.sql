-- Flip the embedding alias's primary route to NVIDIA NIM because LiteLLM's
-- OpenRouter integration does not support `/embeddings` (returns
-- "Unmapped LLM provider for this endpoint"). OR stays in the list as a
-- trailing fallback in case LiteLLM adds support later.

update public.provider_routes
   set priority = 10
 where route_id = 'route-nvidia-embedding';

update public.provider_routes
   set priority = 20
 where route_id = 'route-openrouter-embedding';

update public.alias_route_policies
   set fallback_order = '["route-nvidia-embedding","route-openrouter-embedding"]'::jsonb
 where alias_id = 'hive-embedding-default';
