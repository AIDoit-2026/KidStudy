#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
故事语料分析：分级建议 + 幼儿适宜性筛查。

输入：var/stories/{分类}/*.md（crawl_stories.py 产出）
      var/raw/stages.json（build_stages.py 产出的 24 阶段字表）

输出：var/stories/_analysis.jsonl   每篇一行，含分级与适宜性结论
      var/stories/儿童故事分级清单.md      按分类的可读清单
      var/stories/_不合适清单.md          被判定不宜给幼儿的内容

用法：
  python tools/analyze_stories.py
"""

from __future__ import annotations

import json
import os
import re
from collections import Counter, defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
STORY_DIR = os.path.join(ROOT, "var", "stories")
RAW = os.path.join(ROOT, "var", "raw")
STAGES_JSON = os.path.join(RAW, "stages.json")

STAGE_ORDER = (["S0"] + [f"S{k}" for k in range(1, 6)]
               + [f"G{k}" for k in range(1, 13)] + [f"X{k}" for k in range(1, 7)])

RE_CJK = re.compile(r"[\u4e00-\u9fff]")

# ---------------------------------------------------------------- 适宜性
# 一级：明确不宜，默认拦截（血腥/暴力杀人/成人/粗口）
UNSAFE_PAT = re.compile(
    r"杀死|杀害|谋杀|鲜血|血淋淋|血腥|尸体|尸首|骸骨|砍死|捅死|枪毙|勒死|毒死|"
    r"杀人犯|"
    r"妓|嫖|色情|通奸|强奸|情夫|情妇|床戏|裸体|"
    r"混蛋|杂种|蠢货|他妈|妈的|"
    r"赌博|赌场|毒品|吸毒|贩毒")

# 二级：常见于儿童文学（童话里的鬼怪、悼念场景、寓言里的贬称），
# 不拦截，只标记「家长确认」。早期把「鬼/坟/傻子/王八」放一级，误杀率高达
# 19%（3263/17250），绝大多数是《妈妈赶走了"鬼"》这类正常篇目。
CAUTION_PAT = re.compile(
    r"鬼|幽灵|僵尸|妖怪|坟|棺材|地狱|墓|"
    r"傻子|笨蛋|王八|蠢材|"
    r"奴隶|屠杀|报仇|复仇|"
    r"死了|死亡|打死|饿死|淹死|摔死|病死|"
    r"欺骗|撒谎|偷|坏人|陷阱|惩罚|受伤|打仗|武器|军队|毒药")


def load_stages() -> dict[str, int]:
    """字 → 阶段序号。"""
    if not os.path.exists(STAGES_JSON):
        return {}
    stages = json.load(open(STAGES_JSON, encoding="utf-8"))
    out = {}
    for idx, name in enumerate(STAGE_ORDER):
        for c in stages.get(name, []):
            out[c] = idx
    return out


def parse_front_matter(text: str) -> tuple[dict, str]:
    if not text.startswith("---"):
        return {}, text
    end = text.find("\n---", 3)
    if end < 0:
        return {}, text
    fm = {}
    for line in text[3:end].splitlines():
        m = re.match(r'^(\w+):\s*(.*?)\s*(?:#.*)?$', line)
        if m:
            fm[m.group(1)] = m.group(2).strip().strip('"')
    return fm, text[end + 4:].lstrip("\n")


def grade_story(body: str, char_stage: dict[str, int]) -> dict:
    """按控字规则算「最低可读阶段」：文中所有字都在该阶段及之前。"""
    chars = [c for c in RE_CJK.findall(body)]
    if not chars:
        return {"level": None, "unknown_chars": 0, "unique_chars": 0,
                "oov_ratio": 0.0}
    uniq = set(chars)
    known = {c: char_stage[c] for c in uniq if c in char_stage}
    if not known:
        return {"level": None, "unknown_chars": len(uniq), "unique_chars": len(uniq),
                "oov_ratio": 1.0}
    need = sorted(known.values())
    unknown = len(uniq) - len(known)

    # 100% 覆盖过于严苛：一篇千字文里只要出现 1 个人名生僻字，整篇就被推到
    # X 阶段。改为统计「不同覆盖阈值下所需的最低阶段」，98% 是实用读法。
    def stage_at(ratio: float) -> int | None:
        if not need:
            return None
        target = len(uniq) * ratio
        keep = int(target)
        return need[min(keep, len(need)) - 1]

    return {
        "level": STAGE_ORDER[need[-1]],           # 100% 严格
        "level_idx": need[-1],
        "level98_idx": stage_at(0.98),            # 98% 实用
        "level98": STAGE_ORDER[stage_at(0.98)] if stage_at(0.98) is not None else None,
        "level95_idx": stage_at(0.95),
        "level95": STAGE_ORDER[stage_at(0.95)] if stage_at(0.95) is not None else None,
        "unique_chars": len(uniq),
        "unknown_chars": unknown,
        "oov_ratio": round(unknown / len(uniq), 4),
    }


def main() -> None:
    char_stage = load_stages()
    print(f"[load ] 阶段字表 {len(char_stage)} 字")

    rows = []
    for cat in sorted(os.listdir(STORY_DIR)):
        cdir = os.path.join(STORY_DIR, cat)
        if not os.path.isdir(cdir) or cat.startswith("_"):
            continue
        for fn in sorted(os.listdir(cdir)):
            if not fn.endswith(".md"):
                continue
            raw = open(os.path.join(cdir, fn), encoding="utf-8").read()
            fm, body = parse_front_matter(raw)
            # 去掉二级及以后的插图附录
            body = body.split("\n---\n")[0]
            body = re.sub(r"^#\s+.*$", "", body, flags=re.M)

            hits = sorted(set(UNSAFE_PAT.findall(body)))
            cautions = sorted(set(CAUTION_PAT.findall(body)))
            g = grade_story(body, char_stage)

            rows.append({
                "file": f"{cat}/{fn}",
                "cat": cat,
                "id": fm.get("id", ""),
                "title": fm.get("title", fn[:-3]),
                "source_url": fm.get("source_url", ""),
                "chars": len(RE_CJK.findall(body)),
                "paras": fm.get("para_count", ""),
                "level": g["level"],
                "level_idx": g.get("level_idx", -1),
                "level98": g.get("level98"),
                "level98_idx": g.get("level98_idx", -1),
                "level95": g.get("level95"),
                "unique_chars": g["unique_chars"],
                "oov_ratio": g.get("oov_ratio", 0),
                "unsafe": hits,
                "caution": cautions[:6],
            })

    out_path = os.path.join(STORY_DIR, "_analysis.jsonl")
    with open(out_path, "w", encoding="utf-8") as f:
        for r in rows:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")
    print(f"[out  ] _analysis.jsonl  {len(rows)} 篇")

    # ---------------- 统计
    by_cat = defaultdict(list)
    for r in rows:
        by_cat[r["cat"]].append(r)

    print("\n=== 各分类统计 ===")
    print(f"{'分类':<10s}{'总数':>7s}{'不宜':>7s}{'存疑':>7s}"
          f"{'可用':>7s}{'中位字数':>10s}")
    usable_all = []
    for cat, rs in sorted(by_cat.items(), key=lambda x: -len(x[1])):
        unsafe = [r for r in rs if r["unsafe"]]
        caution = [r for r in rs if not r["unsafe"] and r["caution"]]
        ok = [r for r in rs if not r["unsafe"] and not r["caution"]]
        usable_all += ok
        lens = sorted(r["chars"] for r in rs)
        med = lens[len(lens) // 2] if lens else 0
        print(f"{cat:<10s}{len(rs):>7d}{len(unsafe):>7d}{len(caution):>7d}"
              f"{len(ok):>7d}{med:>10d}")

    print(f"\n  合计：{len(rows)} 篇，其中可用（无不宜且无存疑）{len(usable_all)} 篇")

    # ---------------- 分级分布
    print("\n=== 按最低可读阶段分布（98% 用字覆盖）===")
    lv = Counter(r["level98"] for r in usable_all)
    order = {n: i for i, n in enumerate(STAGE_ORDER)}
    for name, n in sorted(lv.items(), key=lambda x: order.get(x[0], 99)):
        print(f"  {name or '?':<5s} {n:>6d} 篇")

    # ---------------- 不适合清单
    unsafe_rows = [r for r in rows if r["unsafe"]]
    bad_path = os.path.join(STORY_DIR, "_不合适清单.md")
    with open(bad_path, "w", encoding="utf-8") as f:
        f.write("# 不宜直接给幼儿的故事清单\n\n")
        f.write(f"共 {len(unsafe_rows)} 篇。判定依据：正文命中不宜词库"
                f"（恐怖/暴力/死亡渲染/成人关系/粗口）。\n\n")
        f.write("> 用途：阅读器默认屏蔽，家长可在后台手动解除。\n\n")
        byc = defaultdict(list)
        for r in unsafe_rows:
            byc[r["cat"]].append(r)
        for cat, rs in sorted(byc.items(), key=lambda x: -len(x[1])):
            f.write(f"## {cat}（{len(rs)} 篇）\n\n")
            for r in rs:
                f.write(f"- {r['title']} —— {','.join(r['unsafe'][:4])}\n")
            f.write("\n")
    print(f"[out  ] _不合适清单.md  {len(unsafe_rows)} 篇")

    # ---------------- 可读清单（每类取前 60，按字数适中排序）
    list_path = os.path.join(STORY_DIR, "分级清单.md")
    with open(list_path, "w", encoding="utf-8") as f:
        f.write("# 故事分级清单\n\n")
        f.write("`最低可读阶段` = 正文中出现的最晚阶段的字，"
                "即孩子学到该阶段即可通读全文。\n\n")
        f.write(f"可用总量 **{len(usable_all)}** 篇。每类按 stage 由低到高展示。\n\n")
        byc = defaultdict(list)
        for r in usable_all:
            byc[r["cat"]].append(r)
        for cat, rs in sorted(byc.items(), key=lambda x: -len(x[1])):
            rs.sort(key=lambda r: (r["level98_idx"], r["chars"]))
            f.write(f"## {cat}（可用 {len(rs)} 篇）\n\n")
            f.write("| 阶段(98%) | 标题 | 字数 | 生字数 |\n| --- | --- | --- | --- |\n")
            for r in rs[:80]:
                f.write(f"| {r['level98'] or '-'} | {r['title']} | {r['chars']} "
                        f"| {r['unique_chars']} |\n")
            if len(rs) > 80:
                f.write(f"\n_（展示前 80 篇，完整数据见 `_analysis.jsonl`）_\n")
            f.write("\n")
    print(f"[out  ] 分级清单.md")


if __name__ == "__main__":
    main()
