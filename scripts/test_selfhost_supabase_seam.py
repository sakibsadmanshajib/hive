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
        block = ENT_SERVICES[svc]
        depends = set(re.findall(r"^      ([A-Za-z0-9][A-Za-z0-9._-]*):\s*$", block, re.MULTILINE))
        # The override blocks declare depends_on and environment; environment
        # keys are upper case with no trailing colon-only line, so what this
        # picks up is the dependency names.
        outside = {d for d in depends if d not in DATA_PLANE}
        assert not outside, f"{svc} depends on {sorted(outside)}, outside the profile set"
        assert depends, f"{svc} override declares no dependency, so this check is vacuous"


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
        deps = set(
            re.findall(r"^      ([A-Za-z0-9][A-Za-z0-9._-]*):\s*$", ENT_SERVICES[svc], re.MULTILINE)
        )
        assert deps <= DATA_PLANE, f"{svc} depends on {sorted(deps - DATA_PLANE)}"


def test_no_deploy_step_spells_its_own_compose_flags() -> None:
    """Four steps in this workflow run docker compose, and each used to rebuild
    the flag list by hand. A fifth step added later with a copied-and-trimmed
    list is the recurrence this guards: it would read a different project and
    report green over a box missing services, which is indistinguishable from
    healthy in a log.

    So the rule is not "the flags are right somewhere", it is that no step
    spells them at all."""
    invocations = re.findall(r"docker compose[^\n)]*", DEPLOY_TEXT)
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
