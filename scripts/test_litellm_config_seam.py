#!/usr/bin/env python3
"""The repo's LiteLLM config must reach the running proxy.

Two independent mechanisms used to stop it, both measured on the demo box
rather than reasoned about:

  1. The shared config volume was seeded `if [ ! -f /etc/litellm/config.yaml ]`,
     so exactly once in the lifetime of the volume. The volume outlives
     container recreates by design, so on any box that had booted once, an
     edit to deploy/litellm/config.yaml never reached the proxy again. Routes
     added or repointed there were inert, silently, while the repository, the
     pull request and CI all looked correct. Observed live: the container's
     config carried a top-level general_settings key the repo's seed does not
     have, so the running file demonstrably was not the repo's file.

  2. The seed was bind-mounted as a single FILE. A single-file bind mount
     resolves to one inode at container start and keeps it, and git checkout
     replaces a file rather than editing it in place. Measured on the box by
     replacing a file the way git does: the file-mounted container still read
     the old content, a directory-mounted one read the new content. So even a
     forced reseed would have copied the pre-pull file.

The interesting part of the fix is what it must NOT do. control-plane owns
/etc/litellm/config.yaml at runtime: it merges the DB's model catalog over the
seed and restarts the container to apply it. So an unconditional copy on start
would overwrite, on that very restart, the config control-plane just wrote and
restarted for. The reconcile is therefore keyed on a checksum of the seed.

This file checks the wiring statically AND runs the entrypoint's real shell
logic against temporary directories, because the wiring assertions alone would
pass against a script that does the wrong thing.
"""

import hashlib
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
COMPOSE = ROOT / "deploy" / "docker" / "docker-compose.yml"
DEPLOY_WORKFLOW = ROOT / ".github" / "workflows" / "deploy-demo-box.yml"
SEED = ROOT / "deploy" / "litellm" / "config.yaml"

COMPOSE_TEXT = COMPOSE.read_text()
DEPLOY_TEXT = DEPLOY_WORKFLOW.read_text()


def _service_block(name: str) -> str:
    match = re.search(
        rf"^  {re.escape(name)}:\n(.*?)(?=^  [A-Za-z0-9._-]+:\s*$|\Z)",
        COMPOSE_TEXT,
        re.MULTILINE | re.DOTALL,
    )
    assert match, f"docker-compose.yml has no service named {name}"
    return match.group(1)


LITELLM = _service_block("litellm")


def _entrypoint_script() -> str:
    """The shell body of the litellm entrypoint, unescaped as compose hands it
    to the container: compose turns `$$` into a literal `$`."""
    match = re.search(
        r"^    entrypoint:\n"
        r"      - /bin/sh\n"
        r"      - -c\n"
        r"      - \|\n"
        r"(.*?)(?=^    [a-z]|\Z)",
        LITELLM,
        re.MULTILINE | re.DOTALL,
    )
    assert match, "could not find the litellm entrypoint script in docker-compose.yml"
    body = "\n".join(line[8:] for line in match.group(1).splitlines())
    return body.replace("$$", "$")


def test_the_seed_is_mounted_as_a_directory_not_a_single_file() -> None:
    """A single-file bind mount is pinned to the inode it resolved at start,
    and git replaces files rather than editing them, so a pull that changes the
    seed would not be visible inside the container at all."""
    assert "- ../../deploy/litellm:/seed:ro" in LITELLM, (
        "the litellm service does not mount deploy/litellm as a read-only "
        "DIRECTORY at /seed"
    )
    assert "/config.yaml:/seed/config.yaml" not in LITELLM, (
        "the litellm service bind-mounts the seed as a single file, which goes "
        "stale the moment a git checkout replaces it on the host"
    )


def test_the_entrypoint_reconciles_on_a_checksum_not_on_mere_existence() -> None:
    """Keyed on existence, the seed is copied once per volume and every later
    repository change is inert. Keyed on a checksum, a repository change is
    applied and a control-plane restart is not mistaken for one."""
    script = _entrypoint_script()
    assert "sha256sum /seed/config.yaml" in script, script
    assert ".seed.sha256" in script, (
        "the entrypoint does not record which seed it applied, so it cannot "
        "tell a changed seed from a control-plane restart"
    )
    # Recording the marker is what stops the copy from being unconditional.
    assert re.search(r">\s*/etc/litellm/\.seed\.sha256", script), script


def _run_entrypoint(root: Path) -> str:
    """Run the real entrypoint logic with /seed and /etc/litellm redirected
    into a temp tree, and `exec litellm ...` replaced by a marker echo."""
    script = _entrypoint_script()
    script = script.replace("/etc/litellm", str(root / "etc"))
    script = script.replace("/seed", str(root / "seed"))
    script = re.sub(r"^exec litellm .*$", "echo STARTED", script, flags=re.MULTILINE)
    assert "STARTED" in script, "the entrypoint no longer execs litellm"
    proc = subprocess.run(
        ["/bin/sh", "-c", script], capture_output=True, text=True, timeout=30
    )
    assert proc.returncode == 0, f"entrypoint failed: {proc.stderr}"
    return proc.stdout


def _fixture(tmp: Path, seed_text: str) -> Path:
    root = tmp / "root"
    (root / "seed").mkdir(parents=True, exist_ok=True)
    (root / "etc").mkdir(parents=True, exist_ok=True)
    (root / "seed" / "config.yaml").write_text(seed_text)
    return root


def test_the_entrypoint_seeds_an_empty_volume() -> None:
    """A fresh volume, a fresh box, or a recreated volume has to end up with a
    valid config before anything syncs, or LiteLLM cannot boot at all."""
    with tempfile.TemporaryDirectory() as tmp:
        root = _fixture(Path(tmp), "model_list: [seed-v1]\n")
        _run_entrypoint(root)
        live = root / "etc" / "config.yaml"
        assert live.exists(), "an empty volume was not seeded"
        assert live.read_text() == "model_list: [seed-v1]\n"


def test_a_changed_seed_reaches_the_volume() -> None:
    """The reported defect. A repository edit must land on a volume that was
    already seeded by an earlier boot."""
    with tempfile.TemporaryDirectory() as tmp:
        root = _fixture(Path(tmp), "model_list: [seed-v1]\n")
        _run_entrypoint(root)

        (root / "seed" / "config.yaml").write_text("model_list: [seed-v2]\n")
        _run_entrypoint(root)

        live = (root / "etc" / "config.yaml").read_text()
        assert live == "model_list: [seed-v2]\n", (
            "a changed repo seed did not reach the config volume, which is the "
            f"whole defect: {live!r}"
        )


def test_an_unchanged_seed_does_not_clobber_control_planes_merged_config() -> None:
    """The half an unconditional copy gets wrong.

    control-plane merges the DB's catalog over the seed, writes the result to
    this same path, and restarts the container to apply it. That restart runs
    this entrypoint again. If it copied unconditionally it would immediately
    destroy the merged config it was restarted to load, and the proxy would
    come up with no DB-managed routes."""
    with tempfile.TemporaryDirectory() as tmp:
        root = _fixture(Path(tmp), "model_list: [seed-v1]\n")
        _run_entrypoint(root)

        merged = "model_list: [seed-v1, from-the-database]\n"
        (root / "etc" / "config.yaml").write_text(merged)
        _run_entrypoint(root)  # stands in for control-plane's restart

        live = (root / "etc" / "config.yaml").read_text()
        assert live == merged, (
            "the entrypoint overwrote control-plane's merged config on a "
            f"restart where the seed had not changed: {live!r}"
        )


def test_the_recorded_marker_matches_the_seed_actually_applied() -> None:
    """The marker is the whole basis of the decision, so a marker that does not
    match the file that was copied would make the next boot either clobber
    forever or never reconcile again."""
    with tempfile.TemporaryDirectory() as tmp:
        text = "model_list: [seed-v1]\n"
        root = _fixture(Path(tmp), text)
        _run_entrypoint(root)
        marker = (root / "etc" / ".seed.sha256").read_text().strip()
        assert marker == hashlib.sha256(text.encode()).hexdigest(), marker


def _step_pos(job_text: str, step_name: str) -> int:
    match = re.search(
        rf"^      - name: {re.escape(step_name)}\s*$", job_text, re.MULTILINE
    )
    assert match, f"the deploy job has no step named {step_name!r}"
    return match.start()


def test_the_deploy_reconciles_the_seed_before_it_syncs_the_catalog() -> None:
    """`up -d` only recreates a service whose DEFINITION changed, and editing
    deploy/litellm/config.yaml changes no service definition, so a deploy that
    only changes that file never restarts the container and the entrypoint
    never runs.

    The catalog sync does restart it, after writing its merged config, which is
    the wrong order: the sync would merge the DB over the stale config, and the
    entrypoint would then notice the changed seed and overwrite that merged
    result with the raw seed, leaving the proxy with no DB-managed routes.

    Asserted as an order, because both steps being present is exactly the
    arrangement that gets this wrong."""
    match = re.search(
        r"^  deploy:\n(.*?)(?=^  [A-Za-z0-9_-]+:\s*$|\Z)",
        DEPLOY_TEXT,
        re.MULTILINE | re.DOTALL,
    )
    assert match, "deploy-demo-box.yml has no deploy job"
    deploy = match.group(1)

    reconcile = _step_pos(deploy, "Reconcile LiteLLM's config volume with the repo seed")
    sync = _step_pos(deploy, "Sync LiteLLM model list from the DB catalog (issue #713)")
    assert reconcile < sync, (
        "the deploy syncs the model catalog before reconciling the config "
        "volume with the repo seed, so the sync merges onto a stale base and "
        "the reseed then discards the merge"
    )


def test_the_seed_the_tests_describe_is_the_file_the_service_mounts() -> None:
    """Cheap guard against this whole file drifting onto a path that no longer
    exists, which would turn every assertion above into a check of nothing."""
    assert SEED.is_file(), f"{SEED} does not exist"
    assert "model_list" in SEED.read_text(), "the seed config has no model_list"


def main() -> int:
    assert shutil.which("sha256sum"), "these tests need coreutils sha256sum"
    tests = [v for k, v in sorted(globals().items()) if k.startswith("test_")]
    for test in tests:
        test()
    print(f"test_litellm_config_seam: ok ({len(tests)} checks)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
