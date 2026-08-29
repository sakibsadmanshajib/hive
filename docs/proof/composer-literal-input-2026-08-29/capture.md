# Composer literal input, wire-level capture, 2026-08-29

Issue #1399, pull request #1429.

## Why this capture exists in this shape

An independent QA pass on the same day typed straight quotes and a double
hyphen into the composer on `https://chat-hive.scubed.co`, captured the
outbound body, and found it byte identical to what was typed. That is a real
measurement and it contradicted the issue. Rather than argue from the source,
this capture settles it by running the defect and the fix as a controlled pair
where the only difference is the change under review.

## Method

Both arms run the same pinned upstream backend image that the deploy uses,
`ghcr.io/open-webui/open-webui:v0.10.2@sha256:9fcea9c6...`, with a locally
built frontend bind mounted over `/app/build`. That is exactly the arrangement
`deploy/docker/Dockerfile.open-webui` produces, where the backend is the pinned
image and only the frontend is ours.

The two arms differ in one thing only:

| Arm | Frontend built from |
| --- | --- |
| before | `origin/main`, which registers `@tiptap/extension-typography` |
| after | this branch, which does not |

Everything else is held constant: the same image digest, the same container
environment, the same capture script, the same payload, the same browser
(headless Chromium via Playwright on Linux), the same single keystroke-at-a-time
input method (`page.keyboard.type`, 25 ms delay, which dispatches real key
events; pasting would not fire input rules and proves nothing).

Captured at three points: the string typed, the composer DOM read back
immediately after typing, and the body of the outbound
`POST /api/chat/completions` intercepted with `page.on('request')`, which is
the literal bytes sent rather than the interface's account of itself.

Characters above U+007E are printed as codepoints so nothing is lost to the
display.

No credentials appear anywhere in this capture. Both arms run on localhost with
`WEBUI_AUTH=false`, so there is no token, no password and no query string
secret to redact. No shared account was touched and no password was set, reset
or rotated.

## Payload

```
PROBE1399 it's a "test" -- ok a != b f() -> int
```

## before, origin/main frontend, localhost:3398

```
url               http://localhost:3398/
typed             PROBE1399 it's a "test" -- ok a != b f() -> int
1 composer DOM    PROBE1399 itU+2019s a U+201CtestU+201D U+2014 ok a U+2260 b f() U+2192 int
2 outbound body   PROBE1399 itU+2019s a U+201CtestU+201D U+2014 ok a U+2260 b f() U+2192 int
```

Five substitutions, and the composer DOM and the request body agree with each
other and both differ from what was typed:

| Typed | Sent | Rule |
| --- | --- | --- |
| `'` | U+2019 | `closeSingleQuote` |
| `"` | U+201C and U+201D | `openDoubleQuote`, `closeDoubleQuote` |
| `--` | U+2014 | `emDash` |
| `!=` | U+2260 | `notEqual` |
| `->` | U+2192 | `rightArrow` |

The last two are worth noting on their own: neither appears in the issue, and
neither would have been fixed by the `configure()` call the issue suggested.

## after, this branch's frontend, localhost:3399

```
url               http://localhost:3399/
typed             PROBE1399 it's a "test" -- ok a != b f() -> int
1 composer DOM    PROBE1399 it's a "test" -- ok a != b f() -> int
2 outbound body   PROBE1399 it's a "test" -- ok a != b f() -> int
```

All three identical. Straight quotes, the apostrophe, the double hyphen, `!=`
and `->` all reach the request body as typed.

## What this establishes, and what it does not

Establishes: the defect is real, it is in the frontend, it is caused by the
extension named in the issue, and removing it fixes the outbound body and not
merely the display. The before arm is the reproduction; the after arm is the
fix; the substrate is identical.

Does not establish: why the QA pass on the deployed box saw clean output. This
capture cannot speak to that, because it did not run there. The likely
explanations are that the composer's rich text mode was off for that account
(`richText={$settings?.richTextInput ?? true}` is a per-user setting, and the
extension is registered only inside the `richText` branch, so with rich text
off there are no input rules to fire at all), or that the input method used did
not dispatch key events. Both are consistent with every observation on record
and neither changes the fix. Anyone wanting to close that question should
capture on the deployed box with the account's rich text setting recorded
alongside the result.

## Reproduction

```bash
# before arm
docker run -d --name owui1399before -p 3398:8080 \
  -e WEBUI_AUTH=false -e ENABLE_OLLAMA_API=false \
  -v <build-from-origin/main>:/app/build:ro \
  ghcr.io/open-webui/open-webui:v0.10.2@sha256:9fcea9c6...

# after arm, identical but with this branch's build
docker run -d --name owui1399after -p 3399:8080 ...

# then type into each and read the intercepted request body
```

## Unit guard

`vendor/open-webui/src/lib/hive/composer-literal-input.test.ts`, 5 tests. Runs
in the required check `Repo policy lints (tenant + audit)` and in the image
build. Verified green by running that lane's own script locally:

```
$ sh scripts/test-owui-hive-frontend.sh
 ✓ lib/hive/composer-literal-input.test.ts (5 tests) 260ms
 Test Files  19 passed (19)
      Tests  251 passed (251)
EXIT=0
```

The guard was also mutation tested rather than trusted. With the extension
re-added under a different name (`import SmartText from
'@tiptap/extension-typography'`, registered as `SmartText,`), the typing case
fails with `expected 'git push —force' to be 'git push --force'` and the module
guard fails too, so the guard detects a live extension rather than a literal
identifier.
