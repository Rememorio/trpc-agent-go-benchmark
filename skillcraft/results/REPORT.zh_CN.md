# 基于 SkillCraft 基准的 Agent 自进化评估

## 1. 引言

这份报告记录的是 `trpc-agent-go` 在 SkillCraft 上的**完整实验过程**，
而不是某一轮孤立结果。

SkillCraft 很适合回答自进化是否真的有用，因为它的每个任务族都提供一组
“形状相同、规模递增”的变体（`e1` ... `e3`、`m1` ... `m2`、`h1`）。
如果 agent 真能在前面的简单任务里提炼出可复用 skill，那么后面的复杂
任务理论上就应该更稳、更省，或者两者兼有。

核心问题始终没变：

> **一个会在后台自动抽取 `SKILL.md`、并把它们提供给后续任务复用的 agent，
> 是否真的比“每次都从零开始”的 agent 更强？**

变化的是我们对答案的把握方式。最早的一轮单跑非常乐观；后面的受控复现
告诉我们，结论没有那么简单：收益确实存在，但会受到 reviewer 质量、
运行时暴露方式和任务级波动的共同影响。

因此这份报告把实验分成三个阶段来看：

1. 早期里程碑单跑：证明这件事“能 work”；
2. 后续三轮受控 batch：成为当前 runtime 的主要事实来源；
3. 更强 reviewer（`gpt-5.2`）spot check：验证 reviewer 质量是否是
   当前主要瓶颈之一。

## 2. 实验设置

### 2.1 基准与任务族

| 项目 | 值 |
| --- | --- |
| 基准 | SkillCraft |
| 任务族 | `openmeteo-weather`、`recipe-cookbook-builder`、`world-bank-economic-snapshot` |
| 每族变体 | `e1` / `e2` / `e3` / `m1` / `m2` / `h1` |
| 每轮 full compare 任务数 | 18 |
| 打分 | SkillCraft 官方 `evaluation/main.py` |
| 执行模式 | `compare`（`baseline` 后接 `evolution`） |

三个任务族分别覆盖顺序型 API 编排、结构化内容生成和多实体经济数据汇总，
可以避免结论被单一任务族带偏。

### 2.2 对照配置

| 配置 | 描述 |
| --- | --- |
| **Baseline** | 关闭 `evolution`，每个任务从零开始 |
| **Evolution** | 开启 `evolution`，把前面任务学到的 `SKILL.md` 暴露给后续任务 |

在后续受控实验中，两条臂共享相同的任务集、agent runtime、工具、prompt
和 evaluator；变化项只包括 evolution 是否开启，以及最新一轮里 reviewer
是否升级。

### 2.3 `trpc-agent-go` 的 evolution 实现

`evolution` 是一个**异步学习闭环**。主任务路径不被学习阻塞；真正的
review 在 session 结束后才发生。

1. runner 把 transcript 和 outcome 入队；
2. reviewer 产出 `skills` / `updates` / `deletions`；
3. `reconcile.go` 做确定性后处理，去掉明显重复并把一部分近重复改写成 update；
4. publisher 把结果写成 `SKILL.md`，后续任务就能看到。

运行时还会把 skill summary 暴露给 agent，并允许通过 `skill_load`
显式加载正文。一个贯穿整个实验的事实是：**skill 会被 offered，但显式
skill_load 仍然没有真正发生**。

### 2.4 本报告的证据来源

本报告主要引用 4 组产物：

- 历史里程碑单跑：
  [`multi_family_compare`](multi_family_compare)
- 三轮受控 batch：
  [`full_compare_run1`](full_compare_run1)、
  [`full_compare_run2`](full_compare_run2)、
  [`full_compare_run3`](full_compare_run3)
- 三轮聚合快照：
  [`tools/full_compare_analysis.json`](tools/full_compare_analysis.json)
- 更强 reviewer spot check：
  [`full_compare_reviewer_gpt52_run1`](full_compare_reviewer_gpt52_run1)

文中所有数字都来自这些目录下的 `results.json` 或其聚合结果。

---

## 3. 实验过程演进

### 3.1 Phase A：早期里程碑单跑

最早的里程碑单跑
[`multi_family_compare`](multi_family_compare) 首先证明了这条路
“不是空想”：

| 指标 | Baseline | Evolution | Δ |
| --- | ---: | ---: | ---: |
| 通过率 | 83.33% | 100.00% | +16.67pp |
| 平均分 | 80.46 | 97.68 | +17.22 |
| 平均端到端 tokens / task | 185,590.44 | 128,913.22 | -56,677.21 |
| 平均耗时 | 98.93s | 79.68s | -19.24s |
| 最终 skill 数 | – | 16 | – |

这轮结果很重要，因为它说明：后台抽取 skill + 后续 warm-start 复用，
在 SkillCraft 上确实可能带来显著收益。

### 3.2 Phase B：后续三轮受控复现

后来我们收紧了 runtime：managed-skill prompt 更克制、加入了 token
tailoring、使用冻结的 clean warm-start seed，并用同一套 full-18 配置
连续跑了三轮：

- [`full_compare_run1`](full_compare_run1)
- [`full_compare_run2`](full_compare_run2)
- [`full_compare_run3`](full_compare_run3)

三轮聚合之后，结论就没那么乐观了：

| 指标 | Baseline 均值 | Evolution 均值 | Δ |
| --- | ---: | ---: | ---: |
| 通过率 | 90.74% | 90.74% | 0.00pp |
| pass-rate 标准差 | 8.49pp | 3.20pp | – |
| 平均端到端 tokens / task | 169,888.61 | 145,980.13 | -23,908.48 |
| 端到端 token 标准差 | 81,007.55 | 24,363.25 | – |

这三轮把实验的叙事改写成了现在这版：

- 老的“evolution 明显碾压 baseline”不再成立；
- 当前 runtime 更像是**降低波动**，而不是稳定提升 pass rate；
- 显式 `skill_load` 仍然是 `0%`；
- 主要失败簇转移到了 `world-bank-economic-snapshot/e2` 和本地 MCP timeout。

### 3.3 Phase C：更强 reviewer 的 spot check（`gpt-5.2`）

最新一轮 spot check 保持 agent runtime 不变，只升级 reviewer：

- [`full_compare_reviewer_gpt52_run1`](full_compare_reviewer_gpt52_run1)

结果如下：

| 指标 | Baseline | Evolution | Δ |
| --- | ---: | ---: | ---: |
| 通过率 | 100.00% | 100.00% | 0.00pp |
| 平均分 | 97.19 | 97.13 | -0.05 |
| 平均耗时 | 131.10s | 79.53s | -51.56s |
| 平均端到端 tokens / task | 158,005.67 | 152,715.39 | -5,290.27 |
| 最终 skill 数 | – | 11 | – |
| `skill_load` 调用率 | 0.00% | 0.00% | 0.00pp |

这轮最有价值的信号，不是 pass rate，因为 baseline 也跑到了 18/18；
而是 evolution 的最终 skill 库变干净了：只剩 **11 条**，而且没有再长出
`Weather Monitor - 3/4/5 Cities with APIs` 那批 weather siblings。

---

## 4. 主要结果

### 4.1 现在已经能确定的事实

到目前为止，有三件事已经比较稳了。

1. **自进化在 SkillCraft 上确实可能带来收益。**
   最早的里程碑单跑不是“完全偶然”，它证明了 skill 机制有能力消灭一类
   灾难性循环。
2. **当前 runtime 还不能证明自己稳定提升 pass rate。**
   三轮受控 batch 的均值已经打平。
3. **显式 skill 复用仍然没有跑起来。**
   不论是三轮受控 batch，还是 `gpt-5.2` spot check，`skill_load`
   都还是 `0`。

### 4.2 evolution 今天最像在哪些地方起作用

最强的证据，仍然来自“避免灾难性 loop”。

在三轮受控 batch 里：

- `openmeteo-weather/e1` 两边都是 `3/3` pass，但 baseline 平均端到端
  token 高达 `489,459`，evolution 只有 `80,644`，因为其中一轮 baseline
  发生了灾难性爆炸；
- `openmeteo-weather/e2` 在 baseline 中是 `T,F,T`，在 evolution 中是
  `T,T,T`；
- evolution 明显降低了整体波动，即使均值 pass rate 打平。

在最新的 `gpt-5.2` reviewer spot check 里也能看到类似现象：

- `openmeteo-weather/e1` 下降了 `-508,453` 的端到端 token；
- `world-bank-economic-snapshot/e3` 下降了 `-159,046`；
- 但也存在明显回退，比如 `recipe-cookbook-builder/h1`
  反而多花了 `+348,835` 端到端 token。

所以收益是真实存在的，但分布并不均匀。

### 4.3 现在最关键的缺口

最关键的缺口仍然是：**skill 被看到了，但没有被真正“使用”**。

当前证据很一致：

- skill summary 会出现在 prompt 里；
- skill library 也确实会被 reviewer 产出来；
- 但 agent 并没有显式调用 `skill_load`。

这意味着今天看到的收益，更像是 **catalog exposure / reviewer quality**
带来的间接帮助，而不是成熟的 progressive disclosure 闭环。

### 4.4 reviewer 质量重要，但它还不是全部

`gpt-5.2` 这轮 spot check 已经很清楚地提示我们：reviewer 质量确实是
当前瓶颈之一。

它带来的正向信号包括：

- 最终 skill 数从最近的 13–14 条压回到 **11 条**；
- weather API siblings 在这一轮里消失了；
- evolution 在 pass rate 打平的同时，端到端 token 仍略低于 baseline。

但它也没有解决全部问题：

- `skill_load` 还是没有被调用；
- baseline 本轮本身也已经 18/18，所以 reviewer 升档还没带来 pass 优势；
- 它仍然只是一轮结果，所以更适合作为“library cleanliness 变好”的证据，
  还不适合作为新的总 headline。

---

## 5. 结论

到今天为止，最准确的整体结论应该是：

1. **最早的正向结果是真实的，但不足以单独作为最终结论。**
   它证明了自进化这件事值得继续做。
2. **现在真正的事实来源，是后面的受控复现。**
   在这套更严格的视角下，evolution 目前更像“稳定器”，而不是稳定提升
   pass rate 的增强器。
3. **更强 reviewer 很可能有帮助。**
   `gpt-5.2` 这轮最可贵的地方，不是 pass 多了，而是 skill 库变得更干净、
   更泛化。
4. **项目还没结束。**
   下一步真正决定这条路线能不能默认启用的问题，仍然是：
   为什么 `skill_load` 不用？剩下的 timeout / 长循环怎么压下去？

换句话说，这个实验已经从“这个想法到底能不能 work”推进到了：
“在什么 reviewer / runtime 条件下，它能稳定到值得默认信任？”

---

## 附录

### A. 当前关键产物

| 产物 | 角色 |
| --- | --- |
| [`multi_family_compare`](multi_family_compare) | 历史里程碑单跑 |
| [`full_compare_run1`](full_compare_run1) | 三轮受控 batch，第 1 轮 |
| [`full_compare_run2`](full_compare_run2) | 三轮受控 batch，第 2 轮 |
| [`full_compare_run3`](full_compare_run3) | 三轮受控 batch，第 3 轮 |
| [`tools/full_compare_analysis.json`](tools/full_compare_analysis.json) | 三轮聚合快照 |
| [`full_compare_reviewer_gpt52_run1`](full_compare_reviewer_gpt52_run1) | 更强 reviewer 的 spot check |

### B. 复现最新一轮 reviewer spot check

```bash
cd skillcraft/trpc-agent-go-impl

go run . \
  -skillcraft-root "$SKILLCRAFT_ROOT" \
  -tasks "openmeteo-weather/e1,openmeteo-weather/e2,openmeteo-weather/e3,openmeteo-weather/m1,openmeteo-weather/m2,openmeteo-weather/h1,recipe-cookbook-builder/e1,recipe-cookbook-builder/e2,recipe-cookbook-builder/e3,recipe-cookbook-builder/m1,recipe-cookbook-builder/m2,recipe-cookbook-builder/h1,world-bank-economic-snapshot/e1,world-bank-economic-snapshot/e2,world-bank-economic-snapshot/e3,world-bank-economic-snapshot/m1,world-bank-economic-snapshot/m2,world-bank-economic-snapshot/h1" \
  -mode compare \
  -model gpt-4o-mini \
  -reviewer-model gpt-5.2 \
  -max-tool-iterations 24 \
  -load-skills-from ../results/tools/clean_skill_seed \
  -max-prompt-skills 8 \
  -output ../results/full_compare_reviewer_gpt52_run1
```

### C. 当前阅读规则

如果第一次看这组实验，最简单的导读是：

- 用 [`multi_family_compare`](multi_family_compare) 理解为什么这件事当初
  值得做；
- 用 [`full_compare_run1`](full_compare_run1)、
  [`full_compare_run2`](full_compare_run2)、
  [`full_compare_run3`](full_compare_run3) 和
  [`tools/full_compare_analysis.json`](tools/full_compare_analysis.json)
  理解当前 runtime 的真实状态；
- 用 [`full_compare_reviewer_gpt52_run1`](full_compare_reviewer_gpt52_run1)
  看 reviewer 升档后的正向信号，但暂时不要把它当成新的最终 headline。
