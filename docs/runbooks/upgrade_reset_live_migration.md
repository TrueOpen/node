# Upgrade, Reset and Live Migration

Operational procedures for the three ways this chain evolves. It complements the
breaking-change template: the template is the developer's view of a change, this
is the operator's view of landing it.

## Choosing a path

| Situation | Mainnet live | Halt | State kept | Path |
|---|---|---|---|---|
| Pre-mainnet chain-id or genesis change | No | n/a | None | Reset |
| Parameter change by governance | Yes | No | All | Parameter upgrade |
| Store schema change | Yes | Yes | Migrated | Regular upgrade |
| Incident response (funds, consensus fork) | Yes | As short as possible | Migrated | Live migration |

Reset applies only before mainnet. Once mainnet is live, the question is whether
the change fits into a single upgrade plan: if it does, it is a regular halted
upgrade; if it cannot wait for one, it is a live migration.

## Reset (pre-mainnet only)

### Preconditions

- The chain is a testnet or devnet.
- A reset announcement has been published through the breaking-change template,
  covering the new chain-id and any dependency field changes.
- Both the node and keeper maintainers have signed off.

### On each validator

```bash
# 0. Stop
systemctl stop noded

# 1. Back up the old data (optional; a reset may discard it)
tar -czf ~/noded-backup-$(date +%Y%m%d-%H%M).tar.gz ~/.node/data ~/.node/config

# 2. Clear the chain
noded tendermint unsafe-reset-all --keep-addr-book
rm -rf ~/.node/data

# 3. Re-initialise with the coordinated chain-id
noded init "<moniker>" --chain-id "<new-chain-id>"

# 4. Fetch the published genesis
curl -o ~/.node/config/genesis.json <NEW_GENESIS_URL>

# 5. Compare its hash against the announcement, byte for byte
sha256sum ~/.node/config/genesis.json

# 6. Validate it
noded genesis validate

# 7. Start
systemctl start noded

# 8. Confirm blocks are being produced
noded status | jq '.SyncInfo.latest_block_height'
```

### Post-check

| Check | Command | Expected |
|---|---|---|
| chain-id | `noded status \| jq '.NodeInfo.network'` | Matches the announcement |
| Genesis hash | `sha256sum ~/.node/config/genesis.json` | Matches the announcement |
| Parameters | `noded query hub params`, `noded query task params` | Match the new defaults |
| Module accounts | `noded query auth module-account <name>` for each | All present |
| Block production | `noded status \| jq '.SyncInfo.latest_block_height'` | Increasing |

### Rollback

There is no on-chain rollback. If the reset is wrong: stop, restore the backup
tarball, and coordinate either a return to the old chain-id or a further reset.

## Regular upgrade

For a planned breaking change: a new store schema, tightened parameters, a new
module account. Governance passes a plan, the chain halts at the named height,
operators swap the binary, and the migration handler runs at start-up.

### Parameter-only change

If only parameters change — no schema, no wire format, no store key — an
ordinary governance proposal is enough and there is no halt:

```bash
noded tx gov submit-proposal <proposal.json> --from <account> --gas auto
noded tx gov vote <proposal-id> yes --from <account>

# Takes effect on passing; no restart
noded query hub params
noded query task params
```

### Halted upgrade

#### Before the halt

| When | Action | Owner |
|---|---|---|
| T-14d | Publish the upgrade proposal | Lead maintainer |
| T-14d | Announce the halt height, the new binary and the migration semantics | Lead maintainer |
| T-7d | Fetch the new binary and dry-run it locally with `--halt-height` | Each validator |
| T-3d | Take and archive a snapshot | Each validator |
| T-1d | Confirm the proposal reached quorum | Lead maintainer |
| T-0 | The chain halts at the planned height | Automatic |

#### At the halt

```bash
# 1. The log announces the upgrade
journalctl -u noded --since "1 hour ago" | grep UPGRADE

# 2. Stop
systemctl stop noded

# 3. Swap the binary, keeping the old one
mv $(which noded) $(which noded).old
install <new-noded> $(which noded)
noded version

# 4. Snapshot before migrating
tar -czf ~/noded-pre-upgrade-<name>-$(date +%Y%m%d-%H%M).tar.gz ~/.node/data

# 5. Start; the new binary detects the upgrade and runs its migration handler
systemctl start noded

# 6. Watch the migration complete
journalctl -u noded -f | grep -E "migration|upgrade|hub|task"
```

#### After the upgrade

| Check | Command | Expected |
|---|---|---|
| Module versions | `noded query upgrade module_versions` | `hub` and `task` match their new consensus versions |
| Collection counts | Compare pre and post counts per collection | Only the additions and removals the migration documented |
| Escrow | `noded query bank balance <task_escrow_addr> uusdc` | At or below the pre-upgrade value; settlement may release funds |
| Total supply | `noded query bank total-supply` | Unchanged, unless the migration explicitly mints or burns |
| Hook order | Inspect the upgrade plan | Unchanged |
| Block production | `noded status \| jq '.SyncInfo.latest_block_height'` | Increasing |

#### Rollback

Only inside the downgrade window declared in the proposal:

```bash
systemctl stop noded
install $(which noded).old $(which noded)
rm -rf ~/.node/data
tar -xzf ~/noded-pre-upgrade-<name>-*.tar.gz -C ~/
systemctl start noded
```

Past the downgrade window there is no rollback: the rest of the network has
already forked onto the new chain, and the only way out is a forward fix through
the emergency path.

## Live migration

For a mainnet incident — misdirected funds, a consensus fork, a payload
disclosure, large-scale message abuse — where the fix cannot wait for a normal
upgrade cycle. It differs from a regular upgrade in three ways: the halt window
is at most three hours, in-flight state conversion is permitted, and it may be
combined with temporarily freezing a message type.

### Preconditions

All of:

- The incident is signed off by the lead maintainer and security.
- The fix is merged and tagged.
- An emergency proposal has reached the emergency threshold.
- A postmortem owner is named.

### Procedure

Optionally freeze the implicated message type first:

```bash
noded tx gov submit-proposal <freeze-msg.json> --from <account>
noded tx gov vote <proposal-id> yes --from <account>
```

Then deploy as for a halted upgrade, with a mandatory pre-halt snapshot:

```bash
systemctl stop noded
install <emergency-noded> $(which noded)
tar -czf ~/noded-pre-emergency-$(date +%Y%m%d-%H%M).tar.gz ~/.node/data
systemctl start noded
```

An emergency migration handler must expose a read-only query that lets an
operator confirm the outcome:

| Check | Expected |
|---|---|
| Affected accounts | Match the list the postmortem declared |
| Escrow balance | Matches the final value the proposal declared |
| Rewards and treasury balances | Unchanged from the proposal's statement |
| Frozen message | Broadcasting it returns the disabled-message error |

Publish the postmortem within seven days: the timeline, the state diff by
address and denomination, an audit of the rollback and roll-forward paths, and
the preventive measures.

### Rollback

Effectively none. The downgrade window is zero, because the pre-migration state
is itself the harm. Abandoning an emergency migration requires a second
emergency proposal that moves the state somewhere else; a snapshot rollback is
not an option because it would replay the incident.

## Common checks

Before any path:

- [ ] The new binary has replayed the same pre-upgrade snapshot on a
      three-validator devnet, deterministically
- [ ] Genesis hash, upgrade name and height are announced
- [ ] Snapshots archived (except for a reset, which may discard history)
- [ ] `docs/static/openapi.json` regenerated if the surface changed
- [ ] Downstream consumers notified

After any path:

- [ ] Blocks are being produced
- [ ] Parameters match the new specification
- [ ] Total supply holds
- [ ] Every module account exists with an explainable balance
- [ ] Hook order unchanged
- [ ] Module versions updated (upgrade path only)
- [ ] No unexpected alerts on the dashboards

## What the implementation currently guarantees

- Hub and task both export parameters, parameter metadata, business primaries,
  cursors and terminal summaries.
- `InitGenesis` rebuilds derived indexes and refcounts and runs the cross-module
  invariants in both directions.
- Export writes the chain id; continuation resumes at export height plus one.
- The store schema is a fresh V1 at version 1; no older debug data is migrated.
- Module account balances, settlement and finality state, rewards, beacon state
  and cleanup state all have export, init and restart coverage.
- Any future schema bump must ship an explicit migration handler or an explicit
  reset decision. A tolerant decoder is not an acceptable substitute.

## Division of responsibility

The keeper modules own the data transformation inside a migration handler and
the business logic inside an emergency handler.

This repository's operational surface owns upgrade handler registration and
binary release, the start and stop ordering, the pre- and post-checks, the
downgrade semantics, and the replay verification in CI and on localnet.

Both sides touch the module consensus version, the migration function
registration, and parameter defaults and clamps.

## Known limitations

- There is no built-in snapshot restore; operators archive and restore
  `~/.node/data` themselves.
- The downgrade window is not yet a governance parameter; each proposal states
  it explicitly.
- "Zero downtime" for a live migration is an upper-bound goal. In practice a
  block-time gap of a few minutes still occurs.
- Until a schema version is exposed on-chain, confirming that a node upgraded
  relies on `noded version`, not on chain state.

## Related

- `../templates/breaking_change_migration.md`
- `release_artifacts.md` — producing and verifying the new binary
- `mainnet_devnet_config.md` — per-environment configuration
- [Cosmos SDK x/upgrade](https://docs.cosmos.network/main/build/modules/upgrade)
- [CometBFT unsafe-reset-all](https://docs.cometbft.com/main/tools/reset-priv-validator)
