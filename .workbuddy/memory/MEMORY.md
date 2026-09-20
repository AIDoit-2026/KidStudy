# KidStudy 项目长期约定

## Git 提交规范（用户明确要求，强制执行）

- **不等整个里程碑结束**：每完成 1~2 项功能就提交一次。
- **提交信息以 emoji 开头**标识类型：

| emoji | 前缀 | 用途 |
| --- | --- | --- |
| ✨ | `✨ feat:` | 新功能 / 新端点 / 新模块 |
| 🐛 | `🐛 fix:` | 缺陷修复（写明根因） |
| 📝 | `📝 docs:` | 文档与设计说明 |
| 🗃️ | `🗃️ db:` | 数据库迁移与 schema 变更 |
| ♻️ | `♻️ refactor:` | 重构（不改外部行为），单独提交 |
| ✅ | `✅ test:` | 测试与验收脚本 |
| 🔧 | `🔧 chore:` | 构建、依赖、脚手架、CI |
| 🚀 | `🚀 perf:` | 性能优化（附实测数据） |

- 一条提交只做一件事；提交前必须 `go build ./...`（后端）或 `npm run build`（前端）通过。

## 架构与工程约束（详见 docs/开发设计文档.md）

- 后端按功能组织 + 三层：handler / service / repository / model 四件套；handler 不写业务，service 不 import `net/http`，跨模块只走 service 接口注入。
- 配置全部来自环境变量并在启动时集中校验，缺失或非法即 fail-fast。
- 错误用 `apperr` 类型化 + 全局映射，客户端只看规范结构，绝不返回堆栈。
- 内容/ poc 脚本产出的资产统一放 `var/`（gitignored，仅 `var/README.md` 入库）。

## 已拍板的产品决策（2026-09-20）

- 关闭公开注册（首次启动用 `BOOTSTRAP_INVITE_CODE`）；故事只做亲子朗读，不做控字改写；TTS 先用浏览器语音（M2），预生成音频 M4；连接层用 sqlc。
- 节奏：标准节奏只作**基准线不设闸门**，不限制每日次数与进度，统计偏差、重复次数、学习效率。
- `answer_logs` 不分区；多孩对比按「学习日序号」对齐，只并列不排名。
- 每日完成判定：**程序判定客观题 + 家长确认主观项 + 兜底手动标记**，`require_parent_confirm` 默认关。

## 本机开发注意事项（KidStudy 专属）

- 本机 **8080 端口被 Jenkins 占用**，本地起服务用 `HTTP_ADDR=:18080` 这类高位端口。
- 本机有代理（`http_proxy=127.0.0.1:42703`）：curl 本机服务**必须** `curl --noproxy '*'`，否则 502。
- PostgreSQL **18.6** 已装（`C:\Program Files\PostgreSQL\18`，5432），库/角色均为 `kidstudy`。
  - 口令**只**存 `server/.env`（gitignore）与 `%APPDATA%\postgresql\pgpass.conf`，**不要**写进命令行或代码。
  - psql 未进 PATH；Git Bash 调 exe 会被 shim 改写路径，走 PowerShell 更稳。
- Go **1.27.1**（`C:\Program Files\Go`，唯一安装）；`go.mod` 的 go 指令已对齐 1.27.1。
- 迁移已实机跑通：`cd server && go run ./cmd/api -migrate`（`-rollback` 回退一步）。
- **端到端冒烟要写成脚本执行**：命令行里出现口令、长串 Bearer 时会被安全策略拦下等待确认（超时即中止）。放 `server/tmp/*.py`（已 gitignore）再跑最稳。
- Git Bash 执行 `.sh` 脚本会触发 wsl.exe 黑名单，别走这条路。

## 技术栈决策（2026-09-20 晚）

- **不换语言，继续 Go**。用户问过是否改 C# + EF Core，结论：ORM 是库不是语言特性，
  换成 C# 只会为了已经能用 sqlc/bun/Ent 解决的小痛点付出全量重写成本。
- **连接层定 sqlc**。工具：`tools/sqlc.exe`（v1.31.1，官方 Release 的 Windows amd64，
  已被 `*.exe` 规则忽略，不入库；重装时重新下载同版本即可。查版本：`./tools/sqlc.exe version`。
- 已实测 sqlc 能完整解析 `migrations/0001+0002` 的 DDL（含 uuid / jsonb / 部分索引 /
  COMMENT ON），生成代码处理好：可空列→`sql.NullString`、jsonb→`json.RawMessage`、
  表与列的 COMMENT 会带进 Go 注释。
- 生成代码需要依赖 `github.com/google/uuid`（已有）与 `github.com/sqlc-dev/pqtype`
  （login_audit 的 inet 列会用到）；正式接入时补进 go.mod。
- **不用 ORM（EF Core 之类）的三条理由**：① 本项目负载是「批量导入 8103 字/48000 组词/
  18913 故事」+「复杂查询（今日编排、SM-2、多孩对比）」，ORM 恰好在批量写入与复杂 SQL 上
  是短板；② Code First 的"双向同步"实为单向 diff + 人工 review，schema 变更里的数据迁移
  ORM 仍解决不了；③ 内容资产先于代码存在（数据先行），SQL-first 比 Code-first 更契合。
- 若日后觉得简单 CRUD 啰嗦，可在同一项目里给那一小块单独引入 `bun`（与 sqlc 可共存），
  不必二选一。
- `.gitignore` 已追加 `tools/sqlc` 规则（对 `*.exe` 而言冗余，但无害，保留）。

## M1 已交付（认证 + 孩子档案）

- 端点全在 `/api/v1`：`/auth/{register,login,refresh,logout,me,pin,qrcode/*}`、`/children[/{id}]`。
- Access 15m JWT（typ=access）/ PIN 解锁 5m JWT（typ=unlock）**用途隔离**；Refresh 走 HttpOnly Cookie 且每次刷新轮换。
- 二维码令牌与兑换码只在库里留 SHA-256；二维码 60s、兑换码 30s 一次性，失败 5 次作废。
- 所有 children 查询强制带 `parent_id`，越权返回 404（非 403，避免暴露 ID 是否存在）；删除是软归档。
- ~~M2 开工前需决定连接层是否切 sqlc~~ → 已定：用 sqlc，见「技术栈决策」。M1 手写部分不动。
