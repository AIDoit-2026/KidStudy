# var/ —— 采集产物（不入库）

这个目录的内容**全部由脚本生成**，已在 `.gitignore` 中排除。新克隆仓库时这里是空的，按需重建即可。

## 目录用途

| 子目录 | 内容 | 生成方式 |
| --- | --- | --- |
| `var/raw/` | 下载的数据源文件（拼音表、新华字典、笔顺包、ECDICT、结巴词典、成语、唐诗）+ 解析产物 `hanzi.jsonl` / `words.jsonl` / `stages.json` | `python tools/poc_collect.py` |
| `var/stories/` | 第一故事站爬取：17,250 篇（12 个分类）+ 分析索引 | `python tools/crawl_stories.py` |
| `var/beddy/` | SleepyStory 站爬取：中英双语各约 832 篇（按年龄组分目录）+ 双语配对表 | `python tools/crawl_beddy.py` |

体量约 **300 MB**，其中故事 150 MB、词典类原始数据约 140 MB。

> `var/beddy/` 的 `_index.jsonl` 是追加写入的，采集中途中断的批次不留记录；
> 分析脚本因此**直接扫描磁盘**而非依赖该索引，`_pairs.jsonl` 由分析脚本重建。

## 重建步骤

```bash
# 1. 字库 / 组词 / 英语单词
python tools/poc_collect.py

# 2. 阶段字表与控字组词
python tools/build_stages.py

# 3. 故事采集（约 5 分钟，并发 10）
python tools/crawl_stories.py --workers 10

# 4. 故事分级与适宜性分析
python tools/analyze_stories.py

# 5. 双语故事采集（站点走 Cloudflare，需限流；约 30 分钟）
python tools/crawl_beddy.py --workers 5 --delay 0.35

# 6. 双语分析与对照表生成
python tools/analyze_beddy.py
```

境外数据源（GitHub raw / npm registry）走代理 `http://127.0.0.1:11086` 会快很多：

```bash
export https_proxy=http://127.0.0.1:11086 http_proxy=http://127.0.0.1:11086
```

## 注意

`var/stories/` 下是网络转载内容，无开源协议，仅限家庭内部学习使用。若未来对外提供服务须整批移除。
