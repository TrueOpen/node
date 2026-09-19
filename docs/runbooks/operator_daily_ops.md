# Operator Daily Operations

Day-to-day operation of a validator: lifecycle, health probes, backups,
troubleshooting and key management.

Chain evolution — reset, upgrade, live migration — is in
`upgrade_reset_live_migration.md`. Per-environment configuration is in
`mainnet_devnet_config.md`.

## Lifecycle

### systemd

`/etc/systemd/system/noded.service`:

```ini
[Unit]
Description=TrueOpen Node
After=network-online.target
Wants=network-online.target

[Service]
User=noded
Group=noded
Type=simple
ExecStart=/usr/local/bin/noded start --home /var/lib/noded --x-crisis-skip-assert-invariants=false
Restart=on-failure
RestartSec=3
LimitNOFILE=65536
StandardOutput=journal
StandardError=journal

# Mainnet hardening; omit on devnet and localnet.
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
NoNewPrivileges=true
ReadWritePaths=/var/lib/noded

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable noded
sudo systemctl start noded
sudo systemctl status noded --no-pager
sudo journalctl -u noded -f --since "5 min ago"

# Graceful stop; flushes the IAVL commit before exiting
sudo systemctl stop noded

sudo systemctl restart noded
```

Optionally deny the unsafe reset command on a mainnet host through
`/etc/sudoers.d/noded-safe`.

### Foreground, for debugging

```bash
noded start --home ~/.node 2>&1 | tee /tmp/noded.log
# Ctrl-C sends SIGINT; wait for the IAVL commit to finish before it exits
```

## Health probes

### Liveness

```bash
noded status | jq '.SyncInfo.latest_block_height'

before=$(noded status | jq -r '.SyncInfo.latest_block_height')
sleep 10
after=$(noded status | jq -r '.SyncInfo.latest_block_height')
[ "$after" -gt "$before" ] || echo "STALL: height did not advance in 10s (before=$before after=$after)"
```

### Readiness

```bash
noded status | jq '.SyncInfo.catching_up'        # expect false
noded status | jq '.SyncInfo.latest_block_time'  # expect within 30s of now
```

### Full status

```bash
noded status | jq '{
  node_id:        .NodeInfo.id,
  chain_id:       .NodeInfo.network,
  moniker:        .NodeInfo.moniker,
  height:         .SyncInfo.latest_block_height,
  catching_up:    .SyncInfo.catching_up,
  validator_addr: .ValidatorInfo.Address,
  voting_power:   .ValidatorInfo.VotingPower,
}'
```

### Application level

```bash
# Hub and task carry independent parameter sets
noded query hub params
noded query task params

# Every business module account resolves and has a balance entry
for name in hub task_escrow hub_rewards hub_treasury hub_service_bond hub_builder_bond; do
  addr=$(noded query auth module-account "$name" | jq -r '.account.value.address')
  noded query bank balance "$addr" uusdc
done
```

### Endpoints

```bash
curl -s http://127.0.0.1:1317/TrueOpen/hub/v1/params | jq .
curl -s http://127.0.0.1:1317/TrueOpen/task/v1/params | jq .

noded query --grpc-addr 127.0.0.1:9090 hub params

curl -s http://127.0.0.1:26657/status | jq .result.sync_info.latest_block_height
```

## Backups

### What must be backed up

| Path | Content | Frequency |
|---|---|---|
| `config/priv_validator_key.json` | Consensus signing key | Once at install, and on every rotation |
| `config/priv_validator_state.json` | Double-sign protection state | Before every stop |
| `config/vrf_key.json` | Beacon VRF hot key (validators only) | Once at generation, and on every rotation |
| `config/node_key.json` | P2P identity | Once |
| `config/genesis.json` | Genesis | Once, and on every chain reset |
| `data/` | IAVL and block store | Per snapshot policy |
| `config/{app,config,client}.toml` | Configuration | On every change |

### Snapshot

```bash
# Stop first, so priv_validator_state is flushed
sudo systemctl stop noded
sleep 3

sudo tar --exclude='*.log' --exclude='mempool.wal' \
         -czf /backup/noded-$(hostname)-$(date -u +%Y%m%dT%H%M%SZ).tar.gz \
         -C /var/lib noded

sudo systemctl start noded
```

A mainnet validator should snapshot on a schedule, for example every four hours.

### The validator key

Back the consensus key up offline at install time. With a remote signer — which
is strongly recommended on mainnet — the on-host `priv_validator_key.json` is an
empty placeholder and signing happens remotely.

`vrf_key.json` **cannot** move to a remote signer and **cannot** be removed from
the host: ECVRF needs a primitive the consensus signing interface does not
expose. It is a plaintext hot key that must stay resident. Keep its offline
backup on separate media from the consensus key, so one compromised medium does
not expose both.

### Restoring

```bash
sudo systemctl stop noded

# Keep the current double-sign protection state
sudo cp ~/.node/config/priv_validator_state.json /tmp/pvs-current.json 2>/dev/null

sudo rm -rf /var/lib/noded
sudo tar -xzf /backup/noded-<hostname>-<timestamp>.tar.gz -C /var/lib

# Restore the newer protection state, so the height never goes backwards
if [ -s /tmp/pvs-current.json ]; then
  sudo cp /tmp/pvs-current.json ~/.node/config/priv_validator_state.json
fi

sudo systemctl start noded
```

After restoring, verify that the height in `priv_validator_state.json` is at or
above the height in the snapshot. A lower value risks a double sign.

## Disk and logs

```bash
du -sh ~/.node/data
grep -E "^pruning" ~/.node/config/app.toml
```

If the data directory grows faster than expected, check that `pruning` has not
been overridden to `nothing` and that `pruning-keep-recent` is not too large. Do
not delete files by hand: change the pruning settings, restart, and let IAVL
prune naturally over the next retention window.

`journalctl` rotates on its own. If logging to files, rotate them daily and keep
about two weeks.

Spot-check that no payload plaintext reaches the logs. The static defence is the
event attribute allowlist test; this is the operational cross-check:

```bash
sudo journalctl -u noded --since "1 hour ago" | \
    grep -iE 'payload|plaintext|private_key|mnemonic|secret|raw_prompt|raw_answer' | head -20
# Expect no matches. A match is an incident: capture the event and escalate.
```

## Troubleshooting

| Symptom | Diagnose with | Typical cause | Response |
|---|---|---|---|
| Height stalled | `noded status \| jq .SyncInfo` | No peers, hung process, or a planned halt | Check P2P, restart, or follow the upgrade runbook |
| No peers | `curl -s localhost:26657/net_info \| jq '.result.n_peers'` | Unreachable seeds, firewall, corrupt address book | See below |
| Transaction rejected | `noded query tx <hash>` | Insufficient fee, wrong sequence, ante rejection | Read the error code |
| Validator jailed | `noded query staking validator <valoper>` | Missed blocks, or a double sign | `noded tx slashing unjail`, if it was not a double sign |
| Disk full | `df -h; du -sh ~/.node/data` | Pruning ineffective | Archive, fix pruning, restart |
| App hash mismatch | `journalctl -u noded \| grep 'app hash'` | Binary drift or state corruption | Stop, compare binary digests with a peer, restore a snapshot |
| Signature verification failure | Check the client's chain-id | Wrong signing domain | Confirm the chain-id the client signs for |
| Killed by the OOM killer | `dmesg -T \| grep -i 'killed process'` | Mempool or IAVL cache too large | Lower `mempool.size` and the IAVL cache, restart |

### No peers

```bash
# 1. Are the seeds reachable?
grep '^seeds' ~/.node/config/config.toml

# 2. Corrupt address book
sudo systemctl stop noded
mv ~/.node/config/addrbook.json ~/.node/config/addrbook.json.$(date +%s).bak
sudo systemctl start noded

# 3. Firewall
sudo ufw status | grep 26656 || sudo ufw allow 26656/tcp
```

### Halt: upgrade or crash?

```bash
sudo journalctl -u noded --since "10 min ago" | grep -E 'UPGRADE|panic'
```

An `UPGRADE ... NEEDED` line means a planned halt; follow the upgrade runbook. A
panic means a crash; collect a goroutine dump and open an issue.

## Key management

| Key | Purpose | Residency | Backup |
|---|---|---|---|
| `priv_validator_key.json` | Consensus signature, every block | Online; prefer a remote signer | Offline |
| `vrf_key.json` | Beacon ECVRF proof when elected proposer | Must be online in plaintext | Offline, on separate media from the consensus key |
| Operator keyring | Sending transactions | Offline signing, then broadcast | Offline |
| `node_key.json` | P2P identity | Online | Optional; losing it only changes the node id |

Use the `file` or `ledger` keyring backend in production, never `test`:

```bash
noded config set client keyring-backend file
noded keys list --keyring-backend file
```

### Beacon VRF hot key

Beacon randomness is produced by the elected proposer using a **separate VRF hot
key**, registered on chain against the operator's **account** address
(`trueopen1...`, not `trueopenvaloper1...`).

It is not the consensus key and cannot be derived from it. ECVRF proving needs
`gamma = x * hash_to_curve(pk, alpha)`, and a remote signer or HSM exposes only
`R = r*B` over a fixed base point plus `S = r + H(...)*a`. That primitive is not
reachable through the consensus signing interface, so this key must live on the
node as a plaintext file.

#### Who needs one

| Role | Needs `vrf_key.json` | Consequence of not having it |
|---|---|---|
| Full, RPC or seed node | No | On a mainnet build set `[beacon] vrf-key-required = false`, or start-up refuses. Consensus verification is unaffected. |
| Validator, chain has `vrf_required_from_height = 0` | Recommended | The node runs and proposes normally, but the beacon in its proposals degrades to the placeholder source and is not verified. |
| Validator, chain has `vrf_required_from_height > 0` and reached it | Required | Every block it proposes is rejected by the network. It effectively misses every turn and is eventually jailed for downtime. |
| Validator on a mainnet build | Required | Start-up asserts the key is present. |

Check the chain's policy first:

```bash
noded query hub params -o json | jq '.params.beacon.vrf_required_from_height'
# 0 = not enforced (a missing key only degrades); N > 0 = enforced from height N
```

#### Timing: genesis validators must prepare before the genesis freeze

A key registered through `MsgRegisterVrfKey` always lands **pending** and
activates in the **next epoch** — there is no immediate-activation path, not
even for a first registration. The wait is one `epoch_length_blocks`.

**The current deployment deliberately does not rotate epochs.**
`config/localnet_genesis_seed.json` sets `epoch_length_blocks` to `60480000`,
which means no epoch boundary occurs within any foreseeable operating period.
Under that setting, **a VRF key registered after genesis never activates.**

So, while epochs do not turn over:

- **Genesis validators** must generate their key before the genesis freeze and
  hand the printed public key to the genesis coordinator, who writes it into
  `app_state.hub.vrf_keys` with `active_from_epoch = 0`, so it works from the
  first block.
- **Validators joining after genesis** can broadcast a registration and it will
  be accepted into pending state, but it will not activate before the next
  rotation. Their behaviour when elected is the table above. Making them
  participate in the beacon requires either a chain reset with a new genesis, or
  raising `epoch_length_blocks` to a value that actually turns over.
- **Single-node development chains**: whoever starts the chain is also the
  coordinator, so generating and embedding the key is two commands and worth
  doing — the beacon path is only exercised with a real key. It is not a
  prerequisite for starting: with `vrf_required_from_height = 0` the chain runs
  and the full task flow completes, at the cost of every beacon being the
  unverified placeholder.

#### Generating the key

```bash
noded beacon vrf-keygen --home ~/.node
# wrote <home>/config/vrf_key.json
# vrf_pubkey: <hex>

ls -l ~/.node/config/vrf_key.json   # written 0600; verify
```

- If the file already exists the command fails and never overwrites it.
  Overwriting would instantly desynchronise the on-chain public key; rotation
  has its own procedure below.
- The private key is only written to the file and is never printed. The public
  key is public information.
- One key per machine. Never copy a `vrf_key.json` between validators: keys are
  registered per operator, so two operators sharing one key means at least one
  of them cannot register.

#### Embedding it in genesis

Genesis encodes bytes as base64 while the keygen prints hex, so convert:

```bash
python3 -c "import binascii,base64,sys; print(base64.b64encode(binascii.unhexlify(sys.argv[1])).decode())" <vrf_pubkey_hex>
```

One entry per genesis validator in `app_state.hub.vrf_keys`:

```json
{
  "operator_address": "trueopen1...",
  "active_vrf_pubkey": "<base64>",
  "active_from_epoch": "0",
  "vrf_authorization_nonce": "1"
}
```

- `operator_address` is the validator's **account** address, the same one it
  signs transactions with. Not the `valoper` address.
- `active_from_epoch` must be `"0"`, or the first epoch has no active key.
- `vrf_authorization_nonce` starts at `"1"` and increments on each rotation.

Then validate:

```bash
noded genesis validate --home ~/.node
```

The genesis seed file has no `vrf_keys` field, so this section is edited into
`genesis.json` directly, or injected by a script before distribution.

#### Registering and rotating on chain

The operator must already be a known validator, with no rotation pending — only
one may be in flight.

```bash
# 1. Read the current nonce. A first registration has no record; use 1.
noded query hub vrf-key <operator-address> -o json | jq '.vrf_authorization_nonce'

# 2. Produce the proof of possession. The nonce must be exactly one above the
#    chain's value; any other value is rejected. The digest binds the chain-id,
#    so --chain-id is mandatory and must name the target chain.
noded beacon vrf-pop <operator-address> <nonce> --chain-id <chain-id> --home ~/.node

# 3. Broadcast. The operator address comes from the signing account, so --from
#    must be that same operator account.
noded tx hub register-vrf-key \
  --vrf-pubkey <vrf_pubkey_hex> \
  --vrf-key-pop <vrf_key_pop_hex> \
  --vrf-authorization-nonce <nonce> \
  --from <operator-key> --chain-id <chain-id> --keyring-backend file -y
```

Rotation order matters. Getting it wrong costs the ability to produce a verified
beacon for the rest of the current epoch:

1. Generate the new key in a separate directory, leaving `vrf_key.json` in place.
2. Produce the proof of possession with the new key and broadcast the
   registration. The new public key enters pending state.
3. Wait for the pending epoch to actually arrive; activation is automatic.
4. **Only then** replace `vrf_key.json` with the new key and restart.

Replacing the file before step 3 leaves the local key inconsistent with the
active on-chain key. Proposal preparation detects that, logs
`beacon sentinel not injected`, and deliberately injects nothing rather than
proposing a block it would reject itself. The chain does not stall, but that
node's proposals carry only placeholder beacons.

On a chain that does not rotate epochs, step 3 never arrives, so the rotation
cannot complete. Do not replace `vrf_key.json` there; use one of the two routes
described under timing above.

#### Mainnet build assertion

A binary built with `-tags mainnet` checks for the key at start-up under the
validator role:

```toml
[beacon]
vrf-key-required = true
```

Missing key, on a validator:

```
panic: mainnet validator mode requires a beacon VRF hot key at <home>/config/vrf_key.json
```

RPC, seed and plain full nodes carry no proposer duty and must set it to
`false` explicitly. Note that an existing `app.toml` does not gain new fields
automatically; check it during an upgrade.

This is a **local** assertion only. It does not let a validator bypass on-chain
admission and it does not change any accept or reject decision. The sole source
of the consensus policy is the committed `vrf_required_from_height`, and a node
with the assertion disabled still fully verifies every other proposer's beacon.

#### Self-check

```bash
# Is the local key usable? A load failure explains itself in the start-up log.
sudo journalctl -u noded | grep -E 'beacon: proposer signer disabled|beacon sentinel not injected'
# Expect no matches.

# Is the chain actually producing a verified beacon with it? Pick a height this
# node proposed. The height goes to --beacon-height, not the global --height.
noded query hub beacon --beacon-height <height> -o json | jq '.beacon | {source_tag, verified}'
# Expect {"source_tag":"proposer_vrf_v1","verified":true}
# A placeholder source with verified=false means the key is not in effect.
```

#### Production handover checklist

1. **Genesis**: `vrf_required_from_height = 1`; every genesis validator has a
   `vrf_keys` entry with `active_from_epoch = 0`, nonce 1, nothing pending, and
   a public key matching that machine's `config/vrf_key.json`. A missing entry
   fails `InitChain` outright — it cannot be added after the chain starts.
2. **Node roles**: validators keep `vrf-key-required = true`; RPC, seed and full
   nodes set it to `false` explicitly.
3. **State import**: an ordinary export and re-import must keep
   `initial_height > 1`. Never present state that contains a rotated active key
   or a pending key as a fresh height-1 genesis. A reset to height zero requires
   re-pinning the VRF state separately.
4. **Start-up verification**: run `noded genesis validate`, bring up one node to
   exercise `InitChain`, then query the first few heights and confirm the beacon
   is the proposer VRF source and verified, with no `beacon sentinel not
   injected` in the log.
5. **Keys and configuration**: back up `vrf_key.json` at mode 0600, on separate
   media from the consensus key. Confirm every node agrees on the genesis hash,
   the chain id, the binary version, and the denominations in use.
6. **Bridge boundary**: local acceptance runs against a mock ledger only. A
   production deployment must supply the real mailbox, ISM and route
   configuration, and the bridge must not be opened until its status query
   succeeds.

Nothing from a local acceptance run — test keyrings, test private keys, fast
block parameters — may be copied to a production host.

## Alerting

The minimum set:

- Height has not advanced in 30s — critical
- `catching_up` true for more than 5 minutes — warning
- Disk usage above 80% — warning; above 92% — critical
- Process not running — critical
- Validator jailed — critical
- Supply invariant broken — critical

Metric definitions are in `metrics_spec.md`; this runbook covers only the alerts
and the response.

## First-day checklist

```bash
uname -a; noded version

ls -l ~/.node/config/priv_validator_key.json   # 0400
ls -l ~/.node/config/node_key.json             # 0400
ls -ld ~/.node/data                            # 0755

grep -E "^chain_id|^moniker" ~/.node/config/config.toml
grep -E "^minimum-gas-prices|^pruning" ~/.node/config/app.toml
# Validator: true, with config/vrf_key.json present. RPC, seed, full node: false.
grep -A2 '^\[beacon\]' ~/.node/config/app.toml

sudo systemctl start noded
sleep 15
noded status | jq '{catching_up:.SyncInfo.catching_up, height:.SyncInfo.latest_block_height}'

curl -s http://127.0.0.1:1317/TrueOpen/hub/v1/params | jq .params
curl -s http://127.0.0.1:1317/TrueOpen/task/v1/params | jq .params
```

## Related

- `upgrade_reset_live_migration.md` — reset, upgrade and live migration
- `mainnet_devnet_config.md` — per-environment configuration
- `metrics_spec.md` — what to scrape
- `build_and_test.md` — building the binary
- [CometBFT operations](https://docs.cometbft.com/main/tutorials/join-testnet)
- [Cosmos SDK validator manual](https://docs.cosmos.network/main/build/tooling/validator-manual)
