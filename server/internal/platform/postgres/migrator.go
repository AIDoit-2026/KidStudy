package postgres

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "github.com/jackc/pgx/v5/stdlib" // 为 migrate 提供 database/sql 驱动
)

// MigrationsTable 是版本记录表名。
const MigrationsTable = "schema_migrations"

// RunUp 执行全部未应用的迁移。没有新迁移时返回 nil（ErrNoChange 视为成功）。
func RunUp(databaseURL, migrationsPath string, fsys fs.FS, log *slog.Logger) error {
	m, err := newMigrate(databaseURL, migrationsPath, fsys)
	if err != nil {
		return err
	}
	start := time.Now()
	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			log.Info("数据库已是最新版本，无需迁移")
			return nil
		}
		return fmt.Errorf("执行迁移失败: %w", err)
	}
	log.Info("数据库迁移完成", "duration_ms", time.Since(start).Milliseconds())
	return nil
}

// Rollback 回退一步，仅用于本地开发与紧急回滚。
func Rollback(databaseURL, migrationsPath string, fsys fs.FS, log *slog.Logger) error {
	m, err := newMigrate(databaseURL, migrationsPath, fsys)
	if err != nil {
		return err
	}
	if err := m.Steps(-1); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			log.Info("没有可回退的迁移")
			return nil
		}
		return fmt.Errorf("回退迁移失败: %w", err)
	}
	log.Warn("已回退一步迁移")
	return nil
}

func newMigrate(databaseURL, migrationsPath string, fsys fs.FS) (*migrate.Migrate, error) {
	src, err := iofs.New(fsys, strings.TrimPrefix(migrationsPath, "./"))
	if err != nil {
		return nil, fmt.Errorf("加载迁移文件失败: %w", err)
	}

	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("DATABASE_URL 为空，无法执行迁移")
	}
	connDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("打开迁移连接失败: %w", err)
	}
	connDB.SetMaxOpenConns(2)

	driver, err := migratepgx.WithInstance(connDB, &migratepgx.Config{
		MigrationsTable: MigrationsTable,
		DatabaseName:    dbName(databaseURL),
	})
	if err != nil {
		_ = connDB.Close()
		return nil, fmt.Errorf("初始化迁移驱动失败: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		return nil, fmt.Errorf("创建迁移实例失败: %w", err)
	}
	return m, nil
}

// dbName 从连接串里取数据库名，供迁移驱动做锁与版本隔离。
func dbName(databaseURL string) string {
	idx := strings.LastIndex(databaseURL, "/")
	if idx < 0 || idx == len(databaseURL)-1 {
		return "postgres"
	}
	name := databaseURL[idx+1:]
	if q := strings.IndexByte(name, '?'); q >= 0 {
		name = name[:q]
	}
	if name == "" {
		return "postgres"
	}
	return name
}
