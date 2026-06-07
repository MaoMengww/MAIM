#!/bin/bash
# Wait for k3s to be ready, then start port forwards
set -e

# Wait for k3s API
until kubectl cluster-info &>/dev/null; do
  sleep 2
done

# Wait for gateway service to exist
until kubectl get svc aim-gateway &>/dev/null; do
  sleep 2
done

# Wait for gateway pod to be ready
kubectl wait --for=condition=ready pod -l app=aim-gateway --timeout=120s 2>/dev/null || true

# Wait for ws-gateway pod
kubectl wait --for=condition=ready pod -l app=aim-ws-gateway --timeout=120s 2>/dev/null || true

# Start port forwards
nohup kubectl port-forward svc/aim-gateway 8080:8080 --address 0.0.0.0 &>/dev/null &
nohup kubectl port-forward svc/aim-ws-gateway 8081:8081 --address 0.0.0.0 &>/dev/null &
nohup kubectl port-forward svc/aim-minio 9000:9000 --address 0.0.0.0 &>/dev/null &

sleep 2
echo "Port forwards ready:"
echo "  REST API:  http://localhost:8080"
echo "  WebSocket: ws://localhost:8081"
echo "  MinIO:     http://localhost:9000"
