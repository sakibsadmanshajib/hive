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
try:
    import os as hive_os

    hive_email = user.email if user else user_data.get('email')
    hive_db_url = hive_os.environ.get('PGVECTOR_DB_URL', '')
    if hive_email and hive_db_url:
        import psycopg2 as hive_psycopg2

        hive_conn = hive_psycopg2.connect(hive_db_url, connect_timeout=5)
        try:
            with hive_conn.cursor() as hive_cur:
                hive_cur.execute(
                    "SELECT tu.role FROM public.tenant_users tu "
                    "JOIN auth.users u ON u.id = tu.user_id "
                    "WHERE lower(u.email) = lower(%s) AND tu.status = 'ACTIVE' "
                    "ORDER BY tu.joined_at DESC LIMIT 1",
                    (hive_email,),
                )
                hive_row = hive_cur.fetchone()
                if hive_row:
                    role = 'admin' if hive_row[0] == 'OWNER' else 'user'
        finally:
            hive_conn.close()
except Exception as hive_tenant_role_error:
    log.warning(f'hive tenant-role lookup failed, keeping fallback role: {hive_tenant_role_error}')
