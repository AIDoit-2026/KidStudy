# 迷你 SQL 查询器：python server/tests/smoke/dbq.py "select 1"
# 复用 smoke_m4.py 的连接方式 —— 把 DATABASE_URL 当 psql 的连接串传进去，
# 口令不落命令行字面量（来自 .env）。任意 CWD 都可以跑。
import subprocess
import sys

from _common import env

PSQL = r"C:\Program Files\PostgreSQL\18\bin\psql.exe"


sql = sys.argv[1]
proc = subprocess.run([PSQL, env("DATABASE_URL"), "-A", "-t", "-q", "-F", " | ", "-c", sql],
                      capture_output=True, text=True, timeout=60, encoding="utf-8")
if proc.stdout:
    sys.stdout.write(proc.stdout)
if proc.returncode != 0:
    sys.stderr.write(proc.stderr or "")
    sys.exit(proc.returncode)
