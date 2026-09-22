#!/usr/bin/env python3
"""提交前扫描：挡住明文凭据与本机绝对路径入库。

用法::

    python tools/check_secrets.py            # 扫全部已跟踪文件（人工巡检）
    python tools/check_secrets.py --staged   # 只扫暂存区（pre-commit 钩子用）
    python tools/check_secrets.py --path <文件>   # 额外指定目标，可重复

判定分三层，任何一层命中都以非 0 退出：

1. **真实值反查** —— 读出 gitignored 的 env 文件里的敏感值，反查文件里是否出现。
   这比「按键名匹配」准得多：键名匹配抓不到 `"value": "<真实口令>"` 这种形态，
   还会把 `input[type=password]` 之类的 selector 误判成凭据。
2. **模式匹配** —— JWT / Bearer / `password: "..."` 这类字面量，兜住「值不在 env 里」
   的情况（例如别人库里的 token）。
3. **策略** —— 非 `.env.example` 的 `.env` 文件一律不许入库；已跟踪文件里不许出现
   本机绝对路径（换台机器就废）。

输出**只有位置、键名与长度，绝不打印明文值** —— 值一旦进日志/终端就等于二次泄露。
确有需要放行的样板行，在行内写 `secretscan:allow` 注释即可（**只对第 2、3 层生效**：
第 1 层命中说明真实凭据在文件里，没有放行的道理）。
"""

import argparse
import os
import re
import subprocess
import sys

try:  # Windows 控制台默认 GBK，中文输出会炸
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:  # noqa: BLE001
    pass

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# 敏感键名：要求敏感词是**最后一段**，避免把 PIN_LENGTH / TOKEN_TTL 这类
# 普通配置项的值当成凭据（它们的值很短，会疯狂误报）。
SENSITIVE_KEY = re.compile(
    r"(?i)^(?:[A-Z0-9]+_)*"
    r"(PASSWORD|PASSWD|PWD|SECRET|TOKEN|API_?KEY|INVITE_CODE|PIN|DATABASE_URL|DSN)$"
)
ENV_FILES = ["server/.env", "web/.env"]

# 短值（如 4 位 PIN）子串命中率太高，要求被引号紧紧包裹才算。
SHORT_VALUE_LEN = 8

PLACEHOLDER = re.compile(
    r"(?i)example|placeholder|your[-_]|change[-_]?me|xxxx|todo|<|\$\{|^\s*$"
)

PATTERNS = [
    # 必须三段点分才算 JWT：只写 `eyJ[A-Za-z0-9._-]{16,}` 会命中一切 base64
    # （`eyJ` 正是 `{"` 的 base64），而 compress 后的 bundle 里遍地是
    # sourceMappingURL=data:...;base64,eyJ2ZXJzaW9uIjoz...（server/tmp 的调试转储实际踩过）。
    ("jwt", re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}")),
    ("bearer", re.compile(r"(?i)bearer\s+[A-Za-z0-9._-]{16,}")),
    # 前面 (?<![-\w]) 是必需的：否则 `'current-password' : 'new-password'` 这种
    # 三元表达式会把 `password' : 'new-password` 认成「键=password 值=new-password」，
    # 把 autoComplete 的取值当成凭据（web/src/features/auth/LoginPage.tsx 实际踩过）。
    ("kv-literal", re.compile(
        r"(?i)(?<![-\w])[\"']?(password|passwd|pwd|pin|secret|token|api[_-]?key|invite[_-]code)"
        r"[\"']?\s*[:=]\s*[\"']([^\"'\n]{6,200})[\"']")),
]

# 本机路径只对「会随仓库分发/执行」的文件生效。
# `.md` 与 `.workbuddy/`（工作记忆）是开发笔记，经常需要写实际路径说明环境问题，
# 那是文档问题不是泄露问题；而脚本/配置里出现绝对路径换个机器就废，必须拦。
PATH_LAYER_SKIP_EXT = {".md"}
PATH_LAYER_SKIP_PREFIX = (".workbuddy/", ".git/")

MACHINE_PATH = [
    re.compile(re.escape(REPO_ROOT.replace("\\", "/"))),
    re.compile(re.escape(REPO_ROOT)),
    re.compile(r"[A-Za-z]:[\\/]Users[\\/]"),
    re.compile(r"/cygdrive/"),  # secretscan:allow —— 这一行就是在定义「本机路径」本身
]

ALLOW_MARK = "secretscan:allow"

TEXT_EXT = {
    ".py", ".json", ".txt", ".md", ".yaml", ".yml", ".toml", ".ini", ".cfg",
    ".go", ".ts", ".tsx", ".js", ".jsx", ".css", ".html", ".sql", ".sh",
    ".example", ".env", ".log", ".batch", ".conf",
}


def expand_paths(extra):
    """把 --path 给的路径展开成仓库相对文件列表；给目录就递归收集。"""
    out = []
    for item in extra:
        abs_path = os.path.abspath(item)
        if os.path.isdir(abs_path):
            for dirpath, dirnames, filenames in os.walk(abs_path):
                dirnames[:] = [d for d in dirnames
                               if d not in {".git", "__pycache__", "node_modules"}]
                for name in filenames:
                    full = os.path.join(dirpath, name)
                    out.append(os.path.relpath(full, REPO_ROOT).replace("\\", "/"))
        else:
            out.append(os.path.relpath(abs_path, REPO_ROOT).replace("\\", "/"))
    return out


def git_lines(git_args):
    proc = subprocess.run(["git"] + git_args, cwd=REPO_ROOT,
                          capture_output=True, text=True)
    if proc.returncode != 0:
        raise SystemExit(f"!! git {' '.join(git_args)} 失败: {proc.stderr.strip()}")
    return [line for line in proc.stdout.splitlines() if line.strip()]


def is_text_like(rel):
    name = os.path.basename(rel)
    if name.startswith(".env") or ".env" in name:
        return True
    return os.path.splitext(name)[1].lower() in TEXT_EXT


def load_secrets():
    """从 gitignored 的 env 文件读出敏感值（只留在内存里，不外显）。"""
    secrets = {}
    for rel in ENV_FILES:
        path = os.path.join(REPO_ROOT, rel)
        if not os.path.isfile(path):
            continue
        with open(path, encoding="utf-8", errors="replace") as fh:
            for line in fh:
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                key, val = line.split("=", 1)
                key = key.strip()
                val = val.strip().strip('"').strip("'")
                if val and SENSITIVE_KEY.match(key):
                    secrets.setdefault(key, val)
    return secrets


def reverse_hits(line, secrets):
    """第 1 层：真实值反查。"""
    found = []
    for key, val in secrets.items():
        if len(val) >= SHORT_VALUE_LEN:
            if val in line:
                found.append((key, len(val), "值反查"))
        else:
            for quote in ('"', "'"):
                if f"{quote}{val}{quote}" in line:
                    found.append((key, len(val), "值反查(短值·需引号包裹)"))
                    break
    return found


def pattern_hits(line):
    """第 2 层：模式匹配。"""
    found = []
    for kind, rx in PATTERNS:
        for match in rx.finditer(line):
            val = match.group(match.lastindex or 0)
            if PLACEHOLDER.search(val):
                continue
            found.append((kind, len(val), "模式匹配"))
            break
    return found


def path_hits(line):
    """第 3 层：本机绝对路径。"""
    for rx in MACHINE_PATH:
        if rx.search(line):
            return [("machine-path", 0, "本机路径")]
    return []


def is_committed_env(rel):
    """第 3 层：非 .example 的 .env 文件不允许入库。

    按「文件名里含 .env」判定而不是只认 `.env` / `.env.*` —— `app.env`、
    `secrets.env` 这类命名一样是凭据载体，漏掉就白做。
    """
    name = os.path.basename(rel).lower()
    if name.endswith(".example"):
        return False
    return ".env" in name


def path_layer_applies(rel):
    """本机路径检查是否适用于该文件（开发笔记类跳过，见 PATH_LAYER_SKIP_* 注释）。"""
    if os.path.splitext(rel)[1].lower() in PATH_LAYER_SKIP_EXT:
        return False
    return not rel.replace("\\", "/").startswith(PATH_LAYER_SKIP_PREFIX)


def scan_file(rel, secrets, report):
    path = os.path.join(REPO_ROOT, rel)
    if not os.path.isfile(path):
        return 0
    if is_committed_env(rel):
        report(f"  x {rel}:1  .env 文件不允许入库")
        return 1
    if not is_text_like(rel):
        return 0
    check_paths = path_layer_applies(rel)
    hits = 0
    with open(path, encoding="utf-8", errors="replace") as fh:
        for lineno, line in enumerate(fh, 1):
            for key, length, source in reverse_hits(line, secrets):
                report(f"  x {rel}:{lineno}  {key}(len={length}, {source})")
                hits += 1
            if ALLOW_MARK in line:
                continue
            found = pattern_hits(line)
            if check_paths:
                found += path_hits(line)
            for kind, length, source in found:
                report(f"  x {rel}:{lineno}  {kind}(len={length}, {source})")
                hits += 1
    return hits


def main():
    parser = argparse.ArgumentParser(description="扫描明文凭据与本机绝对路径")
    parser.add_argument("--staged", action="store_true",
                        help="只扫暂存区（pre-commit 钩子用）")
    parser.add_argument("--all", action="store_true", help="扫全部已跟踪文件")
    parser.add_argument("--path", action="append", default=[],
                        help="额外指定文件或目录（目录会递归），可重复")
    args = parser.parse_args()

    if args.staged:
        mode, files = "staged", git_lines(
            ["diff", "--cached", "--name-only", "--diff-filter=ACM"])
    elif args.all or not args.path:
        mode, files = "all", git_lines(["ls-files"])
    else:
        mode, files = "path", []

    for rel in expand_paths(args.path):
        if rel not in files:
            files.append(rel)

    secrets = load_secrets()
    if mode == "staged" and not secrets:
        print("!! 找不到 server/.env 的敏感值，第 1 层「真实值反查」已失效 —— "
              "第 2、3 层仍然生效。")

    baseline = ", ".join(f"{k}(len={len(v)})" for k, v in sorted(secrets.items()))
    print(f"[scan] 模式={mode}，待扫 {len(files)} 个文件"
          f"{'；基准凭据: ' + baseline if secrets else ''}")

    lines = []
    total = sum(scan_file(rel, secrets, lines.append) for rel in sorted(set(files)))
    for line in lines:
        print(line)

    if total:
        print(f"\n!! 命中 {total} 处，已阻止提交。"
              f"\n   凭据请移入 gitignored 的 .env，代码里用占位符；"
              f"\n   样板行确需放行就加 `{ALLOW_MARK}` 注释（对真实值反查无效）。")
        return 1

    print(f"[ok] 未发现明文凭据/本机路径（{len(files)} 个文件）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
