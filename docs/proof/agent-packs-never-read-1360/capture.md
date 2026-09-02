# Issue #1360 proof: the pack reaching the agent's assembled system prompt

No screenshot and no live sandbox transcript: the agent-engine SIF is
linux/amd64 Apptainer only and cannot be built or launched on the WSL2 dev box
(CLAUDE.md section 4, deploy/apptainer/README.md), and no host launcher runs
here. What follows is the next strongest thing available off that box: the
vendored OpenHands SDK's own loader and prompt assembly, run against the
shipped pack laid out exactly as `engine.Launch` now lays it out inside
`/workspace`, printing the system-message section the sandboxed agent receives.

What is NOT proven here: that a live sandbox on the demo box loads it. That
needs a task run against the deployed launcher after this merges.

## How it was produced

```shell
docker run --rm -v <repo>:/w:ro -v <scratch>:/s python:3.13-slim sh -c \
  "cp -r /w/vendor/openhands/openhands-sdk /tmp/sdk \
   && pip install --no-cache-dir /tmp/sdk \
   && python /s/proof.py <pack-dir>"
```

`proof.py` copies the pack into an empty temp directory (what `Launch` does to
the session working directory), calls the SDK's own
`load_project_skills(work_dir)` (what `LocalConversation` calls when
`load_project_skills` is true), and prints
`AgentContext.get_system_message_suffix()`.

SDK version reported by the run: OpenHands SDK v1.36.1.

## Before (pack layout on origin/main)

Note this is already generous to the defect: on `origin/main` nothing is copied
into the working directory at all and `load_project_skills` is false, so the
real before state loads nothing whatsoever. This run instead shows what the old
layout would have produced even if both of those had been fixed.

```text
loaded project skills: [('agents', 'repo'), ('agents:skills/code-canvas', 'knowledge'), ('agents:skills/deck-generation', 'knowledge'), ('agents:skills/doc-layout', 'knowledge'), ('knowledge-work-pack', 'repo')]
```

The three skills load as path rules. Path rules force
`disable_model_invocation`, so `_partition_skills` drops them and the rendered
prompt has no `<available_skills>` section at all: the model is never told they
exist. Grep of the rendered output for `available_skills`: no match. The
`knowledge-work-pack` entry is the `.openhands/microagents` mirror, whose whole
body says it is an unread compatibility stub; this run is what showed it would
be injected into every prompt, and it is deleted in this PR.

## After (this branch)

```text
loaded project skills: [('agents', 'repo'), ('code-canvas', 'agentskills'), ('deck-generation', 'agentskills'), ('doc-layout', 'agentskills')]
```

Rendered system-message section, abridged to the two parts that matter:

```text
[BEGIN context from [agents]]
# Knowledge Work Pack

This pack runs inside the same Apptainer rootless sandbox
(apps/agent-engine/internal/sandbox) as the coding pack, at the identical
sandbox trust tier: both packs may run arbitrary shell, build, and test
commands inside the container. Nothing about this pack config grants it
narrower or broader sandbox permissions than the coding pack; the difference
is task framing and default tooling emphasis only.

## Scope

- Read and produce documents, slides, and structured artifacts in the
  mounted `/workspace` directory. Three skills ship with this pack
  (blueprint Wave 3, Step 3.2, issue #300); check whether the task at hand
  matches one before improvising a from-scratch approach:
  - `.agents/skills/doc-layout/SKILL.md` — contract/PDF page understanding via the
    `route-doc-vlm` vision route.
  - `.agents/skills/deck-generation/SKILL.md` — self-contained HTML slide deck
    generation, published through the artifacts API.
  - `.agents/skills/code-canvas/SKILL.md` — self-contained HTML/JS code preview,
    published through the artifacts API.
- When the task's instructions open with a `Skill: <name>` line, read that
  skill's `.agents/skills/<name>/SKILL.md` and follow it, before deciding on
  any other approach. Absent the tag, pick a skill from its description
  above when the task matches one.
- Run arbitrary shell commands where a knowledge-work task needs them:
  document conversion tools, template renderers, arbitrary build/test
  commands are not excluded by pack type.

## Constraints


<available_skills>
  <skill>
    <name>code-canvas</name>
    <description>Write a self-contained HTML page, widget, or visualization and publish it as a live preview artifact.</description>
  </skill>
  <skill>
    <name>deck-generation</name>
    <description>Outline a slide deck or presentation from the task content and publish it as an artifact.</description>
  </skill>
  <skill>
    <name>doc-layout</name>
    <description>Read, summarize, or extract fields from a scanned contract, PDF, or photographed form through the doc-layout vision route.</description>
  </skill>
</available_skills>
```

The pack's own AGENTS.md body is in `<REPO_CONTEXT>` (always active), and all
three skills are listed to the model with their descriptions and an
`invoke_skill(name=...)` contract.
