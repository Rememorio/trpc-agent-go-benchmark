# 基于 SkillCraft 的反思式技能优化评估

## 1. 引言

本报告评估 `trpc-agent-go/evolution/optimization` 的纯 Go 反思式优化器，
并把两个问题明确分开：

1. 离线 reflection 能否找到更好的 skill，并通过 frozen holdout 拒绝不安全
   候选；
2. 已通过 frozen confirmation 的 skill 放回完整异步 evolution loop 后，是否仍然
   有用。

两个问题的答案并不相同：

- **Optimizer 作为有门禁的离线搜索与修复原语是有用的。** 它修复了 reviewer
  产出的 Recipe skill，找到 World Bank 效率候选，也拒绝了一个 validation 上更省
  token、但在 untouched holdout 上失败的 Recipe 候选。
- **当前 optimized overlay 不具备运行时晋升资格。** 在预注册的 5 族、3 seed、
  3 arm 全量回放中，optimized evolution 保持了通过率，并满足质量容忍门槛；但相对
  evolution 的质量变化为 `-0.08pp`，端到端 tokens 增加 `5.79%`，没有达到“有意义
  收益”要求。

**表 1：完整运行时回放（3 轮，每个 arm n = 90）**

| 指标 | Baseline | Evolution | Optimized evolution |
| --- | ---: | ---: | ---: |
| 通过率 | 97.78% | **100.00%** | **100.00%** |
| 官方质量 | 96.06% | **98.24%** | 98.16% |
| 每任务 agent tokens | **311,870** | 325,887 | 344,727 |
| 每任务 reviewer tokens | 0 | 15,168 | 16,089 |
| 每任务端到端 tokens | **311,870** | 341,055 | 360,816 |

Evolution 挽救了两个 baseline 失败，但端到端成本增加 `9.36%`；加入离线 overlay
没有再挽救额外失败，成本又增加 `5.79%`。因此，当前证据支持评审框架 API，但不支持
晋升这组 overlay。

## 2. 实验设计

### 2.1 三阶段证据

实验把发现、确认和运行时使用拆成三个阶段：

1. **Search：** 从五个真实或结构等价的 evolution revision 出发，使用配对 feedback
   case，每次只变更一个 skill 组件；存活候选再进入独立 validation split。
2. **Frozen confirmation：** 固定候选、关闭 reflection，在 validation 与 holdout
   case 上比较 seed 和 candidate，并使用独立随机种子复现。
3. **Operational replay：** 在现有 evolution benchmark 使用的同一套 5 个任务族、
   6 个尺度上比较 `baseline`、`evolution`、`optimized_evolution`。

运行时三个 arm 使用相同的 `gpt-5.2` 模型、temperature 0、8,192 最大响应 tokens、
80 次工具迭代，以及按任务配对的 sampling seed。奇偶 root seed 会反转整个 arm 的执行
顺序。模型端对 sampling seed 的支持仍是 best effort，因此必须保留多轮 root seed。

五个任务族为 `cat-facts-collector`、`openmeteo-weather`、
`pokeapi-pokedex`、`recipe-cookbook-builder` 和
`world-bank-economic-snapshot`；每个族均包含 `e1`、`e2`、`e3`、`m1`、`m2`、
`h1`。

### 2.2 预注册运行时门禁

仓库中的 `skillcraft-5-family-3-arm-v1` protocol 在最终矩阵结果出现前已经固定。
晋升必须同时满足：

- 至少 3 个完整 run，每个 arm 都包含全部 30 个任务；
- 整体与逐族通过率均不回退；
- 整体质量相对 evolution 最多下降 `0.25pp`；
- 每个任务族质量相对 evolution 最多下降 `1.00pp`；
- 至少一个有意义收益：质量 `+0.50pp`，或端到端 tokens `-5%`。

聚合命令还会拒绝重复 root seed、缺失官方评测、额外/缺失任务，以及 arm 间没有配对
的任务 seed。输出包含去除本机路径和模型 transcript 的逐轮机器汇总。

## 3. Search 与 Frozen Confirmation

### 3.1 五族搜索

Optimizer 没有强迫每个任务族都产生不同的 skill。

**表 2：Search 处理结果**

| 任务族 | Search 结果 | 后续动作 |
| --- | --- | --- |
| Cat facts | 保留 seed | Abstain |
| Pokémon | 保留 seed | Abstain |
| Weather | mutation 通过 feedback，但 validation 仍保留 seed | Abstain |
| Recipe | validation 选中效率 mutation | Frozen comparison |
| World Bank | validation 选中效率 mutation | Frozen comparison |

Abstain 本身很重要：完成一次搜索并不等于 mutation 应当发布，validation 与 frozen
holdout 必须是独立决策。

### 3.2 Frozen 结果

有三组关键 frozen 结果。

**表 3：Frozen candidate 结果**

| 候选 | Split / 指标 | Seed skill | Candidate | 决策 |
| --- | --- | ---: | ---: | --- |
| Reviewer 产出 Recipe skill 的修复 | Holdout 质量 | 95.50% | **98.35%** | 接受 |
|  | Holdout 通过率 | 100% | 100% |  |
|  | 每 case agent tokens | 245,317 | **229,211 (-6.57%)** |  |
| Generic Recipe 效率 mutation | Validation 每 case tokens | 167,545 | **150,211 (-10.35%)** | 继续确认 |
|  | Holdout 通过率 | **100%** | 87.50% | **拒绝** |
|  | Holdout 质量 | **95.50%** | 83.41% |  |
| World Bank 效率 mutation | Validation 每 case tokens | **219,299** | 221,716 (+1.10%) | 继续确认 |
|  | Holdout 通过率 / 质量 | 100% / 100% | 100% / 100% | 接受 |
|  | Holdout 每 case tokens | 421,255 | **385,355 (-8.52%)** |  |

早期 Recipe 修复使用两个独立 optimizer seed 和 8 个 holdout pair，得到 4 个质量 win、
4 个 tie、0 个 loss，且通过率没有回退。该候选保存在 `recipe_candidate.json`，也是
运行时全量回放使用的 Recipe overlay。

后续 generic Recipe mutation 是最重要的拒绝案例：它在 validation 上保持质量并降低
token，但一个 untouched `e3` pair 失败；在 untouched 子集上，通过率从 `100%` 降到
`75%`，质量下降 `24.17pp`。即使 pooled holdout tokens 更少，它仍然被丢弃。

World Bank 首轮确认暴露了 optimizer 内部 scalar tie-breaker 与真实部署目标的错位：
官方通过率/质量保持满分，holdout token 也改善，但 scalar 噪声阻止晋升。在采集新的
确认 seed 之前，v2 protocol 将官方通过率与质量设为主安全条件，要求配对主指标零
loss，并以 `5%` holdout token 收益作为效率门槛。新的 `507`、`508` seed 随后复现
`8.52%` token 降低，且通过率与质量均无 loss。

对应机器证据位于
[`evidence.json`](evidence.json)、
[`generic_candidate_frozen_evidence.json`](full_matrix/generic_candidate_frozen_evidence.json)
和
[`worldbank_candidate_frozen_evidence_v2.json`](full_matrix/worldbank_candidate_frozen_evidence_v2.json)。

## 4. 完整运行时回放

### 4.1 逐 Root Seed 结果

**表 4：各 root seed 的三臂结果**

| Root seed | Arm 顺序 | Baseline 通过率 / 质量 / E2E | Evolution 通过率 / 质量 / E2E | Optimized 通过率 / 质量 / E2E | Optimized 相对 evolution |
| ---: | --- | --- | --- | --- | --- |
| 601 | optimized → evolution → baseline | 100% / 98.12% / 343,425 | 100% / 98.21% / 341,727 | 100% / 98.23% / 360,892 | +0.02pp，+5.61% tokens |
| 602 | baseline → evolution → optimized | 96.67% / 95.08% / 271,517 | 100% / 98.21% / 342,637 | 100% / 98.02% / 351,420 | -0.19pp，+2.56% tokens |
| 603 | optimized → evolution → baseline | 96.67% / 94.99% / 320,668 | 100% / 98.29% / 338,802 | 100% / 98.24% / 370,136 | -0.05pp，+9.25% tokens |

Optimized evolution 在三个 root seed 上都更贵。只有 seed `601` 出现很小的质量提升，
而且远低于预注册的 `0.50pp` 有意义收益门槛。

### 4.2 逐族结果

只有 Recipe 与 World Bank 使用了离线 overlay。另三个族是有用的负控制：对这些族，
`evolution` 与 `optimized_evolution` 的起始 skill 相同。

**表 5：逐族 optimized evolution 相对 evolution（每个 arm n = 18）**

| 任务族 | 有 overlay | 通过率变化 | 质量变化 | E2E token 变化 |
| --- | --- | ---: | ---: | ---: |
| Cat facts | 否 | 0.00pp | 0.00pp | -4.34% |
| Weather | 否 | 0.00pp | 0.00pp | +5.78% |
| Pokémon | 否 | 0.00pp | -0.38pp | +14.43% |
| Recipe | 是 | 0.00pp | 0.00pp | -2.68% |
| World Bank | 是 | 0.00pp | 0.00pp | +6.07% |

只看两个 overlay 族，质量持平，端到端 tokens 增加 `1.46%`。Recipe 保留了一点成本
改善，但未达到 `5%` 门槛；World Bank 的 frozen 收益没有迁移到运行时，反而变成成本
增加。没有 overlay 的控制族也出现了双向大幅变化，说明一次运行时轨迹中的小 token
差异不能直接归因于 skill mutation。

### 4.3 Evolution 相对 Baseline

同一轮实验也给出了更新后的 evolution 结果：

- evolution 整体通过率提升 `2.22pp`，质量提升 `2.18pp`；
- 两个 pass win 都来自 Pokémon 产物收尾失败：seed `602` 的 baseline `m1` 和
  seed `603` 的 baseline `m2` 都没有生成 `pokedex_entries.json`，两个 evolution
  arm 则均完成；
- evolution 没有 pass loss，但计入 reviewer 后端到端 tokens 增加 `9.36%`；
- 其他四个任务族的 baseline 通过率已经是 100%，所以可靠性收益集中在 Pokémon，
  不是所有任务族普遍提升。

这组数据不能直接与主 evolution 报告中旧的 `gpt-4o-mini` headline 比较。本实验使用
不同模型、更高且三臂对称的任务预算、entity-serial checkpoint，并完整计入 reviewer
成本。

## 5. Bad Cases 与实验修复

### 5.1 Validation 上省 Token 不代表安全

被拒绝的 Recipe mutation 正是 holdout 应捕获的模式：validation 质量持平且 token
下降，但 untouched scale 丢失最终产物。如果 selector 只看 validation scalar，它会
错误发布该候选。

### 5.2 Frozen 收益不保证 Online 收益

World Bank 在隔离 frozen holdout 上改善，但在完整在线 loop 中成本回退。运行时还
包含顺序 managed-skill 状态、reviewer 调用、更多任务尺度和模型轨迹方差。因此，
frozen confirmation 是必要证据，但不足以支持默认 runtime overlay。

### 5.3 产物收尾失败是真实可靠性问题

两个 baseline Pokémon 失败都有有效中间工作，但没有最终产物或 completion signal。
实验过程中，benchmark 被统一加固为：长任务一次处理一个实体、覆盖写入紧凑的
`working_notes.json`，并允许 finalize-only recovery 读取 notes，但禁止再次调用 domain
API。三个 arm 使用相同修复和相同预算；剩余的两个 baseline 失败被原样保留，没有
选择性重跑。

### 5.4 控制族揭示了残余方差

Cat、Weather、Pokémon 没有 optimized overlay，但 optimized arm 的 token 变化仍在
`-4.34%` 到 `+14.43%` 之间。反转 arm 顺序和配对 task seed 能降低系统偏差，却不能
消除 provider 与模型轨迹方差。三个 root seed 足以执行这里预先固定的门禁，但不足以
宣称模型无关的统计显著性。

## 6. 框架与 API 判断

Benchmark 不需要 Python GEPA bridge，也不需要公开 optimizer 内部结构。Adapter 只
提供公开的 task cases、`Evaluator`、`Dataset`、`Request`、reflection model 和 options，
然后调用 `New` 与 `Optimize`。Candidate graph、Pareto bookkeeping、mutation 解析、
实验存储和晋升逻辑都保持在包内部。

主库 API 已具备评审条件，原因是：

- optimization 是 opt-in 的离线流程，不会直接修改 live skill；
- evaluator 由应用提供，各业务可以保留原生质量和成本目标；
- validation 与 holdout 是显式 dataset contract；
- metric calls、iterations、time 和 reflection batch size 都有界；
- revision submission 通过小型 `RevisionSubmitter` 接口可选接入，成功提交仍进入审批，
  不会静默变成 active；
- 即使调用方晋升策略拒绝候选，框架仍能返回可分析的优化结果。

因此，主库 PR 可以作为 API primitive 进入代码评审；这与两个 benchmark candidate
是否应部署是两个独立结论。

## 7. 结论与下一步

当前 benchmark 支持一个有边界的结论：

> 反思式优化对离线 skill 修复、候选搜索和基于证据的拒绝是有用的；它尚未证明在
> SkillCraft 五个任务族上形成普遍有益的运行时 overlay。

基于当前证据，正确动作是：

1. 评审纯 Go optimizer API；
2. 不把当前 Recipe 与 World Bank overlay 设为默认晋升结果；
3. 保留已接受 candidate 作为研究产物；
4. 如果仍要追求运行时收益，应从新的候选和新的 operational root seed 开始，而不是
   继续针对 `601`--`603` 调参。

精确聚合数值、逐轮摘要、逐族指标、配对结果和门禁判定见
[`full_matrix_evidence.json`](full_matrix_evidence.json)。
