package database

import (
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/metrics"
)

var (
	DBOpenConnections = metrics.NewGaugeVec("db_open_connections",
		"Current open DB connections", "service")
	DBIdleConnections = metrics.NewGaugeVec("db_idle_connections",
		"Current idle DB connections", "service")
	DBInUseConnections = metrics.NewGaugeVec("db_in_use_connections",
		"Current in-use DB connections", "service")
	DBWaitCount = metrics.NewCounterVec("db_wait_count_total",
		"Total number of DB wait counts", "service")
)

type DB struct {
	*gorm.DB
}

func NewDB(cfg config.DatabaseConfig, log logx.Logger) (*DB, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("database DSN is required")
	}
	if cfg.Driver == "" {
		cfg.Driver = "postgres"
	}
	if cfg.MaxOpenConn == 0 {
		cfg.MaxOpenConn = 100
	}
	if cfg.MaxIdleConn == 0 {
		cfg.MaxIdleConn = 10
	}
	if cfg.MaxLifetime == 0 {
		cfg.MaxLifetime = 3600
	}

	var dialector gorm.Dialector
	switch cfg.Driver {
	case "postgres":
		dialector = postgres.Open(cfg.DSN)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	gormLog := logger.Default.LogMode(logger.Warn)
	db, err := gorm.Open(dialector, &gorm.Config{Logger: gormLog})
	if err != nil {
		return nil, fmt.Errorf("database open failed: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying sql.DB failed: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConn)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConn)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.MaxLifetime) * time.Second)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("database ping failed: %w", err)
	}

	CollectDBMetrics(db, "default")

	return &DB{db}, nil
}

func CollectDBMetrics(db *gorm.DB, service string) {
	sqlDB, err := db.DB()
	if err != nil {
		return
	}
	go func() {
		for {
			stats := sqlDB.Stats()
			DBOpenConnections.Set(float64(stats.OpenConnections), service)
			DBIdleConnections.Set(float64(stats.Idle), service)
			DBInUseConnections.Set(float64(stats.InUse), service)
			time.Sleep(15 * time.Second)
		}
	}()
}

func (d *DB) Close() error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (d *DB) Ping() error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}
