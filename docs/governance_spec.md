# TrueOpen on-chain governance · reference

> ⚠️ **Do not use `--gas auto`**. Simulated transactions are signed with no sign
> mode set, and the Phase 0 account ante rejects them (`signer 0 uses an invalid
> signature mode`, `app/account_ante.go`). Give every transaction a fixed
> `--gas`, for example `--gas 900000`.

## Contents

1. In one sentence
2. What governance can and cannot do
3. Overall architecture
4. Participants and roles
5. The full governance lifecycle
6. Voting
7. Tallying rules (with a worked example)
8. Deposits
9. Governance parameter table
10. The list of governable operations
11. Worked examples (CLI)
12. Security and permission model
13. Boundaries against this chain's own mechanisms
14. FAQ
15. Where V2 is heading
16. Appendix (default parameters / status enums / address derivation / glossary)

---

## 1. In one sentence

**Governance = token stakers voting collectively to make enforceable on-chain
decisions about the chain's parameters and key state.** Anyone can raise a
"proposal" (to change a parameter, to list or delist a model, and so on);
stakers vote weighted by their stake; and once a proposal passes, the chain
executes its contents **automatically, with no human step**. The whole process is
public, auditable, and enforced by code.

Governance on this chain uses the industry-standard Cosmos SDK governance module
(`x/gov`), so the flow matches the rest of the Cosmos ecosystem (the Cosmos Hub,
for instance).

---

## 2. What governance can and cannot do

### ✅ Can

- **Change module parameters**: hub's task-acceptance / reward / bonding
  parameters, task's task parameters, and so on.
- **Freeze / unfreeze / delist a model**: set a model (or a profile version) to
  FROZEN / REGISTERED / DELISTED.
  **Note that governance cannot set ACTIVE** — activation is derived
  automatically from aggregated node support; see §11 and governance protocol §5.
- **Change governance's own parameters**: voting period, deposit threshold, pass
  rate, and so on.
- **Adjust business reference tables**: the reference bucket, the timeout bucket.
- In principle, **any operation whose authority is set to the governance
  address** can be executed by governance.

### ❌ Cannot / is not governance's job

- **Move or appropriate business funds directly**: the business modules' escrow,
  reward and treasury accounts are controlled by each module's keeper logic and
  cannot be drained by an ordinary proposal.
- **Change the code of the modules that are wired in**: changing code or swapping
  the binary is a "software upgrade", which needs a separate upgrade module and
  process (V1 does not wire an upgrade module).

---

## 3. Overall architecture

| Component | Description |
|---|---|
| Governance module | Cosmos SDK `x/gov` (v1), wired into the app through depinject/appconfig |
| Governance module account address (authority) | `trueopen10d07y265gmmuvt4z0w9aw880jnsr700jc0zupp` (constant, no private key; derivation in the appendix) |
| When tallying happens | In each block's EndBlock, in the order `gov → staking → hub → task` (gov first) |
| Source of voting power | Bonded staking weight |
| Tallying denom | `ubond` — the non-circulating Phase 0 consensus staking denom (`app/config.go:7`) |
| Deposit denom | `uusdc` — the business denom, forced at genesis (`cmd/noded/cmd/genesis_seed.go:744`) |

**The key point**: governance's power to execute is embodied in the governance
module account address. Every governable operation requires its `authority` field
to equal that address — and that address **has no private key, so nobody can sign
for it**. The only way to act as it is to complete the governance process and
have the gov module execute on its behalf once a proposal passes. That is what
mechanically guarantees that parameters and state can only be changed collectively.

---

## 4. Participants and roles

- **Proposer**: any account. Submits the proposal and (usually) fronts the
  initial deposit.
- **Stakers (validators + delegators)**: the voters; voting power = their bonded
  stake.
    - **Validators**: vote directly; delegators who do not vote separately
      **inherit** their validator's vote by default.
    - **Delegators**: may vote themselves, overriding their validator's default.
- **The governance module account**: the executing identity after a proposal
  passes (no private key).
- **Depositors**: anyone may add to a proposal's deposit to help it reach the
  voting period.

---

## 5. The full governance lifecycle

```
  ┌──────────┐  min_deposit reached  ┌──────────────┐  voting closes  ┌────────────────┐
  │ submit   │ ────────────────────▶ │ voting period │ ──────────────▶ │ tally & execute │
  │ (+deposit)│                       │ VOTING_PERIOD │                 │ (gov EndBlock)  │
  └──────────┘                       └──────────────┘                 └────────────────┘
       │                                                                     │
       │ deposit short and expired                             PASSED   -> execute messages
       ▼                                                       REJECTED -> not executed
   void (deposit handled per the rules)                        FAILED   -> passed but execution errored
```

### Stage one: submit the proposal (submit-proposal)

A proposal is a JSON document whose core is a set of `messages` (the operations
to perform, such as "change hub params"), plus `deposit` (the initial deposit),
`title`, `summary` and `metadata`. After submission it enters the deposit period.

### Stage two: deposit period (DEPOSIT_PERIOD)

- The proposal must accumulate **`min_deposit`** within
  **`max_deposit_period` (currently 48 hours)** (amounts in §9; the denom is the
  business denom `uusdc`).
- Anyone may add to the deposit.
- Threshold reached → the voting period begins; expired without reaching it → the
  proposal is void.

### Stage three: voting period (VOTING_PERIOD, currently 48 hours)

Stakers vote `yes / no / no_with_veto / abstain`, weighted by bonded stake. Votes
may be changed during this stage.

### Stage four: tallying (automatic, in gov's EndBlock)

As soon as the voting period closes, gov tallies in that block's EndBlock (quorum
/ threshold / veto, see §7) and decides:

- **PASSED**: passed, and the proposal's messages are **executed there and then**;
- **REJECTED**: the passing conditions were not met, nothing is executed;
- **FAILED**: the passing conditions were met but execution errored (invalid
  parameters, say).

### Stage five: execution and wrap-up

- A passed proposal: gov executes all messages in order, atomically, as the
  governance address; the deposit is refunded to the proposer/depositors.
- Rejected with `no_with_veto`: the deposit is **forfeited**
  (`burn_vote_veto = true`), but this chain **transfers it to `hub_treasury`**
  rather than burning it; see §17.1.
- Otherwise rejected: the deposit is refunded per the rules.

> **How is the execution time decided?** By the proposal's own voting end time
> (= when it entered the voting period + the voting period length); gov checks
> every block for proposals that have come due. **A proposal does not need to —
> and cannot — name the block it executes in.**

---

## 6. Voting

- **Voting power = bonded stake weight.** Holding tokens without staking confers
  no voting power.
- **Four options**:
    - `yes`; `no`; `no_with_veto` (strong opposition — counts toward the veto
      line and causes the deposit to be forfeited); `abstain` (counts toward
      turnout but not toward the yes/no ratio).
- **Delegation inheritance**: a delegator who does not vote follows their
  validator; a delegator who votes overrides it.
- **Weighted votes**: one address may split its voting power across several
  options.

---

## 7. Tallying rules (with a worked example)

Let `total votes cast = yes + no + no_with_veto + abstain` (all by stake).

Three gates (at the current defaults):

1. **Quorum (turnout) ≥ 33.4%**: `total votes cast / network-wide bonded stake ≥
   0.334`. Below it, the proposal is rejected outright.
2. **Veto (veto rate) < 33.4%**: `no_with_veto / total votes cast < 0.334`. Above
   it, the proposal is rejected and the deposit is forfeited.
3. **Threshold (pass rate) > 50%**: `yes / (yes + no + no_with_veto)` (**abstain
   is not in the denominator**) `> 0.5` → passed.

**Worked example** (network-wide bonded = 100, for one proposal):

- 40 voted in total (yes 25 / no 10 / veto 0 / abstain 5).
- Quorum: 40/100 = 40% ≥ 33.4% ✅
- Veto: 0/40 = 0% < 33.4% ✅
- Threshold: 25/(25+10+0) = 71.4% > 50% ✅ → **passed**.

**Expedited proposals**: a shorter voting period (currently 24h), a higher pass
rate (`expedited_threshold = 66.7%`), and a higher initial deposit threshold
(`expedited_min_deposit = 50,000,000 uusdc`).

---

## 8. Deposits

- **min_deposit (currently 10,000,000 `uusdc`)**: the cumulative deposit a
  proposal needs to enter the voting period; it keeps spam proposals out.
- **max_deposit_period (currently 48h)**: how long there is to raise it; a
  proposal that does not make the threshold in time is void.
- **Where the deposit goes**:
    - The proposal ends normally (passed, or rejected without a veto) → the
      deposit is **refunded**.
    - Rejected with `no_with_veto` (`burn_vote_veto = true`) → the deposit is
      **not refunded**, but this chain does **not burn it**:
      `GovernedGovBankKeeper` transfers it into `hub_treasury` and records a
      treasury inflow; see §17.1. This is exactly what ADR-0018 Decision 3 asks
      for.
- The deposit denom is the business denom `uusdc`, forced at genesis rather than
  gov's default.

---

## 9. Governance parameter table

The current (genesis default) values. **All of them can be changed by
governance** (see the gov parameters in §10):

| Parameter | Current value | Meaning |
|---|---|---|
| `min_deposit` | 10,000,000 `uusdc` | Deposit threshold to enter the voting period |
| `max_deposit_period` | 172800s (48h) | Length of the deposit period |
| `voting_period` | 172800s (48h) | Length of the voting period |
| `quorum` | 0.334 (33.4%) | Minimum turnout |
| `threshold` | 0.500 (50%) | Pass rate (abstain excluded) |
| `veto_threshold` | 0.334 (33.4%) | Veto line |
| `expedited_voting_period` | 86400s (24h) | Voting period for expedited proposals |
| `expedited_threshold` | 0.667 (66.7%) | Pass rate for expedited proposals |
| `expedited_min_deposit` | 50,000,000 `uusdc` | Deposit threshold for expedited proposals |
| `burn_vote_veto` | true | Triggers deposit forfeiture; this chain transfers it to the treasury rather than burning (§17.1) |
| `min_deposit_ratio` | 0.01 | Minimum share of `min_deposit` a single deposit must be |

> These values are written into `genesis.json → app_state.gov.params` from gov's
> defaults during `noded init`; `noded genesis apply-seed` then rewrites the
> deposit denom to the business denom and pins the three burn switches
> (`applySDKGenesisParams`). **Before launch** they can be edited directly in
> genesis; **after launch** only governance can change them.

---

## 10. The list of governable operations

What governance can do = which messages with `authority = the governance address`
it can execute. The typical governable operations on this chain today:

**hub**

- `MsgUpdateHubParams`: change all Hub module parameters (whole-object
  replacement; the complete parameter set must be supplied).
- `MsgSetModelStatus`: set a model's status (FROZEN / REGISTERED / DELISTED;
  **ACTIVE is rejected**).
- `MsgSetProfileStatus`: set the status of a profile version of a model (as
  above; ACTIVE is rejected).
- `MsgUpdateReferenceBucket`: update the pricing/reference bucket.

**task**

- `MsgUpdateTaskParams`: change the task module parameters.
- `MsgUpdateTimeoutBucket`: update the timeout reference bucket.

**Governance itself**

- `/cosmos.gov.v1.MsgUpdateParams`: change the governance parameters in §9.

> The general rule: **any operation whose message has `authority` set to the
> governance address can only be executed by governance, and can be submitted as
> a proposal message.** One proposal may hold several messages; on passing they
> execute in order, atomically.

---

## 11. Worked examples (CLI)

> The step-by-step manual for changing hub params is in the companion document:
> `docs/runbooks/governance_update_hub_params.md`.

**Submit a proposal**

```bash
noded tx gov submit-proposal proposal.json \
  --from <account> --chain-id <chain-id> --keyring-backend <backend> \
  --gas 900000 --fees 0uusdc -y
```

**proposal.json structure** (example: change hub params)

```json
{
  "messages": [
    {
      "@type": "/hub.v1.MsgUpdateHubParams",
      "authority": "trueopen10d07y265gmmuvt4z0w9aw880jnsr700jc0zupp",
      "expected_version": "<meta.params_version from the params query>",
      "params": {
        "...": "the complete HubParams, every field"
      }
    }
  ],
  "deposit": "10000000uusdc",
  "title": "Example: adjust Hub parameters",
  "summary": "What this proposal is for and what it affects."
}
```

> `expected_version` is optimistic concurrency: the message is rejected unless it
> names the params version the chain currently holds. Read it from
> `noded query hub params -o json | jq -r '.meta.params_version'`; an absent
> `meta` means version 0.

**proposal.json structure** (example: **freeze a model**)

> ⛔ **Governance cannot set a model to `ACTIVE`.** `ACTIVE` is derived solely
> from support statistics, and the chain rejects it outright
> (`x/hub/keeper/msg_server_registry.go:28`, reporting `ACTIVE status is derived
> from active profile support and cannot be set by governance`); the protocol
> forbids it too (governance protocol §5, "governance must not: set ACTIVE
> directly"). **Listing a model is not a governance action** — it is activated
> automatically by a threshold once nodes declare support.
>
> These are the only status changes governance can make (governance protocol §5):
>
> | from → to | reason_code |
> |---|---|
> | `REGISTERED` / `ACTIVE` → `FROZEN` | `GOVERNANCE_REASON_FREEZE` |
> | `FROZEN` → `REGISTERED` (unfreeze) | `GOVERNANCE_REASON_UNFREEZE` |
> | `REGISTERED` / `ACTIVE` / `FROZEN` → `DELISTED` | `GOVERNANCE_REASON_DELIST` |
> | `EMERGENCY_FROZEN` → `REGISTERED` | `GOVERNANCE_REASON_EMERGENCY_RECOVERY` |
>
> `EMERGENCY_FROZEN` itself cannot be set by governance — it belongs to the
> validators' emergency-freeze quorum (`MsgEmergencyFreezeSignal` /
> `MsgEmergencyFreezeVote`), not to an x/gov proposal.

The message is `MsgSetModelStatus`, with fields `authority` (the governance
address), `model_id`, `new_status` and `reason_code`. `reason_code` is required
and must match the target status, or the chain reports `does not match reason`.

```json
{
  "messages": [
    {
      "@type": "/hub.v1.MsgSetModelStatus",
      "authority": "trueopen10d07y265gmmuvt4z0w9aw880jnsr700jc0zupp",
      "model_id": "<target model id, e.g. llama3-8b>",
      "new_status": "MODEL_PROFILE_STATUS_FROZEN",
      "reason_code": "GOVERNANCE_REASON_FREEZE"
    }
  ],
  "deposit": "10000000uusdc",
  "title": "Freeze model llama3-8b",
  "summary": "Following community assessment, freeze model llama3-8b to stop it accepting new tasks."
}
```

Field notes:
- `authority`: **must** be the governance address
  `trueopen10d07y265gmmuvt4z0w9aw880jnsr700jc0zupp`, or execution fails.
- `model_id`: the model to act on (check it exists and see its current status
  with `noded query hub models`).
- `new_status`: see the table above. `MODEL_PROFILE_STATUS_ACTIVE` is rejected
  outright.
- `reason_code`: see the table above; it must be paired with `new_status`.

> **To act on a specific profile version**, use `MsgSetProfileStatus` instead,
> which adds a `profile_version` field:
> ```json
> {
>   "messages": [
>     {
>       "@type": "/hub.v1.MsgSetProfileStatus",
>       "authority": "trueopen10d07y265gmmuvt4z0w9aw880jnsr700jc0zupp",
>       "model_id": "<target model id>",
>       "profile_version": 1,
>       "new_status": "MODEL_PROFILE_STATUS_DELISTED",
>       "reason_code": "GOVERNANCE_REASON_DELIST"
>     }
>   ],
>   "deposit": "10000000uusdc",
>   "title": "Delist profile v1 of llama3-8b",
>   "summary": "Delist profile version 1 of model llama3-8b."
> }
> ```

> **Note**: a status set by governance has source `SOURCE_GOVERNANCE` and is not
> overwritten by the automatic support-activation mechanism
> (`SOURCE_AUTO_SUPPORT`); see §13. Conversely, unfreezing back to `REGISTERED`
> hands the status back to automatic derivation — if aggregated support is still
> above the threshold, `ACTIVE` is re-derived within the same transaction.
>
> ⚠️ **Do not put model/profile status messages in the same proposal as anything
> else**: this chain rejects mixed proposals in the ante handler (see §12), so
> they must be submitted as separate proposals.

**Adding a deposit / voting / querying**

```bash
noded tx gov deposit  <proposal-id> 10000000uusdc --from <account> ...
noded tx gov vote     <proposal-id> yes           --from <account> ...
noded query gov proposal <proposal-id>
noded query gov tally    <proposal-id>
noded query gov params
```

---

## 12. Security and permission model

- **A single entry point for execution**: the governance address
  `trueopen10d07y265gmmuvt4z0w9aw880jnsr700jc0zupp` has no private key, so nobody
  can forge its signature; only gov can act in its name, and only after a
  proposal passes. Hence parameters and state cannot move unless the collective
  agrees.
- **Anti-spam**: the deposit threshold (min_deposit) plus the deposit period
  deadline.
- **Anti-malice**: `no_with_veto` reaching 33.4% rejects the proposal outright
  and **forfeits the deposit**, which is an economic penalty on a malicious
  proposer.
- **Against low-turnout manipulation**: the 33.4% quorum floor.
- **Business account protection**: hub's and task's module accounts are on bank's
  blocked list (the governance address itself is not blocked, so that it can
  receive deposits), so ordinary transfers cannot inject into them and business
  funds can only move through each module's keeper logic.

---

## 13. Boundaries against this chain's own mechanisms

This chain has **two distinct "collective decision" mechanisms**, which should
not be confused:

| Dimension | On-chain governance (x/gov) | Automatic model support activation (a business mechanism) |
|---|---|---|
| Trigger | A proposal + a fixed voting period + turnout/pass-rate tallying | The status switches automatically once the **eligible stake share** supporting that profile reaches a threshold |
| Threshold semantics | quorum/threshold (shares of votes cast / of the network, tallied at period end) | `ActiveSupportStake / eligible support stake ≥ active_support_stake_ratio` (currently 2/3) and supporter count ≥ `active_supporter_min_count` |
| Status source | `SOURCE_GOVERNANCE` (set manually by governance) | `SOURCE_AUTO_SUPPORT` (automatic, by support) |
| Applies to | Parameters, manual model listing/delisting, governance's own parameters | Automatic ACTIVE / fallback for models and profiles |
| Wired in by this work | Yes (x/gov) | Already present in hub business logic |

There is also a `SOURCE_EMERGENCY` (emergency freeze) path. **None of the three
overrides another**: statuses set by governance or by an emergency are not
rewritten by the automatic support mechanism.

> If the business wants something like "list/delist a model the instant supporting
> stake reaches 10% of the network" — an **immediate threshold trigger** — that
> belongs to the **parameter semantics of the automatic support mechanism**
> (tuning `active_support_stake_ratio_*` and friends), or needs dedicated new
> logic on the Keeper side. It is **not** something x/gov's general governance can
> express. The semantics need to be agreed with the Keeper side.

---

## 14. FAQ

**Q: When does a proposal take effect after passing?**
A: Automatically, in the EndBlock of the block in which the voting period closes
— within seconds, with no manual step.

**Q: Can an ordinary user vote without staking?**
A: They have no voting power. Voting power comes from bonded stake (which can be
obtained by delegating to a validator).

**Q: Why must a parameter change "supply the complete parameter set"?**
A: Parameter updates are whole-object replacement. The correct approach is to read
the current complete parameters first, change only the target field, and leave the
rest as they are (see the companion runbook).

**Q: Can governance move money out of someone's account?**
A: No. Governance can only execute the defined operations whose authority is the
governance address; business funds are constrained by each module's keeper logic.

**Q: A 48-hour voting period is too long — how do I test?**
A: On local/test networks the `voting_period` and `max_deposit_period` can be
shortened in genesis (to 60s, say); see §7 of the companion runbook, or use
`GOV_FAST=1` with `scripts/localnet_single_node.sh`.

---

## 15. Where V2 is heading

Under the multi-chain architecture (a Hub plus N task chains), **governance
belongs to the Hub chain**. Parameters and model statuses that governance changes
on the Hub will be broadcast to the task chains through the downstream cross-chain
packets **D3 `ParamsUpdate` / D2 `RegistryUpdate`**. In the V1 single-chain stage
governance acts directly on this chain's hub/task; after the split it evolves in
that direction, and the flow and semantics described here do not change.

---

## 16. Appendix

### 16.1 Governance default parameters (genesis)

See the table in §9. Original source: `genesis.json → app_state.gov.params` as
produced by `noded init`.

### 16.2 Model status enum (ModelProfileStatus)

| Value | Meaning |
|---|---|
| UNSPECIFIED | Unspecified |
| REGISTERED | Registered (not activated) |
| ACTIVE | Listed / activated |
| FROZEN | Frozen |
| EMERGENCY_FROZEN | Emergency frozen |
| DELISTED | Delisted |

Status source (StatusSource): `AUTO_*` (automatic, by support) / `GOVERNANCE`
(governance) / `EMERGENCY` (emergency).

### 16.3 Deriving the governance address

The governance address is not a generated key; it is derived deterministically
from the string `"gov"`:

```
address = bech32( hrp="trueopen", data = sha256("gov")[:20] )
        = trueopen10d07y265gmmuvt4z0w9aw880jnsr700jc0zupp
```

Cross-check it with `noded query auth module-accounts`. Every Cosmos chain shares
the middle of this address; only the prefix and the checksum differ.

### 16.4 Glossary

- **ubond**: the Phase 0 consensus staking denom; non-circulating, not
  transferable, and changed only by governance MintBond / BurnBond.
- **uusdc**: the business denom (USDC bridged in over Hyperlane); used for gas,
  task funds and governance deposits alike.
- **bonded stake**: tokens in the bonded (staking-active) state, which determine
  voting power.
- **quorum / threshold / veto**: the three tallying gates — turnout, pass rate and
  veto rate.
- **authority**: the address legally permitted to initiate an operation; for
  governance operations, the governance module address.
- **EndBlock**: the automatic processing stage at the end of each block, where gov
  tallies and executes proposals that have come due.

---

## 17. Phase 0 (a mainnet without a token): this is the current shape

The token-free phase defined by monorepo ADR-0018 (merged) **is already what this
chain is**, not a future plan.

| Item | Phase 0 (current) | Location |
|---|---|---|
| **What carries voting power** | the non-circulating internal denom `ubond`; each validator self-delegates the same amount | `app/config.go:7` |
| **Business / deposit denom** | `uusdc` (USDC bridged in over Hyperlane) | `x/hub/types/params.go:32` |
| **Who decides model freezing/delisting** | **standard governance (the validators)**, using x/gov's default tally — the ADR is explicit that Builders are not given governance voting power, and Builders have no bond left to weight by | `app/gov_domain.go` |
| **Custom tally** | **None.** This chain supplies no `CalculateVoteResultsAndVotingPowerFn` to x/gov | governance protocol §2 / parameter table §8 `[hard boundary]` |
| **gov deposit denom** | forced to the business denom at genesis | `cmd/noded/cmd/genesis_seed.go:744` |
| **veto deposit** | nominally burned, actually **transferred into hub_treasury and recorded** | `app/governance_bank_guard.go:27` |

**One natural consequence**: in Phase 0 every validator holds the same amount of
`ubond` and only self-delegates, so substituting into the weighting formula in §6
gives everyone equal weight — **"one validator, one vote" is a natural degenerate
case of the formula and needs no special code**.

### 17.1 The full path a deposit takes

On a successful veto, x/gov calls `bankKeeper.BurnCoins(gov, deposits)`. This
chain replaces gov's bank keeper with `GovernedGovBankKeeper`, which does not
burn but instead:

```
gov module account --SendCoinsFromModuleToModule--> hub_treasury
                                                  + CreditGovernanceDepositResidual(amount)
```

So ADR-0018 Decision 3's "burning the deposit is implemented as a transfer into
trueopen_treasury" is implemented **literally**, and USDC is never burned. Genesis
therefore sets `burn_vote_veto = true` deliberately — that is the switch that
triggers this path.

> ⚠️ Do not set `proposal_cancel_dest` in genesis. When that value is non-empty
> the cancellation path takes `SendCoinsFromModuleToAccount` (SDK
> `x/gov/keeper/deposit.go:255`) and **bypasses** `BurnCoins`, so the treasury
> receives the money but the hub ledger records no residual. Leaving it empty
> takes the `BurnCoins` branch at `:243`, which the guard correctly takes over.

### 17.2 There is no phase switch

Node **does not implement** a Phase 0 / Phase 1 runtime switch, for two reasons:

1. The switch could only live inside a tally function, and a custom tally is
   expressly forbidden (governance protocol §2 and §6, ADR-0018 Decision 3,
   parameter table §8 `[hard boundary]`, genesis protocol §6).
2. Phase 1 is not yet expressible: `validatePhase0Params` rejects both
   `native_token_enabled = true` and `consensus_bond_denom != "ubond"`
   (`x/hub/types/params.go:349,357`).

To confirm the current phase, ask the chain:

```bash
noded query hub params -o json | jq '.params.phase0.native_token_enabled'   # false
noded query staking params  -o json | jq -r '.params.bond_denom'            # ubond
```

A Phase 1 builder electorate needs the protocol side to define the source of
weight and the snapshot semantics first, and is to be introduced by a **protocol
upgrade** per governance protocol §9 ("adding a governance action type" is listed
among the cases that require an upgrade) — not by Node unilaterally flipping on a
parameter read.

## 18. Four verified implementation facts

The following are often misunderstood; all have been checked against the SDK
source and this repository's code:

1. **Jailed validators are excluded from the tally by default**, with no custom
   tally needed. `jailValidator` calls `DeleteValidatorByPowerIndex`, and the
   tally only walks the power store and requires `IsBonded()` (the power store
   even asserts *"should never retrieve a jailed validator"*), so their power
   enters neither the numerator nor the denominator.
2. **The accepted deposit denom is derived from `min_deposit`'s denom**
   (`validateDepositDenom`). This chain forces it to the business denom at
   genesis, and `GOV_FAST` changes only the amounts, not the denom.
3. **The veto burn path takes no destination address.** That is not an obstacle:
   rather than changing gov, this chain **replaces its bank keeper** and redirects
   the funds to the treasury at the `BurnCoins` layer (§17.1). The cost is a
   40-line adapter.
4. **Builders have no bond in Phase 0.** `BuilderSetState.active_builders` is a
   plain address list (`x/hub/types/builder.pb.go:620`), and `BuilderStatus` is
   down to `ADMITTED` / `REVOKED`. No "weighted by builder bond" design has a
   source of weight under the current code.

---

*This document is maintained alongside changes to governance code and parameters.
For operational detail, the companion runbooks and the chain's actual parameters
(`noded query gov params`) are authoritative.*
