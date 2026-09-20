// Package logger 提供结构化日志（JSON 为主），并保证 request_id 贯穿所有日志。
package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"kidstudy/internal/config"
)

// New 按配置构造 logger。生产默认 JSON，本地可用 text 便于肉眼阅读。
func New(cfg config.Config) *slog.Logger {
	var w io.Writer = os.Stdout
	opts := &slog.HandlerOptions{
		Level:     cfg.LogLevelValue(),
		AddSource: cfg.AppEnv != config.EnvProd,
	}

	var h slog.Handler
	if strings.EqualFold(cfg.LogFormat, "text") {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}

	return slog.New(h)
}
