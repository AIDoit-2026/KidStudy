// Package postgres 管理数据库连接池与 schema 迁移。
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kidstudy/internal/config"
)

// DB 是对连接池的薄封装，业务层只依赖它提供的能力。
type DB struct {
	pool *pgxpool.Pool
}

// Open 创建连接池并立即 Ping。Ping 失败视为启动失败（fail-fast），
// 避免服务带着不可用的数据库对外提供假的健康状态。
func Open(ctx context.Context, cfg config.Config) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("解析 DATABASE_URL 失败: %w", err)
	}
	poolCfg.MaxConns = int32(cfg.DBMaxConns)
	poolCfg.MinConns = int32(cfg.DBMinConns)
	poolCfg.MaxConnLifetime = time.Hour
	poolCfg.MaxConnIdleTime = 10 * time.Minute
	poolCfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("创建连接池失败: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("数据库连通性检查失败: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Pool 暴露底层池，供仓储层（含 sqlc 生成的 Querier）使用。
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// Ping 用于就绪探针。
func (db *DB) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return db.pool.Ping(ctx)
}

// Stats 暴露池状态，便于日志与健康检查展示。
func (db *DB) Stats() *pgxpool.Stat { return db.pool.Stat() }

// Close 关闭连接池。
func (db *DB) Close() { db.pool.Close() }

// HealthChecker 是 /ready 探针依赖的最小接口，便于测试替换。
type HealthChecker interface {
	Ping(ctx context.Context) error
}

// LogStats 输出池状态，便于排查连接泄漏。
func (db *DB) LogStats(log *slog.Logger) {
	s := db.pool.Stat()
	log.Debug("postgres pool stats",
		"acquired", s.AcquiredConns(),
		"idle", s.IdleConns(),
		"total", s.TotalConns(),
		"max", s.MaxConns(),
	)
}
