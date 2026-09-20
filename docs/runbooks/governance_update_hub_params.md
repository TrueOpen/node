# Changing hub params by governance · end-to-end runbook

> ⚠️ **Do not use `--gas auto`**. Simulated transactions are signed with no sign
> mode set, and the Phase 0 account ante rejects them (`signer 0 uses an invalid
> signature mode`, `app/account_ante.go`). Give every transaction a fixed
> `--gas`, for example `--gas 900000`.

> Location: `docs/runbooks/governance_update_hub_params.md`
> Purpose: show how to change the `x/hub` module parameters (`HubParams`) through
> on-chain governance (`x/gov`) — proposal construction, voting, and verifying the
> change took effect, with commands you can paste.
> Prerequisite: this chain has `x/gov` wired (see `feat(app): wire x/gov for
> on-chain governance`).

---

## 0. How it works

- Changing `hub` params goes through **`MsgUpdateHubParams`**, whose `authority`
  field must equal the **governance module address**. An ordinary account cannot
  send this message directly — only gov can, as the module address, **after a
  proposal passes**.
- The governance module address (`trueopen` prefix on this chain, a fixed value):

  ```
  trueopen10d07y265gmmuvt4z0w9aw880jnsr700jc0zupp
  ```
  (= `authtypes.NewModuleAddress("gov")`, constant on-chain; cross-check with
  `noded query auth module-accounts`.)

- **Message type**: `/hub.v1.MsgUpdateHubParams`, fields:
  - `authority` (string) — must be the gov address above.
  - `expected_version` (uint64, rendered as a string in proto3 JSON) — optimistic
    concurrency. The message is rejected with `ErrHubParamsVersionMismatch`
    unless it names the params version the chain currently holds. Read it from
    `meta.params_version` on the params query (§2.1); an absent `meta` means the
    params have never been updated, which is version 0.
  - `params` (`HubParams`) — **every field must be supplied** (proto comment:
    *All parameters must be supplied*). This is whole-object replacement, not a
    patch. **So always read the current params first, change one or two fields,
    and leave the rest as they are.**

- gov defaults (genesis starting values): `min_deposit = 10000000 uusdc` (the
  denom is forced to the business denom by `genesis apply-seed`),
  `voting_period = 172800s` (48h).

---

## 1. Preflight

```bash
# the node is up, and gov and hub params are queryable
noded status | jq -r '.sync_info.latest_block_height'
noded query gov params -o json | jq '{min_deposit:.params.min_deposit, voting_period:.params.voting_period, quorum:.params.quorum, threshold:.params.threshold}'
noded query hub params -o json | jq '.params'

# the proposer account (submits the proposal and the deposit); balance must be >= min_deposit
FROM=validator
noded query bank balances "$(noded keys show $FROM -a --keyring-backend test)" -o json | jq
```

> Everything below assumes localnet: `--chain-id trueopen-localnet-1
> --keyring-backend test`, minimum gas price `0uusdc`. Substitute for your
> environment.

---

## 2. Build the proposal file (the point: edit the *current, complete* params)

**Step 2.1 — export the current params as a template, and read the version**

```bash
noded query hub params -o json > /tmp/hubparams.resp.json
jq '.params' /tmp/hubparams.resp.json > /tmp/hubparams.json
cat /tmp/hubparams.json

# expected_version comes from the same response. This query is the only place
# the current version is published.
EXPECTED_VERSION=$(jq -r '.meta.params_version // "0"' /tmp/hubparams.resp.json)
echo "current params_version: $EXPECTED_VERSION"
```

**Step 2.2 — change only the field you mean to change** (example:
`builders_per_task` 3 → 5), leaving the rest untouched:

```bash
jq '.builders_per_task = "5"' /tmp/hubparams.json > /tmp/hubparams.new.json
diff <(jq -S . /tmp/hubparams.json) <(jq -S . /tmp/hubparams.new.json)   # confirm only that one line differs
```

**Step 2.3 — assemble the gov v1 proposal JSON**, inlining the edited params:

```bash
GOV_AUTH=trueopen10d07y265gmmuvt4z0w9aw880jnsr700jc0zupp
jq -n --slurpfile p /tmp/hubparams.new.json --arg auth "$GOV_AUTH" \
      --arg ver "$EXPECTED_VERSION" '
{
  messages: [
    {
      "@type": "/hub.v1.MsgUpdateHubParams",
      authority: $auth,
      expected_version: $ver,
      params: $p[0]
    }
  ],
  metadata: "",
  deposit: "10000000uusdc",
  title: "Update hub params: builders_per_task 3 -> 5",
  summary: "Raise builders_per_task from 3 to 5 to widen per-task builder assignment."
}' > /tmp/proposal.json
cat /tmp/proposal.json
```

> Paying the full `min_deposit` (10000000uusdc) up front moves the proposal
> straight into the voting period; paying less leaves it in the deposit period
> until you top it up with `tx gov deposit`.

> `scripts/gov_update_hub_param.sh` does all of §2 and §3 for you, including
> reading `meta.params_version`. Use it unless you need the manual steps.

---

## 3. Submit the proposal

```bash
noded tx gov submit-proposal /tmp/proposal.json \
  --from $FROM --chain-id trueopen-localnet-1 --keyring-backend test \
  --gas 900000 --fees 0uusdc -y

# get the proposal id
noded query gov proposals -o json | jq -r '.proposals[-1].id'
PROP=$(noded query gov proposals -o json | jq -r '.proposals[-1].id')
noded query gov proposal "$PROP" -o json | jq '{id:.id, status:.status, messages:.messages}'
```

`status` should be `PROPOSAL_STATUS_VOTING_PERIOD` (the deposit is covered). If
it is `DEPOSIT_PERIOD`, top the deposit up:

```bash
noded tx gov deposit "$PROP" 10000000uusdc --from $FROM \
  --chain-id trueopen-localnet-1 --keyring-backend test --fees 0uusdc -y
```

---

## 4. Vote

```bash
noded tx gov vote "$PROP" yes --from $FROM \
  --chain-id trueopen-localnet-1 --keyring-backend test --fees 0uusdc -y

# options: yes / no / no_with_veto / abstain
noded query gov tally "$PROP" -o json | jq
```

> Single-validator localnet: that validator holds ~100% of the voting power, so
> a `yes` satisfies both quorum (33.4% by default) and threshold (50% by
> default).

---

## 5. Wait for the voting period to close → automatic execution

When the voting period ends (48h by default), gov tallies automatically in
**EndBlock** (`gov → staking → hub → task`, gov first); if it passed, gov
executes `MsgUpdateHubParams` as the gov address.

```bash
# poll until it reaches a terminal state
watch -n 5 'noded query gov proposal '"$PROP"' -o json | jq -r .status'
# expected: PROPOSAL_STATUS_PASSED (passed) -> executed
```

What the states mean: `PASSED` = passed and executed successfully; `REJECTED` =
did not get the votes; `FAILED` = passed but execution errored (wrong
`authority`, a stale `expected_version`, or invalid `params`).

---

## 6. Verify the change took effect

```bash
noded query hub params -o json | jq '.params.builders_per_task'   # expect "5"

# params_version has incremented; the next proposal must name the new value
noded query hub params -o json | jq -r '.meta.params_version'
```

---

## 7. Quick local testing (shorter voting period)

48h is too long for localnet. `scripts/localnet_single_node.sh` takes
`GOV_FAST=1`, which shrinks the gov periods and the min deposit in genesis:

```bash
GOV_FAST=1 RESET=1 scripts/localnet_single_node.sh
# defaults: voting_period=300s, max_deposit_period=300s, min_deposit=1000000uusdc
# override individually, e.g. GOV_FAST=1 GOV_VOTING_PERIOD=60s ...
```

> `GOV_FAST` only touches periods and deposit amounts. It deliberately leaves
> `burn_vote_veto` and `proposal_cancel_dest` alone — those are the deposit
> policy owned by `noded genesis apply-seed`, and the "burn" is what
> `GovernedGovBankKeeper` intercepts and routes to hub_treasury.

---

## 8. Common pitfalls

| Symptom | Cause / fix |
|---|---|
| Proposal `FAILED` (passed but nothing changed) | `authority` is not the gov address (must be `trueopen10d07y265gmmuvt4z0w9aw880jnsr700jc0zupp`), or `params` is invalid / missing fields. **`params` must carry every field.** |
| Proposal `FAILED` with `ErrHubParamsVersionMismatch` | `expected_version` is stale — another update landed while this proposal was in flight. Re-read `meta.params_version` and resubmit. |
| `unauthorized` / validation failure on submit | Same as above, or the `@type` is wrong (it must be `/hub.v1.MsgUpdateHubParams`). |
| Stuck in `DEPOSIT_PERIOD` | The deposit has not reached `min_deposit`; top it up with `tx gov deposit`. |
| `REJECTED` | Quorum or threshold not met; on localnet, remember to vote `yes` from an account that holds voting power. |
| Deposit denom error | The deposit must use the business denom **`uusdc`**, not the staking denom `ubond`. The denom is derived from `min_deposit`; see `noded query gov params`. |

---

## 9. Related operations (same flow, different message)

- **Change task params**: swap the message for `/task.v1.MsgUpdateParams` (same
  rules — `authority` = the gov address, `params` complete).
- **List/delist a model**: swap the message for `/hub.v1.MsgSetModelStatus` or
  `MsgSetProfileStatus` (both gov-gated); fill in the corresponding fields.
- A single proposal's `messages` array may hold several messages; on passing they
  execute in order, atomically.
