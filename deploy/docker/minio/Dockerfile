# Community MinIO no longer publishes container images; build pinned source.
FROM golang:1.25-alpine AS builder
ENV CGO_ENABLED=0 GOMAXPROCS=2
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install -p 2 github.com/minio/minio@RELEASE.2025-04-22T22-12-26Z && \
    go install -p 2 github.com/minio/mc@v0.0.0-20250312172924-c1d5d4cbb4ca

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /go/bin/minio /go/bin/mc /usr/local/bin/
ENTRYPOINT ["minio"]
CMD ["server", "/data", "--console-address", ":9001"]
