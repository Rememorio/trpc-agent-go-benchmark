# 基于 SkillCraft 基准的 Agent 自进化评估

## 1. 引言

本报告使用 **SkillCraft** 基准评估 **trpc-agent-go** 的 agent 自进化
（evolution）能力。报告涵盖两个配置：

- **Baseline**：关闭 evolution，每个任务从零开始。
- **Evolution**：打开 evolution，后台异步抽取的 `SKILL.md` 技能文件
  会暴露给后续任务，agent 可以通过 `skill_load` 工具加载并复用。

核心问题：

> **一个会在后台自动抽取可复用技能、并在后续任务中加载复用的 agent，
> 是否比每次从零开始的 agent 更强？**

SkillCraft 很适合回答这个问题：每个任务族提供"形状相同、规模递增"的
变体（`e1`–`e3` easy, `m1`–`m2` medium, `h1` hard）。如果 agent 能
在简单任务上提炼出可复用技能，那么后续复杂任务就应该更稳定、更省
token，或两者兼有。

## 2. 实验设置

### 2.1 基准数据集

| 项目 | 值 |
| --- | --- |
| 基准 | SkillCraft |
| 任务族 | `openmeteo-weather`（天气监测）、`recipe-cookbook-builder`（食谱构建） |
| 每个族的变体 | `e1` / `e2` / `e3` / `m1` / `m2` / `h1` |
| 每轮任务数 | 12 |
| Agent 模型 | `gpt-4o-mini` |
| Reviewer 模型 | `gpt-4o-mini` |
| 评分 | SkillCraft 官方 `evaluation/main.py` |

### 2.2 技能种子库

所有 run 使用同一份 `clean_library_v19` 作为 warm-start 种子，包含
7 条 generic-parent-only 技能（3 条 weather collection + 2 条
weather monitor + 1 条 `Recipe Cookbook - Multi-Dish` + 1 条
`Economic Snapshot - Multi-Country`）。没有 count-specific 兄弟簇
（不含 `3/4/5 Cities`、`3/4/5 Dishes` 这类变体）。

### 2.3 Evolution 机制

evolution 是一个**异步学习闭环**，主流程不被阻塞：

1. 每个任务完成后，runner 将 transcript + evaluator outcome 入队；
2. 后台 reviewer 模型给出结构化决策（`skills` / `updates` / `deletions`）；
3. 确定性 reconciler（`reconcile.go`）去重、吸回兄弟簇；
4. 通过审批闸门（Phase A/B/C）写入 managed skills 目录。

Agent 侧通过 `skill_load` 工具加载 skill body。框架层在 relevance
ranking 之上增加了 "Top recommended skill" 硬提示，benchmark 层的
instruction 要求 agent 在 domain tool 前先 `skill_load` 匹配的技能
（skill-first protocol）。

### 2.4 审批闸门

| Phase | 组件 | 说明 |
| --- | --- | --- |
| A | `FileCandidateStore` + `FileActivePointer` | 每个 skill 的每次变更都写成 immutable revision（`meta.json` + `SKILL.md`），旁边一个 append-only `audit.log`；`active.txt` 指向当前可见 revision |
| B | `DefaultSpecGate` + `DefaultSafetyGate` | 确定性规则，零 LLM 调用。SpecGate 检查 schema 完整性 / name 稳定性 / 查重 / quantified-sibling；SafetyGate 扫描 secret pattern / 危险 shell / path traversal |
| C | `OutcomeBasedEffectivenessGate` | 检查触发 review 的那个 session 的 Outcome：score < 80 或 status=fail/agent_error 时，revision 停在 `PendingEval` 不自动 promote，防止从灾难 run 中学到错误的技能 |

### 2.5 评估配置

| 配置 | 描述 | 对应版本标记 |
| --- | --- | --- |
| **Baseline** | 无 managed skills | 所有版本共用 |
| **Evolution (v20)** | Phase A + B 审批闸门 | 5 轮 |
| **Evolution (v21b)** | Phase A + B + C 全闸门 | 5 轮 |

每种配置均重复跑 5 轮取均值 + 标准差，以控制 baseline 灾难 loop 带来
的方差。

## 3. 结果

### 3.1 总体指标

**表 1：5 轮聚合对比**

| 指标 | Baseline 均值 | Evolution (v20) | Evolution (v21b) |
| --- | ---: | ---: | ---: |
| Pass rate | 95.00% | **98.33%** (+3.3pp) | **98.33%** (+3.3pp) |
| Average score | 91.35% | 95.44% | 96.36% |
| E2E tokens / task | 148,396 | 129,408 (**-12.8%**) | 131,170 (**-17.3%** vs own baseline) |
| E2E token stddev | 84,820 | 14,857 (**17.5%**) | 6,387 (**13.9%**) |
| `skill_load` invoked | 0% | **100%** | **100%** |
| Gate candidates | — | 47 | 59 |
| Gate promoted | — | 47 | 59 |
| Gate rejected (spec+safety) | — | 0 | 0 |
| Gate held (effectiveness) | — | — | 0 |

> Evolution 在 pass rate、token 均值、token 方差三个维度上全面优于
> baseline。`skill_load` 从历史上长期 0% 提升到 100%，说明 agent 现在
> 确实在消费学到的技能。审批闸门是透明的：不吞掉 evolution 的收益，
> 也不引入可观测的回退。

### 3.2 逐轮明细

**表 2：v20（Phase A + B 审批闸门）5 轮明细**

| Run | Baseline pass | Evolution pass | Baseline E2E | Evolution E2E | Δ E2E | Promoted |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| try1 | 91.67% | **100.00%** | 109,620 | 121,879 | +12,258 | 0\* |
| try2 | 100.00% | 100.00% | 97,798 | 122,495 | +24,697 | 12 |
| try3 | 100.00% | 91.67% | 126,021 | 155,252 | +29,231 | 11 |
| try4 | **83.33%** | **100.00%** | **299,059** | 118,938 | **-180,121** | 12 |
| try5 | 100.00% | 100.00% | 109,482 | 128,475 | +18,993 | 12 |

\* try1 因 metrics-snapshot 时序 bug 读到 0，修正后 try2–try5 正常。

**表 3：v21b（Phase A + B + C 全闸门）5 轮明细**

| Run | Baseline pass | Evolution pass | Baseline E2E | Evolution E2E | Δ E2E | Promoted | Held |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| try1 | 100.00% | 91.67% | 182,681 | 134,646 | -48,035 | 11 | 0 |
| try2 | **91.67%** | **100.00%** | 117,953 | 121,466 | +3,513 | 13 | 0 |
| try3 | 100.00% | 100.00% | 101,708 | 138,565 | +36,857 | 11 | 0 |
| try4 | **91.67%** | **100.00%** | 183,337 | 131,472 | -51,865 | 12 | 0 |
| try5 | **91.67%** | **100.00%** | 207,530 | 129,703 | -77,827 | 12 | 0 |

### 3.3 灾难 loop 压制效果

Baseline 使用 `gpt-4o-mini` 时，weather 任务族存在随机灾难 loop：
agent 反复调用 `weather_get_hourly` 直到上下文爆炸（单任务 token
> 1M）。Evolution 通过 skill 中的明确步骤指引（"每城市调一次即可"）
有效压制了这一问题。

**表 4：代表性灾难 loop 案例**

| Run | Task | Baseline tokens | Evolution tokens | 节省 |
| --- | --- | ---: | ---: | ---: |
| v20/try4 | weather/e1 | 1,343,723 (agent_error) | 72,063 | 94.6% |
| v20/try4 | weather/m1 | 1,097,449 | 107,168 | 90.2% |
| v21b/try5 | weather/e1 | 710,736 | 64,444 | 90.9% |

### 3.4 审批闸门行为

**表 5：审批闸门统计（v21b 5 轮合计）**

| 指标 | 值 |
| --- | --- |
| Candidate revisions seen | 59 |
| Revisions promoted to active | 59 |
| SpecGate rejected | 0 |
| SafetyGate rejected | 0 |
| EffectivenessGate held | 0 |
| Revision store on-disk (per skill) | `revisions/<id>/{meta.json, SKILL.md}` + `audit.log` + `active.txt` |

> 零 rejection 是预期行为：`reconcile.go` 的 Rule 1/2/3 已经在 reviewer
> 输出送入 gate 之前吸掉了绝大部分不合规候选（quantified sibling、
> strict superset 重名）。SpecGate / SafetyGate 对恶意 case（secret
> leak、`rm -rf /`、`../../etc/passwd`）的拦截能力已在单元测试中验证。

### 3.5 附加实验：v21（effectiveness gate 全量拦截）

v21 因 score-scale bug（`Outcome.Score` 被错误地从 0-100 缩放到 0-1），
effectiveness gate 意外拦截了所有 60 个 reviewer-generated revision。
这一意外实验给出了一个重要发现：

| 指标 | Baseline | Evolution (v21, 0/60 promoted) |
| --- | ---: | ---: |
| Pass rate（5 轮均值） | 91.67% | **100.00%** (+8.33pp) |
| E2E tokens / task | 187,427 | **125,324** (-33.1%) |
| E2E token stddev | 36,762 | **5,593** (15.2%) |

> **即使 reviewer 的所有 update 都被拦截，evolution 仍然压倒性胜出。**
> 这证明 evolution 的收益完全来自 warm-start seed + skill_load，而非
> reviewer 在当次 run 中产生的 update。因此 effectiveness gate 可以
> 任意保守——哪怕全量拦截也不影响当次 run 的表现。

### 3.6 Multi-session 累积实验

为验证 skill 库在长期使用下是否会退化，我们进行了 5 轮累积实验：
Round 1 从空库冷启动，后续每轮用上一轮产出的 `managed_skills/` 作为
seed（不使用手工 `clean_library_v19`）。

**表 6：累积实验逐轮结果**

| Round | Baseline pass | Evolution pass | Baseline E2E | Evolution E2E | Δ E2E | Skills | skill_load |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| R1 (cold) | 100.0% | 91.7% | 212,271 | 251,835 | +39,564 | 6 | 83% |
| R2 | 91.7% | 83.3% | 227,334 | 135,773 | -91,561 | 6 | 92% |
| R3 | 91.7% | 91.7% | 170,753 | 141,329 | -29,424 | 6 | 100% |
| R4 | 91.7% | 83.3% | 240,514 | 284,498 | +43,984 | 6 | 92% |
| R5 | 83.3% | **100.0%** | **349,946** | 162,408 | **-187,538** | 6 | 100% |

关键发现：

1. **Skill 库收敛到 6 条，5 轮零增长。** Reconciler + SpecGate 把
   reviewer 的所有 create 都吸成 update，库不会膨胀。
2. **Reviewer 自产 skill 是 count-specific 的**（`3 Cities`、
   `4 Cities`、`5 Cities`），不如手工 generic-parent 有效，
   导致 evolution pass rate 均值 ~90%（手工 seed 为 ~98%）。
3. **Token 压制效果仍在**：R2 (-91k)、R3 (-29k)、R5 (-188k) 证明
   即使用较弱的 reviewer 自产 skill，evolution 在 baseline 灾难 loop
   时仍能救场。
4. **skill_load 率从 83% 收敛到 100%**。

### 3.7 累积实验对比：v2（旧 prompt）vs v3（genericization prompt）

v3 在 reviewer prompt 中新增了 "MANDATORY naming convention" 段，
明确列举 WRONG（`3 Cities`）和 RIGHT（`Multi-City`）格式。

**表 7：5 轮累积实验 v2 vs v3 聚合**

| 维度 | v2（旧 prompt） | v3（genericization prompt） |
| --- | ---: | ---: |
| Evolution pass Δ vs baseline | -1.7pp | **+1.7pp** |
| E2E token Δ vs baseline | -18.7% | **-28.7%** |
| Skill 库条数（5 轮全程） | 6 | 6 |
| skill_load 100% rounds | 3/5 | 4/5 |

> Prompt 改进在 steps/pitfalls 质量上带来了收益：token 节省从 18.7%
> 提升到 28.7%，pass Δ 从负翻正。Skill naming 仍然是 count-specific
> （`gpt-4o-mini` 指令遵循能力瓶颈），但 reconciler 控制住了库不膨胀。

## 4. 讨论

### 4.1 Evolution 的收益来源

实验数据一致指向同一个结论：evolution 的核心价值不是"每轮都好一点"，
而是**压制 baseline 的随机灾难 loop**。在 baseline 风平浪静的轮次里，
evolution 的 token 略高（因为 skill_load + reviewer 的 overhead）；
在 baseline 命中灾难 loop 的轮次里，evolution 能节省 90%+ 的 token
并挽救 pass。这解释了为什么三轮均值有时看起来 evolution 略差——样本
不够时恰好没命中灾难 loop。v20 的经验证明：**效果评估必须用 ≥ 5 轮
均值 + stddev**，否则会把真实收益当成 regression 过滤掉。

### 4.2 审批闸门的实际作用

Phase A（revision store + active pointer）解决的是"skill 库可审计、
可回滚"，不是"让 benchmark 数字更好"。Phase B（SpecGate + SafetyGate）
是最后一道防线，当前因为 reconciler 已经把绝大部分不合规候选清理掉了，
所以 gate 看起来没有拦截任何东西——这是正确的。Phase C（effectiveness
gate）在正常运行时也不会拦截（成功任务的 revision 都能过阈值），只在
灾难 run 触发时才会挡住"从错误中学到的错误 skill"。

### 4.3 局限性

1. **任务族覆盖有限**：当前只评估了 weather 和 recipe 两个族。
   `world-bank-economic-snapshot` 因 MCP 工具超时问题（与 evolution 无关）
   暂时排除在外。
2. **Reviewer 模型较弱**：`gpt-4o-mini` 仍会生成 count-specific 兄弟簇
   （如 `Recipe Cookbook Creation - 5 Dishes`），靠 reconciler 吸回。
   更强的 reviewer 可能直接避免这类问题。
3. **技能消费路径单一**：当前 agent 只通过 `skill_load` 消费技能，
   没有 progressive disclosure（先看摘要再决定是否 load）。
4. **缺乏生产流量验证**：所有数据来自 SkillCraft benchmark，缺乏
   真实线上 adopter 的 skill 产出密度和命中率数据。

## 5. 结论

在 SkillCraft 上的多轮受控实验中，trpc-agent-go 的 agent 自进化机制
展现了三方面确定性收益：

1. **Pass rate 提升**：5 轮均值 +3.3pp（95.0% → 98.3%），且 evolution
   在所有版本中都保持了更低的失败方差。
2. **Token 消耗降低**：5 轮均值 -12.8% 至 -33.1%，主要来自对 baseline
   灾难 loop 的压制，单案例最高节省 94.6%。
3. **方差显著收敛**：evolution 的 e2e-token 标准差仅为 baseline 的
   13.9%–17.5%，说明 skill_load 让 agent 行为更稳定可预测。

审批闸门（Phase A/B/C）已完整落地并在评测中运行，证明其作为透明层
不引入回退，同时为生产上线提供了可审计、可回滚的 skill 生命周期管理。

---

## 附录

### A. 复现命令

```bash
cd skillcraft/trpc-agent-go-impl

# v21b (全闸门)
go run . \
  -skillcraft-root "$SKILLCRAFT_ROOT" \
  -tasks "openmeteo-weather/e1,...,recipe-cookbook-builder/h1" \
  -mode compare \
  -model gpt-4o-mini \
  -reviewer-model gpt-4o-mini \
  -max-tool-iterations 24 \
  -load-skills-from ../results/tools/clean_library_v19 \
  -max-prompt-skills 8 \
  -enable-approval-gate \
  -effectiveness-gate \
  -output ../results/multi_family_compare_v21b_tryN
```

### B. 关键 CLI 参数

| 参数 | 说明 |
| --- | --- |
| `-enable-approval-gate` | 开启 Phase A revision store + Phase B SpecGate/SafetyGate |
| `-effectiveness-gate` | 开启 Phase C outcome-based effectiveness gate |
| `-approval-gate-shadow` | Shadow 模式：gate 评估但不拦截，用于对比 |
| `-load-skills-from` | 指定 warm-start seed 目录 |
| `-max-prompt-skills` | 限制 prompt 中 skill overview 的条数 |
