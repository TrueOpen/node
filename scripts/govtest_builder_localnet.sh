#!/usr/bin/env bash
#
# govtest_builder_localnet.sh — one command to get a localnet whose builders you
# hold the private keys for (docs/runbooks/builder_registration_testing.md).
#
# The default genesis seed ships builder addresses whose private keys nobody
# has, so you cannot sign anything as them. This script:
#
#   1. generates N builder keys in a scratch keyring (so RESET cannot eat them),
#   2. writes a custom genesis seed using those addresses + their pubkeys,
#   3. boots an isolated localnet from that seed (START=0, setup only),
#   4. restores the builder keys into the freshly created keyring,
#   5. starts the node.
#
# NOTE (2026-09-17): builders carry NO bond in ADR-0018 Phase 0 — the genesis
# seed schema has no bond field for them (cmd/noded/cmd/genesis_seed.go
# genesisSeedBuilder) and the loader rejects unknown fields. The old
# BUILDER_BONDS knob and the bond-weighted governance it fed are gone; builder
# governance weight does not exist in Phase 0. See
# docs/governance_dualmode_design.md §12.
#
# Usage:
#   scripts/govtest_builder_localnet.sh              # set up and start
#   START=0 scripts/govtest_builder_localnet.sh      # set up, do not start
#
# Common overrides:
#   HOME_DIR=~/.node-govtest        # isolated home; NOT your ~/.node
#   BUILDER_COUNT=3
#   CHAIN_ID=trueopen-localnet-1
#   SEED_TEMPLATE=config/localnet_genesis_seed.json
#
# Safety: this script always uses its own HOME_DIR (default ~/.node-govtest) and
# passes RESET=1 to the localnet bootstrap, so it wipes THAT directory only. It
# never touches ~/.node unless you point HOME_DIR at it.

if [ -z "${BASH_VERSION:-}" ]; then exec bash "$0" "$@"; fi
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

HOME_DIR="${HOME_DIR:-$HOME/.node-govtest}"
BUILDER_COUNT="${BUILDER_COUNT:-3}"
CHAIN_ID="${CHAIN_ID:-trueopen-localnet-1}"
KEYRING="${KEYRING:-test}"
SEED_TEMPLATE="${SEED_TEMPLATE:-$REPO_ROOT/config/localnet_genesis_seed.json}"
START="${START:-1}"

log()  { printf '\033[1;34m[govtest]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[govtest]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[govtest]\033[0m %s\n' "$*" >&2; exit 1; }

command -v python3 >/dev/null 2>&1 || die "python3 is required"
[ -f "$SEED_TEMPLATE" ] || die "seed template not found: $SEED_TEMPLATE"

# --- resolve the noded binary (same precedence as localnet_single_node.sh) ---
if [ -n "${NODED_BIN:-}" ]; then NODED="$NODED_BIN"
elif [ -x "$REPO_ROOT/build/noded" ]; then NODED="$REPO_ROOT/build/noded"
elif command -v noded >/dev/null 2>&1; then NODED="$(command -v noded)"
else NODED="$REPO_ROOT/build/noded"; fi
if [ ! -x "$NODED" ]; then
    log "building noded (make build)…"
    make -C "$REPO_ROOT" build
    NODED="$REPO_ROOT/build/noded"
fi
[ -x "$NODED" ] || die "noded binary not found at: $NODED"

if [ -n "${BUILDER_BONDS:-}" ]; then
    warn "BUILDER_BONDS is set but ignored: Phase 0 builders carry no bond (see the header)."
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
SCRATCH_KEYS="$WORK/keyring-home"
mkdir -p "$SCRATCH_KEYS"

# ---------------------------------------------------------------------------
log "1/5 generating $BUILDER_COUNT builder keys (scratch keyring)"
ADDRS=(); PUBS=()
for i in $(seq 1 "$BUILDER_COUNT"); do
    name="builder$i"
    # --output json carries address, pubkey AND mnemonic in one document. The
    # human-readable form prints the mnemonic as the last stderr line, which is
    # exactly the kind of thing that silently breaks; do not go back to it.
    "$NODED" keys add "$name" --keyring-backend "$KEYRING" --home "$SCRATCH_KEYS" \
        --output json >"$WORK/$name.json" 2>/dev/null
    read -r addr pub mnemonic_file <<<"$(
        NAME="$name" WORK="$WORK" python3 - "$WORK/$name.json" <<'PYK'
import base64, json, os, pathlib, sys

doc = json.load(open(sys.argv[1], encoding="utf-8"))
key = json.loads(doc["pubkey"])["key"]
pub = base64.b64decode(key).hex()
mnemonic = doc.get("mnemonic", "").strip()
if not mnemonic:
    raise SystemExit(f"{os.environ['NAME']}: keys add returned no mnemonic")
out = pathlib.Path(os.environ["WORK"]) / f"{os.environ['NAME']}.mnemonic.txt"
out.write_text(mnemonic + "\n", encoding="utf-8")
print(doc["address"], pub, out)
PYK
    )"
    [ -n "$addr" ] || die "$name: could not read the generated key"
    [ "${#pub}" -eq 66 ] || die "$name: unexpected pubkey length ${#pub} (want 66 hex chars)"
    ADDRS+=("$addr"); PUBS+=("$pub")
    log "    $name  ${addr}"
done

# ---------------------------------------------------------------------------
log "2/5 writing custom genesis seed"
SEED_OUT="$WORK/govtest_seed.json"
ADDRS_CSV="$(IFS=,; echo "${ADDRS[*]}")"
PUBS_CSV="$(IFS=,; echo "${PUBS[*]}")"

SEED_TEMPLATE="$SEED_TEMPLATE" SEED_OUT="$SEED_OUT" \
ADDRS_CSV="$ADDRS_CSV" PUBS_CSV="$PUBS_CSV" \
python3 - <<'PY'
import json, os

seed = json.load(open(os.environ["SEED_TEMPLATE"], encoding="utf-8"))
addrs = os.environ["ADDRS_CSV"].split(",")
pubs = os.environ["PUBS_CSV"].split(",")

if not seed.get("builders"):
    raise SystemExit("seed template has no builders entry to copy the shape from")
template = seed["builders"][0]

# The seed loader runs DisallowUnknownFields (cmd/noded/cmd/genesis_seed.go),
# so the entry must carry exactly the template's keys — only the identity
# fields are replaced. Do not add a bond field: builders have none in Phase 0.
builders = []
for addr, pub in zip(addrs, pubs):
    b = dict(template)
    b["address"] = addr
    b["service_address"] = addr
    b["service_pubkey"] = pub
    builders.append(b)

seed["builders"] = builders
json.dump(seed, open(os.environ["SEED_OUT"], "w", encoding="utf-8"), indent=2)
print(f"seed written with {len(builders)} builders")
PY

# ---------------------------------------------------------------------------
log "3/5 bootstrapping localnet at $HOME_DIR (RESET=1, seed=custom)"
HOME_DIR="$HOME_DIR" GENESIS_SEED_FILE="$SEED_OUT" CHAIN_ID="$CHAIN_ID" KEYRING="$KEYRING" \
RESET=1 START=0 GOV_FAST="${GOV_FAST:-1}" FAST_BLOCKS="${FAST_BLOCKS:-1}" \
    "$SCRIPT_DIR/localnet_single_node.sh"

log "4/5 restoring builder keys into the fresh keyring"
for i in $(seq 1 "$BUILDER_COUNT"); do
    name="builder$i"
    mnemonic="$(tr -d '\r' < "$WORK/$name.mnemonic.txt" | sed -e '/^$/d' | tail -n 1)"
    [ -n "$mnemonic" ] || die "$name: could not read the generated mnemonic"
    printf '%s\n' "$mnemonic" | "$NODED" keys add "$name" --recover \
        --keyring-backend "$KEYRING" --home "$HOME_DIR" >/dev/null
    got="$("$NODED" keys show "$name" -a --keyring-backend "$KEYRING" --home "$HOME_DIR")"
    [ "$got" = "${ADDRS[$((i-1))]}" ] ||
        die "$name recovered to $got but genesis seeded ${ADDRS[$((i-1))]}"
done
log "    all $BUILDER_COUNT builder keys restored and address-matched"

# ---------------------------------------------------------------------------
cat <<EOF

$(log "5/5 ready")
  home       : $HOME_DIR
  chain-id   : $CHAIN_ID
  builders   :
$(for i in $(seq 1 "$BUILDER_COUNT"); do
    printf '    builder%s  %s\n' "$i" "${ADDRS[$((i-1))]}"
  done)

  These builders are admitted from genesis and are members of the genesis
  BuilderSet, so they can serve tasks immediately. They carry NO bond and NO
  governance voting weight — in Phase 0 builder-domain proposals are decided by
  the validator electorate (docs/governance_dualmode_design.md §12).

  Verify:
    $NODED --home $HOME_DIR query hub builders -o json | jq '.builders[] | {builder_address, status}'

  Then follow docs/runbooks/builder_registration_testing.md.

EOF

if [ "$START" = "1" ]; then
    log "starting node (Ctrl-C to stop)…"
    exec "$NODED" start --home "$HOME_DIR" --minimum-gas-prices "0uusdc" \
        --api.enable --grpc.enable
else
    log "START=0 — start it yourself with:"
    log "  $NODED start --home $HOME_DIR --minimum-gas-prices 0uusdc --api.enable --grpc.enable"
fi
