// Package migrations 仅用于把 SQL 迁移文件嵌入二进制，避免运行时依赖文件路径。
package migrations

import "embed"

// FS 包含本目录下全部 *.sql 迁移文件。
//
//go:embed *.sql
var FS embed.FS
