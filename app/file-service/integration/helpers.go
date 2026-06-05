//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/database"
	goredis "github.com/redis/go-redis/v9"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func newDB(driver, dsn string) *database.DB {
	cfg := config.DatabaseConfig{
		Driver:      driver,
		DSN:         dsn,
		MaxOpenConn: 5,
		MaxIdleConn: 2,
		MaxLifetime: 60,
	}
	db, err := database.NewDB(cfg, nil)
	if err != nil {
		log.Fatalf("integration test DB init failed: %v", err)
	}
	return db
}

func newRedisClient(addr string) *goredis.Client {
	rdb := goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: "",
		DB:       1,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("integration test Redis init failed: %v", err)
	}
	return rdb
}

func etcdRawConfig(serviceName string) []byte {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"localhost:2379"},
		DialTimeout: 3 * time.Second,
	})
	if err != nil {
		log.Printf("etcd not available, using local config: %v", err)
		return nil
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := "aim-config-" + serviceName
	resp, err := cli.Get(ctx, key)
	if err != nil || len(resp.Kvs) == 0 {
		log.Printf("no etcd config for %s, using local config", serviceName)
		return nil
	}

	var raw any
	if err := json.Unmarshal(resp.Kvs[0].Value, &raw); err != nil {
		return nil
	}
	clean, _ := json.Marshal(raw)
	return clean
}

func strPtr(s string) *string { return &s }
