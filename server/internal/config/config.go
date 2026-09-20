// Package config 集中管理服务配置。
//
// 铁律：配置只来自环境变量（本地可选从 .env 读取），并且在启动时集中校验，
// 任何缺失或非法都直接 fail-fast，绝不在运行期才暴露配置错误。
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Environment 运行环境。
type Environment string

const (
	EnvDev     Environment = "development"
	EnvStaging Environment = "staging"
	EnvProd    Environment = "production"
)

// Config 是服务运行所需的全部配置。
type Config struct {
	// 服务
	AppEnv       Environment `env:"APP_ENV" envDefault:"development"`
	HTTPAddr     string      `env:"HTTP_ADDR" envDefault:":8080"`
	BaseURL      string      `env:"BASE_URL" envDefault:"http://localhost:8080"`
	TrustedProxy bool        `env:"TRUSTED_PROXY" envDefault:"false"`
	Version      string      `env:"APP_VERSION" envDefault:"dev"`

	// 日志
	LogLevel  string `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat string `env:"LOG_FORMAT" envDefault:"json"` // json | text

	// 数据库
	DatabaseURL string `env:"DATABASE_URL"`
	DBMaxConns  int32  `env:"DB_MAX_CONNS" envDefault:"10"`
	DBMinConns  int32  `env:"DB_MIN_CONNS" envDefault:"2"`

	// CORS：生产禁用通配符
	CORSAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS" envDefault:"http://localhost:5173" envSeparator:","`

	// 时间与停机
	ReadTimeout    time.Duration `env:"HTTP_READ_TIMEOUT" envDefault:"15s"`
	WriteTimeout   time.Duration `env:"HTTP_WRITE_TIMEOUT" envDefault:"30s"`
	IdleTimeout    time.Duration `env:"HTTP_IDLE_TIMEOUT" envDefault:"60s"`
	ShutdownTimout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`

	// 认证：令牌密钥与有效期。密钥只从环境变量来，绝不写进代码库
	AuthSecret      string        `env:"AUTH_SECRET"`
	AccessTokenTTL  time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTokenTTL time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"720h"` // 30 天
	UnlockTokenTTL  time.Duration `env:"UNLOCK_TOKEN_TTL" envDefault:"5m"`    // PIN 解锁短令牌
	QRCodeTTL       time.Duration `env:"QR_CODE_TTL" envDefault:"60s"`
	ExchangeCodeTTL time.Duration `env:"EXCHANGE_CODE_TTL" envDefault:"30s"`

	// 关闭公开注册：仅凭邀请码建号（首次启动用 BOOTSTRAP_INVITE_CODE）
	BootstrapInviteCode string `env:"BOOTSTRAP_INVITE_CODE"`
}

// Load 读取环境变量并校验。本地存在 .env 时优先加载（文件不存在则忽略）。
func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// 本地没有 .env 属正常情况；读取权限/格式错误才是问题
		return Config{}, fmt.Errorf("load dotenv: %w", err)
	}

	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, fmt.Errorf("parse env: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// IsProd 是否生产环境。
func (c Config) IsProd() bool { return c.AppEnv == EnvProd }

// LogLevelValue 返回 slog 可识别的日志级别。
func (c Config) LogLevelValue() slog.Level {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(c.LogLevel)); err != nil {
		return slog.LevelInfo
	}
	return lvl
}

// Validate 集中校验：把所有问题一次性汇总返回，方便一次性修完。
func (c Config) Validate() error {
	var errs []string

	switch c.AppEnv {
	case EnvDev, EnvStaging, EnvProd:
	default:
		errs = append(errs, fmt.Sprintf("APP_ENV 非法: %q（可选 development|staging|production）", c.AppEnv))
	}

	if strings.TrimSpace(c.HTTPAddr) == "" {
		errs = append(errs, "HTTP_ADDR 不能为空")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		errs = append(errs, "BASE_URL 不能为空")
	}

	switch c.LogFormat {
	case "json", "text":
	default:
		errs = append(errs, fmt.Sprintf("LOG_FORMAT 非法: %q（可选 json|text）", c.LogFormat))
	}
	if _, ok := parseLevel(c.LogLevel); !ok {
		errs = append(errs, fmt.Sprintf("LOG_LEVEL 非法: %q（可选 debug|info|warn|error）", c.LogLevel))
	}

	if strings.TrimSpace(c.DatabaseURL) == "" {
		errs = append(errs, "DATABASE_URL 不能为空")
	}
	if c.DBMinConns <= 0 {
		errs = append(errs, "DB_MIN_CONNS 必须大于 0")
	}
	if c.DBMaxConns < c.DBMinConns {
		errs = append(errs, fmt.Sprintf("DB_MAX_CONNS(%d) 不能小于 DB_MIN_CONNS(%d)", c.DBMaxConns, c.DBMinConns))
	}

	if c.IsProd() {
		for _, o := range c.CORSAllowedOrigins {
			if strings.TrimSpace(o) == "*" {
				errs = append(errs, "生产环境禁止使用通配符来源 CORS_ALLOWED_ORIGINS=*")
			}
		}
	}
	for _, o := range c.CORSAllowedOrigins {
		if strings.TrimSpace(o) == "" {
			errs = append(errs, "CORS_ALLOWED_ORIGINS 含空项")
		}
	}

	if c.ReadTimeout <= 0 || c.WriteTimeout <= 0 || c.IdleTimeout <= 0 || c.ShutdownTimout <= 0 {
		errs = append(errs, "HTTP 超时与 SHUTDOWN_TIMEOUT 必须为正值")
	}

	// 认证：密钥缺失或过短一律拒绝启动，避免用弱密钥签发令牌
	if len(c.AuthSecret) < 32 {
		errs = append(errs, fmt.Sprintf("AUTH_SECRET 缺失或过短（当前 %d 字符，至少 32）", len(c.AuthSecret)))
	}
	if c.AccessTokenTTL <= 0 {
		errs = append(errs, "ACCESS_TOKEN_TTL 必须为正值")
	}
	if c.RefreshTokenTTL <= c.AccessTokenTTL {
		errs = append(errs, "REFRESH_TOKEN_TTL 必须大于 ACCESS_TOKEN_TTL")
	}
	if c.UnlockTokenTTL <= 0 {
		errs = append(errs, "UNLOCK_TOKEN_TTL 必须为正值")
	}
	if c.QRCodeTTL <= 0 || c.QRCodeTTL > 5*time.Minute {
		errs = append(errs, "QR_CODE_TTL 必须为正值且不超过 5m")
	}
	if c.ExchangeCodeTTL <= 0 || c.ExchangeCodeTTL > c.QRCodeTTL {
		errs = append(errs, "EXCHANGE_CODE_TTL 必须为正值且不超过 QR_CODE_TTL")
	}

	if len(errs) > 0 {
		return errors.New("配置校验失败:\n  - " + strings.Join(errs, "\n  - "))
	}
	return nil
}

func parseLevel(s string) (slog.Level, bool) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToLower(s))); err != nil {
		return slog.LevelInfo, false
	}
	return lvl, true
}
