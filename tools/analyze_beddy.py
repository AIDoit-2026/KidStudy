#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
SleepyStory / BeddyStories 双语语料分析。

输入：var/beddy/_index.jsonl（crawl_beddy.py 产出）
      var/raw/stages.json（24 阶段汉字字表）
      var/raw/words.jsonl（ECDICT 英语词表，含 frq 词频排名）

输出：var/beddy/_analysis.jsonl
      var/beddy/中文分级清单.md
      var/beddy/英文分级清单.md
      var/beddy/双语对照清单.md

用法：python tools/analyze_beddy.py
"""

from __future__ import annotations

import json
import os
import re
from collections import Counter, defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BEDDY = os.path.join(ROOT, "var", "beddy")
RAW = os.path.join(ROOT, "var", "raw")

STAGE_ORDER = (["S0"] + [f"S{k}" for k in range(1, 6)]
               + [f"G{k}" for k in range(1, 13)] + [f"X{k}" for k in range(1, 7)])
RE_CJK = re.compile(r"[\u4e00-\u9fff]")
RE_WORD = re.compile(r"[a-z][a-z'\-]*")

# 英语：按 ECDICT frq 排名划分的词频带（frq 越小越常用）
FRQ_BANDS = [(500, "核心500"), (1000, "常用1000"), (2000, "常用2000"),
             (3000, "进阶3000"), (5000, "进阶5000"), (10 ** 9, "生僻")]


def load_char_stage() -> dict[str, int]:
    p = os.path.join(RAW, "stages.json")
    if not os.path.exists(p):
        return {}
    s = json.load(open(p, encoding="utf-8"))
    return {c: i for i, n in enumerate(STAGE_ORDER) for c in s.get(n, [])}


def load_frq() -> dict[str, int]:
    """word -> frq 排名（越小越常用）。

    必须用 ECDICT 全量 77 万词：words.jsonl 只有 1803 条，
    拿它当词频表会把绝大多数正常词判成"生僻"，core_ratio 完全失真。
    """
    p = os.path.join(RAW, "ecdict.csv")
    out: dict[str, int] = {}
    if not os.path.exists(p):
        print("[warn ] 缺少 ecdict.csv，回退到 words.jsonl（覆盖极低）")
        jp = os.path.join(RAW, "words.jsonl")
        if os.path.exists(jp):
            for line in open(jp, encoding="utf-8"):
                try:
                    r = json.loads(line)
                    if r.get("frq"):
                        out[r["word"].lower()] = int(r["frq"])
                except Exception:  # noqa: BLE001
                    continue
        return out
    import csv
    with open(p, encoding="utf-8", newline="") as f:
        rd = csv.DictReader(f)
        for row in rd:
            w = (row.get("word") or "").strip().lower()
            fr = (row.get("frq") or "").strip()
            if w and fr.isdigit():
                cur = out.get(w)
                v = int(fr)
                if cur is None or v < cur:
                    out[w] = v
    return out


def band(frq: int) -> str:
    for lim, name in FRQ_BANDS:
        if frq <= lim:
            return name
    return "生僻"


def grade_zh(body: str, cs: dict[str, int]) -> dict:
    uniq = set(RE_CJK.findall(body))
    if not uniq:
        return {"level": None, "level_idx": -1, "unique_chars": 0, "oov": 1.0}
    known = sorted(cs[c] for c in uniq if c in cs)
    if not known:
        return {"level": None, "level_idx": -1,
                "unique_chars": len(uniq), "oov": 1.0}
    keep = int(len(uniq) * 0.98)
    idx = known[min(keep, len(known)) - 1]
    # 98% 覆盖对短篇会严重失真：36 字的童谣里只要有 1 个「滚/竹/尝」这类
    # 排位靠后的字，整篇就被判到 G9。因此额外给出中位与均值作参考。
    med = known[len(known) // 2]
    mean = sum(known) / len(known)
    return {"level": STAGE_ORDER[idx], "level_idx": idx,
            "level_med": STAGE_ORDER[med], "level_med_idx": med,
            "level_mean_idx": round(mean, 2),
            "unique_chars": len(uniq),
            "oov": round(1 - len(known) / len(uniq), 4)}


def grade_en(body: str, frq: dict[str, int]) -> dict:
    words = [w for w in RE_WORD.findall(body.lower()) if len(w) > 1]
    if not words:
        return {"level": None, "unique_words": 0, "core_ratio": 0.0}
    uniq = set(words)
    ranked = {w: frq.get(w) for w in uniq}
    known = {w: f for w, f in ranked.items() if f}
    counts = Counter(band(f) for f in known.values())
    core = sum(v for k, v in counts.items()
               if k in ("核心500", "常用1000", "常用2000"))
    return {"level": None, "unique_words": len(uniq),
            "known_words": len(known),
            "core_ratio": round(core / len(uniq), 4) if uniq else 0,
            "bands": dict(counts)}


# 建议：英文难度粗分级（用于家长选篇，非严格 CEFR）
def en_advice(uniq: int, core_ratio: float, words: int) -> str:
    if words < 150 and core_ratio >= 0.75:
        return "E3-E4（短篇、核心词为主）"
    if core_ratio >= 0.70:
        return "E5-E6（常用词为主）"
    if core_ratio >= 0.55:
        return "E7-E8（含部分进阶词）"
    return "E9-E10（词汇偏难）"


def parse_front_matter(text: str) -> dict:
    """极简 YAML 解析（本项目的 front matter 只有标量字段）。"""
    fm = {}
    if not text.startswith("---"):
        return fm
    end = text.find("\n---", 3)
    if end < 0:
        return fm
    for line in text[3:end].splitlines():
        m = re.match(r'^(\w+):\s*(.*?)\s*$', line)
        if m:
            fm[m.group(1)] = m.group(2).strip().strip('"')
    return fm


def scan_files() -> list[dict]:
    """直接扫磁盘而不是读 _index.jsonl —— 后者是追加写入的，
    采集中途被中断的批次不会留下记录，用它做分析会漏掉大量已落盘的文件。"""
    rows = []
    for lang in ("zh", "en"):
        base = os.path.join(BEDDY, lang)
        if not os.path.isdir(base):
            continue
        for root, _, files in os.walk(base):
            for fn in files:
                if not fn.endswith(".md"):
                    continue
                path = os.path.join(root, fn)
                fm = parse_front_matter(open(path, encoding="utf-8").read())
                rel = os.path.relpath(path, BEDDY).replace("\\", "/")
                rows.append({
                    "file": rel, "lang": lang,
                    "slug": fm.get("slug", fn[:-3]),
                    "title": fm.get("title", fn[:-3]),
                    "age": fm.get("age_group", "unknown"),
                    "type": fm.get("type", ""),
                    "country": fm.get("country", ""),
                    "minutes": int(fm.get("read_minutes") or 0),
                    "cjk": int(fm.get("cjk_chars") or 0),
                    "words": int(fm.get("word_count") or 0),
                    "cover": fm.get("cover", ""),
                })
    return rows


def main() -> None:
    rows = scan_files()
    if not rows:
        print("[err ] var/beddy 下没有故事文件，先跑 crawl_beddy.py")
        return
    print(f"[load ] 磁盘扫描 {len(rows)} 篇")

    cs = load_char_stage()
    frq = load_frq()
    print(f"[load ] 汉字阶段表 {len(cs)} 字，英语词频 {len(frq)} 词")

    zh_rows, en_rows = [], []
    for r in rows:
        path = os.path.join(BEDDY, r["file"])
        if not os.path.exists(path):
            continue
        txt = open(path, encoding="utf-8").read()
        body = txt.split("\n---\n", 1)[-1]
        body = re.sub(r"^#\s+.*$", "", body, flags=re.M)
        body = re.sub(r"^>.*$", "", body, flags=re.M)

        if r["lang"] == "zh":
            g = grade_zh(body, cs)
            zh_rows.append({**r, **g, "chars": len(RE_CJK.findall(body))})
        else:
            g = grade_en(body, frq)
            g["advice"] = en_advice(g["unique_words"], g["core_ratio"],
                                    r.get("words", 0))
            en_rows.append({**r, **g})

    out = os.path.join(BEDDY, "_analysis.jsonl")
    with open(out, "w", encoding="utf-8") as f:
        for r in zh_rows + en_rows:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")
    print(f"[out  ] _analysis.jsonl  {len(zh_rows)+len(en_rows)} 条")

    # ---------------- 中文统计
    print("\n=== 中文：按最低可读阶段（98% 用字覆盖）===")
    order = {n: i for i, n in enumerate(STAGE_ORDER)}
    lv = Counter(r["level"] for r in zh_rows)
    cum = 0
    for n, c in sorted(lv.items(), key=lambda x: order.get(x[0], 99)):
        cum += c
        print(f"  {str(n):<5s}{c:>5d} 篇   累计 {cum}")

    print("\n=== 中文：按站点年龄组 ===")
    for a, n in sorted(Counter(r["age"] for r in zh_rows).items()):
        sub = [r for r in zh_rows if r["age"] == a]
        med = sorted(r["chars"] for r in sub)[len(sub) // 2]
        lv = sorted(r["level_med_idx"] for r in sub if "level_med_idx" in r)
        lv98 = sorted(r["level_idx"] for r in sub if r["level"])
        print(f"  {a:<8s}{n:>5d} 篇   中位字数 {med:<5d}"
              f"  中位字阶段 {STAGE_ORDER[lv[len(lv)//2]] if lv else '-':<5s}"
              f"  98%覆盖阶段 {STAGE_ORDER[lv98[len(lv98)//2]] if lv98 else '-'}")

    # ---------------- 英文统计
    print("\n=== 英文：难度建议 ===")
    for a, n in sorted(Counter(r["advice"] for r in en_rows).items()):
        print(f"  {a:<28s}{n:>5d} 篇")

    print("\n=== 英文：按站点年龄组 ===")
    for a, n in sorted(Counter(r["age"] for r in en_rows).items()):
        sub = [r for r in en_rows if r["age"] == a]
        w = sorted(r["words"] for r in sub)
        print(f"  {a:<8s}{n:>5d} 篇   中位词数 {w[len(w)//2]}")

    # ---------------- 输出清单
    def dump(path: str, header: str, rows_: list, cols: list, key):
        with open(path, "w", encoding="utf-8") as f:
            f.write(f"# {header}\n\n")
            f.write("| " + " | ".join(c[0] for c in cols) + " |\n")
            f.write("| " + " | ".join("---" for _ in cols) + " |\n")
            for r in sorted(rows_, key=key)[:400]:
                f.write("| " + " | ".join(str(c[1](r)) for c in cols) + " |\n")
            if len(rows_) > 400:
                f.write(f"\n_（展示前 400 篇，完整数据见 `_analysis.jsonl`）_\n")

    dump(os.path.join(BEDDY, "中文分级清单.md"), "中文故事分级清单", zh_rows,
         [("阶段", lambda r: r["level"] or "-"),
          ("标题", lambda r: r["title"]),
          ("年龄组", lambda r: r["age"]),
          ("类型", lambda r: r["type"]),
          ("字数", lambda r: r["chars"])],
         key=lambda r: (r["level_idx"], r["chars"]))

    dump(os.path.join(BEDDY, "英文分级清单.md"), "英文故事分级清单", en_rows,
         [("建议级别", lambda r: r["advice"]),
          ("标题", lambda r: r["title"]),
          ("年龄组", lambda r: r["age"]),
          ("词数", lambda r: r["words"]),
          ("核心词占比", lambda r: f'{r["core_ratio"]*100:.0f}%')],
         key=lambda r: (-r["core_ratio"], r["words"]))

    # ---------------- 双语对照（按 slug 配对，不依赖采集时的索引）
    by_slug = defaultdict(dict)
    for r in rows:
        by_slug[r["slug"]][r["lang"]] = r
    pairs = []
    for slug, d in by_slug.items():
        if "zh" in d and "en" in d:
            pairs.append({
                "slug": slug, "age": d["zh"]["age"], "type": d["zh"]["type"],
                "country": d["zh"]["country"], "minutes": d["zh"]["minutes"],
                "zh_file": d["zh"]["file"], "zh_title": d["zh"]["title"],
                "zh_cjk": d["zh"]["cjk"],
                "en_file": d["en"]["file"], "en_title": d["en"]["title"],
                "en_words": d["en"]["words"],
            })
    with open(os.path.join(BEDDY, "_pairs.jsonl"), "w", encoding="utf-8") as f:
        for p in pairs:
            f.write(json.dumps(p, ensure_ascii=False) + "\n")
        with open(os.path.join(BEDDY, "双语对照清单.md"), "w",
                  encoding="utf-8") as f:
            f.write("# 中英双语对照清单\n\n")
            f.write(f"共 **{len(pairs)}** 组。同一故事的中英文版本 slug 一致，"
                    f"可直接对照阅读。\n\n")
            f.write("| 中文标题 | 英文标题 | 年龄组 | 类型 | "
                    "中文字数 | 英文词数 |\n| --- | --- | --- | --- | --- | --- |\n")
            for p in sorted(pairs, key=lambda x: (x["age"], x["zh_title"])):
                f.write(f"| {p['zh_title']} | {p['en_title']} | {p['age']} | "
                        f"{p['type']} | {p['zh_cjk']} | {p['en_words']} |\n")
        print(f"[out  ] 双语对照清单.md  {len(pairs)} 组")

    print(f"\n[done ] 中文 {len(zh_rows)} 篇 / 英文 {len(en_rows)} 篇")


if __name__ == "__main__":
    main()
