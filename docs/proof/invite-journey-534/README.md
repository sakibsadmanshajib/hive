# Invite journey proof (issues #534, #535, #536)

Captured against a running stack built from this branch: a control-plane and a
web-console container on their own docker network, pointed at the project's
Supabase, on non-conflicting host ports so no shared stack was touched. The
browser ran inside the same network in its own cookie jar.

Every screenshot has a capture overlay across the top printing that page's real
`window.location.href`, so the URL claims (the acceptance token surviving the
sign-in and sign-up bounce, and each feedback flag) are visible in the image.

| Shot | What it shows |
| --- | --- |
| `01-owner-members-invite-form-with-role.png` | Owner's members page: invite form now carries a Role selector, members are identified by email, and the sole owner's row states the real reason its role cannot change ("The workspace must keep at least one owner."). |
| `02-invite-sent-confirmation.png` | `?invited=1` renders "Invitation sent. They join this workspace once they accept." Previously the page never read its own `searchParams`, so this was invisible (#535). |
| `03-signed-out-invitee-bounced-to-signin-token-preserved.png` | A signed-out invitee opening the invitation link lands on sign-in with `next=/invitations/accept?token=...` intact. Before this change the token was dropped and acceptance was impossible (#534). |
| `04-signup-link-carries-the-token.png` | The "Create one" cross-link carries the same `next` value, so an invitee with no account keeps the token through sign-up. |
| `05-signup-submitted.png` | The sign-up form submitted for real. The shared Supabase project's signup email quota is exhausted and it refuses the address, which is an environment limit, not a code path: see the note below. |
| `06-invitee-reopens-link-bounced-to-signin.png` | Reopening the invitation link, still signed out, bounces to sign-in with the token preserved. |
| `07-acceptance-landed-after-signin-joined-confirmation.png` | After signing in from the invitation link, acceptance completes and lands on `/console/members?joined=1`. |
| `08-member-view-disabled-controls-state-the-real-reason.png` | A plain member's view: the disabled invite control says "Only workspace owners can invite teammates." rather than blaming email verification (#536), and the owner-only member list is explained instead of throwing the console error boundary. |
| `09-refused-grant-renders-as-an-alert.png` | A grant the control-plane refuses (a member posting the invite endpoint) renders as an alert: "You do not have permission to invite members." It used to be silent (#535). |
| `10-owner-sees-members-by-email-with-role-editors.png` | Owner's list after the invitee joined: every member shown by email (no raw UUIDs), each row with a role editor, the viewer's own row stating why it has none. |
| `11-role-updated-confirmation.png` | A role change applied through the console: "Role updated." plus the row now reading Owner. |
| `12-already-accepted-message.png` | Reusing a consumed token: "This invitation has already been accepted", pointing at the workspace switcher. No false "ask for a fresh link". |
| `13-expired-message.png` | A genuinely expired invitation (expiry moved into the past in the database): "This invitation has expired", with the one action that helps. |
| `14-wrong-account-message.png` | An invitation addressed to somebody else, opened while signed in as another user: "Signed in as the wrong account", naming the signed-in address and offering sign-out. |
| `15-already-a-member-message.png` | An invitation to a workspace the user already belongs to: "You are already in this workspace". This previously surfaced the membership unique-constraint violation as an opaque server error. |

`journey-log.txt` and `journey-log-part2.txt` list the URL each shot was taken
at, straight from the driver.

## Environment notes

- No mailer is wired in this environment, and the raw acceptance token is
  returned once and never stored, so the invitation the invitee opened was
  created through the same control-plane endpoint the console calls, capturing
  the token from the 201 response body.
- Account creation through the sign-up form could not complete: the shared
  Supabase project's signup email quota was already exhausted (`429 email rate
  limit exceeded`) and it rejects several throwaway domains outright. Shot 05
  records that refusal. The sign-up half of the token round trip is therefore
  proven up to and including the cross-link and the verification-email redirect
  target carrying the token (shot 04 plus the `emailRedirectTo` and
  `/auth/callback` unit tests), and the accounts used from there on were created
  with the admin API. The sign-in half of the round trip is proven end to end.
- Tenant membership (the `tenant_users` claim the access-token hook reads) was
  seeded for these accounts. That provisioning path is a separate concern from
  invitation acceptance and is not part of this change.
