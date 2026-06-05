FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG SERVICE
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /build/service ./app/${SERVICE}/

FROM alpine:3.21

RUN apk add --no-cache tzdata ca-certificates

COPY --from=builder /build/service /app/service
COPY deploy/docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh /app/service

ARG SERVICE
COPY app/${SERVICE}/etc/ /app/etc/

WORKDIR /app
ENTRYPOINT ["/entrypoint.sh"]
CMD ["/app/service"]
