#!/usr/bin/env bash
#
# testnet_multi_node.sh — prepare N validator home directories for a
# multi-node TrueOpen testnet, all agreeing on one genesis.
#
# localnet_single_node.sh is single-machine: it inits, gentx's, and
# collect-gentxs's all on the same home. A multi-validator network needs an
# extra round trip that script has no place for: every validator's gentx has
# to be gathered in one place before any of them can collect-gentxs, and only
# then does every validator's node-id (known only after ITS OWN init) go into
# every other validator's persistent_peers.
#
# This script does the local half of that: it builds N home directories
# side by side, runs init/gentx locally for each, collects them all into one
# genesis, copies that genesis back to every home, and writes
# each home's persistent_peers from the others' node-ids + given IPs.
# It does not touch the remote hosts — see "deploy" below for that half.
#
# One command takes 4 empty home directories to "ready to scp and start":
#   init x4 -> validator keys x4 -> genesis accounts -> gentx x4
#   -> collect-gentxs (once) -> apply-seed (once) -> copy genesis to all
#   -> persistent_peers x4 -> per-node config
#
# Usage:
#   TESTNET_IPS=1.2.3.4,5.6.7.8,9.10.11.12,13.14.15.16 scripts/testnet_multi_node.sh
#   RESET=1 TESTNET_IPS=... scripts/testnet_multi_node.sh   # wipe WORK_DIR first
#
# Common overrides (env vars):
#   CHAIN_ID=trueopen-testnet-1
#   TESTNET_IPS=ip1,ip2,ip3,...        # required — one per validator, in order
#   MONIKERS=name1,name2,...           # optional, defaults to testnet-node-<i>
#   WORK_DIR=$REPO_ROOT/build/testnet-homes
#   KEYRING=test
#   DENOM=uusdc                        # overrides the seed business_denom
#   GENESIS_BALANCE=100000000000000uusdc,1000000000ubond
#   SELF_DELEGATION=1000000000ubond
#   MIN_GAS_PRICES=0uusdc
#   FAST_BLOCKS=0                      # real block timing by default (unlike localnet)
#   GOV_FAST=0                         # real gov periods by default (unlike localnet)
#   GENESIS_SEED_FILE=$REPO_ROOT/config/testnet_genesis_seed.json
#   P2P_PORT=26656
#
# What this script deliberately does NOT do:
#   - It does not SSH/scp anything. Bastion/jump-host access is per-operator
#     and out of scope here. After it prints "ready to deploy", copy each
#     WORK_DIR/node-<i> directory to its matching host's node home and start
#     `noded start --home <home>` there — the printed summary has the exact
#     per-node command.
#   - It does not touch VRF keys. Generate those on each validator's own
#     machine with `noded beacon vrf-keygen`; the private key must never
#     leave the box it runs on.

if [ -z "${BASH_VERSION:-}" ]; then
    exec bash "$0" "$@"
fi

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

CHAIN_ID="${CHAIN_ID:-trueopen-testnet-1}"
WORK_DIR="${WORK_DIR:-$REPO_ROOT/build/testnet-homes}"
KEYRING="${KEYRING:-test}"
GENESIS_SEED_FILE="${GENESIS_SEED_FILE:-$REPO_ROOT/config/testnet_genesis_seed.json}"
DENOM="${DENOM:-}"
GENESIS_BALANCE="${GENESIS_BALANCE:-}"
SELF_DELEGATION="${SELF_DELEGATION:-1000000000ubond}"
MIN_GAS_PRICES="${MIN_GAS_PRICES:-}"
RESET="${RESET:-0}"
BUILD="${BUILD:-0}"
FAST_BLOCKS="${FAST_BLOCKS:-0}"
GOV_FAST="${GOV_FAST:-0}"
GOV_VOTING_PERIOD="${GOV_VOTING_PERIOD:-172800s}"
GOV_MAX_DEPOSIT_PERIOD="${GOV_MAX_DEPOSIT_PERIOD:-172800s}"
GOV_EXPEDITED_VOTING_PERIOD="${GOV_EXPEDITED_VOTING_PERIOD:-}"
GOV_MIN_DEPOSIT="${GOV_MIN_DEPOSIT:-1000000}"
GOV_EXPEDITED_MIN_DEPOSIT="${GOV_EXPEDITED_MIN_DEPOSIT:-5000000}"
P2P_PORT="${P2P_PORT:-26656}"

log()  { printf '\033[1;34m[testnet]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[testnet]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[testnet]\033[0m %s\n' "$*" >&2; exit 1; }

[ -n "${TESTNET_IPS:-}" ] || die "TESTNET_IPS is required, e.g. TESTNET_IPS=1.2.3.4,5.6.7.8,9.10.11.12,13.14.15.16"
IFS=',' read -r -a IPS <<< "$TESTNET_IPS"
NODE_COUNT="${#IPS[@]}"
[ "$NODE_COUNT" -ge 1 ] || die "TESTNET_IPS parsed to zero entries"

if [ -n "${MONIKERS:-}" ]; then
    IFS=',' read -r -a MONIKER_LIST <<< "$MONIKERS"
    [ "${#MONIKER_LIST[@]}" -eq "$NODE_COUNT" ] || die "MONIKERS has ${#MONIKER_LIST[@]} entries, TESTNET_IPS has $NODE_COUNT — they must match"
else
    MONIKER_LIST=()
    for ((i = 0; i < NODE_COUNT; i++)); do
        MONIKER_LIST+=("testnet-node-$i")
    done
fi

# --- resolve the noded binary ----------------------------------------------
if [ -n "${NODED_BIN:-}" ]; then
    NODED="$NODED_BIN"
elif [ -x "$REPO_ROOT/build/noded" ]; then
    NODED="$REPO_ROOT/build/noded"
elif command -v noded >/dev/null 2>&1; then
    NODED="$(command -v noded)"
else
    NODED="$REPO_ROOT/build/noded"
fi

noded() { "$NODED" "$@"; }

if [ "$BUILD" = "1" ] || [ ! -x "$NODED" ]; then
    log "building noded (make build)…"
    make -C "$REPO_ROOT" build
    NODED="$REPO_ROOT/build/noded"
fi
[ -x "$NODED" ] || die "noded binary not found/executable at: $NODED"
log "using binary: $NODED"

[ -f "$GENESIS_SEED_FILE" ] || die "genesis seed not found at $GENESIS_SEED_FILE"

if command -v python3 >/dev/null 2>&1; then
    PYTHON=python3
elif command -v python >/dev/null 2>&1; then
    PYTHON=python
else
    die "python3 or python is required"
fi

if [ -z "$DENOM" ]; then
    DENOM="$("$PYTHON" - "$GENESIS_SEED_FILE" <<'PY'
import json, pathlib, sys
seed = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
denom = seed.get("hub_params", {}).get("phase0", {}).get("business_denom", "")
if not isinstance(denom, str) or not denom or denom != denom.strip():
    raise SystemExit("hub_params.phase0.business_denom must be a non-empty canonical string")
print(denom)
PY
)"
fi
# Per-node app.toml setting, not genesis state: the seed carries it so one file
# describes the whole network, and every node must apply it or the event stream
# is on for some nodes and off for others.
read -r TASK_EVENT_GRPC_ENABLED PROTOCOL_EVENTS_ENABLED < <(
    "$PYTHON" - "$GENESIS_SEED_FILE" <<'PY'
import json, pathlib, sys
seed = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
config = seed.get("node_config", {}).get("task_event_grpc", {})
enabled = bool(config.get("enabled", False))
protocol_enabled = bool(config.get("protocol_events_enabled", False))
if protocol_enabled and not enabled:
    raise SystemExit("node_config.task_event_grpc.protocol_events_enabled requires enabled=true")
print(str(enabled).lower(), str(protocol_enabled).lower())
PY
)
GENESIS_BALANCE="${GENESIS_BALANCE:-100000000000000${DENOM},1000000000ubond}"
MIN_GAS_PRICES="${MIN_GAS_PRICES:-0${DENOM}}"

sed_inplace() {
    if sed --version >/dev/null 2>&1; then
        sed -i "$@"
    else
        sed -i '' "$@"
    fi
}

set_in_section() {
    local file="$1" section="$2" key="$3" repl="$4"
    awk -v sec="$section" -v key="$key" -v repl="$repl" '
        $0 == sec { insec = 1; print; next }
        /^\[/ && $0 != sec { insec = 0 }
        insec && $0 ~ ("^" key " = ") && !done { print repl; done = 1; next }
        { print }
    ' "$file" > "$file.tmp" && mv "$file.tmp" "$file"
}

if [ -e "$WORK_DIR" ]; then
    if [ "$RESET" = "1" ]; then
        warn "RESET=1 — removing existing $WORK_DIR"
        rm -rf "$WORK_DIR"
    else
        die "$WORK_DIR already exists. Re-run with RESET=1, or set WORK_DIR to a fresh path."
    fi
fi
mkdir -p "$WORK_DIR"

log "1/6 init $NODE_COUNT validator homes for chain '$CHAIN_ID'"
KEY_ADDRS=()
NODE_IDS=()
for ((i = 0; i < NODE_COUNT; i++)); do
    HOME_I="$WORK_DIR/node-$i"
    MONIKER_I="${MONIKER_LIST[$i]}"
    noded init "$MONIKER_I" --chain-id "$CHAIN_ID" --home "$HOME_I" >/dev/null 2>&1

    noded keys add "validator" --keyring-backend "$KEYRING" --home "$HOME_I" \
        2> "$HOME_I/validator.mnemonic.txt"
    KEY_ADDR="$(noded keys show "validator" -a --keyring-backend "$KEYRING" --home "$HOME_I")"
    KEY_ADDRS+=("$KEY_ADDR")
    NODE_ID="$(noded tendermint show-node-id --home "$HOME_I")"
    NODE_IDS+=("$NODE_ID")
    log "    node-$i moniker=$MONIKER_I ip=${IPS[$i]} node-id=$NODE_ID validator=$KEY_ADDR"
done

log "2/6 genesis accounts + gentx (each home signs its own, offline of the others)"
COLLECTOR="$WORK_DIR/node-0"
for ((i = 0; i < NODE_COUNT; i++)); do
    HOME_I="$WORK_DIR/node-$i"
    noded genesis add-genesis-account "validator" "$GENESIS_BALANCE" \
        --keyring-backend "$KEYRING" --home "$HOME_I"
done
# Each home's add-genesis-account above only recorded ITS OWN validator's
# account (every home started from its own independent `init`). gentx needs
# every validator's self-delegation source funded in the same home, so merge
# all N homes' auth accounts + bank balances/supply into one combined set,
# then write that combined set into every home before gentx runs.
"$PYTHON" - "$WORK_DIR" "$NODE_COUNT" <<'PY'
import json, pathlib, sys

work_dir, count = pathlib.Path(sys.argv[1]), int(sys.argv[2])
paths = [work_dir / f"node-{i}" / "config" / "genesis.json" for i in range(count)]
docs = [json.loads(p.read_text(encoding="utf-8")) for p in paths]

accounts, balances, supply = [], [], {}
for doc in docs:
    accounts.extend(doc["app_state"]["auth"]["accounts"])
    balances.extend(doc["app_state"]["bank"]["balances"])
    for coin in doc["app_state"]["bank"]["supply"]:
        supply[coin["denom"]] = str(int(supply.get(coin["denom"], 0)) + int(coin["amount"]))
supply_list = [{"denom": denom, "amount": amount} for denom, amount in sorted(supply.items())]
# Each home independently numbered its own lone validator account "0" before
# this merge; renumber sequentially so the combined list has no duplicates.
for index, account in enumerate(accounts):
    account["account_number"] = str(index)

for path, doc in zip(paths, docs):
    doc["app_state"]["auth"]["accounts"] = accounts
    doc["app_state"]["bank"]["balances"] = balances
    doc["app_state"]["bank"]["supply"] = supply_list
    path.write_text(json.dumps(doc, indent=2) + "\n", encoding="utf-8")
PY
for ((i = 0; i < NODE_COUNT; i++)); do
    HOME_I="$WORK_DIR/node-$i"
    noded genesis gentx "validator" "$SELF_DELEGATION" \
        --chain-id "$CHAIN_ID" --keyring-backend "$KEYRING" --home "$HOME_I" >/dev/null 2>&1
    if [ "$i" -ne 0 ]; then
        cp "$HOME_I"/config/gentx/*.json "$COLLECTOR/config/gentx/"
    fi
done

log "3/6 collect-gentxs + apply-seed (once, on node-0, then fan out)"
noded genesis collect-gentxs --home "$COLLECTOR" >/dev/null 2>&1

if [ "$GOV_FAST" = "1" ]; then
    GENESIS_JSON="$COLLECTOR/config/genesis.json"
    GOV_VOTING_PERIOD="$GOV_VOTING_PERIOD" \
    GOV_MAX_DEPOSIT_PERIOD="$GOV_MAX_DEPOSIT_PERIOD" \
    GOV_EXPEDITED_VOTING_PERIOD="$GOV_EXPEDITED_VOTING_PERIOD" \
    GOV_MIN_DEPOSIT="$GOV_MIN_DEPOSIT" \
    GOV_EXPEDITED_MIN_DEPOSIT="$GOV_EXPEDITED_MIN_DEPOSIT" \
    DENOM="$DENOM" \
    "$PYTHON" - "$GENESIS_JSON" <<'PY'
import json, os, pathlib, sys

path = pathlib.Path(sys.argv[1])
genesis = json.loads(path.read_text(encoding="utf-8"))
gov = genesis["app_state"]["gov"]["params"]
denom = os.environ["DENOM"]

def to_seconds(text):
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

noded genesis apply-seed "$GENESIS_SEED_FILE" --home "$COLLECTOR"
log "    genesis accounts, emission, Builder, Cortex, bond, model, profile, and support state applied"

noded genesis validate --home "$COLLECTOR"

log "4/6 fan the collected genesis back out to every home"
for ((i = 1; i < NODE_COUNT; i++)); do
    cp "$COLLECTOR/config/genesis.json" "$WORK_DIR/node-$i/config/genesis.json"
done

log "5/6 persistent_peers (every home points at every other node-id@ip:$P2P_PORT)"
for ((i = 0; i < NODE_COUNT; i++)); do
    HOME_I="$WORK_DIR/node-$i"
    PEERS=""
    for ((j = 0; j < NODE_COUNT; j++)); do
        [ "$i" -eq "$j" ] && continue
        PEER="${NODE_IDS[$j]}@${IPS[$j]}:$P2P_PORT"
        if [ -z "$PEERS" ]; then PEERS="$PEER"; else PEERS="$PEERS,$PEER"; fi
    done
    CONFIG_TOML="$HOME_I/config/config.toml"
    sed_inplace "s/^persistent_peers = .*/persistent_peers = \"$PEERS\"/" "$CONFIG_TOML"
done

log "6/6 per-node app/config.toml: external address, bind-all, min-gas-prices, task-event-grpc"
for ((i = 0; i < NODE_COUNT; i++)); do
    HOME_I="$WORK_DIR/node-$i"
    APP_TOML="$HOME_I/config/app.toml"
    CONFIG_TOML="$HOME_I/config/config.toml"
    CLIENT_TOML="$HOME_I/config/client.toml"

    sed_inplace "s/^chain-id = .*/chain-id = \"$CHAIN_ID\"/" "$CLIENT_TOML"
    sed_inplace "s/^keyring-backend = .*/keyring-backend = \"$KEYRING\"/" "$CLIENT_TOML"
    sed_inplace "s#^node = .*#node = \"tcp://127.0.0.1:26657\"#" "$CLIENT_TOML"

    sed_inplace "s/^minimum-gas-prices = .*/minimum-gas-prices = \"$MIN_GAS_PRICES\"/" "$APP_TOML"
    awk '
        /^\[api\]/ {inapi=1}
        inapi && /^enable = / && !done {sub(/false/,"true"); done=1}
        /^\[/ && !/^\[api\]/ {inapi=0}
        {print}
    ' "$APP_TOML" > "$APP_TOML.tmp" && mv "$APP_TOML.tmp" "$APP_TOML"

    sed_inplace "s#^external_address = .*#external_address = \"${IPS[$i]}:$P2P_PORT\"#" "$CONFIG_TOML"
    set_in_section "$APP_TOML"    "[api]"  "address" 'address = "tcp://0.0.0.0:1317"'
    set_in_section "$APP_TOML"    "[grpc]" "address" 'address = "0.0.0.0:9090"'
    set_in_section "$CONFIG_TOML" "[rpc]"  "laddr"   'laddr = "tcp://0.0.0.0:26657"'
    set_in_section "$CONFIG_TOML" "[p2p]"  "laddr"   "laddr = \"tcp://0.0.0.0:$P2P_PORT\""

    set_in_section "$APP_TOML" "[task-event-grpc]" "enabled"         "enabled = $TASK_EVENT_GRPC_ENABLED"
    set_in_section "$APP_TOML" "[task-event-grpc]" "protocol-events-enabled"         "protocol-events-enabled = $PROTOCOL_EVENTS_ENABLED"

    if [ "$FAST_BLOCKS" = "1" ]; then
        sed_inplace 's/^timeout_commit = .*/timeout_commit = "1s"/' "$CONFIG_TOML"
    fi

    rm -f "$HOME_I"/config/gentx/*.json 2>/dev/null || true
done

cat <<EOF

$(log "ready to deploy — $NODE_COUNT homes prepared under $WORK_DIR")
  chain-id : $CHAIN_ID
  denom    : $DENOM  (min-gas-prices=$MIN_GAS_PRICES)

EOF
for ((i = 0; i < NODE_COUNT; i++)); do
    cat <<EOF
  node-$i: ${MONIKER_LIST[$i]}  ip=${IPS[$i]}  validator=${KEY_ADDRS[$i]}
    1) copy this repo's noded binary + $WORK_DIR/node-$i to that host, as its node home
       (mnemonic is in node-$i/validator.mnemonic.txt — save it offline, do not ship it further)
    2) generate that host's own VRF key ON that host (never copy vrf_key.json between hosts):
       noded beacon vrf-keygen --home <remote-home>
    3) start it:
       noded start --home <remote-home> --minimum-gas-prices $MIN_GAS_PRICES --api.enable --grpc.enable

EOF
done
log "firewall/security-group: open $P2P_PORT (p2p) between all $NODE_COUNT hosts; open 26657/1317/9090 to whatever needs to query this testnet"
log "this script does not open vrf_required_from_height enforcement early — that comes from the genesis seed's hub_params.beacon.vrf_required_from_height, already baked into the collected genesis"
