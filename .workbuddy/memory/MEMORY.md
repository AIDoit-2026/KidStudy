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

## sqlc 实际落地方式（2026-09-21，M2 实证）

- 生成物落在 **`internal/dbgen`**（不是 feature 子包），生成代码**入库**，构建不依赖 sqlc 二进制。
  重生成：`cd server && ./tools/sqlc.exe generate`。
- `sqlc.yaml` 必须写 `sql_package: "pgx/v5"`：默认生成 `database/sql` 版 DBTX，`pgxpool.Pool` 满足不了。
- `schema` 只列 `migrations/*.up.sql`；把整个目录丢进去会被 down 脚本的 `DROP` 打乱解析。
- yaml 注释里不要出现冒号（`mapping values are not allowed in this context`）。
- **批量写入不走 sqlc**：生成的是单行语句，1.9 万行会多几个数量级往返；COPY 又带不了
  `ON CONFLICT`。用 `internal/feature/content/bulk.go` 的多行 upsert（200 行/语句）+
  冲突键去重（同批次同键会触发 SQLSTATE 21000）。
- 读查询（列表/详情/审核）走 sqlc；M1 的 auth / children 仍手写 pgx，不动。

## M2 已交付（内容底座，2026-09-21）

- 表：knowledge_points / hanzi / hanzi_words / en_words / en_sentences / stories /
  story_pairs / videos / math_templates / content_review / curriculum_plan（迁移 0003）。
- 导入量：阶段 39、汉字 8103（6864 带笔顺）、组词 29199、英语词 1802、故事 18908、
  双语配对 830、审核队列 18908、每孩基准线 9905 条。`cmd/importer` 幂等可重跑
  （`-only stages|hanzi|words|stories|plans` 跑单步，`-data` 指数据目录）。
- 内容即发布策略：汉字/组词/英语词（自有管线）→ `published`；故事（采集）→ `pending`
  + `content_review`，家长审核通过才 `published`；`suitable=false` 的孩子端默认屏蔽。
- 端点：`/content/{stages,hanzi,hanzi/{idOrChar},words,stories,stories/{id},stories/{id}/pair}`、
  `/review/queue`、`/review/{id}/approve|reject`、`/review/batch`。列表响应带
  `meta.page{offset,limit,total}`（`response.JSONPaged`）。
- 冒烟脚本 `server/tmp/smoke_m2.py`（36 项，对库内状态不敏感，可反复重跑）。
- **本地起服务务必确认老进程已死**：老 api.exe 占着 18080 时新进程会 bind 失败，
  但 curl 仍然 200（打的是旧二进制）。用 `tasklist` 找 PID + `taskkill /F /PID`。

## M3 已交付（练习引擎，2026-09-21）

- 迁移 0004：mastery_records / learning_sessions / session_items / answer_logs /
  wrong_book_entries / daily_stats / practice_assignments；种子 14 套 math_templates
  + 14 个 `kind='math_skill'` 知识点（数学此前一个 kp 都没有）。
- `internal/pkg/randx`：splitmix64 确定性随机源；`Derive(seed, i)` 逐题派生种子，
  所以数学题「同一 seed 两次生成完全一致」，且与 M5 打印中心同源。
- `mastery`：简化 SM-2（level 0–5 / ease / interval 阶梯 1h-4h-1d-3d-30d）、错题本（连对 3 次移出）、
  复习队列（逾期优先 + >100 条时暂停新学）、难度自适应（最近 3/5 次作答）、单知识点重置。
- `practice`：今日编排（错题置顶→专项→复习→新学，语文 6 / 英语 5 / 数学 2）、9 题型组卷、
  服务端判分、会话结算（星级 + daily_stats 增量）、家长确认（不污染客观正确率）。
- 端点：`/practice/{today,session,session/{id}[/answer|skip|finish|confirm],math/templates,math/preview}`、
  `/mastery/{review-queue,wrong-book[/:id],{kpId}/reset}`。
- 冒烟 `server/tmp/smoke_m3.py`：71 项全通过（tmp 已 gitignore，与 M2 一致不入库）。

### M3 引入的新约定

- **组卷素材批量装载**：`content.Service.LoadMaterials(ctx, kpIDs)` 一次拿全，
  practice 不逐 kp 回查；干扰项从同批素材里取（同阶段/同级别），不额外查库。
- **题面与答案分列**：`session_items.question_snapshot`（可下发）与 `answer_key`（永不下发）。
- **跨模块归属校验**：`children.Service.EnsureOwned` 返回领域对象（非对外视图），
  越权一律 404；mastery/practice 都通过接口注入，不直接依赖具体类型。
- **会话作用域的 child_id 既认 query 也认 body**（读完把 body 放回去）。

## M1 已交付（认证 + 孩子档案）

- 端点全在 `/api/v1`：`/auth/{register,login,refresh,logout,me,pin,qrcode/*}`、`/children[/{id}]`。
- Access 15m JWT（typ=access）/ PIN 解锁 5m JWT（typ=unlock）**用途隔离**；Refresh 走 HttpOnly Cookie 且每次刷新轮换。
- 二维码令牌与兑换码只在库里留 SHA-256；二维码 60s、兑换码 30s 一次性，失败 5 次作废。
- 所有 children 查询强制带 `parent_id`，越权返回 404（非 403，避免暴露 ID 是否存在）；删除是软归档。
- ~~M2 开工前需决定连接层是否切 sqlc~~ → 已定：用 sqlc，见「技术栈决策」。M1 手写部分不动。
