# Phase 2: Identity & Account Foundation - Context

**Gathered:** 2026-03-28
**Status:** Ready for planning

<domain>
## Phase Boundary

Establish hosted Supabase-backed identity, persistent console sessions, shared workspace account tenancy, and the core account profile data needed before billing, payments, invoices, and API-key management. This phase defines how developers enter and inhabit Hive's authenticated control plane; it does not expand into payment execution or broader console feature work from later phases.

</domain>

<decisions>
## Implementation Decisions

### Account model and tenancy
- Signing up should create a business or workspace-style Hive account, not just a standalone personal user profile.
- Phase 2 should make shared accounts actually usable, including the invite, join, and basic member-management flows needed for real multi-user tenancy, not just a future-proof data model.
- The account model must support both personal and business identity data from the start.
- The default account label shown in the UI should seed from the owner's personal name, even though the underlying account is a workspace entity.

### Verification gate
- Users who have not verified their email may enter a limited console instead of being fully blocked.
- The unverified experience should be mostly locked, with access limited to a view-only shell plus basic account maintenance: edit basic account info, resend verification email, and change email.
- Sensitive actions stay blocked until verification is complete, including team invites and API key creation.
- The console should show a persistent verification banner until email verification is complete.
- If a user remains unverified for an extended time, the limited-console policy stays the same indefinitely; Hive should keep reminding without escalating to a harder lockout.

### First-run console flow
- Use a hybrid first-run flow: a short setup step first, then the real console dashboard.
- The highest-priority first-run actions are verifying email and completing the basic account profile.
- While the user is still unverified, onboarding should feel paused because most of the console remains locked.
- Returning users should land on the main dashboard rather than being forced back into a setup-only flow or deep-linked to the last visited area.

### Account profile and billing identity capture
- Billing, legal-entity, and tax-profile collection should not happen during initial onboarding; Hive should ask for that data only when the user starts a payment- or invoice-related flow.
- Before any billing flow begins, the core account profile should stay minimal: owner name, login email, account display name, account type (personal or business), country, and state or province.
- Billing and tax forms should adapt based on whether the account context is personal or business.
- Missing billing or tax details should remain optional until checkout or invoicing; they should not block earlier console completion.

### Claude's Discretion
- Exact membership role granularity beyond making shared workspaces usable; a minimal owner/member model is acceptable unless planning reveals a stricter requirement.
- Exact onboarding checklist structure, dashboard information architecture, and verification-banner copy.
- Exact validation rules and field-level UX for basic profile, jurisdiction, and billing-identity forms.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase scope and product requirements
- `.planning/ROADMAP.md` § "Phase 2: Identity & Account Foundation" — Defines the phase goal, success criteria, and plan breakdown for auth, sessions, tenancy, and profile capture.
- `.planning/REQUIREMENTS.md` § "Authentication & Accounts" — Defines `AUTH-01`, `AUTH-02`, `AUTH-03`, and `AUTH-04`.
- `.planning/PROJECT.md` § "Context" — States that the launch console uses hosted Supabase auth and Supabase-managed Postgres, and that customers need billing and tax profile management in the console.
- `.planning/PROJECT.md` § "Constraints" — Locks hosted Supabase as the auth and primary relational platform and requires Docker-only development workflows.
- `.planning/STATE.md` § "Accumulated Context" — Carries forward the project-level constraints already accepted for current work.

### Architecture and stack guidance
- `.planning/research/SUMMARY.md` § "Recommended Stack" — Confirms hosted Supabase for auth and primary relational state plus Next.js for the developer console.
- `.planning/research/SUMMARY.md` § "Implications for Roadmap" — Explains why auth, tenancy, and durable account state must precede later billing and key-governance phases.
- `.planning/research/ARCHITECTURE.md` § "Component Responsibilities" — Defines the web console as a Next.js app with hosted Supabase auth and the control plane as the owner of account and billing policy state.
- `.planning/research/ARCHITECTURE.md` § "State Management" — Establishes Supabase Postgres as authoritative state with Redis only for hot caches and ephemeral data.
- `.planning/research/STACK.md` § "Recommended Stack" — Captures the locked platform choices for Supabase Hosted and Next.js.
- `.planning/research/STACK.md` § "Version Compatibility" — Notes that transactional flows should use direct Postgres access patterns rather than relying on generated REST layers.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- No application code exists yet; the most reusable assets are the planning and research documents that already define the auth stack, console stack, and control-plane boundaries.

### Established Patterns
- The repository is still greenfield, so there are no implementation-level UI or data-access patterns to preserve yet.
- Hosted Supabase is already locked as the authentication, session, and primary relational platform for v1.
- Docker-only local development is already a non-negotiable project constraint and should shape any Phase 2 tooling choices.
- The expected implementation split is a Next.js developer console plus Go control-plane services with direct transactional access to Supabase Postgres.

### Integration Points
- Phase 2 will establish the first authenticated web-console shell and session model that later console phases will build on.
- Phase 2 will define the account, membership, and profile tables or access patterns that later ledger, API key, payment, and invoice work depend on.
- Phase 2 decisions must leave clear hooks for later payment and invoice flows to collect richer business and tax data only when needed.

</code_context>

<specifics>
## Specific Ideas

- Hive should feel workspace-first even when the default account label starts from the owner's personal name.
- Unverified users should see the real dashboard shell, but in a mostly locked state with a persistent verification banner rather than being bounced out of the product.
- Early profile capture should include country plus state or province so legal jurisdiction is understood before billing starts.
- Billing and tax forms should branch based on personal versus business account context instead of forcing one universal form from day one.

</specifics>

<deferred>
## Deferred Ideas

- Advanced organization hierarchies, richer role matrices, and procurement-style approval flows beyond making shared workspace membership usable.
- Full billing, tax, invoice, and checkout profile capture before any payment or invoice workflow begins; Phase 2 only establishes the minimal pre-billing profile and the hooks for later collection.

</deferred>

---

*Phase: 02-identity-account-foundation*
*Context gathered: 2026-03-28*
