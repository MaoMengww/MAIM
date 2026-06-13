#!/bin/sh
set -e

# Replace localhost/127.0.0.1 addresses with Docker Compose service names.
# Each container has its own loopback — localhost in one container cannot reach
# another container. Compose provides DNS resolution by service name.

# etcd (service discovery + config center)
sed -i 's/127\.0\.0\.1:2379/etcd:2379/g; s/localhost:2379/etcd:2379/g' /app/etc/*.yaml 2>/dev/null || true

# PostgreSQL
sed -i 's/@127\.0\.0\.1:5432/@postgres:5432/g; s/@localhost:5432/@postgres:5432/g' /app/etc/*.yaml 2>/dev/null || true

# Redis
sed -i 's/127\.0\.0\.1:6379/redis:6379/g; s/localhost:6379/redis:6379/g' /app/etc/*.yaml 2>/dev/null || true

# Kafka
sed -i 's/127\.0\.0\.1:9092/kafka:9092/g; s/localhost:9092/kafka:9092/g' /app/etc/*.yaml 2>/dev/null || true

# MinIO (internal endpoint only — preserve PublicEndpoint for browser-facing URLs)
# Handle both "Endpoint:" (capital E) and "endpoint:" (lowercase e) — YAML keys can be either
sed -i 's/[Ee]ndpoint: 127\.0\.0\.1:9000/endpoint: minio:9000/g; s/[Ee]ndpoint: localhost:9000/endpoint: minio:9000/g' /app/etc/*.yaml 2>/dev/null || true
sed -i 's/[Pp]ublicEndpoint: minio:9000/PublicEndpoint: localhost:9000/g' /app/etc/*.yaml 2>/dev/null || true

# Milvus
sed -i 's/localhost:19530/milvus:19530/g' /app/etc/*.yaml 2>/dev/null || true

# Neo4j
sed -i 's/localhost:7687/neo4j:7687/g' /app/etc/*.yaml 2>/dev/null || true

# Elasticsearch
sed -i 's/localhost:9200/elasticsearch:9200/g' /app/etc/*.yaml 2>/dev/null || true

# OpenTelemetry Collector
sed -i 's/localhost:4317/otel-collector:4317/g' /app/etc/*.yaml 2>/dev/null || true

exec "$@"
