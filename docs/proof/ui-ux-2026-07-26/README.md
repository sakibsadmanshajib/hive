# Visual proof: cross-surface UI/UX pass, 2026-07-26

Before and after screenshots for the change described in the pull request that
added this directory. Captured with Playwright against the real stack running
from `deploy/docker/docker-compose.yml` (`--profile local --profile chat`),
signed in as a seeded owner account, not mocked and not hand-drawn.

| File | Surface | Shows |
|------|---------|-------|
| `01-chat-landing-before.png` / `-after.png` | Chat (Open WebUI), 1440px | Agent-workspace launcher appears in the header band; there was no entry point at all before |
| `02-owui-models-empty-before.png` / `-after.png` | Chat, Workspace > Models | Confused-face emoji replaced by the Hive mark drawn as an empty enclosure |
| `03-owui-knowledge-empty-before.png` / `-after.png` | Chat, Workspace > Knowledge | Same override on the second reported tab |
| `04-chat-landing-after-1024.png` | Chat, 1024px | Launcher still clear of Open WebUI's own header controls at the low end of its viewport gate |
| `05-owui-models-empty-after-dark.png` | Chat, dark theme | Launcher and empty-state mark both follow Open WebUI's theme |
| `06-agent-signin-before.png` / `-after.png` | Agent workspace sign-in | Unbranded scaffold to the console's auth treatment |
| `07-agent-tasks-before.png` / `-after.png` | Agent workspace task list | Chrome, page header, back-to-chat, semantic status, real empty state |
| `08-console-overview-1920-before.png` / `-after.png` | Developer console Overview, 1920px | Bounded measure, Next steps band, terminal rule, sans headings, mono metrics, and the light palette that the Tailwind v4 `@theme`-in-media-query bug had been suppressing |
| `09-console-overview-after-dark.png` | Console Overview, dark | Dark palette still correct now that it is conditional |

Capture notes, so the conditions are on the record:

- Open WebUI ran with `WEBUI_AUTH=false` for these captures so its Workspace
  tabs and the injected launcher could be photographed without an OAuth round
  trip. Neither the empty-state markup nor the launcher depends on auth mode.
- Its vector store was switched to the local default for the same run because
  the shared Supabase pooler was at its client ceiling. Chat and RAG are not
  under test here, only rendered chrome.
- The console "before" shots are dark because the console was unconditionally
  dark before this change. That is the defect, not a capture setting: the
  browser was told `prefers-color-scheme: light` in every shot.
