FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY tests/e2e/client/ ./tests/e2e/client/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /e2e ./tests/e2e/client/

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /e2e /e2e
ENTRYPOINT ["/e2e"]
