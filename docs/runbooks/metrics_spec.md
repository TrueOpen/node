# Metrics

What a TrueOpen node exposes, how to turn it on per environment, and the rules a
new metric has to follow.

**Metrics never affect consensus state.** Prometheus, Grafana and any log
aggregator are read-only observers. A metric computation that divides by zero,
overflows or fails to resolve a hostname must not crash block production, and
must not reach consensus state through a read or a write during `DeliverTx`.

## Layers

```
L1  CometBFT built-in        consensus, p2p, mempool
L2  Cosmos SDK built-in      tx, gas, bank, staking
L3  x/hub and x/task         assignment, verification, settlement
L4  Operational              host, disk, log rate
```

L1 and L2 come from CometBFT and the SDK and only need to be enabled. L3 is
specific to this chain. L4 comes from standard tooling such as systemd and
`node_exporter`.

## L1 — CometBFT

Exposed on `http://127.0.0.1:26660/metrics` when instrumentation is enabled.

| Metric | Type | Meaning | Used for |
|---|---|---|---|
| `cometbft_consensus_height` | gauge | Current consensus height | Liveness, lag detection |
| `cometbft_consensus_num_txs` | gauge | Transactions in the current block | Throughput |
| `cometbft_consensus_block_size_bytes` | gauge | Block size | Capacity planning |
| `cometbft_consensus_block_interval_seconds` | histogram | Time between blocks | Consensus health |
| `cometbft_consensus_missing_validators` | gauge | Absent validators this round | Validator downtime |
| `cometbft_consensus_byzantine_validators` | gauge | Byzantine validators | Early slashing warning |
| `cometbft_p2p_peers` | gauge | Connected peers | Partition detection |
| `cometbft_mempool_size` | gauge | Unbatched transactions | Mempool pressure |
| `cometbft_mempool_tx_size_bytes` | histogram | Transaction size distribution | Anomalous transactions |
| `cometbft_state_block_processing_time` | histogram | `FinalizeBlock` duration | Application performance |

```toml
# config.toml
[instrumentation]
prometheus = true
prometheus_listen_addr = ":26660"
max_open_connections = 3
namespace = "cometbft"
```

Enabled on mainnet, listening on an internal interface. Disabled on localnet CI
to avoid port conflicts.

## L2 — Cosmos SDK

Enabled through `[telemetry]` in `app.toml` and served alongside the API server.

| Metric | Type | Meaning |
|---|---|---|
| `tx_count` | counter | Transactions delivered |
| `tx_successful` / `tx_failed` | counter | Split by outcome |
| `gas_used` / `gas_wanted` | histogram | Gas distribution |
| `store_iavl_get` / `store_iavl_set` | histogram | IAVL operation latency |
| `store_iavl_size` | gauge | Total keys in the store |
| `bank_send` | counter | Bank sends |
| `staking_bonded_tokens` | gauge | Total bonded tokens |

```toml
[telemetry]
enabled = true
enable-hostname = false           # mainnet must not leak hostnames
enable-hostname-label = false
enable-service-label = false
prometheus-retention-time = 3600  # seconds
global-labels = [["chain_id", "trueopen-mainnet-1"]]
```

`enable-hostname = false` is mandatory on mainnet.

## L3 — Chain modules

The hub and task modules currently register no metrics of their own: everything
observable is published as a typed event, with the attribute keys frozen by each
module's event allowlist test. When a metric is genuinely needed, it follows the
rules below.

### Naming

`<module>_<domain>_<action>_<unit>`, lower case with underscores, matching the
CometBFT and SDK style. A module-specific metric takes its module's name as the
prefix — `hub_` or `task_`. An app or consensus level observation that belongs
to no module, such as the ante handler or proposal processing, uses the `trueopen_`
product prefix.

Domains are a closed set. Task side: `session`, `task`, `assign`, `verify`,
`commit`, `reveal`, `settle`, `challenge`, `evidence`. Hub side: `earnings`,
`builder`, `reward`, `beacon`, `treasury`. Bank goes through the SDK built-ins.
Adding a domain is a breaking change.

Action suffixes carry the type: `_total` is a monotonic counter that does not
reset on restart, `_gauge` is an instantaneous value, and `_seconds`, `_bytes`
or `_count` are distributions.

### Proposed metrics

Implement these as written if and when they are needed:

| Metric | Type | Emitted at | Used for |
|---|---|---|---|
| `task_session_created_total` | counter | After a successful `CreateSession` | User growth |
| `task_task_assigned_total{status}` | counter, `ok`/`fallback`/`expired` | Settlement classification | Task success rate |
| `task_task_verify_deadline_swept_total` | counter | EndBlocker deadline sweep | Congestion |
| `task_task_assignment_randomness_pending_gauge` | gauge | BeginBlocker candidate walk | Undetermined task backlog |
| `hub_earnings_claimed_amount_bytes` | histogram | After a successful claim | Claim size distribution |
| `hub_builder_bond_slashed_total` | counter | Bond slash to treasury | Slash frequency |
| `hub_reward_epoch_finalized_total{bucket}` | counter | Reward epoch finalisation | Reward pipeline health |
| `hub_beacon_verify_seconds` | histogram | Beacon verification in ProcessProposal | VRF verification latency |
| `hub_beacon_verify_failed_total{reason}` | counter, `proof`/`pubkey`/`duplicate`/`height` | Beacon validation failure branches | Early attack or bug warning |
| `trueopen_ante_order_value_priority_gauge` | histogram | Before the ante handler sets priority | Priority distribution |
| `trueopen_process_proposal_reject_total{reason}` | counter | ProcessProposal reject branches | Proposer violation rate |

### Emission rules

These exist so a metric cannot influence consensus:

- Wrap every Prometheus call in `defer func() { _ = recover() }()`. A panic in
  the client library must not escape into the handler.
- Emit only from committed values: write state first, then the metric. The
  reverse makes the metric overstate reality.
- Keep label cardinality bounded, under 100 distinct values. Labels like
  `{reason}` and `{bucket}` are fine; `{address}` and `{session_id}` are
  forbidden.
- Never emit from `CheckTx` or the mempool path. Counting transactions that
  never reach a block distorts every statistic derived from it.
- Emit at the end of a `DeliverTx` handler, after it has decided to succeed. An
  error branch either emits nothing or emits its `_failed_total` classification.

## L4 — Operational

Collected by `prometheus-node-exporter` and a log shipper, independent of the
node binary.

| Metric | Meaning |
|---|---|
| `node_filesystem_avail_bytes` | Free space on the volume holding `~/.node/data` |
| `node_load1` / `node_load5` | CPU load |
| `node_memory_MemAvailable_bytes` | Available memory |
| `noded_process_cpu_seconds_total` | Per-process CPU, via `process_exporter` |
| `noded_process_open_fds` | Open file descriptors |
| `noded_log_lines_per_second{level}` | From the log shipper |

Set `LimitNOFILE=65536` in the systemd unit, and ship logs from
`journalctl -u noded`.

## Dashboard

Eight panels, matching the alerts in the daily operations runbook:

1. Block height — `cometbft_consensus_height`, with a 60s derivative.
2. Peers — `cometbft_p2p_peers`, with a threshold line.
3. Mempool — `cometbft_mempool_size`.
4. Block time — `cometbft_consensus_block_interval_seconds` as a heatmap.
5. Transaction success rate — `rate(tx_successful[5m]) / rate(tx_count[5m])`.
6. Task outcome — `increase(task_task_assigned_total[1h])` stacked by status.
7. Reward flow — `hub_earnings_claimed_amount_bytes_sum` and
   `hub_builder_bond_slashed_total`.
8. Beacon health — `hub_beacon_verify_seconds` p50/p95 and
   `hub_beacon_verify_failed_total`.

## Per environment

| Environment | Prometheus | node_exporter | `telemetry.enable-hostname` |
|---|---|---|---|
| mainnet | On, internal interface | On | Must be false |
| devnet | On | On | Optional |
| localnet | Off by default | Off | Not applicable |

## Frozen

Changing any of these is a breaking change:

- The L3 domain set.
- The naming convention `<module>_<domain>_<action>_<unit>`, including which
  prefix applies.
- The emission rules above.
- `telemetry.enable-hostname = false` on mainnet.

Not frozen: the specific metric names in the proposed table, dashboard queries,
and alert thresholds.

## Related

- `operator_daily_ops.md` — alerts and first-day setup.
- `mainnet_devnet_config.md` — per-environment telemetry settings.
- `upgrade_reset_live_migration.md` — metric gaps across an upgrade.
- Each module's event allowlist test — the frozen event attribute names, which
  share their vocabulary with the L3 domains.
- [Cosmos SDK telemetry](https://docs.cosmos.network/main/build/tooling/telemetry)
- [CometBFT metrics](https://docs.cometbft.com/main/explanation/core/metrics)
- [Prometheus naming practices](https://prometheus.io/docs/practices/naming/)
