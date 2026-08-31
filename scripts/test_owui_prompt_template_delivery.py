#!/usr/bin/env python3
"""End-to-end proof that a configured prompt template reaches the model.

Adding a key to `deploy/docker/owui-patches/hive_rag_env_config.py`'s allowlist
proves nothing on its own. `scripts/test_owui_rag_env_config.py` exercises the
module directly, which is the fast guard and is what CI runs, but it stops at
"the reconcile wrote the row". The question that matters is the next one: does
setting the value change what a running deployment sends to a model. That is
the question issue #722 answered wrongly for a year, and it is the one a unit
test structurally cannot reach.

So this boots the real thing. It builds the pinned Open WebUI image with the
reconcile patch applied exactly as `deploy/docker/Dockerfile.open-webui` applies
it, boots it twice against ONE volume so the first-boot-wins trap is reproduced
rather than assumed, points its model endpoint at a capture server standing in
for the gateway, and reads the request body Open WebUI actually sent.

Seven assertions, in order. The first four are the ten reconciled templates; the
last three are the chat surface's own system prompt (issue #1596), which is a
different question and needs a real chat turn rather than a task call, because
the splice that applies it lives in `process_chat_payload`.

1. The trap is real. The first boot, with no variable set, persists a
   `task.title.prompt_template` row. That row is what makes a later compose
   change a silent no-op, and it is the reason this reconcile exists at all.
2. The baseline. That same unconfigured boot sends upstream's own default title
   prompt to the model, and does not send the proof text. This is the control,
   and it is what makes the run falsifiable: without it, an assertion that
   merely finds SOME text in the body would pass whatever was configured.
3. The environment wins on the second boot, over the row the first boot seeded.
4. The configured text is in the body the second boot sent to the model, and
   upstream's default is not. This is the assertion that fails if the value
   stops reaching a model for any reason at all, including reasons that have
   nothing to do with the allowlist.
5. The #1596 control, and the strongest shape a control can take: with nothing
   configured, a real chat turn reaches the model with NO system message at all.
   There is no upstream default here to be mistaken for the configured value.
6. With `HIVE_CHAT_SYSTEM_PROMPT` set, that same turn carries it as the system
   message the model receives.
7. Precedence, on a live turn: a request that also carries a Settings > General
   system message gets Hive's block FIRST and the user's text after it, so a
   user adds to the identity and capability statement and cannot delete it.

Both boots use one volume and one image and differ only by that variable, so
the printed before-and-after bodies are a controlled comparison rather than two
unrelated runs.

NOT a CI gate, deliberately, and not wired into any workflow. It pulls a
multi-gigabyte image and boots it twice; `scripts/test_owui_rag_env_config.py`
is the guard that runs on every pull request. This is the reproducible proof an
engineer runs when changing the reconcile, and the evidence a reviewer can
re-run rather than take on trust.

Run: python3 scripts/test_owui_prompt_template_delivery.py
Needs: docker, and network access to ghcr.io on the first run.
"""

import http.server
import json
import re
import secrets
import shutil
import socket
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
DOCKERFILE = REPO / "deploy" / "docker" / "Dockerfile.open-webui"
PATCHES = REPO / "deploy" / "docker" / "owui-patches"

IMAGE_TAG = "hive-owui-prompt-delivery-proof:local"
CONTAINER = "hive-owui-prompt-delivery-proof"
VOLUME = "hive-owui-prompt-delivery-proof-data"

# Distinctive enough that finding it in the outbound body cannot be a
# coincidence, and shaped like a real title prompt so upstream's own
# `title_generation_template` renders it without complaint.
PROOF_TEMPLATE = (
    "### Task:\n"
    "You are HIVEPROOF-TITLE-{run}. Reply with JSON {{\"title\": \"HIVEPROOF\"}}.\n"
    "<chat_history>\n{{{{MESSAGES:END:2}}}}\n</chat_history>\n"
)

# A fragment of upstream's own DEFAULT_TITLE_GENERATION_PROMPT_TEMPLATE, used
# only by the baseline assertion. Asserted to still be present in the vendored
# source, so an upstream rewording fails here loudly instead of quietly making
# the control assertion unfalsifiable.
UPSTREAM_DEFAULT_FRAGMENT = "Generate a concise, 3-5 word title"

# The chat surface's own system prompt (issue #1596), which is a different
# question from the ten templates above and needs a real chat turn rather than a
# task call to answer. Before that change the surface sent no system message at
# all, so the control assertion here is an ABSENCE, which is the strongest shape
# available: there is no upstream default to be mistaken for the configured
# value.
PROOF_CHAT_PROMPT = (
    "You are HIVEPROOF-CHAT-{run}, the deployment system prompt.\n"
    "Answer in one word.\n"
)

# Stands in for a user who filled in Settings > General, which upstream sends as
# a system message at position 0 of the request. Hive's block must end up in
# FRONT of it: a user must be able to add to the identity and capability
# statement and not to delete it.
PROOF_USER_SYSTEM = "USERPROOF-SETTINGS-SYSTEM-PROMPT"

# A second volume, used only by the seeding pair below, so it cannot disturb
# the delivery run's own volume.
SEED_VOLUME = "hive-owui-prompt-delivery-proof-seed"

# A fragment of upstream's DEFAULT_RAG_TEMPLATE, the one default among the ten
# that is real text rather than the empty string.
UPSTREAM_RAG_DEFAULT_FRAGMENT = "Respond to the user query using the provided context"


def log(message: str) -> None:
    print(f"[delivery-proof] {message}", flush=True)


def run(*argv: str, check: bool = True, capture: bool = True) -> str:
    proc = subprocess.run(
        argv,
        check=False,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.STDOUT if capture else None,
    )
    if check and proc.returncode != 0:
        raise RuntimeError(f"{' '.join(argv)} failed ({proc.returncode}):\n{proc.stdout}")
    return proc.stdout or ""


def pinned_image() -> str:
    """The image tag and digest Dockerfile.open-webui pins, read from the file
    so this proof can never drift onto a different backend than the deployment
    ships."""
    text = DOCKERFILE.read_text(encoding="utf-8")
    match = re.search(r"^FROM (ghcr\.io/open-webui/open-webui:\S+)$", text, re.MULTILINE)
    if not match:
        raise RuntimeError("could not find the pinned open-webui FROM line in Dockerfile.open-webui")
    return match.group(1)


def free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class CaptureServer:
    """Stands in for the Hive gateway. Serves one model and records every
    chat-completion body it is handed."""

    def __init__(self) -> None:
        self.port = free_port()
        self.bodies: list[dict] = []
        outer = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def _json(self, payload: dict, status: int = 200) -> None:
                raw = json.dumps(payload).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def do_GET(self) -> None:  # noqa: N802
                if self.path.rstrip("/").endswith("/models"):
                    self._json({"object": "list", "data": [{"id": "proof-model", "object": "model", "owned_by": "hive-proof"}]})
                else:
                    self._json({"ok": True})

            def do_POST(self) -> None:  # noqa: N802
                length = int(self.headers.get("Content-Length") or 0)
                raw = self.rfile.read(length)
                try:
                    outer.bodies.append(json.loads(raw))
                except json.JSONDecodeError:
                    outer.bodies.append({"_unparseable": raw.decode("utf-8", "replace")})
                self._json(
                    {
                        "id": "chatcmpl-proof",
                        "object": "chat.completion",
                        "created": int(time.time()),
                        "model": "proof-model",
                        "choices": [
                            {
                                "index": 0,
                                "finish_reason": "stop",
                                "message": {"role": "assistant", "content": '{"title": "HIVEPROOF"}'},
                            }
                        ],
                        "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
                    }
                )

            def log_message(self, *_args) -> None:
                return

        self.httpd = http.server.ThreadingHTTPServer(("0.0.0.0", self.port), Handler)
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def close(self) -> None:
        self.httpd.shutdown()


def build_image() -> None:
    """Apply the patches to the pinned image exactly as Dockerfile.open-webui
    does (its own COPY plus RUN pairs), and nothing else.

    Two of them: the reconcile that writes the persisted rows, and the chat
    system prompt splice that reads one of those rows on the chat path. Both,
    because the #1596 question is whether a configured value reaches a MODEL,
    and for the chat prompt the reconcile alone would only prove a database
    row."""
    base = pinned_image()
    log(f"building the proof image from {base}")
    dockerfile = (
        f"FROM {base}\n"
        "COPY hive_rag_env_config.py /app/backend/open_webui/utils/hive_rag_env_config.py\n"
        "COPY apply_rag_env_config_patch.py /tmp/apply_rag_env_config_patch.py\n"
        "RUN python3 /tmp/apply_rag_env_config_patch.py \\\n"
        "    && grep -q 'hive_rag_env_config' /app/backend/open_webui/config.py\n"
        "COPY apply_chat_system_prompt_patch.py /tmp/apply_chat_system_prompt_patch.py\n"
        "RUN python3 /tmp/apply_chat_system_prompt_patch.py \\\n"
        "    && grep -q 'hive.chat.system_prompt' "
        "/app/backend/open_webui/utils/middleware.py\n"
    )
    proc = subprocess.run(
        ["docker", "build", "-t", IMAGE_TAG, "-f", "-", str(PATCHES)],
        input=dockerfile, text=True, check=False,
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
    )
    if proc.returncode != 0:
        raise RuntimeError(f"docker build failed:\n{proc.stdout}")


def start(capture: CaptureServer, env: dict, volume: str = VOLUME) -> int:
    """Boot Open WebUI against a volume and return its host port."""
    port = free_port()
    argv = [
        "docker", "run", "-d", "--rm",
        "--name", CONTAINER,
        "--add-host", "host.docker.internal:host-gateway",
        "-p", f"127.0.0.1:{port}:8080",
        "-v", f"{volume}:/app/backend/data",
        "-e", "WEBUI_SECRET_KEY=hive-prompt-delivery-proof",
        "-e", "ENABLE_SIGNUP=true",
        "-e", "ENABLE_OLLAMA_API=false",
        "-e", "ENABLE_OPENAI_API=true",
        "-e", f"OPENAI_API_BASE_URL=http://host.docker.internal:{capture.port}",
        "-e", "OPENAI_API_KEY=proof-key-not-a-credential",
        "-e", "WEBUI_URL=http://127.0.0.1",
        "-e", "OFFLINE_MODE=true",
    ]
    for name, value in env.items():
        argv += ["-e", f"{name}={value}"]
    argv.append(IMAGE_TAG)
    run(*argv)

    deadline = time.time() + 180
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/health", timeout=3) as resp:
                if resp.status == 200:
                    return port
        except (urllib.error.URLError, ConnectionError, TimeoutError):
            time.sleep(2)
    logs = run("docker", "logs", "--tail", "60", CONTAINER, check=False)
    raise RuntimeError(f"Open WebUI never became healthy:\n{logs}")


def stop() -> None:
    run("docker", "rm", "-f", CONTAINER, check=False)


def persisted(key: str, volume: str = VOLUME):
    """Read one config row straight out of the volume, the same way this was
    read off the demo box. Sentinel object when the row is absent, so "absent"
    and "the empty string" can be told apart: they mean very different things
    here."""
    script = (
        "import json,sqlite3;"
        "c=sqlite3.connect('file:/data/webui.db?mode=ro',uri=True);"
        f"r=c.execute('select value from config where key=?',({key!r},)).fetchone();"
        "print(json.dumps({'present': r is not None, 'value': (json.loads(r[0]) if r else None)}))"
    )
    out = run(
        "docker", "run", "--rm", "-v", f"{volume}:/data", IMAGE_TAG,
        "python3", "-c", script,
    )
    return json.loads(out.strip().splitlines()[-1])


def api(port: int, path: str, payload: dict | None = None, token: str | None = None) -> dict:
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}{path}",
        data=json.dumps(payload).encode() if payload is not None else None,
        headers=headers,
        method="POST" if payload is not None else "GET",
    )
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as exc:
        # Named, because a bare "HTTP Error 404" from a script that calls four
        # different endpoints is the least useful failure this could produce.
        raise RuntimeError(
            f"{req.get_method()} {path} -> {exc.code}: "
            f"{exc.read().decode('utf-8', 'replace')[:500]}"
        ) from None


def signup(port: int) -> str:
    """Create the one throwaway account this run uses and return its token.

    The account lives inside a container whose volume is deleted at the end of
    this run, so it is not shared mutable state and no existing account's
    password is touched anywhere. Both facts matter: docs/live-test-auth.md
    forbids obtaining a session by setting or rotating a shared account's
    password, and nothing here does.

    Called once. Open WebUI refuses a second signup on the same database, and
    the token outlives a container restart because WEBUI_SECRET_KEY is fixed
    for the run, so the later boots reuse this one."""
    suffix = secrets.token_hex(6)
    auth = api(
        port,
        "/api/v1/auths/signup",
        {
            "name": "prompt delivery proof",
            "email": f"proof-{suffix}@hive-delivery-proof.invalid",
            "password": secrets.token_urlsafe(24),
        },
    )
    return auth["token"]


def title_request(capture: CaptureServer, port: int, token: str) -> dict:
    """Ask the running deployment to generate a chat title, which is the
    cheapest surface that makes Open WebUI send one of these templates to a
    model, and return the body the model actually received."""
    # Ask for the model list first. `generate_title` resolves form_data["model"]
    # against `request.app.state.MODELS`, which is populated lazily, so without
    # this the title call can 404 with MODEL_NOT_FOUND on a model the capture
    # server is perfectly happy to serve.
    models = api(port, "/api/models", token=token)
    ids = [m.get("id") for m in models.get("data", [])]
    if "proof-model" not in ids:
        raise RuntimeError(f"the capture server's model never reached Open WebUI; it listed {ids}")

    before = len(capture.bodies)
    api(
        port,
        "/api/v1/tasks/title/completions",
        {"model": "proof-model", "messages": [{"role": "user", "content": "what is a bee"}]},
        token=token,
    )
    deadline = time.time() + 30
    while time.time() < deadline and len(capture.bodies) == before:
        time.sleep(0.5)
    if len(capture.bodies) == before:
        raise RuntimeError("Open WebUI sent no request to the model at all")
    return capture.bodies[-1]


def chat_request(capture: CaptureServer, port: int, token: str, system: str | None = None) -> dict:
    """Send a real chat turn through the chat surface and return the body the
    model actually received.

    /api/chat/completions, not a task endpoint, because the chat system prompt
    is spliced in `process_chat_payload` and only the chat path runs that.
    `stream` is false so the capture server's plain JSON reply is accepted.

    `system`, when given, rides at position 0 exactly as the chat front end
    sends the Settings > General field (src/lib/components/chat/Chat.svelte),
    which is what makes the precedence assertion a live one rather than a unit
    test of a helper."""
    models = api(port, "/api/models", token=token)
    ids = [m.get("id") for m in models.get("data", [])]
    if "proof-model" not in ids:
        raise RuntimeError(f"the capture server's model never reached Open WebUI; it listed {ids}")

    messages = []
    if system:
        messages.append({"role": "system", "content": system})
    messages.append({"role": "user", "content": "what is a bee"})

    before = len(capture.bodies)
    api(
        port,
        "/api/chat/completions",
        {"model": "proof-model", "messages": messages, "stream": False},
        token=token,
    )
    deadline = time.time() + 30
    while time.time() < deadline and len(capture.bodies) == before:
        time.sleep(0.5)
    if len(capture.bodies) == before:
        raise RuntimeError("Open WebUI sent no chat request to the model at all")
    return capture.bodies[-1]


def outbound_system(body: dict) -> str:
    """The system message the model received, or "" when it received none.

    The empty string is the state the #1596 audit found and is a real answer
    here, not a parse failure."""
    for message in body.get("messages", []):
        if message.get("role") == "system":
            content = message.get("content")
            if isinstance(content, list):
                return "\n".join(
                    str(part.get("text", "")) for part in content if isinstance(part, dict)
                )
            return str(content or "")
    return ""


def outbound_text(body: dict) -> str:
    return "\n".join(str(m.get("content", "")) for m in body.get("messages", []))


def main() -> int:
    if shutil.which("docker") is None:
        log("SKIPPED: docker is not available on this host")
        return 0

    vendored = (
        REPO / "vendor" / "open-webui" / "backend" / "open_webui" / "config.py"
    ).read_text(encoding="utf-8")
    assert UPSTREAM_DEFAULT_FRAGMENT in vendored, (
        "upstream's DEFAULT_TITLE_GENERATION_PROMPT_TEMPLATE no longer contains "
        f"{UPSTREAM_DEFAULT_FRAGMENT!r}; the control assertion below would be "
        "unfalsifiable, so update the fragment rather than dropping the check"
    )

    run_id = secrets.token_hex(4)
    proof_template = PROOF_TEMPLATE.format(run=run_id)
    proof_chat_prompt = PROOF_CHAT_PROMPT.format(run=run_id)
    capture = CaptureServer()
    try:
        run("docker", "volume", "rm", "-f", VOLUME, check=False)
        run("docker", "volume", "rm", "-f", SEED_VOLUME, check=False)
        stop()
        build_image()

        # Seeding pair. `rag.template` is the ONE key of the ten whose upstream
        # default is real text rather than the empty string
        # (`os.getenv('RAG_TEMPLATE', DEFAULT_RAG_TEMPLATE)`), and os.getenv
        # returns that default only when the key is ABSENT. So how compose
        # passes the variable through decides what a FRESH volume seeds, and
        # the two shapes are measured here against real containers rather than
        # reasoned about.
        #
        # This pair is why the delivery run below uses the title template but
        # this one does not: the title template's own upstream default is
        # already "", so it is structurally incapable of showing this failure.
        # Picking it as the sole representative would have been a check that
        # cannot go red for the thing it appears to cover.
        log("seeding pair: what a fresh volume persists for rag.template")
        port = start(capture, {"RAG_TEMPLATE": ""}, volume=SEED_VOLUME)
        stop()
        defined_blank = persisted("rag.template", volume=SEED_VOLUME)
        assert defined_blank["value"] == "", (
            "a container that DEFINES RAG_TEMPLATE as empty was expected to "
            "seed a blank rag.template row, which is the hazard this guards "
            f"against; it seeded {str(defined_blank['value'])[:80]!r} instead. "
            "If upstream changed, this pair no longer proves anything."
        )
        log("  RAG_TEMPLATE defined-but-empty seeds a BLANK row (the hazard)")
        run("docker", "volume", "rm", "-f", SEED_VOLUME, check=False)

        # 1. First boot, nothing configured. This both seeds the row, which is
        #    the whole reason a compose change alone cannot reach an
        #    already-booted box, and captures the control: what this
        #    deployment sends to a model before anything is configured.
        log("boot 1 of 2: nothing configured, seeding the volume")
        port = start(capture, {})
        token = signup(port)
        seeded = persisted("task.title.prompt_template")
        assert seeded["present"], (
            "the first boot did not persist task.title.prompt_template at all, "
            "so the first-boot-wins trap this reconcile exists for is not "
            "reproduced and the rest of this proof would prove nothing"
        )
        log(f"  persisted row after boot 1: {seeded['value']!r}")

        # `rag.template` is the one key of the ten that can exhibit the
        # seeding defect at all, and therefore the one worth booting a
        # container to check. Upstream reads it as
        # `os.getenv('RAG_TEMPLATE', DEFAULT_RAG_TEMPLATE)`, so unlike the
        # other nine its default is real text rather than the empty string,
        # and os.getenv falls back to that default only when the key is
        # ABSENT from the environment. A compose passthrough that defines the
        # variable unconditionally, which `${VAR:-}` does, would therefore
        # make a fresh volume seed a blank row in place of that text.
        #
        # Nothing would break at the model, because `rag_template()`
        # substitutes the default for a blank value at request time. That
        # masking is the reason this assertion has to look at the persisted
        # row rather than at an outbound request: the defect is invisible in
        # behaviour and visible only here.
        #
        # The title template above cannot show any of this, because its own
        # upstream default is already the empty string. Picking it as the sole
        # representative would have been a test that structurally cannot fail
        # for the thing it appears to cover.
        seeded_rag = persisted("rag.template")
        assert seeded_rag["present"], "the first boot persisted no rag.template row at all"
        assert UPSTREAM_RAG_DEFAULT_FRAGMENT in (seeded_rag["value"] or ""), (
            "a fresh volume seeded rag.template as "
            f"{(seeded_rag['value'] or '')[:80]!r} instead of upstream's own default "
            "text. The container environment defines RAG_TEMPLATE when nobody "
            "set it, which defeats os.getenv's fallback. Compose must pass "
            "these through in its null form, not as ${VAR:-}."
        )
        log("  fresh volume seeded rag.template with upstream's real default text")

        before_body = title_request(capture, port, token)
        before_sent = outbound_text(before_body)

        # The #1596 control, and the finding it reproduces: with nothing
        # configured, a real chat turn reaches the model with NO system message
        # at all. Not a weak one, not upstream's: none. That is what the audit
        # claimed and what the rest of this half is measured against.
        before_chat = chat_request(capture, port, token)
        before_chat_system = outbound_system(before_chat)
        assert before_chat_system == "", (
            "the unconfigured deployment sent a system message after all, so the "
            f"#1596 control is not what this proof assumes:\n{before_chat_system[:2000]}"
        )
        log("  a chat turn reached the model with NO system message (the defect)")
        stop()
        assert UPSTREAM_DEFAULT_FRAGMENT in before_sent, (
            "the unconfigured deployment did not send upstream's own default "
            f"title prompt, so there is no baseline to compare against:\n{before_sent[:2000]}"
        )
        assert f"HIVEPROOF-TITLE-{run_id}" not in before_sent, (
            "the proof text was already in the outbound body before anything "
            "configured it, so this run can prove nothing"
        )
        log("  outbound request carried upstream's own default prompt")

        # 2. Same volume, same image, one variable added. The environment has
        #    to win over the row boot 1 seeded, and the new text has to be what
        #    the model receives.
        log("boot 2 of 2: template configured, over the row boot 1 seeded")
        port = start(
            capture,
            {
                "TITLE_GENERATION_PROMPT_TEMPLATE": proof_template,
                "HIVE_CHAT_SYSTEM_PROMPT": proof_chat_prompt,
            },
        )
        after = persisted("task.title.prompt_template")
        assert after["value"] == proof_template, (
            "the environment did not win over the persisted row: "
            f"{after['value']!r}"
        )
        log("  persisted row now carries the configured template")

        after_body = title_request(capture, port, token)
        after_sent = outbound_text(after_body)

        # The #1596 assertion. Same volume, same image, one variable added, and
        # a real chat turn: the configured prompt has to be the system message
        # the model receives.
        after_chat_row = persisted("hive.chat.system_prompt")
        assert after_chat_row["value"] == proof_chat_prompt, (
            "the environment did not reach the hive.chat.system_prompt row: "
            f"{after_chat_row}"
        )
        after_chat = chat_request(capture, port, token)
        after_chat_system = outbound_system(after_chat)
        assert f"HIVEPROOF-CHAT-{run_id}" in after_chat_system, (
            "the configured chat system prompt did not reach the model. Open "
            f"WebUI sent this system message:\n{after_chat_system[:2000]}"
        )
        log("  a chat turn CARRIED the configured system prompt to the model")

        # Precedence, on a live turn rather than in a helper: a user filling in
        # Settings > General adds to Hive's block and cannot delete it.
        both = chat_request(capture, port, token, system=PROOF_USER_SYSTEM)
        both_system = outbound_system(both)
        assert f"HIVEPROOF-CHAT-{run_id}" in both_system, both_system
        assert PROOF_USER_SYSTEM in both_system, both_system
        assert both_system.index(f"HIVEPROOF-CHAT-{run_id}") < both_system.index(
            PROOF_USER_SYSTEM
        ), (
            "the user's own system text came FIRST, so a user can bury or "
            f"contradict the identity and capability statement:\n{both_system[:2000]}"
        )
        log("  Hive's block stayed in front of the user's own system text")
        stop()
        assert f"HIVEPROOF-TITLE-{run_id}" in after_sent, (
            "the configured template did not reach the model. Open WebUI sent:\n"
            f"{after_sent[:2000]}"
        )
        assert UPSTREAM_DEFAULT_FRAGMENT not in after_sent, (
            "the outbound request still carried upstream's default title prompt "
            "alongside the configured one"
        )
        log("  outbound request to the model CARRIED the configured template")

        print()
        print("--- what the model received BEFORE, boot 1, nothing configured ---")
        print(json.dumps(before_body, indent=2))
        print()
        print("--- what the model received AFTER, boot 2, one variable set ---")
        print(json.dumps(after_body, indent=2))
        print()
        print("--- chat turn BEFORE, boot 1: the system message the model got ---")
        print(json.dumps(before_chat_system))
        print()
        print("--- chat turn AFTER, boot 2: the system message the model got ---")
        print(json.dumps(after_chat_system, indent=2))
        print()
        print("--- chat turn AFTER, with a user Settings > General prompt too ---")
        print(json.dumps(both_system, indent=2))
        print()
        print("ok: a configured Open WebUI prompt template reaches the model")
        print("ok: the Hive chat system prompt reaches the model, ahead of the user's")
        return 0
    finally:
        stop()
        run("docker", "volume", "rm", "-f", VOLUME, check=False)
        run("docker", "volume", "rm", "-f", SEED_VOLUME, check=False)
        capture.close()


if __name__ == "__main__":
    sys.exit(main())
