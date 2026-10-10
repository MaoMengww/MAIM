SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
export GOTOOLCHAIN ?= go1.26.8

PROTOC_VERSION := 29.4
PROTOC_GEN_GO_VERSION := 1.36.11
PROTOC_GEN_GO_GRPC_VERSION := 1.6.1
GO_MODULE := $(shell sed -n 's/^module //p' go.mod)
PROTO_FILES := $(sort $(wildcard idl/*.proto idl/*/*.proto))

.PHONY: proto
proto:
	@test "$$(protoc --version)" = "libprotoc $(PROTOC_VERSION)" || { printf '%s\n' 'Install protoc $(PROTOC_VERSION) and add it to PATH (README: Protobuf 生成).'; exit 1; }
	@test "$$(protoc-gen-go --version)" = "protoc-gen-go v$(PROTOC_GEN_GO_VERSION)" || { printf '%s\n' 'Install protoc-gen-go v$(PROTOC_GEN_GO_VERSION) and add it to PATH (README: Protobuf 生成).'; exit 1; }
	@test "$$(protoc-gen-go-grpc --version)" = "protoc-gen-go-grpc $(PROTOC_GEN_GO_GRPC_VERSION)" || { printf '%s\n' 'Install protoc-gen-go-grpc v$(PROTOC_GEN_GO_GRPC_VERSION) and add it to PATH (README: Protobuf 生成).'; exit 1; }
	protoc --proto_path=idl \
		--go_out=. --go_opt=module=$(GO_MODULE) \
		--go-grpc_out=. --go-grpc_opt=module=$(GO_MODULE) \
		$(PROTO_FILES)

.PHONY: ci-format ci-tidy ci-build ci-test ci-lint ci-integration ci-e2e
ci-format:
	@files="$$(gofmt -l app pkg migrations tests/e2e/client)"; \
	if [[ -n "$$files" ]]; then printf '%s\n' "$$files"; exit 1; fi

ci-tidy:
	go mod tidy -diff

ci-build:
	go build ./...
	go vet ./...

ci-test:
	@mkdir -p .scratch/ci-artifacts
	@packages="$$(go list ./app/... ./pkg/... ./migrations/... | paste -sd, -)"; \
	go test -race -count=1 -timeout=10m -covermode=atomic -coverpkg="$$packages" \
		-coverprofile=.scratch/ci-artifacts/unit.coverage.out ./...

ci-lint:
	golangci-lint run ./...

# INBOX_TEST_DSN must point to an isolated database with no application consumers.
ci-integration:
	@test -n "$${INBOX_TEST_DSN:-}" || { echo 'INBOX_TEST_DSN is required'; exit 1; }
	@mkdir -p .scratch/ci-artifacts
	go test -race -tags integration -p 1 -count=1 -timeout=10m -covermode=atomic \
		-coverprofile=.scratch/ci-artifacts/integration.coverage.out \
		./app/message-service/internal/repo ./app/message-service/internal/logic/messageservice

ci-e2e:
	python3 tests/e2e/run.py --scenario all --artifacts .scratch/ci-artifacts/e2e
