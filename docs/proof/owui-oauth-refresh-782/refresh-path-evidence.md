# Issue 782 evidence: Open WebUI OAuth session destruction

Two capture sessions, both against the live Supabase project.

* 2026-08-06, against the demo box `chat-hive.scubed.co`: the three
  `0*.png` screenshots, which record the failure symptom.
* 2026-08-08, against a local stack running the patched image and the live
  Supabase project: everything under `after-fix/`, plus every claim in
  sections 1 to 5 below, each re-derived from scratch rather than carried
  over.

No token, secret, password or connection string appears in this file or in
any screenshot beside it.

## 1. The trigger rule

Elapsed time since sign-in. Not message count, and not which conversation.

Supabase issues the access token with a 3600 second lifetime, confirmed on the
live token response (`expires_in: 3600`, section 4). Open WebUI refreshes once
`now + 5 minutes` reaches `expires_at`:

```python
# utils/oauth.py, OAuthManager.get_oauth_token, v0.10.2
if (
    force_refresh
    or session.expires_at is None
    or datetime.now() + timedelta(minutes=5) >= datetime.fromtimestamp(session.expires_at)
):
```

So the refresh path is first entered about 55 minutes into a signed-in
session, and until then nothing is wrong. A conversation opened on an already
dead session fails from its first message, which is why the original report
read this as a second-message defect.

When the refresh returns nothing, the session is not left alone, it is
destroyed:

```python
refreshed_token = await self._refresh_token(session)
if refreshed_token:
    return refreshed_token
else:
    log.warning(f'Token refresh failed ... deleting session {session.id}')
    await OAuthSessions.delete_session_by_id(session.id)
    return None
```

From that moment `__oauth_token__` is empty, so
`deploy/docker/pipelines/hive_jwt_forward.py` injects no
`__metadata.upstream_auth`, and `OWUIUnwrap` in
`apps/edge-api/internal/auth/owui_unwrap.go` correctly refuses the request
with `missingUserTokenMessage`. The gateway is not the bug. It is the last
honest component in the chain.

## 2. Root cause, first half: no refresh token was ever issued

`OAUTH_SCOPES` was `openid email profile`. Supabase only issues a refresh
token when `offline_access` is requested, and
`_perform_token_refresh` gives up on its very first guard without one:

```python
if not token_data.get('refresh_token'):
    log.warning(f'No refresh token available for session {session.id}')
    return None
```

Every authorization ever recorded for this project, from `auth.oauth_authorizations`:

| scope | authorizations |
| --- | --- |
| `openid email profile` | 54 |
| `openid email profile offline_access` | 4 (all from this fix's verification runs) |

The capability was always there and was never asked for. The provider's own
discovery document advertises `offline_access` in `scopes_supported` and
`refresh_token` in `grant_types_supported`, both re-confirmed live.

## 3. Root cause, second half: the refresh spoke the wrong dialect

Adding the scope alone is not enough, because the two legs of the same OAuth
client authenticate differently.

The authorization code exchange goes through authlib, which defaults to
`client_secret_basic` when a client secret is set:

```python
# authlib 1.7.2, oauth2/client.py
if token_endpoint_auth_method is None:
    if client_secret:
        token_endpoint_auth_method = "client_secret_basic"
```

The refresh does not go through authlib at all. It hand builds the POST and
always puts the credentials in the form body, which is `client_secret_post`:

```python
refresh_data = {
    'grant_type': 'refresh_token',
    'refresh_token': token_data['refresh_token'],
    'client_id': client.client_id,
}
if hasattr(client, 'client_secret') and client.client_secret:
    refresh_data['client_secret'] = client.client_secret
```

Supabase enforces the registered method exactly. Sent to the live token
endpoint with the same deliberately invalid refresh token, so that a client
authentication failure is distinguishable from a token failure:

| client authentication | status | error | meaning |
| --- | --- | --- | --- |
| `client_secret_post`, what Open WebUI sends | 400 | `invalid_credentials`, "client is registered for 'client_secret_basic' but 'client_secret_post' was used" | rejected before the token is even looked at |
| `client_secret_basic`, what the patch sends | 400 | `refresh_token_not_found`, "Invalid Refresh Token" | client authenticated, then failed only on the deliberately invalid token |

Every OAuth client registered in this project is `client_secret_basic`,
checked live in `auth.oauth_clients`: the CI client, the demo box client, and
the Hive Chat client. None of them is registered for the dialect Open WebUI's
refresh can produce, so with the scope alone the refresh token would exist and
be refused anyway, and the session destroyed all the same.

There are TWO `_perform_token_refresh` implementations in that module, the SSO
`OAuthManager` and the MCP-facing `OAuthClientManager`, and both build the
request the same way. Both are patched.

## 4. The fix, verified on the real code path

One local Open WebUI built from `deploy/docker/Dockerfile.open-webui` with
both halves applied, a throwaway OAuth client registered
`client_secret_basic` exactly like production, real SSO login through the real
consent screen, and chat answered by a real gateway.

The consent screen itself lists the fourth scope, so the request reaching the
provider really did change:

> openid, email, profile, offline_access

The session Open WebUI then stored, read back out of the running container
through its own model layer:

```
token keys: access_token, expires_at, expires_in, id_token,
            issued_at, refresh_token, token_type, userinfo
has refresh_token: True
expires_in: 3600
```

### The timed run

One session, one conversation, one message every five minutes, past the
refresh window at 55 minutes and past the token expiry at 60. See
`after-fix/timed-run.log` for the full log and `after-fix/` for the frames.

RESULTS_PLACEHOLDER

## 5. What is deliberately NOT in this fix

`OAUTH_TOKEN_ENDPOINT_AUTH_METHOD` is not set to `client_secret_post`.

That would be the other way to make both legs agree, and it was the first
approach taken. It is rejected because it only works when the Supabase OAuth
client is also re-registered for `client_secret_post`, and registering those
clients is manual ops with no script in this repository behind it. The two
must then move together forever, in an order no test can enforce, on every
deployment including customer-hosted ones. Getting it wrong does not degrade
anything subtly: authlib would send a dialect the registration rejects and
sign-in would fail outright for everyone.

Patching the refresh to follow the method the client is already configured
with removes the coupling. It works against every client this project has
registered, needs no ops step, and still honours
`OAUTH_TOKEN_ENDPOINT_AUTH_METHOD` for a deployment that really is registered
for `client_secret_post`, so the two legs cannot silently disagree again.

## 6. Why no existing test caught this

Every phase-19 chat spec runs all of its turns inside one short session. The
multi-turn spec at `apps/web-console/e2e/phase-19/owui/02-chat-multi-turn.spec.ts`
budgets 360 seconds for the whole file and sends its two turns back to back;
360 seconds is also the longest budget anywhere in the suite. Nothing holds a
session open past the access token lifetime, and nothing edits `expires_at`,
so no test in this repository can reach the refresh path at all.

A test that sends one message, or several messages inside a minute, passes
against a build that locks every user out an hour later. That is why the
guards that ship with this fix are configuration and build invariants rather
than another chat spec.
