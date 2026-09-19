# syntax=docker/dockerfile:1.6
#
# TrueOpen Node — multi-stage container image.
#
# Runbook: docs/runbooks/release_artifacts.md.
#
# Design notes:
#   - Stage `builder` uses the official golang image pinned to the same
#     minor version go.mod declares (go 1.25.x). The build compiles
#     noded with the mainnet-style ldflags used by `make build`.
#   - Stage `runtime` starts from Debian slim so the image ships with
#     the CA certs + libc noded links against, and the operator can
#     still `exec` a shell for debugging. Distroless is tempting for
#     size but blocks the "exec into a running validator" workflow
#     that N10.8 daily-ops leans on.
#   - Non-root user `noded` (uid 1000) matches the systemd unit in
#     N10.8 §1.1 so bind-mounted volumes with 0755 perms just work.

ARG GO_VERSION=1.25
ARG DEBIAN_CODENAME=trixie

# ---- builder ----------------------------------------------------------
FROM golang:${GO_VERSION}-${DEBIAN_CODENAME} AS builder

WORKDIR /src

# Prime module cache in its own layer so source-only edits do not
# re-download deps.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown

# Match the ldflags Makefile uses so the container binary and the
# host make-build binary produce the same `noded version` output.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=local \
    go build \
      -mod=readonly \
      -trimpath \
      -ldflags "-s -w \
        -X github.com/cosmos/cosmos-sdk/version.Name=node \
        -X github.com/cosmos/cosmos-sdk/version.AppName=noded \
        -X github.com/cosmos/cosmos-sdk/version.Version=${VERSION} \
        -X github.com/cosmos/cosmos-sdk/version.Commit=${COMMIT}" \
      -o /out/noded \
      ./cmd/noded

# ---- runtime ----------------------------------------------------------
FROM debian:${DEBIAN_CODENAME}-slim AS runtime

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        jq \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 noded \
    && useradd  --uid 1000 --gid 1000 --home /home/noded --create-home --shell /bin/bash noded

COPY --from=builder /out/noded /usr/local/bin/noded

USER noded
WORKDIR /home/noded
ENV NODED_HOME=/home/noded/.node

# 26656 p2p / 26657 rpc / 1317 rest / 9090 grpc / 26660 prometheus
EXPOSE 26656 26657 1317 9090 26660

# Default entrypoint launches the node against the mounted home dir.
# Compose / K8s override this for `noded init`, `noded keys`, etc.
ENTRYPOINT ["noded"]
CMD ["start", "--home", "/home/noded/.node"]
