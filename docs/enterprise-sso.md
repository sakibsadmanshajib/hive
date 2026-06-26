# Enterprise SSO Integration Guide

This guide covers the two supported integration paths for connecting your
organisation's identity store to the Hive EnterpriseEdge deployment.

## How it works

Hive EnterpriseEdge uses [GoTrue v2](https://github.com/supabase/gotrue) as
its self-hosted identity provider. GoTrue has native support for SAML 2.0 and
external OIDC providers. No SAML or OIDC protocol code lives in Hive itself;
Hive only gates access at the feature level (per-tenant `sso_enabled` flag)
and maps the resulting JWT claims to a Hive tenant.

All identity data stays on the customer's own infrastructure. No external
SaaS identity service is introduced by this integration.

## Path A: SAML 2.0 (recommended for ADFS and Entra ID in SAML mode)

### When to use

- Your organisation runs Active Directory Federation Services (ADFS)
- You use Microsoft Entra ID (Azure AD) configured as a SAML Identity Provider
- Any SAML 2.0-compliant IdP (Okta, Ping, etc.)

### Steps

1. **Enable SAML in GoTrue.** In your `.env` file set:

   ```
   ENTERPRISE_SSO_SAML_ENABLED=true
   ENTERPRISE_SSO_SAML_PRIVATE_KEY=<base64-encoded PEM RSA-2048 key>
   ```

   Generate the Service Provider (SP) private key:

   ```bash
   openssl genrsa -out saml_sp.pem 2048
   base64 -w 0 saml_sp.pem
   ```

2. **Set the tenant gate.** In the `tenant_settings` table set
   `ENABLE_SSO_SAML = true` for the relevant tenant row. The edge-api
   feature gate reads this flag and allows SSO login flows for that tenant only.

3. **Register your IdP metadata** with GoTrue after the service starts:

   ```bash
   curl -X POST http://supabase-auth:9999/admin/sso/providers \
     -H "Authorization: Bearer <service_role_key>" \
     -H "Content-Type: application/json" \
     -d '{
       "type": "saml",
       "metadata_xml": "<EntityDescriptor ...>...</EntityDescriptor>",
       "domains": ["yourdomain.com"]
     }'
   ```

   Alternatively supply `metadata_url` instead of `metadata_xml` if your IdP
   exposes a federation metadata endpoint.

4. **Configure your IdP** (ADFS or Entra) to trust GoTrue as a Service Provider:

   - SP Entity ID: `http://<your-site-url>/auth/v1/sso/saml/metadata`
   - ACS (Assertion Consumer Service) URL: `http://<your-site-url>/auth/v1/sso/saml/acs`
   - Name ID format: `urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress`
   - Required claim: `email`

5. **Test the flow.** Navigate to `GET /auth/v1/sso` from your frontend,
   passing `provider_id` (returned by the registration call above) or the
   registered domain. GoTrue handles the SAML redirect, assertion validation,
   and JWT issuance.

### Active Directory via ADFS

ADFS exposes a SAML 2.0 federation metadata URL at:

```
https://<adfs-server>/FederationMetadata/2007-06/FederationMetadata.xml
```

Use this URL as `metadata_url` in the registration call. No LDAP client is
required; ADFS translates AD group membership into SAML claims.

---

## Path B: OIDC (Entra ID, Google Workspace, any OIDC IdP)

### Microsoft Entra ID (Azure AD) via OIDC

1. In Entra, register a new application. Add a Redirect URI:
   `<ENTERPRISE_SITE_URL>/auth/v1/callback`.

2. In `.env`:

   ```
   ENTERPRISE_SSO_MICROSOFT_ENABLED=true
   ENTERPRISE_SSO_MICROSOFT_CLIENT_ID=<application (client) ID>
   ENTERPRISE_SSO_MICROSOFT_SECRET=<client secret>
   ENTERPRISE_SSO_MICROSOFT_TENANT_URL=https://login.microsoftonline.com/<tenant-id>/v2.0
   ```

3. Set `ENABLE_SSO_MICROSOFT = true` in `tenant_settings` for the tenant.

### Google Workspace via OIDC

1. In Google Cloud Console, create an OAuth 2.0 client. Add Redirect URI:
   `<ENTERPRISE_SITE_URL>/auth/v1/callback`.

2. In `.env`:

   ```
   ENTERPRISE_SSO_GOOGLE_ENABLED=true
   ENTERPRISE_SSO_GOOGLE_CLIENT_ID=<client ID>
   ENTERPRISE_SSO_GOOGLE_SECRET=<client secret>
   ```

3. Set `ENABLE_SSO_GOOGLE = true` in `tenant_settings` for the tenant.

---

## Path C: LDAP / Active Directory without ADFS

GoTrue does not have a native LDAP client. The recommended integration paths
for raw LDAP are:

### Option 1: Deploy an LDAP-to-OIDC bridge (preferred)

Run [Dex](https://dexidp.io) or [Keycloak](https://www.keycloak.org) as a
sidecar that speaks LDAP upstream and OIDC downstream. Then configure GoTrue
to use the bridge as an OIDC provider (see Path B above). Both Dex and
Keycloak support Active Directory LDAP natively.

Example Dex connector for Active Directory:

```yaml
connectors:
  - type: ldap
    name: Active Directory
    id: ad
    config:
      host: ldap.yourdomain.com:636
      insecureSkipVerify: false
      bindDN: cn=svc-hive,ou=service-accounts,dc=yourdomain,dc=com
      bindPW: <service account password>
      usernamePrompt: Email
      userSearch:
        baseDN: ou=users,dc=yourdomain,dc=com
        filter: "(objectClass=person)"
        username: userPrincipalName
        idAttr: DN
        emailAttr: userPrincipalName
        nameAttr: displayName
```

Configure GoTrue to point at Dex's OIDC issuer URL as an external provider.

### Option 2: Entra ID Connect sync

If your organisation uses Microsoft Entra ID Connect to sync on-premises AD
to Entra, use Path B (Entra OIDC) directly. No LDAP configuration is needed
on the Hive side.

---

## Tenant-to-user mapping

### How the hook works

When a user authenticates via SSO, GoTrue issues a JWT and fires the
`custom_access_token_hook` (installed by migration `20260516_07`). The hook
enriches the token with Hive-specific claims (`tenant_id`, `tenants`, `role`).

**The hook does NOT do email-domain-to-tenant auto-assignment.** It resolves
the tenant as follows:

1. Reads `raw_user_meta_data.selected_tenant_id` from the GoTrue `auth.users`
   row (set by the frontend when the user explicitly selects a tenant).
2. Validates that `selected_tenant_id` appears in the user's active
   `public.tenant_users` memberships.
3. If `selected_tenant_id` is absent or invalid, falls back to the first
   active membership row for the user.
4. If no active membership exists at all, the hook raises
   `no_active_membership` and the login fails.

**Consequence for SSO:** A net-new SSO user who has never been added to a
tenant will hit `no_active_membership` on their first login and be rejected.

### Pre-provisioning requirement (MVP)

> **`GOTRUE_DISABLE_SIGNUP` interaction (verified against GoTrue v2.170.0
> source):** The enterprise compose defaults `GOTRUE_DISABLE_SIGNUP=true`
> (air-gapped hardening from #254). In GoTrue v2, `disable_signup` blocks the
> `CreateAccount` path in `createAccountFromExternalIdentity`, which is the
> shared code path for both external OIDC and SAML ACS. There is no SSO
> bypass. A first-time SSO login that would create a new `auth.users` row is
> rejected with `SignupDisabled` exactly as a password signup would be.
>
> **Working path with `disable_signup=true`:** the admin must pre-create the
> `auth.users` record via the GoTrue invite API before the user's first SSO
> login. The invite creates the record without going through the signup gate.
> The first SSO login then resolves to `UpdateAccount` (existing user), not
> `CreateAccount`, and proceeds normally. Do NOT flip `disable_signup` to
> `false` to work around this; that undoes the air-gapped hardening.

Before an SSO user can log in for the first time, an administrator must
complete these steps in order:

1. Confirm the tenant row exists in `public.tenants`.

2. Set the relevant `ENABLE_SSO_*` key to `true` in `public.tenant_settings`
   for that tenant.

3. (SAML only) Register the IdP metadata via the GoTrue admin API:

   ```bash
   curl -X POST http://supabase-auth:9999/admin/sso/providers \
     -H "Authorization: Bearer <ENTERPRISE_SERVICE_ROLE_KEY>" \
     -H "Content-Type: application/json" \
     -d '{"type":"saml","metadata_xml":"<EntityDescriptor.../>","domains":["yourdomain.com"]}'
   ```

4. **Pre-create the user's `auth.users` record via the GoTrue invite API.**
   This is mandatory when `GOTRUE_DISABLE_SIGNUP=true`:

   ```bash
   curl -X POST http://supabase-auth:9999/invite \
     -H "Authorization: Bearer <ENTERPRISE_SERVICE_ROLE_KEY>" \
     -H "Content-Type: application/json" \
     -d '{"email": "user@yourdomain.com"}'
   ```

   The user does not need to click the invite link. The call creates the
   `auth.users` row. Subsequent SSO login resolves to `UpdateAccount` (the
   existing record), bypassing the `disable_signup` gate entirely.

5. Insert a `tenant_users` row for the user's GoTrue UUID with
   `status = 'active'`. You can retrieve the UUID from the invite response or
   by querying `auth.users` with the service-role Supabase client.

6. The user now authenticates via SSO. GoTrue matches the existing record,
   the `custom_access_token_hook` resolves the `tenant_users` membership, and
   a valid Hive JWT is issued.

> **Important:** Step 4 (invite) must precede step 6 (SSO login). If the
> `auth.users` row does not exist when the user logs in via SSO,
> `disable_signup=true` will reject the attempt. If the `tenant_users` row
> (step 5) is absent, the hook raises `no_active_membership` and the JWT is
> not issued. Both must be in place before first login.

### Future enhancement (not yet implemented)

Automatic domain-based provisioning (creating a `tenant_users` row on first
SSO login based on the user's email domain) is a planned future feature. It
requires verified domain ownership per tenant to prevent tenant-hopping
attacks. This is tracked separately and is NOT implemented in this PR. Do not
configure SSO expecting auto-provisioning to work.

---

## Regulatory notes

- All identity traffic stays within the customer's own network perimeter. No
  user credentials or tokens leave the on-premises stack.
- GoTrue is self-hosted; Hive does not depend on any external SaaS auth service.
- Compliant with OSFI B-10 (data residency) and Quebec Law 25 (personal
  information processed within the organisation's controlled environment).
- Audit events for SSO login and provider registration are recorded in the
  `public.audit_log` table via the existing audit pipeline.
