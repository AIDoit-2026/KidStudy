# M5 打印中心端到端冒烟（server/tests/smoke，已入库，可反复重跑）。
#
# 用法：先把二进制编译好、起服务，再跑脚本（任意 CWD 都可以）：
#       cd server
#       go build -o tmp/worker.exe ./cmd/worker
#       HTTP_ADDR=:18080 go run ./cmd/api &
#       python tests/smoke/smoke_m5.py
#
# 前提：18080 上已跑着最新编译的 api.exe，server/tmp/worker.exe 已编译、
#       且本机能找到 Chromium（PDF 渲染要用）。
#
# 手法说明：
#  * 走真实接口建任务、取数据快照、下载 PDF；PDF 的真实性用文件魔数 %PDF- 与
#    真实页数（page_count）校验，不信任任何「预估」字段；
#  * PDF 渲染交给真实的 cmd/worker 进程（subprocess，显式以 server/ 为工作目录），
#    因此验证的是队列消费行为，而不是内存里的假实现；
#  * 纸质补录用「把某个知识点标错 → 它必须出现在错题本」做确定性验证，
#    避免依赖「答对几次算掌握」这种需要多轮的状态机时序。
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from _common import SERVER_ROOT, TMP_DIR, env

BASE = "http://127.0.0.1:18080/api/v1"
PASS, FAIL = [], []


OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def check(name, ok, detail=""):
    (PASS if ok else FAIL).append(name)
    print(("  PASS  " if ok else "  FAIL  ") + name + (("  <- " + detail) if detail else ""))


def call(method, path, body=None, token=None, expect=None, label="", raw=False):
    if any(ord(ch) > 127 for ch in path):
        base, _, qs = path.partition("?")
        path = urllib.parse.quote(base) + ("?" + urllib.parse.quote(qs, safe="=&") if qs else "")
    req = urllib.request.Request(BASE + path, method=method)
    if token:
        req.add_header("Authorization", "Bearer " + token)
    payload_bytes = None
    if body is not None:
        payload_bytes = json.dumps(body, ensure_ascii=False).encode()
        req.add_header("Content-Type", "application/json")
    try:
        resp = OPENER.open(req, data=payload_bytes, timeout=90)
        status, blob = resp.status, resp.read()
        headers = dict(resp.headers)
        payload = {} if raw else json.loads(blob or b"{}")
    except urllib.error.HTTPError as e:
        status, blob = e.code, e.read()
        headers = dict(e.headers)
        payload = {} if raw else json.loads(blob or b"{}")
    except Exception as e:
        check(label or f"{method} {path}", False, f"请求异常 {e}")
        return 0, {}, {"headers": {}, "blob": None}

    ok = expect is None or status == expect
    detail = "" if ok else f"期望 {expect} 实得 {status}: {str(payload)[:200]}"
    check(label or f"{method} {path}", ok, detail)
    return status, payload, {"headers": headers, "blob": blob}


def data(payload):
    return payload.get("data") or {}


def meta_page(payload):
    return (payload.get("meta") or {}).get("page") or {}


def run_bin(args, label, timeout=300):
    proc = subprocess.run(args, cwd=SERVER_ROOT, capture_output=True, text=True, timeout=timeout)
    ok = proc.returncode == 0
    check(label, ok, "" if ok else (proc.stderr or proc.stdout)[-220:])
    if ok:
        # worker 单条渲染失败时依然以 0 退出（只把它记成 failed 计数），
        # 所以成功时也把它的收尾摘要打出来 —— 否则「任务卡在 queued」会毫无线索。
        # （曾踩过：首次渲染 Chromium 冷启动失败，任务回落 queued，日志被吞，症状看不出来。）
        tail_line = (proc.stdout or "").strip().splitlines()[-1:] or [""]
        print("    · " + tail_line[0][-160:])
    return proc


def create_job(token, code, child=None, params=None, expect=201, label=""):
    body = {"template_code": code}
    if child:
        body["child_id"] = child
    if params:
        body["params"] = params
    st, p, _ = call("POST", "/print/jobs", body, token=token, expect=expect,
                    label=label or f"建任务 {code}")
    return st, data(p)


# ---------------------------------------------------------------- 准备账号

ts = int(time.time())
st, p, _ = call("POST", "/auth/register", {
    "email": f"m5_{ts}@example.com", "password": env("SMOKE_PASSWORD"),
    "display_name": "M5冒烟", "invite_code": env("BOOTSTRAP_INVITE_CODE"),
}, expect=201, label="注册家长")
token = data(p).get("access_token")
assert token, "注册没拿到 token"

st, p, _ = call("POST", "/children", {
    "nickname": "哥哥", "avatar_id": "panda", "birth_ym": "202001", "stage_code": "S1",
}, token=token, expect=201, label="创建孩子 A")
child_a = data(p).get("id")

st, p, _ = call("POST", "/auth/register", {
    "email": f"m5o_{ts}@example.com", "password": env("SMOKE_PASSWORD"),
    "display_name": "路人", "invite_code": env("BOOTSTRAP_INVITE_CODE"),
}, expect=201, label="注册第二个家长")
other_token = data(p).get("access_token")

# ---------------------------------------------------------------- 模板清单

print("\n--- 模板清单 ---")
st, payload, _ = call("GET", "/print/templates", token=token, expect=200, label="列出模板")
tpls = data(payload).get("templates") or []
codes = [t.get("code") for t in tpls]
expect_codes = ["hanzi_trace", "hanzi_flash", "pinyin_grid", "math_drill", "math_compare",
                "en_word_card", "letter_trace", "match_lines", "story_booklet", "weekly_report"]
check("模板共 10 套", len(tpls) == 10, str(len(tpls)))
check("模板 code 与设计一致", set(codes) == set(expect_codes), str(codes))
check("每套都带必填字段",
      all(t.get("code") and t.get("name") and t.get("category") and t.get("paper_size")
          and isinstance(t.get("params"), list) and t.get("params") for t in tpls), "")
trace = [t for t in tpls if t.get("code") == "hanzi_trace"][0]
pnames = {q["name"] for q in trace["params"]}
check("描红卡参数含 range/font_size/with_pinyin",
      {"range", "font_size", "with_pinyin"} <= pnames, str(sorted(pnames)))
check("描红卡可补录", trace.get("answerable") is True, str(trace.get("answerable")))
flash = [t for t in tpls if t.get("code") == "hanzi_flash"][0]
check("闪卡不可补录（纯教具）", flash.get("answerable") is False, str(flash.get("answerable")))
want_child = {t["code"]: t.get("needs_child") for t in tpls}
check("数学与报告类需要孩子",
      want_child.get("math_drill") and want_child.get("weekly_report"), str(want_child))
call("GET", "/print/templates", expect=401, label="未登录 401")

# ---------------------------------------------------------------- 逐套建任务

print("\n--- 10 套模板都能建出任务 ---")
built = {}
for code in expect_codes:
    # 连线题硬要求 >=4 组可配对素材，而新注册的孩子没有「近期新学」记录，
    # 默认 range=recent 会正确地返回 422；这里按阶段取内容来覆盖它的构建路径。
    params = {"range": "stage", "stage": "S1", "subject": "chinese"} if code == "match_lines" else {}
    st, job = create_job(token, code, child=child_a, params=params, label=f"建任务 {code}")
    if st == 201:
        built[code] = job
        check(f"{code} 返回可用视图",
              bool(job.get("id")) and job.get("status") in ("created", "queued")
              and job.get("preview_url") and job.get("data_url") and job.get("pdf_url"),
              str({k: job.get(k) for k in ("status", "preview_url", "pdf_url")}))
check("10 套都建成", len(built) == 10, str(len(built)))

# ---------------------------------------------------------------- 参数与边界

print("\n--- 参数校验 ---")
call("POST", "/print/jobs", {"template_code": "nope"}, token=token, expect=422, label="未知模板 422")
call("POST", "/print/jobs", {"template_code": ""}, token=token, expect=422, label="空模板 422")
call("POST", "/print/jobs", {"template_code": "math_drill"}, token=token, expect=422,
     label="需要孩子却没给 422")
call("POST", "/print/jobs", {"template_code": "hanzi_trace", "child_id": "not-a-uuid"},
     token=token, expect=422, label="非法 child_id 422")
call("POST", "/print/jobs",
     {"template_code": "hanzi_trace", "params": {"range": "recent"}},
     token=token, expect=422, label="近期新学取内容却没孩子 422")

# 越权：用别人的孩子 id 开打印应 404
st, p, _ = call("POST", "/children", {"nickname": "别人家娃", "avatar_id": "panda",
                                      "birth_ym": "202101", "stage_code": "S1"},
                token=other_token, expect=201, label="创建别人的孩子")
other_child = data(p).get("id")
call("POST", "/print/jobs", {"template_code": "math_drill", "child_id": other_child},
     token=token, expect=404, label="拿别人的孩子开打印 404")

# ---------------------------------------------------------------- 数据快照与预览

print("\n--- 数据快照与预览 ---")
st, trace_job = create_job(token, "hanzi_trace", child=child_a,
                           params={"range": "stage", "stage": "S1", "subject": "chinese",
                                   "count": 6, "with_pinyin": True},
                           label="建描红卡任务（S1）")
trace_id = trace_job["id"]
check("描红卡预排页数 >= 1", (trace_job.get("planned_pages") or 0) >= 1, str(trace_job.get("planned_pages")))

st, payload, _ = call("GET", f"/print/jobs/{trace_id}/data", token=token, expect=200, label="取数据快照")
d = data(payload)
check("快照模板码正确", d.get("template_code") == "hanzi_trace", str(d.get("template_code")))
check("快照带渲染契约版本", d.get("render_version") == 1, str(d.get("render_version")))
check("快照 items 非空且 <= 6", 0 < len(d.get("items") or []) <= 6, str(len(d.get("items") or [])))
check("items 带知识点 id（可补录）",
      all(it.get("kp_id") for it in (d.get("items") or [])), str((d.get("items") or [])[:1]))
check("快照带元信息（孩子/纸张）", bool((d.get("meta") or {}).get("paper")), str(d.get("meta")))

st, payload, meta = call("GET", f"/print/jobs/{trace_id}/preview", token=token,
                         expect=200, label="取预览 HTML", raw=True)
html = (meta["blob"] or b"").decode("utf-8", "replace")
check("预览是 HTML", "text/html" in (meta["headers"].get("Content-Type") or ""),
      meta["headers"].get("Content-Type"))
check("预览含完整文档结构", "<html" in html.lower() and "</html>" in html.lower(), html[:60])
check("预览含模板标记与页脚", "hanzi-trace" in html or "描红" in html, html[:80])
check("预览不缓存（no-store）", "no-store" in (meta["headers"].get("Cache-Control") or ""),
      meta["headers"].get("Cache-Control"))

# ---------------------------------------------------------------- 口算：同 seed 可复现

print("\n--- 口算题卡：同 seed 可复现 ---")
seed_params = {"math_template": "M2_ADD10", "count": 20, "seed": 12345, "columns": 4}
st, j1 = create_job(token, "math_drill", child=child_a, params=seed_params, label="口算任务 1")
st, j2 = create_job(token, "math_drill", child=child_a, params=seed_params, label="口算任务 2")
math_id = j1["id"]
_, d1, _ = call("GET", f"/print/jobs/{j1['id']}/data", token=token, expect=200, label="口算快照 1")
_, d2, _ = call("GET", f"/print/jobs/{j2['id']}/data", token=token, expect=200, label="口算快照 2")
i1 = [it.get("main") for it in (data(d1).get("items") or [])]
i2 = [it.get("main") for it in (data(d2).get("items") or [])]
check("口算 20 题", len(i1) == 20, str(len(i1)))
check("同 seed 两次题面完全一致", i1 == i2 and len(i1) == 20, f"{len(i1)} vs {len(i2)}")
check("口算带答案页", len(data(d1).get("answer_items") or []) == 20,
      str(len(data(d1).get("answer_items") or [])))
call("POST", "/print/jobs", {"template_code": "math_drill", "child_id": child_a,
                             "params": {"math_template": "NO_SUCH", "count": 10}},
     token=token, expect=422, label="未知题型模板 422")

# ---------------------------------------------------------------- PDF 队列与渲染

print("\n--- PDF 队列与渲染 ---")
call("GET", f"/print/jobs/{math_id}/pdf", token=token, expect=409, label="未生成时下载 409")
st, payload, _ = call("POST", f"/print/jobs/{math_id}/pdf", token=token, expect=202, label="排队渲染 202")
check("入队后状态 queued", data(payload).get("status") == "queued", str(data(payload).get("status")))

run_bin([os.path.join(TMP_DIR, "worker.exe"), "-print"], "worker 消费渲染队列")

st, payload, _ = call("GET", f"/print/jobs/{math_id}", token=token, expect=200, label="渲染后取任务")
job = data(payload)
check("渲染完成 status=ready", job.get("status") == "ready", str(job.get("status")))
check("pdf_ready=true", job.get("pdf_ready") is True, str(job.get("pdf_ready")))
check("真实页数已写回（>=1）", (job.get("page_count") or 0) >= 1, str(job.get("page_count")))

st, payload, meta = call("GET", f"/print/jobs/{math_id}/pdf", token=token, expect=200,
                         label="下载 PDF", raw=True)
blob = meta["blob"] or b""
check("Content-Type 是 PDF", "application/pdf" in (meta["headers"].get("Content-Type") or ""),
      meta["headers"].get("Content-Type"))
check("文件魔数是 %PDF-", blob.startswith(b"%PDF-"), str(blob[:8]))
check("PDF 体积合理（>1KB）", len(blob) > 1024, str(len(blob)))

st, payload, _ = call("POST", f"/print/jobs/{math_id}/pdf", token=token, expect=202,
                      label="重复排队（幂等）")
check("已就绪任务保持 ready", data(payload).get("status") == "ready", str(data(payload).get("status")))

# ---------------------------------------------------------------- 纸质补录

print("\n--- 纸质补录 ---")
_, snap, _ = call("GET", f"/print/jobs/{trace_id}/data", token=token, expect=200, label="取描红快照")
kps = [it["kp_id"] for it in (data(snap).get("items") or [])]
kp_first = kps[0]

st, payload, _ = call("POST", f"/print/jobs/{trace_id}/mark-done",
                      {"items": [{"kp_id": kp_first, "correct": False}], "note": "纸上第一行写错了"},
                      token=token, expect=200, label="补录（标错一个）")
res = data(payload)
check("补录计数正确", res.get("item_count") == len(kps) and res.get("correct_count") == len(kps) - 1,
      f"item_count={res.get('item_count')} correct={res.get('correct_count')} 期望={len(kps)}/{len(kps)-1}")
check("补录建了会话", bool(res.get("session_id")), str(res.get("session_id")))
check("首次补录不是重复提交", res.get("already_done") is False, str(res.get("already_done")))

st, payload, _ = call("POST", f"/print/jobs/{trace_id}/mark-done", {}, token=token,
                      expect=200, label="重复补录")
check("重复补录幂等（already_done）", data(payload).get("already_done") is True,
      str(data(payload)))

st, payload, _ = call("GET", f"/print/jobs/{trace_id}", token=token, expect=200, label="取补录后任务")
check("任务标记已完成", data(payload).get("marked_done") is True, str(data(payload).get("marked_done")))
check("带完成时间", bool(data(payload).get("marked_done_at")), str(data(payload).get("marked_done_at")))

st, payload, _ = call("GET", f"/mastery/wrong-book?child_id={child_a}", token=token,
                      expect=200, label="查错题本")
wb = payload.get("data") or []
check("标错的知识点进了错题本", any(e.get("kp_id") == kp_first for e in wb),
      f"共 {len(wb)} 条: {[e.get('kp_id') for e in wb][:3]}")

fb_id = built["hanzi_flash"]["id"]
call("POST", f"/print/jobs/{fb_id}/mark-done", {}, token=token, expect=422, label="闪卡补录 422")
wr_id = built["weekly_report"]["id"]
call("POST", f"/print/jobs/{wr_id}/mark-done", {}, token=token, expect=422, label="周报补录 422")
call("POST", f"/print/jobs/{trace_id}/mark-done",
     {"items": [{"kp_id": "not-a-uuid", "correct": True}]}, token=token, expect=422,
     label="非法 kp_id 422")

# ---------------------------------------------------------------- 打印记录列表

print("\n--- 打印记录 ---")
st, payload, _ = call("GET", "/parent/print-jobs?limit=50", token=token, expect=200, label="打印记录列表")
rows = payload.get("data") or []
pg = meta_page(payload)
check("列表非空", len(rows) > 0, str(len(rows)))
check("分页 total 与行数一致", (pg.get("total") or 0) >= len(rows), str(pg))
check("列表项字段齐全", all(r.get("id") and r.get("template_code") and r.get("status") for r in rows), "")
check("已渲染的项标了 pdf_ready", any(r.get("pdf_ready") for r in rows), str([r.get("status") for r in rows][:5]))

st, payload, _ = call("GET", f"/parent/print-jobs?childId={child_a}", token=token, expect=200,
                      label="按孩子过滤")
rows_a = payload.get("data") or []
check("过滤后都属该孩子", all(r.get("child_id") == child_a for r in rows_a), "")
call("GET", "/parent/print-jobs?childId=nonsense", token=token, expect=422, label="非法 childId 422")

# ---------------------------------------------------------------- 越权

print("\n--- 越权 ---")
call("GET", f"/print/jobs/{trace_id}", expect=401, label="未登录取任务 401")
call("POST", "/print/jobs", {"template_code": "hanzi_trace"}, expect=401, label="未登录建任务 401")
call("GET", f"/print/jobs/{trace_id}", token=other_token, expect=404, label="别人的任务 404")
call("GET", f"/print/jobs/{trace_id}/data", token=other_token, expect=404, label="别人的数据 404")
call("GET", f"/print/jobs/{trace_id}/preview", token=other_token, expect=404, label="别人的预览 404")
call("POST", f"/print/jobs/{trace_id}/pdf", token=other_token, expect=404, label="别人的排队 404")
call("POST", f"/print/jobs/{trace_id}/mark-done", {}, token=other_token, expect=404,
     label="别人的补录 404")
call("GET", "/print/jobs/not-a-uuid", token=token, expect=404, label="非法任务 id 404")
call("GET", "/parent/print-jobs", expect=401, label="未登录取打印记录 401")

print(f"\n===== M5 冒烟：{len(PASS)} 项通过，{len(FAIL)} 项失败 =====")
if FAIL:
    for f in FAIL:
        print("  FAILED: " + f)
    sys.exit(1)
