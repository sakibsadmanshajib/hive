-- supabase/migrations/20260625_03_audit_taxonomy_extension.sql
-- Extends the audit taxonomy with inference, RAG, file-access, and
-- data-subject-request event types required for Quebec Law 25, PHIPA,
-- and OSFI B-10 compliance.
--
-- The audit_log table uses a plain text action column (no enum), so
-- this migration adds nothing structural to that table. It documents
-- the new action strings as a Postgres comment on the table so the
-- schema is self-describing, and it creates a lookup view that lists
-- every known action together with its tier for operator reference.
--
-- Idempotent: safe to re-apply.

BEGIN;

-- Operator-visible reference view. Lists every documented action and
-- whether it is security-tier (fail-closed) or wal-tier (async).
-- Replace with CREATE OR REPLACE so re-applying is a no-op.
CREATE OR REPLACE VIEW public.audit_action_taxonomy AS
SELECT action, tier, description
FROM (VALUES
  -- Security tier (fail-closed on Postgres write error)
  ('AUTH_SIGNIN_SUCCESS',          'security', 'Successful authentication'),
  ('AUTH_SIGNIN_FAILURE',          'security', 'Failed authentication attempt'),
  ('AUTH_SIGNUP_SUCCESS',          'security', 'New account created'),
  ('AUTH_JWT_INVALID',             'security', 'JWT failed signature verification'),
  ('AUTH_JWT_EXPIRED',             'security', 'JWT presented after expiry'),
  ('AUTH_SESSION_REVOKED',         'security', 'Session explicitly revoked'),
  ('AUTH_SIGNIN_FAILURE_NO_TENANT','security', 'Auth attempt with no matching tenant'),
  ('AUTH_ROLE_CHANGE',             'security', 'User role modified'),
  ('RBAC_GRANT',                   'security', 'Permission granted'),
  ('RBAC_REVOKE',                  'security', 'Permission revoked'),
  ('RBAC_DENY',                    'security', 'Access denied by policy'),
  ('CROSS_TENANT_ATTEMPT',         'security', 'Request attempted cross-tenant access'),
  ('TENANT_SETTING_UPDATE',        'security', 'Tenant configuration changed'),
  ('TENANT_SWITCH',                'security', 'Actor switched active tenant'),
  ('TENANT_USER_ADD',              'security', 'User added to tenant'),
  ('TENANT_USER_REMOVE',           'security', 'User removed from tenant'),
  ('OWUI_GROUP_CREATE_SUCCESS',    'security', 'Open WebUI group created'),
  ('OWUI_GROUP_CREATE_FAILURE',    'security', 'Open WebUI group creation failed'),
  ('OWUI_GROUP_ADD_SUCCESS',       'security', 'User added to Open WebUI group'),
  ('OWUI_GROUP_ADD_FAILURE',       'security', 'Failed to add user to Open WebUI group'),
  ('API_KEY_ISSUE',                'security', 'API key issued'),
  ('API_KEY_REVOKE',               'security', 'API key revoked'),
  ('CRYPTO_KEY_ROTATE',            'security', 'Cryptographic key rotated'),
  ('TLS_CERT_ROTATE',              'security', 'TLS certificate rotated'),
  ('JWKS_FETCH_FAILURE',           'security', 'JWKS endpoint returned error'),
  ('MIGRATION_APPLY',              'security', 'Database migration applied'),
  ('DEPLOY_PUSH',                  'security', 'New deployment pushed'),
  ('BACKUP_INTEGRITY_FAIL',        'security', 'Backup integrity check failed'),
  ('AUDIT_CHAIN_VERIFY_FAIL',      'security', 'Audit hash-chain verification failed'),
  ('SERVER_PANIC',                 'security', 'Unhandled panic in server process'),
  ('WEBHOOK_SIGNATURE_FAIL',       'security', 'Webhook HMAC signature invalid'),
  ('INCIDENT_DECLARE',             'security', 'Security incident declared'),
  ('INCIDENT_RESOLVE',             'security', 'Security incident resolved'),
  -- DATA_SUBJECT_REQUEST: Law 25 (Quebec), PHIPA, OSFI B-10 data subject
  -- access/erasure/portability requests. Security tier so the record is
  -- never silently lost on Postgres write failure.
  ('DATA_SUBJECT_REQUEST',         'security', 'Data subject access/erasure/portability request (Law 25 / PHIPA / OSFI B-10)'),
  -- WAL tier (async, falls back to local WAL on Postgres write error)
  -- LLM_RESPONSE: completion metadata only — completion_tokens, finish_reason,
  -- latency_ms. The completion text MUST NOT appear in after_json.
  ('LLM_RESPONSE',                 'wal', 'LLM completion metadata (tokens, finish_reason, latency_ms); no completion text'),
  ('RAG_DOCUMENT_UPLOAD',          'wal', 'Document added to retrieval corpus'),
  ('RAG_DOCUMENT_DELETE',          'wal', 'Document removed from retrieval corpus'),
  ('RAG_SEARCH',                   'wal', 'Semantic search query issued against retrieval corpus'),
  -- RAG_CHUNK_RETRIEVED: resource_id = chunk_id,
  -- after_json = {"score": <float>, "document_id": <uuid>}
  ('RAG_CHUNK_RETRIEVED',          'wal', 'Chunk returned by retrieval query; resource_id=chunk_id, after_json={score,document_id}'),
  ('FILE_ACCESS',                  'wal', 'Stored file read or downloaded')
) AS t(action, tier, description);

COMMENT ON VIEW public.audit_action_taxonomy IS
  'Reference listing of every documented audit action and its routing tier. '
  'security = fail-closed synchronous write; wal = async with local WAL fallback.';

GRANT SELECT ON public.audit_action_taxonomy TO auditor_ro;
GRANT SELECT ON public.audit_action_taxonomy TO hive_app;

COMMIT;
