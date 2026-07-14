# 基于 SkillCraft 基准的反思式技能优化评估

## 1. 引言

本实验在两个 SkillCraft 任务族上评测
`trpc-agent-go/evolution/optimization` 的纯 Go 反思式优化器。核心问题是：

> **反思式搜索能否产生一个在未见过的 SkillCraft 任务上仍然有效的技能，
> 而不是只改善搜索过程中见过的 case？**

简短答案是：**这一次还没有。** Optimizer 找到一个在 validation 上更省 token
的 recipe 候选，但这项收益没有泛化到 holdout，因此晋升门禁保留了原技能。

| 实验 | Validation | Holdout | 结论 |
| --- | --- | --- | --- |
| Weather 对照 | 没有候选超过 seed | Seed 与最终选择相同 | 保留 seed |
| Recipe 优化 | 质量相同，token 减少 23.81% | 质量相同，token 增加 60.46% | 拒绝候选 |

这个结果证明搜索和晋升保护链路能够端到端工作：系统可以生成修订、拒绝弱变异、
识别只在 validation 上成立的收益，并避免发布它。但它**还不能**证明 Optimizer
已经能够稳定提升任务质量或效率。

## 2. 实验设置

### 2.1 基准与搜索配置

| 项目 | 取值 |
| --- | --- |
| 基准 | SkillCraft |
| 任务族 | `openmeteo-weather`、`recipe-cookbook-builder` |
| 每个族的变体 | `e1` / `e2` / `e3` / `m1` / `m2` / `h1` |
| Agent / reflection 模型 | `glm52` |
| 评分 | SkillCraft 官方 evaluator 加成本目标 |
| Feedback split | `e1,e2` |
| Validation split | `e3,m1` |
| Holdout split | `m2,h1` |
| 搜索预算 | 4 次 mutation，batch size 2，最多评测 30 个 case |
| 运行限制 | 8192 completion tokens，24 次工具迭代 |

三个 scale split 互不重叠。Reflection 只能看到 feedback case 的输出、评测反馈和
受限 trace；validation 只用于选择候选；候选冻结后才运行 holdout。由于模型服务
不保证严格遵守 optimizer seed，这些结果是配对 benchmark 观察，不是统计显著性结论。

### 2.2 优化机制

Optimizer 重复执行一个带门禁的小型搜索闭环：

1. 用当前技能运行一批 feedback case；
2. 让 reflection 提出一个有边界的修订；
3. 只在同一批 feedback case 上超过 parent 时保留修订；
4. 在 validation 上选择存活候选中的最佳版本；
5. 冻结候选，只有通过 holdout 才允许晋升。

Feedback 负责驱动变异，validation 负责选择，holdout 负责保护晋升。Optimizer
不会从 validation 或 holdout 输出中继续学习。

### 2.3 评估协议

每次评测分别保留官方质量、通过状态、agent tokens、工具调用数、耗时和是否实际
加载技能。用于搜索的标量分数有严格的通过/失败边界；在已经通过的 case 中，官方
质量占主导，token 效率只作为很小的 tie-breaker。

Evaluator 还会把安全、公开的运行证据转成可操作反馈。对于 recipe 任务，它根据
任务声明的工具和最终 JSON，指出缺失的 `category_dishes`、`cuisine_dishes`、
`ingredient_dishes` 字段；它不会读取 evaluator 源码，也不会把 validation 或
holdout case 暴露给 reflection。

## 3. 实验结果

### 3.1 Weather 负对照

旧 weather seed 在所选 validation 和 holdout case 上已经达到官方质量 `1.0`。
三次 mutation 没有通过严格的配对 feedback 接受条件；另一次虽然在 feedback 上
被接受，但 validation 分数低于 seed，因此最终仍选择 seed。

| 指标 | Seed | 已接受候选 / 最终选择 | 决策 |
| --- | ---: | ---: | --- |
| Validation score | 0.999025965 | 0.998781060 | 保留 seed |
| Holdout score | 0.998101740 | 0.998101740 | 因保留 seed 而完全一致 |
| Evaluated cases |  | 22 |  |
| Accepted candidates（含 seed） |  | 2 |  |
| Search agent tokens |  | 2,062,391 |  |
| Reflection tokens |  | 11,763 |  |

这符合负对照预期：候选仅仅“不同”，不足以被选择或晋升。

### 3.2 Recipe 搜索

`description` 和 `when_to_use` 变异没有改善各自的配对 feedback batch，因此被
拒绝；随后一个 `steps` 变异被接受。在 validation 上，它保持官方质量和通过率，
同时减少 token 消耗。

| Validation 指标 | Seed | 选中候选 | Delta |
| --- | ---: | ---: | ---: |
| Scalar score | 0.989455445 | 0.989930445 | +0.000475000 |
| 官方质量 | 0.955 | 0.955 | 0.000 |
| 通过率 | 1.000 | 1.000 | 0.000 |
| Agent tokens | 199,455.5 | 151,955.5 | -47,500.0（-23.81%） |
| 工具调用数 | 20.5 | 19.5 | -1.0 |

但在未见过的 holdout 上，效率结果发生反转：

| Holdout 指标 | Seed | 选中候选 | Delta |
| --- | ---: | ---: | ---: |
| Scalar score | 0.987553435 | 0.986576015 | -0.000977420 |
| 官方质量 | 0.943 | 0.943 | 0.000 |
| 通过率 | 1.000 | 1.000 | 0.000 |
| Agent tokens | 161,656.5 | 259,398.5 | +97,742.0（+60.46%） |
| 工具调用数 | 25.5 | 30.5 | +5.0 |

Holdout delta 低于所配置的非回退阈值，所以候选不能晋升。本轮搜索共评测 26 个
case，接受 3 个候选（含 seed），消耗 3,819,019 agent tokens 和 11,537
reflection tokens。

### 3.3 冻结候选 A/B

为了把搜索过程与最终比较分开，实验关闭搜索（`max_iterations=0`），使用
optimizer seed `29`，独立评测原 seed 和已经冻结的 recipe 候选。

| Split / 指标 | Seed | Candidate | Delta |
| --- | ---: | ---: | ---: |
| Validation `e3` score | 0.992066490 | 0.992477840 | +0.000411350 |
| Validation agent tokens | 166,351.0 | 125,216.0 | -24.73% |
| Holdout `m2,h1` score | 0.987064640 | 0.986568065 | -0.000496575 |
| Holdout agent tokens | 210,536.0 | 260,193.5 | +23.59% |
| Holdout 工具调用数 | 27.0 | 29.5 | +2.5 |
| Holdout 耗时 | 160.36 s | 131.19 s | -18.19% |

两个 split 上的官方质量和通过率都没有变化。候选在 holdout 的墙钟时间更短，
但 token 和工具调用更多，标量分数下降，所以仍不能晋升。

## 4. 讨论

### 4.1 候选为什么失败

该候选过拟合了 validation 中可见的效率信号。新增 checklist 明确了关联菜品字段，
对 `e3` 有帮助；但同一套额外步骤在规模更大的 `m2,h1` 上引入了更多工作，而质量
已经持平，因此新增成本没有带来补偿收益。

这正是把 validation 选择与 holdout 晋升分开的原因：一条在局部有用的指令，可能
随着任务规模变化而失去泛化能力。优化器会同时记录“validation 选中了谁”和
“holdout 为什么拒绝晋升”，而不是把 validation winner 直接包装成可部署改进。

### 4.2 已经证明与尚未证明的部分

本实验验证了三个机制层面的能力：

1. reflection 能把运行反馈转成具体的技能修订；
2. search 能在隔离的 case 上比较候选与 seed；
3. holdout gate 能阻止没有泛化的候选成为 active revision。

但它没有验证更强的产品结论——“反思式优化已经改善 SkillCraft 结果”。要证明这一点，
必须有候选在冻结的 holdout 上经过多次运行仍然稳定获胜。

## 5. 结论与下一步

当前 Optimizer 可以作为实验性的搜索与安全验证框架使用，但本次结果还不足以支持
默认开启自动晋升。下一轮评测应该：

1. 运行多个独立搜索，不依赖单次 mutation 路径；
2. 对冻结候选重复评测并报告均值与方差；
3. 以质量为第一目标，把 token、工具调用和耗时作为独立的次级目标；
4. 扩展到更多任务族，再判断 Optimizer 是否具有普遍收益。

精确数值见 [`evidence.json`](evidence.json)，冻结的候选修订见
[`recipe_candidate.json`](recipe_candidate.json)。
