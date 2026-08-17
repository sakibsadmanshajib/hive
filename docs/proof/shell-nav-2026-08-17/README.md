# Visual proof: the Hive shell, and the agent inside it

Captured 2026-08-17 against two locally built images, both from the same pinned Open WebUI
digest (`ghcr.io/open-webui/open-webui:v0.10.2@sha256:9fcea9c6...`), differing only in this
branch's diff. Same method as PR #909: two images, one A and one B, with the DOM read as well
as the pixels.

## How to reproduce

```bash
docker build -f deploy/docker/Dockerfile.open-webui -t hive-open-webui:hive-fork .
# baseline, from a checkout of main:
docker build -f deploy/docker/Dockerfile.open-webui -t hive-open-webui:main-baseline .
bash docs/proof/shell-nav-2026-08-17/run.sh
```

`run.sh` starts both Open WebUI containers, the agent workspace, and the repository's own
`Caddyfile.owui` in front of the branch container, then runs `capture.mjs` in the Playwright
image. Nothing in the stack holds a credential: the containers run with `WEBUI_AUTH=False`
and the agent workspace is built against placeholder public Supabase values.

## What each capture shows

| File | What it is |
|---|---|
| `01-before-chat-light.png`, `01-before-chat-dark.png` | `main`. An icon rail plus New Chat, Search and Workspace, no agent anywhere in the navigation, and "Agent workspace" as a text link in the top right corner |
| `02-after-chat-light.png`, `02-after-chat-dark.png` | This branch. Labelled Chats, Agents and Knowledge rows, the current row carrying a coral bar, the corner link gone, and the cream and charcoal brand palette in place of Open WebUI's grey |
| `03-after-agents-light.png`, `03-after-agents-dark.png` | The agent workspace reached from the sidebar. The sidebar is still there, Agents is the current row, and the panel fills the region in the shell's own theme |
| `04-after-rail-light.png` | The collapsed sidebar. Every destination survives; only the labels go |
| `05-after-focus-ring.png` | Keyboard focus on the Agents row, showing the two ring treatment |

## What the DOM says

`dom.json` is read from the same runs. The values that matter:

| Claim | Evidence |
|---|---|
| The old floating launcher is present on `main` and gone here | `before-light.launcherCount` 1, `after-light.launcherCount` 0 |
| The navigation carries three labelled destinations | `after-light.navRows` = Chats `/`, Agents `/agents`, Knowledge `/workspace/knowledge` |
| Rows are the specified size | `height` 32, matching `--hv-nav-row`; 36 square on the collapsed rail |
| The current destination is not signalled by colour alone | `ariaCurrent` = `page` moves from Chats to Agents between `/` and `/agents` |
| The brand typeface actually loads | `hankenLoaded` false on `main`, true here; body font is the Hanken stack |
| The agent panel is same origin and knows it is embedded | `agentPanel.sameOrigin` true, `embeddedFlag` 1, `themeFlag` `dark` on the dark capture |
| Motion follows the catalogue | row transition `0.12s`, which is `--hv-duration-fast` |
| Reduced motion removes it rather than shortening it | `reducedMotion` `0s` under `prefers-reduced-motion: reduce` |
| Touch targets clear the floor | `coarsePointer` 44, the row height under a coarse pointer |
| Focus is visible on any surface | `focus.boxShadow` is the two ring treatment: 2px canvas then 2px coral |

## What these captures do not show, stated plainly

The embedded panel renders the agent workspace's own sign-in screen, because this proof stack
has no Supabase session and that application still has a login of its own. That is today's
behaviour on the live box as well, and it is the third login that single sign-on removes; the
plan puts that work in the same epic. What the capture does prove about it is the part this
change is responsible for: the panel is same origin, it is inside the shell, it knows it is
embedded, and it is in the shell's theme rather than the desktop's.

The authenticated panel, with a real task list in it, is captured against the demo box after
this merges and deploys.
