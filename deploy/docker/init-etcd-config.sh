#!/bin/sh
# Initialize etcd config center for all AIM services.
# Usage:
#   Docker:  docker compose run --rm init-etcd-config
#   Local:   bash docker/init-etcd-config.sh local
set -e

ETCD_HOST="${1:-etcd}"
ETCD_URL="http://${ETCD_HOST}:2379"
ENDPOINT="--endpoints=${ETCD_URL}"

# Wait for etcd to be ready
echo "Waiting for etcd at ${ETCD_URL}..."
until etcdctl endpoint health ${ENDPOINT} 2>/dev/null; do
  sleep 1
done
echo "etcd is ready."

put() {
  local key="$1"
  local val="$2"
  echo "$val" | etcdctl put $ENDPOINT "$key" --ignore-lease
  echo "  ✓ $key"
}

echo "Setting config center values..."

# ---- user-service (50051) ----
put "aim-config-user.rpc" '{
  "database": {"dsn": "postgres://aim:aim123@postgres:5432/aim?sslmode=disable&search_path=user"},
  "redis": {"addr": "redis:6379"},
  "snowflake": {"workerId": 0}
}'

# ---- friend-service (50052) ----
put "aim-config-friend.rpc" '{
  "database": {"dsn": "postgres://aim:aim123@postgres:5432/aim?sslmode=disable&search_path=friend"},
  "redis": {"addr": "redis:6379"},
  "snowflake": {"workerId": 1}
}'

# ---- message-service (50053) ----
put "aim-config-message.rpc" '{
  "database": {"dsn": "postgres://aim:aim123@postgres:5432/aim?sslmode=disable&search_path=msg"},
  "redis": {"addr": "redis:6379"},
  "kafka": {"brokers": ["kafka:9092"]},
  "elasticsearch": {"addresses": ["http://elasticsearch:9200"]},
  "snowflake": {"workerId": 2}
}'

# ---- file-service (50054) ----
put "aim-config-file.rpc" '{
  "database": {"dsn": "postgres://aim:aim123@postgres:5432/aim?sslmode=disable&search_path=file"},
  "minio": {"endpoint": "minio:9000", "publicEndpoint": "localhost:9000", "accessKey": "minioadmin", "secretKey": "minioadmin123", "bucket": "aim"}
}'

# ---- conversation-service (50055) ----
put "aim-config-conversation.rpc" '{
  "database": {"dsn": "postgres://aim:aim123@postgres:5432/aim?sslmode=disable&search_path=conv"},
  "redis": {"addr": "redis:6379"},
  "kafka": {"brokers": ["kafka:9092"]},
  "snowflake": {"workerId": 3}
}'

# ---- llm-gateway (50056) ----
put "aim-config-llm-gateway.rpc" '{
  "database": {"dsn": "postgres://aim:aim123@postgres:5432/aim?sslmode=disable&search_path=llm"},
  "redis": {"addr": "redis:6379"}
}'

# ---- knowledge-base (50057) ----
put "aim-config-knowledge-base.rpc" '{
  "database": {"dsn": "postgres://aim:aim123@postgres:5432/aim?sslmode=disable&search_path=knowledge"},
  "redis": {"addr": "redis:6379"},
  "kafka": {"brokers": ["kafka:9092"]},
  "minio": {"endpoint": "minio:9000", "accessKey": "minioadmin", "secretKey": "minioadmin123", "bucket": "aim"},
  "milvus": {"address": "milvus:19530"},
  "neo4j": {"uri": "bolt://neo4j:7687", "enabled": true},
  "snowflake": {"workerId": 4}
}'

# ---- audit-service (50059) ----
put "aim-config-audit.rpc" '{
  "database": {"dsn": "postgres://aim:aim123@postgres:5432/aim?sslmode=disable&search_path=audit"},
  "kafka": {"brokers": ["kafka:9092"]},
  "minio": {"endpoint": "minio:9000", "accessKey": "minioadmin", "secretKey": "minioadmin123", "bucket": "aim"},
  "snowflake": {"workerId": 5}
}'

# ---- ai-bot-service (50062) ----
put "aim-config-ai-bot.rpc" '{
  "database": {"dsn": "postgres://aim:aim123@postgres:5432/aim?sslmode=disable&search_path=bot,conv"},
  "redis": {"addr": "redis:6379"},
  "kafka": {"brokers": ["kafka:9092"]},
  "milvus": {"host": "milvus", "port": 19530}
}'

# ---- bot-platform (8085) ----
put "aim-config-bot-platform.rpc" '{
  "database": {"dsn": "postgres://aim:aim123@postgres:5432/aim?sslmode=disable&search_path=bot"},
  "snowflake": {"workerId": 6}
}'

# ---- ws-gateway (50060 / WebSocket 8081) ----
put "aim-config-ws-gateway" '{
  "database": {"dsn": "postgres://aim:aim123@postgres:5432/aim?sslmode=disable&search_path=notify"},
  "redis": {"addr": "redis:6379"}
}'

# ---- signaling-service (50061) ----
put "aim-config-signaling-service.rpc" '{
  "database": {"dsn": "postgres://aim:aim123@postgres:5432/aim?sslmode=disable&search_path=notify,conv"},
  "redis": {"addr": "redis:6379"},
  "kafka": {"brokers": ["kafka:9092"]}
}'

# ---- gateway (8080) ----
put "aim-config-gateway" '{
  "redis": {"addr": "redis:6379"}
}'

echo ""
echo "Done! All config center values initialized."
