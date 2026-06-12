#!/usr/bin/env sh
# Hive EnterpriseEdge one-line installer
# Usage: curl -fsSL https://raw.githubusercontent.com/sakibsadmanshajib/hive/main/scripts/install.sh | bash
#
# Piping-safety: everything lives inside main() which is called at the very last
# line. A truncated partial download therefore never executes any code.
# Pattern adopted from Ollama install.sh and uv (astral.sh/uv).
#
# Supported platforms: Ubuntu/Debian on x86_64 or arm64.
# Flags:
#   --with-ollama     Enable in-stack Ollama local inference (triggers hardware-aware model advisor)
#   --uninstall       Stop stack and print what is left behind
#   --non-interactive Skip all prompts; read env vars from environment
#   --help            Show usage
#
# Hardware-aware model advisor (active when --with-ollama is selected):
#   Tier 1 (always available): pure-bash detection via /proc/meminfo + nvidia-smi
#   Tier 2 (optional, requires Node.js >= 16): npx llm-checker for richer GPU analysis
#   Recommended Ollama model tags (validated June 2026):
#     CPU-only / low RAM   : phi4-mini     (3.8 GB, 128k ctx, tools support)
#     8 GB VRAM            : qwen3:8b      (5.2 GB, strong multilingual, Bangla-capable)
#     12 to 16 GB VRAM     : qwen2.5:14b  (9.0 GB, strong instruction following)
#     24+ GB VRAM          : qwen3:32b    (20 GB, thinking + tools support)
#     128 GB unified memory: qwen2.5:72b  (47 GB, datacentre / workstation tier)
#   Note: llama3.3 on Ollama is a 70B model only. Use qwen3:8b or llama3.1:8b for 8 GB VRAM.

set -eu

# ─── Colour helpers ────────────────────────────────────────────────────────────
RED=''
GREEN=''
YELLOW=''
BOLD=''
RESET=''
if [ -t 1 ] && command -v tput >/dev/null 2>&1; then
    RED=$(tput setaf 1)
    GREEN=$(tput setaf 2)
    YELLOW=$(tput setaf 3)
    BOLD=$(tput bold)
    RESET=$(tput sgr0)
fi

status()  { printf '%s>>> %s%s\n' "${BOLD}" "$*" "${RESET}"; }
success() { printf '%s>>> %s%s\n' "${GREEN}" "$*" "${RESET}"; }
warn()    { printf '%s>>> WARNING: %s%s\n' "${YELLOW}" "$*" "${RESET}" >&2; }
error()   { printf '%s>>> ERROR: %s%s\n' "${RED}" "$*" "${RESET}" >&2; exit 1; }

# ─── Sudo helper ──────────────────────────────────────────────────────────────
# Lazy escalation: SUDO is empty when already root; set to "sudo" otherwise.
SUDO=''
if [ "$(id -u)" -ne 0 ]; then
    if ! command -v sudo >/dev/null 2>&1; then
        error "Not running as root and sudo not found. Re-run as root or install sudo."
    fi
    SUDO='sudo'
fi

# ─── Defaults ─────────────────────────────────────────────────────────────────
HIVE_HOME="${HIVE_HOME:-/opt/hive}"
HIVE_REPO="https://github.com/sakibsadmanshajib/hive.git"
WITH_OLLAMA=false
UNINSTALL=false
NON_INTERACTIVE=false
# OLLAMA_MODEL is resolved by the model advisor when --with-ollama is set.
# Override via environment: OLLAMA_MODEL=qwen3:8b bash install.sh --with-ollama
OLLAMA_MODEL="${OLLAMA_MODEL:-}"
# Set to true by the advisor when the operator opts to pull immediately (TTY only).
OLLAMA_PULL_NOW=false

# ─── Arg parsing ──────────────────────────────────────────────────────────────
parse_args() {
    for arg in "$@"; do
        case "$arg" in
            --with-ollama)    WITH_OLLAMA=true ;;
            --uninstall)      UNINSTALL=true ;;
            --non-interactive) NON_INTERACTIVE=true ;;
            --help|-h)
                printf 'Hive EnterpriseEdge installer\n\n'
                printf 'Usage:\n'
                printf '  curl -fsSL https://raw.githubusercontent.com/sakibsadmanshajib/hive/main/scripts/install.sh | bash\n'
                printf '  bash install.sh [flags]\n\n'
                printf 'Flags:\n'
                printf '  --with-ollama       Enable in-stack Ollama local inference\n'
                printf '  --uninstall         Stop stack and show what remains\n'
                printf '  --non-interactive   Read config from environment variables only\n'
                printf '  --help              Show this help\n\n'
                printf 'Environment overrides:\n'
                printf '  HIVE_HOME           Install directory (default: /opt/hive)\n'
                exit 0
                ;;
            *) warn "Unknown flag: $arg (ignored)" ;;
        esac
    done
}

# ─── OS / Arch detection ──────────────────────────────────────────────────────
detect_platform() {
    OS="$(uname -s)"
    ARCH="$(uname -m)"

    case "$OS" in
        Linux) ;;
        *)     error "Unsupported OS: $OS. Hive EnterpriseEdge requires Linux (Ubuntu/Debian)." ;;
    esac

    # Normalise arch names
    case "$ARCH" in
        x86_64)          ARCH="amd64" ;;
        aarch64|arm64)   ARCH="arm64" ;;
        *)               error "Unsupported architecture: $ARCH. Only x86_64 and arm64 are supported." ;;
    esac

    # Distro check: require Ubuntu or Debian
    if [ -f /etc/os-release ]; then
        # shellcheck disable=SC1091
        DISTRO_ID="$(. /etc/os-release && printf '%s' "${ID:-}")"
        DISTRO_ID_LIKE="$(. /etc/os-release && printf '%s' "${ID_LIKE:-}")"
    else
        DISTRO_ID=""
        DISTRO_ID_LIKE=""
    fi

    case "$DISTRO_ID $DISTRO_ID_LIKE" in
        *ubuntu*|*debian*) ;;
        *) error "Unsupported distro: $DISTRO_ID. Only Ubuntu and Debian are supported." ;;
    esac

    status "Platform: Linux/$ARCH on $DISTRO_ID"
}

# ─── Uninstall ─────────────────────────────────────────────────────────────────
do_uninstall() {
    status "Stopping Hive EnterpriseEdge stack..."
    if [ -d "$HIVE_HOME/deploy/docker" ]; then
        cd "$HIVE_HOME/deploy/docker"
        $SUDO docker compose --env-file "$HIVE_HOME/.env" --profile enterprise down 2>/dev/null || true
    fi
    printf '\n'
    printf '%s>>> Uninstall complete.%s\n' "${GREEN}" "${RESET}"
    printf '\n'
    printf 'The following items were NOT removed (manual cleanup if desired):\n'
    printf '  %s         source code and .env file\n' "$HIVE_HOME"
    printf '  Docker images              docker image prune or docker rmi individually\n'
    printf '  Docker volumes             docker volume prune (removes ALL unused volumes)\n'
    printf '  Docker runtime             use your package manager to remove docker-ce\n'
    exit 0
}

# ─── Docker installation ──────────────────────────────────────────────────────
install_docker() {
    if command -v docker >/dev/null 2>&1; then
        status "Docker already installed: $(docker --version)"
        # Verify Compose v2 plugin is present even when Docker CLI already exists
        if ! docker compose version >/dev/null 2>&1; then
            status "Docker Compose v2 plugin missing, installing..."
            $SUDO apt-get update -qq
            $SUDO apt-get install -y -qq docker-compose-plugin
            success "Docker Compose v2 plugin installed."
        fi
        return
    fi

    status "Installing Docker via official apt repository..."
    $SUDO apt-get update -qq
    # git added here so clone_or_update_repo always has it available on minimal
    # Ubuntu/Debian images where git is not pre-installed.
    $SUDO apt-get install -y -qq ca-certificates curl gnupg lsb-release git

    $SUDO install -m 0755 -d /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | \
        $SUDO gpg --dearmor -o /etc/apt/keyrings/docker.gpg
    $SUDO chmod a+r /etc/apt/keyrings/docker.gpg

    DISTRO_CODENAME="$(lsb_release -cs 2>/dev/null || . /etc/os-release && printf '%s' "${VERSION_CODENAME:-}")"
    printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/%s %s stable\n' \
        "$ARCH" "$DISTRO_ID" "$DISTRO_CODENAME" | \
        $SUDO tee /etc/apt/sources.list.d/docker.list >/dev/null

    $SUDO apt-get update -qq
    $SUDO apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

    # Allow current user to run docker without sudo on future shells
    if [ -n "${SUDO_USER:-}" ]; then
        $SUDO usermod -aG docker "$SUDO_USER" || true
        warn "Added $SUDO_USER to docker group. Log out and back in for group to take effect."
    fi

    success "Docker installed: $(docker --version)"
}

# ─── Repo clone / update ──────────────────────────────────────────────────────
clone_or_update_repo() {
    if [ -d "$HIVE_HOME/.git" ]; then
        status "Updating existing repo at $HIVE_HOME..."
        cd "$HIVE_HOME"
        $SUDO git fetch --quiet origin
        $SUDO git checkout main --quiet
        $SUDO git reset --hard origin/main --quiet
        success "Repo updated to $(git rev-parse --short HEAD)"
    else
        status "Cloning Hive to $HIVE_HOME..."
        $SUDO mkdir -p "$HIVE_HOME"
        $SUDO git clone --quiet --branch main "$HIVE_REPO" "$HIVE_HOME"
        success "Repo cloned to $HIVE_HOME"
    fi
}

# ─── Prompt helper ────────────────────────────────────────────────────────────
# Reads a value interactively or returns existing env var.
# Usage: prompt_value VAR_NAME "label" "required|optional" [default] [secret]
#
# Assignments use `eval "${_varname}=\$_value"`: the variable NAME (a hardcoded
# literal at every call site) is interpolated, but the VALUE is passed as a
# variable reference and never re-parsed by the shell. Safe for any content
# including single quotes.
#
# When the 5th arg is "secret", terminal echo is disabled while typing
# (POSIX stty, restored afterwards and on interrupt) so secrets never land
# in terminal scrollback.

# Read one line with terminal echo disabled. Restores echo on interrupt.
read_secret() {
    _stty_orig="$(stty -g 2>/dev/null || true)"
    if [ -n "$_stty_orig" ]; then
        trap 'stty "$_stty_orig" 2>/dev/null || true' INT TERM
        stty -echo 2>/dev/null || true
    fi
    read -r _input
    if [ -n "$_stty_orig" ]; then
        stty "$_stty_orig" 2>/dev/null || true
        trap - INT TERM
    fi
    printf '\n'
}

prompt_value() {
    _varname="$1"
    _label="$2"
    _required="$3"
    _default="${4:-}"
    _secret="${5:-}"

    # Non-interactive: use env var if set, or default
    if [ "$NON_INTERACTIVE" = "true" ] || [ ! -t 0 ]; then
        # Dynamic variable dereference: intentional, _varname is controlled input
        eval '_current="${'"${_varname}"':-}"'
        if [ -n "$_current" ]; then
            return
        fi
        if [ -n "$_default" ]; then
            eval "${_varname}=\$_default"
            return
        fi
        if [ "$_required" = "required" ]; then
            error "$_varname is required in non-interactive mode. Set it as an environment variable before running."
        fi
        return
    fi

    # Dynamic variable dereference: intentional, _varname is controlled input
    eval '_current="${'"${_varname}"':-}"'
    _req_marker=""
    [ "$_required" = "required" ] && _req_marker=" ${RED}[required]${RESET}"
    _secret_marker=""
    [ "$_secret" = "secret" ] && _secret_marker=" (input hidden)"

    if [ -n "$_current" ]; then
        printf '%s%s%s%s (current value set, press Enter to keep)%s: ' "${BOLD}" "$_label" "$_req_marker" "$_secret_marker" "${RESET}"
        if [ "$_secret" = "secret" ]; then read_secret; else read -r _input; fi
        [ -n "$_input" ] && eval "${_varname}=\$_input"
    elif [ -n "$_default" ]; then
        if [ "$_secret" = "secret" ]; then
            printf '%s%s%s%s [auto-generated if blank]%s: ' "${BOLD}" "$_label" "$_req_marker" "$_secret_marker" "${RESET}"
            read_secret
        else
            printf '%s%s%s [%s]%s: ' "${BOLD}" "$_label" "$_req_marker" "$_default" "${RESET}"
            read -r _input
        fi
        if [ -n "$_input" ]; then
            eval "${_varname}=\$_input"
        else
            eval "${_varname}=\$_default"
        fi
    else
        printf '%s%s%s%s%s: ' "${BOLD}" "$_label" "$_req_marker" "$_secret_marker" "${RESET}"
        if [ "$_secret" = "secret" ]; then read_secret; else read -r _input; fi
        if [ -z "$_input" ] && [ "$_required" = "required" ]; then
            error "$_varname is required and cannot be empty."
        fi
        eval "${_varname}=\$_input"
    fi
}

# ─── .env wizard ──────────────────────────────────────────────────────────────
setup_env() {
    ENV_FILE="$HIVE_HOME/.env"

    if [ -f "$ENV_FILE" ]; then
        status ".env already exists at $ENV_FILE, reusing it."
        return
    fi

    status "Setting up .env (copy from .env.example)..."
    $SUDO cp "$HIVE_HOME/.env.example" "$ENV_FILE"

    if [ "$NON_INTERACTIVE" = "false" ] && [ -t 0 ]; then
        printf '\n'
        printf '%s=== Hive EnterpriseEdge Configuration ===%s\n' "${BOLD}" "${RESET}"
        printf 'Required fields are marked [required]. Press Enter to keep any existing value.\n'
        printf 'Secret values are not echoed back.\n\n'
    fi

    # ── Supabase (required) ──
    printf '%s-- Supabase --%s\n' "${BOLD}" "${RESET}"
    prompt_value SUPABASE_URL          "Supabase Project URL" required
    prompt_value SUPABASE_ANON_KEY     "Supabase Anon Key" required "" secret
    prompt_value SUPABASE_SERVICE_ROLE_KEY "Supabase Service Role Key" required "" secret
    prompt_value SUPABASE_DB_URL       "Supabase DB URL (postgres://...)" required "" secret

    # ── Storage (required) ──
    printf '%s-- Supabase Storage (S3) --%s\n' "${BOLD}" "${RESET}"
    prompt_value S3_ENDPOINT  "S3 Endpoint (e.g. https://<ref>.supabase.co/storage/v1/s3)" required
    prompt_value S3_ACCESS_KEY "S3 Access Key" required "" secret
    prompt_value S3_SECRET_KEY "S3 Secret Key" required "" secret
    prompt_value S3_REGION    "S3 Region" optional "us-east-1"

    # ── LLM Provider (at least one required) ──
    printf '%s-- LLM Provider (at least one required) --%s\n' "${BOLD}" "${RESET}"
    prompt_value OPENROUTER_API_KEY "OpenRouter API Key" optional "" secret
    prompt_value GROQ_API_KEY       "Groq API Key" optional "" secret

    if [ "$WITH_OLLAMA" = "true" ]; then
        OLLAMA_BASE_URL="http://ollama:11434"
        status "Ollama enabled: OLLAMA_BASE_URL=$OLLAMA_BASE_URL"
    fi

    # ── Security tokens ──
    printf '%s-- Security --%s\n' "${BOLD}" "${RESET}"
    _default_token="$(command -v openssl >/dev/null 2>&1 && openssl rand -base64 32 || printf 'change-me-generate-with-openssl-rand-base64-32')"
    prompt_value CONTROL_PLANE_INTERNAL_TOKEN "Internal service token" required "$_default_token" secret

    _default_litellm="$(command -v openssl >/dev/null 2>&1 && openssl rand -base64 24 || printf 'litellm-change-me')"
    prompt_value LITELLM_MASTER_KEY "LiteLLM master key" required "$_default_litellm" secret

    # OWUI_SHIM_KEY: Open WebUI sends this as upstream API key; edge disables
    # the shim-unwrap middleware when it is empty, so a missing key causes chat
    # requests to fail at edge on the enterprise profile.
    _default_owui_shim="$(command -v openssl >/dev/null 2>&1 && openssl rand -base64 32 || printf 'owui-shim-change-me')"
    prompt_value OWUI_SHIM_KEY "Open WebUI shim key" required "$_default_owui_shim" secret

    # ── Optional: Grafana ──
    printf '%s-- Grafana (optional, for --profile monitoring) --%s\n' "${BOLD}" "${RESET}"
    _default_grafana="$(command -v openssl >/dev/null 2>&1 && openssl rand -base64 18 || printf 'admin')"
    prompt_value GRAFANA_ADMIN_USER     "Grafana admin username" optional "admin"
    prompt_value GRAFANA_ADMIN_PASSWORD "Grafana admin password" optional "$_default_grafana" secret

    # Validate: at least one LLM provider.  Use :- so set -u does not abort
    # when an optional variable was never set (non-interactive path).
    _has_provider=false
    for _v in "${OPENROUTER_API_KEY:-}" "${GROQ_API_KEY:-}" "${OLLAMA_BASE_URL:-}"; do
        [ -n "$_v" ] && _has_provider=true && break
    done
    if [ "$_has_provider" = "false" ]; then
        error "At least one LLM provider key (OPENROUTER_API_KEY, GROQ_API_KEY) or OLLAMA_BASE_URL must be set."
    fi

    # Derive JWT vars from SUPABASE_URL so the edge JWKS validator gets a real
    # URL instead of the <PROJECT_REF> placeholder copied from .env.example.
    # Pattern: https://<ref>.supabase.co  ->  issuer = https://<ref>.supabase.co/auth/v1
    #                                         jwks   = https://<ref>.supabase.co/auth/v1/.well-known/jwks.json
    if [ -n "${SUPABASE_URL:-}" ]; then
        _supabase_base="${SUPABASE_URL%/}"
        SUPABASE_JWT_ISSUER="${_supabase_base}/auth/v1"
        SUPABASE_JWKS_URL="${_supabase_base}/auth/v1/.well-known/jwks.json"
    fi

    # Write values into .env using sed (no secret values echoed in status output)
    _write_env_var() {
        _key="$1"
        _val="$2"
        # Escape forward slashes for sed
        _escaped_val="$(printf '%s' "$_val" | sed 's/[\/&]/\\&/g')"
        $SUDO sed -i "s|^${_key}=.*|${_key}=${_escaped_val}|" "$ENV_FILE" || true
        # If key not present, append it
        if ! grep -q "^${_key}=" "$ENV_FILE" 2>/dev/null; then
            printf '%s=%s\n' "$_key" "$_val" | $SUDO tee -a "$ENV_FILE" >/dev/null
        fi
    }

    _write_env_var "SUPABASE_URL" "${SUPABASE_URL:-}"
    _write_env_var "SUPABASE_ANON_KEY" "${SUPABASE_ANON_KEY:-}"
    _write_env_var "SUPABASE_SERVICE_ROLE_KEY" "${SUPABASE_SERVICE_ROLE_KEY:-}"
    _write_env_var "SUPABASE_DB_URL" "${SUPABASE_DB_URL:-}"
    _write_env_var "SUPABASE_JWT_ISSUER" "${SUPABASE_JWT_ISSUER:-}"
    _write_env_var "SUPABASE_JWKS_URL" "${SUPABASE_JWKS_URL:-}"
    _write_env_var "NEXT_PUBLIC_SUPABASE_URL" "${SUPABASE_URL:-}"
    _write_env_var "NEXT_PUBLIC_SUPABASE_ANON_KEY" "${SUPABASE_ANON_KEY:-}"
    _write_env_var "S3_ENDPOINT" "${S3_ENDPOINT:-}"
    _write_env_var "S3_ACCESS_KEY" "${S3_ACCESS_KEY:-}"
    _write_env_var "S3_SECRET_KEY" "${S3_SECRET_KEY:-}"
    _write_env_var "S3_REGION" "${S3_REGION:-us-east-1}"
    _write_env_var "OPENROUTER_API_KEY" "${OPENROUTER_API_KEY:-}"
    _write_env_var "GROQ_API_KEY" "${GROQ_API_KEY:-}"
    _write_env_var "OLLAMA_BASE_URL" "${OLLAMA_BASE_URL:-}"
    _write_env_var "CONTROL_PLANE_INTERNAL_TOKEN" "${CONTROL_PLANE_INTERNAL_TOKEN:-}"
    _write_env_var "LITELLM_MASTER_KEY" "${LITELLM_MASTER_KEY:-}"
    _write_env_var "OWUI_SHIM_KEY" "${OWUI_SHIM_KEY:-}"
    _write_env_var "GRAFANA_ADMIN_USER" "${GRAFANA_ADMIN_USER:-admin}"
    _write_env_var "GRAFANA_ADMIN_PASSWORD" "${GRAFANA_ADMIN_PASSWORD:-}"

    # Lock down permissions on .env
    $SUDO chmod 600 "$ENV_FILE"

    success ".env written to $ENV_FILE"
}

# ─── Hardware-aware model advisor ─────────────────────────────────────────────
# Runs only when --with-ollama is set. Detects RAM and VRAM via bash builtins
# (Tier 1, always works) then optionally defers to npx llm-checker (Tier 2,
# requires Node.js >= 16 on the host). Resolves OLLAMA_MODEL and offers to pull
# it immediately (TTY) or prints the pull command (non-TTY / non-interactive).
#
# Model selection table (Ollama tags, validated June 2026):
#   CPU-only / <8 GB RAM : phi4-mini      3.8B  2.5 GB  128k ctx  tools
#   8 GB VRAM            : qwen3:8b       8B    5.2 GB  Bangla-capable multilingual
#   12 to 16 GB VRAM     : qwen2.5:14b   14B   9.0 GB  strong instruction following
#   24+ GB VRAM          : qwen3:32b     32B   20 GB   thinking + tools
#   128 GB unified mem   : qwen2.5:72b   72B   47 GB   datacentre / workstation
#
# llm-checker (npm) is a hardware-detection + model-recommendation CLI by a
# Bangladeshi author. It requires Node.js >= 16 and runs fully non-interactively
# via: npx llm-checker hw-detect --json
# We treat it as an optional enrichment layer only; the bash tier always runs first.
advise_ollama_model() {
    if [ "$WITH_OLLAMA" != "true" ]; then
        return
    fi

    # Skip if the operator pre-set OLLAMA_MODEL via env var
    if [ -n "$OLLAMA_MODEL" ]; then
        status "OLLAMA_MODEL pre-set to '$OLLAMA_MODEL', skipping advisor."
        return
    fi

    printf '\n'
    status "Running hardware-aware model advisor..."

    # ── Tier 1: bash detection (always runs) ────────────────────────────────
    _ram_kb=0
    if [ -f /proc/meminfo ]; then
        _ram_kb="$(awk '/^MemTotal:/ { print $2 }' /proc/meminfo 2>/dev/null || printf '0')"
    fi
    _ram_gb=$((_ram_kb / 1024 / 1024))

    _vram_mb=0
    _gpu_name=""
    if command -v nvidia-smi >/dev/null 2>&1; then
        _vram_mb="$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null | head -1 | tr -d ' ' || printf '0')"
        _gpu_name="$(nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | head -1 | xargs || printf '')"
    fi
    _vram_gb=$((_vram_mb / 1024))

    printf '  Detected: RAM %d GB' "$_ram_gb"
    if [ "$_vram_mb" -gt 0 ]; then
        printf ', VRAM %d GB (%s)' "$_vram_gb" "$_gpu_name"
    else
        printf ', no NVIDIA GPU detected (CPU-only path)'
    fi
    printf '\n'

    # Map hardware to recommended model
    _recommended=""
    _tier_label=""
    if [ "$_vram_mb" -ge 98304 ]; then
        # 96 GB+ VRAM or unified memory class (128 GB tier detection via RAM when no discrete GPU)
        _recommended="qwen2.5:72b"
        _tier_label="128 GB unified-memory / datacentre workstation tier"
    elif [ "$_vram_gb" -ge 24 ]; then
        _recommended="qwen3:32b"
        _tier_label="24+ GB VRAM tier (RTX 5090 / professional GPU)"
    elif [ "$_vram_gb" -ge 12 ]; then
        _recommended="qwen2.5:14b"
        _tier_label="12 to 16 GB VRAM tier (RTX 5070/5070 Ti/5080)"
    elif [ "$_vram_gb" -ge 7 ]; then
        _recommended="qwen3:8b"
        _tier_label="8 GB VRAM tier (RTX 5060 Ti 8 GB or equivalent)"
    elif [ "$_ram_gb" -ge 96 ]; then
        # Large unified memory (e.g. Apple M-series 96/128 GB, DGX Spark without nvidia-smi)
        _recommended="qwen2.5:72b"
        _tier_label="128 GB unified-memory tier (detected via RAM, no discrete GPU)"
    elif [ "$_ram_gb" -ge 24 ]; then
        _recommended="qwen3:32b"
        _tier_label="24+ GB RAM tier (CPU inference, large memory)"
    elif [ "$_ram_gb" -ge 12 ]; then
        _recommended="qwen2.5:14b"
        _tier_label="12 to 16 GB RAM tier (CPU inference, mid memory)"
    else
        _recommended="phi4-mini"
        _tier_label="low-resource / CPU-only tier (< 12 GB RAM)"
    fi

    printf '\n'
    printf '%s  Model Recommendation Table%s\n' "${BOLD}" "${RESET}"
    printf '  %-30s  %-16s  %s\n' "Hardware tier" "Ollama tag" "Size"
    printf '  %-30s  %-16s  %s\n' "------------------------------" "----------------" "------"
    printf '  %-30s  %-16s  %s\n' "CPU-only / <12 GB RAM" "phi4-mini" "2.5 GB"
    printf '  %-30s  %-16s  %s\n' "8 GB VRAM" "qwen3:8b" "5.2 GB"
    printf '  %-30s  %-16s  %s\n' "12 to 16 GB VRAM" "qwen2.5:14b" "9.0 GB"
    printf '  %-30s  %-16s  %s\n' "24+ GB VRAM" "qwen3:32b" "20 GB"
    printf '  %-30s  %-16s  %s\n' "128 GB unified memory" "qwen2.5:72b" "47 GB"
    printf '\n'
    printf '  Detected tier : %s\n' "$_tier_label"
    printf '  Recommended   : %s%s%s\n' "${GREEN}" "$_recommended" "${RESET}"

    # ── Tier 2: optional npx llm-checker enrichment ─────────────────────────
    if command -v node >/dev/null 2>&1 && command -v npx >/dev/null 2>&1; then
        _node_major="$(node --version 2>/dev/null | sed 's/v\([0-9]*\).*/\1/' || printf '0')"
        if [ "$_node_major" -ge 16 ] 2>/dev/null; then
            printf '\n'
            printf '  Node.js %s detected. Running npx llm-checker for richer GPU analysis...\n' "$(node --version)"
            printf '  (llm-checker by Pavelevich, npm:llm-checker, optional enrichment only)\n'
            if npx --yes llm-checker hw-detect 2>/dev/null; then
                printf '  llm-checker analysis shown above. Bash detection recommendation stands as default.\n'
            else
                printf '  llm-checker unavailable or failed. Bash detection result used.\n'
            fi
        fi
    else
        printf '\n'
        printf '  Node.js not found. Skipping optional llm-checker enrichment.\n'
        printf '  Install Node.js >= 16 and re-run for richer GPU analysis:\n'
        printf '    npx llm-checker hw-detect\n'
    fi

    # ── Accept or override ──────────────────────────────────────────────────
    if [ "$NON_INTERACTIVE" = "true" ] || [ ! -t 0 ]; then
        OLLAMA_MODEL="$_recommended"
        status "Non-interactive mode: using recommended model '$OLLAMA_MODEL'."
    else
        printf '\n'
        printf '%s  Accept recommended model [%s] or enter a different tag%s: ' \
            "${BOLD}" "$_recommended" "${RESET}"
        read -r _model_input
        if [ -n "$_model_input" ]; then
            OLLAMA_MODEL="$_model_input"
        else
            OLLAMA_MODEL="$_recommended"
        fi
        success "Model selected: $OLLAMA_MODEL"
    fi

    # ── Offer to pull now ───────────────────────────────────────────────────
    printf '\n'
    _pull_cmd="docker exec hive-ollama-1 ollama pull $OLLAMA_MODEL"
    if [ "$NON_INTERACTIVE" = "true" ] || [ ! -t 0 ]; then
        printf '  Pull the model after the stack is running:\n'
        printf '    %s\n' "$_pull_cmd"
    else
        printf '%s  Pull model now? This downloads %s from the Ollama registry. [Y/n]%s: ' \
            "${BOLD}" "$OLLAMA_MODEL" "${RESET}"
        read -r _pull_answer
        case "${_pull_answer:-Y}" in
            [Yy]|[Yy][Ee][Ss]|"")
                status "Will pull '$OLLAMA_MODEL' after the stack starts."
                # Store the pull intent; executed in verify_and_banner after stack is up
                OLLAMA_PULL_NOW=true
                ;;
            *)
                printf '  Skipping pull. Run manually after the stack is up:\n'
                printf '    %s\n' "$_pull_cmd"
                OLLAMA_PULL_NOW=false
                ;;
        esac
    fi
}

# ─── Ollama litellm config patch ───────────────────────────────────────────────
patch_ollama_config() {
    if [ "$WITH_OLLAMA" != "true" ]; then
        return
    fi

    LITELLM_CONFIG="$HIVE_HOME/deploy/litellm/config.yaml"
    if [ ! -f "$LITELLM_CONFIG" ]; then
        warn "LiteLLM config not found at $LITELLM_CONFIG, skipping Ollama patch."
        return
    fi

    status "Enabling Ollama model entries in LiteLLM config..."
    # Uncomment only the YAML model-entry lines in the ollama block.
    # These lines contain YAML keys (model_name, litellm_params, model, api_base)
    # and are formatted as "#   - key:" or "#     key:".
    # Prose comment lines (instructions, numbered steps, docker exec examples)
    # do not contain these keys so they are left untouched, preventing
    # LiteLLM from receiving a malformed config at startup.
    for _key in 'model_name:' 'litellm_params:' 'api_base:'; do
        $SUDO sed -i "s|^# \([ ]*- $_key\)|\1|;s|^# \([ ]*$_key\)|\1|" "$LITELLM_CONFIG" || true
    done
    # model: is ambiguous with "model_name:" so match the exact key word
    $SUDO sed -i 's|^# \([ ]*model: \)|\1|' "$LITELLM_CONFIG" || true
    success "Ollama model entries uncommented in $LITELLM_CONFIG"

    # If the model advisor resolved a specific model, insert it inside the
    # model_list block (before the litellm_settings: line) so LiteLLM can
    # route it. Appending after files_settings would place the entry outside
    # model_list and produce a malformed config.
    if [ -n "${OLLAMA_MODEL:-}" ]; then
        if ! grep -qF "ollama_chat/${OLLAMA_MODEL}" "$LITELLM_CONFIG" 2>/dev/null; then
            status "Inserting advisor-selected model '$OLLAMA_MODEL' into LiteLLM model_list..."
            $SUDO sed -i "s|^litellm_settings:|  - model_name: ollama/${OLLAMA_MODEL}\n    litellm_params:\n      model: ollama_chat/${OLLAMA_MODEL}\n      api_base: os.environ/OLLAMA_BASE_URL\n\nlitellm_settings:|" "$LITELLM_CONFIG" || true
            success "Model '$OLLAMA_MODEL' added to $LITELLM_CONFIG"
        else
            status "Model '$OLLAMA_MODEL' already present in $LITELLM_CONFIG, skipping."
        fi
    fi

    warn "Review $LITELLM_CONFIG to confirm the ollama model entries are correct."
}

# ─── Health polling ────────────────────────────────────────────────────────────
wait_healthy() {
    _service="$1"
    _url="$2"
    _timeout=120
    _elapsed=0
    _interval=5

    status "Waiting for $_service at $_url (timeout: ${_timeout}s)..."
    while [ "$_elapsed" -lt "$_timeout" ]; do
        if command -v curl >/dev/null 2>&1; then
            _code="$(curl -sf -o /dev/null -w '%{http_code}' "$_url" 2>/dev/null || printf '000')"
        else
            _code="$(wget -q -S -O /dev/null "$_url" 2>&1 | grep 'HTTP/' | tail -1 | awk '{print $2}' || printf '000')"
        fi
        if [ "$_code" = "200" ]; then
            success "$_service is healthy"
            return 0
        fi
        printf '  ... still waiting (%ds elapsed, HTTP %s)\n' "$_elapsed" "$_code"
        sleep "$_interval"
        _elapsed=$((_elapsed + _interval))
    done
    return 1
}

# ─── Start stack ──────────────────────────────────────────────────────────────
start_stack() {
    status "Starting Hive EnterpriseEdge stack (docker compose --profile enterprise)..."
    cd "$HIVE_HOME/deploy/docker"
    $SUDO docker compose --env-file "$HIVE_HOME/.env" --profile enterprise up -d --build

    success "Stack started."
}

# ─── Health check and banner ──────────────────────────────────────────────────
verify_and_banner() {
    EDGE_OK=false
    CP_OK=false

    wait_healthy "edge-api"      "http://localhost:8080/health" && EDGE_OK=true || true
    wait_healthy "control-plane" "http://localhost:8081/health" && CP_OK=true || true

    printf '\n'
    if [ "$EDGE_OK" = "true" ] && [ "$CP_OK" = "true" ]; then
        printf '%s%s\n' "${GREEN}" "$(printf '%.0s=' $(seq 1 60))"
        printf '  Hive EnterpriseEdge is running!\n'
        printf '%s\n\n' "${RESET}"
        printf '  Edge API        http://localhost:8080\n'
        printf '  Control Plane   http://localhost:8081\n'
        printf '  Web Console     http://localhost:3000\n'
        printf '  Open WebUI      http://localhost:3003\n'
        printf '  LiteLLM         http://localhost:4000\n'
        printf '\n'
        printf '  Run with monitoring:\n'
        printf '    cd %s/deploy/docker\n' "$HIVE_HOME"
        printf '    docker compose --env-file %s/.env --profile enterprise --profile monitoring up -d\n' "$HIVE_HOME"
        printf '\n'
        if [ "$WITH_OLLAMA" = "true" ]; then
            printf '  Ollama:         http://localhost:11434 (in-stack)\n'
            if [ -n "${OLLAMA_MODEL:-}" ]; then
                printf '  Ollama model:   %s\n' "$OLLAMA_MODEL"
            fi
            printf '\n'
        fi
        printf '%s%s%s\n' "${GREEN}" "$(printf '%.0s=' $(seq 1 60))" "${RESET}"

        # Pull the advisor-selected Ollama model now that the stack is healthy
        if [ "$WITH_OLLAMA" = "true" ] && [ -n "${OLLAMA_MODEL:-}" ] && [ "${OLLAMA_PULL_NOW:-false}" = "true" ]; then
            printf '\n'
            status "Pulling Ollama model '$OLLAMA_MODEL' (this may take several minutes)..."
            if $SUDO docker exec hive-ollama-1 ollama pull "$OLLAMA_MODEL"; then
                success "Model '$OLLAMA_MODEL' pulled successfully."
            else
                warn "Model pull failed. Run manually: docker exec hive-ollama-1 ollama pull $OLLAMA_MODEL"
            fi
        fi
    else
        printf '%s>>> Some services did not become healthy within the timeout.%s\n' "${RED}" "${RESET}"
        printf '\nDiagnostics:\n'
        printf '  cd %s/deploy/docker\n' "$HIVE_HOME"
        printf '  docker compose --env-file %s/.env --profile enterprise logs --tail=50\n' "$HIVE_HOME"
        printf '\nCommon causes:\n'
        printf '  - .env is missing required values (check %s/.env)\n' "$HIVE_HOME"
        printf '  - Supabase Storage buckets hive-files / hive-images do not exist yet\n'
        printf '  - Port 8080 or 8081 is already in use\n'
        exit 1
    fi
}

# ─── Main ─────────────────────────────────────────────────────────────────────
# Everything is wrapped inside main() so a truncated partial download via
# curl | bash cannot execute half a script. (Pattern from Ollama + uv installers.)
main() {
    # shellcheck disable=SC2048,SC2086
    parse_args ${INSTALL_ARGS:-} "$@"

    printf '\n'
    printf '%s%s\n' "${BOLD}" "$(printf '%.0s=' $(seq 1 60))"
    printf '  Hive EnterpriseEdge Installer\n'
    printf '%s\n\n' "${RESET}"

    detect_platform

    if [ "$UNINSTALL" = "true" ]; then
        do_uninstall
    fi

    install_docker
    clone_or_update_repo
    setup_env
    advise_ollama_model
    patch_ollama_config
    start_stack
    verify_and_banner
}

main "$@"
