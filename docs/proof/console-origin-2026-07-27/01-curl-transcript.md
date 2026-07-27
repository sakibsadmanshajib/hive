# Redirect-origin fix: live before/after proof

Captured 2026-07-27 against running containers, not from unit tests.

Both console apps were run from their real Dockerfiles
(`deploy/docker/Dockerfile.web-console.prod`, `deploy/docker/Dockerfile.agent-console`),
which end in `CMD ["npm", "run", "start", "--", "--hostname", "0.0.0.0", "--port", "3000"]`,
behind the repo's real Caddy configs (`deploy/docker/Caddyfile.console`,
`deploy/docker/Caddyfile.owui`) on the pinned `caddy:2-alpine` image from
`docker-compose.yml`. Nothing about the harness is hand-written: the proxy that
stamps `X-Forwarded-Proto` and `X-Forwarded-Host` is the same config production
uses.

`BEFORE` containers were built from `origin/main`. `AFTER` containers were built
from this branch.

Both `AFTER` images were deliberately built with the **stale** value that
`.env.example` ships, `NEXT_PUBLIC_APP_URL=http://localhost:3000`, so the
transcript exercises the worst realistic misconfiguration rather than a
convenient one.

## A. web-console BEFORE

`Caddyfile.console`, `CONSOLE_DOMAIN=console.localhost`, `CONSOLE_EXTERNAL_SCHEME=http`.

```
$ curl -sI -H 'Host: console.localhost' 'http://localhost/auth/callback?code=bogus'
HTTP/1.1 307 Temporary Redirect
Location: http://0.0.0.0:3000/console
```

Same result through the second harness used for the browser capture, where Caddy
is published on port 3106 so the redirect chain is followable:

```
$ curl -sI 'http://localhost:3106/auth/callback?code=bogus'
HTTP/1.1 307 Temporary Redirect
Location: http://0.0.0.0:3000/console
```

The scheme tracks `X-Forwarded-Proto`, so a TLS-terminated deployment emits
`https://0.0.0.0:3000/...`, which is what
`https://console-hive.scubed.co/auth/callback` returned live. The host is the
defect; the scheme only changes which unreachable URL is produced.

## B. agent-console BEFORE

`Caddyfile.owui`, `HIVE_CHAT_EXTERNAL_SCHEME=https`.

```
$ curl -sI -H 'Host: chat-hive.scubed.co' 'http://localhost:3103/agent-workspace/auth/callback?code=bogus'
HTTP/1.1 307 Temporary Redirect
Location: https://0.0.0.0:3000/agent-workspace/auth/sign-in

$ curl -sI -H 'Host: chat-hive.scubed.co' 'http://localhost:3103/agent-workspace/auth/callback'
HTTP/1.1 307 Temporary Redirect
Location: https://0.0.0.0:3000/agent-workspace/auth/sign-in
```

This reproduces the reported live symptom byte for byte, and shows the two
halves of the bug family are independent: PR #438's `BASE_PATH` prefix is
present and correct in the path, while the origin is still the bind address.

## C. web-console AFTER

```
$ curl -sI -H 'Host: console.localhost' 'http://localhost/auth/callback?code=bogus'
HTTP/1.1 307 Temporary Redirect
Location: http://console.localhost/console

$ curl -sI 'http://localhost:3106/auth/callback?code=bogus'
HTTP/1.1 307 Temporary Redirect
Location: http://localhost:3106/console
```

The emitted host is the host the request actually arrived on, including its
port. It is not `0.0.0.0`, and it is not the stale `localhost:3000` baked into
the image, so both halves of the change are demonstrated at once.

## D. agent-console AFTER

```
$ curl -sI -H 'Host: chat-hive.scubed.co' 'http://localhost:3103/agent-workspace/auth/callback?code=bogus'
HTTP/1.1 307 Temporary Redirect
Location: https://chat-hive.scubed.co/agent-workspace/auth/sign-in

$ curl -sI -H 'Host: chat-hive.scubed.co' 'http://localhost:3103/agent-workspace/auth/callback'
HTTP/1.1 307 Temporary Redirect
Location: https://chat-hive.scubed.co/agent-workspace/auth/sign-in
```

Real origin, and the `/agent-workspace` prefix from PR #438 is retained.

## E. A spoofed X-Forwarded-Host does not steer the redirect

Sent with a client-supplied `X-Forwarded-Host: evil.test` against both AFTER
surfaces:

```
$ curl -sI -H 'Host: console.localhost' -H 'X-Forwarded-Host: evil.test' 'http://localhost/auth/callback?code=bogus'
HTTP/1.1 307 Temporary Redirect
Location: http://console.localhost/console

$ curl -sI -H 'Host: chat-hive.scubed.co' -H 'X-Forwarded-Host: evil.test' 'http://localhost:3103/agent-workspace/auth/callback'
HTTP/1.1 307 Temporary Redirect
Location: https://chat-hive.scubed.co/agent-workspace/auth/sign-in
```

Neither redirect goes to `evil.test`. Caddy's `reverse_proxy` replaces
`X-Forwarded-Host` with the real request host rather than passing a
client-supplied value through, so the forwarded-host branch is not reachable by
an external attacker behind either of these proxy configs. A deployment that
also sets a real `NEXT_PUBLIC_APP_URL` ignores the header entirely, which is the
posture PR #157 established and this change preserves.

## F. The route that already used the helper was already correct

Against the shared running stack, whose container has the correct
`NEXT_PUBLIC_APP_URL=https://console-hive.scubed.co`:

```
$ curl -sI -X POST -H 'Host: console.localhost' 'http://localhost:3005/auth/sign-out'
HTTP/1.1 303 See Other
Location: https://console-hive.scubed.co/auth/sign-in
```

`/auth/sign-out` is one of the two call sites PR #157 wired up. It resolves
correctly on the same deployment where `/auth/callback` emitted `0.0.0.0`, which
isolates the defect to the un-wired call sites rather than to the helper or the
environment.

## G. Middleware is not affected, and is deliberately left alone

```
$ curl -sI -H 'Host: console.localhost' 'http://localhost:3005/console'
HTTP/1.1 307 Temporary Redirect
Location: /auth/sign-in
```

Both apps' `middleware.ts` build redirect targets with
`new URL(path, request.url)`, the same expression that breaks the route
handlers. Next.js emits a **relative** `Location` for a same-origin middleware
redirect, so the bind address never reaches the client and the middlewares are
correct as written. `tools/lint-no-request-url-origin.mjs` therefore scopes
itself to route handlers and documents this exclusion, rather than flagging
working code.

## Browser capture

`03-before-browser-dead-ends-on-0000.png` and
`04-after-browser-lands-on-console.png` show the same request in a real browser
against the port-3106 harness, where the redirect chain can be followed
end to end.

Before, Chrome follows the 307 and dies on the bind address: "This site can't be
reached, 0.0.0.0 refused to connect, ERR_CONNECTION_REFUSED". The error page's
tab title is literally `0.0.0.0`.

After, the same URL completes the whole chain, `/auth/callback` to `/console` to
the middleware's unauthenticated redirect, and renders the console sign-in page.
