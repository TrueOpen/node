#!/usr/bin/env bash
#
# gov_update_hub_param.sh — one-click governance change of a single x/hub
# parameter on a localnet, end to end:
#
#   query current params -> build MsgUpdateHubParams proposal (full params with
#   one field edited) -> submit -> (deposit if needed) -> vote yes -> wait for
#   the voting period to end -> print BEFORE vs AFTER.
#
# Meant for localnet/testnet where a single validator holds ~100% voting power
# (so quorum/threshold pass on a `yes`). Do NOT use on a real network.
#
# Usage:
#   scripts/gov_update_hub_param.sh <param_path> <new_value>
#   e.g. scripts/gov_update_hub_param.sh builder.builder_set_cap 20
#
# HubParams is grouped (builder / reward / service / freeze / treasury / ...),
# so <param_path> is a dotted path into that structure, not a flat key.
# Note some params are pinned by validation (builder.builders_per_task must be 3);
# a proposal changing one of those passes the vote and then fails execution with
# PROPOSAL_STATUS_FAILED.
#
# The value is written as a JSON string by default (HubParams ints are
# string-encoded); pass true/false and it is written as a JSON boolean.
# For anything more complex, set JQ_FILTER to a jq expression instead, e.g.
#   JQ_FILTER='.builder.builder_set_cap="20" | .builder.builder_bond="2000000"' \
#       scripts/gov_update_hub_param.sh
#
# Common overrides (env vars):
#   KEY_NAME=validator        # voting/proposing account (must have stake+funds)
#   CHAIN_ID=trueopen-localnet-1
#   KEYRING=test
#   HOME_DIR=$HOME/.node
#   NODE=tcp://127.0.0.1:26657
#   DENOM=uusdc               # gas/fee denom; auto-derived from gov params
#   DEPOSIT=10000000uusdc     # initial deposit (>= min_deposit); auto-derived
#   GAS=900000                # fixed gas; --gas auto does not work on Phase 0
#   VOTE=yes                  # yes|no|no_with_veto|abstain

if [ -z "${BASH_VERSION:-}" ]; then exec bash "$0" "$@"; fi
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

KEY_NAME="${KEY_NAME:-validator}"
CHAIN_ID="${CHAIN_ID:-trueopen-localnet-1}"
KEYRING="${KEYRING:-test}"
HOME_DIR="${HOME_DIR:-$HOME/.node}"
NODE="${NODE:-tcp://127.0.0.1:26657}"
# Left empty so they can be derived from the chain's own gov params below; the
# deposit denom is whatever min_deposit says (SDK validateDepositDenom), and
# hardcoding it is exactly how this script broke when the chain moved to uusdc.
DENOM="${DENOM:-}"
DEPOSIT="${DEPOSIT:-}"
VOTE="${VOTE:-yes}"
GOV_AUTH="trueopen10d07y265gmmuvt4z0w9aw880jnsr700jc0zupp"

# --- resolve noded ---------------------------------------------------------
if [ -n "${NODED_BIN:-}" ]; then NODED="$NODED_BIN"
elif [ -x "$REPO_ROOT/build/noded" ]; then NODED="$REPO_ROOT/build/noded"
elif command -v noded >/dev/null 2>&1; then NODED="$(command -v noded)"
else NODED="$REPO_ROOT/build/noded"; fi
[ -x "$NODED" ] || { echo "noded not found at $NODED" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 1; }

log()  { printf '\033[1;34m[gov]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[gov]\033[0m %s\n' "$*" >&2; exit 1; }

Q=(--node "$NODE" -o json)

# --- derive the deposit denom/amount from the chain -------------------------
# min_deposit is the authoritative source: x/gov's validateDepositDenom accepts
# exactly the denoms listed there.
GOV_PARAMS="$($NODED query gov params "${Q[@]}" --home "$HOME_DIR" 2>/dev/null || true)"
CHAIN_DENOM="$(printf '%s' "$GOV_PARAMS" | jq -r '.params.min_deposit[0].denom // empty' 2>/dev/null || true)"
CHAIN_MIN="$(printf '%s' "$GOV_PARAMS" | jq -r '.params.min_deposit[0].amount // empty' 2>/dev/null || true)"
[ -n "$DENOM" ] || DENOM="${CHAIN_DENOM:-uusdc}"
if [ -z "$DEPOSIT" ]; then
    [ -n "$CHAIN_MIN" ] || die "could not read gov min_deposit from $NODE; set DEPOSIT explicitly"
    DEPOSIT="${CHAIN_MIN}${CHAIN_DENOM}"
fi

# Fixed gas, not --gas auto. Simulation signs with an unset sign mode, which the
# Phase 0 account ante rejects ("signer 0 uses an invalid signature mode",
# app/account_ante.go), so every --gas auto transaction fails on this chain.
# Raise GAS if a proposal body grows past it.
GAS="${GAS:-900000}"
TX=(--from "$KEY_NAME" --chain-id "$CHAIN_ID" --keyring-backend "$KEYRING" \
    --home "$HOME_DIR" --node "$NODE" --gas "$GAS" \
    --fees "0$DENOM" -y -o json)

# --- build the jq edit -----------------------------------------------------
if [ -n "${JQ_FILTER:-}" ]; then
    EDIT="$JQ_FILTER"
    DESC="$JQ_FILTER"
else
    [ "$#" -eq 2 ] || die "usage: $0 <param_path> <new_value>   (e.g. builder.builder_set_cap 20)"
    KEYNAME="$1"; VAL="$2"
    if [ "$VAL" = "true" ] || [ "$VAL" = "false" ]; then
        EDIT=".${KEYNAME}=${VAL}"          # JSON boolean
    else
        EDIT=".${KEYNAME}=\"${VAL}\""      # JSON string (HubParams int encoding)
    fi
    DESC="${KEYNAME} -> ${VAL}"
fi

# --- 1. BEFORE -------------------------------------------------------------
log "querying current hub params …"
PARAMS_RESPONSE="$($NODED query hub params "${Q[@]}")"
BEFORE="$(echo "$PARAMS_RESPONSE" | jq '.params')"
[ -n "$BEFORE" ] && [ "$BEFORE" != "null" ] || die "could not read hub params (is the node running at $NODE?)"

# expected_version is optimistic concurrency against params_version: the Msg is
# rejected unless it names the version the chain currently holds. The query is
# the only place that value is published, so it is read here rather than
# guessed. An absent meta means the params have never been updated, which is
# version 0.
EXPECTED_VERSION="$(echo "$PARAMS_RESPONSE" | jq -r '.meta.params_version // "0"')"
[ -n "$EXPECTED_VERSION" ] || die "could not read meta.params_version from the params query"
log "current params_version: $EXPECTED_VERSION"
echo "----------------------------------------------------------------------"
echo "BEFORE:"; echo "$BEFORE" | jq .
echo "----------------------------------------------------------------------"

NEW_PARAMS="$(echo "$BEFORE" | jq "$EDIT")" || die "jq edit failed: $EDIT"
if [ "$(echo "$BEFORE" | jq -S .)" = "$(echo "$NEW_PARAMS" | jq -S .)" ]; then
    die "edit produced no change ($DESC). Check the key/value."
fi

# --- 2. assemble proposal --------------------------------------------------
PROP_FILE="$(mktemp)"
jq -n --argjson params "$NEW_PARAMS" --arg auth "$GOV_AUTH" --arg dep "$DEPOSIT" --arg d "$DESC" \
      --arg ver "$EXPECTED_VERSION" '
{ messages: [ { "@type": "/hub.v1.MsgUpdateHubParams", authority: $auth,
                expected_version: $ver, params: $params } ],
  deposit: $dep, title: ("Update hub params: " + $d), summary: ("Governance change: " + $d) }' > "$PROP_FILE"
log "proposal ($DESC):"; jq -c '.messages[0]["@type"]' "$PROP_FILE" >/dev/null

# --- 3. submit -------------------------------------------------------------
log "submitting proposal …"
SUBMIT="$($NODED tx gov submit-proposal "$PROP_FILE" "${TX[@]}")"
TXHASH="$(echo "$SUBMIT" | jq -r '.txhash')"
[ -n "$TXHASH" ] && [ "$TXHASH" != "null" ] || die "submit failed: $SUBMIT"

# wait for tx commit
RES=""
for _ in $(seq 1 40); do
    if RES="$($NODED query tx "$TXHASH" "${Q[@]}" 2>/dev/null)"; then break; fi
    sleep 2
done
[ -n "$RES" ] || die "submit tx $TXHASH not committed in time"
CODE="$(echo "$RES" | jq -r '.code')"
[ "$CODE" = "0" ] || die "submit tx failed (code=$CODE): $(echo "$RES" | jq -r '.raw_log')"

# proposal id from events (fallback to latest proposal)
PROP="$(echo "$RES" | jq -r '[.events[]?|select(.type=="submit_proposal")|.attributes[]?|select(.key=="proposal_id")|.value]|first // empty')"
[ -n "$PROP" ] || PROP="$($NODED query gov proposals "${Q[@]}" | jq -r '.proposals[-1].id')"
[ -n "$PROP" ] && [ "$PROP" != "null" ] || die "could not determine proposal id"
log "proposal id = $PROP"

# --- 4. deposit if still in deposit period ---------------------------------
STATUS="$($NODED query gov proposal "$PROP" "${Q[@]}" | jq -r '.proposal.status // .status')"
if [ "$STATUS" = "PROPOSAL_STATUS_DEPOSIT_PERIOD" ] || [ "$STATUS" = "2" ]; then
    log "topping up deposit …"
    $NODED tx gov deposit "$PROP" "$DEPOSIT" "${TX[@]}" >/dev/null; sleep 3
fi

# --- 5. vote ---------------------------------------------------------------
log "voting '$VOTE' on proposal $PROP …"
$NODED tx gov vote "$PROP" "$VOTE" "${TX[@]}" >/dev/null

# --- 6. wait for terminal status -------------------------------------------
VP="$($NODED query gov params "${Q[@]}" | jq -r '.params.voting_period')"
# voting_period may be a Go duration ("5m0s", "1h2m3s") or plain seconds
# ("300s", "300.000000000s"). Parse all of these to whole seconds.
dur_to_secs() {
    local d="$1" h=0 m=0 s=0
    [[ "$d" =~ ([0-9]+)h ]] && h="${BASH_REMATCH[1]}"
    [[ "$d" =~ ([0-9]+)m ]] && m="${BASH_REMATCH[1]}"
    [[ "$d" =~ ([0-9]+)(\.[0-9]+)?s ]] && s="${BASH_REMATCH[1]}"
    echo $(( h * 3600 + m * 60 + s ))
}
VP_SECS="$(dur_to_secs "$VP")"; [ "${VP_SECS:-0}" -gt 0 ] 2>/dev/null || VP_SECS=300
DEADLINE=$(( VP_SECS + 90 ))
log "waiting up to ${DEADLINE}s for voting period ($VP) to close …"
FINAL=""
for _ in $(seq 1 $(( DEADLINE / 5 + 1 )) ); do
    S="$($NODED query gov proposal "$PROP" "${Q[@]}" | jq -r '.proposal.status // .status')"
    case "$S" in
        PROPOSAL_STATUS_PASSED|3)   FINAL="PASSED";   break ;;
        PROPOSAL_STATUS_REJECTED|4) FINAL="REJECTED"; break ;;
        PROPOSAL_STATUS_FAILED|5)   FINAL="FAILED";   break ;;
    esac
    sleep 5
done
[ -n "$FINAL" ] || die "proposal $PROP did not reach a terminal status in time (still $S)"
log "proposal $PROP final status: $FINAL"

# --- 7. AFTER + diff -------------------------------------------------------
AFTER="$($NODED query hub params "${Q[@]}" | jq '.params')"
echo "----------------------------------------------------------------------"
echo "AFTER:"; echo "$AFTER" | jq .
echo "----------------------------------------------------------------------"
echo "CHANGED KEYS:"
diff <(echo "$BEFORE" | jq -S .) <(echo "$AFTER" | jq -S .) || true
echo "----------------------------------------------------------------------"
rm -f "$PROP_FILE"

if [ "$FINAL" = "PASSED" ]; then
    log "done — proposal passed and params updated."
else
    die "proposal ended $FINAL — params unchanged. Check tally: $NODED query gov tally $PROP"
fi
