"""按正确的造数顺序，把 M6 的 UI 验收流程跑一遍。

每条流程依赖不同的数据准备（理由见 README 与 ui_setup.py）：
    flow1 / flow3 / flow5 / flow7  ->  fresh   学习状态清零，组卷才有「多选项题」
    flow2                          ->  setup   只要孩子存在
    flow4                          ->  rest1   rest=1 / session=5
    flow6                          ->  lock1   rest=0 / session=5

单跑某条流程时也建议走本脚本，而不是手工按顺序敲两条命令 —— 免得配错数据，
配错了最典型的后果是方向键断言**悄悄落空**（退出码仍是 0）。

用法（任意 CWD 均可）:
    python tests/ui/run_all.py                # 全部（约 9 分钟，flow6 占大头）
    python tests/ui/run_all.py flow4 flow6    # 只跑指定的几个
    python tests/ui/run_all.py --list         # 只看「流程 -> 造数」配对

退出码：任一条流程失败即非 0。
"""
import argparse
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SERVER_ROOT = os.path.dirname(os.path.dirname(HERE))  # server/
PYTHON = sys.executable or "python"

SETUP = os.path.join(HERE, "ui_setup.py")
RUNNER = os.path.join(HERE, "run.py")

# 流程 -> 造数命令（顺序即执行顺序）
FIXTURE = {
    "flow1": "fresh",
    "flow2": "setup",
    "flow3": "fresh",
    "flow4": "rest1",
    "flow5": "fresh",
    "flow6": "lock1",
    "flow7": "fresh",
}
ORDER = ["flow1", "flow2", "flow3", "flow4", "flow5", "flow6", "flow7"]

# 单条流程的整体超时（秒）。flow6 要等单次时长到点（5 分钟），给足余量。
TIMEOUT = {"flow6": 900}
DEFAULT_TIMEOUT = 300


def run_step(label, argv):
    print(f"\n{'=' * 60}\n[$] {label}\n{'=' * 60}", flush=True)
    proc = subprocess.run([PYTHON] + argv, cwd=SERVER_ROOT)
    return proc.returncode


def main():
    ap = argparse.ArgumentParser(description="按正确造数顺序跑 M6 的 UI 验收流程")
    ap.add_argument("flows", nargs="*", help="要跑的流程名（默认全部）")
    ap.add_argument("--list", action="store_true", help="只打印「流程 -> 造数」配对")
    args = ap.parse_args()

    if args.list:
        for name in ORDER:
            print(f"  {name:6s} <- ui_setup.py {FIXTURE[name]}")
        return

    wanted = args.flows or ORDER
    unknown = [f for f in wanted if f not in FIXTURE]
    if unknown:
        raise SystemExit(f"未知流程 {unknown}；可用: {', '.join(ORDER)}")
    # 按 ORDER 排序，保证 fresh 与后续流程的相对顺序稳定
    wanted = [f for f in ORDER if f in wanted]

    results = {}
    for name in wanted:
        fixture = FIXTURE[name]
        rc = run_step(f"ui_setup.py {fixture}", [SETUP, fixture])
        if rc != 0:
            results[name] = f"造数失败(rc={rc})"
            print(f"[!] {name}: 造数 {fixture} 失败，跳过", flush=True)
            continue
        rc = run_step(f"run.py {name}", [
            RUNNER, name,
            "--timeout", str(TIMEOUT.get(name, DEFAULT_TIMEOUT)),
        ])
        results[name] = "通过" if rc == 0 else f"失败(rc={rc})"

    print(f"\n{'=' * 60}\n验收汇总\n{'=' * 60}")
    for name in wanted:
        print(f"  {name:6s} {results[name]}")
    bad = [n for n in wanted if results[n] != "通过"]
    print(f"\n通过 {len(wanted) - len(bad)}/{len(wanted)}")
    sys.exit(1 if bad else 0)


main()
