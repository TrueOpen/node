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
#   - business    : config/localnet_genesis_seed.json phase0.business_denom
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
#   DENOM=uusdc                        # overrides the seed business_denom
#   GENESIS_BALANCE=100000000000000uusdc,1000000000ubond
#   SELF_DELEGATION=1000000000ubond
#   MIN_GAS_PRICES=0uusdc
#   KEY_MNEMONIC="word1 ... word24"   # recover a deterministic key instead of random
#   FAST_BLOCKS=1                      # ~1s commit for snappy local dev
#   GOV_FAST=1                         # shrink x/gov params (short voting/deposit
#                                       # periods, tiny min_deposit) for testing
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
GENESIS_SEED_FILE="${GENESIS_SEED_FILE:-$REPO_ROOT/config/localnet_genesis_seed.json}"
DENOM="${DENOM:-}"
GENESIS_BALANCE="${GENESIS_BALANCE:-}"
SELF_DELEGATION="${SELF_DELEGATION:-1000000000ubond}"
MIN_GAS_PRICES="${MIN_GAS_PRICES:-}"
RESET="${RESET:-0}"
START="${START:-1}"
BUILD="${BUILD:-0}"
FAST_BLOCKS="${FAST_BLOCKS:-0}"
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
# GOV_FAST=1 shrinks x/gov params in genesis for quick testing of the full
# proposal -> deposit -> vote -> execute cycle: short voting/deposit periods
# and a tiny min_deposit. Quorum/threshold stay at defaults (a single
# validator holds ~100% voting power on localnet, so they pass trivially).
# Do NOT use on a real network. Individual values below are overridable.
#
# It only touches periods and deposit amounts. The Phase 0 deposit POLICY is
# owned by `noded genesis apply-seed` (cmd/noded/cmd/genesis_seed.go
# applySDKGenesisParams), which runs after this block and pins
# burn_vote_veto=true plus the deposit denom. That is deliberate: the "burn"
# is intercepted by GovernedGovBankKeeper and routed to hub_treasury with
# a matching treasury-inflow record, so turning the burn off here would silently
# disable the residual. Setting proposal_cancel_dest would
# break it the other way, by sending the cancellation fee straight to the
# treasury account and skipping that record. Leave both alone.
GOV_FAST="${GOV_FAST:-0}"
# Defaults are short but leave enough time to submit + query + vote before the
# voting period closes. Override any of them as needed.
GOV_VOTING_PERIOD="${GOV_VOTING_PERIOD:-300s}"
GOV_MAX_DEPOSIT_PERIOD="${GOV_MAX_DEPOSIT_PERIOD:-300s}"
# Left empty by default: gov requires expedited_voting_period to be strictly
# less than voting_period, so it is derived from GOV_VOTING_PERIOD below unless
# you set it explicitly. Setting only GOV_VOTING_PERIOD used to produce a
# confusing "expedited voting period must be strictly less" genesis error.
GOV_EXPEDITED_VOTING_PERIOD="${GOV_EXPEDITED_VOTING_PERIOD:-}"
GOV_MIN_DEPOSIT="${GOV_MIN_DEPOSIT:-1000000}"
GOV_EXPEDITED_MIN_DEPOSIT="${GOV_EXPEDITED_MIN_DEPOSIT:-5000000}"

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

# The seed owns the localnet business denom. DENOM remains an explicit override
# for tests that need a different genesis without editing the checked-in seed.
if [ -z "$DENOM" ] && [ -f "$GENESIS_SEED_FILE" ]; then
    if command -v python3 >/dev/null 2>&1; then
        DENOM_PYTHON=python3
    elif command -v python >/dev/null 2>&1; then
        DENOM_PYTHON=python
    else
        die "python3 or python is required to read business_denom from $GENESIS_SEED_FILE"
    fi
    DENOM="$($DENOM_PYTHON - "$GENESIS_SEED_FILE" <<'PY'
import json
import pathlib
import sys

seed = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
denom = seed.get("hub_params", {}).get("phase0", {}).get("business_denom", "")
if not isinstance(denom, str) or not denom or denom != denom.strip():
    raise SystemExit("hub_params.phase0.business_denom must be a non-empty canonical string")
print(denom)
PY
)"
fi
DENOM="${DENOM:-uusdc}"
GENESIS_BALANCE="${GENESIS_BALANCE:-100000000000000${DENOM},1000000000ubond}"
MIN_GAS_PRICES="${MIN_GAS_PRICES:-0${DENOM}}"

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

if [ "$GOV_FAST" = "1" ]; then
    # Same interpreter resolution as the node_config block below: some
    # platforms (Windows/Git Bash in particular) ship only "python".
    if command -v python3 >/dev/null 2>&1; then
        GOV_PYTHON=python3
    elif command -v python >/dev/null 2>&1; then
        GOV_PYTHON=python
    else
        die "python3 or python is required for GOV_FAST"
    fi
    GENESIS_JSON="$HOME_DIR/config/genesis.json"
    GOV_VOTING_PERIOD="$GOV_VOTING_PERIOD" \
    GOV_MAX_DEPOSIT_PERIOD="$GOV_MAX_DEPOSIT_PERIOD" \
    GOV_EXPEDITED_VOTING_PERIOD="$GOV_EXPEDITED_VOTING_PERIOD" \
    GOV_MIN_DEPOSIT="$GOV_MIN_DEPOSIT" \
    GOV_EXPEDITED_MIN_DEPOSIT="$GOV_EXPEDITED_MIN_DEPOSIT" \
    DENOM="$DENOM" \
    "$GOV_PYTHON" - "$GENESIS_JSON" <<'PY'
import json
import os
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
genesis = json.loads(path.read_text(encoding="utf-8"))
gov = genesis["app_state"]["gov"]["params"]
denom = os.environ["DENOM"]

def to_seconds(text):
    """Parse a Go-ish duration ("90s", "2m", "1h30m") into whole seconds."""
    text = text.strip()
    total, number = 0, ""
    units = {"h": 3600, "m": 60, "s": 1}
    for ch in text:
        if ch.isdigit():
            number += ch
        elif ch in units and number:
            total += int(number) * units[ch]
            number = ""
        elif ch == ".":
            break
    if number:
        total += int(number)
    return total


voting = os.environ["GOV_VOTING_PERIOD"]
voting_secs = to_seconds(voting)
if voting_secs <= 0:
    raise SystemExit(f"GOV_VOTING_PERIOD {voting!r} must be a positive duration")

# gov requires expedited_voting_period < voting_period. Derive it when the
# operator did not pin one, so overriding only GOV_VOTING_PERIOD just works.
expedited = os.environ.get("GOV_EXPEDITED_VOTING_PERIOD", "").strip()
if not expedited:
    expedited = f"{max(1, voting_secs // 2)}s"
elif to_seconds(expedited) >= voting_secs:
    raise SystemExit(
        f"GOV_EXPEDITED_VOTING_PERIOD {expedited} must be strictly less than "
        f"GOV_VOTING_PERIOD {voting}")

gov["voting_period"] = voting
gov["max_deposit_period"] = os.environ["GOV_MAX_DEPOSIT_PERIOD"]
gov["expedited_voting_period"] = expedited
gov["min_deposit"] = [{"denom": denom, "amount": os.environ["GOV_MIN_DEPOSIT"]}]
gov["expedited_min_deposit"] = [{"denom": denom, "amount": os.environ["GOV_EXPEDITED_MIN_DEPOSIT"]}]
path.write_text(json.dumps(genesis, indent=2) + "\n", encoding="utf-8")
PY
    log "    GOV_FAST: voting=$GOV_VOTING_PERIOD deposit_period=$GOV_MAX_DEPOSIT_PERIOD min_deposit=$GOV_MIN_DEPOSIT$DENOM"
fi

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
