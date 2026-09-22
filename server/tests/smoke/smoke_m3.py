# M3 练习引擎端到端冒烟（server/tests/smoke，已入库，可反复重跑）。
#
# 用法：先起服务（cd server && go run ./cmd/api），再
#       python server/tests/smoke/smoke_m3.py   —— 任意 CWD 都可以
#
# 关键手法：数学题的答案可以直接从题面算出来，因此「答对 / 答错」两条路径
# 都能确定性地验证，不用靠猜选项碰运气。
import json
import re
import time
import urllib.parse
import urllib.request

from _common import env

BASE = "http://127.0.0.1:18080/api/v1"
PASS, FAIL = [], []


OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def call(method, path, body=None, token=None, expect=None, label=""):
    if any(ord(ch) > 127 for ch in path):
        base, _, qs = path.partition("?")
        path = urllib.parse.quote(base) + ("?" + urllib.parse.quote(qs, safe="=&") if qs else "")
    req = urllib.request.Request(BASE + path, method=method)
    if token:
        req.add_header("Authorization", "Bearer " + token)
    data = None
    if body is not None:
        data = json.dumps(body, ensure_ascii=False).encode()
        req.add_header("Content-Type", "application/json")
    try:
        resp = OPENER.open(req, data=data, timeout=30)
        status, payload = resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        status, payload = e.code, json.loads(e.read() or b"{}")
    except Exception as e:  # 连接层问题直接算失败
        check(label or f"{method} {path}", False, f"请求异常 {e}")
        return status if "status" in dir() else 0, {}

    ok = expect is None or status == expect
    detail = "" if ok else f"期望 {expect} 实得 {status}: {str(payload)[:160]}"
    check(label or f"{method} {path}", ok, detail)
    return status, payload


def check(name, ok, detail=""):
    (PASS if ok else FAIL).append(name)
    print(("  PASS  " if ok else "  FAIL  ") + name + (("  <- " + detail) if detail else ""))


def data(payload):
    return payload.get("data") or {}


# ---------------------------------------------------------------- 数学题求解

def solve(prompt):
    """从题面算出答案，算不出来返回 None（应用题交给调用方兜底）。"""
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
email = f"m3_{ts}@example.com"
status, payload = call("POST", "/auth/register", {
    "email": email, "password": env("SMOKE_PASSWORD"),
    "display_name": "M3冒烟", "invite_code": env("BOOTSTRAP_INVITE_CODE"),
}, expect=201, label="注册家长")
token = data(payload).get("access_token")
assert token, "注册没拿到 token"

status, payload = call("POST", "/children", {
    "nickname": "冒烟娃", "avatar_id": "panda", "birth_ym": "202001", "stage_code": "S1",
}, token=token, expect=201, label="创建孩子档案")
child_id = data(payload).get("id")
assert child_id, "没拿到 child_id"

# 另一个家长，用来验证越权
status, payload = call("POST", "/auth/register", {
    "email": f"m3o_{ts}@example.com", "password": env("SMOKE_PASSWORD"),
    "display_name": "路人", "invite_code": env("BOOTSTRAP_INVITE_CODE"),
}, expect=201, label="注册第二个家长")
other_token = data(payload).get("access_token")

print("\n--- 鉴权与归属 ---")
call("GET", f"/practice/today?child_id={child_id}", expect=401, label="未登录访问今日任务 401")
call("GET", f"/practice/today?child_id={child_id}", token=other_token, expect=404, label="别人的孩子 404")
call("GET", "/practice/today?child_id=not-a-uuid", token=token, expect=404, label="非法 child_id 404")

print("\n--- 数学参数化：seed 可复现 ---")
_, p1 = call("GET", "/practice/math/templates", token=token, expect=200, label="数学模板列表")
check("模板数量 14", len(data(p1)) == 14, f"实得 {len(data(p1))}")
_, a1 = call("GET", "/practice/math/preview?template=M3_ADD20&count=20&seed=42",
             token=token, expect=200, label="预览 seed=42")
_, a2 = call("GET", "/practice/math/preview?template=M3_ADD20&count=20&seed=42",
             token=token, expect=200, label="同 seed 再预览一次")
check("同 seed 两次题目完全一致", data(a1) == data(a2))
_, a3 = call("GET", "/practice/math/preview?template=M3_ADD20&count=20&seed=43",
             token=token, expect=200, label="换 seed 预览")
check("换 seed 题目不同", data(a1) != data(a3))
call("GET", "/practice/math/preview?template=NOPE&count=5&seed=1", token=token, expect=404, label="模板不存在 404")
_, a4 = call("GET", "/practice/math/preview?template=M3_ADD20&seed=7",
             token=token, expect=200, label="不传 count 用默认")
check("默认出 10 题", len(data(a4)) == 10, f"实得 {len(data(a4))}")

print("\n--- 今日编排 ---")
_, p = call("GET", f"/practice/today?child_id={child_id}", token=token, expect=200, label="今日任务")
plan = data(p)
check("返回 remaining_minutes", plan.get("remaining_minutes", -1) >= 0, str(plan.get("remaining_minutes")))
check("今日任务非空", len(plan.get("items") or []) > 0, f"{len(plan.get('items') or [])} 项")
check("任务项带来源 reason", all(i.get("reason") for i in plan["items"]))
_, pm = call("GET", f"/practice/today?child_id={child_id}&subject=math",
             token=token, expect=200, label="只要数学")
check("数学过滤生效",
      len(data(pm)["items"]) > 0 and all(i["subject_code"] == "math" for i in data(pm)["items"]),
      str(len(data(pm)["items"])) + " 项")

print("\n--- 会话：数学（答对 / 答错两条路径）---")
_, s = call("POST", "/practice/session", {"child_id": child_id, "device_type": "desktop", "subject": "math"},
            token=token, expect=201, label="建数学会话")
sess = data(s)
sid, items = sess["id"], sess["items"]
check("会话有题目", len(items) > 0, f"{len(items)} 题")

_, g = call("GET", f"/practice/session/{sid}?child_id={child_id}", token=token, expect=200,
            label="取会话（断点续练）")
check("未作答题目不带答案", all(i.get("correct_answer") is None for i in data(g)["items"]))

# 第 1 题答对
first = items[0]
ans = solve(first["question"].get("prompt", ""))
if ans is None:
    check("首题可解析", False, first["question"].get("prompt", ""))
else:
    _, r = call("POST", f"/practice/session/{sid}/answer",
                {"child_id": child_id, "item_id": first["id"], "answer": ans, "elapsed_ms": 1200},
                token=token, expect=200, label=f"答对首题（{ans}）")
    res = data(r)
    check("判分正确", res.get("is_correct") is True, str(res))
    check("带回掌握度变化", isinstance(res.get("mastery"), dict))
    call("POST", f"/practice/session/{sid}/answer",
         {"child_id": child_id, "item_id": first["id"], "answer": ans}, token=token, expect=409, label="重复作答 409")

# 第 2 题故意答错
if len(items) > 1:
    second = items[1]
    wrong = "99999"
    _, r2 = call("POST", f"/practice/session/{sid}/answer",
                 {"child_id": child_id, "item_id": second["id"], "answer": wrong, "elapsed_ms": 900},
                 token=token, expect=200, label="故意答错第 2 题")
    res2 = data(r2)
    check("判分错误", res2.get("is_correct") is False, str(res2))
    check("答错后 10 分钟复现", (res2.get("mastery") or {}).get("next_review_in_minutes") == 10,
          str(res2.get("mastery")))
    check("答错进错题本", (res2.get("mastery") or {}).get("entered_wrong_book") is True)

_, wb = call("GET", f"/mastery/wrong-book?child_id={child_id}", token=token, expect=200, label="错题本")
wb_total = wb.get("meta", {}).get("page", {}).get("total", 0)
check("错题本非空", wb_total > 0, str(wb_total))
call("GET", f"/mastery/review-queue?child_id={child_id}", token=token, expect=200, label="复习队列")

_, fin = call("POST", f"/practice/session/{sid}/finish", {"child_id": child_id},
              token=token, expect=200, label="结算会话")
summary = data(fin)
check("结算给出正确率", "accuracy" in summary, str(summary))
check("客观题由程序判定完成", summary.get("completed_by") in ("auto", ""), str(summary.get("completed_by")))

before_correct = summary.get("correct_count")
_, conf = call("POST", f"/practice/session/{sid}/confirm",
               {"child_id": child_id, "parent_score": 5, "parent_note": "很认真"},
               token=token, expect=200, label="家长打分")
check("家长评分已记录", data(conf).get("status") == "finished")
call("POST", f"/practice/session/{sid}/confirm", {"child_id": child_id, "parent_score": 9},
     token=token, expect=422, label="评分越界 422")

_, after = call("GET", f"/practice/session/{sid}?child_id={child_id}", token=token, expect=200,
                label="结算后再取会话")
check("作答过的题目回传正确答案", any(i.get("correct_answer") for i in data(after)["items"]))
check("客观正确率未被家长评分污染",
      all(i.get("is_correct") is not None for i in data(after)["items"] if i["state"] == "answered"))

print("\n--- 会话：语文（九种题型）---")
VALID_TYPES = {"choice_text", "choice_image", "choice_audio", "fill_blank",
               "match", "order", "trace", "say", "math_param"}
_, sc = call("POST", "/practice/session", {"child_id": child_id, "subject": "chinese"},
             token=token, expect=201, label="建语文会话")
cn = data(sc)
check("语文会话有题目", len(cn["items"]) > 0, f"{len(cn['items'])} 题")
check("题型都在九种之内", all(i["question_type"] in VALID_TYPES for i in cn["items"]),
      str({i["question_type"] for i in cn["items"]}))
types_seen = set()
subjective = 0  # 描红/跟读这类不判分的题
for it in cn["items"]:
    types_seen.add(it["question_type"])
    _, ra = call("POST", f"/practice/session/{cn['id']}/answer",
                 {"child_id": child_id, "item_id": it["id"], "answer": "0", "elapsed_ms": 800},
                 token=token, expect=200, label=f"作答 {it['question_type']}")
    if it["question_type"] in ("trace", "say"):
        subjective += 1
        check(f"{it['question_type']} 不计正确率（is_correct 为空）",
              data(ra).get("is_correct") is None, str(data(ra).get("is_correct")))
check("本批出现过主观题型", subjective > 0, f"{subjective} 题")
_, cnf = call("POST", f"/practice/session/{cn['id']}/finish", {"child_id": child_id},
              token=token, expect=200, label="结算语文会话")

print("\n--- 会话：英语 ---")
_, se = call("POST", "/practice/session", {"child_id": child_id, "subject": "english"},
             token=token, expect=201, label="建英语会话")
check("英语会话有题目", len(data(se)["items"]) > 0, f"{len(data(se)['items'])} 题")
if data(se)["items"]:
    it0 = data(se)["items"][0]
    call("POST", f"/practice/session/{data(se)['id']}/skip", {"child_id": child_id, "item_id": it0["id"]},
         token=token, expect=200, label="跳过一题")
call("POST", f"/practice/session/{data(se)['id']}/finish", {"child_id": child_id},
     token=token, expect=200, label="结算英语会话")

print("\n--- 掌握度维护 ---")
_, wb2 = call("GET", f"/mastery/wrong-book?child_id={child_id}&all=true", token=token, expect=200,
              label="错题本（含已移出）")
entries = data(wb2) or []  # 分页响应：data 直接是条目数组，total 在 meta.page
if entries:
    eid = entries[0]["id"]
    call("DELETE", f"/mastery/wrong-book/{eid}?child_id={child_id}", token=token, expect=200,
         label="移除错题条目")
    kp = entries[0]["kp_id"]
    call("POST", f"/mastery/{kp}/reset?child_id={child_id}", token=token, expect=200, label="重置知识点")
call("POST", "/mastery/00000000-0000-0000-0000-000000000000/reset?child_id=" + child_id,
     token=token, expect=404, label="重置不存在的知识点 404")
call("GET", f"/mastery/wrong-book?child_id={child_id}&offset=-1", token=token, expect=422, label="分页参数非法 422")

print("\n--- 参数校验 ---")
call("POST", f"/practice/session/{sid}/answer", {"child_id": child_id, "item_id": "bad", "answer": "1"},
     token=token, expect=422, label="非法 item_id 422")
call("POST", "/practice/session", {}, token=token, expect=422, label="空请求体 422")
call("POST", "/practice/session", {"child_id": "00000000-0000-0000-0000-000000000000"},
     token=token, expect=404, label="不存在的孩子 404")

print("\n================================")
print(f"通过 {len(PASS)} 项，失败 {len(FAIL)} 项")
if FAIL:
    print("失败项：")
    for f in FAIL:
        print("  - " + f)
    raise SystemExit(1)
print("M3 冒烟全部通过")
