# -*- coding: utf-8 -*-
"""
KidStudy 数据采集 POC（可行性验证）

目的：在正式开发前验证汉字 / 组词 / 英语 三类内容能采集到什么规模、字段完整度如何。
设计：优先复用 var/raw 下已下载的文件；缺失才下载；单个数据源失败不影响其他部分。

产出：
  var/raw/hanzi.jsonl      汉字全量数据（每行一个字）
  var/raw/words.jsonl      英语单词数据
  var/raw/poc_report.md    采集覆盖率报告

用法：
  set https_proxy=http://127.0.0.1:11086       # 访问 GitHub/npm 建议走代理
  set http_proxy=http://127.0.0.1:11086
  python tools/poc_collect.py                 # 全量
  python tools/poc_collect.py --skip-english  # 跳过英语（66MB 词典很慢时）
  python tools/poc_collect.py --no-download   # 只用本地已有文件
"""

# 采集目标站点（GitHub raw / npm registry / 清华 THUOCL）在国内直连很慢，
# 脚本内 urllib 会自动读取 http_proxy / https_proxy 环境变量，设置后即可加速。

import argparse
import csv
import json
import os
import re
import sys
import time
import urllib.request
from collections import defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RAW = os.path.join(ROOT, "var", "raw")
os.makedirs(RAW, exist_ok=True)

UA = "KidStudyBot/1.0 (+educational-research)"

# name -> (本地文件名, URL)
SOURCES = {
    "ktghz": ("ktghz.txt", "https://raw.githubusercontent.com/mozillazg/pinyin-data/master/kTGHZ2013.txt"),
    "xinhua": ("xinhua.json", "https://raw.githubusercontent.com/pwxcoo/chinese-xinhua/master/data/word.json"),
    "strokes": ("strokes.json", "https://raw.githubusercontent.com/chanind/hanzi-writer-data/master/data/all.json"),
    # 笔顺的压缩备用源（npm 包，体积小得多）
    "strokes_tgz": ("hwdata.tgz", "https://registry.npmjs.org/hanzi-writer-data/-/hanzi-writer-data-2.0.1.tgz"),
    "ecdict": ("ecdict.csv", "https://raw.githubusercontent.com/skywind3000/ECDICT/master/ecdict.csv"),
}
THUOCL = ["animal", "food", "chengyu", "poem", "diming", "caijing", "law", "medical", "IT", "car"]

# 英语主题种子词（内容词，人工核定；音标/释义/词频由词典补全）
THEMES = {
    "animals": "cat dog pig cow duck bird fish bear tiger lion monkey rabbit horse sheep chicken mouse frog bee ant elephant panda wolf fox deer snake turtle whale giraffe zebra camel goat donkey dolphin crab wing tail".split(),
    "food": "apple banana orange egg milk bread rice cake water juice meat soup noodle candy chocolate cookie grape pear peach lemon tomato potato carrot onion salt sugar meal breakfast lunch dinner".split(),
    "colors": "red blue green yellow black white pink purple brown gray gold silver color".split(),
    "family": "father mother dad mom brother sister baby family parent child son daughter grandpa grandma uncle aunt friend name".split(),
    "body": "head hair eye ear nose mouth tooth face hand foot leg arm finger knee back neck body".split(),
    "school": "book pen pencil bag desk chair school teacher student class paper ruler eraser crayon story word letter number".split(),
    "home": "house room door window bed table sofa clock lamp key box cup bowl plate spoon knife towel".split(),
    "clothes": "shirt coat dress hat shoe sock pants jacket glove scarf sweater".split(),
    "weather": "sun rain snow wind cloud hot cold warm cool weather sky air moon star earth".split(),
    "transport": "car bus bike train plane boat ship truck taxi road street map".split(),
    "nature": "tree flower grass leaf river sea mountain stone sand island forest garden".split(),
    "numbers": "one two three four five six seven eight nine ten eleven twelve twenty hundred zero".split(),
    "time": "day night morning afternoon evening today tomorrow yesterday hour minute week month year clock".split(),
    "actions": "go come eat drink sleep run walk jump play read write sing dance draw swim look see hear say speak love like have make do give take put open close find think know want".split(),
    "describing": "big small long short tall new old good bad happy sad fast slow clean dirty young nice kind".split(),
    "little_words": "in on under at to from with and or but yes no not this that here there what who where when how why".split(),
}

CJK = r"[一-鿿]"


def log(msg):
    print(msg, flush=True)


def resolve(name, allow_download):
    """返回已存在的本地文件路径；不存在且允许下载则尝试下载。"""
    fn, url = SOURCES[name]
    path = os.path.join(RAW, fn)
    if os.path.exists(path) and os.path.getsize(path) > 1024:
        log(f"[local] {fn}  {os.path.getsize(path)/1024/1024:.1f} MB")
        return path
    if not allow_download:
        log(f"[skip ] {fn} 不存在")
        return None
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    t0 = time.time()
    try:
        with urllib.request.urlopen(req, timeout=600) as r, open(path, "wb") as f:
            while True:
                chunk = r.read(262144)
                if not chunk:
                    break
                f.write(chunk)
    except Exception as e:  # noqa: BLE001
        log(f"[FAIL ] {fn}: {e}")
        return None
    log(f"[down ] {fn}  {os.path.getsize(path)/1024/1024:.1f} MB in {time.time()-t0:.0f}s")
    return path


def parse_ktghz(path):
    """U+4E00: yī  # 一   ->  {char: [pinyin]}"""
    out = {}
    with open(path, encoding="utf-8") as f:
        for line in f:
            m = re.match(r"U\+[0-9A-Fa-f]+:\s*([a-zà-ǜāáǎàēéěèīíǐìōóǒòūúǔùǖǘǚǜü]+.*?)\s*#\s*(\S)", line)
            if m:
                out[m.group(2)] = m.group(1).strip().split()
    return out


def parse_strokes(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def load_strokes(dl):
    """优先用 npm 压缩包（体积小），失败再用 all.json。"""
    import tarfile
    tgz = os.path.join(RAW, "hwdata.tgz")
    if os.path.exists(tgz) and os.path.getsize(tgz) > 1024 * 1024:
        try:
            out = {}
            with tarfile.open(tgz, "r:gz") as tf:
                for m in tf.getmembers():
                    if not m.isfile() or not m.name.endswith(".json"):
                        continue
                    ch = os.path.basename(m.name)[:-5]  # 去掉 .json
                    if len(ch) != 1:
                        continue
                    try:
                        out[ch] = json.loads(tf.extractfile(m).read().decode("utf-8"))
                    except Exception:  # noqa: BLE001
                        continue
            if out:
                log(f"[tar  ] 从 {os.path.basename(tgz)} 读入 {len(out)} 字的笔顺")
                return out, os.path.basename(tgz)
        except Exception as e:  # noqa: BLE001
            log(f"[warn ] 压缩包解压失败: {e}")
    p = resolve("strokes", dl)
    if p:
        try:
            return parse_strokes(p), os.path.basename(p)
        except Exception as e:  # noqa: BLE001
            log(f"[warn ] 笔顺解析失败: {e}")
    return {}, "—"


def parse_xinhua(path):
    """chinese-xinhua word.json：标准 JSON 数组。字段 strokes=笔画数, radicals=部首, pinyin, explanation。"""
    try:
        with open(path, encoding="utf-8") as f:
            arr = json.load(f)
    except Exception as e:  # noqa: BLE001
        log(f"[warn ] 新华字典 JSON 解析失败（文件可能未下完）: {e}")
        return {}
    out = {}
    for obj in arr:
        w = obj.get("word")
        if isinstance(w, str) and len(w) == 1:
            out[w] = obj
    return out


def parse_thuocl():
    freq = defaultdict(int)
    n = 0
    for t in THUOCL:
        p = os.path.join(RAW, f"thuocl_{t}.txt")
        if not os.path.exists(p):
            # 尝试下载（小文件）
            try:
                req = urllib.request.Request(
                    f"https://raw.githubusercontent.com/thunlp/THUOCL/master/data/THUOCL_{t}.txt",
                    headers={"User-Agent": UA})
                with urllib.request.urlopen(req, timeout=60) as r, open(p, "wb") as f:
                    f.write(r.read())
            except Exception:  # noqa: BLE001
                continue
        try:
            with open(p, encoding="utf-8") as f:
                for line in f:
                    parts = line.split()
                    if len(parts) < 2:
                        continue
                    w = parts[0]
                    if not re.fullmatch(CJK + "{2,4}", w):
                        continue
                    try:
                        freq[w] = max(freq[w], int(parts[1]))
                    except ValueError:
                        pass
            n += 1
        except Exception:  # noqa: BLE001
            pass
    return freq, n


def parse_ecdict(path, want):
    want = set(w.lower() for w in want)
    hit, extra = {}, []
    with open(path, encoding="utf-8", newline="") as f:
        for row in csv.DictReader(f):
            w = (row.get("word") or "").strip().lower()
            if w in want:
                hit[w] = {k: (row.get(k) or "") for k in
                          ("phonetic", "definition", "translation", "pos", "collins", "oxford", "frq", "bnc", "tag")}
            elif re.fullmatch(r"[a-z]{1,7}", w) and row.get("phonetic") and row.get("translation"):
                try:
                    frq = int(row.get("frq") or 0)
                except ValueError:
                    frq = 0
                if frq > 0:
                    extra.append((frq, w, row))
    # 注意：ECDICT 的 frq 是「词频排名」，数值越小越常用（cat=1775，jadeite=47047）
    extra.sort(key=lambda x: x[0])
    return hit, extra


def fetch_en_api(word):
    """dictionaryapi.dev 免费词典，作为 ECDICT 不可用时的兜底。"""
    url = f"https://api.dictionaryapi.dev/api/v2/entries/en/{urllib.parse.quote(word)}"
    try:
        req = urllib.request.Request(url, headers={"User-Agent": UA})
        with urllib.request.urlopen(req, timeout=20) as r:
            data = json.loads(r.read().decode("utf-8"))
    except Exception:  # noqa: BLE001
        return None
    if not isinstance(data, list) or not data:
        return None
    ph = ""
    audio = ""
    for e in data:
        if e.get("phonetic"):
            ph = ph or e["phonetic"]
        if e.get("audio"):
            audio = audio or e["audio"]
        for p in e.get("phonetics", []) or []:
            if p.get("text"):
                ph = ph or p["text"]
            if p.get("audio"):
                audio = audio or p["audio"]
    defs = []
    for e in data:
        for m in e.get("meanings", []) or []:
            for d in (m.get("definitions") or [])[:1]:
                if d.get("definition"):
                    defs.append(f"{m.get('partOfSpeech','')}. {d['definition']}")
    return {"phonetic": ph, "definition": " ".join(defs[:3]), "translation": "",
            "pos": "", "collins": "", "oxford": "", "frq": "", "bnc": "", "tag": "", "audio": audio}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--skip-english", action="store_true")
    ap.add_argument("--no-download", action="store_true")
    ap.add_argument("--english-api", action="store_true", help="用在线免费词典代替 ECDICT")
    ap.add_argument("--en-limit", type=int, default=80, help="--english-api 模式下最多请求多少个词")
    args = ap.parse_args()
    dl = not args.no_download

    log("=" * 62)
    log("KidStudy 数据采集 POC")
    log("=" * 62)

    # ---------- 汉字 ----------
    ktghz = {}
    p = resolve("ktghz", dl)
    if p:
        ktghz = parse_ktghz(p)
    log(f"[parse] 通用规范汉字表: {len(ktghz)} 字")

    strokes, stroke_src = load_strokes(dl)
    log(f"[parse] 笔顺数据: {len(strokes)} 字（来源 {stroke_src}）")

    xinhua = {}
    p = resolve("xinhua", dl)
    if p:
        try:
            xinhua = parse_xinhua(p)
        except Exception as e:  # noqa: BLE001
            log(f"[warn ] 新华字典解析失败: {e}")
    log(f"[parse] 新华字典单字: {len(xinhua)} 字")

    word_freq, n_tab = parse_thuocl()
    log(f"[parse] 词频词库: {len(word_freq)} 词（来自 {n_tab} 个领域表）")

    by_char = defaultdict(list)
    for w, frq in word_freq.items():
        if len(w) > 3:
            continue
        for c in set(w):
            by_char[c].append((frq, w))
    for c in by_char:
        by_char[c] = [w for _, w in sorted(by_char[c], key=lambda x: -x[0])]

    hanzi = []
    stat = defaultdict(int)
    for c in sorted(ktghz.keys()):
        xh = xinhua.get(c, {})
        st = strokes.get(c) or {}
        rec = {
            "char": c,
            "pinyin": ktghz.get(c, []),
            "radical": xh.get("radicals", ""),
            "stroke_count": xh.get("strokes", ""),
            "pinyin_xh": xh.get("pinyin", ""),
            "explanation": (xh.get("explanation") or "")[:160],
            "has_stroke_anim": bool(st.get("strokes")),
            "stroke_paths": len(st.get("strokes", []) or []),
            "words": by_char.get(c, [])[:8],
        }
        hanzi.append(rec)
        stat["total"] += 1
        for k, v in (("pinyin", rec["pinyin"]), ("radical", rec["radical"]),
                     ("stroke_count", rec["stroke_count"]), ("strokes", rec["has_stroke_anim"]),
                     ("words", rec["words"])):
            if v:
                stat[k] += 1

    with open(os.path.join(RAW, "hanzi.jsonl"), "w", encoding="utf-8") as f:
        for r in hanzi:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")
    log(f"[write] hanzi.jsonl: {len(hanzi)} 条")

    # ---------- 英语 ----------
    en = {}
    if not args.skip_english:
        theme_words = sorted({w.lower() for ws in THEMES.values() for w in ws})
        cache_path = os.path.join(RAW, "en_api_cache.json")
        cache = {}
        if os.path.exists(cache_path):
            try:
                cache = json.load(open(cache_path, encoding="utf-8"))
            except Exception:  # noqa: BLE001
                cache = {}

        if args.english_api:
            theme_words = theme_words[: args.en_limit]
            for i, w in enumerate(theme_words, 1):
                if w in cache:
                    continue
                d = fetch_en_api(w)
                cache[w] = d or {}
                if i % 25 == 0:
                    log(f"[api  ] {i}/{len(theme_words)}")
                    json.dump(cache, open(cache_path, "w", encoding="utf-8"), ensure_ascii=False)
            json.dump(cache, open(cache_path, "w", encoding="utf-8"), ensure_ascii=False)
            hit = {w: d for w, d in cache.items() if d}
            extra = []
            log(f"[parse] 在线词典命中: {len(hit)}/{len(theme_words)}")
        else:
            p = resolve("ecdict", dl)
            if p and os.path.getsize(p) > 10 * 1024 * 1024:
                hit, extra = parse_ecdict(p, theme_words)
                log(f"[parse] ECDICT 主题词命中: {len(hit)}/{len(theme_words)}")
            else:
                hit, extra = {}, []
                log("[skip ] ECDICT 不可用，改用 --english-api 兜底")

        with open(os.path.join(RAW, "words.jsonl"), "w", encoding="utf-8") as f:
            for t, ws in THEMES.items():
                for w in sorted(set(ws)):
                    d = hit.get(w.lower())
                    if not d:
                        continue
                    f.write(json.dumps({"word": w, "topic": t, **d}, ensure_ascii=False) + "\n")
            for frq, w, row in extra[:1500]:
                f.write(json.dumps({"word": w, "topic": "high_frequency",
                                    **{k: (row.get(k) or "") for k in
                                       ("phonetic", "definition", "translation", "pos",
                                        "collins", "oxford", "frq", "bnc", "tag")}},
                                   ensure_ascii=False) + "\n")
        en = {
            "theme_words": len(theme_words),
            "hit": len(hit),
            "with_phonetic": sum(1 for v in hit.values() if v.get("phonetic")),
            "with_translation": sum(1 for v in hit.values() if v.get("translation")),
            "with_definition": sum(1 for v in hit.values() if v.get("definition")),
            "high_freq_pool": len(extra),
        }

    # ---------- 报告 ----------
    def pct(a, b):
        return f"{a:,}/{b:,}（{a*100.0/b:.1f}%）" if b else "—"

    L = ["# 数据采集 POC 报告", "", f"生成时间：{time.strftime('%Y-%m-%d %H:%M:%S')}", "",
         "## 一、汉字（骨架：《通用规范汉字表》8105 字）", "",
         "| 字段 | 覆盖 | 数据源 |", "| --- | --- | --- |"]
    L += [
        f"| 总字数 | {stat['total']:,} | kTGHZ2013（通用规范汉字表） |",
        f"| 拼音 | {pct(stat['pinyin'], stat['total'])} | kTGHZ2013 |",
        f"| 部首 | {pct(stat['radical'], stat['total'])} | chinese-xinhua |",
        f"| 笔画数 | {pct(stat['stroke_count'], stat['total'])} | chinese-xinhua |",
        f"| 笔顺动画 | {pct(stat['strokes'], stat['total'])} | hanzi-writer-data |",
        f"| 组词（≥1 个） | {pct(stat['words'], stat['total'])} | THUOCL 词频 |",
        f"| 组词（≥4 个） | {pct(sum(1 for r in hanzi if len(r['words'])>=4), stat['total'])} | THUOCL 词频 |",
        f"| 组词（≥8 个） | {pct(sum(1 for r in hanzi if len(r['words'])>=8), stat['total'])} | THUOCL 词频 |",
        f"| 词库规模 | {len(word_freq):,} 词 | THUOCL × {n_tab} 领域 |",
    ]
    if en:
        L += ["", "## 二、英语单词", "", "| 指标 | 数值 |", "| --- | --- |"]
        L += [f"| 主题种子词 | {en['theme_words']} |", f"| 词典命中 | {en['hit']} |",
              f"| 含音标 | {en['with_phonetic']} |", f"| 含英文释义 | {en['with_definition']} |",
              f"| 含中文释义 | {en['with_translation']} |", f"| 高频通用词池 | {en['high_freq_pool']:,} |"]
    L += ["", "## 三、产出文件", ""]
    for fn in ("hanzi.jsonl", "words.jsonl"):
        fp = os.path.join(RAW, fn)
        if os.path.exists(fp):
            with open(fp, encoding="utf-8") as f:
                n = sum(1 for _ in f)
            L.append(f"- `var/raw/{fn}`：{n:,} 条，{os.path.getsize(fp)/1024/1024:.2f} MB")
    # 常用度代理指标：组词数量多 + 有部首 + 有笔顺 → 越常用
    sample = sorted(hanzi, key=lambda r: (len(r["words"]), bool(r["radical"]),
                                          bool(r["has_stroke_anim"])), reverse=True)[:15]
    L += ["", "## 四、抽样（按常用度代理指标取前 15 字）", "",
          "| 字 | 拼音 | 部首 | 笔画 | 笔顺 | 组词示例 |", "| --- | --- | --- | --- | --- | --- |"]
    for r in sample:
        L.append(f"| {r['char']} | {'/'.join(r['pinyin'])} | {r['radical']} | {r['stroke_count']} | "
                 f"{'✓' if r['has_stroke_anim'] else '—'} | {'、'.join(r['words'][:4]) or '—'} |")

    out = "\n".join(L) + "\n"
    with open(os.path.join(RAW, "poc_report.md"), "w", encoding="utf-8") as f:
        f.write(out)
    log("\n" + out)
    log("POC 完成。")


if __name__ == "__main__":
    import urllib.parse  # noqa: E402
    sys.exit(main())
