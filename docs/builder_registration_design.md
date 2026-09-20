# Builder registration · design details

> **⚠️ Status (2026-09-17): the Builder bond lifecycle this document describes no
> longer exists.**
> After main landed Phase 0, `MsgBondBuilder` /
> `MsgBeginBuilderUnbonding` / `MsgWithdrawBuilderUnbonded` were all removed from
> `x/hub`, the CLI is down to `register-builder`, `BuilderStatus` is down to
> `ADMITTED` / `REVOKED`, and `BuilderSetState.active_builders` is a plain address
> list. A Phase 0 Builder is a **zero-bond allowlist**, added to and removed from
> by the governance action `ReplaceBuilderSetV1`.
>
> **Still valid**: `register-builder` and the service-key proof of possession
> (the `TRUEOPEN_SERVICE_REGISTRATION_V1` preimage framing is unchanged, and the
> golden digest test in `scripts/servicekeyproof` still passes).
> **No longer valid**: every section about bond / unbonding / `active_bond` /
> `effective_term`.
>
> Those sections describe Keeper-side behaviour, and rewriting them needs the
> `x/hub` owner's sign-off, so they are flagged here rather than rewritten.

> Location: `docs/builder_registration_design.md`
> Subject: the `x/hub` Builder admission path (identity registration → bonding →
> selection into the BuilderSet → exit/accountability).
> Audience: colleagues on the project, and management.
> Note: this document is **a reading of the current code**; `x/hub` is Keeper-side
> business logic.
> Every statement was checked against the code; line numbers are where things were
> at the time of writing and may drift with refactoring.
> The companion testing runbook is
> `docs/runbooks/builder_registration_testing.md`.

---

## 0. Executive summary

Before a Builder can actually start taking tasks, it has to pass **three
independent gates**:

1. **Identity**: prove it controls a service key, and register its service
   endpoints (`RegisterBuilder`)
2. **Capital**: post a bond into the on-chain escrow account (`BondBuilder`)
3. **Seat**: be selected into the BuilderSet at a term change (`ACTIVE`)

**Registered ≠ usable.** Keeping the three gates separate is the heart of this
design: identity can exist before capital, and capital that has arrived still
waits for the next term window to take effect. The point is to make manipulations
like "buy an identity at the last minute, pile on stake suddenly, grab a seat just
before the draw" impossible in time, not merely discouraged.

Exit is not instant either: after requesting unbonding, the Builder enters a
**slashable window** during which it can still be held to account, and only once
the window closes can it withdraw its principal.

---

## 1. State machine

```
                RegisterBuilder             BondBuilder          term election
   (none) ─────────────────────▶ CANDIDATE ───────────────▶ CANDIDATE ──────────▶ ACTIVE
                                identity ready            bond in escrow      joins the electorate
                                                                                    │
                          JAILED ◀────── fault count hits the threshold ────────────┤
                                                                                    │
                      TOMBSTONED ◀────── serious misbehaviour (unrecoverable) ──────┤
                                                                                    │
                         EXITING ◀────── BeginBuilderUnbonding ─────────────────────┘
                             │
                             └──── slashable window closes ────▶ WithdrawBuilderUnbonded
```

The six `BuilderStatus` states: `UNSPECIFIED(0) / CANDIDATE(1) / ACTIVE(2) /
JAILED(3) / TOMBSTONED(4) / EXITING(5)`.

> **Identity and bond each hold their own status** (`BuilderState.Status` and
> `BuilderBondState.Status`). The two must agree as a pair, which
> `ValidateBuilderIdentityBondPair` enforces on every write path; a mismatch fails
> closed with `ErrInvariantBroken` rather than picking one and carrying on.

---

## 2. The message surface (Msg)

**Builder lifecycle** (`x/hub/keeper/msg_server_builder.go`)

| Message | Purpose |
|---|---|
| `RegisterBuilder` | Writes the identity, the service key and the service descriptor |
| `BondBuilder` | Moves funds into module escrow and sets the effective term |
| `BeginBuilderUnbonding` | Starts unbonding: `EXITING` plus the slashable window |
| `WithdrawBuilderUnbonded` | Reclaims the principal once the window closes |
| `SubmitBuilderEvidence` | Submits objective fault evidence |
| `RunBuilderTerm` | Advances one term that has come due (with a per-call cap) |

**Service identity maintenance** (`msg_server_identity.go`)

| Message | Purpose |
|---|---|
| `RotateServiceKey` | Rotates the service key (the identity is unchanged) |
| `RevokeServiceKey` | Revokes the service key |
| `UpdateServiceDescriptor` | Updates the service endpoint descriptor |

**Governance-gated** (not self-service for a Builder; requires a passed
proposal): `SetModelStatus` / `SetProfileStatus` / `UpdateReferenceBucket` /
`UpdateTimeoutBucket` / `UpdateHubParams`.

---

## 3. The identity layer: service-key proof of possession

### 3.1 What the two keys are for

| Key | Role |
|---|---|
| **operator account key** | Signs the registration transaction, pays gas, owns the bond, and is the on-chain "owner" |
| **service key** | The online key used for later business signatures (accepting tasks, stage submissions, and so on) |

The two **must be different keys**. That way, if the online service key leaks, the
operator can rotate it with `RotateServiceKey` without migrating the identity or
the funds.

### 3.2 How the proof of possession is built

`RegisterBuilder` requires a `service_key_proof` showing that the submitter really
controls that service key (otherwise anyone could register someone else's public
key as their own). It is a signature over this **domain-separated canonical
digest**:

```
digest = SHA256( CanonicalFrame(
            "TRUEOPEN_SERVICE_REGISTRATION_V1",   // domain
            chain_id,                             // prevents cross-chain replay
            participant_type,                     // BUILDER / CORTEX, prevents cross-role replay
            operator address bytes,               // binds the owner
            service pubkey,                       // binds the key being proven
            nonce (fixed at 1 for registration)   // works together with key rotation
        ))
signature = secp256k1(digest)   // 64 bytes R‖S, strict low-S
```

The chain verifies it with `VerifyStrictSecp256k1Digest`
(`identity_validation.go`).

**Every field is genuinely bound**, not decorative: the field-binding matrix test
in `scripts/servicekeyproof` changes each of the five fields in turn and asserts
the digest must change; and it has been verified on a live chain that a proof
generated for another chain_id is rejected.

> ⚠️ A key implementation detail (the code comments call it "ruling 24"): the
> preimage uses the **codec-decoded address bytes**, not the bech32 text.
> Historically Builder used the text and Cortex used the bytes, which produced two
> different preimages within one domain; they are unified now. `participant_type`
> enters the preimage immediately after chain_id precisely so that Builder and
> Cortex proofs cannot be interchanged.

### 3.3 The descriptor hash is computed on-chain

Once the descriptor (the list of service endpoints) is submitted, **the chain
computes the hash itself** with `CanonicalServiceDescriptorHash(role, operator
bytes, version=1, endpoints, params)`. The `descriptor_hash` field the user
submits is legacy and ignored (the same goes for that field in the genesis seed).

The intent: make it impossible for a self-reported hash to disagree with the
content.

Endpoint kinds differ by role: Builder uses
`SERVICE_ENDPOINT_KIND_NEXUS_GRPC`, Cortex uses
`SERVICE_ENDPOINT_KIND_OBJECT_GATEWAY_HTTPS`.

### 3.4 Identity state after registration

The key `BuilderState` fields:

```
builder_address / current_service_address / current_service_pubkey
service_authorization_nonce      service-key authorisation sequence (incremented on rotation)
service_key_status               ACTIVE / REVOKED
current_descriptor_version       descriptor version
status                           BuilderStatus
registered_height                registration height
immunity_until_epoch             immunity period (see §7.3)
active_task_liability_count      liabilities for tasks in hand
pending_stage_duty_count         stage duties still to perform
pending_evidence_submission_count evidence submissions still owed
proposal_equivocation_fault_count / invalid_stage_submission_fault_count
objective_missed_duty_fault_count / data_unavailable_fault_count
fault_clear_progress / last_fault_height
```

Once registration completes the status is **`CANDIDATE`**, which cannot yet take
tasks.

---

## 4. The capital layer: bonding and deferred effect

### 4.1 The funds are really escrowed

`BondBuilder` moves the funds into the **`hub_builder_bond`** module account with
`SendCoinsFromAccountToModule`. This is a real bank transfer, not a bookkeeping
number, and that module account is on the app-level blocked list, so an ordinary
`MsgSend` cannot inject into it.

### 4.2 Effective two epochs later

```go
nextEffectiveTerm := currentEpoch + 2      // msg_server_builder.go
bond.EffectiveTerm = nextEffectiveTerm
```

Bonding **does not confer election eligibility immediately**; it counts only from
`currentEpoch + 2`.

The intent: make "piling on stake after seeing the draw" impossible in time — an
increase only affects the electorate two term cycles later.

### 4.3 The bond ledger

```
active_bond              the currently effective bond
pending_unbonding_total  the part being unbonded
pending_unbonding_id     the unbonding voucher (32 bytes)
total_slashed            total slashed historically
bond_version             version number (incremented on every change, prevents replay)
status                   paired with the identity status
last_bond_height / last_unbond_height
slashable_until_height   the height at which the slashable window closes
unbonding_height         the height unbonding was started at
```

---

## 5. The seat layer: BuilderSet election

### 5.1 The seven conditions for selection

`isBuilderEligibleForSet` requires **all** of the following
(`builder_runtime.go`):

| # | Condition |
|---|---|
| 1 | The identity and bond records agree as a pair |
| 2 | **The fault jail threshold has not been reached** |
| 3 | `BuilderState.Status` ∈ {CANDIDATE, ACTIVE} |
| 4 | `BuilderBondState.Status` ∈ {CANDIDATE, ACTIVE}, and **not EXITING** |
| 5 | `EffectiveTerm != 0` and `targetTerm >= EffectiveTerm` (the two-epoch delay from §4.2) |
| 6 | **`active_bond − pending_unbonding_total >= minBond`** |
| 7 | The identity is complete: `registered_height>0`, `descriptor_version>0`, `nonce>0`, service key `ACTIVE`, and a non-empty service address and pubkey |

Condition 6 is worth calling out on its own: **the portion requested for
unbonding is deducted from election eligibility immediately**, so nobody can
prepare to withdraw and keep holding a seat at the same time.

### 5.2 The election rule: sort by address bytes, then truncate to capacity

```go
sort.Slice(candidates, ...)                    // ascending by raw address bytes
if len(candidates) > capacity { candidates = candidates[:capacity] }   // capacity = builder_set_cap, default 15
if len(candidates) < buildersPerTask { return error }                  // at least enough for one task (default 3)
```

**The sort key is the raw address bytes — not bond size, and not a capability
score.** A dedicated regression test pins this
(`TestIntegrationBuilderSetUsesAddressBytesNotBondRanking`). The implication:
**under the current implementation, bonding more does not improve your chance of
selection**; bond is only an eligibility threshold (condition 6), never a sort
weight.

The snapshot it produces, `BuilderSetSnapshot`: the term number, start and end
heights, the member list (typed `[]string`), `set_hash` (raw 32 bytes),
`active_builder_count`, `body_status` and `snapshot_height`. The identity and bond
statuses of those selected are promoted to `ACTIVE` along with it.

### 5.3 A rotation policy that is declared but not implemented

`BuilderParamsV1` carries three parameters that **currently exist only in the
defaults, the parameter validation and the parameter hash — the election logic
does not use them**:

| Parameter | Default | Current state |
|---|---|---|
| `rotation_out_ratio_ppm` | 200_000 (20%) | **Not read by the election logic** |
| `min_overlap_ratio_ppm` | 600_000 (60%) | **Not read by the election logic** |
| `immunity_epochs` | 1 | Enters the parameter hash only; no behaviour |

In other words, the policy of "rotate 20% out each term, with at least 60% overlap
between consecutive terms" **is not in effect**; the actual behaviour is §5.2's
"sort by address and take the first N". This is a gap to settle with the Keeper
side: is it left for later implementation, or should the parameters be withdrawn
first so they do not mislead?

### 5.4 What triggers a term change

- `RunBuilderTerm(target_term, max_items, submitter)` — manually advances a term
  that **has come due**; one that has not reached its start height returns
  `term N is not due`.
- Under the integration profile (`INTEGRATION_PROFILE=1`), the EndBlocker advances
  the integration BuilderSet by itself.
- The work done per block is capped by `max_builder_term_items_per_block`, which
  keeps a single block's workload bounded.

---

## 6. Exit: unbonding and withdrawal

### 6.1 Starting to unbond

`BeginBuilderUnbonding`:
- Rejects a duplicate unbonding ("builder already has an open unbonding")
- Rejects an amount larger than `active_bond`
- Sets both the identity and the bond to **`EXITING`**
- Computes `slashable_until_height` (see 6.2)
- Increments `bond_version` and generates a 32-byte `pending_unbonding_id` as the
  withdrawal voucher

### 6.2 The slashable window

`builderDutySlashableUntil` derives the end of the window from the duties that
Builder holds **within its term**, covering the assignment and open-verify
proposal windows and the settlement grace period
(`assignment_builder_proposal_window_blocks`,
`open_verify_builder_proposal_window_blocks`,
`settlement_builder_grace_blocks`).

The intent: **you cannot escape liabilities you have already incurred by taking a
task and immediately withdrawing**. JAILED / EXITING members still inside some
term's coverage must see the window out as well.

### 6.3 Withdrawal

`WithdrawBuilderUnbonded` requires **exactly one** locator:
- `by_id`: the 32-byte `unbonding_id`, which must match `pending_unbonding_id`
- or batch mode (subject to the per-call cap)

With nothing pending it returns `MUTATION_STATUS_V1_NOOP` (idempotent, not an
error).

---

## 7. Accountability: faults, jail and immunity

### 7.1 The four fault counters

`proposal_equivocation`, `invalid_stage_submission`, `objective_missed_duty`, and
`data_unavailable`.

### 7.2 The jail decision

```go
invalidSubmissionCount = proposal_equivocation + invalid_stage_submission
jailed = invalidSubmissionCount >= builder_invalid_submission_jail_threshold
      || objective_missed_duty   >= builder_objective_missed_duty_jail_threshold
```
Reaching either threshold meets the jail condition, and immediately costs the
Builder its BuilderSet eligibility (§5.1 condition 2). A threshold parameter of 0
is treated as an invalid parameter (fail-closed, rather than "never jail").

### 7.3 Clearing: all counters reset at once after N clean terms of service

Clearing is **all or nothing**, not a decrement per item (`builder_runtime.go`):

```
if this Builder still has uncleared faults (any counter non-zero, or it is JAILED):
    fault_clear_progress += 1
    if fault_clear_progress < builder_fault_clear_count:   only the progress is saved; the faults remain
    else:
        all four fault counters are zeroed
        fault_clear_progress is reset
        if it was JAILED -> restored to CANDIDATE
            (but if active_bond is fully covered by an unbonding request, it becomes EXITING)
```

The points that matter:
- It takes **`builder_fault_clear_count` clean terms in a row** (default 1000)
  before all faults clear in one go; there is no partial relief along the way.
- **JAILED is recoverable**: after clearing it returns to `CANDIDATE` and must
  wait for another term change to become `ACTIVE` again. This contrasts with
  `TOMBSTONED` (see §10.3).
- If a full unbonding was requested while jailed, recovery goes straight to
  `EXITING` rather than `CANDIDATE`.

The `immunity_until_epoch` field is in the state, but as §5.3 notes the
`immunity_epochs` parameter currently drives no behaviour. Fault records are
retained per `builder_fault_retention_blocks` and pruned in chunks.

---

## 8. The two paths onto the chain

| Path | Scenario | Proof needed | Landing state |
|---|---|---|---|
| **Transaction registration** | Real operations | Yes (generated by `scripts/servicekeyproof`) | `CANDIDATE`, waiting for a term change |
| **Genesis seeding** | Local / test networks | No | Straight to `ACTIVE`, already in the term 1 snapshot |

The genesis path is handled by `noded genesis apply-seed`, which also funds the
builder accounts (`account_balance` must exceed their `bond`) and produces the
term 1 BuilderSet snapshot. `scripts/govtest_builder_localnet.sh` builds on this
to produce ACTIVE builders **whose private keys you control**.

---

## 9. Design trade-offs in brief

| Trade-off | Choice | Cost |
|---|---|---|
| Identity / capital / seat | **Three separate layers** | Cannot work immediately after registering; operators must understand three states |
| When a bond takes effect | **Deferred two epochs** | New Builders come online slowly, but last-minute piling-on is ruled out |
| Election ordering | **Address byte order** (not bond-weighted) | Simple and whale-resistant; but bonding cannot express "I want in more" |
| Unbonding | **With a slashable window** | Exit is slow, but liability cannot be escaped |
| Descriptor hash | **Computed on-chain** | Users cannot customise it, but a self-reported hash can never disagree |
| Service key | **Separate from the operator, rotatable** | One more key to manage, in exchange for a rotatable online key |

## 10. Open items

1. **The three rotation parameters in §5.3 are unimplemented**
   (`rotation_out_ratio_ppm` / `min_overlap_ratio_ppm` / `immunity_epochs`) — are
   they pending implementation, or should they be withdrawn first? This shapes
   what outsiders expect the election policy to be.
2. Is **bonding not affecting selection priority** the final intent? Wanting "bond
   more, get more seats" would require changing the sort rule.
3. The conditions for entering `TOMBSTONED` are not covered here — the
   registration path has no direct write point for it, so it must live in the
   punishment/evidence paths. The contrast that is confirmed: **`JAILED` can
   recover to `CANDIDATE` through the clearing mechanism in §7.3**, whereas
   `TOMBSTONED` is permanently excluded from election in
   `isBuilderEligibleForSet`.
