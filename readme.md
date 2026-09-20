# node

`noded` is the TrueOpen chain: a Cosmos SDK v0.53 / CometBFT v0.38 application
implementing the Hub and Task modules, with Hyperlane core and warp mounted for
the USDC bridge.

> **Importing the wire types from Go?** Read [CONSUMING.md](CONSUMING.md) first.
> It lists which commit to pin, the supported import packages, the Go 1.25.10
> toolchain requirement, and the four `replace` directives that do not
> propagate to consumers. The full repository is expected to build and test.

## Modules

| Module | Responsibility |
|---|---|
| `x/hub` | Participant registry and identity, models and profiles, the BuilderSet, rewards, emission and the treasury |
| `x/task` | Task lifecycle, escrow, stage submissions, verification and settlement |
| `x/shared` | Canonical framing, signing domains and types both modules read |

The protobuf contract is not owned here. It comes from the pinned
[`TrueOpen/wire`](https://github.com/TrueOpen/wire) release recorded in
`wire/pin.json`; `make proto-wire-check` verifies the tree against it.

## Build

Go 1.25.10 or newer.

```sh
make build        # -> build/noded
make test         # full unit suite
make lint         # golangci-lint plus exported dead-code analysis
```

`make help` lists every target.

## Run a local chain

```sh
./scripts/localnet_single_node.sh
```

This initialises a single-validator chain and starts it. Useful switches:

```sh
RESET=1            # wipe the home directory first
FAST_BLOCKS=1      # ~1s commits
GOV_FAST=1         # short gov voting/deposit periods, for testing governance
START=0            # set up genesis but do not start
HOME_DIR=/tmp/x    # use a separate home directory
```

Once it is up:

```sh
build/noded status
build/noded query hub params -o json
build/noded query task params -o json
```

## Container

```sh
docker build --build-arg VERSION=$(git describe --tags) \
             --build-arg COMMIT=$(git rev-parse HEAD) -t noded .
```

The image is published as `ghcr.io/trueopen/noded:<version>`.

## Releases

Releases are cut from a `v*` tag. `.github/workflows/release.yml` builds the
platform binaries, attaches them with a `SHA256SUMS.txt`, and leaves a **draft**
release for the maintainer to sign and publish.

```sh
git tag -s v1.2.3 -m "release v1.2.3"
git push origin v1.2.3
```

Artifacts, signing, verification, distribution and rollback are documented in
[docs/runbooks/release_artifacts.md](docs/runbooks/release_artifacts.md).

## Documentation

- [docs/governance_spec.md](docs/governance_spec.md) — what governance can and
  cannot do, the lifecycle, tallying and parameters
- [docs/governance_dualmode_design.md](docs/governance_dualmode_design.md) —
  proposal domain routing, and why there is no custom tally
- [docs/builder_registration_design.md](docs/builder_registration_design.md) —
  the Builder admission path
- [docs/runbooks/](docs/runbooks/) — operator runbooks: build and test, daily
  ops, governance changes, upgrades and migration, metrics, release artifacts
- [docs/rpc.md](docs/rpc.md) — the RPC surface

## Related repositories

- [TrueOpen/wire](https://github.com/TrueOpen/wire) — the protobuf contract,
  signing domains and cross-language fixtures this chain is generated from
