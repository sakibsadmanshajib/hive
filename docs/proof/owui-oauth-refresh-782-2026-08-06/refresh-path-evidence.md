# Issue 782 evidence: Open WebUI OAuth session destruction

Captured 2026-08-06 against the live demo box (`chat-hive.scubed.co`), the live
Supabase project, and a local upstream Open WebUI v0.10.2 run against that same
project. No token, secret, password or connection string appears in this file
or in the screenshots beside it.

## 1. The trigger rule

Elapsed time since sign-in, not message count, and not which conversation.

Fresh SSO login at `22:48:25Z`, three messages back to back in one new
conversation. Screenshot `02-fresh-session-three-messages-answer.png`.

| message | outcome | elapsed since login |
| --- | --- | --- |
| ALPHA | answered | 5s |
| BRAVO | answered | 11s |
| CHARLIE | answered | 17s |

The same account driven from a browser state captured at `11:28`, whose
`oauth_id_token` carried `iat 11:28:53Z` and `exp 12:28:53Z`. Screenshot
`01-dead-session-first-and-second-message-both-fail.png`.

| message | outcome |
| --- | --- |
| first message in a brand new conversation | fails, signed-in-user-token error |
| second message | fails identically |

A conversation opened on an already-dead session fails from its first message.
That is why the original report read this as a second-message defect.

Access token lifetime measured two independent ways, both 3600 seconds: the
GoTrue password grant returns `expires_in: 3600`, and the id_token has `exp`
minus `iat` of exactly 3600. Open WebUI refreshes at 5 minutes before expiry,
so the refresh path is first entered about 55 minutes after sign-in.

## 2. Root cause, first half: no refresh token is ever issued

Project-wide, from `auth.sessions`:

| scopes | sessions | sessions carrying refresh material |
| --- | --- | --- |
| `openid email profile` | 27 | 0 |

Every authorize request recorded in `auth.oauth_authorizations` carries
`scope = 'openid email profile'`. The source is `deploy/docker/docker-compose.yml`,
which requested exactly that. Supabase's discovery document advertises
`offline_access` in `scopes_supported` and `refresh_token` in
`grant_types_supported`, so the capability was there and was never asked for.

With `offline_access` added, the token Open WebUI stores gains a
`refresh_token` key. Verified by decrypting the stored session inside a running
container:

```
token keys: access_token, expires_at, expires_in, id_token,
            issued_at, refresh_token, token_type, userinfo
refresh_token present: True
expires_in: 3600
```

## 3. Root cause, second half: the refresh speaks the wrong dialect

Adding the scope alone is not enough. With a refresh token present, ageing the
session into the refresh window and calling the exact function the chat request
calls still destroyed it:

```
session_survived:  false
token_returned:    false
```

Open WebUI's `_perform_token_refresh` does not use authlib. It hand builds the
refresh POST and always puts `client_id` and `client_secret` in the form body,
which is `client_secret_post`. The Supabase client is registered
`client_secret_basic`. The same stored refresh token, sent both ways to the
live token endpoint:

| client authentication | status | result |
| --- | --- | --- |
| `client_secret_post`, what Open WebUI sends | 400 | `invalid_credentials`, "client is registered for 'client_secret_basic' but 'client_secret_post' was used" |
| `client_secret_basic`, the registered method | 200 | new access token and new refresh token |

## 4. The fix, verified on the real code path

Upstream Open WebUI v0.10.2, live Supabase project, a throwaway OAuth client
registered for the test and deleted afterwards, real SSO login through the real
consent screen. Both halves applied: `offline_access` on the authorize request,
and `client_secret_post` on both the client registration and Open WebUI's token
endpoint auth method. The session was then aged into the refresh window and the
production function called:

```
session_survived:   true
token_returned:     true
has_access_token:   true
has_refresh_token:  true
new_expires_in:     3600
```

The session survives, receives a fresh access token, and receives a rotated
refresh token, so the next window is survivable too.

## 5. What is not proven here

The fix has not been exercised on the demo box. GitHub Actions was in a major
outage on the day of the fix, so the deploy workflow could not run. The
verification above was performed on a separate Open WebUI instance running the
same pinned image version against the same provider, which reaches the same
code path, but it is not the deployed stack. A post-deploy capture on
`chat-hive.scubed.co` is still owed.
