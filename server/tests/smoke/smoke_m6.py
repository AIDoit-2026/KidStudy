# M6「护眼与多端」端到端冒烟（server/tests/smoke，已入库，可反复重跑）。
#
# 用法：起后端（cd server && go run ./cmd/api）与前端（cd web && npm run dev -- --port 5173），
#       再 python server/tests/smoke/smoke_m6.py   —— 任意 CWD 都可以
#
# 前提：18080 上跑着最新 api.exe；5173 上跑着 `npm run dev`（vite）。
#
# M6 本身是前端里程碑，后端没有新端点，所以这份脚本不重复验业务，
# 而是**逐条验证新前端所依赖的后端契约**——任何一条破了，页面就会白给：
#
#   1. CORS：带 Cookie 的跨源请求必须回显白名单来源 + Allow-Credentials，
#      且非白名单来源一个 CORS 头都不给（前端 401 静默刷新全靠这条）。
#   2. 响应包络：恒为 {data, meta.request_id}；分页带 meta.page
#      （前端 client.ts 的 unwrap / unwrapPaged 就是照这个写的）。
#   3. Refresh Cookie 轮换：每次刷新都换新令牌，且是 HttpOnly。
#   4. PIN 校验发的是 5 分钟 unlock 令牌（锁屏 / 跳过休息用它解锁）。
#   5. 扫码登录 SSE 四态：事件名 scanned → confirmed，载荷带 exchange_code
#      （前端 EventSource 按事件名分派，名字错一个字母就是死页面）。
#   6. 护眼计时的权威值来自 /parent/settings（rest_interval_min / session_limit_min）。
#   7. 打印预览 HTML 与 PDF 同源，且**不含任何 <script>**——
#      前端 iframe 的 sandbox 故意不给 allow-scripts，含脚本的模板会静默失效。
#   8. dev server 真的把 VITE_API_BASE_URL 注入进了产物，且主机是 localhost
#      （同站，SameSite=Lax 的 Refresh Cookie 才会随请求发出）。
import http.cookiejar
import json
import os
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from _common import env

API = "http://127.0.0.1:18080/api/v1"
WEB = "http://localhost:5173"
ORIGIN_OK = "http://localhost:5173"
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
    """发 JSON 请求并断言状态码。opener 传 PLAIN 可绕开 CookieJar（测「无 Cookie」场景）。"""
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


def fetch_text(url, token=None, expect=200, label=""):
    """抓一段文本（HTML / 前端模块），不做 JSON 解析，也不走代理。"""
    req = urllib.request.Request(url)
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with PLAIN.open(req, timeout=60) as resp:
            status, text, hdrs = resp.status, resp.read().decode("utf-8", "replace"), dict(resp.headers)
    except urllib.error.HTTPError as e:
        status, text, hdrs = e.code, e.read().decode("utf-8", "replace"), dict(e.headers)
    except Exception as e:  # noqa: BLE001
        check(label or url, False, f"请求异常 {e}")
        return 0, "", {}
    ok = status == expect
    check(label or url, ok, "" if ok else f"期望 {expect} 实得 {status}")
    return status, text, hdrs


def data(payload):
    return payload.get("data") or {}


def has_envelope(payload):
    return isinstance(payload, dict) and "data" in payload and "request_id" in (payload.get("meta") or {})


# ---------------------------------------------------------------- 1. 服务与 CORS

print("\n--- 服务存活与 CORS（前端静默刷新的前提）---")
call("GET", "/health", expect=200, label="后端 /health", base="http://127.0.0.1:18080")
call("GET", "/ready", expect=200, label="后端 /ready", base="http://127.0.0.1:18080")

st, _, hdrs = call(
    "OPTIONS", "/auth/login",
    headers={
        "Origin": ORIGIN_OK,
        "Access-Control-Request-Method": "POST",
        "Access-Control-Request-Headers": "content-type,authorization",
    },
    expect=204, label="预检 OPTIONS /auth/login",
)
check("CORS 回显白名单来源", hdrs.get("Access-Control-Allow-Origin") == ORIGIN_OK,
      f"实得 {hdrs.get('Access-Control-Allow-Origin')!r}")
check("CORS 允许携带 Cookie", (hdrs.get("Access-Control-Allow-Credentials") or "").lower() == "true",
      f"实得 {hdrs.get('Access-Control-Allow-Credentials')!r}")
check("CORS 允许 Authorization 头",
      "authorization" in (hdrs.get("Access-Control-Allow-Headers") or "").lower(),
      f"实得 {hdrs.get('Access-Control-Allow-Headers')!r}")
check("CORS 声明 Vary: Origin", "origin" in (hdrs.get("Vary") or "").lower())

st, _, hdrs = call(
    "OPTIONS", "/auth/login",
    headers={"Origin": "http://evil.example", "Access-Control-Request-Method": "POST"},
    # 非白名单来源不会被 CORS 中间件接管，请求会落到路由上；
    # chi 没给 /auth/login 注册 OPTIONS，于是 405。关键不是状态码，
    # 而是**一个 CORS 头都不给**，浏览器自己会拦掉。
    expect=405, label="预检（非白名单来源）原样透传",
)
check("非白名单来源不给 CORS 头", "Access-Control-Allow-Origin" not in hdrs)

# 非预检的真实请求也要带 CORS 头，否则浏览器拿得到响应体却读不了
req = urllib.request.Request("http://127.0.0.1:18080/health")
req.add_header("Origin", ORIGIN_OK)
with PLAIN.open(req, timeout=20) as resp:
    real_hdrs = dict(resp.headers)
check("非预检请求带 Origin 时也回显来源",
      real_hdrs.get("Access-Control-Allow-Origin") == ORIGIN_OK,
      f"实得 {real_hdrs.get('Access-Control-Allow-Origin')!r}")

# ---------------------------------------------------------------- 2. 注册与包络

ts = int(time.time())
print("\n--- 注册、令牌与响应包络 ---")
st, payload, _ = call("POST", "/auth/register", {
    "email": f"m6_{ts}@example.com", "password": env("SMOKE_PASSWORD"),
    "display_name": "M6冒烟", "invite_code": env("BOOTSTRAP_INVITE_CODE"),
}, expect=201, label="注册家长")
check("成功响应是 {data, meta.request_id} 包络", has_envelope(payload))
token = data(payload).get("access_token")
check("注册返回 access_token", bool(token))
check("注册返回 expires_in", isinstance(data(payload).get("expires_in"), int))

st, payload, hdrs = call("POST", "/auth/login", {
    # 后端登录只认一个 account 字段（邮箱或手机号都塞这里）。
    # 前端一度按 {email, phone} 发，必然 422 —— 这条就是那个 bug 的回归哨兵。
    "account": f"m6_{ts}@example.com", "password": env("SMOKE_PASSWORD"),
}, expect=200, label="登录（account 字段）")
token = data(payload).get("access_token") or token

st, payload, _ = call("POST", "/auth/login", {
    "email": f"m6_{ts}@example.com", "password": env("SMOKE_PASSWORD"),
}, expect=422, label="登录（误用 email 字段）应被拒")
check("按 email 发登录必然失败（证明前端必须发 account）",
      (payload.get("error") or {}).get("code") == "VALIDATION_FAILED",
      str(payload)[:160])

set_cookie = hdrs.get("Set-Cookie", "")
check("登录下发 Refresh Cookie", "refresh" in set_cookie.lower(), set_cookie[:120])
check("Refresh Cookie 是 HttpOnly", "httponly" in set_cookie.lower(), set_cookie[:120])

st, payload, _ = call("GET", "/auth/me", token=token, expect=200, label="GET /auth/me")
me = data(payload)
check("me 带 has_pin 字段（前端据此提示设 PIN）", "has_pin" in me, str(me)[:160])
check("me 初始 has_pin=false", me.get("has_pin") is False)

# ---------------------------------------------------------------- 3. 刷新令牌轮换

print("\n--- 401 静默刷新链路 ---")


def refresh_cookie():
    for c in COOKIES:
        if "refresh" in c.name.lower():
            return c.value
    return ""


before = refresh_cookie()
check("已持有 Refresh Cookie", bool(before))

st, payload, hdrs = call("POST", "/auth/refresh", expect=200, label="带 Cookie 刷新")
fresh = data(payload).get("access_token")
check("刷新返回非空 access_token（同秒签发时 JWT 可能字节相同，只断言非空）", bool(fresh))

st, _, _ = call("GET", "/auth/me", token=fresh, expect=200, label="用刷新后的令牌访问 /auth/me")
check("刷新后的令牌确实可用", st == 200)
check("刷新同时轮换了 Set-Cookie", "Set-Cookie" in hdrs)

after = refresh_cookie()
check("Refresh Cookie 值确实换了（每次刷新都轮换）", bool(after) and after != before)

call("GET", "/auth/me", expect=401, label="无令牌访问 /auth/me 返回 401")

# 「无 Cookie」必须换一个干净的 opener —— 带 CookieJar 的 OPENER 会自动补上 Cookie，
# 否则测出来的是「有 Cookie」，这条永远会假绿。
st, payload, _ = call("POST", "/auth/refresh", expect=401, label="无 Cookie 刷新返回 401",
                      opener=PLAIN)
check("刷新失败响应是错误包络", "error" in payload and "code" in (payload.get("error") or {}))

# ---------------------------------------------------------------- 4. PIN 与解锁令牌

print("\n--- 家长 PIN（锁屏 / 跳过休息的解锁凭据）---")
st, payload, _ = call("PUT", "/auth/pin", {"pin": env("SMOKE_PIN")}, token=token, expect=200, label="设置 PIN")
check("设置 PIN 返回 has_pin=true", data(payload).get("has_pin") is True)

st, payload, _ = call("PUT", "/auth/pin", {"pin": "abc"}, token=token, expect=422, label="非法 PIN 被拒")
check("非法 PIN 返回 422 且带 field 明细", bool(((payload.get("error") or {}).get("details"))))

st, payload, _ = call("POST", "/auth/pin/verify", {"pin": env("SMOKE_PIN")}, token=token, expect=200,
                      label="校验 PIN")
unlock = data(payload)
check("PIN 校验返回 unlock_token", bool(unlock.get("unlock_token")))
check("unlock_token 有效期约 5 分钟",
      295 <= (unlock.get("expires_in") or 0) <= 300,
      f"实得 {unlock.get('expires_in')}（后端用 time.Until 截断，5 分钟会显示 299）")

call("POST", "/auth/pin/verify", {"pin": "0000"}, token=token, expect=401, label="错误 PIN 被拒")

# ---------------------------------------------------------------- 5. 扫码登录 SSE

print("\n--- 扫码登录（SSE 四态，事件名一字不能错）---")
st, payload, _ = call("GET", "/auth/qrcode", expect=200, label="取二维码（免登录）")
qr = data(payload)
qr_token = qr.get("qr_token")
check("二维码含 qr_token / deep_link / expires_in",
      bool(qr_token) and bool(qr.get("deep_link")) and qr.get("expires_in") == 60)
check("deep_link 指向同站主机（localhost）", "localhost" in str(qr.get("deep_link")))

events, errs = [], []


def sse_reader():
    try:
        req = urllib.request.Request(f"{API}/auth/qrcode/{qr_token}/events")
        resp = PLAIN.open(req, timeout=20)
        events.append(("__ctype__", resp.headers.get("Content-Type", "")))
        while True:
            raw = resp.readline()
            if not raw:
                break
            line = raw.decode("utf-8", "replace").rstrip("\n")
            if line.startswith("event:"):
                name = line.split(":", 1)[1].strip()
                nxt = resp.readline().decode("utf-8", "replace").rstrip("\n")
                body = nxt.split(":", 1)[1].strip() if nxt.startswith("data:") else ""
                events.append((name, body))
                if name in ("confirmed", "expired", "failed"):
                    break
    except Exception as e:  # noqa: BLE001
        errs.append(str(e))


thread = threading.Thread(target=sse_reader, daemon=True)
thread.start()
time.sleep(0.5)  # 等订阅建立，否则会漏掉 scanned 事件

call("POST", "/auth/qrcode/scan", {"qr_token": qr_token}, token=token, expect=200, label="手机扫码")
call("POST", "/auth/qrcode/confirm", {"qr_token": qr_token}, token=token, expect=200, label="手机确认")
thread.join(timeout=15)

names = [n for n, _ in events]
ctype = next((b for n, b in events if n == "__ctype__"), "")
check("SSE 用 text/event-stream", "text/event-stream" in ctype, ctype)
check("SSE 收到 scanned 事件", "scanned" in names, str(names))
check("SSE 收到 confirmed 事件（终态后断开）", "confirmed" in names, str(names))
check("SSE 读取无异常", not errs, str(errs))

exchange_code = ""
for n, b in events:
    if n == "confirmed":
        try:
            exchange_code = json.loads(b).get("exchange_code", "")
        except Exception:  # noqa: BLE001
            pass
check("confirmed 载荷带 exchange_code", bool(exchange_code))

st, payload, hdrs = call("POST", "/auth/qrcode/exchange",
                         {"qr_token": qr_token, "exchange_code": exchange_code},
                         expect=200, label="兑换码换令牌")
check("兑换得到 access_token", bool(data(payload).get("access_token")))
check("兑换同样下发 Refresh Cookie", "Set-Cookie" in hdrs)

# ---------------------------------------------------------------- 6. 护眼与节奏设置

print("\n--- 护眼计时依赖的权威设置 ---")
st, payload, _ = call("GET", "/parent/settings", token=token, expect=200, label="读家长设置")
s = data(payload)
for field in ("daily_limit_min", "session_limit_min", "rest_interval_min",
              "require_parent_confirm", "pace_mode", "compare_children"):
    check(f"设置含 {field}", field in s, str(s)[:200])

st, payload, _ = call("PUT", "/parent/settings", {"rest_interval_min": 25}, token=token,
                      expect=200, label="把休息间隔改成 25 分钟")
check("休息间隔写入生效", data(payload).get("rest_interval_min") == 25)

st, payload, _ = call("GET", "/parent/settings", token=token, expect=200, label="复读确认")
check("休息间隔持久化", data(payload).get("rest_interval_min") == 25)

call("PUT", "/parent/settings", {"rest_interval_min": 20}, token=token, expect=200, label="还原为 20 分钟")

# ---------------------------------------------------------------- 7. 今日任务与打印

print("\n--- 今日任务（TimeBar / 首页）---")
st, payload, _ = call("POST", "/children", {
    "nickname": "妹妹", "avatar_id": "rabbit", "birth_ym": "202103", "stage_code": "S1",
}, token=token, expect=201, label="创建孩子")
child_id = data(payload).get("id")

st, payload, _ = call("GET", f"/practice/today?child_id={child_id}", token=token, expect=200,
                      label="取今日任务")
plan = data(payload)
for field in ("remaining_minutes", "used_minutes", "daily_limit_min", "due_count", "items"):
    check(f"今日任务含 {field}", field in plan, str(plan)[:200])

print("\n--- 打印中心（预览与 PDF 同源）---")
st, payload, _ = call("GET", "/print/templates", token=token, expect=200, label="列出模板")
templates = data(payload).get("templates") or []
check("模板非空", len(templates) > 0, f"实得 {len(templates)}")
required_tpl = {"code", "name", "category", "description", "paper_size",
                "double_sided", "answerable", "needs_child", "params"}
check("模板字段与前端类型一致",
      all(required_tpl <= set(t) for t in templates),
      str(sorted(set(templates[0]) if templates else [])))

param_ok = True
for t in templates:
    for spec in t.get("params") or []:
        if not {"name", "label", "type", "default"} <= set(spec):
            param_ok = False
check("参数 schema 可驱动动态表单（name/label/type/default 齐全）", param_ok)

tpl = next((t for t in templates if t.get("needs_child")), templates[0])
st, payload, _ = call("POST", "/print/jobs", {
    "template_code": tpl["code"], "child_id": child_id,
    "params": {spec["name"]: spec["default"] for spec in (tpl.get("params") or [])},
}, token=token, expect=201, label=f"建打印作业 {tpl['code']}")
job = data(payload)
job_id = job.get("id")
check("作业返回 planned_pages / status", "planned_pages" in job and "status" in job)

st, html, ctype_headers = fetch_text(f"{API}/print/jobs/{job_id}/preview", token=token,
                                     label="取预览 HTML")
preview_ctype = ctype_headers.get("Content-Type", "")
check("预览返回 HTML", "html" in preview_ctype.lower() or html.lstrip().startswith("<"),
      preview_ctype)
check("预览 HTML 是完整文档", "<html" in html.lower())
check("预览 HTML 不含 <script>（前端 iframe 禁脚本）", "<script" not in html.lower())
check("预览 HTML 自带样式（不依赖前端 CSS）",
      "<style" in html.lower() or "@media print" in html.lower())

st, payload, _ = call("POST", f"/print/jobs/{job_id}/pdf", token=token, expect=202,
                      label="入队渲染 PDF（异步）")
check("入队后状态是 queued/rendering/ready",
      data(payload).get("status") in ("queued", "rendering", "ready"),
      str(data(payload).get("status")))

st, payload, _ = call("GET", "/parent/print-jobs", token=token, expect=200, label="打印作业列表")
page = (payload.get("meta") or {}).get("page") or {}
check("打印作业列表带 meta.page", {"offset", "limit", "total"} <= set(page), str(page))
check("打印作业列表 data 是数组", isinstance(data(payload), list))

# ---------------------------------------------------------------- 8. dev server 注入

print("\n--- 前端 dev server 与注入 ---")
st, html, _ = fetch_text(f"{WEB}/", label="dev server 首页")
check("首页是 Vite 挂载页（含 #root）", 'id="root"' in html)

st, module_src, _ = fetch_text(f"{WEB}/src/api/client.ts", label="dev server 提供客户端模块")
check("VITE_API_BASE_URL 已注入产物", "18080/api/v1" in module_src, module_src[:160])
check("注入的主机是 localhost（同站，Cookie 才会随请求发出）",
      "localhost:18080" in module_src, module_src[:160])
check("客户端不含硬编码回退地址", "127.0.0.1:18080" not in module_src, module_src[:160])

# ---------------------------------------------------------------- 汇总

print(f"\n===== M6 冒烟：{len(PASS)} 通过 / {len(FAIL)} 失败 =====")
for name in FAIL:
    print("  FAILED: " + name)
raise SystemExit(1 if FAIL else 0)
