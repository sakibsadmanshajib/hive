# #457: Supabase's OAuth Authorization Server issues a minimal
# third-party OIDC id_token/userinfo (standard OIDC claims only) for
# external relying parties like Open WebUI. It never carries a custom
# owui_role claim (supabase/migrations/20260726_01_owui_role_claim.sql
# only reaches GoTrue's own first-party session access tokens, not this
# third-party OAuth-provider token path), and it does not carry
# user_metadata either -- both confirmed live via DEBUG logs
# ("User roles from oauth: []" both times) against the physical demo
# box for a real tenant OWNER's real OAuth login. Neither
# OAUTH_ROLES_CLAIM nor any Postgres claims hook can fix this: the
# third-party id_token is simply too minimal, by Supabase's own design.
#
# Open WebUI's own pgvector connection (PGVECTOR_DB_URL,
# deploy/docker/docker-compose.yml) already reaches the same Postgres
# project, so the real tenant role is looked up there directly instead,
# keyed by email -- the one claim that IS reliably present in every
# login. Falls back to whatever role the OAUTH_* claim machinery above
# already computed (DEFAULT_USER_ROLE) if the lookup fails or the user
# has no active tenant membership, so a lookup/network hiccup degrades
# to the pre-existing behaviour instead of raising.
#
# This Open WebUI instance is shared across every Hive tenant (one
# instance, not one-per-tenant -- see apps/control-plane/internal/
# signup/webhook.go's per-tenant OWUI *group* provisioning, which only
# makes sense against a shared instance). An email-keyed lookup that
# picks "some" active membership would let a user who owns their own
# throwaway tenant become Open WebUI admin for everyone, regardless of
# their role in any OTHER tenant -- a cross-tenant privilege escalation.
# There is no tenant_id available anywhere in this OAuth callback
# context to scope against instead: the third-party id_token carries
# neither the owui_role claim nor user_metadata (both confirmed empty
# above), so tenant_id would be equally unreachable via that path.
# Given that, this only ever resolves a role when the email has EXACTLY
# ONE active tenant membership (the actual shape of the incident: a
# brand-new self-serve tenant's sole OWNER). Zero or more than one
# active membership is genuinely ambiguous and must never resolve
# toward more privilege, so both fail closed to the existing fallback
# (DEFAULT_USER_ROLE, "pending") rather than guessing.
try:
    import os as hive_os

    hive_email = user.email if user else user_data.get('email')
    hive_db_url = hive_os.environ.get('PGVECTOR_DB_URL', '')
    if hive_email and hive_db_url:
        import psycopg2 as hive_psycopg2

        # This lookup runs on every login, so the query must be bounded.
        # It used to be bounded with a libpq `options=-c statement_timeout=...`
        # startup string. That mechanism is applied by whichever backend the
        # client is handed at connection time, so it is honoured on a direct
        # Postgres and on a session-mode pooler, but a transaction-mode pooler
        # multiplexes many client sessions over fewer backends and does not
        # reliably carry it: the connection still succeeds, with no error and
        # no warning, and the requested timeout is simply never in effect.
        # `SET LOCAL` inside the implicit transaction this cursor block already
        # runs in is honoured in every one of those three cases, and it is the
        # same pattern every RLS `set_config(..., true)` call here relies on,
        # so it is used unconditionally rather than depending on what
        # PGVECTOR_DB_URL happens to point at.
        #
        # Measured, not assumed. On the current self-hosted deployment
        # (PGVECTOR_DB_URL -> supabase-db:5432, a direct Postgres with no
        # pooler in front of it) both mechanisms report the requested 3s, and
        # the role default with neither is 0, meaning unbounded. On the
        # previous hosted Supavisor pooler in transaction mode the `options=`
        # form measured as the role's untouched default instead of the
        # requested 3s. The change is therefore a no-op on today's deployment
        # and a real fix on any deployment that puts a transaction-mode pooler
        # back in this path.
        hive_conn = hive_psycopg2.connect(hive_db_url, connect_timeout=5)
        try:
            with hive_conn.cursor() as hive_cur:
                hive_cur.execute("SET LOCAL statement_timeout = 3000")
                hive_cur.execute("SET LOCAL lock_timeout = 3000")
                hive_cur.execute(
                    "SELECT tu.role FROM public.tenant_users tu "
                    "JOIN auth.users u ON u.id = tu.user_id "
                    "WHERE lower(u.email) = lower(%s) AND tu.status = 'ACTIVE'",
                    (hive_email,),
                )
                hive_rows = hive_cur.fetchall()
                if len(hive_rows) == 1:
                    role = 'admin' if hive_rows[0][0] == 'OWNER' else 'user'
                # else: no membership, or more than one (ambiguous which
                # tenant this login is for) -- leave `role` at whatever
                # the fallback machinery above already computed.
        finally:
            hive_conn.close()
except Exception as hive_tenant_role_error:
    log.warning(f'hive tenant-role lookup failed, keeping fallback role: {hive_tenant_role_error}')
