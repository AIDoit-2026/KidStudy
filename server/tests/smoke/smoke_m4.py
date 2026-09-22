# M4 评价与报表端到端冒烟（server/tests/smoke，已入库，可反复重跑）。
#
# 用法：先把两个二进制编译好、起服务，再跑脚本（任意 CWD 都可以）：
#       cd server
#       go build -o tmp/worker.exe ./cmd/worker
#       go build -o tmp/importer.exe ./cmd/importer
#       HTTP_ADDR=:18080 go run ./cmd/api &
#       python tests/smoke/smoke_m4.py
#
# 手法说明：
#  * 数学题的答案能从题面直接算出来，所以「答对」路径可以确定性验证；
#  * 日汇总 worker 与基准线铺线都通过 subprocess 调用已编译的二进制（server/tmp/worker.exe、
#    server/tmp/importer.exe），且显式以 server/ 为工作目录 —— godotenv 是从**当前工作目录**
#    读 .env 的，不固定 CWD 的话换个目录跑就会因为缺配置而失败。
#    因此本脚本验证的是真实进程行为，不是内存里的假实现；
#  * 「连续 3 天低正确率」这条建议需要跨天数据，脚本用一条 SQL fixture 造出 3 天的
#    错误作答（只在 dev 环境、只动 answer_logs），随后按真实接口取建议。
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from _common import SERVER_ROOT, TMP_DIR, env

BASE = "http://127.0.0.1:18080/api/v1"
PSQL = r"C:\Program Files\PostgreSQL\18\bin\psql.exe"
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
        resp = OPENER.open(req, data=payload_bytes, timeout=60)
        status, blob = resp.status, resp.read()
        headers = dict(resp.headers)
        payload = {} if raw else json.loads(blob or b"{}")
    except urllib.error.HTTPError as e:
        status, blob = e.code, e.read()
        headers = dict(e.headers)
        payload = {} if raw else json.loads(blob or b"{}")
    except Exception as e:
        check(label or f"{method} {path}", False, f"请求异常 {e}")
        return 0, {}, {}

    ok = expect is None or status == expect
    detail = "" if ok else f"期望 {expect} 实得 {status}: {str(payload)[:180]}"
    check(label or f"{method} {path}", ok, detail)
    return status, payload, {"headers": headers, "blob": (blob if raw else None)}


def data(payload):
    return payload.get("data") or {}


def run_bin(args, label):
    """跑已编译的 worker / importer 二进制。"""
    proc = subprocess.run(args, cwd=SERVER_ROOT, capture_output=True, text=True, timeout=300)
    ok = proc.returncode == 0
    check(label, ok, "" if ok else (proc.stderr or proc.stdout)[-200:])
    return proc


def solve(prompt):
    m = re.match(r"^\s*(\d+)\s*([+\-×÷])\s*(\d+)\s*=\s*\?", prompt)
    if m:
        a, op, b = int(m.group(1)), m.group(2), int(m.group(3))
        return str(a + b if op == "+" else a - b if op == "-" else a * b if op == "×" else a // b)
    m = re.match(r"^\s*(\d+)\s*\+\s*(\d+)\s*×\s*(\d+)\s*=\s*\?", prompt)
    if m:
        return str(int(m.group(1)) + int(m.group(2)) * int(m.group(3)))
    m = re.match(r"^\s*(\d+)\s*-\s*(\d+)\s*\+\s*(\d+)\s*=\s*\?", prompt)
    if m:
        return str(int(m.group(1)) - int(m.group(2)) + int(m.group(3)))
    m = re.match(r"^\s*(\d+)\s*○\s*(\d+)", prompt)
    if m:
        a, b = int(m.group(1)), int(m.group(2))
        return ">" if a > b else "<" if a < b else "="
    return None


# ---------------------------------------------------------------- 准备账号

ts = int(time.time())
status, payload, _ = call("POST", "/auth/register", {
    "email": f"m4_{ts}@example.com", "password": env("SMOKE_PASSWORD"),
    "display_name": "M4冒烟", "invite_code": env("BOOTSTRAP_INVITE_CODE"),
}, expect=201, label="注册家长")
token = data(payload).get("access_token")
assert token, "注册没拿到 token"

status, payload, _ = call("POST", "/children", {
    "nickname": "哥哥", "avatar_id": "panda", "birth_ym": "202001", "stage_code": "S1",
}, token=token, expect=201, label="创建孩子 A")
child_a = data(payload).get("id")

status, payload, _ = call("POST", "/children", {
    "nickname": "妹妹", "avatar_id": "rabbit", "birth_ym": "202203", "stage_code": "S0",
}, token=token, expect=201, label="创建孩子 B")
child_b = data(payload).get("id")

# 第二个家长 + 他的孩子，用于验证越权
status, payload, _ = call("POST", "/auth/register", {
    "email": f"m4o_{ts}@example.com", "password": env("SMOKE_PASSWORD"),
    "display_name": "路人", "invite_code": env("BOOTSTRAP_INVITE_CODE"),
}, expect=201, label="注册第二个家长")
other_token = data(payload).get("access_token")
status, payload, _ = call("POST", "/children", {
    "nickname": "别人家娃", "avatar_id": "panda", "birth_ym": "202101", "stage_code": "S1",
}, token=other_token, expect=201, label="创建别人的孩子")
other_child = data(payload).get("id")

# 基准线是 importer 铺的（不是建档案时自动铺），这里显式跑一次
run_bin([os.path.join(TMP_DIR, "importer.exe"), "-only", "plans"], "铺标准节奏基准线")

print("\n--- 家长设置 ---")
status, payload, _ = call("GET", "/parent/settings", token=token, expect=200, label="读设置")
st = data(payload)
check("默认 require_parent_confirm=false", st.get("require_parent_confirm") is False, str(st))
check("默认 compare_children=true", st.get("compare_children") is True, str(st))
check("默认每日 60 分钟", st.get("daily_limit_min") == 60, str(st.get("daily_limit_min")))
check("默认 pace_mode=standard", st.get("pace_mode") == "standard", str(st.get("pace_mode")))

status, payload, _ = call("PUT", "/parent/settings", {"require_parent_confirm": True},
                          token=token, expect=200, label="开启家长确认")
st = data(payload)
check("开关已生效", st.get("require_parent_confirm") is True, str(st))
check("部分更新不动其它字段", st.get("daily_limit_min") == 60 and st.get("compare_children") is True, str(st))

call("PUT", "/parent/settings", {"daily_limit_min": 9999}, token=token, expect=422, label="时长超范围 422")
call("PUT", "/parent/settings", {"pace_mode": "crazy"}, token=token, expect=422, label="非法 pace_mode 422")
call("PUT", "/parent/settings", {"session_limit_min": 90, "daily_limit_min": 60},
     token=token, expect=422, label="单次大于每日 422")
call("PUT", "/parent/settings", {"daily_limit_min": 0}, token=token, expect=200, label="0 表示不限")
status, payload, _ = call("GET", "/parent/settings", token=token, expect=200, label="0 被正确保留")
check("零值不等于未提供", data(payload).get("daily_limit_min") == 0, str(data(payload)))
call("PUT", "/parent/settings", {"daily_limit_min": 60}, token=token, expect=200, label="恢复 60 分钟")

print("\n--- 报表基础（尚无学习数据）---")
status, payload, _ = call("GET", f"/reports/{child_a}/overview", token=token, expect=200, label="总览")
ov = data(payload)
check("总览掌握量为 0", ov.get("mastered_total") == 0, str(ov.get("mastered_total")))
check("总览含三个学科", len(ov.get("subjects") or []) == 3, str(len(ov.get("subjects") or [])))
check("总览徽章总数 16", ov.get("badges_total") == 16, str(ov.get("badges_total")))
check("总览带生成时间", bool(ov.get("generated_at")), str(ov.get("generated_at")))

status, payload, _ = call("GET", f"/reports/{child_a}/trend?days=7", token=token, expect=200, label="趋势")
tr = data(payload)
check("趋势窗口 7 天", tr.get("days") == 7, str(tr.get("days")))
check("趋势逐日补零无断点", len(tr.get("points") or []) == 7, str(len(tr.get("points") or [])))
check("趋势起点为空说明", isinstance(tr.get("note"), str) and tr.get("note"), str(tr.get("note"))[:40])

status, payload, _ = call("GET", f"/reports/{child_a}/subject/chinese", token=token, expect=200, label="学科明细")
sub = data(payload)
check("学科明细带阶段", len(sub.get("stages") or []) > 0, str(len(sub.get("stages") or [])))
check("学科名是中文", sub.get("subject_name") == "语文", str(sub.get("subject_name")))
call("GET", f"/reports/{child_a}/subject/music", token=token, expect=422, label="非法学科 422")

status, payload, _ = call("GET", f"/reports/{child_a}/pace?days=30", token=token, expect=200, label="节奏")
pc = data(payload)
check("节奏带中性文案", isinstance(pc.get("note"), str) and "不等于" in (pc.get("note") or ""), (pc.get("note") or "")[:30])

status, payload, _ = call("GET", f"/reports/{child_a}/growth", token=token, expect=200, label="成长树")
gr = data(payload)
check("成长树三学科", len(gr.get("subjects") or []) == 3, str(len(gr.get("subjects") or [])))

call("GET", f"/reports/{child_a}/suggestions", token=token, expect=200, label="建议列表")

print("\n--- 学一次数学会话（答案可从题面算出）---")
status, payload, _ = call("POST", "/practice/session",
                          {"child_id": child_a, "subject": "math", "device_type": "web"},
                          token=token, expect=201, label="开数学会话")
sess = data(payload)
items = sess.get("items") or []
check("数学会话有题目", len(items) > 0, str(len(items)))
check("会话不下发答案", all("answer_key" not in it for it in items), "出现 answer_key 字段")

answered = 0
for it in items:
    if it.get("question_type") != "math_param":
        continue
    ans = solve((it.get("question") or {}).get("prompt") or "")
    if ans is None:
        continue
    status, payload, _ = call("POST", f"/practice/session/{sess['id']}/answer",
                              {"child_id": child_a, "item_id": it["id"], "answer": ans, "elapsed_ms": 1500},
                              token=token, expect=200,
                              label=f"答对 {(it.get('question') or {}).get('prompt')}")
    answered += 1
check("至少答对一道数学题", answered > 0, str(answered))

status, payload, _ = call("POST", f"/practice/session/{sess['id']}/finish",
                          {"child_id": child_a}, token=token, expect=200, label="结算会话")
summary = data(payload)
check("结算带正确率", summary.get("accuracy") == 1.0, str(summary.get("accuracy")))

print("\n--- 日汇总 worker ---")
status, payload, _ = call("GET", f"/reports/{child_a}/trend?days=1", token=token, expect=200, label="结算后趋势")
today_before = (data(payload).get("points") or [{}])[0]
check("会话增量已写入当日汇总", (today_before.get("question_count") or 0) > 0, str(today_before.get("question_count")))

run_bin([os.path.join(TMP_DIR, "worker.exe"), "-child", child_a, "-days", "6"], "跑日汇总（最近 6 天）")

status, payload, _ = call("GET", f"/reports/{child_a}/pace?days=30&subject=math", token=token, expect=200, label="数学节奏")
series = data(payload).get("series") or []
check("节奏序列非空", len(series) > 0, str(len(series)))
plan_by_date = {p["date"]: p for p in series}
today_row = series[-1] if series else {}
check("今日计划量=2（数学每日标准）", today_row.get("planned_new") == 2, str(today_row.get("planned_new")))
check("今日累计计划=2", today_row.get("cum_planned") == 2, str(today_row.get("cum_planned")))
check("今日偏差为 0（进度落在计划首日）", abs(today_row.get("deviation_days") or 0) < 0.01, str(today_row.get("deviation_days")))

first_row = series[0]
check("历史日偏差为负（当日早于计划首日 → 超前）", (first_row.get("deviation_days") or 0) < 0,
      f"{first_row.get('date')} dev={first_row.get('deviation_days')}")
check("偏差随日期推进单调回升", (today_row.get("deviation_days") or 0) > (first_row.get("deviation_days") or 0),
      f"{first_row.get('deviation_days')} -> {today_row.get('deviation_days')}")

status, payload, _ = call("GET", f"/reports/{child_a}/trend?days=6", token=token, expect=200, label="合计趋势")
pts = {p["date"]: p for p in (data(payload).get("points") or [])}
check("合计行累计计划=13（语文6+数学2+英语5）",
      max((p.get("cum_planned") or 0) for p in pts.values()) == 13,
      str(max((p.get("cum_planned") or 0) for p in pts.values())))

print("\n--- require_parent_confirm 生效 ---")
# 此时设置里 require_parent_confirm=true（前面开过又没关，只在最后恢复），
# 客观题正确率 100% 但没有家长确认 → 当日不点亮。
status, payload, _ = call("GET", f"/reports/{child_a}/trend?days=1", token=token, expect=200, label="开启确认时趋势")
p1 = (data(payload).get("points") or [{}])[0]
check("开启家长确认后当日未点亮", p1.get("passed") is False, str(p1.get("passed")))
check("当日未被家长确认", p1.get("parent_confirmed") is False, str(p1.get("parent_confirmed")))

call("PUT", "/parent/settings", {"require_parent_confirm": False}, token=token, expect=200, label="关闭家长确认")
run_bin([os.path.join(TMP_DIR, "worker.exe"), "-child", child_a, "-days", "2"], "重跑日汇总")
status, payload, _ = call("GET", f"/reports/{child_a}/trend?days=1", token=token, expect=200, label="关闭确认时趋势")
p2 = (data(payload).get("points") or [{}])[0]
check("关闭家长确认后当日点亮", p2.get("passed") is True, str(p2.get("passed")))

print("\n--- 成就 ---")
status, payload, _ = call("GET", f"/reports/{child_a}/badges", token=token, expect=200, label="徽章列表")
bg = data(payload)
check("徽章目录 16 枚", bg.get("total") == 16, str(bg.get("total")))
earned_codes = {i["code"] for i in (bg.get("items") or []) if i.get("earned")}
check("已授予「第一次练习」", "first_session" in earned_codes, str(earned_codes))
check("已授予「满分小达人」", "perfect_1" in earned_codes, str(earned_codes))
cand = [i for i in (bg.get("items") or []) if i.get("code") == "first_session"][0]
check("徽章带获得时间", bool(cand.get("earned_at")), str(cand.get("earned_at")))
check("徽章带事实快照", bool(cand.get("progress")), str(cand.get("progress")))

status, payload, _ = call("GET", f"/reports/{child_a}/badges", token=token, expect=200, label="再取一次（幂等）")
check("重复评测不重复发", data(payload).get("earned") == bg.get("earned"),
      f"{bg.get('earned')} -> {data(payload).get('earned')}")

print("\n--- 建议规则（3 天低正确率）---")
status, payload, _ = call("GET", f"/practice/today?child_id={child_a}&subject=chinese",
                          token=token, expect=200, label="取语文任务")
cn_items = data(payload).get("items") or []
kp_id = cn_items[0]["kp_id"] if cn_items else None
check("取到一个语文知识点", bool(kp_id), str(kp_id))

fixture_ok = os.path.exists(PSQL) and kp_id
if fixture_ok:
    sql = (
        "INSERT INTO answer_logs (child_id, kp_id, subject_code, question_type, is_correct, elapsed_ms, created_at) "
        "SELECT '%s'::uuid, '%s'::uuid, 'chinese', 'choice_text', false, 900, "
        "now() - (d || ' days')::interval FROM generate_series(1,3) AS d, generate_series(1,2) AS n;"
    ) % (child_a, kp_id)
    proc = subprocess.run([PSQL, env("DATABASE_URL"), "-q", "-c", sql],
                          capture_output=True, text=True, timeout=60)
    check("插入 3 天低正确率 fixture", proc.returncode == 0, (proc.stderr or "")[-160:])

    status, payload, _ = call("GET", f"/reports/{child_a}/suggestions", token=token, expect=200, label="建议列表")
    codes = {s["code"] for s in (data(payload).get("suggestions") or [])}
    check("触发「连续 3 天低正确率」建议", "low_accuracy_3days" in codes, str(codes))
    low = [s for s in (data(payload).get("suggestions") or []) if s["code"] == "low_accuracy_3days"]
    if low:
        check("建议带可执行动作", len(low[0].get("actions") or []) > 0, str(low[0].get("actions")))
        blob = json.dumps(low[0], ensure_ascii=False)
        check("建议文案不含负向标签", "落后" not in blob and "跟不上" not in blob, blob[:60])
else:
    check("插入 3 天低正确率 fixture", False, "psql 不存在或没有可用知识点，跳过")

print("\n--- 多孩对比 ---")
status, payload, _ = call("GET", f"/reports/compare?childIds={child_a},{child_b}&align=session",
                          token=token, expect=200, label="按学习日对比")
cmp = data(payload)
check("对比返回两个孩子", len(cmp.get("children") or []) == 2, str(len(cmp.get("children") or [])))
check("默认按学习日对齐", cmp.get("align") == "session", str(cmp.get("align")))
blob = json.dumps(cmp, ensure_ascii=False)
check("响应不含排名字段", "rank" not in blob and "score" not in blob, blob[:120])
check("文案声明不排名", "不做排名" in (cmp.get("note") or ""), (cmp.get("note") or "")[:40])
ka = [c for c in (cmp.get("children") or []) if c["child_id"] == child_a][0]
kb = [c for c in (cmp.get("children") or []) if c["child_id"] == child_b][0]
check("孩子 A 曲线非空", len(ka.get("points") or []) > 0, str(len(ka.get("points") or [])))
check("孩子 B 曲线为空", len(kb.get("points") or []) == 0, str(len(kb.get("points") or [])))
check("指标含掌握量与偏差", "mastered_total" in ka.get("metrics", {}) and "deviation_days" in ka.get("metrics", {}),
      str(sorted(ka.get("metrics", {}).keys())))
check("学习日序号从 1 开始", (ka.get("points") or [{}])[0].get("index") == 1,
      str((ka.get("points") or [{}])[0].get("index")))

call("GET", f"/reports/compare?childIds={child_a},{child_b}&align=calendar",
     token=token, expect=200, label="按自然日对比")
call("GET", f"/reports/compare?childIds={child_a}", token=token, expect=200, label="单孩对比（退化）")
call("GET", f"/reports/compare?childIds=", token=token, expect=422, label="空 childIds 422")
call("GET", f"/reports/compare?childIds={child_a},{child_b},00000000-0000-0000-0000-000000000000,11111111-1111-1111-1111-111111111111,22222222-2222-2222-2222-222222222222",
     token=token, expect=422, label="超过 4 个孩子 422")
call("GET", f"/reports/compare?childIds={child_a},{other_child}", token=token, expect=404,
     label="混入别人的孩子 404")

print("\n--- 导出 ---")
status, payload, meta = call("GET", f"/reports/{child_a}/export?format=csv&days=7",
                             token=token, expect=200, label="导出 CSV", raw=True)
ctype = (meta["headers"].get("Content-Type") or "")
blob = meta["blob"] or b""
check("Content-Type 是 CSV", "text/csv" in ctype, ctype)
check("带 UTF-8 BOM", blob.startswith(b"\xef\xbb\xbf"), str(blob[:8]))
text = blob.decode("utf-8-sig", "replace")
check("表头含中文列名", "日期" in text and "偏差(天)" in text, text[:80])
check("行数与窗口一致（表头+7 行）", len([l for l in text.strip().splitlines()]) == 8,
      str(len(text.strip().splitlines())))
call("GET", f"/reports/{child_a}/export?format=pdf", token=token, expect=422, label="PDF 未支持 422")

print("\n--- 越权 ---")
call("GET", f"/reports/{child_a}/overview", expect=401, label="未登录 401")
call("GET", f"/reports/{other_child}/overview", token=token, expect=404, label="别人的孩子 404")
call("GET", f"/reports/{other_child}/pace", token=token, expect=404, label="别人的孩子节奏 404")
call("GET", f"/reports/{other_child}/badges", token=token, expect=404, label="别人的孩子徽章 404")
call("GET", "/reports/nope/overview", token=token, expect=404, label="非法 childId 404")

print(f"\n===== M4 冒烟：{len(PASS)} 项通过，{len(FAIL)} 项失败 =====")
if FAIL:
    for f in FAIL:
        print("  FAILED: " + f)
    sys.exit(1)
