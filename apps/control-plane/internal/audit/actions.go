package audit

// Action constants for security-tier events. These route synchronously
// (fail-closed on Postgres write errors).
const (
	ActionAuthSigninSuccess         = "AUTH_SIGNIN_SUCCESS"
	ActionAuthSigninFailure         = "AUTH_SIGNIN_FAILURE"
	ActionAuthSignupSuccess         = "AUTH_SIGNUP_SUCCESS"
	ActionAuthJWTInvalid            = "AUTH_JWT_INVALID"
	ActionAuthJWTExpired            = "AUTH_JWT_EXPIRED"
	ActionAuthSessionRevoked        = "AUTH_SESSION_REVOKED"
	ActionAuthSigninFailureNoTenant = "AUTH_SIGNIN_FAILURE_NO_TENANT"
	ActionAuthRoleChange            = "AUTH_ROLE_CHANGE"
	ActionRBACGrant                 = "RBAC_GRANT"
	ActionRBACRevoke                = "RBAC_REVOKE"
	ActionRBACDeny                  = "RBAC_DENY"
	ActionCrossTenantAttempt        = "CROSS_TENANT_ATTEMPT"
	ActionTenantSettingUpdate       = "TENANT_SETTING_UPDATE"
	ActionTenantSwitch              = "TENANT_SWITCH"
	ActionTenantUserAdd             = "TENANT_USER_ADD"
	ActionTenantUserRemove          = "TENANT_USER_REMOVE"
	ActionOWUIGroupCreateSuccess    = "OWUI_GROUP_CREATE_SUCCESS"
	ActionOWUIGroupCreateFailure    = "OWUI_GROUP_CREATE_FAILURE"
	ActionOWUIGroupAddSuccess       = "OWUI_GROUP_ADD_SUCCESS"
	ActionOWUIGroupAddFailure       = "OWUI_GROUP_ADD_FAILURE"
	ActionAPIKeyIssue               = "API_KEY_ISSUE"
	ActionAPIKeyRevoke              = "API_KEY_REVOKE"
	ActionCryptoKeyRotate           = "CRYPTO_KEY_ROTATE"
	ActionTLSCertRotate             = "TLS_CERT_ROTATE"
	ActionJWKSFetchFailure          = "JWKS_FETCH_FAILURE"
	ActionMigrationApply            = "MIGRATION_APPLY"
	ActionDeployPush                = "DEPLOY_PUSH"
	ActionBackupIntegrityFail       = "BACKUP_INTEGRITY_FAIL"
	ActionAuditChainVerifyFail      = "AUDIT_CHAIN_VERIFY_FAIL"
	ActionServerPanic               = "SERVER_PANIC"
	ActionWebhookSignatureFail      = "WEBHOOK_SIGNATURE_FAIL"
	ActionIncidentDeclare           = "INCIDENT_DECLARE"
	ActionIncidentResolve           = "INCIDENT_RESOLVE"
	// ActionDataSubjectRequest records Law 25 / PHIPA / OSFI B-10 data subject
	// requests (access, erasure, portability). Security tier: fail-closed so
	// the request is never silently lost.
	ActionDataSubjectRequest = "DATA_SUBJECT_REQUEST"
)

// Action constants for WAL-tier events. These fall back to the local WAL
// on Postgres write errors and are drained asynchronously.
const (
	// ActionLLMResponse records completion metadata only: completion_tokens,
	// finish_reason, latency_ms. The completion text MUST NOT be included.
	ActionLLMResponse = "LLM_RESPONSE"
	// ActionRAGDocumentUpload records a document added to a retrieval corpus.
	ActionRAGDocumentUpload = "RAG_DOCUMENT_UPLOAD"
	// ActionRAGDocumentDelete records a document removed from a retrieval corpus.
	ActionRAGDocumentDelete = "RAG_DOCUMENT_DELETE"
	// ActionRAGSearch records a semantic search query against a retrieval corpus.
	ActionRAGSearch = "RAG_SEARCH"
	// ActionRAGChunkRetrieved records a single chunk returned by a retrieval query.
	// resource_id = chunk_id; after_json = {"score": <float>, "document_id": <uuid>}.
	ActionRAGChunkRetrieved = "RAG_CHUNK_RETRIEVED"
	// ActionFileAccess records a read or download of a stored file.
	ActionFileAccess = "FILE_ACCESS"
)

// Security-tier actions fail closed on Postgres write errors. LLM-tier
// actions fall back to local WAL on Postgres failure. The classifier is
// an immutable switch — there is no package-level map any test or
// downstream caller can mutate to alter tier routing.
func IsSecurityAction(action string) bool {
	switch action {
	case
		ActionAuthSigninSuccess,
		ActionAuthSigninFailure,
		ActionAuthSignupSuccess,
		ActionAuthJWTInvalid,
		ActionAuthJWTExpired,
		ActionAuthSessionRevoked,
		ActionAuthSigninFailureNoTenant,
		ActionAuthRoleChange,
		ActionRBACGrant,
		ActionRBACRevoke,
		ActionRBACDeny,
		ActionCrossTenantAttempt,
		ActionTenantSettingUpdate,
		ActionTenantSwitch,
		ActionTenantUserAdd,
		ActionTenantUserRemove,
		ActionOWUIGroupCreateSuccess,
		ActionOWUIGroupCreateFailure,
		ActionOWUIGroupAddSuccess,
		ActionOWUIGroupAddFailure,
		ActionAPIKeyIssue,
		ActionAPIKeyRevoke,
		ActionCryptoKeyRotate,
		ActionTLSCertRotate,
		ActionJWKSFetchFailure,
		ActionMigrationApply,
		ActionDeployPush,
		ActionBackupIntegrityFail,
		ActionAuditChainVerifyFail,
		ActionServerPanic,
		ActionWebhookSignatureFail,
		ActionIncidentDeclare,
		ActionIncidentResolve,
		ActionDataSubjectRequest:
		return true
	}
	return false
}
