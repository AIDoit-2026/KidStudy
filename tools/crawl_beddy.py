#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
SleepyStory / BeddyStories（sleepystory.net）双语故事采集器。

站点：Nuxt 3 SSR。robots.txt 只禁 /admin /api /private，允许 / 与 sitemap。
语言路由：/zh/ /en/（/fr/ /de/ 按用户要求不采集）
详情页  ：/{lang}/stories/{slug}
元信息条：<h1>标题</h1> 后紧跟 <div> 内含
          国家 /category/country/XX、类型 /category/type/xxx、
          年龄 /category/type/{0-2|3-5|6-8|9-12}、时长 <span>N m</span>
正文    ：<article class="prose ... story-content ...">…</article>
简介    ：故事简介</h2><p class="text-gray-700">…</p>

中英同名 slug 一一对应（sitemap 中 zh/en 各 3328 且交集为全集），
因此可用 slug 做双语配对 —— 这是本站点相对其它源最大的附加价值。

输出：var/beddy/{zh,en}/{年龄组}/{slug}.md
      var/beddy/_index.jsonl    全量索引（含中英配对）
      var/beddy/_pairs.jsonl    双语配对表（可直接用于对照阅读功能）
      var/beddy/_failed.jsonl   失败清单

用法：
  python tools/crawl_beddy.py                 # 全量（从 sitemap 取 URL + 抓正文）
  python tools/crawl_beddy.py --lang zh       # 只抓中文
  python tools/crawl_beddy.py --limit 50      # 冒烟
  python tools/crawl_beddy.py --retry-failed
  python tools/crawl_beddy.py --stats
  python tools/crawl_beddy.py --refresh-sitemap
"""

from __future__ import annotations

import argparse
import concurrent.futures as futures
import html
import json
import os
import random
import re
import sys
import threading
import time
import urllib.error
import urllib.request
from collections import Counter, defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT = os.path.join(ROOT, "var", "beddy")

BASE = "https://sleepystory.net"
SITEMAP = BASE + "/sitemap.xml"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/126.0 Safari/537.36")

LANGS = ("zh", "en")          # 用户明确不要 fr / de
AGE_GROUPS = ("0-2", "3-5", "6-8", "9-12")

_lock = threading.Lock()


def log(msg: str) -> None:
    with _lock:
        print(msg, flush=True)


STATS = {"http_err": Counter(), "net_err": 0, "ok": 0}
_DELAY = [0.0]


def set_delay(v: float) -> None:
    _DELAY[0] = v


def fetch(url: str, timeout: int = 18, retries: int = 2) -> str | None:
    """单请求。站点经 Cloudflare：正常响应 1–3 秒，但持续抓取一段时间后
    部分请求会被挂起（不返回也不拒绝）。超时设 40s 时整个线程池会被这类
    请求拖死（实测 0.09 篇/秒）；设成 18s 快速失败进入补采队列，
    整体吞吐反而更高。"""
    for attempt in range(retries):
        if _DELAY[0]:
            time.sleep(_DELAY[0] * (0.6 + random.random() * 0.8))
        try:
            req = urllib.request.Request(url, headers={
                "User-Agent": UA,
                "Accept": "text/html,*/*;q=0.8",
                "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
                "Referer": BASE + "/",
            })
            with urllib.request.urlopen(req, timeout=timeout) as r:
                raw = r.read()
                with _lock:
                    STATS["ok"] += 1
                try:
                    return raw.decode("utf-8")
                except UnicodeDecodeError:
                    return raw.decode("utf-8", errors="replace")
        except urllib.error.HTTPError as e:
            with _lock:
                STATS["http_err"][e.code] += 1
            if e.code in (429, 503):
                time.sleep(5 * (attempt + 1))
                continue
            return None
        except Exception:  # noqa: BLE001
            with _lock:
                STATS["net_err"] += 1
            time.sleep(1.0 * (attempt + 1))
    return None


# ---------------------------------------------------------------- URL 清单

def load_sitemap(force: bool = False) -> list[dict]:
    """从 sitemap 提取 zh/en 故事 URL。注意站点 sitemap 有未转义实体（&apos;）。"""
    path = os.path.join(OUT, "_urls.jsonl")
    if os.path.exists(path) and not force:
        recs = [json.loads(l) for l in open(path, encoding="utf-8") if l.strip()]
        log(f"[url  ] 复用已有清单 {len(recs)} 条（--refresh-sitemap 可强制重取）")
        return recs

    log(f"[url  ] 下载 {SITEMAP}")
    body = fetch(SITEMAP, timeout=60)
    if not body:
        log("[FATAL] sitemap 下载失败")
        sys.exit(1)
    locs = [html.unescape(x).strip()
            for x in re.findall(r"<loc>([^<]+)</loc>", body)]
    log(f"[url  ] sitemap 共 {len(locs)} 条 URL")

    recs, seen = [], set()
    for loc in locs:
        for lang in LANGS:
            m = re.match(rf"^{BASE}/{lang}/stories/([A-Za-z0-9'\-]+)$", loc)
            if m:
                slug = m.group(1)
                key = (lang, slug)
                if key in seen:
                    continue
                seen.add(key)
                recs.append({"lang": lang, "slug": slug,
                             "url": f"{BASE}/{lang}/stories/{slug}"})
                break

    with open(path, "w", encoding="utf-8") as f:
        for r in recs:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")
    by = defaultdict(int)
    for r in recs:
        by[r["lang"]] += 1
    log(f"[url  ] 故事 URL：{dict(by)}，合计 {len(recs)} 条")
    return recs


# ---------------------------------------------------------------- 解析

RE_H1 = re.compile(r'<h1[^>]*class="text-4xl[^"]*"[^>]*>(.*?)</h1>', re.S)
RE_META = re.compile(
    r'items-center gap-2 text-white/90[^>]*>(.*?)</div>', re.S)
RE_COUNTRY = re.compile(r'/category/country/([A-Za-z]{2})"[^>]*>([^<]+)<')
# 注意字符集必须含数字：年龄段 slug 形如 3-5，写成 [a-z\-]+ 会漏掉整个年龄字段
RE_TYPE = re.compile(r'/category/type/([a-z0-9\-]+)"[^>]*>([^<]+)<')
RE_MIN = re.compile(r"<span>(\d+)\s*m</span>")
RE_SUMMARY = re.compile(
    r'故事简介|Story Summary|Résumé', re.I)
RE_SUMMARY_P = re.compile(r'text-gray-700">([^<]+)</p>', re.S)
RE_ARTICLE = re.compile(
    r'<article[^>]*class="prose[^"]*story-content[^"]*"[^>]*>(.*?)</article>', re.S)
RE_IMG = re.compile(r'<img[^>]+src="([^"]+)"')
RE_SCRIPT = re.compile(r"<(script|style)[^>]*>.*?</\1>", re.S | re.I)
RE_COVER = re.compile(r'<img src="(https://api\.zsiga[^"]*-cover\.webp)"')


def clean_body(seg: str) -> tuple[str, list[str]]:
    """正文 HTML → (纯文本段落, 图片 URL)"""
    imgs = re.findall(r'src="([^"]+)"', seg)
    seg = RE_SCRIPT.sub("", seg)
    seg = re.sub(r"<img[^>]*>", "", seg)
    seg = re.sub(r"<br\s*/?>", "\n", seg, flags=re.I)
    seg = re.sub(r"</p\s*>", "\n", seg, flags=re.I)
    seg = re.sub(r"<[^>]+>", "", seg)
    seg = html.unescape(seg)
    paras = []
    for ln in seg.split("\n"):
        ln = re.sub(r"[\u3000\xa0]+", "", ln).strip()
        if ln:
            paras.append(ln)
    return "\n\n".join(paras), [u for u in imgs if u.startswith("http")]


def parse_page(h: str, lang: str) -> dict | None:
    m = RE_H1.search(h)
    title = html.unescape(m.group(1)).strip() if m else ""
    if not title:
        return None

    country = cname = ""
    age = ""
    stype = ""
    minutes = 0
    mm = RE_META.search(h)
    if mm:
        blk = mm.group(1)
        cm = RE_COUNTRY.search(blk)
        if cm:
            country, cname = cm.group(1), html.unescape(cm.group(2)).strip()
        for code, label in RE_TYPE.findall(blk):
            if code in AGE_GROUPS:
                age = code
            else:
                stype = html.unescape(label).strip()
        dm = RE_MIN.search(blk)
        if dm:
            minutes = int(dm.group(1))

    summary = ""
    sm = RE_SUMMARY.search(h)
    if sm:
        tail = h[sm.end():sm.end() + 400]
        sp = RE_SUMMARY_P.search(tail)
        if sp:
            summary = html.unescape(sp.group(1)).strip()

    am = RE_ARTICLE.search(h)
    body, imgs = clean_body(am.group(1)) if am else ("", [])

    cover = ""
    cov = RE_COVER.search(h)
    if cov:
        # 站点把单引号转义成 &#39;，不还原会得到失效的图片地址
        cover = html.unescape(cov.group(1))

    n_cjk = len(re.findall(r"[\u4e00-\u9fff]", body))
    n_words = len(re.findall(r"[A-Za-z][A-Za-z'\-]*", body))
    return {
        "title": title, "country": country, "country_name": cname,
        "age": age or "unknown", "type": stype, "minutes": minutes,
        "summary": summary, "body": body, "cover": cover,
        "images": imgs, "cjk": n_cjk, "words": n_words,
        "paras": len([x for x in body.split("\n\n") if x.strip()]),
    }


def slug_file(slug: str) -> str:
    """slug 里可能有单引号，文件名里去掉。"""
    s = re.sub(r"[\\/:*?\"<>|']+", "", slug)
    return s.strip() or "untitled"


def write_story(rec: dict, data: dict) -> tuple[str, str]:
    lang, slug = rec["lang"], rec["slug"]
    adir = os.path.join(OUT, lang, data["age"])
    os.makedirs(adir, exist_ok=True)
    fname = slug_file(slug) + ".md"
    path = os.path.join(adir, fname)

    other = "en" if lang == "zh" else "zh"
    pair_rel = f"../../{other}/{data['age']}/{fname}"

    fm = [
        "---",
        f'slug: "{slug}"',
        f'lang: "{lang}"',
        f'title: "{data["title"]}"',
        f'country: "{data["country"]}"',
        f'country_name: "{data["country_name"]}"',
        f'type: "{data["type"]}"',
        f'age_group: "{data["age"]}"',
        f'read_minutes: {data["minutes"]}',
        f'summary: "{data["summary"]}"',
        f'cjk_chars: {data["cjk"]}',
        f'word_count: {data["words"]}',
        f'para_count: {data["paras"]}',
        f'pair: "{pair_rel}"',
        'source: "sleepystory"',
        f'source_url: "{BASE}/{lang}/stories/{slug}"',
        f'cover: "{data["cover"]}"',
        'license: "转载自网络，家庭内部学习使用"',
        f'crawled_at: "{time.strftime("%Y-%m-%d %H:%M:%S")}"',
    ]
    if data["images"]:
        fm.append("images:")
        for u in data["images"][:8]:
            fm.append(f'  - "{u}"')
    fm.append("---")

    body_md = f"\n\n# {data['title']}\n\n"
    if data["summary"]:
        body_md += f"> {data['summary']}\n\n"
    body_md += data["body"] + "\n"

    with open(path, "w", encoding="utf-8") as f:
        f.write("\n".join(fm) + body_md)
    return f"{lang}/{data['age']}/{fname}", str(data["age"])


# ---------------------------------------------------------------- 抓取

def crawl(records: list[dict], workers: int, limit: int | None) -> None:
    done = set()
    for lang in LANGS:
        base = os.path.join(OUT, lang)
        if os.path.isdir(base):
            for root, _, files in os.walk(base):
                for fn in files:
                    if fn.endswith(".md"):
                        done.add((lang, fn[:-3]))
    todo = [r for r in records if (r["lang"], slug_file(r["slug"])) not in done]
    if done:
        log(f"[fetch] 已存在 {len(done)} 篇，跳过")
    if limit:
        todo = todo[:limit]
    if not todo:
        log("[fetch] 无新增")
        return
    log(f"[fetch] 待抓 {len(todo)} 篇，并发 {workers}")

    ok = [0]
    failed = []
    index = []
    t0 = time.time()

    def work(rec: dict) -> None:
        for attempt in range(3):
            h = fetch(rec["url"])
            if not h:
                time.sleep(0.5 * (attempt + 1))
                continue
            try:
                data = parse_page(h, rec["lang"])
                # 阈值必须放得很低：0-2 岁组是童谣，正文往往只有 40-60 字
                # （如「小狗崽，尾巴摇。跑跑跳，真开心。」），
                # 设成 60 会把整批最贴合幼儿的内容全部误杀。
                if not data or len(data["body"]) < 15:
                    with _lock:
                        failed.append({**rec, "reason": "无正文或过短"})
                    return
                rel, _ = write_story(rec, data)
                with _lock:
                    ok[0] += 1
                    index.append({
                        "slug": rec["slug"], "lang": rec["lang"],
                        "file": rel, "title": data["title"],
                        "age": data["age"], "type": data["type"],
                        "country": data["country"], "minutes": data["minutes"],
                        "cjk": data["cjk"], "words": data["words"],
                        "cover": data["cover"],
                    })
                    if ok[0] % 300 == 0:
                        el = time.time() - t0
                        log(f"[fetch] {ok[0]}/{len(todo)} "
                            f"({ok[0]/el:.1f} 篇/秒，剩余 "
                            f"{(len(todo)-ok[0])/ok[0]*el/60:.1f} 分钟)")
                return
            except Exception as e:  # noqa: BLE001
                if attempt == 2:
                    with _lock:
                        failed.append({**rec, "reason": f"异常 {e}"})
                time.sleep(0.4 * (attempt + 1))
        with _lock:
            failed.append({**rec, "reason": "网络失败"})

    with futures.ThreadPoolExecutor(max_workers=workers) as ex:
        list(ex.map(work, todo))

    if index:
        with open(os.path.join(OUT, "_index.jsonl"), "a", encoding="utf-8") as f:
            for r in index:
                f.write(json.dumps(r, ensure_ascii=False) + "\n")
    if failed:
        with open(os.path.join(OUT, "_failed.jsonl"), "w", encoding="utf-8") as f:
            for r in failed:
                f.write(json.dumps(r, ensure_ascii=False) + "\n")
    log(f"[fetch] 完成：成功 {ok[0]}，失败 {len(failed)}，"
        f"耗时 {(time.time()-t0)/60:.1f} 分钟")
    if STATS["http_err"]:
        log(f"[diag ] HTTP 错误分布：{dict(STATS['http_err'])}")
    log(f"[diag ] 网络异常 {STATS['net_err']} 次，成功请求 {STATS['ok']} 次")


def build_pairs() -> None:
    idx_path = os.path.join(OUT, "_index.jsonl")
    if not os.path.exists(idx_path):
        return
    rows = [json.loads(l) for l in open(idx_path, encoding="utf-8") if l.strip()]
    by = defaultdict(dict)
    for r in rows:
        by[r["slug"]][r["lang"]] = r
    pairs = []
    for slug, d in by.items():
        if "zh" in d and "en" in d:
            pairs.append({
                "slug": slug,
                "zh_file": d["zh"]["file"], "zh_title": d["zh"]["title"],
                "en_file": d["en"]["file"], "en_title": d["en"]["title"],
                "age": d["zh"]["age"], "type": d["zh"]["type"],
                "country": d["zh"]["country"], "minutes": d["zh"]["minutes"],
                "zh_cjk": d["zh"]["cjk"], "en_words": d["en"]["words"],
            })
    with open(os.path.join(OUT, "_pairs.jsonl"), "w", encoding="utf-8") as f:
        for p in pairs:
            f.write(json.dumps(p, ensure_ascii=False) + "\n")
    log(f"[pair ] 中英配对 {len(pairs)} 组 → _pairs.jsonl")


def stats() -> None:
    total = defaultdict(int)
    by_age = defaultdict(lambda: defaultdict(int))
    for lang in LANGS:
        base = os.path.join(OUT, lang)
        if not os.path.isdir(base):
            continue
        for d in os.listdir(base):
            p = os.path.join(base, d)
            if os.path.isdir(p):
                n = len([f for f in os.listdir(p) if f.endswith(".md")])
                total[lang] += n
                by_age[lang][d] += n
    log("=== 采集统计 ===")
    for lang in LANGS:
        ages = "  ".join(f"{a}:{by_age[lang][a]}" for a in sorted(by_age[lang]))
        log(f"  {lang}: {total[lang]} 篇   [{ages}]")
    log(f"  合计: {sum(total.values())} 篇")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--workers", type=int, default=10)
    ap.add_argument("--lang", choices=LANGS, help="只抓单一语言")
    ap.add_argument("--limit", type=int)
    ap.add_argument("--refresh-sitemap", action="store_true")
    ap.add_argument("--retry-failed", action="store_true")
    ap.add_argument("--stats", action="store_true")
    ap.add_argument("--delay", type=float, default=0.0,
                    help="每个请求前的随机延迟基数（秒）。站点走 Cloudflare，"
                         "高并发会被拖慢，建议 0.3–0.6")
    args = ap.parse_args()

    os.makedirs(OUT, exist_ok=True)
    set_delay(args.delay)
    if args.stats:
        stats()
        return

    if args.retry_failed:
        p = os.path.join(OUT, "_failed.jsonl")
        if not os.path.exists(p):
            log("[retry] 无失败清单")
            return
        recs = [json.loads(l) for l in open(p, encoding="utf-8") if l.strip()]
        log(f"[retry] 重试 {len(recs)} 篇")
        os.remove(p)
        crawl(recs, min(args.workers, 5), None)
        build_pairs()
        stats()
        return

    recs = load_sitemap(args.refresh_sitemap)
    if args.lang:
        recs = [r for r in recs if r["lang"] == args.lang]
    crawl(recs, args.workers, args.limit)
    build_pairs()
    stats()


if __name__ == "__main__":
    main()
