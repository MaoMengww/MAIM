#!/bin/bash
cd /home/maomeng/project/AIM

echo "=== Docker Build All Services ==="

services=(
  user-service friend-service conversation-service message-service
  gateway file-service llm-gateway knowledge-base
  ai-bot-service bot-platform ws-gateway signaling-service
  audit-service
)

total=${#services[@]}
i=0
failed=""

for svc in "${services[@]}"; do
  ((i++))
  echo "=== [$i/$total] Building $svc ==="
  if docker build -f deploy/docker/Dockerfile --build-arg SERVICE="$svc" -t "aim-${svc}:latest" .; then
    echo "✓ $svc done"
  else
    echo "✗ $svc FAILED"
    failed="$failed $svc"
  fi
  echo ""
done

echo "=== All builds complete ==="
if [ -n "$failed" ]; then
  echo "Failed: $failed"
  exit 1
else
  echo "All $total services built successfully!"
fi
