# Target versus current: chat shell captures, 2026-08-17

Paired captures of the interface Hive is aiming at and the interface Hive ships today,
collected so that the UX issues filed on 2026-08-17 can show the gap rather than describe it.

The specification these support is `spec-2026-08-17-target-ux-claude-desktop-parity` in the
project vault. That document, not this directory, is authoritative on what we build.

## What each image is

| File | Product | Surface |
|---|---|---|
| `target-chat-home.png` | Claude.ai, design reference | New chat, empty state: greeting and composer |
| `target-model-picker.png` | Claude.ai, design reference | Model menu open: four models with purpose lines, Effort and More models |
| `target-settings-rail.png` | Claude.ai, design reference | Settings, left rail only: two labelled groups |
| `target-sidebar-nav.png` | Claude.ai, design reference | Sidebar: panel switch and five labelled navigation rows |
| `current-chat-home.png` | Hive, chat-hive.scubed.co | Chat home, empty state |
| `current-model-picker.png` | Hive, chat-hive.scubed.co | Model picker open |
| `current-sidebar-nav.png` | Hive, chat-hive.scubed.co | Sidebar, expanded |
| `current-settings-general.png` | Hive, chat-hive.scubed.co | Settings, General pane |
| `current-settings-interface.png` | Hive, chat-hive.scubed.co | Settings, Interface pane |
| `current-agent-workspace.png` | Hive, chat-hive.scubed.co | Agent workspace, reached from the top right link |
| `current-signin.png` | Hive, chat-hive.scubed.co | Sign in page, signed out |

## How they were captured

The `target-*` and `current-*` browser captures other than `current-signin.png` were taken on
2026-08-17 by driving the owner's Chrome on Windows against each product live, signed in as the
owner on both. `current-signin.png` was taken the same day from a headless Chromium against the
same deployed host with no session, as the last frame of the cold load measurement recorded in
the load-time issue.

## The target images are a design reference, not our product

Every `target-*` image is Claude.ai, made by Anthropic. They appear here because the owner named
that product as the experience Hive is aiming at, so they are the reference the issues compare
against. Nothing in them is a Hive screen, a Hive claim, or a Hive asset.

## Redaction

The public-repository rule was applied before anything was committed: every image was opened and
inspected, and anything belonging to the owner personally was cropped out rather than obscured in
place. Specifically, `target-chat-home.png` and `target-model-picker.png` are cropped to the
greeting and the composer so the organization identifier above them is not reproduced;
`target-sidebar-nav.png` is cropped above the Pinned heading so a pinned private project is not
reproduced. One further target capture, of the settings General page, is deliberately absent: it
carried the owner's custom instructions text and a column of private conversation titles, and no
crop of it was worth publishing.
