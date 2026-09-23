# KidStudy · 幼儿家庭教育应用

一个面向家庭场景的幼儿启蒙学习平台：**由家长主导、孩子使用**，覆盖语文 / 数学 / 英语三科，内置护眼自适应界面、纸质打印输出、学习评价与进度追踪。家长既能用屏幕教孩子，也能随时把学习内容变成纸上的练习卷。

- 目标用户：学龄前 ~ 小学低年级（4–8 岁）孩子的家长
- 核心场景：家长在家里用大屏 / 平板 / 手机教孩子，并支持把内容打印成纸质材料
- 部署形态：云服务器 + 公网域名 + HTTPS，家长扫码登录

---

## 目录

- [一、项目介绍](#一项目介绍)
- [二、本地调试与启动](#二本地调试与启动)
- [三、部署](#三部署)

---

## 一、项目介绍

### 1.1 产品定位与差异点

产品的第一用户是**家长**，孩子端只是被家长带着用的一个极简界面。四个差异点：

| 差异点 | 说明 |
| --- | --- |
| 家长主导，非孩子自助 | 家长排任务、看报表、控时长；孩子端无广告、无社交、无排行榜。 |
| 屏幕不是唯一载体 | 任何学习内容都能一键生成 **A4 可打印练习卷**（描红、口算、单词卡、找规律），是核心能力而非附加功能。 |
| 护眼是一等需求 | 贯穿配色、字号、动效、时长控制、休息提醒的系统性设计，不是「加个夜间模式」。 |
| 内容可生长 | 自带采集与导入管线，可持续补充汉字、单词、故事素材，家长审核通过后上线。 |

明确**不做**：孩子自主注册 / 社交 / 排行榜、直播课与真人答疑、应用商店上架、付费订阅与支付。

### 1.2 功能范围

- **练习引擎**：今日任务编排、9 种题型、数学参数化生成（同 seed 逐题可复现）、自动判分、间隔重复、错题本。
- **评价与报表**：星级与成就、成长树、日汇总、家长学习报告（总览 / 趋势 / 建议）、CSV 与 PDF 导出、多孩对比。
- **打印中心**：10 套模板、A4 预览、答案页分离、Chromium 渲染 PDF、打印记录与纸质补录、一键重印。
- **护眼与多端**：米黄 / 深色主题、字号与亮度调节、20-20-20 休息提醒、时长到点锁屏（PIN 解锁）、四档响应式断点、大屏键盘答题。
- **账号与家长控制**：邀请码建号、家长 PIN、大屏扫码登录（SSE）、每日与单次时长限制、节奏模式（标准 / 快速 / 复习）。

### 1.3 技术栈

| 层 | 选型 | 说明 |
| --- | --- | --- |
| 后端 | Go 1.27 + chi + pgx | 按功能组织的三层架构（handler / service / repository） |
| 数据访问 | sqlc | 不用 ORM：本项目是批量导入 + 复杂查询，恰是 ORM 的短板 |
| 数据库 | PostgreSQL 16+ | 全部变更走可回滚的迁移（`server/migrations`） |
| 前端 | Vite + React 18 + TypeScript | 路由级代码分割、Tailwind（CSS 变量主题） |
| 前端数据层 | TanStack Query + Zustand | 服务端状态与 UI 状态分离 |
| 打印 | chromedp（Chromium） | 预览 HTML 与最终 PDF 是同一份字符串 |
| 内容资产 | Python 采集/构建脚本 | 一次性工具，产物落在 `var/`（不入库） |

### 1.4 仓库结构

```
KidStudy/
├── server/                     Go 后端
│   ├── cmd/
│   │   ├── api/                API 服务入口（含 -migrate / -rollback）
│   │   ├── worker/             后台任务：日汇总、打印渲染、过期清理
│   │   └── importer/           一次性内容导入器
│   ├── internal/
│   │   ├── config/             集中配置（环境变量 + 启动校验）
│   │   ├── feature/            业务模块：auth / children / content / mastery /
│   │   │                       parent / practice / print / report / review / system
│   │   ├── platform/           apperr / auth / logger / middleware / postgres /
│   │   │                       requestctx / response / server / storage
│   │   ├── dbgen/              sqlc 生成物
│   │   └── pkg/                内部小工具（randx 等）
│   ├── migrations/             数据库迁移（.up.sql / .down.sql）
│   ├── queries/                sqlc 查询定义
│   ├── templates/print/        打印模板与 print.css
│   ├── tests/
│   │   ├── smoke/              端到端冒烟脚本（Python，入库）
│   │   └── ui/                 浏览器 UI 验收（agent-browser）
│   └── deploy/                 部署产物（占位，见第三节）
├── web/                        React 前端
│   └── src/{api,app,components,features,hooks,stores,styles}
├── docs/                       需求、设计、内容体系、采集报告
├── tools/                      Python 采集与构建脚本
├── var/                        内容资产（gitignored，需自行生成）
└── .githooks/                  pre-commit 凭据扫描
```

### 1.5 里程碑进度

| 里程碑 | 内容 | 状态 |
| --- | --- | --- |
| M0 基础设施 | 目录骨架、配置校验、结构化日志、错误体系、健康检查、迁移框架 | 已完成 |
| M1 认证与档案 | 注册登录刷新登出、PIN、扫码登录（SSE）、孩子档案 CRUD | 已完成 |
| M2 内容底座 | 内容表 + importer、检索、审核队列、笔顺、课程基准线 | 已完成 |
| M3 练习引擎 | 今日编排、9 种题型、seed 可复现、判分、间隔重复、完成判定 | 已完成 |
| M4 评价与报表 | 星级、成就、成长树、日汇总 worker、报表与建议、CSV 导出 | 已完成 |
| M5 打印中心 | 10 套模板、PDF 渲染、打印记录与补录 | 已完成 |
| M6 护眼与多端 | 主题、字号亮度、20-20-20、锁屏 PIN、四档断点、大屏键盘 | 已完成 |
| M7 上云加固 | 限流、安全头、配置加固、依赖清零、前端优化 | **代码部分已完成**，部署部分待办 |

> M7 的部署尾巴（Caddy + HTTPS/HSTS、`pg_dump` 备份、部署文档）尚未落地，详见第三节「当前状态」。

---

## 二、本地调试与启动

### 2.0 前置依赖

| 依赖 | 版本 | 用途 |
| --- | --- | --- |
| Go | 1.27+ | 后端编译与运行 |
| Node.js | 20 LTS+ | 前端构建与开发服务器 |
| PostgreSQL | 16+ | 数据库 |
| Python | 3.10+ | 冒烟与 UI 验收脚本（可选，但推荐） |

### 2.1 准备数据库

```sql
CREATE ROLE kidstudy LOGIN PASSWORD '<自拟口令>';
CREATE DATABASE kidstudy OWNER kidstudy;
```

连接串形如（写进 `server/.env`，不要提交）：

```
postgres://kidstudy:<口令>@localhost:5432/kidstudy?sslmode=disable
```

### 2.2 配置后端环境变量

配置**只来自环境变量**，本地可选从 `.env` 读取；任何缺失或非法都会在启动时直接退出（fail-fast）。

```bash
cd server
cp .env.example .env
```

至少补齐这三项：

| 变量 | 说明 |
| --- | --- |
| `DATABASE_URL` | 上一步的连接串 |
| `AUTH_SECRET` | 签发 Access / Unlock 令牌的密钥，**至少 32 字符**。生成：`openssl rand -base64 48` |
| `BOOTSTRAP_INVITE_CODE` | 关闭了公开注册，首次建号要凭这个邀请码。**首次注册后应清空** |

本机开发注意（Windows 上 8080 常被 Jenkins 占用）：

```
HTTP_ADDR=:18080
BASE_URL=http://localhost:18080
CORS_ALLOWED_ORIGINS=http://localhost:5173
```

完整变量清单见 `server/.env.example`，权威定义在 `server/internal/config/config.go`。

### 2.3 执行数据库迁移

```bash
cd server
go run ./cmd/api -migrate
```

> **注意**：`-migrate` 跑完迁移后会**继续启动 API 服务**，不是「迁移完就退出」。只想迁移的话，看到迁移日志后按 `Ctrl+C` 停掉即可。
>
> 回退一步（仅用于本地调试）：`go run ./cmd/api -rollback`。

### 2.4 导入内容资产

`var/` 下的内容资产**不入库**，需要自行生成：

```bash
# 1) 采集（按需，耗时长；详见 tools/*.py 头部注释）
python tools/crawl_stories.py

# 2) 构建阶段字表
python tools/build_stages.py

# 3) 灌库（幂等，可反复重跑，不会覆盖家长已做的判定）
cd server
go run ./cmd/importer                      # 默认数据目录 ../var
go run ./cmd/importer -only stages         # 只跑某一步：stages|hanzi|words|stories|plans
```

### 2.5 启动后端

API 与后台任务**是两个进程**（重活不进请求处理器）：

```bash
cd server

# 终端 1：API 服务
go run ./cmd/api

# 终端 2：后台 worker —— 打印 PDF 渲染必须由它消费
go run ./cmd/worker -print-loop 5s

# 或者一个进程全包：日汇总 + 打印消费 + 过期清理
go run ./cmd/worker -loop -print-loop 5s -purge-loop 1h
```

常用 worker 参数：

| 参数 | 作用 |
| --- | --- |
| 无参数 | 重算最近 3 天的日汇总（覆盖式，幂等） |
| `-loop -at 03:30` | 常驻，每天 03:30 做日汇总 |
| `-print` / `-print-loop 5s` | 跑一轮打印渲染 / 常驻消费打印队列 |
| `-purge` / `-purge-loop 1h` | 清理过期 PDF（保留期由 `PRINT_RETENTION` 控制，默认 30 天） |

> 不启动 worker，打印中心会一直停在 `queued`——因为 `POST /pdf` 只入队并返回 202。

### 2.6 启动前端

```bash
cd web
npm install
cp .env.example .env     # 按需改 VITE_API_BASE_URL
npm run dev
```

前端脚本：

| 命令 | 作用 |
| --- | --- |
| `npm run dev` | 开发服务器（默认 5173） |
| `npm run build` | 类型检查 + 生产构建，产物在 `web/dist/` |
| `npm run typecheck` | 仅类型检查，不写盘 |
| `npm run preview` | 本地预览构建产物（4173） |

**同站纪律（最容易踩的坑）**：`VITE_API_BASE_URL` 的**主机名必须与浏览器访问前端的地址一致**。

后端的 Refresh Token 是 `HttpOnly` + `SameSite=Lax` 的 Cookie，跨站 fetch 不会携带它，换页即掉登录。而 `localhost` 与 `127.0.0.1` 在浏览器眼里是**两个不同的站**：

- 前端 `http://localhost:5173` + 后端 `http://localhost:18080` = 同站，正常。
- 前端 `http://localhost:5173` + 后端 `http://127.0.0.1:18080` = 跨站，`SameSite=Lax` 的 Cookie 不会被带上，登录态丢失。

同时 `CORS_ALLOWED_ORIGINS` 必须包含前端的来源（如 `http://localhost:5173`）。

### 2.7 验证

```bash
# 健康检查（注意：本机有代理时 curl 要加 --noproxy '*'，否则请求会被转发到代理导致失败）
curl --noproxy '*' http://localhost:18080/health     # 期望 200
curl --noproxy '*' http://localhost:18080/ready      # 数据库连通时 200，未连通 503

# 后端单元测试
cd server && go test ./...

# 端到端冒烟（需先起后端；不需要前端）
python server/tests/smoke/smoke_m7.py

# 浏览器 UI 验收（需同时起后端与前端，约 9 分钟；详见 server/tests/ui/README.md）
cd server && python tests/ui/run_all.py
```

> 健康检查在**根路径** `/health`、`/ready`；业务 API 统一在 `/api/v1` 下。

### 2.8 常见问题

| 现象 | 原因与处理 |
| --- | --- |
| 启动即退出并打印配置错误 | 配置校验 fail-fast。检查 `.env` 是否在 `server/` 目录下、必填项是否齐全。 |
| `curl` 访问本机服务 502 / 连不上 | 本机代理把请求转发了。加 `--noproxy '*'`。 |
| 登录后一换页就掉登录 | `VITE_API_BASE_URL` 与访问地址不同站，见 2.6。 |
| 打印任务一直 `queued` | worker 没起，或没带 `-print-loop`。 |
| 迁移报错缺少内容 | 先跑 `-migrate` 再跑 `importer`；`importer` 依赖迁移后的表结构。 |
| 测试脚本报口令错误 | 冒烟与 UI 脚本用 `server/.env` 里的 `SMOKE_PASSWORD` / `SMOKE_ACCOUNT` / `SMOKE_PIN`。 |
| 换了克隆后提交不扫描凭据 | `git config core.hooksPath .githooks`（hooks 未配置时静默失效）。 |

---

## 三、部署

### 3.1 部署形态

```
                  ┌─────────────────────────────────────────┐
  浏览器 ── HTTPS ─▶  Caddy（TLS 终止 + HSTS + 静态托管）      │
                  │      ├─ /api/*  ──▶ 127.0.0.1:8080  api   │
                  │      └─ 其他     ──▶ web/dist（SPA 回退）  │
                  └─────────────────────────────────────────┘
                                   │
                       ┌───────────┴────────────┐
                       │  PostgreSQL（本机/云）   │
                       │  var/storage（PDF 等）  │
                       └────────────────────────┘
                                worker（systemd 常驻）
```

- Caddy 负责 TLS 与静态文件；API 只监听回环地址，不直接暴露公网。
- `api` 与 `worker` 都是常驻进程，由 systemd 托管。

### 3.2 生产环境变量

生产（`APP_ENV=production`）下配置校验会**额外强制**几项，配错直接拒绝启动：

| 变量 | 生产取值 | 说明 |
| --- | --- | --- |
| `APP_ENV` | `production` | 决定 Cookie 默认 Secure、是否下发 HSTS、是否启用严格校验 |
| `BASE_URL` | `https://你的域名` | **必须 https**，否则拒绝启动 |
| `CORS_ALLOWED_ORIGINS` | `https://你的域名` | 每个来源都**必须 https**，禁用通配符 |
| `COOKIE_SECURE` | 省略或 `true` | 生产置 `false` 会被校验拦下 |
| `HTTP_ADDR` | `127.0.0.1:8080` | 只监听本机，由 Caddy 反代 |
| `TRUSTED_PROXY` | `true` | **反代场景必须开**：否则限流会把所有请求当成同一个 IP（都是 Caddy 的地址），登录限流会误伤所有人 |
| `LOG_FORMAT` | `json` | 结构化日志，便于采集 |
| `LOG_LEVEL` | `info` | 排障时可临时调 `debug` |
| `AUTH_SECRET` | 强随机 ≥32 字符 | 绝不提交，绝不写入镜像 |
| `BOOTSTRAP_INVITE_CODE` | 首次注册后清空 | 留着等于公开注册入口 |
| `RATE_LIMIT_ENABLED` | `true` | 三档令牌桶限流（登录 / 扫码 / 打印） |
| `CHROME_PATH` | 按需 | PDF 渲染用的 Chromium 路径，留空则自动探测 |
| `STORAGE_DIR` | 如 `/opt/kidstudy/var/storage` | 换工作目录启动时**必须给绝对路径** |

> 变量清单以 `server/internal/config/config.go` 为准，`server/.env.example` 有逐项注释。

### 3.3 构建

**后端**（在构建机或服务器上）：

```bash
cd server

# 本机编译
go build -o bin/api ./cmd/api
go build -o bin/worker ./cmd/worker
go build -o bin/importer ./cmd/importer

# 交叉编译 Linux（若在别的机器上构建产物）
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/api ./cmd/api
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/worker ./cmd/worker
```

**前端**：

```bash
cd web
npm ci
npm run build          # 产出 web/dist/
```

> `VITE_API_BASE_URL` 是**构建期**注入的。同域部署（Caddy 同时托管前端与 `/api/*`）时，可以设为相对路径 `VITE_API_BASE_URL=/api/v1`，免去域名硬编码；跨域部署则填完整地址 `https://你的域名/api/v1`。

**数据库迁移**（部署时执行一次，注意它会继续起服务）：

```bash
./bin/api -migrate
# 看到迁移完成后 Ctrl+C，再交给 systemd 起常驻进程
```

### 3.4 Caddy 配置

`/etc/caddy/Caddyfile`：

```caddyfile
kidstudy.example.com {
    encode zstd gzip

    # API 反代到本机
    handle /api/* {
        reverse_proxy 127.0.0.1:8080
    }

    # 前端静态产物（SPA 路由回退到 index.html）
    handle {
        root * /opt/kidstudy/web/dist
        try_files {path} /index.html
        file_server
    }

    # 后端已下发一套安全头；静态文件不过后端，这里补一份覆盖全站
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
        Referrer-Policy "no-referrer"
        Cross-Origin-Opener-Policy "same-origin"
        -Server
    }

    log {
        output file /var/log/caddy/kidstudy.log
        format json
    }
}
```

Caddy 会自动申请并续期 Let's Encrypt 证书（域名需先解析到服务器 IP）。

### 3.5 systemd 常驻

`/etc/systemd/system/kidstudy-api.service`：

```ini
[Unit]
Description=KidStudy API
After=network.target postgresql.service

[Service]
Type=simple
User=kidstudy
WorkingDirectory=/opt/kidstudy/server
EnvironmentFile=/opt/kidstudy/server/.env
ExecStart=/opt/kidstudy/server/bin/api
Restart=on-failure
RestartSec=3

# 加固
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/opt/kidstudy/var

[Install]
WantedBy=multi-user.target
```

`/etc/systemd/system/kidstudy-worker.service`：

```ini
[Unit]
Description=KidStudy Worker（日汇总 + 打印渲染 + PDF 清理）
After=network.target postgresql.service

[Service]
Type=simple
User=kidstudy
WorkingDirectory=/opt/kidstudy/server
EnvironmentFile=/opt/kidstudy/server/.env
ExecStart=/opt/kidstudy/server/bin/worker -loop -at 03:30 -print-loop 5s -purge-loop 1h
Restart=on-failure
RestartSec=5

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/opt/kidstudy/var

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now kidstudy-api kidstudy-worker
sudo systemctl status kidstudy-api
```

> worker 要渲染 PDF，需要 Chromium 内核可被运行用户访问（无头模式）。缺内核时 worker 会在渲染时明确报错，API 不受影响。

### 3.6 数据库备份与恢复

**备份脚本** `/opt/kidstudy/backup.sh`（保留 30 天）：

```bash
#!/usr/bin/env bash
set -euo pipefail

BACKUP_DIR=/opt/kidstudy/backups
STAMP=$(date +%Y%m%d_%H%M%S)
KEEP_DAYS=30

mkdir -p "$BACKUP_DIR"
pg_dump --format=custom --no-owner --file="$BACKUP_DIR/kidstudy_$STAMP.dump" "$DATABASE_URL"

# 清理超过保留期的备份
find "$BACKUP_DIR" -name 'kidstudy_*.dump' -mtime "+$KEEP_DAYS" -delete

# 校验：dump 能被读取，避免留下坏备份
pg_restore --list "$BACKUP_DIR/kidstudy_$STAMP.dump" > /dev/null
echo "backup ok: kidstudy_$STAMP.dump"
```

> `DATABASE_URL` 从环境变量取。定时任务用 `cron`（注意 cron 环境没有你的 shell 变量，需显式 source）：
> `0 3 * * * . /opt/kidstudy/server/.env; /opt/kidstudy/backup.sh >> /var/log/kidstudy-backup.log 2>&1`

**恢复演练**（**上线前必做一次**，用真实的 dump 恢复到临时库验证）：

```bash
# 1) 建一个临时库，不要直接覆盖生产库
createdb -O kidstudy kidstudy_restore_test

# 2) 恢复
pg_restore --no-owner --dbname kidstudy_restore_test /opt/kidstudy/backups/kidstudy_<STAMP>.dump

# 3) 抽查关键表行数是否合理
psql kidstudy_restore_test -c 'SELECT count(*) FROM parents;'
psql kidstudy_restore_test -c 'SELECT count(*) FROM learning_records;'

# 4) 清理
dropdb kidstudy_restore_test
```

> `var/storage` 下的 PDF 等对象有其自己的保留策略：由 worker `-purge` 按 `PRINT_RETENTION`（默认 30 天）清理。备份脚本只负责数据库；若需要一并备份对象存储，把 `var/storage` 加进备份范围（它通常是可重建的，按需决定）。

### 3.7 上线检查清单

- [ ] 域名解析到服务器，HTTPS 证书自动签发成功（`https://域名` 可访问）
- [ ] `APP_ENV=production`，`BASE_URL` 与 `CORS_ALLOWED_ORIGINS` 均为 https 域名
- [ ] `COOKIE_SECURE` 未置 false；浏览器里 Refresh Cookie 带 `Secure` + `HttpOnly`
- [ ] `TRUSTED_PROXY=true`，限流按真实客户端 IP 生效（可用连续错误登录触发 429 验证）
- [ ] `AUTH_SECRET` 为强随机；`BOOTSTRAP_INVITE_CODE` 在首位家长注册后已清空
- [ ] `/health` 返回 200，`/ready` 在数据库连通时返回 200
- [ ] 响应头含 `Strict-Transport-Security` / `X-Content-Type-Options` / `X-Frame-Options` / `Cross-Origin-Opener-Policy`
- [ ] 备份脚本已进 cron，且**完成过一次真实恢复演练**
- [ ] `govulncheck ./...` 与 `npm audit` 均无高危
- [ ] worker 常驻运行，打印任务能从 `queued` 走到 `ready`
- [ ] 浏览器端跑一遍 UI 验收（`server/tests/ui/run_all.py`），尤其 `react-router-dom` 大版本升级后

### 3.8 当前状态

截至本次更新，仓库内：

- **已就绪**：限流、安全头、配置加固与生产强校验、依赖漏洞清零、前端构建产物、迁移与备份所需的一切代码基础。
- **未落地**：`server/deploy/` 目前是空目录。上面 3.4–3.6 的 Caddy 配置、systemd 单元、备份脚本均为**推荐方案**，尚未作为仓库文件提交；HTTPS/HSTS 与备份恢复也尚未在真实服务器上验证过。

也就是说，应用本身的「上云加固」代码部分已完成，但**「公网 HTTPS 访问」与「备份可恢复」这两条验收标准尚未达成**——补上 `deploy/` 下的配置与一次恢复演练即可收口。

---

## 相关文档

| 文档 | 内容 |
| --- | --- |
| `docs/开发设计文档.md` | 编码落地蓝图：模块划分、分层铁律、数据表、时序、API 契约、里程碑验收 |
| `docs/overview.md` | 交付概览与当前状态 |
| `docs/需求规划文档.md` | 完整需求与技术规划 |
| `docs/内容与分级体系设计.md` | 汉字 24 阶段、英语 10 级梯度、控字规则 |
| `server/tests/ui/README.md` | 浏览器 UI 验收脚本用法（含长等待与同站等坑） |
