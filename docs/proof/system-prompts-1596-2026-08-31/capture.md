# Chat system prompt, Cowork identity, RAG framing: capture log, 2026-08-31

Issue #1596, first slice. Branch `feat/1596-system-prompts`. Spec:
`spec-2026-08-31-system-prompt-rebuild.md` in the project vault.

Three claims are proved here, in this order, because the third is the one the
issue actually asks for and the first two are what make it credible.

1. The reconcile writes the row, on a real container, and the boot log says so.
2. A real chat turn reaches a real model carrying the configured system prompt,
   and reaches it with NO system message at all when the variable is unset.
3. On screen, the answer a user gets changes, and it changes in the two ways the
   audit named: the provider stops being disclosed, and the model stops offering
   capabilities this product does not have.

No credential appears in any URL, screenshot or transcript below. Every account
used was created inside a throwaway container whose volume was deleted at the
end of the run, so no shared fixture account's password was set, reset or
rotated (`docs/live-test-auth.md`).

## 1. The row, and the boot line that proves it

The image is the pinned upstream backend with this pull request's two patches
applied exactly as `deploy/docker/Dockerfile.open-webui` applies them, built by
`scripts/test_owui_prompt_template_delivery.py`'s own `build_image()`. The
prompt text was read out of `.github/workflows/deploy-demo-box.yml` rather than
retyped, so what booted is what this pull request ships.

With `HIVE_CHAT_SYSTEM_PROMPT` set:

```
$ docker logs hive-1596-visual-proof | grep 'hive: reconciled'
hive: reconciled Open WebUI config from env: hive.chat.system_prompt=<1735 chars>,
rag.embedding_model=sentence-transformers/all-MiniLM-L6-v2
```

The same image, same command, variable unset, which is the control:

```
$ docker logs hive-1596-visual-before | grep 'hive: reconciled'
hive: reconciled Open WebUI config from env:
rag.embedding_model=sentence-transformers/all-MiniLM-L6-v2
```

The key is named with its length rather than its text, deliberately: this is the
longest of the eleven prompt keys and logging it verbatim would bury the line an
operator reads it for. `scripts/test_owui_chat_system_prompt.py` asserts that.

## 2. What the model received, on a real turn

`python3 scripts/test_owui_prompt_template_delivery.py`, extended in this pull
request to cover the chat surface. Exit 0. It boots one image twice against ONE
volume so the first-boot-wins trap is reproduced rather than assumed, points the
model endpoint at a capture server standing in for the gateway, and reads the
request body Open WebUI actually sent.

Boot 1, nothing configured. The system message the model received on a real
`/api/chat/completions` turn:

```
""
```

Not a weak prompt, not upstream's: none. That is the #1596 finding, reproduced
rather than quoted.

Boot 2, same volume, same image, one variable added:

```
"You are HIVEPROOF-CHAT-7cf048e5, the deployment system prompt.\nAnswer in one word."
```

Boot 2 again, this time with a system message at position 0 of the request,
which is exactly how the chat front end sends the Settings > General field
(`vendor/open-webui/src/lib/components/chat/Chat.svelte`):

```
"You are HIVEPROOF-CHAT-7cf048e5, the deployment system prompt.\nAnswer in one word.
USERPROOF-SETTINGS-SYSTEM-PROMPT"
```

Hive's block is first and the user's text follows it. A user adds to the identity
and capability statement; they cannot delete it.

The same run's ten-template half still passes unchanged: the configured title
template reached the model and upstream's default did not.

## 3. On screen, against a real model

Same patched image, `OPENAI_API_BASE_URL` pointed at a real provider so the
reply is a real model's, one free-tier model
(`minimax/minimax-m3:free`), the same question typed into the composer in a
browser both times. The only difference between the two runs is whether
`HIVE_CHAT_SYSTEM_PROMPT` is set.

Caveat stated plainly: the frontend in these two screenshots is upstream Open
WebUI's, not the Hive-branded build, because this change is entirely backend and
`Dockerfile.open-webui`'s frontend stage is orthogonal to it. What the
screenshots demonstrate is the model's answer, which is what the prompt governs.

URL, control run: `http://127.0.0.1:8597/c/33b145fa-d08c-4aab-8609-3e9a5bd89f8d`
URL, configured run: `http://127.0.0.1:8596/c/e17102f4-dd7f-452a-aa24-960a0746293b`

Both are loopback addresses on a container deleted at the end of the run, and
neither carries a credential in the query string.

Question, identical in both:

```
Which model am I talking to, and can you browse the web for me right now?
```

### Control, no system prompt (this is what main does today)

Screenshot: `proof-1596-chat-no-system-prompt-control.png`.

```
I'm MiniMax-M3, developed by MiniMax, so that's the model you're talking to right now.

As for browsing the web: I don't have a web browsing tool available in this
conversation. I can't open URLs, fetch live pages, or search the open internet in
real time.

What I can do that might help:
  Search your connected knowledge bases (uploaded docs, attached files, internal sources)
  Search your notes, memories, chats, and calendar
  Create automations that run on a schedule

If you let me know what you're trying to find out, I can likely help using one of
those, or point you to the right tool if there's a web-enabled assistant in your setup.
```

Two defects in one answer, neither of them hypothetical:

* The routed provider and model are disclosed by name to the customer. Nothing
  in the product asked it to, and nothing was stopping it.
* Three offers the product cannot honour. Notes, Calendar and Automations are
  turned OFF on this deployment by `FEATURE_CONFIG_ENV` in
  `deploy/docker/owui-patches/hive_rag_env_config.py`, and memory is not built
  (`.wolf/decisions.md` D-020). The model invented a capability list because it
  had no statement of what it actually had.

### Configured, with this pull request's prompt

Screenshot: `proof-1596-chat-system-prompt-live.png`.

```
You're talking to Hive, which routes each turn to one of several underlying
models, the specific one for this conversation is shown in the model picker on
your end.

I can't browse the web in this chat. No search or fetch tool is available to me
here, so I can only work with what you've attached or pasted into the
conversation. If you want live web results, share the text or screenshots and
I'll work from those.
```

The provider is not named, the routing is described accurately, the capability
answer is scoped to what was actually supplied for the turn, and no invented
surface is offered.

## 4. The Cowork suffix survives its own transport

Not a sandbox launch, and not claimed as one. What is proved here is the one
link in that chain with a real chance of mangling a multi-line value: the
installer writes it through `printf %q`, Bash's own shell-safe quoting, which
renders a multi-line string as `$'line1\nline2'`, and the unit entry point reads
it back with `set -a; . "$ENV_FILE"`. That is Bash ANSI-C quoting, which systemd
`EnvironmentFile=` would NOT understand; the entry point sources the file in
Bash instead, so it does.

Run against the exact text this pull request puts in the workflow, read out of
the workflow rather than retyped:

```
chars before: 1533
chars after:  1533
ROUND TRIP OK, byte for byte
first line after: <HIVE>
last line after:  </HIVE>
```

So a multi-line prompt containing angle brackets, asterisks and slashes reaches
the launcher process environment unchanged. From there
`apps/agent-engine/cmd/agent-engine/serve.go` reads it into engine
`Config.SystemMessageSuffix`, and two pre-existing Go tests already prove that
field reaches `agent_context.system_message_suffix` on the launch payload
(`TestSandboxEngine_Launch_SendsConfiguredSystemMessageSuffix`,
`TestStartConversation_SystemMessageSuffixReachesTheWire`).

Also re-run green, because both parse this workflow and the installer:
`scripts/test-agent-engine-restart-gate.sh` and
`scripts/test-agent-engine-health-probe.sh`.

## What this does NOT prove

Stated so nobody reads more into it than is here.

* The Cowork suffix half has no live SANDBOX capture. Launching one needs an
  Apptainer host and the WSL2 development box is not one. Section 4 proves the
  transport and the Go tests prove the wire, so what is unproved is narrower
  than "the suffix works": it is that the sandboxed agent, having received the
  suffix, then presents itself as Hive Cowork. First live Cowork turn after
  deploy is that evidence.
* The RAG half's untrusted framing is proved to be the row and the position
  (`RAG_SYSTEM_CONTEXT=true`, so the wrapper lands in a system message), not to
  be effective against any particular injection. It narrows issue #1571's
  framing gap and closes nothing.
* Nothing here is a capture against the deployed demo box. These are local
  containers running this branch's image. The deployed capture follows the
  merge.
