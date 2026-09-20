# Builder registration · testing runbook

> ⚠️ **Do not use `--gas auto`**. Simulated transactions are signed with no sign
> mode set, and the Phase 0 account ante rejects them (`signer 0 uses an invalid
> signature mode`, `app/account_ante.go`). Give every transaction a fixed
> `--gas`, for example `--gas 900000`.

> **⚠️ Status (2026-09-17): the Builder bond lifecycle this document describes no
> longer exists.**
> After main landed ADR-0018 Phase 0, `MsgBondBuilder` /
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

> Location: `docs/runbooks/builder_registration_testing.md`
> Covers the `x/hub` Builder registration path: `register-builder` →
> `bond-builder` → entering the BuilderSet.
> Three layers: **L1 Go tests** (already covered) → **L2 genesis seeding** (when
> you just want builders that exist) → **L3 a real registration transaction on a
> live chain**.

---

## 0. The registration path at a glance

```
register-builder   writes identity + service key + descriptor   -> status = CANDIDATE
      |
      v
bond-builder       bonds into module escrow                     -> active_bond > 0, effective_term = the next term
      |
      v
BuilderSet snapshot  selected when the term comes due           -> status = ACTIVE, joins the electorate
```

Only `ACTIVE` counts as genuinely "registered and usable" (and only then does it
carry builder governance voting power, see
`docs/runbooks/governance_dualmode_testing.md`).

---

## L1 Go tests (already covered, free)

```bash
go test ./x/hub/keeper/ -run 'Builder' -v          # 30 cases
go test ./x/hub/keeper/ -run 'BuilderRegistrationAndBond|InitGenesisValidatesBuilderIdentityBond' -v
```

The cases closest to registration:
- `TestBuilderRegistrationAndBondEmitCommittedFacts` — the register + bond loop
  and the event contract
- `TestInitGenesisValidatesBuilderIdentityBondJoin` — consistency between
  identity and bond
- `TestBuilderRuntimeProjectionAndInvariantFailClosedOnBondDrift` — the invariant
  fails closed on bond drift

> This part is **Keeper-side business logic** (`x/hub`) and belongs to the Keeper
> owners.

---

## L2 genesis seeding (when you only want "a few usable builders")

Skip the registration messages and write builders straight into genesis, already
registered, already bonded, and already in the BuilderSet — with **the private
keys in your hands**:

```bash
./scripts/govtest_builder_localnet.sh
```

Good for downstream tests (builder governance voting, say); **not** suitable for
validating the registration flow itself.

---

## L3 a real registration transaction on a live chain

### 3.1 The hard part: service_key_proof

`register-builder` needs a **service-key proof of possession** — a secp256k1
signature over this canonical digest:

```
TRUEOPEN_SERVICE_REGISTRATION_V1 ‖ chain_id ‖ participant_type(BUILDER) ‖ operator address bytes ‖ service pubkey ‖ nonce(=1)
```

(See `RegisterBuilder` in `x/hub/keeper/msg_server_builder.go`.)
The CLI has no command to produce it and you cannot assemble it in shell, so the
repo ships a tool:

```bash
go run ./scripts/servicekeyproof --chain-id <chain-id> --operator <trueopen1...> --generate
```

It builds and signs the digest with **the same production functions the chain
verifies against**, and **self-verifies before printing**, so it never emits a
proof the chain would reject. The output carries `service_pubkey` /
`service_key_proof` / a usable `descriptor`, plus a ready-to-paste
`register-builder` command.

The tool has its own regression tests (`go test ./scripts/servicekeyproof/`):
- **golden digest**: pins the preimage, so any change to the domain constant or
  the framing fails and forces a human to look;
- **field-binding matrix**: changing chain_id / role / operator / service pubkey /
  nonce must each change the digest, so no field can silently fall out of the
  preimage and allow a cross-chain or cross-role replay;
- **a tampered proof, a wrong key, or the wrong chain must all fail to verify**.

> ⚠️ `--generate` **prints the service private key in the clear** — local and
> testnet only.
> With an existing service key, use `--service-privkey-hex <64 hex chars>`
> instead.
> For Cortex nodes, use `--participant cortex`.

### 3.2 The full flow

```bash
# 0) start a chain and create a funded operator account
HOME_DIR=/tmp/regtest RESET=1 START=0 FAST_BLOCKS=1 ./scripts/localnet_single_node.sh
build/noded start --home /tmp/regtest --minimum-gas-prices 0uusdc &
export ND="build/noded --home /tmp/regtest"
$ND keys add operator --keyring-backend test
OP=$($ND keys show operator -a --keyring-backend test)
$ND tx bank send validator "$OP" 5000000uusdc --keyring-backend test \
  --chain-id trueopen-localnet-1 --fees 0uusdc --gas 900000 -y

# 1) generate the service key and the proof
go run ./scripts/servicekeyproof --chain-id trueopen-localnet-1 --operator "$OP" --generate

# 2) register (fill in the pubkey / proof / descriptor printed by the previous step)
$ND tx hub register-builder "$OP" <service_pubkey> <service_key_proof> \
  --descriptor '{"endpoints":[{"endpoint_kind":"SERVICE_ENDPOINT_KIND_NEXUS_GRPC","protocol_version":"1","uri":"http://127.0.0.1:8080"}]}' \
  --from operator --keyring-backend test --chain-id trueopen-localnet-1 \
  --fees 0uusdc --gas 900000 -y

# 3) bond. Note that amount is a proto message, not a coin string like "1000000uusdc"
$ND tx hub bond-builder '{"atomic_units":"1000000"}' "$OP" \
  --from operator --keyring-backend test --chain-id trueopen-localnet-1 \
  --fees 0uusdc --gas 900000 -y

# 4) query the state (positional arguments, not --builder-address)
$ND query hub builder "$OP"
$ND query hub builder-bond "$OP"
```

### 3.3 Results observed in practice

The flow above has been run for real, and the chain **accepted the proof the tool
generated** (`code: 0`):

```
# after registering
builder_address:            trueopen1epde...
current_descriptor_version: "1"
current_service_pubkey:     AnQsng0pISO1CavpXuaycibb/YatASDCeWSIHSKBNxKz
service_key_status:         SERVICE_KEY_STATUS_ACTIVE
status:                     BUILDER_STATUS_CANDIDATE

# after bonding
active_bond:     "1000000"
effective_term:  "2"
status:          BUILDER_STATUS_CANDIDATE
```

### 3.4 Negative controls: a bad proof must be rejected (verified live)

Checking only that a good proof is accepted is not enough — if verification were
a no-op, the happy path would still pass. Both negative controls below have been
run on a live chain, and both were rejected by
`x/hub/keeper/identity_validation.go:84`:

| Negative control | How it is built | Result |
|---|---|---|
| Flip one byte | XOR byte 10 of the proof with 0x01 | Rejected: `signature does not verify against public key` |
| Bound to another chain | Generate the proof with `--chain-id WRONG-CHAIN`, then send it to this chain | Rejected (proving chain_id really is bound into the preimage) |
| Positive control | The tool's normal output | `code: 0` |

To reproduce (`$OP` is the funded operator):
```bash
PROOF=$(go run ./scripts/servicekeyproof --chain-id trueopen-localnet-1 --operator "$OP" --generate | grep '^service_key_proof' | awk '{print $3}')
BAD=$(python3 -c "b=bytearray.fromhex('$PROOF'); b[10]^=0x01; print(b.hex())")
$ND tx hub register-builder "$OP" <pubkey> "$BAD" --descriptor '...' --from operator ... # expected to fail
```

### 3.5 Cortex mode

Cortex registration (`stake-service`) uses **exactly the same framing**, with the
role enum swapped to `PARTICIPANT_TYPE_CORTEX` (see
`service_provider_runtime.go:88-95`). The tool takes `--participant cortex`:

```bash
go run ./scripts/servicekeyproof --chain-id <chain-id> --operator <addr> --participant cortex --generate
```
Verified: the same service key produces **different proofs** under the builder
and cortex roles, so the role really is bound and a proof for one role cannot be
replayed as the other.

> How far this is verified: cortex **proof generation** is verified (unit tests
> plus a real run of the tool); full cortex on-chain registration (building the
> `ServiceStakeActionV1` oneof for `stake-service`) has **not** been verified on
> a live chain.

### 3.6 On CANDIDATE → ACTIVE

After registering and bonding the status is **CANDIDATE**, and the bond's
`effective_term` is **the next term**. Becoming ACTIVE needs that term to come due
and a BuilderSet snapshot to be taken:

- `epoch_length_blocks` defaults to 60480, so term 2 starts at a very high height,
  and on a short-lived local chain `run-builder-term 2 ...` simply reports
  **`term 2 is not due`** (observed).
- With `INTEGRATION_PROFILE=1` the EndBlocker advances the integration BuilderSet
  by itself.
- To see ACTIVE quickly, **L2 genesis seeding** is the more direct route.

In other words: **L3 fully validates "registration and bonding", but is not the
way to validate "promotion to ACTIVE"** — that either waits for the term or goes
through L2.

---

## Common pitfalls

| Symptom | Cause |
|---|---|
| `invalid argument "1000000uusdc" for "--0" flag` | `bond-builder`'s amount is a proto message; use `'{"atomic_units":"1000000"}'` |
| `unknown flag: --builder-address` | `query hub builder/builder-bond` take **positional arguments** |
| `invalid service key proof-of-possession` | The proof does not match one of chain_id / operator address / service pubkey / nonce; regenerate with the tool, making sure `--chain-id` and `--operator` match the actual transaction |
| `term N is not due` | That term has not reached its start height; see 3.6 |
