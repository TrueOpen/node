# Consuming `github.com/TrueOpen/node` as a Go library

For off-chain consumers — a relayer, an external signer, an indexer — that need
the node's wire types, canonical hashing helpers, domain tags and enum numbers
without running the chain.

Read the dependency section before you decide to import anything: this is a
single Go module, so importing one package pulls the whole chain's dependency
graph into your build. There is a lighter alternative, described at the end.

## What to pin

Pin an exact release tag, or an exact commit SHA. Do not track a branch.

```
go get github.com/TrueOpen/node@<TAG-OR-SHA>
```

## Importable packages

These are the wire-type packages an off-chain consumer normally needs. The
keeper, module, app and command packages also compile, but they are chain
implementation, not a supported lightweight surface.

| Package | Contents |
|---|---|
| `x/shared/types` | `Amount` and `SignedAmount`, the canonical hash framing (`CanonicalHashBytes`, `CanonicalFrameBytes`), the domain registry (`MustDomain`, `DomainXxx`), `TaskType`, model id validation |
| `x/hub/types` | Hub state messages, Msg and Query messages, hub enums, and the cross-language domain fixture |
| `x/task/types` | Task state messages, `task.v1.Msg` and `task.v1.Query` messages, task enums, task id derivation, order envelope and signature preimage helpers |

Verify on the commit you pinned:

```
go build ./x/shared/... ./x/hub/types ./x/task/types
```

## Toolchain

`go.mod` declares `go 1.25.10`, and that is not lowered.

With the default `GOTOOLCHAIN=auto` this is transparent: a consumer whose own
`go.mod` says an older version has the `go` command download and re-exec the
required toolchain. `go get` may record a `toolchain` line in your `go.mod`;
that is the mechanism working as intended.

You have to install the toolchain yourself in exactly two cases:

- `GOTOOLCHAIN=local`, which disables auto-switching.
- A hermetic build with no network access to the module proxy. Bake the
  toolchain into the image, or pre-seed it in the module cache.

## Dependency graph

This repository is one Go module covering the binary, the app wiring, the
keepers and the wire types. There is no separate types-only module, so importing
any package pulls the module's dependency graph into your build — roughly 84
modules, including:

| Module | Version |
|---|---|
| `github.com/cosmos/cosmos-sdk` | `v0.53.6` |
| `cosmossdk.io/api` | `v0.9.2` |
| `github.com/cometbft/cometbft` | `v0.38.21` |
| `github.com/TrueOpen/wire` | `v0.1.0`, pinned by `wire/pin.json` |
| `github.com/grpc-ecosystem/grpc-gateway` | `v1.16.0`, the runtime the generated gateway code uses |
| `cosmossdk.io/collections`, `core`, `store`, `math`, `errors`, `log` | see `go.mod` |
| `github.com/cosmos/gogoproto`, `google.golang.org/grpc`, `google.golang.org/protobuf` | see `go.mod` |
| `github.com/cockroachdb/pebble`, `github.com/syndtr/goleveldb`, `github.com/cosmos/cosmos-db` | through `cosmossdk.io/store` |

### The replace directives do not propagate

Go ignores `replace` in dependency modules; only the main module's `replace`
block applies. This repository's is:

```
replace (
	// support for go 1.26 (remove when cosmossdk.io/log is updated)
	github.com/bytedance/sonic => github.com/bytedance/sonic v1.15.0
	// fix upstream GHSA-h395-qcrw-5vmq vulnerability.
	github.com/gin-gonic/gin => github.com/gin-gonic/gin v1.9.1
	// replace broken goleveldb
	github.com/syndtr/goleveldb => github.com/syndtr/goleveldb v1.0.1-0.20210819022825-2ae1ddf74ef7
	// replace broken vanity url
	nhooyr.io/websocket => github.com/coder/websocket v1.8.7
)
```

Two of them sit inside the closure of the importable packages and are the ones
most likely to affect you: `bytedance/sonic`, reached through `cosmossdk.io/log`,
where the absence of the replace can fail the build on newer toolchains; and
`syndtr/goleveldb`, reached through the store, where it can resolve to a broken
pseudo-version. If you hit a resolution or build conflict, copy the block into
your own `go.mod` verbatim.

## The lighter alternative

If a full Cosmos SDK dependency graph is unacceptable for your service, do not
import these packages. Generate your own bindings from the protobuf contract
instead.

The contract is published by `TrueOpen/wire` as a release, not by this
repository. Take `wire.binpb` from the release named in `wire/pin.json` and
generate from that descriptor image. It is the authority for field numbers, enum
numbers and message names, and the release's `release-manifest.json` lets you
verify the bytes you downloaded before you trust them.

## Stability

- Field numbers, enum numbers and message names are frozen within a wire
  release. A change to any of them requires a new release that declares it.
- The handwritten Go helpers in the three packages above are versioned with the
  commit you pin. If another implementation has to reproduce a consensus hash,
  pin the golden vectors alongside the generated types.
- Hash preimages are assembled from bytes, never from text. There is
  deliberately no helper that takes `...string`: decimal digits, Go enum names
  and hex strings must not reach a consensus hash through a convenience
  signature.
- `CONTRACT-GAP` and `CONTRACT-CONFLICT` comments in the protobuf sources mark
  places where the frozen contract does not pin a value and the comment is the
  working authority. They survive into the descriptor image and into generated
  code, so `grep -rn CONTRACT-GAP` over your bindings finds them. Read them
  before implementing any preimage — those are exactly the points where two
  independent implementations will disagree.
- Genesis is fresh V1: a deleted collection is deleted, with no compatibility
  field and no "must be empty" runtime path. Field names and numbers may be
  reserved to prevent reuse; that is not a decoder and not stored compatibility
  state.
