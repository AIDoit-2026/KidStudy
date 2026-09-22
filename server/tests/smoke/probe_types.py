# 探一下三科各题型的形态：方向键答题只对「有 options 的题」有意义，
# 所以先摸清哪个学科的会话里会出现选项题，再决定浏览器测试打哪一科。
#
# 用法：起服务后 python server/tests/smoke/probe_types.py
# 依赖 .env 里的 SMOKE_ACCOUNT / SMOKE_PASSWORD（一个已存在的家长账号，
# 由 server/tmp/ui_setup.py 造出来）。
import json
import urllib.error
import urllib.request

from _common import env

API = "http://127.0.0.1:18080/api/v1"
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def call(method, path, body=None, token=None):
    req = urllib.request.Request(API + path, method=method)
    if token:
        req.add_header("Authorization", "Bearer " + token)
    payload = None
    if body is not None:
        payload = json.dumps(body, ensure_ascii=False).encode()
        req.add_header("Content-Type", "application/json")
    try:
        with OPENER.open(req, data=payload, timeout=60) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except Exception:  # noqa: BLE001
            return e.code, {}


_, p = call("POST", "/auth/login", {"account": env("SMOKE_ACCOUNT"), "password": env("SMOKE_PASSWORD")})
token = (p.get("data") or {}).get("access_token")
_, p = call("GET", "/children", token=token)
child = (p.get("data") or [{}])[0].get("id")
print("child:", child)

for subject in ("chinese", "english", "math"):
    st, p = call("POST", "/practice/session",
                 {"child_id": child, "subject": subject, "device_type": "web"}, token=token)
    sid = (p.get("data") or {}).get("id")
    if not sid:
        print(subject, "开不了会话", st, p)
        continue
    st, p = call("GET", f"/practice/session/{sid}?child_id={child}", token=token)
    items = (p.get("data") or {}).get("items") or []
    print(f"\n=== {subject}：{len(items)} 题 ===")
    for it in items:
        q = it.get("question") or {}
        n = len(q.get("options") or [])
        print(f"  seq={it.get('seq')} type={it.get('question_type'):14s} options={n:2d} "
              f"match={len(q.get('match_left') or [])} order={len(q.get('order_items') or [])} "
              f"prompt={str(q.get('prompt'))[:28]}")
