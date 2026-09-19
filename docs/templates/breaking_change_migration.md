# Breaking Change PR Template

Any pull request that breaks a frozen contract must copy the checklist below
into its description and answer every line before it can merge.

## What counts as breaking

An incompatible change to any of these:

| Category | Example |
|---|---|
| Proto Msg, Query or Event naming | Renaming an RPC, removing one |
| Proto field semantics or type | Changing a field from string to enum; adding a field that is rejected when absent |
| Store key or prefix | Renaming a store prefix |
| Module account name | Renaming `task_escrow` or `hub_treasury` |
| ABCI hook order | Changing the hub-before-task EndBlock order |
| REST path or route | Changing a registered `hub.v1` or `task.v1` route |
| Parameter set or clamp | Removing a published parameter; tightening an upper bound |
| Error code | Reusing a published code for different semantics |
| Genesis schema | Renaming or removing a published JSON key |
| Hash domain literal | Changing a registered domain string |
| Bech32 prefix or chain-id | Changing `AccountAddressPrefix` |

Not breaking, and therefore not covered by this template: adding an optional
field, adding an RPC, comment-only changes, internal refactors that preserve the
wire contract, new tests, dependency bumps that do not change an API, and
performance work that preserves semantics.

## Choosing a path

- **Reset** — only before mainnet. Clear the chain and start from a new genesis.
  Cheapest, and it discards history.
- **Regular upgrade** — the normal post-mainnet path: a governance proposal and
  an upgrade handler.
- **Emergency migration** — for funds loss, a consensus fork, a disclosure into
  consensus state, or message abuse that governance cannot freeze fast enough.
  Requires the emergency governance threshold.

The operational side of all three is in
`../runbooks/upgrade_reset_live_migration.md`.

## Path requirements

### Reset

```
- [ ] The PR states that it forces a chain reset
- [ ] Default genesis updated for the new schema and defaults
- [ ] docs/static/openapi.json regenerated
- [ ] Fields that downstream consumers depend on updated
- [ ] Testnet operators notified of the reset procedure
- [ ] Localnet scripts still start a fresh chain in one command
```

### Regular upgrade

```
- [ ] Upgrade handler registered with its name and planned height
- [ ] Migration function converts old state to the new schema
    - [ ] Every affected store key covered: source, target, conversion
    - [ ] Asserts that total supply, escrow and rewards identities hold
    - [ ] Module version map updated
- [ ] Parameter migration states the clamp handling for each changed parameter
- [ ] Rollback plan to the pre-upgrade snapshot
- [ ] Deterministic replay: three or more validators upgrade from the same
      snapshot and agree byte for byte
- [ ] Halt height and proposal draft
- [ ] Downgrade window, in blocks
- [ ] Announcement period agreed with downstream consumers
```

### Emergency migration

Everything a regular upgrade requires, plus:

```
- [ ] The PR description opens with EMERGENCY MIGRATION
- [ ] Approved through the emergency governance threshold
- [ ] Halt height within three hours of the proposal
- [ ] Impact statement: which accounts, funds and tasks are affected
- [ ] State mapping for every affected account, escrow and reward balance,
      before and after
- [ ] Postmortem committed to a date
```

## Checklist to copy into the PR

```markdown
## Breaking Change Impact

<!-- Answer every line. Write N/A and the reason where one does not apply. -->

### Classification
- [ ] Type: proto / store / module_account / abci_hook / rest_path / params /
      error_code / genesis / hash_domain / bech32
- [ ] Path: reset / regular_upgrade / emergency_migration

### Contract surface
- [ ] Frozen items affected:
- [ ] Conflicting semantic name, RPC, REST path, event, module account or store key:

### Cross-repository impact
- [ ] Protocol specification sections to update:
- [ ] Wire release required: yes / no
- [ ] Downstream client updates required: yes / no
- [ ] Indexer must re-scan history: yes / no

### Tests
- [ ] Do the frozen-list tests fail? (They should. If not, this is not breaking.)
- [ ] Do the REST path tests fail?
- [ ] Do the ABCI hook order tests fail?
- [ ] New migration test paths:
- [ ] Three-node deterministic replay verified:

### Migration
- [ ] Migration handler location (package, file, function):
- [ ] Reset procedure, if taking the reset path:
- [ ] Rollback plan:
- [ ] Downgrade window, in blocks:

### Communication
- [ ] Downstream consumers notified: date and channel
- [ ] Announcement period met (except for an emergency):
- [ ] Genesis, configuration and CLI documentation updated:
- [ ] docs/static/openapi.json regenerated:

### Sign-off
- [ ] Node reviewer:
- [ ] Keeper reviewer:
- [ ] Governance reviewer, if parameters or governance are involved:
- [ ] Security, mandatory for an emergency:
```

## Maintenance

Changes to this template go through an ordinary PR. Once mainnet is live the
migration checklist is frozen and its items may not be relaxed.
