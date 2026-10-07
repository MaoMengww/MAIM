FROM golang:1.26.8-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY tests/e2e/client/ ./tests/e2e/client/
COPY pkg/identity/ ./pkg/identity/
COPY pkg/sequence/ ./pkg/sequence/
COPY pkg/pb/common/ ./pkg/pb/common/
COPY app/user-service/pb/user/ ./app/user-service/pb/user/
COPY app/realtime-service/pb/realtime/ ./app/realtime-service/pb/realtime/
COPY app/file-service/pb/file/ ./app/file-service/pb/file/
COPY app/message-service/pb/message/ ./app/message-service/pb/message/
COPY app/llm-gateway/pb/llmgateway/ ./app/llm-gateway/pb/llmgateway/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /e2e ./tests/e2e/client/

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /e2e /e2e
ENTRYPOINT ["/e2e"]
