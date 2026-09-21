# Governance domain routing · testing runbook

> ⚠️ **Do not use `--gas auto`**. Simulated transactions are signed with no sign
> mode set, and the Phase 0 account ante rejects them (`signer 0 uses an invalid
> signature mode`, `app/account_ante.go`). Give every transaction a fixed
> `--gas`, for example `--gas 900000`.

> Location: `docs/runbooks/governance_dualmode_testing.md`
> Subject: the **current** implementation described in
> `docs/governance_dualmode_design.md` §11–§12.
> Two layers: **L1 automated tests** (seconds, and the strictest) → **L2 localnet
> end to end**.

> **⚠️ Rewritten twice.**
> 2026-09-17: the old L2 §2.3–2.4 and the old L3 were written around "builders
> vote weighted by bond"; after main landed Phase 0 builders have no
> bond, the implementation was deleted, and those scenarios no longer exist.
> 2026-09-18: after checking against monorepo governance protocol v0.5, **the
> custom tally function was removed entirely** (§2, "no TrueOpen custom tally is
> added"), and the phase gate went with it; L1 now pins the three denominator
> boundaries of the SDK default tally instead.
> Both times the material was removed rather than rewritten — keeping it would
> mislead.

---

## L1 automated tests (fastest, broadest coverage)

```bash
# every governance-domain test
go test ./app/ -run 'GovDomain|BuilderDomain|Tally|ProposalDomain|HasMixedGovDomains' -v

# full regression
go test ./... && make check-split && go vet ./app/...
```

What is covered:

- Domain classification (pure standard / pure builder / mixed / empty / nil
  messages)
- Both domains are decided by the same SDK default tally, and both can pass
- **The three denominator boundaries of the SDK default tally**: abstain is
  outside the threshold denominator and inside the veto denominator; jailing
  empties the numerator without shrinking the quorum denominator; only unbonding
  truly leaves the denominator (the MUST in governance protocol §2)
- Mixed proposals rejected at submission, wired into the real ante chain

### Checking that the tests themselves are load-bearing (negative controls)

```bash
# 1) drop the Jail call -> the jailed-boundary test no longer observes power going to zero
#    comment out `app.StakingKeeper.Jail(ctx, consAddr)` in app/gov_tally_test.go
go test ./app/ -run TestTallyJailedValidator          # expect FAIL

# 2) make the abstain weights asymmetric (0.4/0.6)
go test ./app/ -run TestTallyAbstainIsExcludedFromThresholdDenominator   # expect FAIL

# 3) raise the veto weight to 0.4
go test ./app/ -run TestTallyAbstainIsIncludedInVetoDenominator          # expect FAIL

# 4) drop the submit-time ante wiring -> mixed proposals are no longer rejected
#    comment out the proposalDomainGuard block in app/app.go
go test ./app/ -run TestProposalDomainGuardIsWiredIntoApp   # expect FAIL (reports "Tx must be GasTx")
```

Remember to revert afterwards. All four have been reproduced in practice.

> **This chain supplies no custom tally function** (governance protocol §2, "no
> TrueOpen custom tally is added"). So the denominator tests in L1 exercise the
> SDK's own implementation, and act as an **upgrade tripwire**: an SDK upgrade
> that moves a denominator fails here, rather than quietly changing governance
> outcomes in production.

---

## L2 localnet end to end

> ⚠️ Use a **separate HOME_DIR** so you do not disturb your existing chain data.

### 2.0 Start the chain

```bash
export TESTHOME=~/.node-govtest
HOME_DIR=$TESTHOME GOV_FAST=1 FAST_BLOCKS=1 RESET=1 \
  ./scripts/localnet_single_node.sh
```

`GOV_FAST=1` → a 300s voting period and a 1,000,000 base-unit deposit threshold.
In another terminal:

```bash
export TESTHOME=~/.node-govtest
alias nd="build/noded --home $TESTHOME"
nd status | jq -r '.sync_info.latest_block_height'
nd query gov params -o json | jq '{min_deposit, burn_vote_veto, proposal_cancel_dest}'
```

**Expected**: `min_deposit`'s denom is the business denom `uusdc`;
`burn_vote_veto` is `true`; `proposal_cancel_dest` is empty. All three are
settled by `genesis apply-seed` — do not edit them by hand; the reasons are in
`docs/governance_spec.md` §17.1.

### 2.1 A mixed proposal is rejected **at submission**

```bash
GOV_AUTH=trueopen10d07y265gmmuvt4z0w9aw880jnsr700jc0zupp
MODEL=$(nd query hub models -o json | jq -r '.models[0].model_id')
cat > /tmp/mixed.json <<EOF
{
  "messages": [
    { "@type": "/hub.v1.MsgSetModelStatus", "authority": "$GOV_AUTH",
      "model_id": "$MODEL", "new_status": "MODEL_PROFILE_STATUS_FROZEN",
      "reason_code": "GOVERNANCE_REASON_FREEZE" },
    { "@type": "/hub.v1.MsgUpdateHubParams", "authority": "$GOV_AUTH",
      "params": $(nd query hub params -o json | jq '.params') }
  ],
  "deposit": "1000000uusdc",
  "title": "mixed domain (should be rejected)",
  "summary": "builder-domain + standard-domain in one proposal"
}
EOF
nd tx gov submit-proposal /tmp/mixed.json --from validator --keyring-backend test \
  --chain-id trueopen-localnet-1 --fees 0uusdc -y
```

**Expected**: the transaction fails with an error containing
`proposal mixes builder-domain and standard-domain messages`.

This is the **only** behavioural difference this feature produces in a real
transaction flow, which makes it the core of L2.

### 2.2 A standard proposal → passes

```bash
HOME_DIR=$TESTHOME ./scripts/gov_update_hub_param.sh builder.builder_set_cap 20
```

**Expected**: `final status: PASSED`, and the parameter changes from its old
value to 20.

### 2.3 A builder-domain proposal → also passes in Phase 0

Split into its own proposal, `MsgSetModelStatus` is decided by the validators'
staked votes and **should pass**. The block below has been run for real on the
merged tree (see "Verified record" at the end).

```bash
MODEL=$(nd query hub models -o json | jq -r '.models[0].model_id')
cat > /tmp/model_freeze.json <<EOF
{
  "messages": [
    { "@type": "/hub.v1.MsgSetModelStatus", "authority": "$GOV_AUTH",
      "model_id": "$MODEL", "new_status": "MODEL_PROFILE_STATUS_FROZEN",
      "reason_code": "GOVERNANCE_REASON_FREEZE" }
  ],
  "deposit": "1000000uusdc",
  "title": "freeze demo model",
  "summary": "builder-domain proposal, decided by the standard electorate in Phase 0"
}
EOF
nd tx gov submit-proposal /tmp/model_freeze.json --from validator --keyring-backend test \
  --chain-id trueopen-localnet-1 --fees 0uusdc --gas 600000 -y
nd tx gov vote 1 yes --from validator --keyring-backend test \
  --chain-id trueopen-localnet-1 --fees 0uusdc --gas 300000 -y
# wait for the voting period to close (300s under GOV_FAST)
nd query gov proposal 1 -o json | jq '{status:.proposal.status, tally:.proposal.final_tally_result}'
nd query hub models -o json | jq -r '.models[] | "\(.model_id) \(.status)"'
```

**Expected**: `PROPOSAL_STATUS_PASSED`, `yes_count` equal to the validator's
`ubond` self-delegation, and the model moving from `ACTIVE` to `FROZEN`.

If this comes back `REJECTED`, the domain classification is being misused as an
electorate — under Phase 0 **models could then never be frozen or delisted**,
which is exactly the failure mode §12.2 guards against.

---

## Phases: there is no switch to test

Node implements no Phase 0 / Phase 1 runtime switch (the reasons are in §12.3 of
the design document). All you can inspect is the on-chain facts:

```bash
nd query hub params -o json | jq '.params.phase0.native_token_enabled'   # false
nd query staking params  -o json | jq -r '.params.bond_denom'            # ubond
```

Both are pinned by `validatePhase0Params` (`x/hub/types/params.go:349,357`), so
Phase 1 cannot be constructed on localnet. A Phase 1 builder electorate is to be
introduced by a protocol upgrade per governance protocol §9, and this runbook
will need a matching section when that happens.

---

## Suggested test order

1. **L1** — seconds; run it first to confirm the code logic is right.
2. **L2 §2.1** — the only observable end-to-end difference; always test it.
3. **L2 §2.2 / §2.3** — confirm both domains complete the governance flow.

---

## Verified record (2026-09-18, full live-chain regression)

Two independent localnets, 14 proposals in total, covering every reachable
governance branch.

### Rejected at submission

| Case | Result |
|---|---|
| Mixed-domain proposal | CheckTx `code=12`, `proposal mixes builder-domain and standard-domain messages` |
| Deposit paid in `ubond` | DeliverTx `code=23`, `gov accepts only the following denom(s): [uusdc]` |
| `authority` not the governance address | DeliverTx `code=13`, `expected gov account as only signer` (the proposal is never created) |

### The four voting outcomes

| Vote | Result | Tally |
|---|---|---|
| yes (all voting power) | `PASSED` and executed | yes=1000000000 |
| no | `REJECTED` | no=1000000000 |
| no vote at all | `REJECTED` (quorum not met) | all zero |
| abstain only | `REJECTED` ("everyone abstains") | abstain=1000000000 |
| weighted split yes 0.6 / no 0.4 | `PASSED` (0.6 > the 0.5 threshold) | yes=600000000 no=400000000 |
| no, then changed to yes | The change takes effect and counts as yes | yes=1000000000 |

### Deposit lifecycle

| Case | Result |
|---|---|
| Initial deposit below `min_deposit` | Stays in `DEPOSIT_PERIOD` |
| Topped up to the threshold | Moves into `VOTING_PERIOD` immediately |
| PASSED / FAILED / non-veto REJECTED | The deposit is **refunded in full** (the validator balance returns to baseline) |

### Model / profile status governance

| Case | Result |
|---|---|
| `ACTIVE → FROZEN` (reason FREEZE) | `PASSED`, the status really changes |
| `FROZEN → REGISTERED` (reason UNFREEZE) | `PASSED` |
| **`→ ACTIVE`** | `FAILED` — governance cannot set ACTIVE |
| **`→ EMERGENCY_FROZEN`** | `FAILED` — reserved for the validators' emergency-freeze quorum |
| **reason not paired with the target status** | `FAILED` |
| `MsgSetProfileStatus` freezing the only active profile | `PASSED`, and the parent model falls back from `ACTIVE` to `REGISTERED` automatically |

### Parameters and expedited proposals

| Case | Result |
|---|---|
| `MsgUpdateHubParams` (correct `expected_version`) | `PASSED`, the parameter really takes effect |
| `MsgUpdateHubParams` (stale `expected_version`) | `FAILED`, `expected_version N does not match current params_version M` |
| `/cosmos.gov.v1.MsgUpdateParams` changing `voting_period` | `PASSED`, the new value applies immediately |
| `expedited: true` | `PASSED`, using `expedited_min_deposit` and the shorter voting period |

### Consistency throughout

The chain produced blocks continuously to height 760, `grep -ciE
"invariant|panic"` = 0, and the treasury bank balance stayed consistent with the
ledger version throughout.

---

## The veto and cancellation paths

`no_with_veto` rejection and proposal cancellation both go through x/gov's
`BurnCoins`. That call used to halt the chain:

```
panic: module account gov does not have permissions to burn tokens: unauthorized
  bank/keeper.BaseKeeper.BurnCoins        <- the raw bank keeper, not GovernedGovBankKeeper
  gov/keeper.Keeper.DeleteAndBurnDeposits
  gov.EndBlocker                           abci.go:151
```

The cause was that the hand-written `depinject.BindInterface` type names did not
match depinject's `fullyQualifiedTypeName` (the short package name has to appear
twice), so the bindings silently never matched and the guards were never
constructed.

**This is fixed.** `app/depinject_binding.go` derives both names from the types
themselves via `bindGuardedInterface[Iface, Impl]()`, so the spelling cannot
drift again, and it panics at package initialisation if the pair is not an
interface and an implementation of it. `app/bank_guard_wiring_test.go` covers all
four modules that must never see the raw bank keeper (x/gov, x/staking, and both
Hyperlane modules) — `TestUpstreamModulesReceiveTheirBankGuards` checks the
wiring itself by reflection, and `TestGovDepositForfeitureReachesTreasury` drives
the exact call x/gov's EndBlocker makes on the veto path.

So `no_with_veto` and proposal cancellation are now safe to exercise. The
forfeited deposit is not burned: `GovernedGovBankKeeper.BurnCoins` turns it into
a transfer to hub_treasury with a matching treasury-inflow record.
