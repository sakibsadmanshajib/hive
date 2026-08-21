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
    for name, text in (("base", BASE_TEXT), ("enterprise", ENT_TEXT)):
        assert not re.search(r"^networks:\s*$", text, re.MULTILINE), name
    watched = DATA_PLANE | {"edge-api", "control-plane", "open-webui"}
    for source in (BASE_SERVICES, ENT_SERVICES):
        for svc, block in source.items():
            if svc in watched:
                assert not re.search(r"^    networks:\s*$", block, re.MULTILINE), svc


def test_selfhost_profile_covers_exactly_the_data_plane() -> None:
    """`selfhost` is what lets the demo box run the data plane underneath the
    `local` and `chat` application profiles. Too narrow and the flip starts an
    incomplete Supabase; too wide and it drags in unrelated services (today
    `ollama`, which is `enterprise` only and would take memory on a box that
    has none to spare)."""
    with_selfhost = {
        svc for svc, block in ENT_SERVICES.items() if "selfhost" in profiles_of(block)
    }
    assert with_selfhost == DATA_PLANE, sorted(with_selfhost ^ DATA_PLANE)
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
    assert re.search(
        r"^      - supabase-ca:" + re.escape(ca_path.rsplit("/", 1)[0]) + r":ro\s*$",
        block,
        re.MULTILINE,
    ), block


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
        assert "supabase.co" not in bare, line
        assert "pooler.supabase.com" not in bare, line


def main() -> int:
    tests = [value for name, value in sorted(globals().items()) if name.startswith("test_")]
    for test in tests:
        test()
    print(f"test_selfhost_supabase_seam: ok ({len(tests)} checks)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
