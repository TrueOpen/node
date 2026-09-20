# TrueOpen dual-mode governance design (standard governance + Builder-role governance)

> **⚠️ Status (2026-09-17): §1–§10 are the original design proposal, and their
> builder-electorate part no longer holds.**
> After main landed ADR-0018 Phase 0, builders have no bond, so a bond-weighted
> electorate has no source of weight and the corresponding implementation was
> deleted. Further, monorepo governance protocol v0.5 §2 states outright that
> **no TrueOpen custom tally is added**, so the "custom tally function" extension
> point that §1–§10 rest on is itself not permitted; the tally hook has been
> removed too.
> **For what the code actually does now see [§11](#11-implementation-notes-where-the-code-differs-from-the-design)
> and [§12](#12-adr-0018-alignment-phase-0-has-only-one-electorate)**; the first
> ten sections are kept as design history and as the starting point for Phase 1.

> Location: `docs/governance_dualmode_design.md`
> Status: **converged onto the protocol** (`app/gov_domain.go` +
> `app/gov_proposal_ante.go`).
> **This chain supplies no custom tally function to x/gov**; the differences
> between the design and what shipped are in "11. Implementation notes" and "12".
> Related: the governance overview is `docs/governance_spec.md`; the runbook for
> parameter changes is `docs/runbooks/governance_update_hub_params.md`.

---

## 0. Executive summary

On-chain governance needs **two modes side by side**:

1. **Standard governance**: no identity restriction, weighted by **network-wide
   stake** (the `x/gov` that is already live).
2. **Builder-role governance**: **only builder votes count**, weighted by
   **builder bond**. For decisions tightly coupled to the builder population
   (such as **listing/delisting a model**).

The core design principle: **which mode a proposal takes is fixed by the chain
rules from the kind of operation it performs (its message content), and the
proposer cannot influence it** — this stops a builder decision being disguised as
a standard proposal to dodge the builder bar.

Technically this needs **no new governance module** — it reuses the existing
`x/gov` and implements builder-electorate tallying through its official "custom
tally function" extension point. The changes sit in the application layer (Node
side) plus one builder-roster read interface that the Keeper side must provide.

---

## 1. Requirements and decisions (confirmed)

| Item | Decision |
|---|---|
| Architecture | **A single `x/gov` + a custom tally branch** (no new governance module) |
| Builder vote weight | **Weighted by builder bond** |
| Scope | **Fixed by subject (content-bound)**: the proposal's message types decide the mode; the manual submit-time label is **dropped** |
| Builder-mode quorum/threshold | **Builder-specific basis**: judged against the builder electorate itself (see §4 for the implementation) |

**Why not "the submitter labels the electorate"**: if the proposer declares the
mode, a builder-domain operation can be labelled "standard" and dodge the builder
bar — the electorate would be under the proposer's control, which is not safe.
Hence content binding.

---

## 2. Overall architecture

```
                         ┌───────────────────────────────────────────────┐
   proposal(messages) ─▶ │  custom tally function (Node supplies to gov)  │
                         │                                                │
                         │  1) classify domain from messages:             │
                         │     standard | builder                         │
                         │  2a) standard -> reproduce the default          │
                         │      stake-weighted tally                       │
                         │  2b) builder  -> tally by builder bond, scale   │
                         │      linearly into stake space so gov's three   │
                         │      gates reproduce it exactly                 │
                         └───────────────────────────────────────────────┘
                                        │ returns (totalVotingPower, results)
                                        ▼
                    gov's own decision: quorum -> veto -> threshold -> execute
```

- Attachment point: the official `x/gov` extension
  `keeper.CalculateVoteResultsAndVotingPowerFn` (the **optional input**
  `CustomCalculateVoteResultsAndVotingPowerFn` on gov's depinject). Node supplies
  an implementation; no SDK change is needed.
- Submission/voting/execution **stay on the standard gov flow and CLI**
  (`tx gov submit-proposal / vote`) and the user notices nothing; the difference
  is only in whose votes count and how they are weighted.

---

## 3. Mode assignment: content binding (by message type)

The chain holds a **fixed mapping** (a code constant, and therefore a consensus
rule):

| Message type | Domain |
|---|---|
| `/hub.v1.MsgSetModelStatus` | **builder** |
| `/hub.v1.MsgSetProfileStatus` | **builder** |
| (everything else, e.g. `/hub.v1.MsgUpdateHubParams`, `/cosmos.gov.v1.MsgUpdateParams`, task params …) | standard |

> The table above is a **first proposal**; which operations finally belong to the
> builder domain has to be settled with product and the Keeper side.

The classifier `domainOf(proposal.messages)`:
- All messages standard → **standard**.
- Any builder-domain message → **builder** (**the strictest wins**).
- **Mixed proposals** (builder-domain and standard-domain in one): judged
  **builder** (strictest wins) to close the bypass; **and it is recommended to
  reject mixed proposals outright at submission** for clearer UX (see §6).

---

## 4. Tally implementation details

The custom tally function signature (SDK 0.53):
```go
func(ctx, k govkeeper.Keeper, proposal v1.Proposal,
     validators map[string]v1.ValidatorGovInfo)
    (totalVotingPower math.LegacyDec, results map[v1.VoteOption]math.LegacyDec, err error)
```
gov then decides by a fixed formula:
```
percentVoting = totalVotingPower / totalBonded         // the quorum denominator is always network-wide stake and cannot be changed
veto          = results[NoWithVeto] / totalVotingPower
threshold     = results[Yes] / (totalVotingPower - results[Abstain])
```

### 4a. The standard branch
Reproduce gov's default tally logic (the default function is not exported, so
Node has to keep its own copy): walk `k.Votes`, accumulate into each option
weighted by the delegator's/validator's bonded share, and — as the default
implementation does — clear the counted votes from storage. Dependencies:
`k.Votes` (exported) + the staking / auth keepers Node holds itself (injected via
a closure) + the `validators` table that is passed in.

### 4b. The builder branch (the interesting one)
Let `totalBonded` be the network-wide bonded total (read from the staking keeper
Node holds).

1. Take the **eligible builder electorate** and its weights `w(b) = builder bond`
   (from the Keeper-side interface, see §5). Let `P = Σ_b w(b)` (total electorate
   weight).
2. Walk the votes: only when the voter is an **eligible builder**, split their
   `w(b)` across options per the weighted vote and accumulate into `r[option]`;
   `participated += w(b)`. Non-builder votes are **ignored** (and cleared from
   storage).
3. If `P == 0` → return `(0, all zero)` → gov's quorum fails → REJECTED.
4. **Scale linearly** into stake space with the factor `s = totalBonded / P`:
   ```
   totalVotingPower = participated * s
   results[opt]     = r[opt]       * s     (for every option)
   ```

**Why this reproduces the builder basis exactly** (the scale factor cancels in
every ratio):
```
percentVoting = participated*s / totalBonded = participated / P           // = builder turnout
veto          = r[veto]*s / (participated*s) = r[veto] / participated     // = builder veto rate
threshold     = r[yes]*s / ((participated-r[abstain])*s)                  // = builder pass rate
              = r[yes] / (participated - r[abstain])
```
So gov applies the **quorum / threshold / veto percentages already on chain** to
the **builder electorate** — that is, "the same passing standard, but the voters
are builders, the weight is bond, and the total is total builder bond". **No new
governance parameters are needed** (builder-specific thresholds can be added
later without disturbing this structure).

> Note: `totalBonded` is only used for scaling and cancels out, so it introduces
> no "builders relative to network-wide stake" bias.

---

## 5. Node / Keeper responsibility boundary

### Node side (the bulk of this proposal)
- `domainOf(messages)`, the message-type mapping plus the builder-domain message
  list (constants).
- The custom tally function (the standard copy + the builder scaled tally + vote
  cleanup).
- Supplying that function to gov via depinject (the closure captures the
  staking / auth / hub read interfaces).
- Unit + integration tests; documentation updates.

### Keeper side (hub, to provide/confirm)
Node defines and Keeper implements a **read-only** interface (builder identity
and weight are Keeper domain data):
```go
// to be implemented by the hub keeper
type BuilderElectorate interface {
    // walk the eligible builder voters for this tally and their voting weight (bond)
    IterateEligibleBuilders(ctx context.Context,
        fn func(addr sdk.AccAddress, weight uint64) (stop bool)) error
    // whether an address is an eligible builder and its weight (for O(1) voter checks)
    EligibleBuilderPower(ctx context.Context, addr sdk.AccAddress) (weight uint64, ok bool, err error)
}
```
Existing material to reuse: `collectActiveBuilders(minBond, snapshotStartHeight)`
(private), `GetBuilder` / `GetBuilderBond` / `isRegisteredBuilderState`, and the
BuilderSet snapshot. Keeper needs to converge these into the **exported**
interface above.

**Calls the Keeper side has to make**:
- **What counts as an eligible builder**: "currently active and at minBond", or
  "the BuilderSet snapshot (term) at the start of the voting period"?
  (The **snapshot** is recommended, so bond cannot be added or removed mid-vote
  to manipulate the result.)
- **Which weight**: which reading of builder bond (current bond / snapshot bond /
  net of unbonding).

---

## 6. Submit-time validation (optional hardening)

For better UX and to catch mistakes, a light check can be added at submission:
- **Reject mixed proposals** (builder-domain and standard-domain messages in one).
- The tally still falls back on "strictest wins", so even an unblocked mixed
  proposal cannot be used as a bypass.

(If that costs too much, the tally fallback alone is enough to start with and the
submit-time check can come later.)

---

## 7. Test plan

- **Unit**: `domainOf` over each combination (pure standard / pure builder /
  mixed); numeric correctness of the builder scaled tally (construct builder
  votes, assert gov's three gates agree with the hand calculation); non-builder
  votes are ignored; `P == 0` leads to rejection.
- **Integration**: start a local chain, register several builders (with different
  bonds), and for
  - a standard proposal (`MsgUpdateHubParams`): verify it tallies by stake;
  - a builder proposal (`MsgSetModelStatus`): verify only builders count, that
    they are bond-weighted, and that non-builder votes have no effect;
  covering pass / reject / veto / quorum-not-met in each case.
- **Regression**: the existing `abci_order_test`, gov genesis, and
  `make check-split` stay green.

---

## 8. Risks and trade-offs

- **Version coupling from copying the default tally**: the standard branch
  transcribes the SDK default logic, so an SDK upgrade requires re-checking it
  (with integration tests against default behaviour as the backstop).
- **The quorum denominator is fixed to network-wide stake**: resolved by the
  linear scaling, which is mathematically equivalent to the builder basis; that
  equivalence needs to be pinned by tests.
- **Builder identity data depends on the Keeper**: the interface semantics
  (active vs snapshot, which bond) must be agreed with Keeper first, or the tally
  semantics are undefined.
- **The message-domain mapping is a consensus rule**: adding a message type that
  "should be builder domain" is a rule change that needs review (it edits the
  mapping constant).

---

## 9. Deliverables (at implementation time)

**Node**
- `app/gov_tally.go` (new): `domainOf` + the custom tally function (standard copy
  + builder scaling).
- `app/app_config.go` / `app.go`: supply `CustomCalculateVoteResultsAndVotingPowerFn`
  via depinject, injecting the staking/auth/hub read interfaces by closure.
- Tests: `app/gov_tally_test.go` (unit), plus governance integration tests.
- Docs: add a "dual mode" chapter to `docs/governance_spec.md`.

**Keeper (hub)**
- Export the `BuilderElectorate` read interface (`IterateEligibleBuilders` /
  `EligibleBuilderPower`).
- Confirm the eligible-builder and weight semantics (active vs snapshot, which
  bond).

---

## 10. Open items for review

1. **The builder-domain message list**: the first cut is `MsgSetModelStatus` /
   `MsgSetProfileStatus` — should anything else be included (certain registry /
   bucket operations, say)?
2. **Eligible-builder semantics**: currently active vs the BuilderSet snapshot
   (snapshot recommended).
3. **Builder thresholds**: reuse the chain's gov quorum/threshold/veto
   percentages (recommended), or add builder-specific parameters?
4. **Mixed proposals**: reject at submission, or rely on the tally fallback
   (judged builder)?

Once reviewed, the Node side can implement the custom tally and its wiring, while
the Keeper side provides `BuilderElectorate` in parallel.

---

## 11. Implementation notes (where the code differs from the design)

> **This section went through two rounds of convergence and was finalised
> 2026-09-18.**
> Round one (merging main): after ADR-0018 Phase 0 landed, **builder bond ceased
> to exist**, a bond-weighted electorate had no source of weight, and that
> implementation was deleted.
> Round two (against monorepo governance protocol v0.5): **a custom tally is
> itself forbidden**, so that went too.
> What follows is the code as it stands.

Only two files remain on the Node side:

| File | Responsibility |
|---|---|
| `app/gov_domain.go` | Classifies the domain by message type URL; a pure function that **takes no part in tallying** |
| `app/gov_proposal_ante.go` | Rejects mixed-domain proposals at submission; the only consumer of the classification |

Tests: `app/gov_domain_test.go` (classification),
`app/gov_proposal_ante_test.go` (guard wiring), `app/gov_tally_test.go` (the
three denominator boundaries of the SDK default tally, see §12.4).

**What was kept** (matching §3 / §6 of this document):

- Content-bound domain classification (by message type URL, not declared by the
  proposer)
- Strictest wins: any builder-domain message makes the whole proposal
  builder-domain
- Submit-time mixed check: mixed proposals are rejected in the ante handler

**What was removed**: the `BuilderElectorate` interface, the
`hubBuilderElectorate` adapter, `weightOfBuilder`, `tallyBuilderDomain`, and the
§4b linear scaling (round one); plus `ProvideDualModeTallyFn` /
`NewDualModeTallyFn` / `GovPhaseSource` / `tallyStandardDomain` (round two).
**`app/app.go` no longer supplies any `CalculateVoteResultsAndVotingPowerFn` to
x/gov.**

**On the attachment point**: the original design assumed a gov hook, but the
x/gov hook slot is already taken by depinject's `InvokeSetHooks` (it installs
MultiGovHooks during wiring, and a second `SetHooks` panics — confirmed
experimentally), so an **ante decorator** is used instead — which is anyway the
standard place to "reject at transaction admission". The check is a pure function
of the tx content, so CheckTx and DeliverTx agree. Note: this chain does not wire
x/authz, so `MsgSubmitProposal` is never nested inside `MsgExec`; if authz is
wired later, this scan must recurse into nested messages.

**What is still not done**: item 1 of the old §10 (whether the builder-domain
message list should grow) is still waiting on product. The current list is
`MsgSetModelStatus` / `MsgSetProfileStatus`, defined in
`builderDomainMsgTypeURLs`.

---

## 12. ADR-0018 alignment: Phase 0 has only one electorate

### 12.1 The facts: main landed Phase 0, and builder bond no longer exists

Verified after merging main:

| Fact | Location |
|---|---|
| `sdk.DefaultBondDenom = "ubond"` | `app/config.go:7` |
| `BuilderSetState.active_builders` is a plain address list with no bond field | `x/hub/types/builder.pb.go:620` |
| `BuilderStatus` is down to `ADMITTED` / `REVOKED` / `UNSPECIFIED` | `x/hub/types/participant_identity.go:326` |
| There is no `builder_bond` left in the hub params | `x/hub/types/params.go` |
| `hubtypes.BuilderStateSnapshot`, `BuilderSetTermForHeight` and `GetBuilderSetSnapshot` are all deleted | 5 compile errors after the merge |

In other words ADR-0018 Decision 6, "a Phase 0 Builder is a zero-bond
allowlist", is already implemented in code, and **a bond-weighted builder
electorate has no usable source of weight**. This is not an API change to adapt
to; the premise is gone.

### 12.2 Conclusion: drop the builder electorate, keep the domain routing

`tallyBuilderDomain` and all its supporting code are deleted. Current behaviour:

- **In Phase 0, builder-domain proposals are tallied by the standard electorate
  (the validators) and can pass.**
  This is exactly what ADR-0018 asks for — it states that Builders are not given
  governance voting power.
  Keeping bond weighting would mean zero bond → total electorate weight 0 →
  quorum can never be met → **a model could never be listed**.
- Domain classification and the submit-time mixed check are **kept**, because
  what they record is the rule "which operations belong to the builder domain",
  which is independent of whose weight does the tallying. When Phase 1 defines
  builder weights, the rule is already in place and auditable.

### 12.3 Even the phase gate went: the protocol forbids a custom tally

Round one kept a phase gate (reading hub `Phase0ParamsV1.native_token_enabled`
to give builder-domain proposals zero weight in Phase 1). It was deleted after
checking against monorepo `main@9b2c29c`, because **a custom tally is itself
forbidden** and the gate can only live inside a tally function:

| Source | Text |
|---|---|
| governance protocol §2 | the tally is Cosmos SDK v0.53.6 x/gov's, used unchanged; **no TrueOpen custom tally is added** |
| governance protocol §6 | and **uses the SDK default tally** |
| ADR-0018 Decision 3 | … **no TrueOpen custom tally is added** |
| ADR-0018 implementation boundary | x/gov, x/slashing, ubond and a closed ValidatorSet (**the SDK default tally**) |
| parameter table §8 | executed by the x/gov tally; **hub and task do not re-count votes** `[hard boundary]` |
| genesis protocol §6 | initialise the x/gov params and **verify the default tally** |

The deleted `tallyStandardDomain` was a **hand copy** of x/gov's unexported
implementation. Its behaviour was correct at the time, but that is precisely what
the rule guards against: when an SDK upgrade changes a denominator, the hand copy
does not follow, and no test would notice.

**What about Phase 1**: nothing. A Phase 1 builder electorate needs the protocol
side to define first whether builders hold bond again, where the weight comes
from, and how it is snapshotted; until then there is nothing for Node to
implement, and no reason to bury a switch that cannot be thrown. When it does
come, it should arrive as a **protocol upgrade** (governance protocol §9
explicitly lists "adding a governance action type" as requiring an upgrade),
rather than Node unilaterally flipping on a parameter read.

### 12.4 Instead, pin the three denominators of the SDK default tally

In the same paragraph that forbids a custom tally, governance protocol §2 gives a
MUST:

> the tests must cover the **abstain, jailed and unbond** boundaries, and must not
> write quorum, veto and the pass threshold against one shared denominator.

`app/gov_tally_test.go` is where that lands. SDK v0.53.6's three denominators
really are different (`x/gov/keeper/tally.go`):

```text
quorum    = totalVotingPower / TotalBondedTokens        :176
veto      = NoWithVeto / totalVotingPower               :189   includes abstain
threshold = Yes / (totalVotingPower − Abstain)          :204   excludes abstain
```

| Test | What it pins | Negative control |
|---|---|---|
| `TestTallyAbstainIsExcludedFromThresholdDenominator` | the proposal **passes** when yes = abstain, which is only possible if abstain is outside the threshold denominator | change the weights to 0.4/0.6 → FAIL |
| `TestTallyAbstainIsIncludedInVetoDenominator` | 30% veto does **not** trigger a veto, which only holds if abstain is inside the veto denominator | raise veto to 0.4 → FAIL |
| `TestTallyJailedValidatorLeavesNumeratorButStaysInQuorumDenominator` | jailing empties the numerator but does **not** shrink the quorum denominator | drop the `Jail` call → FAIL |
| `TestTallyUnbondingLeavesTheQuorumDenominator` | only unbonding truly leaves the quorum denominator | — |
| `TestTallyDenominatorsAreDistinct` | the three ratios differ from one another within one tally | change the expected value to 0.4 → FAIL |

The third one also turns the Phase 0 liveness risk named by ADR-0018 Decision 3
into an executable assertion: after a jail, voting power is zero but the `ubond`
is still in the denominator, until it unbonds.

Because this chain supplies **no** custom tally function, these tests exercise
the SDK's own implementation, which makes them an upgrade tripwire: an SDK
upgrade that moves a denominator fails here, rather than quietly changing
governance outcomes in production.

### 12.5 Phase 0 gov deposit policy: not this proposal's business

The deposit policy is settled by `noded genesis apply-seed`
(`cmd/noded/cmd/genesis_seed.go`, `applySDKGenesisParams`):

```go
governance.Params.MinDeposit          = <businessDenom>   // uusdc
governance.Params.BurnVoteQuorum             = false
governance.Params.BurnProposalDepositPrevote = false
governance.Params.BurnVoteVeto               = true
```

**`burn_vote_veto = true` is deliberate**: `GovernedGovBankKeeper.BurnCoins`
(`app/governance_bank_guard.go:27`) intercepts gov's burn and turns it into
`SendCoinsFromModuleToModule(gov → hub_treasury)`, calling
`CreditGovernanceDepositResidual` to record it. ADR-0018 Decision 3's "burning
the deposit is implemented as a transfer into trueopen_treasury" is thereby
implemented **literally**, and USDC is never burned.

So the `GOV_PHASE0=1` script switch this branch added earlier **has been
deleted**; both of its settings are harmful today:

| Old setting | Why it is harmful now |
|---|---|
| `burn_vote_veto = false` | Switches the Treasury residual path above off outright. |
| `proposal_cancel_dest = <treasury address>` | When `destAddress` is non-empty, x/gov's cancellation path takes `SendCoinsFromModuleToAccount` (SDK `x/gov/keeper/deposit.go:255`) and **bypasses** `BurnCoins`, so the treasury receives the money but the hub ledger records no residual. Leaving it empty instead takes the `BurnCoins` branch at `:243`, which the guard correctly takes over. |

### 12.6 Out of scope here

A Phase 1 builder electorate (source of weight, snapshot semantics, thresholds)
needs the protocol side to define first whether builders hold bond again in Phase
1. Until then there is nothing for Node to implement.
