---
description: OpenWolf protocol enforcement — active on all files
globs: **/*
---

- Check .wolf/anatomy.md before read any project file
- Check .wolf/cerebrum.md Do-Not-Repeat list before generate code
- After write/edit files, update .wolf/anatomy.md + append .wolf/memory.md
- After user correction, update .wolf/cerebrum.md immediately (Preferences, Learnings, or Do-Not-Repeat)
- LEARN every interaction: discover convention, user preference, project pattern → add .wolf/cerebrum.md. Low threshold — doubt = log.
- BEFORE fix any bug/error: read .wolf/buglog.json for known fixes
- AFTER fix any bug, error, failed test, failed build, user-reported problem: ALWAYS log .wolf/buglog.json w/ error_message, root_cause, fix, tags
- Edit file >2x per session → likely bug → log .wolf/buglog.json
- User ask check/evaluate UI design: run `openwolf designqc` capture screenshots, read from .wolf/designqc-captures/
- User ask change/pick/migrate UI framework: read .wolf/reframe-frameworks.md, ask decision questions, recommend framework, execute w/ framework's prompt