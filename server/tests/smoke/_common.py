"""冒烟脚本共用的路径解析与 .env 读取。

为什么需要它：这些脚本原先放在 server/tmp/，用 `open(".env")` 或
`dirname(dirname(__file__))` 去够 server/ 根目录。迁到 server/tests/smoke/ 后目录深度变了，
那两种写法都会指错地方（甚至够到 server/tests/）。这里统一按「脚本自身位置」推导，
于是脚本可以从**任意 CWD** 运行：

    cd server && python tests/smoke/smoke_m6.py
    python server/tests/smoke/smoke_m6.py          # 在仓库根也一样

注意：被脚本拉起的 Go 二进制（api / worker / importer）用 godotenv 从**当前工作目录**
读 .env，所以 subprocess 必须显式传 cwd=SERVER_ROOT，不能沿用调用者的 CWD。

凭据一律从 server/.env 读（SMOKE_ACCOUNT / SMOKE_PASSWORD / SMOKE_PIN），
脚本里不出现明文口令；server/.env 已 gitignore，仓库里只提交 .env.example 占位值。
"""
import os

HERE = os.path.dirname(os.path.abspath(__file__))
SERVER_ROOT = os.path.dirname(os.path.dirname(HERE))  # -> server/
ENV_PATH = os.path.join(SERVER_ROOT, ".env")
TMP_DIR = os.path.join(SERVER_ROOT, "tmp")  # 编译产物：api.exe / worker.exe / importer.exe


def env(key, default=None):
    """读 server/.env 里的键。

    找不到时：给了 default 就返回它，否则带指引退出 —— 比静默返回空串好排查
    （空串往往表现为后端 422/401，离真正原因很远）。
    """
    if os.path.exists(ENV_PATH):
        with open(ENV_PATH, encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                k, v = line.split("=", 1)
                if k.strip() == key:
                    return v.strip()
    if default is not None:
        return default
    raise SystemExit(f"缺少环境变量 {key}：请写进 {ENV_PATH}（可参照 server/.env.example）")
