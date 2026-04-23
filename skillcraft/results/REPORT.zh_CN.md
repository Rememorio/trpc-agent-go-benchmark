# SkillCraft 自进化评测报告：三轮全量对照更新

## 1. 范围

本次报告不再沿用旧的单跑结论，而是基于最新的
**三轮全量对照实验**：

- [`full_compare_run1`](full_compare_run1)
- [`full_compare_run2`](full_compare_run2)
- [`full_compare_run3`](full_compare_run3)

聚合脚本和冻结后的分析结果分别在：

- [`tools/aggregate_runs.py`](tools/aggregate_runs.py)
- [`tools/full_compare_analysis.json`](tools/full_compare_analysis.json)

实验配置：

- agent / reviewer 模型：`gpt-4o-mini`
- 任务族：`openmeteo-weather`、`recipe-cookbook-builder`、
  `world-bank-economic-snapshot`
- 每轮任务数：`18`
- `max-tool-iterations`：`24`
- warm-start seed：
  [`tools/clean_skill_seed`](tools/clean_skill_seed)
- prompt overview cap：`8`

## 2. 单轮结果

| Run | Baseline Pass | Evolution Pass | Pass Δ | Baseline E2E Tokens | Evolution E2E Tokens | E2E Δ |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Run 1 | 15 / 18 | 16 / 18 | +1 | 130,308.06 | 138,603.72 | +8,295.66 |
| Run 2 | 16 / 18 | 16 / 18 | 0 | 116,280.94 | 126,157.50 | +9,876.56 |
| Run 3 | 18 / 18 | 17 / 18 | -1 | 263,076.83 | 173,179.17 | -89,897.66 |

结论不是看某一轮谁赢，而是要看三轮整体方差。

## 3. 三轮聚合

| 指标 | Baseline | Evolution | Δ（Evolution − Baseline） |
| --- | ---: | ---: | ---: |
| 平均 pass rate | 90.74% | 90.74% | 0.00pp |
| pass-rate 标准差 | 8.49pp | 3.20pp | - |
| 平均端到端 tokens / task | 169,888.61 | 145,980.13 | -23,908.48 |
| 端到端 tokens 标准差 | 81,007.55 | 24,363.25 | 57,153.77 |
| 平均 agent tokens / task | 169,888.61 | 131,990.93 | -37,897.68 |
| 平均耗时增量 | - | - | +21.14s |

解释：

1. **pass rate 现在整体打平。**
2. **evolution 的波动更小。**
3. **token 平均收益主要来自 Run 3 的 baseline 灾难性 weather loop。**
   在前两轮里，evolution 其实仍然比 baseline 更贵。

## 4. `e1/e2/m1` 聚焦结论

| Task | Baseline Passes | Evolution Passes | Baseline Mean E2E | Evolution Mean E2E | 主要结论 |
| --- | --- | --- | ---: | ---: | --- |
| `openmeteo-weather/e1` | `T,T,T` | `T,T,T` | 489,459.00 | 80,643.67 | 一轮 baseline 爆到 132 万 token，evolution 明显压住了 |
| `openmeteo-weather/e2` | `T,F,T` | `T,T,T` | 514,047.67 | 189,678.00 | evolution 救回 1 次 baseline fail，但仍然没有 `skill_load` |
| `openmeteo-weather/m1` | `T,T,T` | `T,T,T` | 107,458.33 | 215,112.33 | evolution 一直正确，但始终更贵 |

这三条在 evolution arm 中都有：

- `hadAvailableSkills = true`
- `skillToolInvoked = false`
- `loadedSkillNames = []`

所以当前收益仍然不是来自显式 `skill_load`，而是来自
overview 本身。

## 5. 当前主失败簇

现在最稳定的 evolution 失败点不是 weather，而是
`world-bank-economic-snapshot/e2`：

- `evolution` 在 3/3 轮里都 fail 了 `world-bank-economic-snapshot/e2`
- `evolution` 在前两轮还额外 fail 了 `e3`
- `baseline` 的失败点则在 weather、recipe、world-bank 之间漂移

第三轮日志里可以直接看到多次 `worldbank_*` MCP 工具
`request timeout after 1m0s`。说明当前更像是
World Bank 本地工具链稳定性问题，而不是 reviewer parser 或
weather loop 单点问题。

## 6. Skill 库结论

三轮最终 skill 数分别是：

- Run 1：`14`
- Run 2：`13`
- Run 3：`14`

另外一个需要纠正的点是：

- `reconcile.go` 里的 quantified sibling -> generic parent 重写其实已经实现了；
- 日志里已经能看到 `rewrite_quantified_sibling_to_update`；
- 但当 seed 里没有对应 generic API parent 时，
  `Weather Monitor - 3/4/5 Cities with APIs` 这类 count-specific
  skill 仍会保留下来。

## 7. 当前结论

这三轮之后，SkillCraft 上最准确的说法是：

1. evolution 现在还没有证明自己在 pass rate 上稳定优于 baseline；
2. evolution 也还没有证明“显式 skill 复用”真的跑起来了，因为
   `skill_load` 仍是 `0`；
3. 老的 weather 爆炸不再是唯一主线问题，新的主线问题已经转向
   `world-bank` 工具超时和 runtime 稳定性；
4. 下一步最值得继续推的是：
   - 让 agent 真正调用 `skill_load`
   - 查清 world-bank 本地 MCP 工具为什么会频繁 60 秒超时
