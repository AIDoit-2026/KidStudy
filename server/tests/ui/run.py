"""渲染 UI 流程脚本的占位符，再交给 agent-browser 执行。

flow*.json 是 agent-browser 的 batch 指令数组，里面**不含**任何凭据或本机路径，
全部以占位符表示：

    ${SMOKE_ACCOUNT} / ${SMOKE_PASSWORD} / ${SMOKE_PIN}   取自 server/.env
    ${BASE_URL}                                           前端地址
    ${OUT_DIR}                                            截图输出目录

替换值会做 **JSON 字符串转义**后再填进去。这一步不能省：Windows 路径形如
E:\\Personal\\...，反斜杠直接嵌进 JSON 会变成非法转义（\\P / \\s / \\u），
agent-browser 会整批拒收。

用法（任意 CWD 均可）:
    cd server && python tests/ui/run.py flow2
    python server/tests/ui/run.py flow2 --base-url http://localhost:5173 --out-dir server/tmp/ui
    python tests/ui/run.py flow2 --dry-run          只渲染并报告替换结果，不启动浏览器

长等待怎么办（重要）
--------------------
agent-browser 单条命令的默认超时是 25 秒（CLI 侧 IPC 读超时 30 秒兜底），
`wait <selector>` / `wait --fn` 一旦超时就报 `Failed to read: ...`，既打 `✗`
又把退出码拖成非 0 —— 对「元素还没出现」这种正常中间态是**假失败**。

所以流程里写 `["wait", "<selector>", "--timeout", N]`（N > 25s）时，本脚本
**不把它交给 agent-browser**，而是：

1. 用同一个 `--session` 把前面的指令跑成一批；
2. 在 host 侧每 `--poll-interval` 毫秒发一次 `eval`，直到元素出现或超 N 秒
   —— **命中即返回**，不会白等满 N 秒，也不产生任何假失败；
3. 再用同一个 `--session` 继续跑后面的指令。

`--session` 保证跨调用是同一个浏览器上下文（Cookie / localStorage / URL 都在），
所以流程脚本照旧只写「点什么、断言什么」，不用自己拆段。裸的
`["wait", "<ms>"]`（ms > 25s）同理，由 host 侧分片盲等。

环境变量:
    AGENT_BROWSER_JS   agent-browser.js 绝对路径（探测不到时用）
    NODE_BIN           node 可执行文件路径（默认从 PATH / 托管目录找）

安全说明：凭据经 **stdin** 交给 agent-browser，不出现在命令行参数里——
否则会进 shell history 与进程列表（ps）可见。

输出是**边跑边转发**的：中途卡住时能直接看出停在哪条指令，不用等整批跑完。
"""
import argparse
import json
import os
import shutil
import subprocess
import sys
import threading
import time

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from _common import SERVER_ROOT, env  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_BASE_URL = "http://localhost:5173"
DEFAULT_OUT_DIR = os.path.join(SERVER_ROOT, "tmp", "ui")
DEFAULT_TIMEOUT = 600
DEFAULT_POLL_MS = 3000
# 超过这个时长的等待不交给 agent-browser（它是单条 25 秒硬上限），改由 host 侧轮询。
LONG_WAIT_MS = 25000
# host 侧盲等（裸 wait <ms>）时，每片多长；分片是为了能持续打印进度。
SLEEP_SLICE_MS = 10000

CRED_KEYS = ["SMOKE_ACCOUNT", "SMOKE_PASSWORD", "SMOKE_PIN"]


def json_lit(value):
    """把值转成可安全嵌进 JSON 字符串字面量的形式（转义反斜杠与引号）。"""
    return json.dumps(value, ensure_ascii=False)[1:-1]


def render(text, base_url, out_dir):
    """替换占位符，返回 (结果, 各自的替换次数)。"""
    counts = {}
    for key in CRED_KEYS:
        ph = "${" + key + "}"
        n = text.count(ph)
        if n:
            text = text.replace(ph, json_lit(env(key)))
        counts[key] = n
    for ph, val in (("${BASE_URL}", base_url), ("${OUT_DIR}", out_dir)):
        n = text.count(ph)
        if n:
            text = text.replace(ph, json_lit(val))
        counts[ph] = n
    return text, counts


def long_wait_of(cmd):
    """这条指令是不是 host 侧长等待？是则返回 ("selector", sel, ms) / ("sleep", None, ms)。"""
    if not isinstance(cmd, list) or not cmd or cmd[0] != "wait":
        return None
    if len(cmd) >= 4 and cmd[2] == "--timeout":
        try:
            budget = int(cmd[3])
        except (TypeError, ValueError):
            return None
        return ("selector", cmd[1], budget) if budget > LONG_WAIT_MS else None
    if len(cmd) == 2 and str(cmd[1]).isdigit() and int(cmd[1]) > LONG_WAIT_MS:
        return ("sleep", None, int(cmd[1]))
    return None


def plan_steps(cmds):
    """把指令切成 [("cmds", [...]) | ("wait", kind, sel, ms)] 的序列。"""
    steps, cur = [], []
    for cmd in cmds:
        w = long_wait_of(cmd)
        if w is None:
            cur.append(cmd)
            continue
        if cur:
            steps.append(("cmds", cur))
            cur = []
        steps.append(("wait", w))
    if cur:
        steps.append(("cmds", cur))
    return steps


def find_node(explicit):
    if explicit:
        return explicit
    if os.environ.get("NODE_BIN"):
        return os.environ["NODE_BIN"]
    found = shutil.which("node")
    if found:
        return found
    raise SystemExit("找不到 node：用 --node <路径> 指定，或设 NODE_BIN 环境变量")


def _js_entry_beside(start):
    """从一个 shim/目录向上找 node_modules/agent-browser/bin/agent-browser.js。"""
    cur = start
    for _ in range(3):
        cand = os.path.join(cur, "node_modules", "agent-browser", "bin", "agent-browser.js")
        if os.path.exists(cand):
            return cand
        parent = os.path.dirname(cur)
        if parent == cur:
            break
        cur = parent
    return None


def find_agent_browser(explicit, node_path=None):
    if explicit:
        return explicit
    if os.environ.get("AGENT_BROWSER_JS"):
        return os.environ["AGENT_BROWSER_JS"]
    # 注意：which 会命中 npm 的 shim（.cmd / .ps1 / 无扩展名），那些是 shell/exe 包装，
    # `node <shim>` 会把批处理文件当 JS 解析而报语法错。必须换成相邻的 .js 入口。
    probes = [shutil.which("agent-browser"), os.environ.get("NODE_BIN"), node_path]
    for probe in filter(None, probes):
        if probe.lower().endswith(".js"):
            return probe
        found = _js_entry_beside(os.path.dirname(os.path.abspath(probe)))
        if found:
            return found
    raise SystemExit(
        "找不到 agent-browser 的 .js 入口，请二选一：\n"
        "  --agent-browser <agent-browser.js 的绝对路径>\n"
        "  设环境变量 AGENT_BROWSER_JS\n"
        "（用托管 node 时通常位于 <node 目录>/node_modules/agent-browser/bin/agent-browser.js）"
    )


class Runner:
    """在同一 agent-browser 会话里跑指令，长等待在 host 侧轮询。"""

    def __init__(self, node, ab, session, logf, poll_ms):
        self.node = node
        self.ab = ab
        self.session = session
        self.logf = logf
        self.poll_ms = poll_ms
        self.fails = 0

    def _argv(self, *args):
        return [self.node, self.ab, "--session", self.session, *args]

    def emit(self, text):
        if not text:
            return
        sys.stdout.write(text)
        sys.stdout.flush()
        self.logf.write(text)
        self.logf.flush()

    def _run_stream(self, argv, stdin_text=None):
        """跑一条 agent-browser 命令，边跑边转发输出。返回 (returncode, 输出行列表)。"""
        proc = subprocess.Popen(
            argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT, text=True, encoding="utf-8", errors="replace",
            cwd=SERVER_ROOT, bufsize=1,
        )
        lines = []

        def pump():
            for line in proc.stdout:
                self.emit(line)
                lines.append(line)

        reader = threading.Thread(target=pump, daemon=True)
        try:
            if stdin_text is not None:
                proc.stdin.write(stdin_text)
            proc.stdin.close()
            reader.start()
            # 只等 agent-browser 自己退出。不能等 stdout EOF：它拉起的浏览器会继承管道，
            # 浏览器不关就永远读不到 EOF，会把整批误判成「卡住」。
            rc = proc.wait()
        finally:
            if proc.poll() is None:
                proc.kill()
            reader.join(timeout=5)
        return rc, lines

    def batch(self, cmds):
        """跑一批指令；返回退出码。"""
        self.emit(f"\n[batch] {len(cmds)} 条指令\n")
        rc, lines = self._run_stream(
            self._argv("batch"), json.dumps(cmds, ensure_ascii=False))
        self.fails += sum(1 for l in lines if l.lstrip().startswith("✗"))
        return rc

    def eval(self, js):
        """执行 eval，返回解析后的值（解析失败返回 None）。"""
        rc, lines = self._run_stream(self._argv("eval", js))
        if rc != 0:
            return None
        for line in reversed(lines):
            s = line.strip()
            if not s:
                continue
            try:
                return json.loads(s)
            except json.JSONDecodeError:
                return s
        return None

    def wait_selector(self, sel, budget_ms):
        """轮询元素是否出现；命中即返回 True，超时返回 False。"""
        js = ("(()=>{try{return !!document.querySelector('%s')}catch(_){return false}})()"
              % sel.replace("\\", "\\\\").replace("'", "\\'"))
        start = time.monotonic()
        deadline = start + budget_ms / 1000.0
        self.emit(f"\n[poll] 等元素 {sel} 出现，最多 {budget_ms / 1000:.0f}s\n")
        while True:
            if self.eval(js) is True:
                self.emit(f"[poll] 命中，等了约 {time.monotonic() - start:.1f}s\n")
                return True
            if time.monotonic() >= deadline:
                self.emit(f"[poll] 超时：{sel} 未出现"
                          f"（等了 {time.monotonic() - start:.1f}s）\n")
                self.fails += 1
                return False
            time.sleep(self.poll_ms / 1000.0)

    def sleep_ms(self, ms):
        """host 侧盲等，分片打印进度。"""
        self.emit(f"\n[wait] 盲等 {ms / 1000:.0f}s（host 侧，分片转发）\n")
        left = ms
        while left > 0:
            chunk = min(SLEEP_SLICE_MS, left)
            time.sleep(chunk / 1000.0)
            left -= chunk
            self.emit(f"[wait] 还有 {left / 1000:.0f}s\n")


def main():
    ap = argparse.ArgumentParser(description="渲染并执行 UI 流程脚本")
    ap.add_argument("flow", help="流程名，如 flow2（可省略 .json）")
    ap.add_argument("--base-url", default=DEFAULT_BASE_URL,
                    help=f"前端地址，默认 {DEFAULT_BASE_URL}")
    ap.add_argument("--out-dir", default=DEFAULT_OUT_DIR,
                    help="截图与日志输出目录，默认 server/tmp/ui")
    ap.add_argument("--node", default=None, help="node 可执行文件路径")
    ap.add_argument("--agent-browser", default=None, help="agent-browser.js 路径")
    ap.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT,
                    help=f"整体超时秒数，默认 {DEFAULT_TIMEOUT}（flow6 约需 350s）")
    ap.add_argument("--poll-interval", type=int, default=DEFAULT_POLL_MS,
                    help=f"长等待轮询间隔毫秒，默认 {DEFAULT_POLL_MS}")
    ap.add_argument("--session", default=None,
                    help="agent-browser 会话名，默认按流程名派生（同一会话跨调用保状态）")
    ap.add_argument("--keep-open", action="store_true",
                    help="跑完不关浏览器（默认会 close --all 回收进程）")
    ap.add_argument("--dry-run", action="store_true",
                    help="只渲染并报告替换结果与执行计划，不启动浏览器")
    args = ap.parse_args()

    name = args.flow if args.flow.endswith(".json") else args.flow + ".json"
    path = os.path.join(HERE, name)
    if not os.path.exists(path):
        raise SystemExit(f"找不到流程文件 {path}")

    # 路径统一成正斜杠：Node 与 Windows 都接受，且嵌进 JSON 时不必转义
    out_dir = args.out_dir if os.path.isabs(args.out_dir) else os.path.join(SERVER_ROOT, args.out_dir)
    out_dir = out_dir.replace("\\", "/")

    with open(path, encoding="utf-8") as fh:
        text = fh.read()

    rendered, counts = render(text, args.base_url, out_dir)
    summary = ", ".join(f"{k}={v}" for k, v in counts.items() if v)

    leftover = [c for c in ("${SMOKE_ACCOUNT", "${SMOKE_PASSWORD", "${SMOKE_PIN",
                            "${BASE_URL}", "${OUT_DIR}") if c in rendered]
    if leftover:
        raise SystemExit(f"!! 占位符未展开: {leftover}")

    # 本地先验一遍，别等 agent-browser 报 "invalid escape" 才发现
    try:
        cmds = json.loads(rendered)
    except json.JSONDecodeError as exc:
        raise SystemExit(
            f"!! 渲染后的 JSON 非法: {exc}\n"
            "   多半是某个替换值里有未转义的字符（Windows 路径的反斜杠最常见）。"
        )

    steps = plan_steps(cmds)
    waits = [s for s in steps if s[0] == "wait"]
    plan = f"{len(cmds)} 条指令 / {len(steps)} 段"
    if waits:
        plan += "，host 侧长等待 " + "、".join(
            f"{w[1][1]}≤{w[1][2] / 1000:.0f}s" if w[1][0] == "selector"
            else f"盲等{w[1][2] / 1000:.0f}s" for w in waits)
    print(f"[render] {name}: {summary or '无占位符'}  ({plan})", flush=True)

    if args.dry_run:
        for i, step in enumerate(steps):
            if step[0] == "cmds":
                print(f"  seg{i}: {len(step[1])} 条指令")
            else:
                kind, sel, ms = step[1]
                target = sel if kind == "selector" else "(盲等)"
                print(f"  wait{i}: {target} ≤ {ms / 1000:.0f}s")
        print("[dry-run] 渲染成功，未启动浏览器")
        return

    os.makedirs(out_dir, exist_ok=True)
    log_path = out_dir.rstrip("/") + "/" + name.replace(".json", ".log")

    node = find_node(args.node)
    ab = find_agent_browser(args.agent_browser, node)
    session = args.session or ("kidstudy-ui-" + name.replace(".json", ""))
    print(f"[exec] session={session}\n       node={node}\n       agent-browser={ab}\n"
          f"       cwd={SERVER_ROOT}, 整体超时={args.timeout}s", flush=True)

    logf = open(log_path, "w", encoding="utf-8", newline="\n")
    runner = Runner(node, ab, session, logf, args.poll_interval)
    state = {"timeout": False}

    def on_timeout():
        state["timeout"] = True
        subprocess.run([node, ab, "close", "--all"],
                       capture_output=True, timeout=60, cwd=SERVER_ROOT)

    timer = threading.Timer(args.timeout, on_timeout)
    timer.start()
    rc = 0
    try:
        # 先清干净：同名会话可能是上次跑剩下的，留着会让流程拿到旧页面
        subprocess.run([node, ab, "close", "--all"],
                       capture_output=True, timeout=60, cwd=SERVER_ROOT)
        for step in steps:
            if state["timeout"]:
                break
            if step[0] == "cmds":
                rc = runner.batch(step[1]) or rc
                # 有了 host 侧轮询，流程里不该再出现「预期内」的命令失败；
                # 一旦整批非 0，多半是登录/选择/断言真挂了，别再傻等后面的长等待。
                if rc != 0:
                    runner.emit("\n[bail] 上一批有命令失败，停止后续步骤\n")
                    break
            else:
                kind, sel, ms = step[1]
                if kind == "selector":
                    if not runner.wait_selector(sel, ms):
                        rc = rc or 1
                        break
                else:
                    runner.sleep_ms(ms)
    finally:
        timer.cancel()
        if not args.keep_open:
            subprocess.run([node, ab, "close", "--all"],
                           capture_output=True, timeout=60, cwd=SERVER_ROOT)
        logf.close()

    note = "，整体超时被杀" if state["timeout"] else ""
    print(f"[log] {log_path}\n[result] exit={rc}{note}，失败项 {runner.fails} 条",
          flush=True)
    sys.exit(rc if not state["timeout"] else 1)


main()
