# KidStudy 项目长期约定

## Git 提交规范（强制）
- 每完成 1–2 项功能就提交，不等里程碑结束；一条提交只做一件事。
- 消息以 emoji 开头：✨feat 🐛fix 📝docs 🗃️db ♻️refactor ✅test 🔧chore 🚀perf。
- 提交前必须过 `go build ./...`（后端）或 `npm run build`（前端）。

## 架构与工程约束（详见 docs/开发设计文档.md）
- 后端按功能组织 + 三层：handler / service / repository / model。handler 不写业务，
  service 不 import `net/http`，跨模块只走 service 接口注入。
- 配置全来自环境变量并在启动时集中校验，缺失即 fail-fast；只提交 `.env.example`。
- 错误用 `apperr` 类型化 + 全局映射，绝不返回堆栈；响应恒为 `{data, meta.request_id}`。
- 脚本与内容资产统一放 `var/`（gitignored，仅 README 入库）；冒烟脚本放 `server/tmp/`。

## 已拍板的产品决策
- 关闭公开注册（`BOOTSTRAP_INVITE_CODE`）；故事只做亲子朗读，不做控字改写。
- 节奏只作基准线不设闸门，不限每日次数与进度；`answer_logs` 不分区。
- 多孩对比按「学习日序号」对齐，只并列不排名。
- 每日完成 = 程序判客观题 + 家长确认主观项 + 兜底手动标记，`require_parent_confirm` 默认关。

## 本机开发注意事项
- 8080 被 Jenkins 占，本地起 API 用 `HTTP_ADDR=:18080`。本机有代理，
  curl 本机**必须** `curl --noproxy '*'`，否则 502。
- PostgreSQL 18.6（5432），库/角色均为 `kidstudy`；口令只在 `server/.env` 与 pgpass，
  不进命令行与代码；psql 未进 PATH，走 PowerShell 更稳。
- Go 1.27.1；迁移：`cd server && go run ./cmd/api -migrate`（`-rollback` 回退一步）。
- **端到端冒烟一律写成脚本再跑**（`server/tmp/*.py`，已 gitignore）：命令行里出现口令或
  长 Bearer 会被安全策略拦下等待确认。Git Bash 执行 `.sh` 会触发 wsl.exe 黑名单。
- 起服务前确认老进程已死（老 api.exe 占着 18080 时 curl 仍打旧二进制）：
  `netstat -ano | grep 18080` 取 PID 后 kill。
- 前端 `web/.env` 的 `VITE_API_BASE_URL` 主机必须与**访问前端的地址同站**：
  后端 Refresh 是 HttpOnly + `SameSite=Lax`，跨站的 fetch 不会带它 → 换页即掉登录。
  localhost 与 127.0.0.1 在浏览器眼里是**两个站**，故本机统一 `localhost`
  （CORS 白名单也配 `http://localhost:5173`）。
- 装/跑前端走托管 node 全路径 `C:\Users\zhang\.workbuddy\binaries\node\versions\22.22.2-3\`；
  npm 用 `node <该目录>/node_modules/npm/bin/npm-cli.js`（`.cmd` shim 在 Git Bash 会踩坑），
  registry 用 `https://registry.npmmirror.com`；如需独立缓存加 `--cache web/.npm-cache`
  （该目录已 gitignore）。
- 从零入库的前端无法让每一笔提交都过 `vite build`（`index.html` 引用的 `main.tsx` 最后才出现）：
  中间各笔只保证 `tsc --noEmit` 通过，最后一笔跑全量 build；故 add 顺序必须按依赖拓扑排。
- 浏览器自动化：`node <同上>/node_modules/agent-browser/bin/agent-browser.js`
  （全局装的 wrapper 脚本在 Git Bash 解析路径会错，直接调 JS 入口）。
  命令串用 `batch` + stdin 的 JSON 数组（`[["open","url"],...]`，不是行文本）。
  **每个 batch 都要重新登录** —— daemon 会在批处理之间重建上下文，Cookie/localStorage 被清空；
  且前台调浏览器易被 SIGTERM，放后台跑并重定向日志。

## 技术栈
- 后端 Go + chi + pgx；连接层用 sqlc，**不用 ORM**：本项目是「批量导入 + 复杂查询
  （今日编排 / SM-2 / 多孩对比）」，恰是 ORM 的短板，且内容资产先于代码存在。
- `sqlc.yaml` 必须写 `sql_package: "pgx/v5"`；`schema` 只列 `migrations/*.up.sql`
  （整个目录会被 down 脚本的 DROP 打乱解析）；生成物在 `internal/dbgen`（入库）。
- 读查询走 sqlc；**批量写入不走 sqlc**，用 `content/bulk.go` 的多行 upsert
  （200 行/语句 + 同批次冲突键去重，否则 SQLSTATE 21000）。
- 前端 Vite + React 18 + TS + Tailwind（CSS 变量主题）+ TanStack Query + Zustand + RR6。

## 接口契约易错点（踩过就长记性）
- `POST /auth/login` 只认 `{account, password}`（account 收邮箱或手机号）；
  注册才是 email/phone 分开。写成 email/phone 必 422。
- `/parent/settings` 的 `session_limit_min` 只收 **5–120**（0 不合法）；
  `daily_limit_min` 0–480、`rest_interval_min` 0–120 才允许 0 表示不限/不强制。
- 健康检查在根路径 `/health`、`/ready`，**不在** `/api/v1` 下。

## 各里程碑固化下来的约定
- **M1 认证**：Access 15m JWT（typ=access）与 PIN 解锁 5m JWT（typ=unlock）用途隔离；
  Refresh 走 HttpOnly Cookie 且每次刷新轮换；越权一律 404（不 403，避免暴露 ID 是否存在）；
  孩子删除是软归档。
- **M2 内容**：阶段 39 / 汉字 8103 / 组词 29199 / 英语词 1802 / 故事 18908 / 双语配对 830；
  内容即发布（自有管线 → published，采集故事 → pending + 审核）；列表响应带 `meta.page`。
- **M3 练习**：`randx.Derive(seed, i)` 逐题派生种子 → 同一 seed 题目完全一致（与 M5 打印同源）；
  `question_snapshot` 可下发、`answer_key` 永不下发；组卷用 `LoadMaterials` 批量装素材不逐 kp 回查。
- **M4 报表**：`daily_stats.subject_code = ''` 是当天全天合计，只有它的 `passed` 代表达标（趋势同理）；
  日汇总 worker 按 (孩子,日期,学科) 覆盖式重算；**算日期差必须用 `daysBetween`**
  （pgx 的 date 是 UTC 零点，本机 +08 直接相减会差 8 小时）。
- **M5 打印**：预览 HTML 与 chromedp 出的 PDF 是**同一份字符串**；`POST /pdf` 只置 queued 返 202，
  由 `cmd/worker -print` 消费（`FOR UPDATE SKIP LOCKED`）；`page_count` 读 PDF 真实页树；
  mark-done 一个事务四步、靠 `marked_done_at IS NULL` 幂等。
- **M6 前端**：Access Token 只存模块级内存（不进 localStorage / 不进 URL）；
  401 单飞刷新并重放一次，4xx 不重试、5xx/网络错误最多 3 次指数退避；
  亮度用两层 fixed 遮罩而非 `filter`（filter 会给 fixed 造新 containing block）；
  护眼计时只在孩子端外壳；打印预览 `/print/:jobId` 不挂外壳（无导航、无护眼计时）。
