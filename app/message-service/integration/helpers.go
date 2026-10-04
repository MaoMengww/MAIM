//go:build integration

package integration

import (
	"context"
	"log"

	"github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/database"
	goredis "github.com/redis/go-redis/v9"
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
