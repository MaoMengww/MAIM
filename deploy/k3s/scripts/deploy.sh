#!/bin/bash
# AIM k3s 一键部署脚本
# Usage: ./deploy/k3s/scripts/deploy.sh [build|deploy|all]
set -e

ACTION="${1:-all}"
SUDO_PASS="${SUDO_PASS:-}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
K3S_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PROJECT_ROOT="$(cd "$K3S_ROOT/.." && pwd)"

SERVICES=(user-service friend-service message-service conversation-service file-service
           llm-gateway knowledge-base bot-platform audit-service ai-bot-service
           signaling-service ws-gateway gateway)

build_images() {
  echo "=== Building all service images ==="
  cd "$PROJECT_ROOT"
  for svc in "${SERVICES[@]}"; do
    echo "Building aim-${svc}..."
    docker build -f deploy/docker/Dockerfile --build-arg SERVICE="$svc" -t "aim-${svc}:latest" .
  done
}

import_images() {
  echo "=== Importing images into k3s containerd ==="
  for svc in "${SERVICES[@]}"; do
    echo "Importing aim-${svc}..."
    docker save "aim-${svc}:latest" -o "/tmp/aim-${svc}.tar"
    echo "$SUDO_PASS" | sudo -S ctr --namespace k8s.io image import "/tmp/aim-${svc}.tar"
    rm "/tmp/aim-${svc}.tar"
  done
}

deploy_infra() {
  echo "=== Deploying infrastructure ==="
  kubectl apply -f "$K3S_ROOT/infrastructure/all.yaml"
  echo "Waiting for infrastructure..."
  kubectl wait --for=condition=available --timeout=120s deployment/aim-postgres 2>/dev/null || true
  kubectl wait --for=condition=available --timeout=120s deployment/aim-redis 2>/dev/null || true
  kubectl wait --for=condition=available --timeout=120s deployment/aim-etcd 2>/dev/null || true
  kubectl wait --for=condition=available --timeout=120s deployment/aim-kafka 2>/dev/null || true
  kubectl wait --for=condition=available --timeout=120s deployment/aim-minio 2>/dev/null || true
  kubectl wait --for=condition=available --timeout=120s deployment/aim-elasticsearch 2>/dev/null || true
  echo "Infrastructure ready"
}

init_db() {
  echo "=== Running database migrations ==="
  PG_POD=$(kubectl get pods -l app=aim-postgres -o jsonpath='{.items[0].metadata.name}')
  [ -z "$PG_POD" ] && { echo "PostgreSQL not running!"; exit 1; }
  for f in "$PROJECT_ROOT/migrations/postgres"/*.sql; do
    echo "Running $f..."
    kubectl exec "$PG_POD" -- psql -U aim -d aim -f - < "$f" 2>/dev/null
  done
  echo "Migrations complete"
}

init_etcd() {
  echo "=== Initializing etcd config center ==="
  kubectl delete job init-etcd-config 2>/dev/null || true
  kubectl create -f "$K3S_ROOT/jobs/init-etcd.yaml"
  kubectl wait --for=condition=complete --timeout=60s job/init-etcd-config 2>/dev/null || true
  echo "etcd config initialized"
}

deploy_services() {
  echo "=== Deploying all services ==="
  for svc in "${SERVICES[@]}"; do
    echo "Installing aim-${svc}..."
    helm install "aim-${svc}" "$K3S_ROOT/charts/aim-service/" \
      -f "$K3S_ROOT/values/staging/${svc}.yaml" 2>&1 | grep STATUS
  done
  echo "All services deployed"
  echo ""
  echo "=== Pod Status ==="
  sleep 5
  kubectl get pods | grep aim
}

deploy_ingress() {
  echo "=== Deploying Traefik Ingress ==="
  kubectl apply -f "$K3S_ROOT/infrastructure/ingress.yaml"
  echo "Ingress ready → http://$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[0].address}' 2>/dev/null || echo 'NODE_IP')"
}

start_forwards() {
  echo "=== Starting port forwards ==="
  nohup kubectl port-forward svc/aim-gateway 8080:8080 &>/dev/null &
  nohup kubectl port-forward svc/aim-ws-gateway 8081:8081 &>/dev/null &
  nohup kubectl port-forward svc/aim-minio 9000:9000 &>/dev/null &
  sleep 2
  echo "REST API:  http://localhost:8080"
  echo "WebSocket: ws://localhost:8081"
  echo "MinIO:     http://localhost:9000  (presigned upload/download)"
}

# --- Main ---
case "$ACTION" in
  build)
    build_images
    import_images
    ;;
  deploy)
    deploy_infra
    sleep 5
    init_db
    init_etcd
    deploy_services
    deploy_ingress
    start_forwards
    ;;
  all)
    build_images
    import_images
    deploy_infra
    sleep 5
    init_db
    init_etcd
    deploy_services
    deploy_ingress
    start_forwards
    ;;
  forward)
    start_forwards
    ;;
  *)
    echo "Usage: $0 {build|deploy|all|forward}"
    echo "  build   - Build images and import to containerd"
    echo "  deploy  - Deploy infrastructure + services + ingress to k3s"
    echo "  all     - Build + deploy everything"
    echo "  forward - Start port-forwards to localhost"
    exit 1
    ;;
esac

echo ""
echo "=== Done ==="
