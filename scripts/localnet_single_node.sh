#!/usr/bin/env bash
#
# localnet_single_node.sh — bootstrap and launch a fresh single-node TrueOpen
# localnet from nothing.
#
# One command takes you from an empty machine to a producing node:
#   init -> validator key -> genesis account -> gentx -> collect-gentxs
#   -> validate -> localnet config -> start.
#
# Chain facts (from app/config.go + app/app.go, do not change lightly):
#   - binary name : noded
#   - bond denom  : ubond    (consensus accounting only)
#   - business    : uusdc    (fees and protocol funds)
#   - bech32      : trueopen    (AccountAddressPrefix)
#   - default home: ~/.node
#
# Usage:
#   scripts/localnet_single_node.sh                 # build if needed, init, start
#   RESET=1 scripts/localnet_single_node.sh         # wipe an existing home first
#   START=0 scripts/localnet_single_node.sh         # set up but do not start
#   BUILD=1 scripts/localnet_single_node.sh         # force `make build` first
#
# Common overrides (env vars, all optional):
#   CHAIN_ID=trueopen-localnet-1
#   MONIKER=trueopen-localnode
#   HOME_DIR=$HOME/.node
#   KEY_NAME=validator
#   KEYRING=test
#   GENESIS_BALANCE=100000000000000uusdc,1000000000ubond
#   SELF_DELEGATION=1000000000ubond
#   MIN_GAS_PRICES=0uusdc
#   KEY_MNEMONIC="word1 ... word24"   # recover a deterministic key instead of random
#   FAST_BLOCKS=1                      # ~1s commit for snappy local dev
#   GENESIS_SEED_FILE=/path/to/seed.json
#                                       # optional Builder/Cortex/model seed
#
# Note: this is a bash script. If invoked as `sh script.sh` on a system
# where /bin/sh is dash (Debian/Ubuntu), the guard below re-execs it under
# bash so bashisms like `set -o pipefail` work regardless of how it's run.

# --- ensure we are running under bash (POSIX-sh compatible guard) ----------
if [ -z "${BASH_VERSION:-}" ]; then
    exec bash "$0" "$@"
fi

set -euo pipefail

# --- resolve paths ---------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

CHAIN_ID="${CHAIN_ID:-trueopen-localnet-1}"
MONIKER="${MONIKER:-trueopen-localnode}"
HOME_DIR="${HOME_DIR:-$HOME/.node}"
KEY_NAME="${KEY_NAME:-validator}"
KEYRING="${KEYRING:-test}"
DENOM="${DENOM:-uusdc}"
GENESIS_BALANCE="${GENESIS_BALANCE:-100000000000000uusdc,1000000000ubond}"
SELF_DELEGATION="${SELF_DELEGATION:-1000000000ubond}"
MIN_GAS_PRICES="${MIN_GAS_PRICES:-0uusdc}"
RESET="${RESET:-0}"
START="${START:-1}"
BUILD="${BUILD:-0}"
FAST_BLOCKS="${FAST_BLOCKS:-0}"
GENESIS_SEED_FILE="${GENESIS_SEED_FILE:-$REPO_ROOT/config/localnet_genesis_seed.json}"
# BIND_ALL=1 binds REST(1317) / gRPC(9090) / RPC(26657) to 0.0.0.0 so the
# node is reachable from outside the host (e.g. a VPS). Default 0 = bind to
# localhost only. NOTE: exposing RPC/gRPC to the public internet is risky —
# firewall the ports and prefer a reverse proxy / auth in front of them.
BIND_ALL="${BIND_ALL:-0}"
# ENABLE_CORS=1 opens CORS on REST(1317, [api].enabled-unsafe-cors) and
# RPC(26657, [rpc].cors_allowed_origins) so a browser explorer (e.g. Ping) on
# another origin can query the node. Defaults to BIND_ALL (exposing off-host
# usually implies browser access). CORS_ORIGINS controls the RPC allow-list.
# NOTE: "*" is convenient for dev but permissive — restrict on any real deploy.
ENABLE_CORS="${ENABLE_CORS:-$BIND_ALL}"
CORS_ORIGINS="${CORS_ORIGINS:-*}"

# --- resolve the noded binary ----------------------------------------------
# Prefer the repo build output; fall back to noded on PATH.
if [ -n "${NODED_BIN:-}" ]; then
    NODED="$NODED_BIN"
elif [ -x "$REPO_ROOT/build/noded" ]; then
    NODED="$REPO_ROOT/build/noded"
elif command -v noded >/dev/null 2>&1; then
    NODED="$(command -v noded)"
else
    NODED="$REPO_ROOT/build/noded"
fi

log()  { printf '\033[1;34m[localnet]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[localnet]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[localnet]\033[0m %s\n' "$*" >&2; exit 1; }

noded() { "$NODED" "$@"; }

# --- build if requested or missing -----------------------------------------
if [ "$BUILD" = "1" ] || [ ! -x "$NODED" ] ||
   { [ -f "$GENESIS_SEED_FILE" ] && [ "$REPO_ROOT/cmd/noded/cmd/genesis_seed.go" -nt "$NODED" ]; }; then
    log "building noded (make build)…"
    make -C "$REPO_ROOT" build
    NODED="$REPO_ROOT/build/noded"
fi
[ -x "$NODED" ] || die "noded binary not found/executable at: $NODED"
log "using binary: $NODED ($(noded version 2>/dev/null || echo unknown))"

# --- guard against clobbering an existing home ------------------------------
if [ -e "$HOME_DIR" ]; then
    if [ "$RESET" = "1" ]; then
        warn "RESET=1 — removing existing home $HOME_DIR"
        rm -rf "$HOME_DIR"
    else
        die "home $HOME_DIR already exists. Re-run with RESET=1 to wipe it, or set HOME_DIR to a fresh path."
    fi
fi

# --- portable in-place sed --------------------------------------------------
# macOS/BSD sed needs an explicit empty suffix for -i; GNU sed does not.
sed_inplace() {
    if sed --version >/dev/null 2>&1; then
        sed -i "$@"
    else
        sed -i '' "$@"
    fi
}

# ---------------------------------------------------------------------------
log "1/8 init chain '$CHAIN_ID' (moniker '$MONIKER') at $HOME_DIR"
noded init "$MONIKER" --chain-id "$CHAIN_ID" --home "$HOME_DIR" >/dev/null 2>&1

log "2/8 create validator key '$KEY_NAME' (keyring: $KEYRING)"
if [ -n "${KEY_MNEMONIC:-}" ]; then
    printf '%s\n' "$KEY_MNEMONIC" | noded keys add "$KEY_NAME" \
        --recover --keyring-backend "$KEYRING" --home "$HOME_DIR" >/dev/null
else
    # Capture the mnemonic so the operator can recover the key later.
    noded keys add "$KEY_NAME" --keyring-backend "$KEYRING" --home "$HOME_DIR" \
        2> "$HOME_DIR/${KEY_NAME}.mnemonic.txt"
    warn "mnemonic written to $HOME_DIR/${KEY_NAME}.mnemonic.txt (dev only — do NOT reuse on mainnet)"
fi
KEY_ADDR="$(noded keys show "$KEY_NAME" -a --keyring-backend "$KEYRING" --home "$HOME_DIR")"
log "    validator address: $KEY_ADDR"

log "3/8 add genesis account: $GENESIS_BALANCE"
noded genesis add-genesis-account "$KEY_NAME" "$GENESIS_BALANCE" \
    --keyring-backend "$KEYRING" --home "$HOME_DIR"

if [ ! -f "$GENESIS_SEED_FILE" ]; then
    log "    genesis seed not found at $GENESIS_SEED_FILE; skipping optional seed"
fi

log "4/8 gentx self-delegation: $SELF_DELEGATION"
noded genesis gentx "$KEY_NAME" "$SELF_DELEGATION" \
    --chain-id "$CHAIN_ID" --keyring-backend "$KEYRING" --home "$HOME_DIR" >/dev/null 2>&1

log "5/8 collect-gentxs"
noded genesis collect-gentxs --home "$HOME_DIR" >/dev/null 2>&1

if [ -f "$GENESIS_SEED_FILE" ]; then
    noded genesis apply-seed "$GENESIS_SEED_FILE" --home "$HOME_DIR"
    log "    genesis accounts, emission, Builder, Cortex, bond, model, profile, and support state applied"
fi

log "6/8 validate genesis"
noded genesis validate --home "$HOME_DIR"

log "7/8 apply localnet config"
CLIENT_TOML="$HOME_DIR/config/client.toml"
APP_TOML="$HOME_DIR/config/app.toml"
CONFIG_TOML="$HOME_DIR/config/config.toml"

# client.toml: so `noded query/tx` need no repeated --chain-id/--node/--keyring flags.
sed_inplace "s/^chain-id = .*/chain-id = \"$CHAIN_ID\"/" "$CLIENT_TOML"
sed_inplace "s/^keyring-backend = .*/keyring-backend = \"$KEYRING\"/" "$CLIENT_TOML"
sed_inplace "s#^node = .*#node = \"tcp://127.0.0.1:26657\"#" "$CLIENT_TOML"

# app.toml: localnet zero-fee, enable REST + gRPC for local tooling.
sed_inplace "s/^minimum-gas-prices = .*/minimum-gas-prices = \"$MIN_GAS_PRICES\"/" "$APP_TOML"
# Enable the API (REST) server — the [api] enable key is the first `enable`
# after the "[api]" section header.
awk '
    /^\[api\]/ {inapi=1}
    inapi && /^enable = / && !done {sub(/false/,"true"); done=1}
    /^\[/ && !/^\[api\]/ {inapi=0}
    {print}
' "$APP_TOML" > "$APP_TOML.tmp" && mv "$APP_TOML.tmp" "$APP_TOML"

if [ "$FAST_BLOCKS" = "1" ]; then
    log "    FAST_BLOCKS=1 — setting ~1s commit timeout"
    sed_inplace 's/^timeout_commit = .*/timeout_commit = "1s"/' "$CONFIG_TOML"
fi

# set_in_section rewrites `<key> = ...` to `repl` only inside the given TOML
# section header, so e.g. [grpc].address is changed without touching
# [grpc-web].address or [api].address.
set_in_section() {
    local file="$1" section="$2" key="$3" repl="$4"
    awk -v sec="$section" -v key="$key" -v repl="$repl" '
        $0 == sec { insec = 1; print; next }
        /^\[/ && $0 != sec { insec = 0 }
        insec && $0 ~ ("^" key " = ") && !done { print repl; done = 1; next }
        { print }
    ' "$file" > "$file.tmp" && mv "$file.tmp" "$file"
}

# node_config is local tooling input, not consensus state. apply-seed ignores
# this section; the localnet bootstrap projects it into app.toml here.
TASK_EVENT_GRPC_ENABLED=false
PROTOCOL_EVENTS_ENABLED=false
if [ -f "$GENESIS_SEED_FILE" ]; then
    if command -v python3 >/dev/null 2>&1; then
        PYTHON=python3
    elif command -v python >/dev/null 2>&1; then
        PYTHON=python
    else
        die "python3 or python is required to read node_config from the genesis seed"
    fi
    read -r TASK_EVENT_GRPC_ENABLED PROTOCOL_EVENTS_ENABLED < <(
        "$PYTHON" - "$GENESIS_SEED_FILE" <<'PY'
import json
import pathlib
import sys

seed = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
config = seed.get("node_config", {}).get("task_event_grpc", {})
enabled = bool(config.get("enabled", False))
protocol_enabled = bool(config.get("protocol_events_enabled", False))
if protocol_enabled and not enabled:
    raise SystemExit("node_config.task_event_grpc.protocol_events_enabled requires enabled=true")
print(str(enabled).lower(), str(protocol_enabled).lower())
PY
    )
fi
set_in_section "$APP_TOML" "[task-event-grpc]" "enabled" \
    "enabled = $TASK_EVENT_GRPC_ENABLED"
set_in_section "$APP_TOML" "[task-event-grpc]" "protocol-events-enabled" \
    "protocol-events-enabled = $PROTOCOL_EVENTS_ENABLED"
log "    task-event-grpc enabled=$TASK_EVENT_GRPC_ENABLED protocol-events-enabled=$PROTOCOL_EVENTS_ENABLED"

REST_HOST="localhost"; GRPC_HOST="localhost"; RPC_HOST="127.0.0.1"
if [ "$BIND_ALL" = "1" ]; then
    log "    BIND_ALL=1 — binding REST/gRPC/RPC to 0.0.0.0 (reachable off-host)"
    set_in_section "$APP_TOML"    "[api]"  "address" 'address = "tcp://0.0.0.0:1317"'
    set_in_section "$APP_TOML"    "[grpc]" "address" 'address = "0.0.0.0:9090"'
    set_in_section "$CONFIG_TOML" "[rpc]"  "laddr"   'laddr = "tcp://0.0.0.0:26657"'
    # client.toml should still talk to the node over loopback locally.
    REST_HOST="0.0.0.0"; GRPC_HOST="0.0.0.0"; RPC_HOST="0.0.0.0"
fi

if [ "$ENABLE_CORS" = "1" ]; then
    log "    ENABLE_CORS=1 — opening CORS on REST(1317) and RPC(26657); origins=$CORS_ORIGINS"
    # REST/LCD (app.toml [api]): allow cross-origin browser requests.
    set_in_section "$APP_TOML"    "[api]" "enabled-unsafe-cors" 'enabled-unsafe-cors = true'
    # CometBFT RPC (config.toml [rpc]): cors_allowed_origins is a TOML list.
    set_in_section "$CONFIG_TOML" "[rpc]" "cors_allowed_origins" \
        "cors_allowed_origins = [\"$CORS_ORIGINS\"]"
fi

# ---------------------------------------------------------------------------
cat <<EOF

$(log "8/8 setup complete")
  chain-id   : $CHAIN_ID
  home       : $HOME_DIR
  validator  : $KEY_NAME -> $KEY_ADDR
  denom      : $DENOM  (min-gas-prices=$MIN_GAS_PRICES)
  endpoints  : RPC $RPC_HOST:26657 | REST $REST_HOST:1317 | gRPC $GRPC_HOST:9090$([ "$BIND_ALL" = "1" ] && printf '\n  bind-all   : ON — open firewall/security-group for 1317/9090/26657; do NOT expose RPC/gRPC publicly without a proxy/auth')

  Useful commands (client.toml already points at this node):
    $NODED status
    $NODED query hub params -o json
    $NODED query task params -o json
    $NODED query bank balances $KEY_ADDR
    $NODED keys list --keyring-backend $KEYRING --home $HOME_DIR

EOF

if [ "$START" = "1" ]; then
    log "starting node (Ctrl-C to stop)…"
    exec "$NODED" start \
        --home "$HOME_DIR" \
        --minimum-gas-prices "$MIN_GAS_PRICES" \
        --api.enable \
        --grpc.enable
else
    log "START=0 — not launching. Start it yourself with:"
    log "  $NODED start --home $HOME_DIR --minimum-gas-prices $MIN_GAS_PRICES --api.enable --grpc.enable"
fi
