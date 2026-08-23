# Resolves this login's Open WebUI role from Hive's own Postgres.
#
# Two separate questions are answered here, and keeping them separate is the
# whole point of this file (issue #748):
#
#   1. May this login use the chat at all? Yes if the email has any ACTIVE
#      public.tenant_users membership on a non-archived tenant. That resolves
#      Open WebUI's ordinary 'user' role.
#   2. Does this login administer the Open WebUI instance? Only if the control
#      plane says so, explicitly, at the platform level: an ACTIVE 'owner' row
#      in public.account_memberships on an account carrying
#      is_platform_admin = true. That is the same predicate the control plane
#      uses for its own platform-admin surfaces
#      (apps/control-plane/internal/platform/role_pgx.go, IsPlatformAdmin), so
#      there is one definition of "platform operator" with two consumers rather
#      than a second, chat-only notion of admin.
#
# What this file used to do, and must never do again: map a tenant_users row
# with role OWNER onto Open WebUI 'admin'. This Open WebUI instance is shared
# by every Hive tenant (one instance, not one per tenant, which is why
# apps/control-plane/internal/signup provisions per-tenant OWUI *groups*), so
# that mapping made a customer an administrator of every other customer's chat.
# A live audit of the demo box confirmed it: a legitimately provisioned tenant
# OWNER signed in, received admin, enumerated other tenants' users, read another
# user's chat titles and read another tenant's uploaded file. A tenant OWNER is
# a customer. Tenant ownership is a billing and RBAC concept and carries no
# platform authority, so it must not decide the administrative role of a shared
# deployment. See .wolf/decisions.md D-044: the control plane owns state and
# knobs, Open WebUI is a view.
#
# Why the lookup exists at all (#457): Supabase's OAuth Authorization Server
# issues a minimal third-party OIDC id_token/userinfo (standard OIDC claims
# only) for external relying parties like Open WebUI. It never carries a custom
# owui_role claim (supabase/migrations/20260823_03_owui_role_never_admin.sql,
# like the migrations before it, only reaches GoTrue's own first-party session
# access tokens, not this third-party OAuth-provider token path), and it does
# not carry user_metadata either -- both confirmed live via DEBUG logs
# ("User roles from oauth: []" both times) against the physical demo box for a
# real tenant OWNER's real OAuth login. Neither OAUTH_ROLES_CLAIM nor any
# Postgres claims hook can fix that: the third-party id_token is simply too
# minimal, by Supabase's own design. Open WebUI's own pgvector connection
# (PGVECTOR_DB_URL, deploy/docker/docker-compose.yml) already reaches the same
# Postgres, so the real state is read there directly, keyed by email, the one
# claim that IS reliably present in every login.
#
# Failure is closed, not open. A lookup that raises, an email this Postgres has
# never seen, or an email that somehow resolves more than one auth.users row all
# leave `role` at whatever the OAUTH_* claim machinery above already computed,
# which is DEFAULT_USER_ROLE ("pending" on this deployment). Pending means the
# account activation screen, so the failure mode is no access rather than more
# access. The multi-row case is genuine ambiguity about who is signing in, and
# ambiguity must never resolve toward privilege.
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
                # One round trip, two independent booleans, so neither answer
                # can be derived from the other. The email is a bound
                # parameter, never interpolated.
                hive_cur.execute(
                    "SELECT"
                    "  EXISTS ("
                    "    SELECT 1 FROM public.account_memberships m"
                    "      JOIN public.accounts a ON a.id = m.account_id"
                    "     WHERE m.user_id = u.id"
                    "       AND m.role = 'owner'"
                    "       AND m.status = 'active'"
                    "       AND a.is_platform_admin = true"
                    "  ) AS is_platform_operator,"
                    "  EXISTS ("
                    "    SELECT 1 FROM public.tenant_users tu"
                    "      JOIN public.tenants t ON t.id = tu.tenant_id"
                    "     WHERE tu.user_id = u.id"
                    "       AND tu.status = 'ACTIVE'"
                    "       AND t.archived_at IS NULL"
                    "  ) AS has_active_membership "
                    "FROM auth.users u "
                    "WHERE lower(u.email) = lower(%s)",
                    (hive_email,),
                )
                hive_rows = hive_cur.fetchall()
                if len(hive_rows) == 1:
                    hive_is_operator, hive_has_membership = hive_rows[0]
                    if hive_is_operator:
                        role = 'admin'
                    elif hive_has_membership:
                        role = 'user'
                    # else: no active membership anywhere -- leave `role` at
                    # the fallback (pending) rather than admitting an identity
                    # this deployment knows nothing about.
        finally:
            hive_conn.close()
except Exception as hive_tenant_role_error:
    log.warning(f'hive tenant-role lookup failed, keeping fallback role: {hive_tenant_role_error}')
