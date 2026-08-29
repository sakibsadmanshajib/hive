# Invitation delivery, and telling the truth about it: capture log

Date: 2026-08-29. Branch `fix/invitation-email-transport`. Pull request #1452,
issue #1440.

## No token in this directory, or in any image

Every token in every artifact here is the literal string
`REDACTED-NOT-A-REAL-TOKEN`, injected by the capture harness. No real
acceptance token was generated, rendered, screenshotted, or written at any
point. The redaction is by construction rather than after the fact, which
matters because nothing scans an uploaded image: PR #578 leaked four live
invite tokens exactly this way.

## Why this is not a live-stack capture

No full local stack can start from this sandbox, and that is a repo-wide
condition independent of this change. `.env` on this machine carries
`SUPABASE_URL`, `SUPABASE_DB_URL`, `S3_ENDPOINT` and
`NEXT_PUBLIC_SUPABASE_URL` as present but zero length, so `control-plane`
exits at boot on `storage unavailable: missing S3_ENDPOINT, S3_ACCESS_KEY,
S3_SECRET_KEY, S3_REGION` before it ever reaches the database check. Repointing
`.env` at the real data plane is not available either and is deliberate:
`deploy/docker/Caddyfile.supabase` serves `/rest/v1` and `/storage/v1` on the
in-network listener only, so no value writable from a sandbox reaches the
self-hosted data plane. See `.claude/skills/worktree-compose-stack.md`, which
documents this state and prescribes exactly the fallback used below.

## Method

Real React rendering, real client component state, real compiled CSS, real
browser. Only the network boundary to `control-plane` is mocked, at the same
seam the committed unit tests already mock through
`vi.mock("../../lib/control-plane/client")`.

1. `docker compose run --no-deps --build web-console npm run build` produced the
   real compiled Tailwind CSS chunk under `.next/static/chunks/`. Exit 0,
   TypeScript clean, and the route table lists the new
   `/api/console/members/invitations/revoke` handler.
2. A throwaway vitest file (`apps/web-console/tests/unit/__visual_proof__.test.tsx`,
   deleted before commit, not part of this PR's diff) rendered the real
   `MembersPage()` with the real `InviteTeammateForm` client component in the
   tree, stubbed `fetch` to return the response the deployment actually
   produces today (`delivered: false`, `delivery: "not_configured"`, plus the
   link), submitted the form, and serialised the resulting DOM. It asserted
   before writing anything that the banner does not match
   `/invitation sent|we emailed/i`, so the harness fails rather than
   screenshotting a lie.
3. That HTML was inlined with the compiled CSS chunk and opened in a real
   headless Chromium through Playwright, at 1280 wide, in both colour schemes.
4. The invitation email was rendered by the real `renderInvitation` code through
   a throwaway Go test (`zz_visual_proof_test.go`, also deleted before commit)
   and screenshotted the same way at 760 wide.

## Shot 1, `members-invited.png` and `members-invited-dark.png`

The invite panel after issuing an invitation on a deployment with no mail
delivery configured, which is every deployment today.

The banner reads: "The invitation for newhire@example.test is ready, but this
deployment has no mail delivery configured, so nothing was emailed. Pass the
link on yourself." It is warning toned, not success toned. Compare what this
surface said before the change, on `origin/main`:

    successMessage() -> "Invitation sent. They join this workspace once they accept."

rendered unconditionally on any non-throwing create, while no email transport
existed anywhere in the product and the acceptance token had already been
discarded by the proxy route.

Below the banner, the invitation link is shown in a read-only field with a Copy
control, and the warning under it says the link is bearer equivalent and is
shown once.

The card description above no longer asserts a send either. Before: "An email
invite is sent with a sign-in link." After: "Creating an invitation produces a
private link that joins this workspace with the role you pick. Where mail
delivery is configured we email that link as well, and either way the link is
shown to you here so you can pass it on yourself."

The same shot carries the second half of the fix. The members table now lists
outstanding invitations alongside members:

- `invitee@example.test`, role member, **Invited**, "Expires 2026-09-01"
- `lapsed@example.test`, role owner, **Expired**, "Expired 2026-08-20"

each with **New link** and **Withdraw**. Before the change neither row appeared
anywhere in the console at all, so an inviting user could not see, re-send or
revoke anything outstanding.

The dark shot is the same DOM under `prefers-color-scheme: dark`, included
because the warning tone is a new colour path on this page.

## Shot 2, `members-table.png`

The same page before an invitation is issued, showing the table alone.

## Shot 3, `email-light.png` and `email-dark.png`

The invitation email, rendered by the real template code.

Subject: `Join Acme Legal on Hive`. Sender display name comes from
`HIVE_MAIL_FROM_NAME`, which was set nowhere before this change.

One action, "Accept the invitation", as a button. The full URL repeated as
selectable text underneath for clients that strip the anchor. An explicit
expiry line. An explicit "if you were not expecting this" line. No images at
all, so nothing depends on remote content loading. The dark shot shows the
`prefers-color-scheme` block doing its job: the card recolours as a whole
rather than leaving dark text on a dark ground.

The plain-text alternative, rendered by the same call:

    Subject: Join Acme Legal on Hive

    ada@acme.example invited you to join Acme Legal on Hive as a member.

    Open this link to accept the invitation:
    https://console-hive.scubed.co/invitations/accept?token=REDACTED-NOT-A-REAL-TOKEN

    The link expires on 1 September 2026 at 09:30 UTC.

    If you were not expecting this invitation you can ignore this email. Nothing
    happens until you open the link.

    Hive

## What these shots do not prove

They do not prove delivery. Delivery is blocked on the Brevo account
activation, which is an owner action in a third party console: the relay
accepts authentication and the envelope, then refuses the body with
`502 5.7.0 Your SMTP account is not yet activated`. Alertmanager on the demo
box has logged 45,702 such failures and was still failing at 17:03 on
2026-08-29. That is exactly why the `not_configured` and `failed` branches are
the ones screenshotted here: they are what a user sees today, and making them
honest is the point of the change.

The `sent` branch is covered by tests rather than by a screenshot, on both
sides of the wire: `TestCreateInvitation_DeliveryReportsWhatActuallyHappened`
in `apps/control-plane/internal/accounts/service_invitation_delivery_test.go`
and `invite-outcome.test.ts` in `apps/web-console/lib/members/`.

## Commands

    docker compose run --no-deps --build web-console npm run build
    docker compose run --no-deps --build -v <out>:/out web-console \
      sh -c "npm run build; npx vitest run tests/unit/__visual_proof__.test.tsx"
    docker compose --profile tools run --rm toolchain \
      "cd /workspace && go test ./apps/control-plane/internal/accounts/ \
       -run TestZZVisualProofRenderInvitationEmail -count=1 -v"
    node shoot.mjs        # Playwright, 1280x900, light and dark
    node shoot-email.mjs  # Playwright, 760 wide, light and dark
