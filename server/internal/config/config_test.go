package config

import (
	"strings"
	"testing"
	"time"
)

// baseValid 返回一份通过校验的最小配置，供各用例在其上改单个字段。
func baseValid() Config {
	return Config{
		AppEnv:             EnvDev,
		HTTPAddr:           ":8080",
		BaseURL:            "http://localhost:8080",
		LogLevel:           "info",
		LogFormat:          "json",
		DatabaseURL:        "postgres://u:p@localhost:5432/db",
		DBMaxConns:         10,
		DBMinConns:         2,
		CORSAllowedOrigins: []string{"http://localhost:5173"},
		ReadTimeout:        15 * time.Second,
		WriteTimeout:       30 * time.Second,
		IdleTimeout:        60 * time.Second,
		ShutdownTimout:     15 * time.Second,

		AuthSecret:      strings.Repeat("a", 32),
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 720 * time.Hour,
		UnlockTokenTTL:  5 * time.Minute,
		QRCodeTTL:       60 * time.Second,
		ExchangeCodeTTL: 30 * time.Second,

		StorageDir:       "../var/storage",
		PDFRenderTimeout: 60 * time.Second,
		PrintRetention:   720 * time.Hour,

		RateLimitEnabled:     true,
		RateLimitLoginPerMin: 12, RateLimitLoginBurst: 6,
		RateLimitQRPerMin: 120, RateLimitQRBurst: 40,
		RateLimitPrintPerMin: 30, RateLimitPrintBurst: 10,
	}
}

func TestValidate_BaseIsClean(t *testing.T) {
	if err := baseValid().Validate(); err != nil {
		t.Fatalf("基础配置应通过，却报错：%v", err)
	}
}

func TestValidate_RateLimit(t *testing.T) {
	t.Run("关闭时不校验各档数值", func(t *testing.T) {
		c := baseValid()
		c.RateLimitEnabled = false
		c.RateLimitLoginPerMin = 0
		c.RateLimitLoginBurst = 0
		if err := c.Validate(); err != nil {
			t.Fatalf("关闭限流后不应因数值为 0 报错：%v", err)
		}
	})

	t.Run("启用时逐档校验", func(t *testing.T) {
		c := baseValid()
		c.RateLimitQRPerMin = 0
		c.RateLimitPrintBurst = -1
		err := c.Validate()
		if err == nil {
			t.Fatal("非法限流参数应报错")
		}
		msg := err.Error()
		if !strings.Contains(msg, "RATE_LIMIT_QR_PER_MIN") {
			t.Errorf("错误信息应点出 RATE_LIMIT_QR_PER_MIN：%s", msg)
		}
		if !strings.Contains(msg, "RATE_LIMIT_PRINT_BURST") {
			t.Errorf("错误信息应点出 RATE_LIMIT_PRINT_BURST：%s", msg)
		}
		// 登录档参数合法，不该出现在错误里 —— 顺带验证「只报有问题的档」
		if strings.Contains(msg, "RATE_LIMIT_LOGIN") {
			t.Errorf("登录档参数合法，不应出现在错误里：%s", msg)
		}
	})
}
