# 基于 SkillCraft 的反思式技能优化评估

## 1. 引言

本实验在 SkillCraft 的 `recipe-cookbook-builder` 任务族上评测
`trpc-agent-go/evolution/optimization` 的纯 Go 反思式优化器。它关注的问题比
主 evolution 报告更具体：

> **Optimizer 能否修复现有 evolution service 真实产出的技能，冻结候选，并在
> 后续配对运行中带来收益？**

在这个场景中，答案是：**可以。** Optimizer 从 evaluator 反馈中恢复了缺失的产物
约束，又针对 hard case 学到了一条防止昂贵重复调用的 guardrail。最终候选在两组
独立 frozen comparison 中都通过 validation 和 holdout gate；8 个 holdout 配对为
4 胜、4 平、0 负，没有通过率回退。

**表 1：合并后的 frozen holdout 结果（2 个 optimizer seed，8 个配对 case）**

| 指标 | 现有技能 | 优化后技能 | 变化 |
| --- | ---: | ---: | ---: |
| 官方质量 | 95.50% | **98.35%** | **+2.85pp** |
| 通过率 | 100% | 100% | 0.00pp |
| 每 case agent tokens | 245,317 | **229,211** | **-6.57%** |
| 每 case 工具调用数 | 25.13 | **24.13** | **-3.98%** |
| 每 case 耗时 | 137.61 秒 | **133.59 秒** | **-2.92%** |
| 标量分数 | 0.988997 | **0.994573** | **+0.005576** |

这证明了一个有边界的产品结论：反思式优化可以作为 legacy skill 的离线修复与整合
步骤。它还不能证明所有任务族都会提升，也不支持在没有 validation/holdout 策略的
情况下默认自动晋升。

## 2. 实验设置

### 2.1 Benchmark 与 Seed 来源

| 项目 | 取值 |
| --- | --- |
| Benchmark | SkillCraft |
| 任务族 | `recipe-cookbook-builder` |
| Agent / reflection 模型 | 通过 OpenAI-compatible endpoint 调用 `glm52` |
| 初始技能 | 仓库中已有 reviewer session skill 的等价 `SkillSpec` 转换 |
| 评分 | SkillCraft 官方质量，并独立保留成本目标 |
| 重复次数 | 每个任务尺度运行 2 组 baseline/candidate 配对 |
| 评测 temperature | 0 |
| 最大工具迭代 | 80 |

初始技能不是为实验故意削弱的 prompt。
[`recipe_session_legacy.json`](../../seeds/recipe_session_legacy.json) 原样保留了现有
evolution service 产物的名称、描述、适用场景、步骤和注意事项。冻结后的结果位于
[`recipe_candidate.json`](recipe_candidate.json)。

### 2.2 优化机制

Optimizer 执行一个规模小且可审计的搜索闭环：

1. 在一批配对 feedback case 上运行 parent skill；
2. reflection 只能看到该批次的输出、evaluator 反馈和受限 trace；
3. 每次只变更一个字段，且只有在同一批配对 case 上超过 parent 才接受；
4. 在隔离的 validation split 上选择存活候选；
5. 冻结最终候选，再与原技能比较。

最终候选累计学到了四类通用 guardrail：

- 保留产物契约要求的关联菜品精确字段；
- 根据当前任务声明选择 domain tools，不把某个 case 的 endpoint 列表写死到技能里；
- 写入目标产物并明确发送完成信号；
- 相同参数的工具调用复用已有结果，不重复请求。

框架保存候选谱系、feedback 决策、多目标指标和晋升原因。Reflection 看不到
validation/holdout 输出；frozen comparison 也不会调用 reflection 或改写输入。

### 2.3 配对与数据隔离

每个 baseline/candidate 配对使用相同的确定性 case seed，并交替执行顺序，降低固定
先后顺序造成的偏差。官方质量、通过状态、token、工具调用、耗时和技能加载分别记录；
标量分数对 pass/fail 设置硬边界，以官方质量为主，token 只作为很小的 tie-breaker。

本实验是一次真实的“发现问题—修复—回归”生命周期，因此没有把最终所有 case 都
包装成从未见过：

- `e1,m1` 用于初始 legacy skill 修复；
- `e2,m2` 用于候选选择，之后以新 seed 执行回归；
- `e3` 从未进入 reflection，始终是未见任务尺度 holdout；
- 首轮 frozen `h1` 暴露了大产物写入不完整的问题，因此后续明确把 `h1` 转为
  feedback，并用未见过的 case seed 做 hard-case 回归。

所以 `e3` 验证尺度泛化，最终 `h1` 验证已发现的故障是否真正修好。若要提出更广泛
的泛化结论，仍需新的任务族或新的 hard scale。

## 3. 搜索与修复结果

### 3.1 修复现有 Legacy Skill

初始搜索使用 `e1,m1` 作为 feedback，`e2,m2` 作为 validation。4 次 mutation 中
有 2 次通过严格的配对 feedback 比较。选中候选不再把技能限定为固定菜品数量，并
学到了产物字段、动态任务工具契约和完成信号三类 guardrail。

**表 2：初始 validation 结果**

| 指标 | Legacy skill | 选中候选 | 变化 |
| --- | ---: | ---: | ---: |
| 官方质量 | 95.50% | **99.175%** | **+3.675pp** |
| 通过率 | 100% | 100% | 0.00pp |
| 每 case agent tokens | 137,331 | 193,249 | +40.72% |
| 标量分数 | 0.990077 | **0.996500** | **+0.006423** |

这是一次质量换成本的选择，不是“免费”的效率提升。搜索共评测 44 个 case，保留
3 个候选（含 seed），消耗 7,200,446 agent tokens 和 22,547 reflection tokens。

### 3.2 Bad Case 与针对性修复

首轮多 seed frozen 检查发现了一个 hard-case 回退：某次 `h1` 运行试图写入过大的
结构化结果，最终没有产出完整文件并导致任务失败。这个问题没有被平均值掩盖，而是
作为开发失败进入下一轮修复。

修复搜索把 `h1` 作为 feedback，`e2,m2` 作为 validation。第一个提案被拒绝；最终
接受的 mutation 保留了所有已有 guardrail，只新增一条规则：不要用相同参数重复
调用同一工具。在配对 hard feedback 上，它保持 100% 质量和通过率，同时减少
19.33% tokens；在 validation 上，质量从 98.575% 提升到 100%，token 增加
14.39%。质量优先的 selector 因此选择它进入 frozen 测试。

这个 failure 也反向改进了主库 reflection prompt：要求采用最小充分变更；除非证据
直接矛盾，否则保留累计 guardrail；不把单个 case 的 endpoint 名称泛化成全局规则；
当长输出接近响应预算时，优先生成紧凑但契约完整的产物。

## 4. Frozen Confirmation

随后把选中技能固定为输入，关闭 search iteration，比较过程无法再修改它。两个独立
optimizer seed 分别运行相同的 `e2,m2` validation 与 `e3,h1` holdout，每个尺度
包含 2 个配对重复。

**表 3：各 optimizer seed 的 frozen 结果**

| Seed | Split | Legacy 质量 | 优化后质量 | Legacy tokens | 优化后 tokens | Gate |
| ---: | --- | ---: | ---: | ---: | ---: | --- |
| 191 | Validation | 95.50% | **96.925%** | 137,977 | 182,186 | 通过 |
| 191 | Holdout | 95.50% | **98.35%** | 233,337 | **223,487** | 通过 |
| 197 | Validation | 95.50% | **99.175%** | 161,380 | 169,395 | 通过 |
| 197 | Holdout | 95.50% | **98.35%** | 257,297 | **234,935** | 通过 |

两个 seed 都独立得到可晋升结论。合并后的 validation 质量为
`95.50% -> 98.05%`，token 增加 `17.45%`，符合当前“质量优先”的目标设置。合并后的
frozen holdout 则同时提升质量，并降低 token、工具调用和墙钟时间。

逐 case 看，4 个 `e3` 配对质量全部持平；4 个 `h1` 配对全部从 `0.943` 提升到
`1.000`。没有质量下降，也没有通过率回退。候选在个别运行中可能多消耗 token，但
配对聚合结果为正，而且没有再次触发质量失败。

## 5. 补充对照实验

另一组探索性搜索从仓库内已经较好的通用 recipe seed 出发。其 validation 质量和
通过率不变，token 减少 13.58%，耗时减少 41.71%。由于这组实验没有 frozen
holdout，它只能说明 optimizer 找到了一个效率候选，不能算作上面的晋升证据。

这个对照说明：当质量已经饱和时，同一个 optimizer 也能搜索执行成本，但不能把只在
validation 上成立的结果包装成可部署收益。

## 6. Benchmark 已经证明什么

当前实验支持以下结论：

1. evaluator 发现可以转成有边界、可泛化的技能 mutation；
2. 起点可以是 reviewer 的真实 legacy 产物，而不是人工为 benchmark 编写的 prompt；
3. 无效 mutation 会被拒绝，frozen hard case 失败也会进入下一轮修复，而不会被
   误晋升；
4. 最终候选在配对 holdout 上提升质量且不降低通过率，并在两个独立 seed 上复现；
5. benchmark adapter 只依赖主库的公开 API，候选图、reflection 协议、Pareto 逻辑
   和存储实现都无需导出。

它尚未证明所有任务族都会提升，也没有为所有模型 endpoint 给出统计显著性结论，更
不支持取消门禁的在线自动晋升。这些结论需要更多任务族、更多独立运行，以及调用方
自己的部署晋升策略。

## 7. 结论

纯 Go Optimizer 对当前测试工作流是有用的，并已具备进入代码评审的条件。它目前最
适合做 opt-in 的离线技能修复：从真实 session evidence 出发搜索，冻结候选，再由
validation 与 holdout 决定调用方是否晋升。自动晋升仍是调用方策略，不是 optimizer
内部隐式副作用。

精确机器可读数值见 [`evidence.json`](evidence.json)。
