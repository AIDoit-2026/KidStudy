package content

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// bulkChunkRows 单条 INSERT 携带的最大行数。
//
// 一次导入 1.8 万篇故事（正文平均 7KB），行数太大单条语句会顶到参数上限，
// 太小又退化成逐行往返。200 行是实测下来比较平衡的档位。
const bulkChunkRows = 200

// execer 只暴露批量写入需要的 Exec，便于在连接池与事务之间复用同一套代码。
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// bulkUpsert 生成多行 INSERT ... ON CONFLICT，并按 chunk 切分执行。
//
// 为什么不用 sqlc：sqlc 生成的是单行语句，1.8 万行内容按单行写会多出几个数量级的
// 往返；COPY 又不能带 ON CONFLICT。折中办法就是在 repository 里手写这一段，
// 保持「重跑导入幂等」这一硬要求。
func bulkUpsert(ctx context.Context, db execer, table string, columns []string, rows [][]any, conflictCols []string, updateCols []string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	conflict := ""
	if len(conflictCols) > 0 {
		// PostgreSQL 不允许同一条语句里对同一冲突键 UPDATE 两次（21000），
		// 采集内容里确实存在正文完全相同的篇章，这里先按冲突键去重。
		rows = dedupeByConflict(columns, rows, conflictCols)

		conflict = " ON CONFLICT (" + strings.Join(conflictCols, ", ") + ")"
		if len(updateCols) > 0 {
			sets := make([]string, 0, len(updateCols))
			for _, c := range updateCols {
				sets = append(sets, c+" = EXCLUDED."+c)
			}
			conflict += " DO UPDATE SET " + strings.Join(sets, ", ")
		} else {
			conflict += " DO NOTHING"
		}
	}

	written := 0
	for start := 0; start < len(rows); start += bulkChunkRows {
		end := start + bulkChunkRows
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[start:end]

		// 拼 (…), (…), … 占位符
		placeholders := make([]string, 0, len(chunk))
		args := make([]any, 0, len(chunk)*len(columns))
		n := 1
		for _, row := range chunk {
			if len(row) != len(columns) {
				return written, fmt.Errorf("%s 批量写入列数不匹配: 期望 %d 实际 %d", table, len(columns), len(row))
			}
			ph := make([]string, 0, len(columns))
			for range columns {
				ph = append(ph, fmt.Sprintf("$%d", n))
				n++
			}
			placeholders = append(placeholders, "("+strings.Join(ph, ", ")+")")
			args = append(args, row...)
		}

		sql := "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES " +
			strings.Join(placeholders, ", ") + conflict

		tag, err := db.Exec(ctx, sql, args...)
		if err != nil {
			return written, fmt.Errorf("批量写入 %s 失败: %w", table, err)
		}
		written += int(tag.RowsAffected())
	}
	return written, nil
}

// dedupeByConflict 按冲突键去掉重复行，保留最后一条（后者覆盖前者）。
func dedupeByConflict(columns []string, rows [][]any, conflictCols []string) [][]any {
	idx := make([]int, 0, len(conflictCols))
	for _, c := range conflictCols {
		for i, col := range columns {
			if col == c {
				idx = append(idx, i)
				break
			}
		}
	}
	if len(idx) != len(conflictCols) {
		return rows // 冲突列不在列清单里，交给数据库报错更清楚
	}

	seen := make(map[string]int, len(rows))
	out := make([][]any, 0, len(rows))
	for _, row := range rows {
		key := ""
		for _, i := range idx {
			key += fmt.Sprint(row[i]) + "\x1f"
		}
		if pos, ok := seen[key]; ok {
			out[pos] = row // 同键取最后一条
			continue
		}
		seen[key] = len(out)
		out = append(out, row)
	}
	return out
}
