# Release Artifacts

How a `noded` release is produced, signed, distributed and — if necessary —
rolled back.

## Artifacts

| Artifact | Produced by | Consumed by |
|---|---|---|
| Source tarball `noded-<version>-source.tar.gz` | `git archive` on the tag | Validators building from source |
| Platform binaries `noded-<version>-<os>-<arch>.tar.gz` | `.github/workflows/release.yml` | Most validators |
| Container image `ghcr.io/trueopen/noded:<version>` | `Dockerfile`, multi-stage | Containerised validators, Kubernetes, compose |

## Versioning

- Semantic versioning: `vMAJOR.MINOR.PATCH`, optionally with an `-rc.N` or
  `-beta.N` suffix.
- Mainnet starts at `v1.0.0`. Only the lead maintainer can push a tag.
- Testnet uses `v0.MAJOR.MINOR-testnet.N`.
- A pushed tag is never deleted. A fix ships as a new patch tag; tags are never
  force-pushed.

Every tag's changelog entry states the specification sections involved, any
breaking change with a link to its PR, and any upstream Cosmos SDK or CometBFT
version bump.

## Producing a release

```
1. Review and merge every target PR into main.
2. Confirm `make test` is green.
3. Tag and push:
     git tag -s v1.2.3 -m "release v1.2.3"
     git push origin v1.2.3
4. The release workflow builds the platform binaries and uploads them to a
   draft release.
5. The maintainer publishes the release.
```

Building the same artifacts by hand:

```bash
# source tarball
git archive --format=tar.gz --prefix=noded-v1.2.3/ v1.2.3 -o noded-v1.2.3-source.tar.gz
sha256sum noded-v1.2.3-source.tar.gz

# container image
docker build \
    --build-arg VERSION=v1.2.3 \
    --build-arg COMMIT=$(git rev-parse HEAD) \
    -t ghcr.io/trueopen/noded:v1.2.3 \
    -t ghcr.io/trueopen/noded:latest \
    .
```

## Signing and verification

Attach a signed `SHA256SUMS.txt` to the release:

```bash
sha256sum noded-v1.2.3-*.tar.gz > SHA256SUMS.txt
gpg --armor --detach-sign --output SHA256SUMS.txt.asc SHA256SUMS.txt
```

A validator verifies it with:

```bash
gpg --verify SHA256SUMS.txt.asc SHA256SUMS.txt
sha256sum -c SHA256SUMS.txt
```

Container images are signed with [cosign](https://github.com/sigstore/cosign)
keyless:

```bash
cosign sign ghcr.io/trueopen/noded:v1.2.3

cosign verify ghcr.io/trueopen/noded:v1.2.3 \
    --certificate-identity-regexp "https://github.com/TrueOpen/node/.*" \
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com"
```

The Dockerfile builds with `-trimpath` and fixed ldflags, so the same tag built
on different machines produces the same digest. Before a mainnet release, verify
that on three machines.

## Distribution

| Channel | Content | Access |
|---|---|---|
| GitHub Releases | Source and platform tarballs, `SHA256SUMS.txt` and its signature | Public |
| GHCR | Container image | Public |
| Announcement | Tag, upgrade height, proposal id | Mailing list, chat, social |

Never publish a mainnet binary to an unofficial mirror, and never rewrite a
published `SHA256SUMS.txt`.

## Running the image

```bash
docker pull ghcr.io/trueopen/noded:v1.2.3

# initialise the node home on a persistent volume
docker run --rm -v trueopen-data:/home/noded/.node ghcr.io/trueopen/noded:v1.2.3 \
    init my-validator --chain-id trueopen-mainnet-1

docker run -d --name noded \
    -v trueopen-data:/home/noded/.node \
    -p 26656:26656 -p 26657:26657 -p 1317:1317 -p 9090:9090 -p 26660:26660 \
    ghcr.io/trueopen/noded:v1.2.3
```

```yaml
services:
  noded:
    image: ghcr.io/trueopen/noded:v1.2.3
    container_name: noded
    restart: unless-stopped
    volumes:
      - trueopen-data:/home/noded/.node
    ports:
      - "26656:26656"              # p2p
      - "127.0.0.1:26657:26657"    # CometBFT RPC, loopback only
      - "127.0.0.1:1317:1317"      # REST
      - "127.0.0.1:9090:9090"      # gRPC
      - "127.0.0.1:26660:26660"    # Prometheus
    stop_grace_period: 60s

volumes:
  trueopen-data:
```

Hardening for mainnet:

- Expose only `26656` publicly. Keep RPC, REST, gRPC and Prometheus on loopback
  or behind a VPN.
- Mount `priv_validator_key.json` read-only, or use a remote signer.
- Keep `stop_grace_period` at 60s or more so an IAVL commit completes before the
  process exits.

## Pre-release checklist

- [ ] `make test` green
- [ ] `make test-race` green
- [ ] `make lint` reports no errors
- [ ] Specification changes listed in the changelog
- [ ] For a breaking change, the template checklist is answered in the tag notes
- [ ] `docker build .` succeeds
- [ ] `noded version` matches the tag
- [ ] A single-node localnet produces blocks
- [ ] `SHA256SUMS.txt` generated and GPG-signed
- [ ] Container image signed with cosign

## Rollback

If a published release turns out to crash or diverge:

1. Mark the affected assets as pre-release immediately, so no new validator
   pulls them.
2. Announce "do not upgrade to vX.Y.Z; stay on vX.Y.(Z-1)".
3. Tag `vX.Y.(Z+1)` through the full release process.
4. Validators that already upgraded follow the downgrade window in
   `upgrade_reset_live_migration.md`.

Never delete a published tag, never force-push one to a different commit, and
never modify a published `SHA256SUMS.txt`. Any of the three permanently breaks
the chain of trust validators rely on.

## Testnet versus mainnet

| | Testnet | Mainnet |
|---|---|---|
| Build tag | none | `-tags mainnet` |
| Image tag suffix | `-testnet` | none |
| GPG signature | one signer | at least two co-signers |
| Reproducible build check | one machine | three machines |
| Announcement period | 1 day or more | 7 days or more |
| Downgrade window | 100 blocks or more | 500 blocks or more |

A mainnet image is built with `-tags mainnet`. That tag adds a local start-up
assertion for the validator role: the node requires `config/vrf_key.json`
unless `[beacon] vrf-key-required = false` is set explicitly, which RPC, seed
and plain full nodes must do. The switch controls only that local assertion; it
never changes whether a proposal is accepted or rejected.

## Frozen

Changing any of these requires lead maintainer sign-off:

- The set of three artifact kinds.
- The image base, `debian:trixie-slim`, which fixes libc compatibility.
- The non-root `noded` user at uid 1000.
- The five published ports: 26656, 26657, 1317, 9090, 26660.

Not frozen: which platforms the binaries target, the image tag scheme, and the
CI wiring itself.

## Related

- `Dockerfile` and `.dockerignore`
- `.github/workflows/release.yml`
- `build_and_test.md` — building on each platform
- `upgrade_reset_live_migration.md` — upgrade paths
- `mainnet_devnet_config.md` — per-environment differences
- `operator_daily_ops.md` — day-to-day operations
- `../templates/breaking_change_migration.md` — breaking-change PR template
