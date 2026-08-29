#!/usr/bin/env python3
"""Self-check for the seam between the application stack and the self-hosted
Supabase data plane.

Why this exists
---------------
On the demo box the two halves ran as two separate compose projects, `hive` and
`hivesupabase`, from two separate checkouts. Two projects means two default
networks, so `getent hosts caddy-supabase` from inside control-plane returned
nothing and every Supabase-facing variable was unreachable no matter what it was
set to. The fix is that both halves are ONE compose project, layered as
`-f docker-compose.yml -f docker-compose.enterprise.yml`, which works only
because of a handful of properties that are invisible at a glance and silent
when broken:

  * both files declare the same project name, so the services share one default
    network and service DNS resolves;
  * neither file puts any of these services on a named network, which would
    split them again;
  * the `selfhost` profile covers exactly the Supabase data-plane services, so a
    deployment can start the data plane under the application profiles without
    also starting the rest of the enterprise surface;
  * edge-api reads its JWKS over https from the gateway and mounts the exported
    certificate authority, and it exits at boot if that first refresh fails;
  * GoTrue's issuer and the issuer edge-api compares against derive from the
    same variable, because a mismatch is not a boot error, it is every token
    being rejected;
  * Open WebUI's pgvector DSN comes from the libpq flavour, never the pgx one.
    A pgx-only parameter in a libpq DSN fails the whole connection, which is
    what killed a container and took chat down for fifty minutes.
  * migrations run somewhere that can actually reach the stack's database, and
    prove they reached THAT database. The data plane publishes no host port, so
    a GitHub-hosted runner cannot reach it at all, yet the migrate job reported
    success anyway by connecting to the hosted project the old secrets still
    named. Nothing went red; the database the application reads simply stopped
    receiving schema changes.
  * every psql in the deploy workflow goes through scripts/stack-psql.sh, whose
    default network is the compose project's own. A second, hand-rolled
    `docker run` lands on the default bridge network, where the data plane's
    hostname does not resolve, and the failure reads as a broken database.

Every one of those fails silently, or fails somewhere far from its cause, so
each gets an assertion here. No framework, no docker daemon, no network: this
reads the two compose files as text.

Run: python3 scripts/test_selfhost_supabase_seam.py
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
BASE = ROOT / "deploy" / "docker" / "docker-compose.yml"
ENTERPRISE = ROOT / "deploy" / "docker" / "docker-compose.enterprise.yml"
DEPLOY_WORKFLOW = ROOT / ".github" / "workflows" / "deploy-demo-box.yml"

# The Supabase data plane: the services that must answer before any application
# service can authenticate, read the database or store an object.
DATA_PLANE = {
    "supabase-db",
    "supabase-auth",
    "supabase-rest",
    "supabase-storage",
    "supabase-init",
    "caddy-supabase",
    "supabase-ca-export",
}

SERVICE_RE = re.compile(r"^  ([A-Za-z0-9][A-Za-z0-9._-]*):\s*$")
TOP_LEVEL_RE = re.compile(r"^([A-Za-z0-9][A-Za-z0-9._-]*):\s*$")


def split_services(text: str) -> dict:
    """Return {service name: block text} for the top-level `services:` mapping.

    Indentation based rather than a YAML parse, matching the other script
    self-checks in this directory, which deliberately depend on nothing outside
    the standard library.
    """
    lines = text.splitlines()
    services = {}
    in_services = False
    current = None
    buf = []
    for line in lines:
        top = TOP_LEVEL_RE.match(line)
        if top:
            if current is not None:
                services[current] = "\n".join(buf)
                current, buf = None, []
            in_services = top.group(1) == "services"
            continue
        if not in_services:
            continue
        svc = SERVICE_RE.match(line)
        if svc:
            if current is not None:
                services[current] = "\n".join(buf)
            current, buf = svc.group(1), []
            continue
        if current is not None:
            buf.append(line)
    if current is not None:
        services[current] = "\n".join(buf)
    return services


def project_name(text: str) -> str:
    match = re.search(r"^name:\s*(\S+)\s*$", text, re.MULTILINE)
    return match.group(1) if match else ""


def profiles_of(block: str) -> set:
    """The profile names a service block declares, empty set when it declares none."""
    out = set()
    seen_profiles = False
    for line in block.splitlines():
        if re.match(r"^    profiles:\s*$", line):
            seen_profiles = True
            continue
        if seen_profiles:
            item = re.match(r"^      - (\S+)\s*$", line)
            if item:
                out.add(item.group(1))
                continue
            if line.strip():
                break
    return out


def depends_on_of(block: str) -> set:
    """The service names a service block's `depends_on:` mapping actually names.

    Scanning every six-space key in the whole block was wrong twice over: an
    unrelated nested key such as `healthcheck:` parsed as a dependency, and
    deleting the `depends_on:` line while leaving its children in place still
    satisfied the non-empty guard that was supposed to stop the check going
    vacuous. So the mapping is sliced first, and an absent `depends_on:` returns
    the empty set rather than the whole block's keys.
    """
    match = re.search(r"^    depends_on:\s*$", block, re.MULTILINE)
    if not match:
        return set()
    rest = block[match.end():]
    end = re.search(r"^    \S", rest, re.MULTILINE)
    body = rest[: end.start()] if end else rest
    return set(re.findall(r"^      ([A-Za-z0-9][A-Za-z0-9._-]*):\s*$", body, re.MULTILINE))


def env_value(block: str, key: str) -> str:
    """The right-hand side of one `KEY: value` line in a service's environment."""
    match = re.search(
        r"^      " + re.escape(key) + r":\s*(.+?)\s*$", block, re.MULTILINE
    )
    return match.group(1) if match else ""


BASE_TEXT = BASE.read_text()
ENT_TEXT = ENTERPRISE.read_text()
DEPLOY_TEXT = DEPLOY_WORKFLOW.read_text()
BASE_SERVICES = split_services(BASE_TEXT)
ENT_SERVICES = split_services(ENT_TEXT)


def test_the_parser_actually_found_the_services() -> None:
    """A silent parse failure would make every assertion below vacuously true,
    which is the exact shape of check this repo keeps shipping by accident."""
    assert "edge-api" in BASE_SERVICES, sorted(BASE_SERVICES)
    assert "control-plane" in BASE_SERVICES, sorted(BASE_SERVICES)
    assert "open-webui" in BASE_SERVICES, sorted(BASE_SERVICES)
    assert DATA_PLANE <= set(ENT_SERVICES), sorted(set(ENT_SERVICES))


def test_one_compose_project_across_both_files() -> None:
    """Two project names means two default networks and no service DNS between
    them, which is the failure this seam exists to fix."""
    assert project_name(BASE_TEXT) == "hive", project_name(BASE_TEXT)
    assert project_name(ENT_TEXT) == "hive", project_name(ENT_TEXT)


def test_no_named_network_splits_the_seam() -> None:
    """Sharing one project only shares one network while nothing overrides it.
    A `networks:` key on any of these services, or a top-level `networks:`
    block, re-splits them, and the symptom is an unresolvable service name
    rather than a compose error."""
    # Either form, block or flow, with or without a trailing comment, and
    # network_mode alongside networks. A regex anchored on end-of-line missed
    # `networks: [supa]` and `networks:  # comment` completely, which is the
    # shape of an assertion that cannot fail.
    top_level = re.compile(r"^networks:(\s|$)", re.MULTILINE)
    per_service = re.compile(r"^    (networks|network_mode):(\s|$)", re.MULTILINE)
    for name, text in (("base", BASE_TEXT), ("enterprise", ENT_TEXT)):
        assert not top_level.search(text), name
    watched = DATA_PLANE | {"edge-api", "control-plane", "open-webui"}
    for source in (BASE_SERVICES, ENT_SERVICES):
        for svc, block in source.items():
            if svc in watched:
                found = per_service.search(block)
                assert not found, f"{svc}: {found.group(0) if found else ''}"


def test_selfhost_profile_covers_exactly_the_data_plane() -> None:
    """`selfhost` is what lets the demo box run the data plane underneath the
    `local` and `chat` application profiles. Too narrow and the flip starts an
    incomplete Supabase; too wide and it drags in unrelated services (today
    `ollama`, which is `enterprise` only and would take memory on a box that
    has none to spare)."""
    with_selfhost = {
        svc for svc, block in ENT_SERVICES.items() if "selfhost" in profiles_of(block)
    }
    # The expectation is derived from the file rather than from the list at the
    # top of this module, so a service ADDED to the enterprise file without the
    # profile fails here. Comparing only against the constant could not catch
    # that, because the constant would still equal itself.
    introduced = set(ENT_SERVICES) - set(BASE_SERVICES)
    assert with_selfhost == introduced, sorted(with_selfhost ^ introduced)
    # And that set is still the data plane this module documents, so a genuine
    # addition has to be considered here rather than sliding in silently.
    assert introduced == DATA_PLANE, sorted(introduced ^ DATA_PLANE)
    # Every one of them keeps `enterprise` too, so a genuine enterprise install
    # is unaffected by the addition.
    for svc in DATA_PLANE:
        assert "enterprise" in profiles_of(ENT_SERVICES[svc]), svc
    # Nothing in the base file joins the profile: it names the data plane, and
    # the data plane lives entirely in the enterprise file.
    for svc, block in BASE_SERVICES.items():
        assert "selfhost" not in profiles_of(block), svc


def test_edge_api_reads_its_jwks_over_tls_with_the_exported_authority() -> None:
    """edge-api refuses a plain-http JWKS URL and exits at boot if its first
    refresh fails, so both halves of this are load bearing: the https URL, and
    the one-file certificate authority export it verifies the chain against."""
    block = ENT_SERVICES["edge-api"]
    jwks = env_value(block, "SUPABASE_JWKS_URL")
    assert "https://caddy-supabase/" in jwks, jwks
    assert "http://caddy-supabase/" not in jwks, jwks
    ca = env_value(block, "SUPABASE_JWKS_CA_FILE")
    assert ca, "edge-api has no SUPABASE_JWKS_CA_FILE default"
    ca_path = ca.split(":-")[-1].rstrip("}")
    mount_dir, _, ca_name = ca_path.rpartition("/")
    assert ca_name, ca_path
    assert re.search(
        r"^      - supabase-ca:" + re.escape(mount_dir) + r":ro\s*$",
        block,
        re.MULTILINE,
    ), block
    # The mount is a directory and the variable names a file inside it, so
    # matching the directory is not enough on its own: the one-shot export has
    # to publish that exact filename or edge-api opens nothing.
    export = ENT_SERVICES["supabase-ca-export"]
    assert re.search(r"install -m 0644 .* /out/" + re.escape(ca_name), export), (
        f"supabase-ca-export does not publish {ca_name}, which SUPABASE_JWKS_CA_FILE names"
    )


def test_the_issuer_edge_api_checks_is_the_issuer_gotrue_stamps() -> None:
    """A mismatch here raises nothing at boot. GoTrue issues tokens, edge-api
    rejects all of them, and the only visible symptom is that authentication
    stopped working. Both sides must derive from one variable."""
    gotrue = env_value(ENT_SERVICES["supabase-auth"], "GOTRUE_JWT_ISSUER")
    external = env_value(ENT_SERVICES["supabase-auth"], "API_EXTERNAL_URL")
    edge = env_value(ENT_SERVICES["edge-api"], "SUPABASE_JWT_ISSUER")
    assert "ENTERPRISE_AUTH_EXTERNAL_URL" in gotrue, gotrue
    assert "ENTERPRISE_AUTH_EXTERNAL_URL" in external, external
    assert "ENTERPRISE_AUTH_EXTERNAL_URL" in edge, edge
    # Substring presence is not agreement: both sides can mention the variable
    # and still resolve differently, which is exactly how this drifts. GoTrue's
    # issuer and its external URL must be the SAME expression, and edge-api's
    # must fall back to it with no other default in between.
    assert gotrue == external, (gotrue, external)
    fallback = re.fullmatch(r"\$\{SUPABASE_JWT_ISSUER:-(.+)\}", edge)
    assert fallback and fallback.group(1) == gotrue, (edge, gotrue)


def test_open_webui_pgvector_dsn_comes_from_the_libpq_flavour() -> None:
    """Open WebUI's pgvector store speaks libpq through psycopg2, and libpq
    fails the whole connection on an unknown option rather than ignoring it. The
    pgx flavour carries `pool_max_conns` and `default_query_exec_mode`, so
    handing it over here is what produced `invalid dsn: invalid connection
    option "pool_max_conns"` and a fifty minute chat outage."""
    dsn = env_value(BASE_SERVICES["open-webui"], "PGVECTOR_DB_URL")
    assert "SUPABASE_DB_POOL_URL_LIBPQ" in dsn, dsn
    assert not re.search(r"SUPABASE_DB_POOL_URL(?!_LIBPQ)", dsn), dsn


def test_storage_accepts_the_s3_credentials_the_consumers_sign_with() -> None:
    """Storage verifies an S3 request by recomputing the SigV4 signature from
    S3_PROTOCOL_ACCESS_KEY_ID and S3_PROTOCOL_ACCESS_KEY_SECRET. With neither
    set it refuses every request with 403 AccessDenied and "Missing S3 Protocol
    Access Key ID or Secret Key Environment variables", which is a 500 on POST
    /v1/files, on every Batches object and on RAG document upload while the
    container stays healthy and the bucket rows exist (issue #1282).

    They must come from the SAME two variables edge-api and control-plane sign
    with. A separate pair would let the signing half and the verifying half
    drift with no boot error on either side, and the only symptom is a 403 from
    a service that looks configured."""
    storage = ENT_SERVICES["supabase-storage"]
    key_id = env_value(storage, "S3_PROTOCOL_ACCESS_KEY_ID")
    secret = env_value(storage, "S3_PROTOCOL_ACCESS_KEY_SECRET")
    assert "${S3_ACCESS_KEY" in key_id, key_id
    assert "${S3_SECRET_KEY" in secret, secret
    for consumer in ("edge-api", "control-plane"):
        block = BASE_SERVICES[consumer]
        assert "${S3_ACCESS_KEY" in env_value(block, "S3_ACCESS_KEY"), consumer
        assert "${S3_SECRET_KEY" in env_value(block, "S3_SECRET_KEY"), consumer


def test_the_storage_s3_prefix_matches_the_prefix_the_gateway_strips() -> None:
    """The consumers sign the path they send to the gateway, and the gateway's
    `handle_path` strips its prefix before Storage sees the request. Storage
    puts S3_PROTOCOL_PREFIX back before recomputing the signature, so an empty
    or mismatched value fails every request with SignatureDoesNotMatch, which
    reads as a wrong key rather than a wrong path. Three places have to agree:
    this variable, S3_ENDPOINT, and the Caddyfile route."""
    prefix = env_value(ENT_SERVICES["supabase-storage"], "S3_PROTOCOL_PREFIX")
    assert prefix == "/storage/v1", prefix
    caddy = (ROOT / "deploy" / "docker" / "Caddyfile.supabase").read_text()
    assert f"handle_path {prefix}/*" in caddy, prefix
    # The endpoint the consumers are told to use, in the file that documents it.
    example = (ROOT / ".env.example").read_text()
    assert f"S3_ENDPOINT=http://caddy-supabase{prefix}/s3" in example


def test_no_hosted_supabase_host_is_hardcoded_in_the_data_plane() -> None:
    """The data plane IS the replacement for the hosted project. A hosted
    hostname appearing here is either a stale copy-paste or a repointing that
    silently sends one consumer back out to the internet."""
    for line in ENT_TEXT.splitlines():
        bare = line.strip()
        if bare.startswith("#"):
            continue
        # One pattern, not two asserts: `pooler.supabase.com` CONTAINS the
        # substring `supabase.co`, so a plain-substring check for the project
        # host fires first and the pooler check below it could never be reached.
        # A word boundary on the domain also stops `supabase.community` and the
        # like from reading as a hosted project.
        found = re.search(r"\.supabase\.com?\b", bare)
        assert not found, line


def test_loading_the_enterprise_file_requires_activating_its_profile() -> None:
    """Passing `-f docker-compose.enterprise.yml` without `enterprise` or
    `selfhost` does not merely leave the data plane stopped, it invalidates the
    WHOLE project: `edge-api` carries no profile of its own, so compose refuses
    every command with `depends on undefined service`. Confirmed on the demo box
    while building this seam.

    That is only survivable while every service the override blocks depend on is
    inside the data-plane profile set, so one profile activation covers all of
    them. A new dependency outside that set would need its own profile flag and
    would break the documented invocation with no warning."""
    for svc in ("edge-api", "control-plane"):
        depends = depends_on_of(ENT_SERVICES[svc])
        assert depends, f"{svc} override declares no depends_on, so this check is vacuous"
        outside = depends - DATA_PLANE
        assert not outside, f"{svc} depends on {sorted(outside)}, outside the profile set"


def test_open_webui_oidc_discovery_uses_the_browser_facing_origin() -> None:
    """Open WebUI fetches the discovery document server side, but the browser
    then resolves every endpoint inside it, so a compose service name there kills
    login at the redirect with nothing in any log naming the cause. The variable
    has to be separable from control-plane's in-network SUPABASE_URL, while
    still defaulting to it so hosted Supabase is unaffected."""
    value = env_value(BASE_SERVICES["open-webui"], "OPENID_PROVIDER_URL")
    assert "SUPABASE_PUBLIC_URL" in value, value
    assert value.startswith('"${SUPABASE_PUBLIC_URL:-'), value
    assert "SUPABASE_URL" in value, value


def _compose_flags() -> str:
    """The one flag definition every step in the deploy workflow uses."""
    match = re.search(
        r"^  HIVE_COMPOSE_FLAGS: >-\n((?:^    .*\n)+)", DEPLOY_TEXT, re.MULTILINE
    )
    assert match, "deploy-demo-box.yml has no HIVE_COMPOSE_FLAGS block"
    return " ".join(line.strip() for line in match.group(1).splitlines())


def test_the_deploy_invocation_and_the_compose_project_agree() -> None:
    """The seam only exists for a command that resolves BOTH compose files. A
    deploy step that resolves only the base file resolves a different project,
    so it reads a stack missing every Supabase service and reports green over
    it, and the one step carrying `--remove-orphans` deletes them outright.

    Both halves are asserted, because either one alone is worse than useless:
    the file without a profile makes compose refuse every command
    (`depends on undefined service`), and the profile without the file activates
    nothing. The profile name is checked against what the data-plane services
    actually carry rather than against a literal, so renaming the profile in the
    compose file without updating the workflow fails here."""
    flags = _compose_flags()
    assert "-f docker-compose.yml" in flags, flags
    assert "-f docker-compose.enterprise.yml" in flags, flags

    declared = {
        p for svc in DATA_PLANE for p in profiles_of(ENT_SERVICES[svc])
    }
    common = set.intersection(*(profiles_of(ENT_SERVICES[svc]) for svc in DATA_PLANE))
    assert common, f"the data plane shares no profile: {declared}"
    active = {m.group(1) for m in re.finditer(r"--profile (\S+)", flags)}
    assert active & common, (
        f"the deploy activates {sorted(active)}, none of which covers the whole "
        f"data plane (needs one of {sorted(common)})"
    )

    # Every dependency the override blocks name has to be covered by that same
    # profile, or the invocation is refused outright rather than merely
    # incomplete.
    for svc in ("edge-api", "control-plane"):
        deps = depends_on_of(ENT_SERVICES[svc])
        assert deps, f"{svc} override declares no depends_on, so this check is vacuous"
        assert deps <= DATA_PLANE, f"{svc} depends on {sorted(deps - DATA_PLANE)}"


def test_no_deploy_step_spells_its_own_compose_flags() -> None:
    """Four steps in this workflow run docker compose, and each used to rebuild
    the flag list by hand. A fifth step added later with a copied-and-trimmed
    list is the recurrence this guards: it would read a different project and
    report green over a box missing services, which is indistinguishable from
    healthy in a log.

    So the rule is not "the flags are right somewhere", it is that no step
    spells them at all."""
    # Backslash continuations are folded FIRST. Without that, a step spelling
    # its flags on the next line matches as a bare `docker compose \\`, which
    # carries no --env-file, no --profile and no -f, so the offenders filter
    # below drops it as harmless. That step would read a different project and
    # report green over a box missing services, which is the exact recurrence
    # this test claims to catch.
    folded = re.sub(r"\\\n\s*", " ", DEPLOY_TEXT)
    invocations = re.findall(r"docker compose[^\n)]*", folded)
    # Prose in comments mentions `docker compose exec` and `docker compose ps`;
    # only lines that are actually run matter, and those are the ones that pass
    # flags or subcommands rather than sitting inside a sentence.
    offenders = [
        i for i in invocations
        if ("--env-file" in i or "--profile" in i or " -f " in i)
        and "$HIVE_COMPOSE_FLAGS" not in i
    ]
    assert not offenders, offenders
    # And the definition is genuinely used, so this test cannot pass by the
    # workflow having no invocations at all.
    used = DEPLOY_TEXT.count("$HIVE_COMPOSE_FLAGS")
    assert used >= 4, f"only {used} steps use the shared definition"


def _job_block(name: str) -> str:
    """The text of one job in the deploy workflow.

    Job keys sit at two-space indent and every line inside a job is deeper than
    that, so the next two-space key is the end of this job.
    """
    match = re.search(
        rf"^  {re.escape(name)}:\n(.*?)(?=^  [A-Za-z0-9_-]+:\s*$|\Z)",
        DEPLOY_TEXT,
        re.MULTILINE | re.DOTALL,
    )
    assert match, f"deploy-demo-box.yml has no job named {name}"
    return match.group(1)


def _step_block(job_text: str, step_name: str) -> str:
    """The text of one step, by its `name:`."""
    match = re.search(
        rf"^      - name: {re.escape(step_name)}\n(.*?)(?=^      - (?:name|uses):|\Z)",
        job_text,
        re.MULTILINE | re.DOTALL,
    )
    assert match, f"no step named {step_name!r}"
    return match.group(1)


def test_migrations_run_where_the_stacks_database_is_reachable() -> None:
    """The data plane binds no host port, so nothing outside the compose network
    can reach it. A migrate job on a GitHub-hosted runner therefore cannot
    apply anything to the database this deployment uses, and the failure is
    invisible: the SUPABASE_DB_* secrets still named a reachable hosted project,
    so the job applied every migration there and reported success while the
    live database drifted.

    Both halves are asserted. Running on the box is not enough on its own if the
    old secret set is still what names the target, and dropping the secrets is
    not enough if the job still runs where it cannot connect."""
    block = _job_block("migrate")
    assert "runs-on: [self-hosted, hive-demo]" in block, block[:200]
    assert "ubuntu-latest" not in block, "the migrate job cannot reach the data plane from a hosted runner"
    for name in (
        "SUPABASE_DB_HOST",
        "SUPABASE_DB_PORT",
        "SUPABASE_DB_USER",
        "SUPABASE_DB_NAME",
        "SUPABASE_DB_PASSWORD",
    ):
        assert f"secrets.{name}" not in block, (
            f"the migrate job reads secrets.{name}, which names a database "
            "independently of the one the stack uses"
        )
    assert "SUPABASE_DB_URL" in block, (
        "the migrate job must derive its target from the value the stack itself "
        "connects with"
    )


def test_the_migration_target_is_asserted_to_be_the_stacks_database() -> None:
    """An identity assertion, not a comment. system_identifier comes from initdb
    and is unique per cluster, so two connections that agree on it are talking
    to the same server.

    What makes it able to fail is that the two sides are named by unrelated
    inputs: the target by SUPABASE_DB_URL out of the box's .env, the stack by
    compose project resolution of the running container. Assert both sides are
    still independent, and that an empty answer from either fails rather than
    comparing equal to the other empty answer."""
    block = _job_block("migrate")
    step = _step_block(block, "Assert the migration target is the database this stack uses")
    assert "pg_control_system()" in step, step
    assert '"$PSQL_BIN"' in step, "the target side must connect the way the migration does"
    assert "exec -T supabase-db" in step, "the stack side must come from the running container"
    assert 'if [ "$target" != "$stack" ]' in step, step
    # Not a count of `exit 1`, which a later change that adds guards inflates
    # until turning one guard advisory no longer trips it. That is exactly what
    # happened here once: the threshold version of this assertion passed with
    # the empty-target guard mutated to `exit 0`. The invariant is per message:
    # every ::error:: in this step aborts.
    messages = re.findall(r"::error::", step)
    aborting = re.findall(r'echo "::error::[^\n]*"\n\s*exit 1\n', step)
    assert messages, step
    assert len(aborting) == len(messages), (
        f"{len(messages) - len(aborting)} of the {len(messages)} error messages "
        "in the identity step do not abort, so the step reports a mispointed "
        "database and carries on"
    )
    # And both sides still have an emptiness guard of their own, so the step
    # cannot be reduced to the mismatch comparison alone, where two empty
    # answers compare equal.
    assert len(re.findall(r"returned no system_identifier", step)) >= 2, step
    live = [
        line for line in block.splitlines() if not line.strip().startswith("#")
    ]
    assert not any("continue-on-error:" in line for line in live), (
        "the assertion cannot be advisory"
    )


def test_no_deploy_step_reaches_the_database_without_the_wrapper() -> None:
    """scripts/stack-psql.sh is the single answer to "how does a command on the
    box reach the stack's database". The deploy job's price assertion already
    ran psql in a container and still broke, because that container was on the
    default bridge network where `supabase-db` does not resolve. A second
    hand-rolled invocation is that failure again, and it reads as a broken
    database rather than as a wiring mistake.

    The one exemption is psql run INSIDE the database container itself, which
    needs no network at all."""
    folded = re.sub(r"\\\n\s*", " ", DEPLOY_TEXT)
    offenders = []
    for line in folded.splitlines():
        stripped = line.strip()
        if stripped.startswith("#") or "psql" not in stripped:
            continue
        if "stack-psql.sh" in stripped or "PSQL_BIN" in stripped:
            continue
        if "docker compose" in stripped and "exec -T supabase-db" in stripped:
            continue
        offenders.append(stripped)
    assert not offenders, offenders
    # And the wrapper is genuinely EXECUTED, not merely mentioned. Counting
    # occurrences in the whole file counts the comments explaining it, so that
    # count stays high while both real invocations are deleted.
    migrate = _job_block("migrate")
    assert re.search(
        r"^      PSQL_BIN: \$\{\{ github\.workspace \}\}/scripts/stack-psql\.sh\s*$",
        migrate,
        re.MULTILINE,
    ), "the migrate job does not point PSQL_BIN at the wrapper"
    price = _step_block(
        _job_block("deploy"),
        "Assert model catalog prices agree with the model LiteLLM will call",
    )
    assert re.search(
        r"^\s*rows=\$\(\.\./\.\./scripts/stack-psql\.sh\b",
        price,
        re.MULTILINE,
    ), "the price assertion does not run the wrapper"


def test_the_wrapper_default_network_is_the_compose_project_network() -> None:
    """The wrapper's default network is derived, not guessed: compose names a
    project's implicit network `<project>_default`. Renaming the project without
    following it here would put every psql on a network with no database on it,
    and the error would name a hostname rather than a project."""
    wrapper = (ROOT / "scripts" / "stack-psql.sh").read_text()
    match = re.search(r"HIVE_STACK_NETWORK:-([A-Za-z0-9_.-]+)", wrapper)
    assert match, "stack-psql.sh has no HIVE_STACK_NETWORK default"
    project = project_name(ENT_TEXT)
    assert project, "docker-compose.enterprise.yml declares no project name"
    assert match.group(1) == f"{project}_default", (
        f"wrapper defaults to {match.group(1)!r} but the compose project is "
        f"{project!r}, whose default network is {project}_default"
    )


def test_the_wrapper_forwards_every_parameter_the_deriver_maps() -> None:
    """derive-pooler-dsn.py turns a DSN query parameter into a PG* variable, and
    scripts/stack-psql.sh is what carries PG* variables into the container. A
    variable mapped by one and not forwarded by the other is dropped in
    silence: the connection still opens, just without the sslmode or the
    statement timeout the DSN asked for."""
    deriver = (ROOT / "scripts" / "derive-pooler-dsn.py").read_text()
    block = re.search(r"QUERY_PARAM_ENV = \{(.*?)\}", deriver, re.DOTALL)
    assert block, "derive-pooler-dsn.py has no QUERY_PARAM_ENV mapping"
    mapped = set(re.findall(r'"(PG[A-Z_]+)"', block.group(1)))
    assert mapped, block.group(1)
    wrapper = (ROOT / "scripts" / "stack-psql.sh").read_text()
    forwarded = set(re.findall(r"-e (PG[A-Z_]+)", wrapper))
    missing = mapped - forwarded
    assert not missing, f"stack-psql.sh does not forward {sorted(missing)}"


RETENTION_CHECK = ROOT / "scripts" / "check-retention-schedule.sh"
RETENTION_CHECK_TEXT = RETENTION_CHECK.read_text()
RETENTION_JOB = "metering-shadow-verdicts-purge"


def test_the_stacks_postgres_ships_and_preloads_pg_cron() -> None:
    """pg_cron is what runs nightly metering retention, and for weeks this
    deployment had neither the extension nor the job while a deploy step
    reported both as healthy.

    Two properties, and both have to hold together. The extension files must be
    IN THE IMAGE, because a hand-run CREATE EXTENSION does not survive a
    container recreate or a fresh volume. And the library must be preloaded at
    startup, because pg_cron refuses CREATE EXTENSION outright otherwise, which
    is the installed-but-unusable shape of issue #615.

    Asserted here rather than left to the migration, since the migration cannot
    fix either one from inside a session."""
    # Comments stripped first. The block's prose names Dockerfile.supabase-db
    # and shared_preload_libraries too, so an unstripped substring match passes
    # on a service that only TALKS about pg_cron. Measured, not assumed: with
    # `dockerfile:` repointed at a file that does not exist, the substring
    # version of this assertion stayed green.
    block = "\n".join(
        line for line in ENT_SERVICES["supabase-db"].splitlines()
        if not line.lstrip().startswith("#")
    )
    assert re.search(r"^      dockerfile: Dockerfile\.supabase-db\s*$", block, re.MULTILINE), (
        "supabase-db no longer builds the image that carries pg_cron; a stock "
        "pgvector image has no pg_cron, so retention silently stops being "
        "scheduled"
    )
    dockerfile = ROOT / "deploy" / "docker" / "Dockerfile.supabase-db"
    assert dockerfile.is_file(), f"{dockerfile} is missing"
    assert "postgresql-16-cron" in dockerfile.read_text(), (
        "Dockerfile.supabase-db no longer installs pg_cron"
    )
    assert re.search(r"^      - shared_preload_libraries=pg_cron\s*$", block, re.MULTILINE), (
        "supabase-db does not preload pg_cron, so CREATE EXTENSION pg_cron "
        "fails and no retention job can exist"
    )
    # The cron schema lives in exactly one database, and it has to be the one
    # migrations run against. pg_cron's default is the literal `postgres`, so a
    # deployment that renames its database would otherwise schedule into a
    # database nothing else touches.
    assert re.search(
        r"^      - cron\.database_name=\$\{ENTERPRISE_DB_NAME:-postgres\}\s*$",
        block,
        re.MULTILINE,
    ), block


def test_a_migration_actually_schedules_the_retention_job() -> None:
    """20260729_02 creates the schedule behind a guard that degrades to a
    NOTICE, and on this deployment that guard skipped every time. It is
    recorded as applied, so editing it would change nothing: the ledger keys on
    filename. Some LATER migration has to do the scheduling, and it has to fail
    loudly where pg_cron is available but the job did not appear, which is the
    half that was silent before."""
    migrations = sorted((ROOT / "supabase" / "migrations").glob("*.sql"))
    schedulers = [
        p for p in migrations
        if "cron.schedule" in p.read_text() and RETENTION_JOB in p.read_text()
    ]
    assert len(schedulers) >= 2, (
        "no migration after 20260729_02 schedules the retention job, so a "
        "database that skipped that file's guard never gets one: "
        f"{[p.name for p in schedulers]}"
    )
    latest = schedulers[-1]
    text = latest.read_text()
    assert "RAISE EXCEPTION" in text, (
        f"{latest.name} schedules the job but cannot fail: a host where "
        "pg_cron is available and the job still does not appear gets a notice "
        "nobody reads, which is the defect this file replaces"
    )
    # The assertion block has to key on availability, not on the extension
    # being installed, or a failed CREATE EXTENSION returns early and the
    # RAISE becomes unreachable.
    assert "pg_available_extensions" in text, text[:400]


def test_the_retention_check_fails_instead_of_reporting() -> None:
    """It printed "scheduled and active" for weeks over a database with no
    pg_cron at all. Half of why that was invisible is that absence was a
    ::warning:: and the script always exited 0, and a warning on a green run is
    indistinguishable from no warning to anyone reading conclusions."""
    text = RETENTION_CHECK_TEXT
    body = "\n".join(
        line for line in text.splitlines() if not line.lstrip().startswith("#")
    )
    assert "::warning::" not in body, (
        "the retention check is warning again instead of failing"
    )
    # Every error message aborts. Not a count of `exit 1`, which a later change
    # that adds guards inflates until turning one advisory no longer trips it.
    messages = re.findall(r"::error::", body)
    aborting = re.findall(r'echo "::error::[^\n]*"\n\s*exit 1\n', body)
    assert messages, body
    assert len(aborting) == len(messages), (
        f"{len(messages) - len(aborting)} of the {len(messages)} error messages "
        "in check-retention-schedule.sh do not abort, so it reports a broken "
        "retention job and exits 0"
    )
    # Two success exits, and both are OK branches. Anything else means some
    # negative verdict leaves by the same door as a healthy one.
    zero_exits = re.findall(r"^\s*exit 0\s*$", body, re.MULTILINE)
    assert len(zero_exits) == 2, f"{len(zero_exits)} success exits, expected 2"
    ok_branch = re.search(r"\n  OK\)\n(.*?);;", body, re.DOTALL)
    assert ok_branch and "exit 0" in ok_branch.group(1), body
    boot_branch = re.search(r"\n  OK_BOOTSTRAP\)\n(.*?);;", body, re.DOTALL)
    assert boot_branch and "exit 0" in boot_branch.group(1), body
    # The second door is the one that could quietly become a permanent pass, so
    # what opens it is asserted too: a NEVER_SUCCEEDED verdict, plus a
    # scheduling migration whose ledger timestamp is younger than the SAME
    # window, and nothing else. No flag, no environment variable, and no
    # unconditional allowance.
    assert re.search(r'if \[ "\$code" = "NEVER_RAN" \]', body), body
    # NEVER_RAN and not NEVER_SUCCEEDED, and the difference is load bearing: a
    # job with run rows and no successful one among them has been trying and
    # failing, and cron.schedule upserting its row does not make it new. Review
    # caught the version that granted such a job 72 hours of green.
    assert "NEVER_SUCCEEDED" in body, "the two verdicts must stay distinguishable"
    assert not re.search(r'if \[ "\$code" = "NEVER_SUCCEEDED" \]', body), (
        "a job that has already failed is eligible for the bootstrap allowance"
    )
    # Both the definition and the use, anchored. A bare `"any_run" in body`
    # substring match survives renaming the CTE, because the reference to it
    # still spells the old name: measured, that mutation stayed green while the
    # query it produces is broken.
    assert re.search(r"^any_run AS \($", body, re.MULTILINE), body
    assert "NOT EXISTS (SELECT 1 FROM any_run)" in body, (
        "nothing tells a never-due job apart from a failing one, so the "
        "allowance cannot be restricted to the first"
    )
    assert "hive_schema_migrations" in body, (
        "the bootstrap allowance is not tied to when the schedule was actually "
        "created, so it cannot expire"
    )
    assert re.search(
        r"applied_at > now\(\) - interval '\$MAX_SUCCESS_AGE'", body
    ), "the bootstrap allowance does not expire with the same window"
    # Anchored, because the same assignment appears again inline as the query's
    # own fallback. Measured: the substring version of this assertion stayed
    # green with the initialiser flipped to "t", which is an allowance that
    # opens for every database with no ledger row at all.
    assert re.search(r'^  bootstrap="f"$', body, re.MULTILINE), (
        "the bootstrap allowance must default to closed, so a missing ledger "
        "row grants nothing"
    )
    # And the step that runs it cannot be made advisory in the workflow either.
    step = _step_block(
        _job_block("migrate"),
        "Assert nightly metering retention is scheduled and running",
    )
    assert "check-retention-schedule.sh" in step, step
    assert "continue-on-error" not in step, step


def test_the_retention_verdict_reads_only_this_databases_job() -> None:
    """cron.job is cluster-wide and jobname is not unique across databases, so a
    same-named job in another database of the same cluster lands in the same
    table. Joining run history on the unfiltered job set lets that stranger's
    history answer for ours, in both directions. Measured against a real
    Postgres carrying a synthetic cron schema: with a foreign job holding one
    SUCCEEDED run and the local job holding none, the unfiltered query returned
    OK, a green verdict for a database whose retention has never once run.
    With the foreign run FAILED instead, it returned NEVER_SUCCEEDED and denied
    a brand new local schedule its bootstrap allowance. A locally inactive job
    beside an active foreign one returned NEVER_RAN, which the allowance then
    covers, instead of SCHEDULE_INACTIVE."""
    body = "\n".join(
        line
        for line in RETENTION_CHECK_TEXT.splitlines()
        if not line.lstrip().startswith("#")
    )
    assert re.search(
        r"^mine AS \(\n\s*SELECT \* FROM j WHERE database = current_database\(\)$",
        body,
        re.MULTILINE,
    ), "the verdict has no per-database job set, so cron.job is read cluster-wide"
    # The three run-history CTEs. Asserted as the absence of the cluster-wide
    # join rather than the presence of the scoped one, because adding a fourth
    # CTE that joins j would not disturb a count of the scoped ones.
    assert not re.search(r"JOIN j ON j\.jobid = d\.jobid", body), (
        "a run-history CTE still joins the cluster-wide job set, so another "
        "database's runs answer for this one"
    )
    assert body.count("JOIN mine ON mine.jobid = d.jobid") == 3, body
    # Every per-job verdict reads mine. j is allowed in exactly two places: the
    # SCHEDULE_MISSING test and the WRONG_DB message that names the database it
    # did find, both of which are about a job that is NOT ours.
    assert re.search(r"FROM j\)\n\s*THEN 'SCHEDULE_MISSING", body), body
    for verdict in ("SCHEDULE_INACTIVE", "SCHEDULE_HOLLOW"):
        clause = re.search(r"WHEN ([^\n]*)\n\s*THEN '" + verdict, body)
        assert clause and " j " not in clause.group(1), (
            f"{verdict} is decided over the cluster-wide job set, so another "
            "database's job can mask this one"
        )
    # WRONG_DB is decided by mine being empty, so it has to be tested before the
    # verdicts that read mine, otherwise they fire first with a wrong message.
    order = [
        m.group(1)
        for m in re.finditer(r"THEN '([A-Z_]+)\|", body)
    ]
    assert order.index("SCHEDULE_WRONG_DB") < order.index("SCHEDULE_INACTIVE"), order


def test_the_retention_check_cannot_report_on_another_database() -> None:
    """The other half of the false green: the check was reading the hosted
    Supabase project, where pg_cron IS preloaded, while the stack's own
    database had no job. It was not a wrong row, it was the right row in the
    wrong cluster.

    So the check is handed the identifier of the database the stack runs, and
    it compares that against its own connection. Unset must fail too: a check
    that cannot name the database it read has checked nothing."""
    text = RETENTION_CHECK_TEXT
    assert "HIVE_EXPECTED_DB_CLUSTER" in text, text[:400]
    assert "pg_control_system()" in text, (
        "the check does not identify the cluster it is connected to, so it "
        "cannot tell whether it is the right one"
    )
    assert re.search(
        r'if \[ -z "\$\{HIVE_EXPECTED_DB_CLUSTER:-\}" \]', text
    ), "an unset expected cluster must fail rather than skip the comparison"
    assert re.search(
        r'if \[ "\$actual_cluster" != "\$HIVE_EXPECTED_DB_CLUSTER" \]', text
    ), text
    # The value's provenance is what makes the comparison able to fail: it comes
    # from what the running container reported, not from the same DSN this
    # script connects with.
    step = _step_block(
        _job_block("migrate"),
        "Assert the migration target is the database this stack uses",
    )
    assert 'HIVE_EXPECTED_DB_CLUSTER=$stack' in step, (
        "the retention check's expected cluster must come from the stack side "
        "of the identity assertion, not from the target side"
    )


def test_the_retention_check_demands_evidence_of_execution() -> None:
    """A row in cron.job is not evidence of anything. A job whose background
    worker never starts, or that errors every night, looks identical there to a
    working one, which is the same false green in a new costume."""
    text = RETENTION_CHECK_TEXT
    assert "cron.job_run_details" in text, (
        "the check reads only cron.job, so a scheduled job that never executes "
        "passes it"
    )
    assert "'succeeded'" in text, text[:400]
    # The staleness window is a literal on purpose. An environment override is a
    # way to make this check pass without making retention work.
    assert re.search(r"^MAX_SUCCESS_AGE='[^']+'$", text, re.MULTILINE), text[:400]
    assert not re.search(r"MAX_SUCCESS_AGE=\"?\$\{", text), (
        "the staleness window is overridable from the environment"
    )


def _step_pos(job_text: str, step_name: str) -> int:
    """Character offset of a step's `- name:` line within a job, for ordering
    assertions. Ordering is the whole point of the deadlock below, so it is
    asserted on positions rather than on mere presence."""
    match = re.search(
        rf"^      - name: {re.escape(step_name)}\s*$", job_text, re.MULTILINE
    )
    assert match, f"the migrate job has no step named {step_name!r}"
    return match.start()


def test_migrations_run_against_a_reconciled_database_container() -> None:
    """The 2026-08-22 deadlock, which cost three deploy runs and took chat
    sign-in down while its fix sat merged on main.

    The shape: migrate reconciles the database service by running
    `docker compose ... up -d --build supabase-db` with a working-directory of
    /home/sakib/hive/deploy/docker, so it reads the BOX's clone, not the
    runner workspace this job checks out. Nothing in migrate advanced that
    clone. The only step that did was deploy's Pull latest main, and deploy
    `needs: migrate`.

    So the moment a migration depended on something only a newer compose file
    provides, the run could not converge: PR #994 moved supabase-db onto a
    locally built image carrying pg_cron, 20260822_01 needed pg_cron, migrate
    rebuilt from the pre-#994 compose file, got the old image, failed on the
    missing extension, and skipped the job that would have pulled the fix.

    The fix is ordering, so the assertions are on order. Presence alone would
    pass against the exact arrangement that deadlocked."""
    migrate = _job_block("migrate")

    pull = _step_pos(migrate, "Pull latest main")
    reconcile = _step_pos(migrate, "Make sure the stack's database is up")
    apply_ = _step_pos(migrate, "Apply pending migrations")

    assert pull < reconcile, (
        "the migrate job reconciles the database container before it updates "
        "the box's clone, so the compose file it builds from is whatever the "
        "last successful deploy left behind. A migration that needs a change "
        "to that file can then never be satisfied."
    )
    assert reconcile < apply_, (
        "migrations are applied before the database service is reconciled to "
        "the compose file, so they run against yesterday's server"
    )

    # The coupling that makes the order matter: the pull and the reconcile must
    # act on the SAME directory. A pull that updated some other clone would
    # satisfy the ordering assertions above and change nothing.
    pull_step = _step_block(migrate, "Pull latest main")
    reconcile_step = _step_block(migrate, "Make sure the stack's database is up")
    clone = "/home/sakib/hive"
    assert f"cd {clone}\n" in pull_step, (
        f"the Pull latest main step does not update {clone}, which is the "
        "clone the reconcile step below builds from"
    )
    assert "git pull --ff-only origin main" in pull_step, pull_step[:200]
    assert f"working-directory: {clone}/deploy/docker" in reconcile_step, (
        "the reconcile step does not build from the clone the pull updates, "
        f"so updating {clone} would not change what it builds"
    )
    assert "--build supabase-db" in reconcile_step, (
        "the reconcile step does not pass --build, so a change to "
        "Dockerfile.supabase-db never reaches the running container: compose "
        "builds a locally built image only when it is absent entirely"
    )

    # Exactly one pull, and it is this one. Leaving a second copy behind in
    # deploy would be harmless today and would rot into two sources of truth
    # for which commit the box is on.
    deploy = _job_block("deploy")
    assert "- name: Pull latest main" not in deploy, (
        "the deploy job still pulls as well; the box's clone must be advanced "
        "in exactly one place, and that place has to be upstream of migrate"
    )


def _do_blocks(sql: str) -> list:
    """Every `DO $tag$ ... $tag$;` body in a migration, in file order."""
    return [m.group(2) for m in re.finditer(r"DO \$(\w+)\$(.*?)\$\1\$;", sql, re.DOTALL)]


def _strip_sql_comments(sql: str) -> str:
    """`-- ...` comments removed, so an assertion about code cannot be
    satisfied by prose.

    Written because it happened: the first version of the guard test below
    passed against a deliberately reverted guard, because the comment
    explaining the guard mentions pg_available_extensions by name and the
    assertion was reading the comment. A check that cannot go red is worse
    than no check, since it also reports that the thing is covered."""
    return "\n".join(re.sub(r"--.*$", "", line) for line in sql.splitlines())


def test_the_retention_migration_skips_a_pg_cron_that_is_only_a_catalog_row() -> None:
    """pg_extension is a catalog row. pg_available_extensions is what is on
    disk. The migration gated its CREATE EXTENSION block on the second and its
    cron.schedule block on the first, which is fine right up until the two
    disagree.

    They disagreed on the demo box. The restored production dump carried a
    pg_cron row (and the cron schema, and cron.schedule) into a server whose
    image had never shipped the library, so the first block correctly announced
    that pg_cron was unavailable and skipped, and the second block then saw the
    catalog row, resolved cron.schedule by name, and died calling it:
    `could not access file "$libdir/pg_cron"`. The migration runs as a
    superuser on the box, so its handler re-raised, the migration aborted, and
    the deploy that would have supplied the library was skipped behind
    `needs: migrate`.

    Both blocks must ask the same question. The file's own NOTICE already
    promises this file is a clean no-op where pg_cron is genuinely absent."""
    migrations = sorted((ROOT / "supabase" / "migrations").glob("*.sql"))
    schedulers = [
        p for p in migrations
        if "cron.schedule" in p.read_text() and RETENTION_JOB in p.read_text()
    ]
    assert schedulers, "no migration schedules the retention job at all"
    latest = schedulers[-1]

    blocks = [_strip_sql_comments(b) for b in _do_blocks(latest.read_text())]
    calling = [b for b in blocks if "cron.schedule(" in b]
    assert calling, (
        f"{latest.name} names cron.schedule but not inside a DO block this "
        "test can read; if the file was restructured, restate the guard "
        "assertion below against the new shape rather than deleting it"
    )
    for block in calling:
        guard = block[: block.index("cron.schedule(")]
        assert "pg_available_extensions" in guard, (
            f"{latest.name} calls cron.schedule guarded only by the catalog "
            "row. A database that carries a pg_cron row from a restored dump "
            "but has no pg_cron library on disk reaches the call and aborts "
            "the migration, which is the 2026-08-22 deadlock."
        )


def main() -> int:
    # The parser guard runs FIRST, deliberately. Alphabetical order put it last,
    # so a silent parse failure would have been reported by whichever assertion
    # happened to sort earliest rather than by the guard written to catch it.
    named = {name: value for name, value in globals().items() if name.startswith("test_")}
    guard = "test_the_parser_actually_found_the_services"
    order = [guard] + sorted(n for n in named if n != guard)
    tests = [named[n] for n in order]
    for test in tests:
        test()
    print(f"test_selfhost_supabase_seam: ok ({len(tests)} checks)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
