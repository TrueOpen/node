APP_NAME := node
DAEMON_NAME := noded
BUILD_DIR := build
BIN := $(BUILD_DIR)/$(DAEMON_NAME)

# Default to the `go` on PATH so the build is portable across machines.
# Override with `make GO=/path/to/go ...` if you need a specific toolchain.
GO ?= go
# GOTOOLCHAIN=auto lets Go fetch the exact toolchain go.mod pins (go 1.25.10)
# when the `go` on PATH is older, so the build is portable on a stock Go
# install. Set GOENV=GOTOOLCHAIN=local for a fully offline/reproducible build
# when the required toolchain is already present (the Dockerfile does this).
GOENV ?= GOTOOLCHAIN=auto
IGNITE ?= ignite
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)

LDFLAGS := -X github.com/cosmos/cosmos-sdk/version.Name=$(APP_NAME) \
	-X github.com/cosmos/cosmos-sdk/version.AppName=$(DAEMON_NAME) \
	-X github.com/cosmos/cosmos-sdk/version.Version=$(VERSION) \
	-X github.com/cosmos/cosmos-sdk/version.Commit=$(COMMIT)

BUILD_FLAGS := -mod=readonly -ldflags '$(LDFLAGS)'
TEST_FLAGS := -mod=readonly -timeout 30m

# WIRE_IMAGE is the descriptor image published by the pinned TrueOpen/wire
# release; wire/pin.json records the tag, the commit and its SHA-256. It is the
# generation input for every protobuf artifact in this repository, so the wire
# release - not proto/ - is the source of the contract.
#
# WIRE_EXCLUDE drops packages the wire release scope withholds. Wire v0.4.1
# publishes every package in the image, so generation receives the complete
# descriptor set. Nexus output follows its own go_package and is discarded
# after generation; Node does not copy or implement that service.
WIRE_IMAGE := wire/wire.binpb
WIRE_EXCLUDE :=

.PHONY: all help build install test test-unit test-race test-cover bench vet lint lint-fix proto-go proto-gen proto-deps proto-event-check proto-wire-check clean version check-split

all: build

help:
	@echo "Targets:"
	@echo "  make build       Build ./$(BIN)"
	@echo "  make install     Install $(DAEMON_NAME)"
	@echo "  make test        Run all tests"
	@echo "  make vet         Run go vet"
	@echo "  make lint        Run golangci-lint and exported dead-code analysis"
	@echo "  make proto-go    Regenerate the protobuf Go sources only"
	@echo "  make proto-gen   Regenerate proto Go files, OpenAPI and node docs"
	@echo "  make proto-event-check Verify typed event protos and Go output"
	@echo "  make proto-wire-check  Verify the pinned wire release descriptor"
	@echo "  make check-split Enforce module-split import-direction rules"
	@echo "  make clean       Remove build artifacts"
	@echo ""
	@echo "Variables:"
	@echo "  GO=$(GO)"
	@echo "  GOENV=$(GOENV)"
	@echo "  IGNITE=$(IGNITE)"

version:
	@$(GOENV) $(GO) version
	@echo "$(DAEMON_NAME) $(VERSION) ($(COMMIT))"

build:
	@mkdir -p $(BUILD_DIR)
	@$(GOENV) $(GO) build $(BUILD_FLAGS) -o $(BIN) ./cmd/$(DAEMON_NAME)

install:
	@$(GOENV) $(GO) install $(BUILD_FLAGS) ./cmd/$(DAEMON_NAME)

test: test-unit

test-unit:
	@$(GOENV) $(GO) test $(TEST_FLAGS) ./...

test-race:
	@$(GOENV) $(GO) test $(TEST_FLAGS) -race ./...

test-cover:
	@$(GOENV) $(GO) test $(TEST_FLAGS) -coverprofile=coverage.out -covermode=atomic ./...
	@$(GOENV) $(GO) tool cover -html=coverage.out -o coverage.html
	@rm coverage.out

bench:
	@$(GOENV) $(GO) test $(TEST_FLAGS) -bench=. ./...

vet:
	@$(GOENV) $(GO) vet ./...

lint:
	@$(GOENV) $(GO) run github.com/golangci/golangci-lint/cmd/golangci-lint run ./... --timeout 15m
	@$(GOENV) $(GO) run golang.org/x/tools/cmd/deadcode -test ./...

lint-fix:
	@$(GOENV) $(GO) run github.com/golangci/golangci-lint/cmd/golangci-lint run ./... --fix --timeout 15m

proto-deps:
	@echo "Proto tools are tracked in tools.go."

# Regenerate every protobuf Go source into the module tree from the pinned wire
# release image.
#
# This is the single authoritative generation mode and every check diffs against
# it. Narrowing the request is not an equivalent shortcut: protoc-gen-gocosmos
# output for one file depends on which *other* files are in the same generation
# request, so adding task/v1/deadline.proto to the request changes the oneof
# doc comments emitted for task/v1/event.proto. proto-event-check used to
# regenerate four event protos with `--path` and diff that narrow output against
# the tree, which compares the results of two different generator inputs; the
# committed bytes of x/task/types/event.pb.go could then only ever be
# whatever the narrow invocation emitted.
#
# WIRE_EXCLUDE is the one narrowing that is safe here, because a withheld package
# is not imported by any released one - wire_pin_test.go is what holds that.
proto-go:
	@$(GOENV) $(GO) run github.com/bufbuild/buf/cmd/buf generate --template codegen/buf.gen.gogo.yaml $(WIRE_EXCLUDE) $(WIRE_IMAGE)
	@$(GOENV) $(GO) run github.com/bufbuild/buf/cmd/buf generate internal/proto --template internal/proto/buf.gen.gogo.yaml
	@if [ -d github.com/TrueOpen/node ]; then \
		for pkg in hub task shared; do \
			dir=github.com/TrueOpen/node/x/$$pkg/types; \
			if [ -d $$dir ] && ls $$dir/*.go >/dev/null 2>&1; then \
				mkdir -p x/$$pkg/types; \
				cp $$dir/*.go x/$$pkg/types/; \
			fi; \
			done; \
		internal_dir=github.com/TrueOpen/node/x/hub/internal/types; \
		if [ -d $$internal_dir ] && ls $$internal_dir/*.go >/dev/null 2>&1; then \
			mkdir -p x/hub/internal/types; \
			cp $$internal_dir/*.go x/hub/internal/types/; \
		fi; \
		rm -rf github.com; \
	fi

proto-gen: proto-go
	@$(GOENV) $(GO) run github.com/bufbuild/buf/cmd/buf generate --template codegen/buf.gen.sta.yaml $(WIRE_EXCLUDE) $(WIRE_IMAGE)
	@$(GOENV) $(GO) run github.com/bufbuild/buf/cmd/buf generate --template codegen/buf.gen.swagger.yaml $(WIRE_EXCLUDE) $(WIRE_IMAGE)
	@$(GOENV) $(GO) run ./scripts/merge_openapi -hub hub/v1/query.swagger.json -task task/v1/query.swagger.json -out docs/static/openapi.json
	@$(GOENV) $(GO) run ./scripts/generate_node_api_doc.go -descriptor $(WIRE_IMAGE) -out docs/static/node-api.md
	@rm -rf node bus hub nexus shared task

# Verify that every committed protobuf Go source matches generation.
#
# The `buf lint` and `buf build` steps this target used to run against four event
# protos are gone with proto/: linting the contract is TrueOpen/wire's job now,
# and its CI runs `buf lint` over the whole module plus `buf breaking` against the
# previous release. Re-linting a descriptor image here would only re-check bytes
# that were already linted before they were published.
#
# What remains is the half that is about THIS repository: regenerate from the
# pinned release and require the committed Go and public API documents to be
# identical. The untracked check is required because git diff alone cannot see a
# newly generated file that a Wire release added and a commit forgot to include.
proto-event-check: proto-gen
	@git diff --exit-code -- 'x/*/types/*.pb.go'
	@git diff --exit-code -- 'x/hub/internal/types/*.pb.go'
	@git diff --exit-code -- docs/static/openapi.json docs/static/node-api.md
	@test -z "$$(git ls-files --others --exclude-standard -- 'x/*/types/*.pb.go' 'x/hub/internal/types/*.pb.go')" || { \
		echo "untracked generated protobuf files:"; \
		git ls-files --others --exclude-standard -- 'x/*/types/*.pb.go' 'x/hub/internal/types/*.pb.go'; \
		exit 1; \
	}

# Verify the pinned wire release descriptor.
proto-wire-check:
	@$(GOENV) $(GO) test $(TEST_FLAGS) -run TestWirePin -count=1 .

check-split:
	@bash scripts/check_split_import_direction.sh

clean:
	@rm -rf $(BUILD_DIR) coverage.html coverage.out
