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
