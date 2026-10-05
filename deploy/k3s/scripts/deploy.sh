#!/bin/bash
# AIM k3s 一键部署脚本
# Usage: ./deploy/k3s/scripts/deploy.sh [build|deploy|all]
set -e

ACTION="${1:-all}"
SUDO_PASS="${SUDO_PASS:-}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
K3S_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PROJECT_ROOT="$(cd "$K3S_ROOT/.." && pwd)"

SERVICES=(user-service message-service file-service
           llm-gateway knowledge-base bot-service
           realtime-service gateway)
WORKLOADS=("${SERVICES[@]}" bot-runtime knowledge-ingest)

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
  kubectl wait --for=condition=available --timeout=120s deployment/aim-kafka 2>/dev/null || true
  kubectl wait --for=condition=available --timeout=120s deployment/aim-minio 2>/dev/null || true
  kubectl wait --for=condition=available --timeout=120s deployment/aim-elasticsearch 2>/dev/null || true
  echo "Infrastructure ready"
}

deploy_services() {
  # Schema migrations are not replayed from SQL files here: every service runs the
  # versioned migrations (migrations/postgres, advisory-locked) on startup, so an
  # untracked replay would only re-apply 000 against an already-migrated database.
  echo "=== Deploying all services ==="
  : "${INGEST_EMBEDDING_TOKEN:?Set the private ingestion quota token before deploying}"
  kubectl create secret generic aim-ingest-embedding \
    --from-literal=token="$INGEST_EMBEDDING_TOKEN" \
    --dry-run=client -o yaml | kubectl apply -f -
  for wl in "${WORKLOADS[@]}"; do
    # Secondary roles reuse their domain's values file and are instantiated with
    # explicit --set overrides instead of a dedicated values file.
    case "$wl" in
      bot-runtime)
        values_file=bot-service.yaml
        set_args=(
          --set config.name=bot-runtime
          --set image.name=bot-service
          --set 'args={-f,/app/etc/bot.yaml,-role,runtime}'
          --set service.metricsPort=9119
          --set config.extraEnv.BOT_METRICS_PORT=9119
        )
        ;;
      knowledge-ingest)
        values_file=knowledge-base.yaml
        set_args=(
          --set config.name=knowledge-ingest
          --set image.name=knowledge-base
          --set 'args={-f,/app/etc/knowledge-base.yaml,-role,ingest}'
          --set service.grpcPort=null
          --set service.httpPort=9118
          --set service.metricsPort=9118
          --set probes.httpPort=9118
          --set config.secretEnv.INGEST_EMBEDDING_TOKEN.name=aim-ingest-embedding
          --set config.secretEnv.INGEST_EMBEDDING_TOKEN.key=token
        )
        ;;
      *)
        values_file="${wl}.yaml"
        set_args=()
        ;;
    esac
    echo "Installing aim-${wl}..."
    helm install "aim-${wl}" "$K3S_ROOT/charts/aim-service/" \
      -f "$K3S_ROOT/values/staging/${values_file}" "${set_args[@]}" 2>&1 | grep STATUS
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
  nohup kubectl port-forward svc/aim-realtime-service 8081:8081 &>/dev/null &
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
    deploy_services
    deploy_ingress
    start_forwards
    ;;
  all)
    build_images
    import_images
    deploy_infra
    sleep 5
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
