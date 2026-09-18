# -*- coding: utf-8 -*-
"""
生成「汉字学习阶段表」并按控字规则生成组词。

针对 POC 发现的组词质量问题做的修正：
  1. 词源从 THUOCL（领域词库）换成 **结巴主词典**（58 万词，通用语料词频）
     —— 实测 THUOCL 给出"丁酸、丙二醇、灌丛"，结巴给出"一个、人们、火车"。
  2. 过滤：只保留 2–3 字、全为通用规范汉字（去掉繁体异体）、剔除幼儿不宜词。
  3. 用「字常用度」给 8105 字排序，切成 S0 / S1-S5 / G1-G12 / X1-X6 共 24 阶段。
  4. 组词应用**控字规则**：某词的可用阶段 = 它所含各字的最大阶段序号。
     低阶段孩子看到的组词，一定只由他学过的字构成。

产出：
  var/raw/stages.json        阶段 → 字表
  var/raw/stage_words.jsonl  每字的组词（已控字）
  var/raw/stage_report.md    统计报告
"""

import json
import os
import re
import sys
from collections import defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RAW = os.path.join(ROOT, "var", "raw")

CJK = r"[一-鿿]"

# S0：数学题目常用字（人工核定，详见《内容与分级体系设计》§2.2）
S0_CHARS = set(
    "一二三四五六七八九十百千万零半两双几每各共"
    "个只条颗朵张把本支辆块片对群排根粒台件份束串"
    "多少大小长短高矮粗细厚薄轻重快慢远近最更样同"
    "上下左右前后里外中间旁边内第"
    "加减等于还剩合起总计差和出够不到又再全部"
    "看数找画圈连填选算想分类比说写划是有"
    "圆方形正球星"
    "时分秒点年月日星期早晚今明"
    "元钱块毛币"
    "来去走拿放买卖送给吃喝玩"
    "对答题目结果"
)

# 幼儿不宜词（军事/政治/暴力/成人/过度书面），组词时剔除
BLOCK_WORDS = set("""
抗日 战争 军事 军队 武器 枪炮 炮弹 炸弹 射击 杀死 死亡 尸体 毒品 赌博 监狱 警察
法院 官司 党员 政府 主义 革命 斗争 皇帝 朝廷 奴隶 殖民地 皇上 封建
股票 期货 贷款 利率 税收 合同 诉讼 违约 博彩 色情
中央 省委 市委 县委 国务院 主席 总理 部长 部队 敌 匪 奸 贼 屠
他妈的 妈的 你妈 狗日 走狗 二奶 干爹 小姐 妓女 嫖 卖淫 傻子 笨蛋 混蛋
王八 屁股 大便 小便 拉屎 撒尿 屎尿 妓院 淫秽 裸体 死人 杀人 出血 血腥
母狗 公狗 大奶妹 少奶奶 奶妹 母猪 种猪 公猪 猪肚 猪圈 狗鱼 店小二 天安门
校长  러 伤亡 牺牲 作战 敌人 敌军 战俘 绑架 抢劫 小偷 盗窃 骗子 法庭
""".split())

# 含这些字的词一律不要（粗俗/暴力单字兜底）
BLOCK_CHARS = set("尸屎尿屁屄肏淫赌毒匪奸屠妓")


def load_chars():
    """kTGHZ2013 → {char: [pinyin]}"""
    out = {}
    with open(os.path.join(RAW, "ktghz.txt"), encoding="utf-8") as f:
        for line in f:
            m = re.match(
                r"U\+[0-9A-Fa-f]+:\s*([a-zà-ǜāáǎàēéěèīíǐìōóǒòūúǔùǖǘǚǜü]+.*?)\s*#\s*(\S)",
                line)
            if m:
                out[m.group(2)] = m.group(1).strip().split()
    return out


def load_extra():
    """复用 hanzi.jsonl 已解析的部首/笔画/笔顺标记"""
    out = {}
    p = os.path.join(RAW, "hanzi.jsonl")
    if os.path.exists(p):
        with open(p, encoding="utf-8") as f:
            for line in f:
                r = json.loads(line)
                out[r["char"]] = r
    return out


def load_word_freq():
    freq = {}
    path = os.path.join(RAW, "jieba_dict.txt")
    if not os.path.exists(path):
        print("缺少 var/raw/jieba_dict.txt")
        sys.exit(1)
    with open(path, encoding="utf-8") as f:
        for line in f:
            p = line.split()
            if len(p) < 2:
                continue
            try:
                freq[p[0]] = max(freq.get(p[0], 0), int(p[1]))
            except ValueError:
                pass
    return freq


def main():
    print("=" * 64)
    print("汉字阶段生成 + 控字组词")
    print("=" * 64)

    chars_py = load_chars()
    print(f"[load ] 通用规范汉字：{len(chars_py)} 字")
    extra = load_extra()
    wfreq = load_word_freq()
    print(f"[load ] 结巴词库：{len(wfreq):,} 词")

    valid = set(chars_py)

    words = {}
    for w, f in wfreq.items():
        if not re.fullmatch(CJK + "{2,3}", w):
            continue
        if not set(w) <= valid:
            continue
        if w in BLOCK_WORDS or (set(w) & BLOCK_CHARS):
            continue
        words[w] = f
    print(f"[clean] 过滤后可用词：{len(words):,} 条")

    char_score = defaultdict(int)
    for w, f in words.items():
        for c in set(w):
            char_score[c] += f

    all_chars = list(chars_py)
    s0 = sorted(c for c in S0_CHARS if c in valid)

    # S1–S5 用人工主题种子词提取（纯字频会把「国、产、生、地」排在前面，
    # 那是新闻语料的高频字，不是幼儿口语的高频字）
    seeds_path = os.path.join(ROOT, "tools", "curated_seeds.json")
    seeded, report_lines = {}, []
    if os.path.exists(seeds_path):
        with open(seeds_path, encoding="utf-8") as f:
            seeds = json.load(f)
        # 逐阶段累计去重：同一个字只归它「首次出现」的阶段
        seen_all = set(s0)
        for name in ("S1", "S2", "S3", "S4", "S5"):
            ordered = []
            for w in seeds.get(name, []):
                for c in w:
                    if c in valid and c not in seen_all:
                        seen_all.add(c)
                        ordered.append(c)
            seeded[name] = ordered[:200]
            report_lines.append(f"        {name} 种子词抽出 {len(ordered)} 字，取前 200")

    used = set(s0) | {c for v in seeded.values() for c in v}
    rest = sorted((c for c in all_chars if c not in used),
                  key=lambda c: -char_score.get(c, 0))

    stages = {"S0": s0}
    i = 0
    for k in range(1, 6):
        base = list(seeded.get(f"S{k}", [])[:200])
        # 补位必须始终从 rest[i:] 起切，否则连续多个阶段不足时
        # 会重复取到同一批字（曾导致 568 个重复、148 字归属冲突）
        need = 200 - len(base)
        if need > 0:
            base += rest[i:i + need]
            i += need
        stages[f"S{k}"] = base
    for k in range(1, 13):
        stages[f"G{k}"] = rest[i:i + 250]
        i += 250
    left = rest[i:]
    per = max(1, (len(left) + 5) // 6)
    for k in range(1, 7):
        stages[f"X{k}"] = left[(k - 1) * per:k * per]

    order = ["S0"] + [f"S{k}" for k in range(1, 6)] + \
            [f"G{k}" for k in range(1, 13)] + [f"X{k}" for k in range(1, 7)]
    stage_of = {}
    dup_cnt = 0
    for idx, name in enumerate(order):
        for c in stages[name]:
            if c in stage_of:
                dup_cnt += 1
            stage_of[c] = idx
    if dup_cnt:
        print(f"[FATAL] 阶段字表存在 {dup_cnt} 个跨阶段重复字，请修补齐逻辑")
    else:
        print(f"[check] 阶段字表去重校验通过：{len(stage_of)} 字无重复")

    print("[stage] 阶段划分完成")
    for ln in report_lines:
        print(ln)
    for name in order[:9]:
        print(f"        {name:4s} {len(stages[name]):4d} 字  {''.join(stages[name][:12])}")

    word_stage = {}
    for w in words:
        if set(w) <= set(stage_of):
            word_stage[w] = max(stage_of[c] for c in w)

    on_stage = defaultdict(list)   # 全控字：词的所有字都已学
    relax1 = defaultdict(list)     # 放宽：允许 1 个超纲字
    for w, st in word_stage.items():
        for c in set(w):
            on_stage[(c, st)].append(w)
    for w in words:
        if not set(w) <= set(stage_of):
            continue
        for c in set(w):
            others = [stage_of[x] for x in w if x != c]
            mx = max(others) if others else 0
            if stage_of[c] <= mx and mx - stage_of[c] <= 1:
                relax1[(c, stage_of[c])].append((mx, w))

    out_path = os.path.join(RAW, "stage_words.jsonl")
    cnt = defaultdict(int)
    with open(out_path, "w", encoding="utf-8") as f:
        for idx, name in enumerate(order):
            for c in stages[name]:
                avail = []
                for st in range(idx + 1):
                    avail += on_stage.get((c, st), [])
                avail = sorted(set(avail), key=lambda w: -words[w])
                top = avail[:6]
                has_ood = False
                if len(top) < 4:
                    # 全控字词太少 → 放宽到允许 1 个超纲字
                    cand = sorted(set(w for _, w in relax1.get((c, idx), [])),
                                  key=lambda w: -words[w])
                    for w in cand:
                        if w not in top:
                            top.append(w)
                            has_ood = True
                        if len(top) >= 6:
                            break
                e = extra.get(c, {})
                f.write(json.dumps({
                    "char": c, "stage": name, "stage_index": idx,
                    "pinyin": chars_py.get(c, []),
                    "radical": e.get("radical", ""),
                    "stroke_count": e.get("stroke_count", ""),
                    "has_stroke_anim": e.get("has_stroke_anim", False),
                    "words": top,
                    "words_with_new_char": has_ood,
                }, ensure_ascii=False) + "\n")
                if top:
                    cnt[name] += 1
    print(f"[write] {out_path}")

    with open(os.path.join(RAW, "stages.json"), "w", encoding="utf-8") as f:
        json.dump(stages, f, ensure_ascii=False, indent=1)

    rows = defaultdict(list)
    with open(out_path, encoding="utf-8") as f:
        for line in f:
            r = json.loads(line)
            rows[r["stage"]].append(r)

    L = ["# 阶段划分与控字组词报告", "", "## 一、各阶段字数与组词覆盖", "",
         "| 阶段 | 字数 | 有组词 | 覆盖率 |", "| --- | --- | --- | --- |"]
    for name in order:
        n = len(stages[name])
        L.append(f"| {name} | {n} | {cnt[name]} | {cnt[name]*100.0/n:.1f}% |")
    L += ["", f"**合计 {sum(len(v) for v in stages.values()):,} 字**", "",
          "## 二、组词抽样（每阶段前 6 字）", ""]
    for name in order:
        L += [f"### {name}", "", "| 字 | 拼音 | 组词 |", "| --- | --- | --- |"]
        for r in rows.get(name, [])[:6]:
            L.append(f"| {r['char']} | {'/'.join(r['pinyin'])} | "
                     f"{'、'.join(r['words']) or '—'} |")
        L.append("")

    with open(os.path.join(RAW, "stage_report.md"), "w", encoding="utf-8") as f:
        f.write("\n".join(L))
    print()
    for name in order[:6]:
        print(f"--- {name} ---")
        for r in rows.get(name, [])[:5]:
            print(f"  {r['char']} {'/'.join(r['pinyin']):10s} -> {'、'.join(r['words']) or '-'}")
    print("\n完成。")


if __name__ == "__main__":
    main()
