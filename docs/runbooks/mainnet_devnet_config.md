# Mainnet / Devnet / Localnet Configuration

How the three chain environments should each deviate from the defaults under
`~/.node/config/`, and which dev-only behaviour must be off on mainnet.

## The three environments

| Environment | chain-id shape | Audience | Dev-only features | Guarantee |
|---|---|---|---|---|
| mainnet | `trueopen-mainnet-1` | Real users and real funds | None | Determinism, no payload leakage, no developer backdoor |
| devnet | `trueopen-devnet-*` | Partners, SDK, cortex and nexus integration | Some, documented below | Stable operation, reproducible bugs, developer inspection allowed |
| localnet | `trueopen-localnet-*` | One machine, CI, demos | All | Start, stop and reset in one command |

The risk this table exists for: if a mainnet forgets to disable the dev-only
beacon source (`placeholder_blockhash_v1`), consensus randomness becomes a
function of the header hash, and any rational proposer can influence sampling by
choosing its payload. That is why the freeze checklist carries it.

## Required overrides

`noded init <moniker> --chain-id <id>` writes
`~/.node/config/{app.toml,config.toml,client.toml,genesis.json}` with
development-oriented defaults. Override the following per environment.

### Mainnet — `app.toml`

```toml
# Fees. min-gas-prices is the anti-spam floor and must be non-empty. It uses the
# canonical business denomination frozen in genesis.
minimum-gas-prices = "0.005uusdc"

# Beacon role assertion. Validators keep this true and provision
# config/vrf_key.json. RPC, seed and plain full nodes running the same binary
# must set it to false explicitly.
[beacon]
vrf-key-required = true

# Exposed surfaces.
[api]
enable = true
address = "tcp://0.0.0.0:1317"
enabled-unsafe-cors = false   # never wildcard CORS on mainnet
swagger = false               # off on mainnet to reduce attack surface

[grpc]
enable = true
address = "0.0.0.0:9090"

[grpc-web]
enable = true

# Retention; see the pruning section below.
pruning = "custom"
pruning-keep-recent = "362880"   # roughly 21 days at a 10s block
pruning-interval = "100"

[telemetry]
enabled = true
enable-hostname = false          # do not report hostname from mainnet
enable-hostname-label = false
enable-service-label = false
prometheus-retention-time = 3600

[state-sync]
snapshot-interval = 1000
snapshot-keep-recent = 2
```

### Mainnet — `config.toml` (CometBFT)

```toml
[consensus]
# The exact values are announced by the lead maintainer before genesis freeze.
timeout_propose = "3s"
timeout_prevote = "1s"
timeout_precommit = "1s"
timeout_commit = "5s"            # about a 10s block time

[p2p]
laddr = "tcp://0.0.0.0:26656"
seeds = "<seed_1@ip:port>,<seed_2@ip:port>"
persistent_peers = ""            # configured per validator
max_num_inbound_peers = 40
max_num_outbound_peers = 10
addr_book_strict = true          # on for mainnet, so a forged address book is rejected

[mempool]
size = 5000
max_txs_bytes = 1073741824
recheck = true
broadcast = true
```

### Devnet — differences from mainnet

```toml
# app.toml
minimum-gas-prices = "0uusdc"    # zero fee, so load tests are cheap

[api]
enabled-unsafe-cors = true       # browser SDK integrators call it directly
swagger = true                   # kept for exploration

[telemetry]
enable-hostname = true           # hostname is useful for devnet monitoring

# config.toml
[consensus]
timeout_commit = "2s"            # faster blocks while debugging

[p2p]
addr_book_strict = false         # allows gossip between development machines
```

### Localnet

```toml
# app.toml
minimum-gas-prices = "0uusdc"

[api]
enable = true
enabled-unsafe-cors = true
swagger = true

# config.toml
[consensus]
timeout_commit = "1s"

[instrumentation]
prometheus = false               # a single CI machine does not need metrics
```

`cmd/noded/cmd/testnet_multi_node.go` embeds its own minimum gas price so a
localnet starts in one command; a long-lived localnet should still override it
by hand.

## Dev-only features and how mainnet disables them

| Feature | State | Where | How mainnet disables it |
|---|---|---|---|
| `placeholder_blockhash_v1` beacon source | Permitted only by the committed `vrf_required_from_height`; a build tag does not change the consensus decision | `x/hub/keeper/beacon_runtime.go`, `app/beacon_wiring.go`, `app/beacon_wiring_mainnet.go` | Production genesis fixes `required = 1`. Validators keep `[beacon] vrf-key-required = true`; non-producing nodes set it false explicitly |
| Relaxed signature gate | Does not exist | Strict service signature verification | No configuration may be added that skips verification |
| `unsafe-reset-all` CLI | Available by default | Inherited from the SDK server commands | Deny it in the mainnet systemd unit's command allowlist |
| `enabled-unsafe-cors` | Off by default | `app.toml` | Keep `false` |
| API `swagger` | Off by default | `app.toml` | Keep `false` |
| P2P `addr_book_strict` | On by default | CometBFT | Keep `true` |
| Large dev pre-allocations in genesis | Per genesis | Operator-owned | Mainnet genesis audit |

A new dev-only feature must do all three of: gate it in source behind a build
tag or a configuration switch; add a row to the table above; and record how it
is disabled, plus the verification result, here and in the release checklist.

## Frozen identity

Taken from `app/config.go` and `x/shared/types/keys.go`. These are identical in
all three environments, and any change goes through the breaking-change
template.

```text
AccountAddressPrefix = "trueopen"
business_denom       = "uusdc"   // canonical business denomination, genesis-only
consensus_bond_denom = "ubond"   // never enters the business funds domain
```

- mainnet: the chain-id contains `mainnet`, for example `trueopen-mainnet-1`.
- devnet: the chain-id contains `devnet`, for example `trueopen-devnet-2026-07`.
- localnet: prefer `trueopen-localnet-<username>`, so a localnet cannot
  accidentally peer with a devnet.

## State retention

| Environment | Strategy | keep-recent | Why |
|---|---|---|---|
| mainnet | `custom` | ~21 days (362880 blocks) | Indexer backfill and governance audit |
| devnet | `custom` | ~7 days (120960 blocks) | Bounded disk cost |
| localnet | `nothing` | unbounded | Single machine; full history is cheap |

## Start commands

```bash
# mainnet
noded start --home ~/.node --x-crisis-skip-assert-invariants=false

# devnet
noded start --home ~/.node --api.enable --grpc.enable

# localnet, single node
noded start --home ~/.node --api.enable --grpc.enable --minimum-gas-prices=0uusdc

# localnet, multiple nodes
noded testnet init-files --v 3 --chain-id trueopen-localnet-1 --output-dir ./mytestnet
```

- Keep `--x-crisis-skip-assert-invariants=false` on mainnet: invariants are
  asserted every block. It is slower, and it is what keeps state sound.
- `--api.enable` and `--grpc.enable` belong in `app.toml`; the flags are for
  testing.
- Override `--minimum-gas-prices` on the command line only for localnet and
  devnet. On mainnet `app.toml` must be its single definition, so it cannot be
  forgotten during an operational change.

## Genesis differences

Beyond `chain_id`, these genesis fields may differ per environment:

| Field | mainnet | devnet | localnet |
|---|---|---|---|
| Duration parameters in `hub.params.*` / `task.params.*` | Frozen specification values | May be shortened | Same as devnet |
| Genesis validator count | At least 4 | 1-3 | 1 |
| Pre-allocated balances | Only announced team and treasury accounts | Test faucet allowed | Development keys allowed |
| `consensus_version` of each module | Matches the module's `ConsensusVersion()` | Same | Same |

A mainnet genesis is signed off by the lead maintainer, the node maintainer and
security. Devnet and localnet genesis files are self-signed by their operator.

## Related

- `upgrade_reset_live_migration.md` — switching environments and handling
  breaking changes.
- `build_and_test.md` — building on each platform.
- `../templates/breaking_change_migration.md` — the template required for a PR
  that changes any frozen field above.
- `cmd/noded/cmd/config.go` — where to move an override if it should become a
  built-in default.
