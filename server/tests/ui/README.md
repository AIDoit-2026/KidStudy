# UI 端到端验收脚本

用真实浏览器跑 M6 的验收标准（三档布局、20-20-20 休息页、到点锁屏需 PIN、大屏方向键答题）。
分两层：**数据准备走接口**（`ui_setup.py`），**界面验证走浏览器**（`run.py` + `flow*.json`）。
拆开是为了让流程脚本只关心「点什么、断言什么」，不用塞一堆注册表单交互。

## 前置条件

1. 后端在 `:18080`（`cd server && HTTP_ADDR=:18080 go run ./cmd/api`）
2. 前端在 `:5173`（`cd web && npm run dev -- --port 5173`）
   - 必须用 `localhost` 访问，不能用 `127.0.0.1` —— Refresh Cookie 是 `SameSite=Lax`，
     localhost 与 127.0.0.1 在浏览器眼里是两个站，跨站 fetch 不带 Cookie，换页即掉登录。
3. `server/.env` 里有 `SMOKE_ACCOUNT` / `SMOKE_PASSWORD` / `SMOKE_PIN`（见 `.env.example`）
4. 装了 agent-browser，且用 `AGENT_BROWSER_JS` 或 `--agent-browser` 指到它的 `.js` 入口。
   不指也行：`run.py` 会从 `node` 的位置反推 `<node 目录>/node_modules/agent-browser/bin/agent-browser.js`，
   并**主动绕开 npm 的 `.cmd` / `.ps1` shim**（`node <shim>` 会把批处理当 JS 解析而报语法错）。

## 怎么跑

**推荐：一条命令跑完整套**（自动按正确顺序造数，约 9 分钟，flow6 占大头）

```bash
cd server
python tests/ui/run_all.py                 # 全部 7 条
python tests/ui/run_all.py flow4 flow6     # 只跑指定的
python tests/ui/run_all.py --list          # 只看「流程 -> 造数」配对
```

**单跑某条流程**：造数与验证分两步，先造数再跑流程（配对关系见下表与
`run_all.py --list`）：

```bash
cd server

# 1. 造数据（按要验的场景选命令）
python tests/ui/ui_setup.py fresh

# 2. 跑流程（任意 CWD 均可）
python tests/ui/run.py flow1

# 先看渲染结果与执行计划（分段 + 长等待），不起浏览器
python tests/ui/run.py flow1 --dry-run
```

> 单跑时务必按配对关系造数。配错的典型后果是方向键断言**悄悄落空**（退出码仍是 0），
> 所以能用 `run_all.py` 就用它。

`ui_setup.py` 的命令，按要验的场景选：

| 命令 | 作用 | 配合哪个流程 |
| --- | --- | --- |
| `setup` / `reset` | 建家长+孩子，护眼/时长恢复测试默认值（幂等，不动已有孩子） | flow2 |
| `fresh` | **归档该账号现有全部孩子**，重建同名「哥哥」，护眼恢复测试默认值 | flow1 / flow3 / flow5 / flow7 |
| `rest1` | `rest_interval_min=1`、`session_limit_min=5` | flow4 |
| `lock1` | `rest_interval_min=0`、`session_limit_min=5` | flow6 |

三处容易困惑的地方：

- **`fresh` 为什么存在。** 方向键类流程（flow1/3/5/7）要验「方向键能在选项间移动」，
  前提是当日组卷里有多选项题；而多选项要求同一会话里有多个知识点当干扰项。反复跑会把
  「新学」池吃光，当天只剩寥寥几道题，甚至退化成**单选项**题（方向键无处可移，断言悄悄落空）。
  `fresh` 用「归档旧孩子 + 重建同名新孩子」把学习状态清零，组卷回到满池。
  ⚠️ **它会归档当前账号下所有孩子，只对测试账号（`SMOKE_ACCOUNT`）用。**
- `session_limit_min` 后端只收 **5–120**（0 不合法），能取 0 的只有 `rest_interval_min`
  和 `daily_limit_min`。所以「压到最小以尽快触发」时单次时长只能给 5（→ 等 5 分钟）。
- **`daily_limit_min` 一律给到上限 480**（= 日额度不设闸门）。原因是后端 `TodayUsedSeconds`
  对「已开始但没结束」的会话按 `now - started_at` 计，而当前没有会话过期回收，反复跑同一天
  的验收会不断把当日时长顶高；一旦 `remaining<=0`，组卷就不再排新题、会话可能起不来，
  护眼场景会被日额度掩盖。日额度本身的文案由后端 `planMessage` 承担，不在这几条浏览器流程里验。

## 占位符

流程脚本里**不含任何凭据或本机路径**，全部是占位符，执行时由 `run.py` 渲染：

| 占位符 | 来源 | 默认 |
| --- | --- | --- |
| `${SMOKE_ACCOUNT}` `${SMOKE_PASSWORD}` `${SMOKE_PIN}` | `server/.env` | 无（缺了直接报错） |
| `${BASE_URL}` | `--base-url` | `http://localhost:5173` |
| `${OUT_DIR}` | `--out-dir` | `server/tmp/ui` |

凭据经 **stdin** 传给 agent-browser 而不是命令行参数 —— 命令行参数会进 shell history，
也能被 `ps` 看到。

## 流程清单

| 流程 | 验的是 | 大致耗时 |
| --- | --- | --- |
| `flow1` | 登录 → 选孩子(PIN) → 四档断点截图 → 语文选项题方向键作答 | ~30s |
| `flow2` | 登录 → 选孩子(PIN) → 四档断点 `matchMedia` 实测 → 截图 | ~25s |
| `flow3` | 登录后大屏语文选项题键盘作答 + 按钮态 | ~25s |
| `flow4` | 等 20-20-20 休息页自动弹出 → 跳过必须过家长 PIN | ~90s |
| `flow5` | 大屏语文选项题键盘作答（验提交后按钮停用） | ~25s |
| `flow6` | 等单次时长到点锁屏 → 必须过 PIN 才能继续 | ~5.5min |
| `flow7` | 语文选项题方向键 → 主观题「做完了」→ 下一题 | ~30s |

> 方向键导航只对**有选项的题**有意义。数学题全是 `math_param` 填空题（没有选项按钮），
> 所以这几条流程一律走语文。要验数学作答请用 `tests/smoke/smoke_m3.py` —— 那边能直接从
> 题面把答案算出来，做确定性的对错断言，浏览器流程做不到这点。
>
> 走语文还不够：**flow1/3/5/7 之前要先 `ui_setup.py fresh`**。理由见上一节「`fresh` 为什么存在」
> —— 学习状态不归零的话，当日组卷可能只剩单选项题，方向键断言会悄悄落空。
> flow4/flow6 只等遮罩、不组卷，`rest1` / `lock1` 即可。

`flow*.json` 是 agent-browser 的 batch 指令数组，也可以不用 `run.py`、手工渲染后直接喂：

```bash
node <agent-browser.js> batch < rendered.json
```

截图默认落在 `server/tmp/ui/`（gitignored）。要看历史验收截图就直接翻那个目录。

## 长等待怎么处理（最容易踩的坑）

**agent-browser 单条命令的默认超时是 25 秒**，硬上限受 CLI 侧 30 秒 IPC 读超时约束。
所以 `wait <selector>` / `wait --fn` / `wait <ms>` 一旦超过这个时长就会报
`Failed to read: A connection attempt failed...`（CDP 空闲断连），既打 `✗` 又把退出码拖成非 0。
**对「元素还没出现」这种正常中间态，这是假失败。**

`run.py` 的处理方式：流程里写 `["wait", "<selector>", "--timeout", N]`（N > 25s）时，
**不交给 agent-browser**，而是

1. 用同一个 `--session` 把前面的指令跑成一批；
2. 在 **host 侧**每 `--poll-interval`（默认 3s）发一次 `agent-browser --session <S> eval`，
   直到元素出现或超 N 秒 —— **命中即返回**，不会白等满 N 秒，也不产生任何假失败；
3. 再用同一个 `--session` 继续跑后面的指令。

`--session` 让跨调用落在同一个浏览器上下文（Cookie / localStorage / URL 都在），
所以流程脚本照旧只写「点什么、断言什么」，不用自己拆段。裸的 `["wait", "<ms>"]`
（ms > 25s）同理，由 host 侧分片盲等。

**所以：不要在流程里写「指望 agent-browser 等够时间」的长等待**，超过 25 秒一律用
`["wait", "<selector>", "--timeout", N]` 表达。护眼那两块遮罩为此加了
`data-testid="rest-overlay"` / `data-testid="lock-screen"` 当锚点。
`run.py --dry-run` 会打印分段计划，能直接看出识别到了几处长等待。

其余几条坑：

1. **一个 batch 就等于一次会话。** 不开 `--session` 时，agent-browser 的 daemon 在两次
   批处理之间会重建上下文，Cookie / localStorage 被清空。所以每个 `flow*.json` 都必须自带
   完整登录流程，不能指望「上一个流程登录过」。（`run.py` 内部用固定 `--session` 串起同一
   流程的多个分段；这是同一流程内的事，跨流程仍然各自登录。）

2. **渲染值必须做 JSON 转义。** Windows 路径形如 `E:\Personal\...`，反斜杠直接嵌进
   JSON 字符串会变成非法转义（`\P` / `\s` / `\u`），agent-browser 会**整批拒收**，
   而且只报一句 `Invalid JSON input: invalid escape at line N column M`，很不直观。
   `run.py` 里由 `json_lit()` 统一转义，并在启动浏览器**之前**先用 `json.loads` 本地校验，
   把这类问题挡在启动浏览器之前。

3. **退出码仍非唯一结论。** 有 host 侧轮询后，流程里不该再有「预期内」的命令失败，
   因此 `run.py` 会在某批非 0 时**直接停下**（不再傻等后面的长等待），并在末尾打印
   `失败项 N 条`。判定最终仍要看日志里的关键断言值（`restShown` / `lockShown` /
   `clicked-*` / `active` 等），`✗` 只代表「命令执行失败」。
