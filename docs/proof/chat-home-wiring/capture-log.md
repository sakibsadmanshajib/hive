# chat home wiring: greeting + quick-start chips render

Date: 2026-08-26. Captured by the chatwire builder against the image built
from this branch (hive-open-webui:chatwire, build log /tmp/chatwire-build.log,
BUILD_EXIT=0), run standalone with no compose file:

    docker run -d --name hive-chatwire-verify -p 127.0.0.1:18080:8080 \
      -e WEBUI_SECRET_KEY=chatwire-verify-only hive-open-webui:chatwire

First-run admin account created through the signup form (throwaway local
account on the container's own sqlite; credentials not recorded here and not
reused anywhere).

Route: / (post-signup landing). Method: Playwright MCP, chromium.

Assertions against the live DOM:

- .hv-greeting text: "Good morning, Chatwire Verify"
- [data-hive-quickstart] .hv-chip count: 4
- chip labels: Code, Write, Explain, Analyze

Screenshot: chat-home-greeting-chips.png (posted to the PR as a permanent
release asset via scripts/post-pr-visual-proof.sh, not committed to git).

Image build assertions, same build:

- npm run test:frontend: 14 files passed, including home-surface.test.ts 5/5
- final bundle check: "hive: shell present, removed surfaces absent", now
  also requiring hv-greeting and data-hive-quickstart in /app/build/_app/immutable

Console during capture: 1 error, the standalone container's expected
control-plane-dependent fetch failure (no control-plane token in a bare run);
no errors from the landing surface itself.
