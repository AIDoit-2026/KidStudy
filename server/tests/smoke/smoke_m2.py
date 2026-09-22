"""M2 内容底座端到端冒烟（server/tests/smoke，已入库）。

覆盖：鉴权 → 内容检索（阶段/汉字/组词/英语词/故事）→ 审核队列 → 批量通过 →
可检索性变化 → 双语配对 → 参数校验。

脚本对库内既有状态不敏感（先取基线再做相对断言），因此可以反复重跑。

用法：先起服务（cd server && go run ./cmd/api），再
      python server/tests/smoke/smoke_m2.py   —— 任意 CWD 都可以
"""
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from _common import env

BASE = "http://127.0.0.1:18080"

# 不走系统代理：本机有 http_proxy，用它会 502
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

PASS, FAIL = [], []


def call(method, path, body=None, token=None, expect=None):
    # 路径里可能含中文（汉字、分类名），http.client 只接受 ASCII
    if any(ord(ch) > 127 for ch in path):
        base, _, qs = path.partition("?")
        path = urllib.parse.quote(base) + ("?" + urllib.parse.quote(qs, safe="=&") if qs else "")
    req = urllib.request.Request(BASE + path, data=json.dumps(body).encode() if body is not None else None, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with opener.open(req, timeout=30) as resp:
            status, payload = resp.status, json.loads(resp.read().decode() or "{}")
    except urllib.error.HTTPError as e:
        status = e.code
        raw = e.read().decode()
        try:
            payload = json.loads(raw or "{}")
        except json.JSONDecodeError:
            payload = {"raw": raw[:200]}
    if expect is not None and status != expect:
        FAIL.append(f"{method} {path} 期望 {expect} 实际 {status}: {str(payload)[:200]}")
    return status, payload


def check(name, cond, extra=""):
    (PASS if cond else FAIL).append(f"{name}{(' — ' + extra) if extra else ''}")


def total_of(payload):
    return ((payload.get("meta") or {}).get("page") or {}).get("total")


# ---------- 1. 注册/登录取令牌 ----------
email = f"m2smoke{int(time.time())}@example.com"
status, reg = call("POST", "/api/v1/auth/register",
                   {"email": email, "password": env("SMOKE_PASSWORD"), "display_name": "M2冒烟",
                    "invite_code": env("BOOTSTRAP_INVITE_CODE")}, expect=201)
token = (reg.get("data") or {}).get("access_token", "")
check("注册并拿到访问令牌", bool(token), f"status={status}")

# ---------- 2. 基线（既有状态） ----------
_, pubs0 = call("GET", "/api/v1/content/stories?limit=1", token=token, expect=200)
published_base = total_of(pubs0) or 0
_, pend0 = call("GET", "/api/v1/content/stories?status=pending&limit=1", token=token, expect=200)
pending_base = total_of(pend0) or 0
_, q0 = call("GET", "/api/v1/review/queue?limit=3", token=token, expect=200)
queue_base = total_of(q0) or 0
print(f"基线：已发布 {published_base} / 待审 {pending_base} / 队列 {queue_base}")

# ---------- 3. 鉴权边界 ----------
st, _ = call("GET", "/api/v1/content/stages", expect=401)
check("未登录访问内容接口 → 401", st == 401)

# ---------- 4. 阶段字典 ----------
_, stages = call("GET", "/api/v1/content/stages", token=token, expect=200)
data = stages.get("data") or []
codes = {s["code"] for s in data}
check("阶段字典 39 条（24 汉 + 10 英 + 5 数）", len(data) == 39, f"实际 {len(data)}")
check("阶段编码覆盖 S0/G12/X6/E1/E10/M5", {"S0", "G12", "X6", "E1", "E10", "M5"} <= codes)

_, cn = call("GET", "/api/v1/content/stages?subject=chinese", token=token, expect=200)
check("按学科过滤阶段", len(cn.get("data") or []) == 24, f"实际 {len(cn.get('data') or [])}")

# ---------- 5. 汉字列表与详情 ----------
_, hanzi = call("GET", "/api/v1/content/hanzi?stage=S0&include_words=true&limit=5", token=token, expect=200)
d = hanzi.get("data") or []
check("汉字列表带分页元信息（S0=155 字）", total_of(hanzi) == 155, f"total={total_of(hanzi)}")
check("汉字列表返回组词", bool(d) and bool(d[0].get("words")),
      json.dumps(d[0], ensure_ascii=False)[:110] if d else "空")

_, one = call("GET", "/api/v1/content/hanzi/" + urllib.parse.quote("一"), token=token, expect=200)
detail = one.get("data") or {}
check("按字符取汉字详情", detail.get("char") == "一")
check("详情含笔顺字形数据", bool(detail.get("stroke_paths")) and detail.get("has_anim") is True)
check("详情含拼音与笔画", bool(detail.get("pinyin")) and detail.get("stroke_count") == 1,
      f"pinyin={detail.get('pinyin')} stroke={detail.get('stroke_count')}")
check("详情含释义或组词", bool(detail.get("explanation")) or bool(detail.get("words")))

st, _ = call("GET", "/api/v1/content/hanzi/" + urllib.parse.quote("𠀀"), token=token)
check("不存在的字 → 404", st == 404, f"实际 {st}")

# ---------- 6. 英语词 ----------
_, words = call("GET", "/api/v1/content/words?level=E7&limit=3", token=token, expect=200)
wd = words.get("data") or []
check("按级别检索英语词", (total_of(words) or 0) > 0, f"total={total_of(words)}")
check("英语词带释义与级别", bool(wd) and bool(wd[0].get("meaning_zh")) and wd[0].get("level_code") == "E7",
      json.dumps(wd[0], ensure_ascii=False)[:110] if wd else "空")

_, bad = call("GET", "/api/v1/content/words?level=E99", token=token, expect=200)
check("不存在的级别返回空列表而非报错", (bad.get("data") or []) == [])

# ---------- 7. 故事可见性 ----------
_, pubs = call("GET", "/api/v1/content/stories?limit=1", token=token, expect=200)
check("默认只返回已发布故事", total_of(pubs) == published_base, f"total={total_of(pubs)} base={published_base}")

_, unsuitable = call("GET", "/api/v1/content/stories?status=pending&suitable_only=true&limit=1", token=token, expect=200)
check("不宜内容可被过滤掉", (total_of(unsuitable) or 0) < pending_base,
      f"suitable_only={total_of(unsuitable)} < 全部待审={pending_base}")

# ---------- 8. 审核队列 ----------
_, queue = call("GET", "/api/v1/review/queue?limit=3", token=token, expect=200)
items = queue.get("data") or []
check("审核队列可取待审条目", len(items) == 3 and total_of(queue) == queue_base,
      f"total={total_of(queue)} len={len(items)}")
check("队列条目恒返回 hits 字段（空数组表示未命中）", all("hits" in it for it in items))
check("队列条目带来源与目标表", bool(items) and bool(items[0].get("source")) and items[0].get("ref_table") == "stories")

review_ids = [it["id"] for it in items]
story_ids = [it["ref_id"] for it in items]
_, res = call("POST", "/api/v1/review/batch", {"ids": review_ids, "action": "approve"}, token=token, expect=200)
check("批量通过 3 条", (res.get("data") or {}).get("applied") == 3, json.dumps(res.get("data"), ensure_ascii=False))

st, _ = call("POST", f"/api/v1/review/{review_ids[0]}/approve", token=token)
check("重复审核同一条 → 404（幂等）", st == 404, f"实际 {st}")

_, pubs2 = call("GET", "/api/v1/content/stories?limit=5", token=token, expect=200)
p2 = pubs2.get("data") or []
check("通过后即可检索到（+3）", total_of(pubs2) == published_base + 3, f"total={total_of(pubs2)}")
check("列表不返回正文（省流量）", bool(p2) and "body_md" not in p2[0])

_, q2 = call("GET", "/api/v1/review/queue?limit=1", token=token, expect=200)
check("队列待审数 -3", total_of(q2) == queue_base - 3, f"total={total_of(q2)}")

_, q3 = call("GET", "/api/v1/review/queue?status=approved&limit=1", token=token, expect=200)
check("可按 approved 查历史审核", (total_of(q3) or 0) >= 3, f"total={total_of(q3)}")

# 用 ref_id（内容 ID）取详情：队列给的是队列 ID，不是内容 ID
_, detail_story = call("GET", f"/api/v1/content/stories/{story_ids[0]}", token=token, expect=200)
sd = detail_story.get("data") or {}
check("详情返回正文", bool(sd.get("body_md")), f"正文前 40 字：{(sd.get('body_md') or '')[:40]}")
check("详情带 status/suitable/level", sd.get("status") == "published" and "suitable" in sd and "level_code" in sd)

# ---------- 9. 双语配对 ----------
pair_ok, tried = False, 0
_, cand = call("GET", "/api/v1/content/stories?status=all&lang=zh&category=" + urllib.parse.quote("动物主角") + "&limit=5",
               token=token, expect=200)
for item in cand.get("data") or []:
    tried += 1
    st, pair = call("GET", f"/api/v1/content/stories/{item['id']}/pair", token=token)
    if st == 200:
        pd = pair.get("data") or {}
        pair_ok = bool((pd.get("zh") or {}).get("body_md")) and bool((pd.get("en") or {}).get("body_md"))
        if pair_ok:
            check("配对返回中英两份正文", (pd.get("en") or {}).get("lang") == "en",
                  f"slug={pd.get('slug')} en={(pd.get('en') or {}).get('title')}")
            break
check("存在双语配对的中文篇可取到中英对照", pair_ok, f"尝试 {tried} 篇")

st, _ = call("GET", "/api/v1/content/stories/00000000-0000-0000-0000-000000000000/pair", token=token)
check("无配对/不存在 → 404", st == 404, f"实际 {st}")

# ---------- 10. 参数校验 ----------
st, err = call("GET", "/api/v1/content/hanzi?limit=999", token=token, expect=422)
check("limit 超限 → 422", st == 422 and (err.get("error") or {}).get("code") == "VALIDATION_FAILED")
st, _ = call("GET", "/api/v1/content/stories?lang=fr", token=token, expect=422)
check("非法语言参数 → 422", st == 422)
st, _ = call("GET", "/api/v1/review/queue?status=bogus", token=token, expect=422)
check("非法审核状态 → 422", st == 422)
st, _ = call("POST", "/api/v1/review/batch", {"ids": [], "action": "approve"}, token=token, expect=422)
check("空 ids 批量 → 422", st == 422)
st, _ = call("POST", "/api/v1/review/batch", {"ids": ["not-a-uuid"], "action": "approve"}, token=token, expect=422)
check("非法 UUID 批量 → 422", st == 422)

# ---------- 汇总 ----------
print("\n=========== M2 冒烟结果 ===========")
for p in PASS:
    print("  ✅", p)
for f in FAIL:
    print("  ❌", f)
print(f"\n通过 {len(PASS)} 项，失败 {len(FAIL)} 项")
sys.exit(1 if FAIL else 0)
