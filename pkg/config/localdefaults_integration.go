//go:build integration

package config

import "os"

// localDefaults are the loopback addresses a developer machine uses for the
// integration tests' real Postgres/Redis/... instances. Service config files
// are environment-templated (see docs/adr/0003), so tests must supply the
// placeholders they reference.
var localDefaults = map[string]string{
	"POSTGRES_DSN_BASE":                 "postgres://aim:aim123@localhost:5432/aim",
	"REDIS_ADDR":                        "localhost:6379",
	"KAFKA_BROKERS":                     "[localhost:9092]",
	"MINIO_ENDPOINT":                    "localhost:9000",
	"MINIO_ACCESS_KEY":                  "minioadmin",
	"MINIO_SECRET_KEY":                  "minioadmin123",
	"OTEL_ENDPOINT":                     "localhost:4317",
	"MILVUS_HOST":                       "localhost",
	"MILVUS_PORT":                       "19530",
	"NEO4J_URI":                         "bolt://localhost:7687",
	"NEO4J_USER":                        "neo4j",
	"NEO4J_PASSWORD":                    "password123",
	"JWT_SECRET":                        "aim-dev-secret-key",
	"MINIO_PUBLIC_ENDPOINT":             "localhost:9000",
	"ELASTICSEARCH_ADDRESSES":           "[http://localhost:9200]",
	"MINERU_URL":                        "http://localhost:30000",
	"USER_SERVICE_ADDR":                 "dns:///localhost:50051",
	"MESSAGE_SERVICE_ADDR":              "dns:///localhost:50053",
	"FILE_SERVICE_ADDR":                 "dns:///localhost:50054",
	"BOT_SERVICE_ADDR":                  "dns:///localhost:50058",
	"BOT_RUNTIME_ADDR":                  "dns:///localhost:50058",
	"BOT_METRICS_PORT":                  "9109",
	"BOT_ENCRYPTION_KEY":                "0123456789abcdef0123456789abcdef",
	"KNOWLEDGE_BASE_ADDR":               "dns:///localhost:50057",
	"LLM_GATEWAY_ADDR":                  "dns:///localhost:50056",
	"REALTIME_SERVICE_ADDR":             "dns:///localhost:50059",
	"REALTIME_HEARTBEAT_INTERVAL":       "30",
	"REALTIME_REGISTRY_TTL_SECONDS":     "90",
	"REALTIME_INSTANCE_ID":              "",
	"REALTIME_READINESS_DELAY_SECONDS":  "5",
	"REALTIME_DRAIN_SECONDS":            "30",
	"REALTIME_WRITE_TIMEOUT_SECONDS":    "5",
	"REALTIME_SHUTDOWN_TIMEOUT_SECONDS": "40",
	"ENC_KEY":                           "Ay+h5wU31Vfy5gITlP1P2cmNtOPkTsnqIupXHqpgutw=",
}

// SetLocalDefaults sets any unset environment variable referenced by the
// service config templates to its loopback value, so tests can load the same
// templates the deployments do.
func SetLocalDefaults() {
	for key, value := range localDefaults {
		if _, ok := os.LookupEnv(key); !ok {
			_ = os.Setenv(key, value)
		}
	}
}
