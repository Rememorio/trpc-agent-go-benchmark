# 基于 SkillCraft 的反思式技能优化评估

## 1. 引言

本报告评估 `trpc-agent-go/evolution/optimization` 中的纯 Go 反思式优化器，
并把三个容易混淆的问题分开：

1. 离线 reflection 能否发现更好的 skill，并通过 frozen holdout 拒绝不安全候选；
2. 已接受的 skill 放回完整异步 evolution loop，而且继续使用生成它的同一模型时，
   是否仍然有用；
3. 即使某个具体 candidate 不值得部署，框架 API 本身是否仍是合理的基础能力。

最终同模型 GLM-5.2 回放给出了有边界的正面结论：

- **Recipe candidate 有用，可以在本次运行时晋升。** 18 个配对任务全部通过，
  官方质量提升 `0.32pp`，端到端 tokens 降低 `14.75%`；三个 root seed 的 token
  方向全部为正收益。
- **World Bank candidate 不应晋升。** 两个 arm 的通过率和质量都保持 100%，但端到端
  tokens 增加 `3.29%`，且三个 root seed 都变贵。
- **全局 gate 不足以完成因果归因。** 预注册门禁在机械意义上通过了，但 `+1.25pp`
  质量收益主要来自没有使用离线 overlay 的 Pokémon 偶发收尾失败。只聚合真正发生变化
  的两个任务族时，overlay 的端到端 tokens 降低 `6.77%`、质量提升 `0.16pp`；进一步
  分解可见收益由 Recipe 产生，World Bank 是负贡献。
- **Optimizer API 已合入主库，并可从官方上游直接使用。** Benchmark 现在直接依赖
  包含 optimizer 的 `trpc-agent-go` main revision，不再使用 fork replace。实验结论
  不变：optimizer 找到了稳定收益，拒绝了一个存在安全回退的 validation winner，也
  允许一个 frozen winner 在完整在线证据无法复现收益时被再次拒绝。

**表 1：同模型 GLM-5.2 完整运行时回放（3 轮，每个 arm n = 90）**

| 指标 | Baseline | Evolution | Optimized evolution |
| --- | ---: | ---: | ---: |
| 通过率 | 97.78% | 97.78% | **98.89%** |
| 官方质量 | 95.98% | 95.96% | **97.21%** |
| 每任务 agent tokens | **305,240** | 337,288 | 346,978 |
| 每任务 reviewer tokens | 0 | 15,683 | 15,390 |
| 每任务端到端 tokens | **305,240** | 352,971 | 362,368 |

全局 optimized arm 相对 evolution 的通过率提升 `1.11pp`、质量提升 `1.25pp`，
但端到端成本增加 `2.66%`。这些总数适合检查固定门禁；candidate 是否应晋升，则必须
以下面的逐族结果为依据。

## 2. 实验设计

### 2.1 三阶段证据链

实验把发现、确认和运行时使用拆成三个阶段：

1. **Search：** 从五个真实或结构等价的 evolution revision 出发，使用配对 feedback
   case，每次只修改一个 skill 组件；存活候选再进入独立 validation split。
2. **Frozen confirmation：** 固定候选、关闭 reflection，在 validation 和 untouched
   holdout case 上比较 seed 与 candidate，并使用独立随机种子复现。
3. **Operational replay：** 在现有 evolution benchmark 的同一套 5 个任务族、6 个尺度
   上比较 `baseline`、`evolution`、`optimized_evolution`。

Search 可以 abstain，frozen confirmation 可以拒绝 search winner，operational replay
也可以拒绝 frozen winner。这是设计目标：每个阶段都在回答比前一阶段更严格的部署问题。

### 2.2 模型、任务与配对

Search、frozen confirmation 和最终运行时回放都通过 model ID `glm52` 请求自部署的
GLM-5.2 路由。最终矩阵显式使用
`-model glm52 -reviewer-model glm52`、temperature 0、最大响应 8,192 tokens 和
80 次工具迭代。聚合器会校验这些配置，而不是从 endpoint 设置猜测模型。

本轮使用全新的 root seed `701`、`702`、`703`。同一 run 中，三个 arm 使用相同的
task-specific sampling seed。奇偶 root seed 反转整个 arm 的执行顺序，但不改变各 arm
内部的在线学习顺序：

- `701`：optimized evolution → evolution → baseline；
- `702`：baseline → evolution → optimized evolution；
- `703`：optimized evolution → evolution → baseline。

五个任务族为 `cat-facts-collector`、`openmeteo-weather`、
`pokeapi-pokedex`、`recipe-cookbook-builder` 和
`world-bank-economic-snapshot`。每族均包含 `e1`、`e2`、`e3`、`m1`、`m2`、`h1`，
即每个 arm 每轮 30 个任务、三轮 90 个任务，总计 270 个 arm-case。

更早的矩阵使用 root seed `601`--`603`，但在 GLM-5.2 发现 candidate 之后，运行时
请求了 GPT-5.2。路由探针已确认 `gpt-5.2` 与 `glm52` 是两个不同路由。该矩阵仍是
有价值的跨模型可迁移性测试，将在第 5 节单独报告，不与同模型结果混合聚合。

### 2.3 预注册运行时门禁

仓库中的 `skillcraft-5-family-3-arm-v1` protocol 在两个最终矩阵出结果前已经固定。
机械意义上的晋升资格要求同时满足：

- 至少 3 个完整 run，每个 arm 都包含全部 30 个任务；
- 整体与逐族通过率均不回退；
- 整体质量相对 evolution 最多下降 `0.25pp`；
- 每个任务族质量相对 evolution 最多下降 `1.00pp`；
- 至少一个有意义收益：质量 `+0.50pp`，或端到端 tokens `-5%`。

聚合命令还会拒绝重复 root seed、缺失官方评测、额外或缺失任务、arm 间没有配对的
task seed，以及实验配置漂移。输出经过清理，不包含本机路径、模型 transcript 或凭据。

该 gate 是必要的聚合安全检查，不是因果归因方法。只有部分任务族使用 overlay 时，
晋升还必须证明收益确实发生在这些任务族，并且能跨 root seed 保持方向稳定。

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

Abstain 本身很重要：完成一次搜索并不等于 mutation 应当发布。

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

已接受的 Recipe 修复使用两个独立 optimizer seed 和 8 个 holdout pair，得到 4 个质量
win、4 个 tie、0 个 loss，且通过率没有回退。该候选保存在
`recipe_candidate.json`，也是两个运行时矩阵使用的 Recipe overlay。

后续 generic Recipe mutation 是最重要的拒绝案例：它在 validation 上保持质量并降低
token，但一个 untouched `e3` pair 失败；在 untouched 子集上，通过率从 100% 降到
75%，质量下降 `24.17pp`。即使 pooled holdout tokens 更低，它仍被丢弃。

World Bank 首轮确认暴露了 scalar tie-breaker 与部署目标的错位。在采集新确认 seed
之前，v2 protocol 将官方通过率和质量设为主安全条件，要求配对主指标零 loss，并以
5% holdout token 收益作为效率门槛。新 seed `507`、`508` 随后复现 `8.52%` token
降低且没有通过率或质量 loss。完整运行时回放再验证这一隔离收益能否存活于在线 loop。

## 4. 同模型 GLM-5.2 运行时回放

### 4.1 逐 Root Seed 结果

**表 4：各 root seed 的三臂结果**

| Root seed | Arm 顺序 | Baseline 通过率 / 质量 / E2E | Evolution 通过率 / 质量 / E2E | Optimized 通过率 / 质量 / E2E | Optimized 相对 evolution |
| ---: | --- | --- | --- | --- | --- |
| 701 | optimized → evolution → baseline | 100% / 98.15% / 306,475 | 96.67% / 94.97% / 339,270 | 100% / 98.23% / 355,400 | 通过率 +3.33pp，质量 +3.26pp，tokens +4.75% |
| 702 | baseline → evolution → optimized | 96.67% / 94.92% / 335,723 | 100% / 97.93% / 362,645 | 96.67% / 94.90% / 368,126 | 通过率 -3.33pp，质量 -3.03pp，tokens +1.51% |
| 703 | optimized → evolution → baseline | 96.67% / 94.86% / 273,524 | 96.67% / 94.98% / 356,998 | 100% / 98.51% / 363,578 | 通过率 +3.33pp，质量 +3.53pp，tokens +1.84% |

全局结果噪声较大，因为 evolution 与 optimized 之间三个非 tie 的通过率差异都来自没有
使用 overlay 的任务族中的孤立失败。配对结果为 2 个 pass win、87 个 tie、1 个 loss；
质量为 7 个 win、80 个 tie、3 个 loss。预注册聚合门禁通过，但具体 candidate 的有效性
必须由下一节判断。

### 4.2 逐族因果归因

只有 Recipe 与 World Bank 使用离线 overlay。Cat、Weather、Pokémon 是负控制，因为
这些任务族的 evolution 与 optimized evolution 使用相同起始 skill。

**表 5：逐族 optimized evolution 相对 evolution（每个 arm n = 18）**

| 任务族 | 有 overlay | 通过率变化 | 质量变化 | Agent token 变化 | E2E token 变化 |
| --- | --- | ---: | ---: | ---: | ---: |
| Cat facts | 否 | 0.00pp | 0.00pp | -16.08% | -15.11% |
| Weather | 否 | 0.00pp | 0.00pp | +5.39% | +5.22% |
| Pokémon | 否 | +5.55pp | +5.95pp | +22.93% | +22.02% |
| Recipe | **是** | 0.00pp | **+0.32pp** | **-14.86%** | **-14.75%** |
| World Bank | **是** | 0.00pp | 0.00pp | +3.27% | +3.29% |

全局质量 win 主要由 Pokémon 贡献，但两个 arm 在这里使用相同的 warm-start skill，
只是 evolution arm 偶发丢失了两个最终产物，因此不能把该收益归因于 optimized
candidate。相反，Recipe 和 World Bank 才是在相同在线机制下直接比较发生变化与未变化
的 skill library。

只汇总两个发生变化的任务族时，两个 arm 的通过率均为 100%，质量变化 `+0.16pp`，
agent tokens 变化 `-6.80%`，端到端 tokens 变化 `-6.77%`。这个 attribution-aware
范围独立跨过了 5% 有意义收益门槛，说明 optimized overlay 作为实验单元确实有用；
但两个 revision 并非同样有价值：全部节省由 Recipe 提供，World Bank 反而让 bundle
比 Recipe 单独部署更低效。生产晋升单位是单个 skill revision，所以更精确的决策仍是
晋升 Recipe、拒绝 World Bank。

### 4.3 Recipe：可重复的运行时收益

**表 6：Recipe optimized evolution 相对 evolution**

| Root seed | 通过率变化 | 质量变化 | Agent token 变化 | E2E token 变化 |
| ---: | ---: | ---: | ---: | ---: |
| 701 | 0.00pp | 0.00pp | -6.69% | -6.61% |
| 702 | 0.00pp | 0.00pp | -25.28% | -24.25% |
| 703 | 0.00pp | +0.95pp | -11.80% | -12.52% |
| **聚合** | **0.00pp** | **+0.32pp** | **-14.86%** | **-14.75%** |

Recipe 两个 arm 的 18 个任务全部通过。收益不是单轮离群点：三个 root seed 都降低
tokens，而且每轮都超过 5% 有意义收益门槛。seed `703` 的 optimized `h1` reviewer
发生一次 timeout，但该 seed 的 agent-only tokens 仍降低 `11.80%`，聚合降低
`14.86%`，所以晋升结论不依赖遗漏 reviewer 成本。

这是本实验最强的证据：optimizer 修复 skill，frozen confirmation 接受它，新的同模型
在线矩阵又在不产生安全回退的前提下复现了更大的效率收益。

### 4.4 World Bank：Frozen Winner 被运行时拒绝

World Bank 两个 arm 的 18 个任务也全部通过，质量同为 100%；但 seed `701`、`702`、
`703` 的端到端 tokens 分别变化 `+5.63%`、`+2.55%`、`+1.70%`，聚合增加
`3.29%`。seed `702/e3` 的 evolution reviewer 结果缺失也不会反转结论：agent-only
tokens 聚合仍增加 `3.27%`。

因此，隔离 frozen holdout 上的收益没有存活于顺序 managed-skill 状态和完整在线 loop。
两层门禁都按预期工作：frozen confirmation 允许合理候选继续，operational evidence
则阻止它部署。

### 4.5 Evolution 相对 Baseline

在 GLM-5.2 下，evolution 和 baseline 都通过 88/90 个任务。Evolution 质量变化
`-0.02pp`，端到端 tokens 增加 `15.64%`；配对结果包含 2 个 pass win 与 2 个 loss，
以及 5 个质量 win 与 5 个 loss。因此，这组矩阵不能证明未叠加离线优化的 online
evolution arm 在该模型和预算下普遍有益。

该结论不会替代旧 online-evolution 报告。旧实验在不同模型和预算下显示，它能避免少数
灾难性循环。两个实验回答不同运行时问题，不能混合聚合。

## 5. 早期 GPT-5.2 跨模型回放

同模型矩阵之前，同一组 GLM 产生的 overlay 已在独立 GPT-5.2 路由上使用 root seed
`601`--`603` 回放。

**表 7：GPT-5.2 完整运行时回放（3 轮，每个 arm n = 90）**

| 指标 | Baseline | Evolution | Optimized evolution |
| --- | ---: | ---: | ---: |
| 通过率 | 97.78% | **100.00%** | **100.00%** |
| 官方质量 | 96.06% | **98.24%** | 98.16% |
| 每任务端到端 tokens | **311,870** | 341,055 | 360,816 |

Optimized evolution 相对 evolution 的质量变化为 `-0.08pp`，端到端 tokens 增加
`5.79%`，没有通过有意义收益门槛。Recipe 端到端只节省 `2.68%`，World Bank 则
增加 `6.07%`。这个负结果仍然有价值：它说明 GLM-5.2 上的 skill 改进不会自动迁移
到 GPT-5.2，也说明必须运行同模型矩阵，而不能给旧结果换一个标签。

## 6. Bad Cases 与局限

### 6.1 五个收尾失败被原样保留

同模型矩阵共有五个失败 arm-case，全部不在两个 overlay 任务族中：

- seed `701`，evolution Pokémon `m2`：没有最终产物；
- seed `702`，baseline Cat `h1`：最终响应输出了文本形式的 tool call，却没有真正执行；
- seed `702`，optimized Pokémon `e2`：同类文本 tool-call 失败；
- seed `703`，evolution Pokémon `m2`：长工具输出和重复恢复后仍未生成产物；
- seed `703`，baseline Pokémon `h1`：在 finalization 前停止。

两个 optimized/evolution Pokémon arm 使用相同起始 skill，因此它们的 pass 差异是运行时
方差，不是 overlay 证据。没有任何失败 case 被选择性重跑。

### 6.2 负控制揭示残余轨迹方差

无 overlay 任务族的 token 变化从 Cat 的 `-15.11%` 到 Pokémon 的 `+22.02%`。
Cat 在 seed `702` 的方向甚至与另两个 seed 相反。反转 arm 顺序和配对 task seed 可以
降低系统性偏差，但不能让 provider sampling 完全确定。三个 seed 满足固定 protocol，
并不等于获得了模型无关的统计显著性。

### 6.3 Reviewer 隔离按预期工作

三次 reviewer timeout 没有使任务结果无效：seed `702` 的 evolution World Bank `e3`，
以及 seed `703` 的 optimized Pokémon `m2` 与 Recipe `h1`。同时报告 agent-only 和
端到端敏感性，可以确认这些缺失 reviewer tokens 既没有制造 Recipe 的 win，也没有制造
World Bank 的 loss。

### 6.4 Tool Response 鲁棒性仍是 Benchmark 问题

多个 arm 都出现过可恢复的 filesystem MCP response 错误。两个文本 tool-call 失败还
说明 OpenAI-compatible endpoint 可能把预期工具调用编码为普通文本。Pokémon 尤其容易
受到大工具响应和长上下文恢复循环影响。这些是后续值得加固的 harness 能力，但在观察
矩阵结果后修改会破坏 frozen protocol，所以本轮没有针对性调整。

## 7. 框架与 API 判断

本报告评估的框架实现已经通过 `trpc-group/trpc-agent-go#2204` 合入。Benchmark 当前
解析到官方上游 main revision `99a8667aa8ad`，不再需要 fork `replace` 或相邻的本地
checkout。这是集成状态更新，不是重新运行实验：模型结果和 evidence artifact 仍然
如实记录实际产生它们的 revision 与运行时配置。

Benchmark 不需要 Python GEPA bridge，也不需要公开 optimizer 内部结构。Adapter 只
提供公开 task cases、`Evaluator`、`Dataset`、`Request`、reflection model 和 options，
然后调用 `NewGEPA`，并使用返回的单方法 `Optimizer` 接口。具体 GEPA 类型、candidate
graph、Pareto bookkeeping 和 mutation 解析都保持私有；统一的内部生命周期负责 seed
与 holdout 评估、预算、实验记录、晋升及可选 revision submission，因此后续内置搜索
算法无需复制这些控制逻辑。

已合入的主库 API 仍然保持清晰、合理的框架边界，原因是：

- optimization 是 opt-in 离线流程，不会直接修改 live skill；
- 算法由类型明确的 constructor 选择，而不是字符串 registry；每份结果也会记录实际
  使用的实现；
- evaluator 由应用提供，各业务可以保留原生质量和成本目标；
- validation 与 holdout 是显式 dataset contract；
- metric calls、iterations、time 和 reflection batch size 都有界；
- revision submission 通过小型 `RevisionSubmitter` 接口可选接入，成功提交仍进入审批，
  不会静默变成 active；
- search 可以 abstain，promotion policy 仍由应用拥有；
- 即使调用方正确拒绝 candidate，框架仍返回可分析的完整 evidence。

本次实验也验证了预期的扩展边界：optimizer contract 不依赖 GEPA 内部结构，benchmark
则在外部表达 SkillCraft 专属评测、token 计费、frozen holdout policy 和部署门禁。

## 8. 结论与下一步

完整证据支持以下有边界的结论：

> 纯 Go 反思式 optimizer 在 SkillCraft 上是有用的。它找到一个 Recipe skill，
> 在同模型 GLM-5.2 在线回放中保持 100% 通过率，并在三个方向一致的 root seed 上将
> 端到端 tokens 聚合降低 14.75%。它还拒绝了一个不安全 Recipe mutation，并在
> World Bank frozen winner 的运行时收益无法复现时阻止其晋升。

正确的后续动作是：

1. Benchmark 继续依赖官方上游 module，不再使用 fork replace；
2. 针对本次 GLM-5.2 运行时晋升或打包已接受的 Recipe candidate；
3. 不晋升 World Bank candidate；combined experimental overlay 在 changed-family
   范围为正收益，但只部署 Recipe 是严格更优的选择；
4. 保留 GPT-5.2 结果作为负面的跨模型可迁移性证据；
5. 把更广泛的模型可迁移性和 Pokémon tool-response 鲁棒性作为后续工作，不把它们
   作为采用已合入 API 的阻塞项。

同模型精确聚合值、逐轮摘要、逐族指标、配对结果和预注册门禁见
[`glm_full_matrix_evidence.json`](glm_full_matrix_evidence.json)。早期跨模型聚合仍保存在
[`full_matrix_evidence.json`](full_matrix_evidence.json)。
