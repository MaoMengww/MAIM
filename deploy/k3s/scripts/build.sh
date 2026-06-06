#!/bin/bash
# Build all AIM microservice images and push to k3s local registry
# Usage: ./deploy/k3s/scripts/build.sh [tag] [registry]
set -e

TAG="${1:-latest}"
REGISTRY="${2:-registry.aim.local}"

SERVICES=(
  user-service
  friend-service
  message-service
  conversation-service
  file-service
  llm-gateway
  knowledge-base
  bot-platform
  audit-service
  ai-bot-service
  signaling-service
  ws-gateway
  gateway
)

for svc in "${SERVICES[@]}"; do
  echo "Building aim-${svc}:${TAG} ..."
  docker build \
    -f deploy/docker/Dockerfile \
    --build-arg SERVICE="${svc}" \
    -t "${REGISTRY}/aim-${svc}:${TAG}" \
    .
done

echo "All images built."

# Push to registry
for svc in "${SERVICES[@]}"; do
  echo "Pushing aim-${svc}:${TAG} ..."
  docker push "${REGISTRY}/aim-${svc}:${TAG}"
done

echo "Done. Total: ${#SERVICES[@]} images."
