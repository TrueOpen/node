# Build and Test

How to build the node and run its test tiers on Linux, macOS and Windows.

## Prerequisites

| Tool | Version | Why |
|---|---|---|
| Go | 1.25.10 | Pinned by `go.mod`. With the default `GOTOOLCHAIN=auto` the Go command downloads the matching toolchain itself; set `GOTOOLCHAIN=local` only if it is already installed. |
| Git | any recent | `git describe` supplies the build version stamped into the binary. |
| GNU Make | 3.81+ | Present on macOS and most Linux distributions. On Windows see [Windows](#windows). |

No other build tool needs to be installed. `buf` and `golangci-lint` are
invoked through `go run` with the versions pinned in `go.mod`, and `protoc` is
not used at all — protobuf generation runs on the `buf` toolchain.

## Targets

| Target | Does |
|---|---|
| `make build` | Builds `build/noded`. |
| `make install` | Installs `noded` into `$GOPATH/bin`. |
| `make test` | Runs every test. |
| `make test-unit` | Runs the unit tier only. |
| `make test-race` | Runs with the race detector. |
| `make test-cover` | Writes `coverage.out` and `coverage.html`. |
| `make bench` | Runs benchmarks. |
| `make vet` | `go vet`. |
| `make lint` / `make lint-fix` | golangci-lint, optionally applying fixes. |
| `make proto-go` | Regenerates the protobuf Go sources from the pinned wire image. |
| `make proto-gen` | `proto-go` plus OpenAPI and the generated node API document. |
| `make proto-event-check` | Regenerates and fails if the committed generated files differ. |
| `make proto-wire-check` | Verifies the pinned wire release descriptor and registry. |
| `make check-split` | Enforces the module import direction. |
| `make clean` | Removes build and coverage artifacts. |
| `make version` | Prints the version the build would stamp. |

`make help` lists the same set.

The Makefile takes `go` from `PATH`. Point it elsewhere with
`make GO=/path/to/go ...` when a specific toolchain is needed.

## Linux

```bash
# Install Go 1.25.10 from https://go.dev/dl/ if the distribution package is older.
export PATH=/usr/local/go/bin:$PATH
go version

make build
make test
```

## macOS

```bash
make build
make test
```

If `make test` reports `too many open files`, raise the descriptor limit for the
shell first: `ulimit -n 8192`.

## Windows

WSL2 is the smoothest path: inside it, follow the Linux instructions and none of
the line-ending or path-separator differences apply.

For native Windows, Git Bash with GNU Make installed behaves like the Linux
instructions. Without Make, run the Go commands directly:

```powershell
go build -mod=readonly -o build\noded.exe .\cmd\noded
go test -mod=readonly -timeout 30m .\...
go test -mod=readonly -timeout 30m -coverprofile=coverage.out -covermode=atomic .\...
go tool cover -html=coverage.out -o coverage.html
```

Set `git config --global core.autocrlf input` before cloning; otherwise the
checkout rewrites line endings and every file shows as modified.

## Protobuf generation

Generation reads the vendored descriptor image named in `wire/pin.json`, not any
`.proto` file in this repository. `make proto-gen` rewrites the generated Go
sources, `docs/static/openapi.json` and `docs/static/node-api.md`.

`make proto-event-check` is the gate: it regenerates and then fails if anything
committed differs from what generation produces, which is what keeps the
generated tree honest. If it fails, run `make proto-gen` and commit the result.

To move to a newer wire release, replace the vendored artifacts and update
`wire/pin.json` from that release's manifest — the procedure is written out in
the pin file itself.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `go: version mismatch: go.mod requires 1.25.10` | Local Go is older and `GOTOOLCHAIN=local` is set | Unset `GOTOOLCHAIN`, or install 1.25.10 |
| `go: unknown revision` during `proto-gen` | Generator dependencies not fetched | `go mod download`, then retry |
| `command not found: make` on Windows | No native Make | Use WSL2, install GNU Make, or run the Go commands directly |
| `too many open files` during tests on macOS | Default descriptor limit | `ulimit -n 8192` |

## First-run check

Run these in order. If all pass, the environment is complete:

```bash
go version          # go1.25.10
make version        # noded <version> (<commit>)
make build          # produces build/noded
./build/noded version
make test
make vet            # silence means success
```

## Related

- `Makefile` — the definition of every target above.
- `.github/workflows/` — the same gates as they run in CI.
- `upgrade_reset_live_migration.md` — upgrade and reset procedures.
- `release_artifacts.md` — what a release publishes and how to verify it.
