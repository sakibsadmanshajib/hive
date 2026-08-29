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

Four assertions, in order:

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
    """Apply the reconcile patch to the pinned image exactly as
    Dockerfile.open-webui does (its own COPY plus RUN, lines around 228)."""
    base = pinned_image()
    log(f"building the proof image from {base}")
    dockerfile = (
        f"FROM {base}\n"
        "COPY hive_rag_env_config.py /app/backend/open_webui/utils/hive_rag_env_config.py\n"
        "COPY apply_rag_env_config_patch.py /tmp/apply_rag_env_config_patch.py\n"
        "RUN python3 /tmp/apply_rag_env_config_patch.py \\\n"
        "    && grep -q 'hive_rag_env_config' /app/backend/open_webui/config.py\n"
    )
    proc = subprocess.run(
        ["docker", "build", "-t", IMAGE_TAG, "-f", "-", str(PATCHES)],
        input=dockerfile, text=True, check=False,
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
    )
    if proc.returncode != 0:
        raise RuntimeError(f"docker build failed:\n{proc.stdout}")


def start(capture: CaptureServer, env: dict) -> int:
    """Boot Open WebUI against the shared volume and return its host port."""
    port = free_port()
    argv = [
        "docker", "run", "-d", "--rm",
        "--name", CONTAINER,
        "--add-host", "host.docker.internal:host-gateway",
        "-p", f"127.0.0.1:{port}:8080",
        "-v", f"{VOLUME}:/app/backend/data",
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


def persisted(key: str):
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
        "docker", "run", "--rm", "-v", f"{VOLUME}:/data", IMAGE_TAG,
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
    capture = CaptureServer()
    try:
        run("docker", "volume", "rm", "-f", VOLUME, check=False)
        stop()
        build_image()

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

        before_body = title_request(capture, port, token)
        before_sent = outbound_text(before_body)
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
        port = start(capture, {"TITLE_GENERATION_PROMPT_TEMPLATE": proof_template})
        after = persisted("task.title.prompt_template")
        assert after["value"] == proof_template, (
            "the environment did not win over the persisted row: "
            f"{after['value']!r}"
        )
        log("  persisted row now carries the configured template")

        after_body = title_request(capture, port, token)
        after_sent = outbound_text(after_body)
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
        print("ok: a configured Open WebUI prompt template reaches the model")
        return 0
    finally:
        stop()
        run("docker", "volume", "rm", "-f", VOLUME, check=False)
        capture.close()


if __name__ == "__main__":
    sys.exit(main())
