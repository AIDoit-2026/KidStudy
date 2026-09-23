# M7「上云加固」端到端冒烟（server/tests/smoke，已入库，可反复重跑）。
#
# 用法：起后端（cd server && go run ./cmd/api，或先编好 server/tmp/api.exe 直接跑），
#       再 python server/tests/smoke/smoke_m7.py   —— 任意 CWD 都可以
#
# 前提：18080 上跑着最新 api.exe。**本脚本不需要前端**（M7 是后端加固为主的里程碑）。
#
# 逐条验证 M7 新增/加固的后端契约——任何一条破了，前端或运维就会踩坑：
#   1. 安全头：nosniff / DENY / no-referrer / CSP / COOP / no-store 全在；
#      非生产**不**下发 HSTS（否则本机 http 会被浏览器锁定，开发自找麻烦）。
#   2. 报表建议动作端点（§4.7）：review_day 真正改到家长 pace_mode，
#      非法 type 被拒，越权孩子一律 404。
#   3. 报表导出：csv 直出、pdf 复用 M5 周报模板（202 + job_id），非法 format 被拒。
#   4. 打印「一键重印」：克隆原任务且**逐题复现**（同 seed 的题面 items 完全一致）。
#   5. 限流：登录档突发打满后返回 429 + Retry-After + RATE_LIMITED 错误码。
#      —— 限流按 IP 计，会把本机后续登录一并挡掉，所以放在**最后**跑。
import http.cookiejar
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from _common import env

API = "http://127.0.0.1:18080/api/v1"
ROOT = "http://127.0.0.1:18080"
PASS, FAIL = [], []

# 本机有代理：所有 opener 都关掉代理，否则打本机会被转发成 502。
COOKIES = http.cookiejar.CookieJar()
OPENER = urllib.request.build_opener(
    urllib.request.ProxyHandler({}),
    urllib.request.HTTPCookieProcessor(COOKIES),
)
PLAIN = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def check(name, ok, detail=""):
    (PASS if ok else FAIL).append(name)
    print(("  PASS  " if ok else "  FAIL  ") + name + (("  <- " + detail) if detail else ""))


def call(method, path, body=None, token=None, expect=None, label="", headers=None, base=API, opener=None):
    """发 JSON 请求并断言状态码。opener 传 PLAIN 可绕开 CookieJar。"""
    op = opener or OPENER
    url = path if path.startswith("http") else base + path
    if any(ord(ch) > 127 for ch in url):
        head, _, qs = url.partition("?")
        url = urllib.parse.quote(head, safe=":/?") + ("?" + urllib.parse.quote(qs, safe="=&") if qs else "")
    req = urllib.request.Request(url, method=method)
    if token:
        req.add_header("Authorization", "Bearer " + token)
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    payload_bytes = None
    if body is not None:
        payload_bytes = json.dumps(body, ensure_ascii=False).encode()
        req.add_header("Content-Type", "application/json")
    try:
        resp = op.open(req, data=payload_bytes, timeout=90)
        status, blob, hdrs = resp.status, resp.read(), dict(resp.headers)
    except urllib.error.HTTPError as e:
        status, blob, hdrs = e.code, e.read(), dict(e.headers)
    except Exception as e:  # noqa: BLE001
        check(label or f"{method} {path}", False, f"请求异常 {e}")
        return 0, {}, {}

    try:
        payload = json.loads(blob or b"{}")
    except Exception:  # noqa: BLE001
        payload = {"__text__": blob.decode("utf-8", "replace")}

    ok = expect is None or status == expect
    detail = "" if ok else f"期望 {expect} 实得 {status}: {str(payload)[:200]}"
    check(label or f"{method} {path}", ok, detail)
    return status, payload, hdrs


def data(payload):
    return payload.get("data") or {}


# ---------------------------------------------------------------- 1. 安全头

print("\n--- 安全头（生产加固第 2 条）---")
st, _, hdrs = call("GET", "/health", base=ROOT, expect=200, label="后端 /health")
check("X-Content-Type-Options: nosniff", hdrs.get("X-Content-Type-Options") == "nosniff",
      str(hdrs.get("X-Content-Type-Options")))
check("X-Frame-Options: DENY", hdrs.get("X-Frame-Options") == "DENY")
check("Referrer-Policy: no-referrer", hdrs.get("Referrer-Policy") == "no-referrer")
check("CSP 默认全拒（default-src 'none'）", "default-src 'none'" in (hdrs.get("Content-Security-Policy") or ""))
check("Cross-Origin-Opener-Policy: same-origin", hdrs.get("Cross-Origin-Opener-Policy") == "same-origin")
check("Cache-Control: no-store（儿童隐私不落缓存）", (hdrs.get("Cache-Control") or "").lower() == "no-store")
check("非生产不下发 HSTS（本机 http 不被锁定）", "Strict-Transport-Security" not in hdrs,
      str(hdrs.get("Strict-Transport-Security")))

# ---------------------------------------------------------------- 2. 注册与登录

ts = int(time.time())
print("\n--- 注册家长并取令牌 ---")
st, payload, _ = call("POST", "/auth/register", {
    "email": f"m7_{ts}@example.com", "password": env("SMOKE_PASSWORD"),
    "display_name": "M7冒烟", "invite_code": env("BOOTSTRAP_INVITE_CODE"),
}, expect=201, label="注册家长")
token = data(payload).get("access_token")
check("注册返回 access_token", bool(token))

st, payload, _ = call("POST", "/auth/login", {
    "account": f"m7_{ts}@example.com", "password": env("SMOKE_PASSWORD"),
}, expect=200, label="登录（account 字段）")
token = data(payload).get("access_token") or token

st, payload, _ = call("POST", "/children", {
    "nickname": "M7娃", "avatar_id": "rabbit", "birth_ym": "202006", "stage_code": "S1",
}, token=token, expect=201, label="创建孩子")
child_id = data(payload).get("id")
check("孩子创建成功并有 id", bool(child_id))

# ---------------------------------------------------------------- 3. 报表建议动作

print("\n--- 报表建议一键动作（§4.7）---")
# 越权：拿一个不存在的 childId，必须 404（与全站一致，不暴露 ID 是否存在）
st, payload, _ = call("POST", "/reports/00000000-0000-0000-0000-000000000000/suggestions/actions",
                      {"type": "review_day"}, token=token, expect=404, label="越权孩子 → 404")
check("越权返回错误包络", (payload.get("error") or {}).get("code") == "NOT_FOUND", str(payload)[:160])

# 非法动作类型：422（本项目校验类错误统一走 VALIDATION_FAILED / 422，不是 400）
call("POST", f"/reports/{child_id}/suggestions/actions", {"type": "no_such_action"},
     token=token, expect=422, label="非法动作类型 → 422")

# review_day：把节奏切成复习日，且真的写进了家长设置
st, payload, _ = call("POST", f"/reports/{child_id}/suggestions/actions", {"type": "review_day"},
                      token=token, expect=200, label="执行 review_day")
res = data(payload)
check("动作返回 applied=true 与可读 Detail", res.get("applied") is True and bool(res.get("detail")),
      str(res)[:200])

st, payload, _ = call("GET", "/parent/settings", token=token, expect=200, label="读家长设置")
check("review_day 真正改到 pace_mode=review", data(payload).get("pace_mode") == "review",
      str(data(payload).get("pace_mode")))

# 还原，避免污染后续
call("PUT", "/parent/settings", {"pace_mode": "standard"}, token=token, expect=200, label="还原标准节奏")

# ---------------------------------------------------------------- 4. 报表导出

print("\n--- 报表导出（csv 直出 / pdf 复用周报模板）---")
st, csv_blob, hdrs = call("GET", f"/reports/{child_id}/export?format=csv", token=token,
                          expect=200, label="CSV 导出")
check("CSV 的 Content-Type 正确", "text/csv" in (hdrs.get("Content-Type") or "").lower(),
      str(hdrs.get("Content-Type")))
check("CSV 是附件下载", "attachment" in (hdrs.get("Content-Disposition") or "").lower())

call("GET", f"/reports/{child_id}/export?format=xml", token=token, expect=422,
     label="非法 format → 422")

st, payload, _ = call("GET", f"/reports/{child_id}/export?format=pdf&days=30", token=token,
                      expect=202, label="PDF 导出（异步入队）")
res = data(payload)
pdf_job_id = res.get("job_id")
check("PDF 导出返回 job_id / pdf_url",
      bool(pdf_job_id) and res.get("pdf_url", "").endswith("/pdf"), str(res)[:200])

st, payload, _ = call("GET", f"/print/jobs/{pdf_job_id}", token=token, expect=200,
                      label="查 PDF 导出对应的打印任务")
check("导出的任务用的是 weekly_report 模板",
      data(payload).get("template_code") == "weekly_report", str(data(payload).get("template_code")))

# ---------------------------------------------------------------- 5. 打印与一键重印

print("\n--- 打印与一键重印（逐题复现）---")
st, payload, _ = call("GET", "/print/templates", token=token, expect=200, label="列出模板")
templates = data(payload).get("templates") or []
tpl = next((t for t in templates if t.get("code") == "math_drill"), None)
check("存在 math_drill 模板", tpl is not None)

# 用模板默认参数 + 固定 seed，保证重印时题面可逐题比对
params = {spec["name"]: spec["default"] for spec in (tpl.get("params") or [])}
params["seed"] = 12345
params["count"] = 10
body = {"template_code": tpl["code"], "params": params}
if tpl.get("needs_child"):
    body["child_id"] = child_id

st, payload, _ = call("POST", "/print/jobs", body, token=token, expect=201, label="建打印作业（固定 seed）")
orig_job_id = data(payload).get("id")
check("原作业创建成功", bool(orig_job_id))

st, orig_env, _ = call("GET", f"/print/jobs/{orig_job_id}/data", token=token, expect=200,
                       label="取原作业题面快照")
# /data 返回的是 {data, meta} 信封，题面在 data 里 —— 不拆包会永远取到空 items
orig_items = data(orig_env).get("items") or []
check("原作业有题目（items 非空）", len(orig_items) > 0, f"实得 {len(orig_items)}")

st, payload, _ = call("POST", f"/print/jobs/{orig_job_id}/reprint", token=token, expect=201,
                      label="一键重印")
new_job_id = data(payload).get("id")
check("重印产生新任务且 id 不同", bool(new_job_id) and new_job_id != orig_job_id,
      f"orig={orig_job_id} new={new_job_id}")
check("重印沿用同模板", data(payload).get("template_code") == tpl["code"])

st, new_env, _ = call("GET", f"/print/jobs/{new_job_id}/data", token=token, expect=200,
                      label="取重印作业题面快照")
new_items = data(new_env).get("items") or []
check("重印题面与原作业**逐题一致**（同 seed 可复现）", new_items == orig_items,
      f"orig={len(orig_items)} new={len(new_items)}，首题 orig={orig_items[:1]} new={new_items[:1]}")

# 越权重印：别人的/不存在的任务 → 404
call("POST", "/print/jobs/00000000-0000-0000-0000-000000000000/reprint", token=token,
     expect=404, label="重印不存在的任务 → 404")

# ---------------------------------------------------------------- 6. 限流（放最后）

print("\n--- 限流（登录档，按 IP 计——故置末）---")
got_429 = False
retry_after = ""
err_code = ""
for i in range(1, 21):
    # 故意用错口令（真口令加后缀），只为一层层打满登录限流桶
    st, payload, hdrs = call("POST", "/auth/login",
                             {"account": f"m7_{ts}@example.com", "password": env("SMOKE_PASSWORD") + "-wrong"},
                             expect=None, label=f"登录尝试 #{i}")
    if st == 429:
        got_429 = True
        retry_after = hdrs.get("Retry-After", "")
        err_code = (payload.get("error") or {}).get("code", "")
        break
check("突发打满登录档后返回 429", got_429)
check("429 带 Retry-After 头", bool(retry_after), str(retry_after))
check("429 错误码是 RATE_LIMITED", err_code == "RATE_LIMITED", str(err_code))

# ---------------------------------------------------------------- 汇总

print(f"\n===== M7 冒烟：{len(PASS)} 通过 / {len(FAIL)} 失败 =====")
for name in FAIL:
    print("  FAILED: " + name)
raise SystemExit(1 if FAIL else 0)
