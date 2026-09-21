# KidStudy 项目长期约定

## Git 提交规范（强制）
- 每完成 1–2 项功能就提交，不等里程碑结束；一条提交只做一件事。
- 消息以 emoji 开头：✨feat 🐛fix 📝docs 🗃️db ♻️refactor ✅test 🔧chore 🚀perf。
- 提交前必须过 `go build ./...`（后端）或 `npm run build`（前端）。

## 架构与工程约束（详见 docs/开发设计文档.md）
- 后端按功能组织 + 三层 handler/service/repository/model：handler 不写业务，service 不 import
  `net/http`，跨模块只走 service 接口注入。
- 配置全来自环境变量、启动时集中校验，缺失即 fail-fast；只提交 `.env.example`。
- 错误用 `apperr` 类型化 + 全局映射，绝不返回堆栈；响应恒为 `{data, meta.request_id}`。
- 内容资产与脚本产出放 `var/`，冒烟脚本放 `server/tmp/`（均已 gitignored）。

## 已拍板的产品决策
- 关闭公开注册（`BOOTSTRAP_INVITE_CODE`）；故事只做亲子朗读，不做控字改写。
- 节奏只作基准线不设闸门；`answer_logs` 不分区；多孩对比按「学习日序号」对齐、只并列不排名。
- 每日完成 = 程序判客观题 + 家长确认主观项 + 兜底手动标记，`require_parent_confirm` 默认关。

## 本机开发注意
- 8080 被 Jenkins 占，API 用 `HTTP_ADDR=:18080`；本机有代理，curl 本机必须 `--noproxy '*'`。
- PostgreSQL 18.6（5432），库/角色 `kidstudy`；口令只在 `server/.env` 与 pgpass，不进命令行；
  Go 1.27.1；迁移 `cd server && go run ./cmd/api -migrate`。
- **端到端冒烟一律写成脚本再跑**（命令行里出现口令/长 Bearer 会被安全策略拦下）；Git Bash
  跑 `.sh` 会触发 wsl.exe 黑名单。
- 起服务前确认老进程已死：`netstat -ano | grep 18080` 取 PID 后 kill（否则打的是旧二进制）。
- 前端 `VITE_API_BASE_URL` 主机必须与访问前端的地址**同站**：Refresh Cookie 是 HttpOnly +
  `SameSite=Lax`，跨站 fetch 不带它 → 换页掉登录；localhost 与 127.0.0.1 是两个站，本机统一
  `localhost`（CORS 白名单同）。
- 前端工具链走托管 node 全路径 `...\22.22.2-3\`：npm 用
  `node <该目录>/node_modules/npm/bin/npm-cli.js`（`.cmd` shim 在 Git Bash 踩坑），registry 用
  npmmirror，独立缓存 `--cache web/.npm-cache`；浏览器自动化用同目录下
  `node_modules/agent-browser/bin/agent-browser.js`，命令串用 `batch` + stdin JSON 数组，
  **每个 batch 都要重新登录**（daemon 重建上下文会清 Cookie），且放后台跑。
- 从零入库的前端无法每笔都过 `vite build`：中间各笔只保证 `tsc --noEmit`，最后一笔跑全量
  build；add 顺序按依赖拓扑排，不能 `git add .` 一把梭。

## 技术栈
- 后端 Go + chi + pgx；连接层 sqlc，**不用 ORM**（本项目是批量导入 + 复杂查询，恰是 ORM 短板）。
- `sqlc.yaml` 必须 `sql_package: "pgx/v5"`，`schema` 只列 `migrations/*.up.sql`（含 down 会
  打乱解析）；生成物在 `internal/dbgen`（入库）。读查询走 sqlc，**批量写不走**（多行 upsert，
  200 行/语句 + 同批冲突键去重，否则 SQLSTATE 21000）。
- 前端 Vite + React 18 + TS + Tailwind（CSS 变量主题）+ TanStack Query + Zustand + RR6。

## 接口契约易错点
- `POST /auth/login` 只认 `{account, password}`（account 收邮箱或手机号）；注册才分 email/phone。
- `/parent/settings`：`session_limit_min` 只收 **5–120**（0 非法）；`daily_limit_min` 0–480、
  `rest_interval_min` 0–120 才允许 0。
- 健康检查在根路径 `/health`、`/ready`，**不在** `/api/v1` 下。

## 里程碑约定
细节见 docs/开发设计文档.md §10.x；只留容易再犯的坑：
- **越权一律 404**（不 403，避免暴露 ID 是否存在）；孩子删除是软归档。
- **内容即发布**：自有管线→published，采集故事→pending + 审核。
- **同 seed 题目必须完全一致**：`randx.Derive(seed, i)` 逐题派生（屏幕练习与打印同源）；
  `question_snapshot` 可下发、`answer_key` 永不下发；组卷用 `LoadMaterials` 批量装素材。
- **`daily_stats.subject_code=''` 是全天合计**，只有它的 `passed` 代表达标；日汇总 worker
  按 (孩子,日期,学科) 覆盖式重算。
- **算日期差必须用 `daysBetween`**：pgx 的 date 是 UTC 零点，本机 +08 直接相减差 8 小时。
- **打印预览 HTML 与 chromedp 的 PDF 是同一份字符串**；`POST /pdf` 只置 queued 返 202，由
  `cmd/worker -print` 消费（`FOR UPDATE SKIP LOCKED`）；`page_count` 读 PDF 真实页树。
- **前端**：Access Token 只存模块级内存（不进 localStorage/URL）；401 单飞刷新重放一次，
  4xx 不重试、5xx 最多 3 次退避；亮度用两层 fixed 遮罩而非 `filter`；护眼计时只在孩子端外壳；
  打印预览 `/print/:jobId` 不挂外壳。
