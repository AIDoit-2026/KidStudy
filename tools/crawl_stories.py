#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
第一故事（yigushi.com）故事采集器。

站点：Destoon CMS，UTF-8，robots.txt 允许抓取 /gushi/。
列表分页：/gushi/{cat}/list{N}.html
详情页  ：/gushi/{id}.html
结构    ：<h1 class="title" id="title">标题</h1> / <div class="info">日期：YYYY-MM-DD</div>
          <div class="content" id="article">正文</div>

输出：var/stories/{分类中文名}/{id}-{slug}.md
      YAML Front Matter + Markdown 正文（符合《内容格式规范与故事采集》定义）
      var/stories/_index.jsonl  全量索引
      var/stories/_failed.jsonl 失败清单（可重跑补采）

用法：
  python tools/crawl_stories.py                # 全量（列表 + 详情）
  python tools/crawl_stories.py --list-only    # 只抓列表，产出 _urls.jsonl
  python tools/crawl_stories.py --detail-only  # 用已有 _urls.jsonl 抓详情
  python tools/crawl_stories.py --retry-failed # 只重试 _failed.jsonl
  python tools/crawl_stories.py --cat ertonggushi --limit 50   # 冒烟
  python tools/crawl_stories.py --workers 12   # 调整并发（默认 8，礼貌起见别太高）
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
from collections import defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT = os.path.join(ROOT, "var", "stories")
RAW = os.path.join(ROOT, "var", "raw")

BASE = "https://www.yigushi.com"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/126.0 Safari/537.36")

# 站点分类 → 中文目录名（同时标注是否适合幼苗 <-6 岁）
CATEGORIES = {
    "ertonggushi":   ("儿童故事", 1),
    "yuyangushi":    ("寓言故事", 1),
    "chengyugushi":  ("成语故事", 1),
    "minjiangushi":  ("民间故事", 1),
    "yizhigushi":    ("益智故事", 1),
    "lishigushi":    ("历史故事", 0),
    "mingrengushi":  ("名人故事", 0),
    "renshenggushi": ("人生故事", 0),
    "youmogushi":    ("幽默故事", 0),
    "zheligushi":    ("哲理故事", 0),
    "lizhigushi":    ("励志故事", 0),
    "zhichanggushi": ("职场故事", 0),
}

# 正文最少字数，低于此值视为残页
MIN_CHARS = 80

_print_lock = threading.Lock()


def log(msg: str) -> None:
    with _print_lock:
        print(msg, flush=True)


def fetch(url: str, timeout: int = 20, retries: int = 3) -> str | None:
    """带重试与退避的 GET。返回解码后的 HTML，失败返回 None。"""
    for attempt in range(retries):
        try:
            req = urllib.request.Request(url, headers={
                "User-Agent": UA,
                "Accept": "text/html,application/xhtml+xml,*/*;q=0.8",
                "Accept-Language": "zh-CN,zh;q=0.9",
                "Referer": BASE + "/gushi/",
            })
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                raw = resp.read()
                for enc in ("utf-8", "gb18030"):
                    try:
                        return raw.decode(enc)
                    except UnicodeDecodeError:
                        continue
                return raw.decode("utf-8", errors="replace")
        except Exception:  # noqa: BLE001  网络类异常一律重试
            if attempt == retries - 1:
                return None
            time.sleep(0.6 * (2 ** attempt) + random.random() * 0.4)
    return None


# ---------------------------------------------------------------- 阶段一：列表

RE_ITEM = re.compile(
    r'<a[^>]+href="(https?://[^"]*?/gushi/(\d+)\.html)"[^>]*?title="([^"]*)"',
    re.S)
RE_CITE = re.compile(r"<cite>共(\d+)条/(\d+)页")


def cat_page_count(cat: str) -> int:
    total_pages = 0
    for url in (f"{BASE}/gushi/{cat}/", f"{BASE}/gushi/{cat}/list1.html"):
        h = fetch(url)
        if not h:
            continue
        m = RE_CITE.search(h)
        if m:
            total_pages = int(m.group(2))
            break
    return total_pages


def parse_list(html_text: str, cat: str) -> list[tuple[str, str, str]]:
    """返回 [(id, title, cat)]，去重保序。"""
    seen, out = set(), []
    for url, sid, title in RE_ITEM.findall(html_text):
        if "/gushi/" not in url or sid in seen:
            continue
        seen.add(sid)
        out.append((sid, html.unescape(title).strip(), cat))
    return out


def build_url_list(workers: int, only_cat: str | None) -> list[dict]:
    urls_path = os.path.join(OUT, "_urls.jsonl")
    existing: dict[str, dict] = {}
    if os.path.exists(urls_path):
        with open(urls_path, encoding="utf-8") as f:
            for line in f:
                try:
                    r = json.loads(line)
                    existing[r["id"]] = r
                except Exception:  # noqa: BLE001
                    pass
        log(f"[list ] 已有 {len(existing)} 条 URL，跳过已抓分页")

    cats = [only_cat] if only_cat else list(CATEGORIES)
    jobs = []
    for cat in cats:
        pages = cat_page_count(cat)
        if not pages:
            log(f"[warn ] {cat}: 取不到页数，跳过")
            continue
        log(f"[list ] {CATEGORIES[cat][0]}({cat}): {pages} 页")
        for p in range(1, pages + 1):
            jobs.append((cat, p, f"{BASE}/gushi/{cat}/list{p}.html"))

    found: dict[str, dict] = {}
    lock = threading.Lock()
    done = [0]

    def work(job: tuple[str, int, str]) -> None:
        cat, pno, url = job
        h = fetch(url)
        if not h:
            return
        items = parse_list(h, cat)
        with lock:
            for sid, title, c in items:
                found.setdefault(sid, {"id": sid, "title": title, "cat": c,
                                       "page": pno})
            done[0] += 1
            if done[0] % 100 == 0:
                log(f"[list ] 已完成 {done[0]}/{len(jobs)} 页，累计 {len(found)} 篇")

    with futures.ThreadPoolExecutor(max_workers=workers) as ex:
        list(ex.map(work, jobs))

    merged = dict(existing)
    merged.update(found)
    with open(urls_path, "w", encoding="utf-8") as f:
        for r in merged.values():
            f.write(json.dumps(r, ensure_ascii=False) + "\n")
    log(f"[list ] 列表完成，合计 {len(merged)} 篇 → _urls.jsonl")
    return list(merged.values())


# ---------------------------------------------------------------- 阶段二：详情

RE_TITLE = re.compile(r'<h1[^>]*id="title"[^>]*>(.*?)</h1>', re.S)
RE_DATE = re.compile(r"日期：\s*(\d{4}-\d{2}-\d{2})")
RE_ARTICLE = re.compile(
    r'<div class="content" id="article">(.*?)</div>\s*</div>\s*<div class="np">', re.S)
RE_ARTICLE_LAZY = re.compile(r'<div class="content" id="article">(.*?)</div>', re.S)
RE_IMG = re.compile(r"<img[^>]*>", re.I)
RE_SCRIPT = re.compile(r"<(script|style|iframe)[^>]*>.*?</\1>", re.S | re.I)
RE_FULLWIDTH = re.compile(r"[\u3000\xa0]+")


def clean_text(seg: str) -> tuple[str, list[str]]:
    """正文 HTML → (纯文本正文, 图片 URL 列表)"""
    imgs = re.findall(r'src="([^"]+)"', seg)
    seg = RE_SCRIPT.sub("", seg)
    seg = RE_IMG.sub("", seg)
    seg = re.sub(r"<br\s*/?>", "\n", seg, flags=re.I)
    seg = re.sub(r"</p\s*>", "\n", seg, flags=re.I)
    seg = re.sub(r"<[^>]+>", "", seg)
    seg = html.unescape(seg)
    lines = []
    for ln in seg.split("\n"):
        ln = RE_FULLWIDTH.sub("", ln).strip()
        if ln:
            lines.append(ln)
    return "\n\n".join(lines), imgs


def slugify(title: str, limit: int = 24) -> str:
    s = re.sub(r"[\\/:*?\"<>|\s]+", "", title)
    s = re.sub(r"[^\w\u4e00-\u9fff\-]", "", s)
    return s[:limit] or "untitled"


BAD_TITLE_PAT = re.compile(r"成人|情色|鬼故事|恐怖|惊悚")


def write_story(rec: dict, h: str) -> tuple[bool, str]:
    """解析详情页并落盘。返回 (是否成功, 说明)"""
    mt = RE_TITLE.search(h)
    title = html.unescape(mt.group(1)).strip() if mt else rec["title"]
    if not title:
        return False, "无标题"

    m = RE_ARTICLE.search(h) or RE_ARTICLE_LAZY.search(h)
    if not m:
        return False, "无正文容器"
    body, imgs = clean_text(m.group(1))

    # 去掉正文里常见的站点尾巴
    for tail in ("【上一篇】", "上一篇：", "下一篇：", "本文来自", "本文地址",
                 "手机版地址", "第一故事", "更多精彩"):
        idx = body.find(tail)
        if idx > 0:
            body = body[:idx].strip()

    md = RE_DATE.search(h)
    date = md.group(1) if md else ""

    n_chars = len(re.findall(r"[\u4e00-\u9fff]", body))
    if n_chars < MIN_CHARS:
        return False, f"正文过短({n_chars}字)"

    cname, _ = CATEGORIES[rec["cat"]]
    cdir = os.path.join(OUT, cname)
    fname = f"{rec['id']}-{slugify(title)}.md"
    path = os.path.join(cdir, fname)

    # 按《内容格式规范》写 Front Matter，level 由后续阶段生成器回填
    fm = [
        "---",
        f'id: "{rec["id"]}"',
        f'title: "{title}"',
        f'category: "{cname}"',
        f'source: "yigushi"',
        f'source_url: "{BASE}/gushi/{rec["id"]}.html"',
        f'date: "{date}"',
        f'char_count: {n_chars}',
        f'para_count: {len([x for x in body.split(chr(10)+chr(10)) if x.strip()])}',
        "level: null            # 待 build_stages 控字校验后回填（S0-S5/G1-G12）",
        "new_chars: []          # 本阶段新增生字，待回填",
        "keywords: []           # 待回填",
        "questions: []          # 阅读理解题，待生成",
        "discussion: []         # 亲子口头讨论题，待生成",
        'license: "转载自网络，家庭内部学习使用"',
        f'crawled_at: "{time.strftime("%Y-%m-%d %H:%M:%S")}"',
    ]
    if imgs:
        fm.append("images:")
        for u in imgs[:6]:
            if u.startswith("http"):
                fm.append(f'  - "{u}"')
    fm.append("---")

    tail_block = ""
    if imgs:
        tail_block = "\n\n---\n\n## 插图\n\n"
        for u in imgs[:6]:
            if u.startswith("http"):
                tail_block += f"![插图]({u})\n\n"

    os.makedirs(cdir, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write("\n".join(fm) + f"\n\n# {title}\n\n{body}\n" + tail_block)
    return True, f"{n_chars}字"


def crawl_details(records: list[dict], workers: int, limit: int | None) -> None:
    log(f"[fetch] {len(records)} 篇待抓详细内容")

    # 已落盘的跳过（按 id 前缀匹配，支持改名后仍幂等）
    done_ids = set()
    for cname, _ in CATEGORIES.values():
        cdir = os.path.join(OUT, cname)
        if os.path.isdir(cdir):
            for fn in os.listdir(cdir):
                if fn.endswith(".md"):
                    done_ids.add(fn.split("-")[0])
    todo = [r for r in records if r["id"] not in done_ids]
    if done_ids:
        log(f"[fetch] 已存在 {len(done_ids)} 篇，跳过")
    if limit:
        todo = todo[:limit]
    if not todo:
        log("[fetch] 无新增，完成")
        return

    ok = [0]
    failed: list[dict] = []
    lock = threading.Lock()
    t0 = time.time()

    def work(rec: dict) -> None:
        url = f"{BASE}/gushi/{rec['id']}.html"
        for attempt in range(3):
            h = fetch(url)
            if not h:
                time.sleep(0.5 * (attempt + 1))
                continue
            try:
                success, note = write_story(rec, h)
            except Exception as e:  # noqa: BLE001
                success, note = False, f"异常 {e}"
            if success:
                with lock:
                    ok[0] += 1
                    if ok[0] % 200 == 0:
                        el = time.time() - t0
                        spd = ok[0] / el if el else 0
                        log(f"[fetch] {ok[0]}/{len(todo)} "
                            f"({spd:.1f} 篇/秒，预计剩余 "
                            f"{(len(todo)-ok[0])/spd/60:.1f} 分钟)")
                return
            if "正文过短" in note or "无正文容器" in note or "无标题" in note:
                with lock:
                    failed.append({**rec, "reason": note})
                return
            time.sleep(0.4 * (attempt + 1))
        with lock:
            failed.append({**rec, "reason": "网络失败"})

    with futures.ThreadPoolExecutor(max_workers=workers) as ex:
        list(ex.map(work, todo))

    # 礼节性小停顿，别在结束时打满对方最后一秒
    if failed:
        with open(os.path.join(OUT, "_failed.jsonl"), "w", encoding="utf-8") as f:
            for r in failed:
                f.write(json.dumps(r, ensure_ascii=False) + "\n")
    log(f"[fetch] 完成：成功 {ok[0]}，失败 {len(failed)}，"
        f"耗时 {(time.time()-t0)/60:.1f} 分钟")


def retry_failed(workers: int) -> None:
    path = os.path.join(OUT, "_failed.jsonl")
    if not os.path.exists(path):
        log("[retry] 无失败清单")
        return
    recs = [json.loads(l) for l in open(path, encoding="utf-8") if l.strip()]
    log(f"[retry] 重新抓取 {len(recs)} 篇")
    # 清掉 文件，让 crawl_details 重新决策（正文过短的会再次落盘为失败）
    crawl_details(recs, workers, None)


def stats() -> None:
    total, by_cat = 0, defaultdict(int)
    for cname, _ in CATEGORIES.values():
        cdir = os.path.join(OUT, cname)
        if os.path.isdir(cdir):
            n = len([f for f in os.listdir(cdir) if f.endswith(".md")])
            by_cat[cname] = n
            total += n
    log("=== 采集统计 ===")
    for cname, n in sorted(by_cat.items(), key=lambda x: -x[1]):
        log(f"  {cname:<8s} {n:>6d} 篇")
    log(f"  {'合计':<8s} {total:>6d} 篇")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--workers", type=int, default=8, help="并发数，默认 8")
    ap.add_argument("--cat", help="只抓单个分类目录名，如 ertonggushi")
    ap.add_argument("--limit", type=int, help="最多抓多少篇（冒烟用）")
    ap.add_argument("--list-only", action="store_true")
    ap.add_argument("--detail-only", action="store_true",
                    help="跳过列表抓取，直接用 _urls.jsonl")
    ap.add_argument("--retry-failed", action="store_true")
    ap.add_argument("--stats", action="store_true")
    args = ap.parse_args()

    os.makedirs(OUT, exist_ok=True)

    if args.stats:
        stats()
        return
    if args.retry_failed:
        retry_failed(args.workers)
        stats()
        return

    if args.detail_only:
        upath = os.path.join(OUT, "_urls.jsonl")
        if not os.path.exists(upath):
            log("[err  ] _urls.jsonl 不存在，先跑一次列表抓取")
            sys.exit(1)
        records = [json.loads(l) for l in open(upath, encoding="utf-8") if l.strip()]
        if args.cat:
            records = [r for r in records if r["cat"] == args.cat]
    else:
        records = build_url_list(args.workers, args.cat)
        if args.list_only:
            log("[done ] 列表阶段结束（--list-only）")
            return

    crawl_details(records, args.workers, args.limit)
    stats()


if __name__ == "__main__":
    main()
