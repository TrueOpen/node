#!/usr/bin/env bash
#
# localnet_send.sh — send tokens between accounts on a TrueOpen localnet.
#
# Wraps `noded tx bank send` with sensible localnet defaults, address/key
# resolution, before/after balance display, and DeliverTx result checking so
# a silent failed tx (nonzero code) does not look like success.
#
# Usage:
#   scripts/localnet_send.sh FROM TO AMOUNT
#
#   FROM    payer: a key name in the keyring, or a trueopen1... address that the
#           keyring can sign for.
#   TO      recipient: a key name, or a trueopen1... address. A bare denom is not
#           required on AMOUNT — it defaults to $DENOM.
#   AMOUNT  e.g. "1000000uusdc" or just "1000000" (denom appended).
#
# Examples:
#   scripts/localnet_send.sh validator alice 1000000uusdc
#   scripts/localnet_send.sh validator trueopen1abc...xyz 5000000
#   CREATE_TO=1 scripts/localnet_send.sh validator bob 1000000   # make key bob first
#
# Env overrides (all optional):
#   HOME_DIR=$HOME/.node   KEYRING=test   CHAIN_ID=trueopen-localnet-1
#   DENOM=uusdc            FEES=0uusdc    NODE=tcp://127.0.0.1:26657
#   GAS=                   # e.g. GAS="--gas auto --gas-adjustment 1.3"
#   CREATE_TO=0            # 1 = create TO as a new key if it does not exist
#   WAIT=3                 # seconds to wait for the tx to be included
#
# Guard: re-exec under bash if invoked as `sh script.sh` on dash.
if [ -z "${BASH_VERSION:-}" ]; then exec bash "$0" "$@"; fi
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

HOME_DIR="${HOME_DIR:-$HOME/.node}"
KEYRING="${KEYRING:-test}"
CHAIN_ID="${CHAIN_ID:-trueopen-localnet-1}"
DENOM="${DENOM:-uusdc}"
FEES="${FEES:-0uusdc}"
NODE="${NODE:-tcp://127.0.0.1:26657}"
GAS="${GAS:-}"
CREATE_TO="${CREATE_TO:-0}"
WAIT="${WAIT:-3}"

log()  { printf '\033[1;34m[send]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[send]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[send]\033[0m %s\n' "$*" >&2; exit 1; }

# --- args ------------------------------------------------------------------
[ "$#" -eq 3 ] || die "usage: $(basename "$0") FROM TO AMOUNT   (e.g. validator alice 1000000uusdc)"
FROM_REF="$1"; TO_REF="$2"; AMOUNT="$3"

# Append default denom if AMOUNT is bare digits.
if [[ "$AMOUNT" =~ ^[0-9]+$ ]]; then
    AMOUNT="${AMOUNT}${DENOM}"
fi

# --- resolve the noded binary (same order as localnet_single_node.sh) -------
if [ -n "${NODED_BIN:-}" ]; then NODED="$NODED_BIN"
elif [ -x "$REPO_ROOT/build/noded" ]; then NODED="$REPO_ROOT/build/noded"
elif command -v noded >/dev/null 2>&1; then NODED="$(command -v noded)"
else die "noded binary not found (build with 'make build' or set NODED_BIN)"; fi

KFLAGS=(--keyring-backend "$KEYRING" --home "$HOME_DIR")

# --- address/key resolution -------------------------------------------------
# Return an address for a name-or-address reference. Creates the key if
# CREATE_TO=1 and it is a bare name that does not exist (recipient only).
resolve_addr() {
    local ref="$1" allow_create="${2:-0}" addr
    if addr="$("$NODED" keys show "$ref" -a "${KFLAGS[@]}" 2>/dev/null)"; then
        printf '%s' "$addr"; return 0
    fi
    if [[ "$ref" == trueopen1* ]]; then
        printf '%s' "$ref"; return 0        # raw bech32 address
    fi
    if [ "$allow_create" = "1" ]; then
        warn "key '$ref' not found — creating it (CREATE_TO=1)"
        "$NODED" keys add "$ref" "${KFLAGS[@]}" >&2
        "$NODED" keys show "$ref" -a "${KFLAGS[@]}"
        return 0
    fi
    die "cannot resolve '$ref': not a key in keyring '$KEYRING' and not a trueopen1 address (set CREATE_TO=1 to create it)"
}

FROM_ADDR="$(resolve_addr "$FROM_REF" 0)"
TO_ADDR="$(resolve_addr "$TO_REF" "$CREATE_TO")"

log "from  : $FROM_REF ($FROM_ADDR)"
log "to    : $TO_REF ($TO_ADDR)"
log "amount: $AMOUNT   fees: $FEES   node: $NODE"

# --- balances helper --------------------------------------------------------
show_balance() {
    local who="$1" addr="$2"
    printf '  %-8s %s\n' "$who" "$("$NODED" query bank balances "$addr" \
        --node "$NODE" -o json 2>/dev/null | tr -d '\n ' || echo '<query failed>')"
}

log "balances before:"
show_balance "from" "$FROM_ADDR"
show_balance "to"   "$TO_ADDR"

# --- send -------------------------------------------------------------------
log "broadcasting…"
# shellcheck disable=SC2206  # GAS is intentionally word-split into flags
TX_OUT="$("$NODED" tx bank send "$FROM_ADDR" "$TO_ADDR" "$AMOUNT" \
    "${KFLAGS[@]}" \
    --chain-id "$CHAIN_ID" --node "$NODE" \
    --fees "$FEES" ${GAS} \
    --broadcast-mode sync -y -o json)"

# Extract txhash + CheckTx code without hard-depending on jq.
extract() { printf '%s' "$1" | sed -n "s/.*\"$2\":[[:space:]]*\"\{0,1\}\([^\",}]*\).*/\1/p" | head -1; }
TXHASH="$(extract "$TX_OUT" txhash)"
CHECK_CODE="$(extract "$TX_OUT" code)"
[ -n "$TXHASH" ] || die "no txhash in broadcast response: $TX_OUT"
if [ -n "$CHECK_CODE" ] && [ "$CHECK_CODE" != "0" ]; then
    warn "CheckTx rejected (code=$CHECK_CODE):"
    printf '%s\n' "$TX_OUT" >&2
    exit 1
fi
log "txhash: $TXHASH — waiting ${WAIT}s for inclusion…"
sleep "$WAIT"

# --- verify DeliverTx result ------------------------------------------------
TX_RES="$("$NODED" query tx "$TXHASH" --node "$NODE" -o json 2>/dev/null || true)"
RES_CODE="$(extract "$TX_RES" code)"
if [ -z "$TX_RES" ]; then
    warn "tx not found yet (may still be pending). Check later: $NODED query tx $TXHASH --node $NODE"
elif [ -n "$RES_CODE" ] && [ "$RES_CODE" != "0" ]; then
    warn "tx FAILED on-chain (code=$RES_CODE). Raw log:"
    extract "$TX_RES" raw_log >&2 || true
    exit 1
else
    log "tx succeeded (code=0)."
fi

log "balances after:"
show_balance "from" "$FROM_ADDR"
show_balance "to"   "$TO_ADDR"
