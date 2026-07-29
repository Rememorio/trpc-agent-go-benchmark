# 基于 LoCoMo 与 LongMemEval 的长期对话记忆评估

## 1. 引言

本报告使用两个互补基准评估 **trpc-agent-go** 的长期对话记忆能力：

- **LoCoMo** 通过 1,986 个问题评估广泛的对话问答质量，并支持
  与 Agent 框架及公开记忆系统进行横向比较。
- **LongMemEval** 将生产 memory 生命周期拆分为提取、持久化、
  召回和回答阶段，直接比较 trpc-agent-go pgvector memory service
  与 self-hosted mem0。

LoCoMo 评估涵盖一个 long-context 上界和三种 memory 策略：

- **Long-Context**：将完整对话放入模型上下文
- **trpc-agent-go (原版)**：基线版本（Auto 提取 + pgvector）
- **trpc-agent-go (优化版)**：经过多轮优化，包括情境化记忆提取、
  情景记忆分类、混合检索、多轮检索等（详见 2.3 节）
- **Session Recall**：在查询时检索已持久化的原始 session event

以上方案与四个 Python Agent 框架（AutoGen、Agno、ADK、
CrewAI）和十个外部记忆系统（Mem0、Zep 等）进行对比。当前
LongMemEval protocol v2 评估使用一个固定、已经观察过的 16 题开发集，
每个实验臂生成三次独立答案。它用于回归与机制分析，不是未见证据，
也不代表完整 500 题数据集。

LoCoMo 表格保留的是历史产物。trpc-agent-go 的“原版”“优化版”和
Agentic 使用 auto-replay-v3：每个历史 session 写入后还会执行一个
占位 agent turn，因此可能附加 synthetic assistant response；当源
session 以 transport `assistant` 结尾时，还可能重复写入最近的 user
turn。Exact-replay-v4 现在只写入一次映射后的数据集 turn，不再运行
占位 agent。Long-Context、Session Recall 和手工写入的外部框架不受
影响；旧 trpc-agent-go Auto/Agentic 数值在 v4 重跑前只作历史描述。

## 2. 实验设置

### 2.1 基准数据集

| 基准 | 范围 | 类别 | 模型 | Embedding |
| --- | --- | --- | --- | --- |
| LoCoMo-10 | 10 个对话，1,986 个 QA | single-hop (282), multi-hop (321), temporal (96), open-domain (841), adversarial (446) | GPT-4o-mini（推理 + 评判） | text-embedding-3-small |
| LongMemEval Oracle | 从 500 题中固定的 16 题已观察开发回归集 | knowledge-update、multi-session、single-session-assistant、single-session-preference、single-session-user、temporal-reasoning；所选问题中 4 题为 abstention | glm52（memory、回答与评判） | text-embedding-3-small |

LoCoMo 用于广泛质量评估与跨系统比较。LongMemEval Oracle 去掉了
大量无关 session 的 haystack，使失败更容易归因到 memory 提取、
持久化、召回或回答阶段，但它不能替代未来在嘈杂长历史上的
LongMemEval-M 评测。早期 protocol v1 子集仍可用于诊断，但不同后端的
日期传递方式不一致，因此不再支撑正式的跨后端结论。

### 2.2 LoCoMo 评估场景

| 场景 | 描述 |
| --- | --- |
| **Long-Context** | 完整对话文本作为 LLM 上下文（上界） |
| **Session Recall** | 在查询时搜索已持久化的原始历史 session event |
| **原版** | Auto 提取 + pgvector 基线；后台提取器自动生成记忆并在查询时检索 |
| **优化版** | 面向抽取式持久化 memory 的优化记忆提取策略与多轮检索流程 |

新的 Auto run 使用 `chronological-session-sequential-auto-v4`：每条
映射后的数据集 user/assistant turn 只写入一次，再对完整 session
执行一次提取。报告中的原版/优化版历史产物使用 v3，不能作为当前
候选的 gate。Session Recall 原本就直接写入历史 event，不受此次修正
影响。

### 2.3 Memory 优化

在 LoCoMo 上，优化版在原版基线的基础上，围绕记忆提取、存储和检索三个环节
进行了一系列针对性改进：

1. **情境化记忆提取（Contextualized Memory Extraction）**——
   原版提取器生成的记忆为扁平、无结构的文本。优化版使用精心设计
   的提取 prompt，强制要求**原子性**（每条记忆仅包含一个信息点）、
   **完备性**（提取所有说话者、所有细节）和**具体性**（保留
   准确的人名、日期、数量），从而显著提升信息密度和检索召回率。

2. **情景记忆分类（Episodic Memory Classification）**——每条
   提取的记忆被分类为**事实（Fact）**（稳定的属性、偏好、关系）
   或**情景（Episode）**（带时间锚点的事件，包含 `event_time`、
   `participants`、`location` 元数据）。结构化 schema 使检索时
   可按时间范围过滤和按 event_time 排序，这对 multi-hop 和
   temporal 类问题至关重要。

3. **相对时间绝对化（Absolute Date Resolution）**——对话中的
   相对时间表达（如「昨天」「上个月」）在存储前会根据 session
   的参考日期解析为绝对 ISO 8601 日期。这避免了时间漂移，
   使基于日期的查询更加准确。

4. **主题标签（Topic Tagging）**——每条记忆被标注描述性主题
   标签（如 `["hiking", "Mt. Fuji", "travel"]`），且提取器被
   指导优先复用已有的主题名，而非发明同义词。主题标签提升了
   检索相关性，并为未来的主题过滤提供了基础。

5. **混合检索（Hybrid Search：向量 + 关键词）**——原版仅使用
   纯向量相似度搜索。优化版新增**混合检索**，将向量余弦相似度
   与 PostgreSQL 全文检索（`tsvector/tsquery`）通过**倒数排名
   融合（Reciprocal Rank Fusion, RRF）**合并。这显著提升了对
   特定实体名称、书名等精确匹配项的召回率——这些词单靠向量
   embedding 往往无法获得高排名。

6. **多轮检索（Multi-Pass Retrieval）**——QA Agent 不再只做
   一次搜索，而是执行 **2–3 轮搜索**，每轮使用不同的查询策略
   （如关键词式查询、实体聚焦查询、宽泛人名查询），从不同角度
   最大化召回后再综合回答。

7. **类型回退（Kind Fallback）**——当按记忆类型过滤的检索
   （如仅检索 episode）返回结果不足（< 3 条）时，系统自动
   回退为不带类型过滤的检索，并合并两组结果，优先展示匹配
   目标类型的条目。这防止了因分类不确定而遗漏结果。

8. **内容去重（Content Deduplication）**——对检索结果中近重复
   的记忆（词级 Jaccard 相似度 > 80%）进行去重，仅保留得分
   最高的版本，减少检索结果中的冗余上下文。

LongMemEval 进一步暴露了生产路径上的另一组可靠性问题。相应改动
会保留 assistant 给出的具体答案、列表和结构化产物，在提取过程中携带
observation time，并在结构化提取结果格式错误时重试。如果重试后仍没有
operation，符合条件的长 assistant 输出会通过保守 fallback 保存。

最终 candidate 进一步收窄了第二遍结构化结果 recovery 的上下文：模型只
接收当前带日期的 user/assistant pair，而 persistence 仍使用既有 memory
snapshot 进行重复与冲突处理。这样可以从 recovery prompt 中移除整份既有
memory，同时不削弱后续 reconciliation。

当前 candidate 还压缩了 assistant-result 提取说明，并将私有
assistant-result tool 的 schema 收窄为必填 `memory`、可选 `topics`。
该改动没有新增公共 API，也没有改变 opt-in 边界；它减少每次提取重复发送的
prompt 文本，同时保留来源标记、精确数值和结构化结果整体性等约束。

提取上下文还会累积 observation time，避免后续 turn 擦除早期状态的
观察时间；focused source passage 在 recovery 时保留触发它的具体实体
或列表；temporal retrieval 在混合检索之外保留一个有界的日期事件尾部。
这些机制只在 candidate 实验臂显式开启；普通 user memory 的更新继续使用
兼容 upstream main 的默认 reconcile 行为。

最终检索阶段复用 assistant-result memory 中已经保存的来源标记。问题明确
引用历史 assistant 回答时，RRF 增加一组 assistant-result 排名；普通事实、
偏好和当前建议问题则增加一组 user-grounded 排名。这是软性的第四个信号，
不是过滤器，不改变 similarity score、持久化 memory 或公共 API。Intent
分类器刻意保持窄范围：单独的“提醒我”、泛化 follow-up 或当前推荐请求，
不会被归类为 assistant-history query。

较早的 candidate 曾为普通 memory 导出一套独立的 history-preserving update
policy。新鲜 LoCoMo 策略消融没有支持它：默认 reconcile 的整体分数略高，
并在三次回答重复中赢得两次。因此最终设计删除该公共策略及其专用状态恢复
路径。严格保留只存在于私有 assistant-result memory 中，因为改写带引用的
答案或结构化产物会丢失该特性本来要保存的证据。

自动 Add reconcile 现在只改写高置信近重复内容，相关但不同的计划、
推荐、事件和实体列表会分别保留。异步任务中的 Add、Update、Delete
和 Clear 错误会向上传播并写入 session state，失败任务不再推进提取
完成标记。提取 prompt 中类似 benchmark 的示例也已换成合成内容，
避免 prompt leakage。

### 2.4 LongMemEval 重放与公平性

每个 LongMemEval 问题使用独立的 user 和 run scope。Haystack session
按时间排序，并逐个 user/assistant pair 重放。每个 pair 后，pgvector
通过生产接口 `memory.Service.EnqueueAutoMemoryJob` 触发提取并等待完成；
mem0 通过公开 API 接收同一个原始 pair。源 session 日期不写入消息
正文，而是独立传递并填入各后端的 observation-date context。回答模型
只看到搜索出的 memories，不会看到原始对话。

所有实验臂都使用 temperature 0 的 glm52、`text-embedding-3-small` 和
top-k 30。接受的冻结 baseline 比较默认 reconcile 的 upstream-main
pgvector、采用默认 reconcile 加 assistant-result extraction 的 candidate，
以及固定镜像、以 pgvector 为后端的 self-hosted Mem0 OSS。最终 provenance
ranking 只从 candidate 的精确持久化 memory 快照刷新 retrieval，之后重新运行
answer 和 judge。这样可以把 retrieval 改动与 extraction 随机性分离，并且只有在
memory 逐字节稳定后才继承入库 usage。Runner 记录 extraction operation、memory
diff、retrieval hit、证据来源、错误、耗时、LLM/embedding usage、cached token、
构建 revision 和脱敏 Mem0 配置。

固定开发集从每种 LongMemEval 类型抽取 2 个可回答问题，再加入 4 个
abstention，共 16 题；每个实验臂重放 183 个 user/assistant pair。每个臂
基于保存的 top-k 独立回答三次，每次答案再接受三个独立 semantic-judge
投票。同一 replicate 内各臂共享一个空的 content-addressed answer/judge
ledger，不同 replicate 使用不同 ledger。其中一个计划内 replicate 出现不完整
Mem0 答案，因此整次 replicate 对所有实验臂、所有 case 使用新 ledger 替换，
没有按 case 选择性重采样。Exact Match、F1 和 BLEU 仅作辅助诊断。

比较前会校验 dataset、selection、protocol、prompt、model、build 和 Mem0
runtime digest。运行前冻结的晋级 gate 同时覆盖多数正确率、正确 replicate
总数、类别回退、provider usage、backend error、token、embedding 和 memory
数量。本轮没有选择或运行新的 blind holdout。

## 3. 结果

3.1-3.3 节保留 legacy LoCoMo 产物及原始数值，以便追溯 provenance；
涉及 trpc-agent-go Auto、Agentic、SQLite 或 SQLiteVec 的比较必须在
exact-replay-v4 下重跑。3.4 节是独立的 LongMemEval 实验，不受影响。

### 3.1 内部场景对比（Legacy LoCoMo Replay）

**表 1：总体指标**

| 场景 | F1 | BLEU | LLM Score | Tokens/QA | 调用/QA | 延迟 | 总耗时 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Long-Context | 0.469 | 0.426 | 0.526 | 18,776 | 1.0 | 2,607ms | 1h26m |
| Session Recall | **0.549** | **0.511** | **0.609** | 3,694 | 1.0 | 6,430ms | 3h33m |
| 优化版 | **0.469** | **0.431** | **0.532** | 17,182 | 3.0 | 8,585ms | 4h44m |
| 原版 | 0.399 | 0.371 | 0.416 | 3,056 | 2.0 | 6,659ms | 3h40m |

> 优化版的 F1 从 0.399 提升到 **0.469**（+17.5%），达到
> Long-Context F1 的 **99.9%**（原版仅为 85.1%）。虽然名义
> Tokens/QA 较高（17,182），但其中 **43.9% 命中 prompt cache**，
> 实际新增 token 成本约为 9,663/QA（详见 4.5 节）。
>
> 作为补充检索路径，Session Recall 现在把总体 F1 推到
> **0.549**，同时将 Tokens/QA 控制在 **3,694**。相比
> Long-Context，token 成本降低 **80.3%**；相比优化版，
> 降低 **78.5%**。

**表 2：各类别 F1**

| 类别 | Count | Long-Context | Session Recall | 优化版 | 原版 |
| --- | ---: | ---: | ---: | ---: | ---: |
| single-hop | 282 | 0.320 | 0.368 | **0.396** | 0.316 |
| multi-hop | 321 | 0.308 | **0.554** | 0.453 | 0.096 |
| temporal | 96 | 0.088 | 0.174 | **0.247** | 0.088 |
| open-domain | 841 | 0.518 | **0.618** | 0.441 | 0.358 |
| adversarial | 446 | 0.667 | 0.610 | 0.626 | **0.814** |

**表 3：加权平均 F1**

| 平均方式 | Long-Context | Session Recall | 优化版 | 原版 |
| --- | ---: | ---: | ---: | ---: |
| 5 类加权 (÷1986) | 0.469 | **0.549** | 0.469 | 0.399 |
| 4 类加权 (÷1540，排除 adversarial) | 0.411 | **0.531** | 0.423 | 0.279 |

> 优化版依然在四类知识型问题上相比原版有全面提升，其中
> **multi-hop** 从 0.096 提升到 0.453（+372%）最为显著，
> **temporal** 从 0.088 提升到 0.247（+181%）次之。adversarial
> 从 0.814 降到 0.626，主要是因为原版有更强的拒答倾向。
>
> 作为补充方案，Session Recall 现在更大幅地改变了整体权衡：它在
> **multi-hop** 和 **open-domain** 上表现最佳，**temporal** 也
> 提升到 0.174，并将 4 类加权 F1 推到 **0.531**。优化版依然在
> **single-hop** 和 **temporal** 上更强，而 Long-Context 与优化版
> 在 **adversarial** 上仍略有优势。

**表 4：各样本 F1**

| 样本 | QA 数 | Long-Context | Session Recall | 优化版 | 原版 |
| --- | ---: | ---: | ---: | ---: | ---: |
| locomo10_1 | 199 | 0.455 | **0.530** | 0.432 | 0.331 |
| locomo10_2 | 105 | 0.496 | **0.636** | 0.422 | 0.302 |
| locomo10_3 | 193 | 0.527 | **0.644** | 0.521 | 0.432 |
| locomo10_4 | 260 | 0.466 | **0.482** | 0.447 | 0.378 |
| locomo10_5 | 242 | 0.433 | **0.542** | 0.436 | 0.451 |
| locomo10_6 | 158 | 0.511 | **0.553** | 0.505 | 0.455 |
| locomo10_7 | 190 | 0.461 | **0.530** | 0.487 | 0.407 |
| locomo10_8 | 239 | 0.453 | **0.563** | 0.492 | 0.404 |
| locomo10_9 | 196 | 0.450 | **0.508** | 0.464 | 0.383 |
| locomo10_10 | 204 | 0.471 | **0.562** | 0.478 | 0.407 |
| **平均** | **199** | 0.469 | **0.549** | 0.469 | 0.399 |

> 优化版相较原版在全部 10 个样本上都有提升，并在其中 6 个样本上
> 超过 Long-Context。
>
> 作为补充方案，Session Recall 现在在 10 个样本里全部超过
> Long-Context，也在 10 个样本里全部超过优化版，提升最大的样本是
> `locomo10_2`、`locomo10_3` 和 `locomo10_5`。

### 3.2 检索策略 vs Long-Context

Long-Context 将完整对话历史放入单次 LLM 调用，在短单 session
场景中有效；两种检索式方案则体现出不同的生产权衡：

| 维度 | Long-Context | Session Recall | 优化版 |
| --- | --- | --- | --- |
| **跨 session 来源** | 无 | 直接在 query 时搜索历史 session 原始事件 | 搜索抽取后的持久化 memory |
| **上下文窗口** | 受模型限制（GPT-4o-mini 128K） | 无上限——仅注入召回的事件片段 | 无上限——仅注入检索到的 memory |
| **可扩展性** | 成本随转录长度线性增长 | 成本近似常量（top-K 召回） | 成本受 tool-call 步数和 memory payload 影响 |
| **总体 F1** | 0.469 | **0.549** | 0.469 |
| **4 类加权 F1** | 0.411 | **0.531** | 0.423 |
| **Tokens/QA** | 18,776 | **3,694** | 17,182 |
| **突出优势** | adversarial 更稳 | 总体准确率、open-domain 与 multi-hop 最强 | temporal / adversarial 更均衡 |

---

### 3.3 SQLite vs SQLiteVec（子集实验）

本小节对比 `sqlite`（关键词/Token 匹配）与 `sqlitevec`（sqlite-vec 语义向量检索）
在若干个可控的子集实验上的表现，用于观察 token 成本与检索差异。

**子集实验 A：端到端 QA（Auto / 全类别）**

该实验保持端到端流程与主要实验一致，但仅评估单个样本以控制成本。

**实验配置**：

- 数据集：LoCoMo `locomo10.json`
- 样本：`locomo10_1`（199 个 QA，包含全部类别）
- 场景：`auto`
- 模型：`gpt-4o-mini`
- LLM 评判：启用
- SQLiteVec embedding 模型：`text-embedding-3-small`
- SQLiteVec 检索 top-k：10（默认值）

**端到端结果：总体指标与 token 消耗（Auto / 199 QA）**

| 后端 | #QA | F1 | BLEU | LLM Score | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | 平均延迟 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 199 | 0.327 | 0.301 | 0.370 | 1,287,813 | 5,624 | 1,293,437 | 398 | 5,805ms |
| SQLiteVec | 199 | 0.307 | 0.285 | 0.325 | 407,969 | 5,556 | 413,525 | 396 | 6,327ms |

**解读（locomo10_1）**：

- **SQLiteVec 的 prompt token 约减少 3.2x**（top-k 有界检索），但在该样本上
  **F1/BLEU/LLM Score 略低**（默认 top-k=10）。
- 类别层面的表现存在差异：`sqlitevec` 在 `adversarial` 上更好（更多正确拒答），
  但当关键信息未进入 top-k 时，其他类别会出现召回不足导致的下降。

我们也在另一个代表性样本上复现相同配置。

- 样本：`locomo10_6`（158 个 QA，包含全部类别）

**端到端结果：总体指标与 token 消耗（Auto / 158 QA）**

| 后端 | #QA | F1 | BLEU | LLM Score | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | 平均延迟 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 158 | 0.269 | 0.243 | 0.289 | 1,296,580 | 5,103 | 1,301,683 | 340 | 6,359ms |
| SQLiteVec | 158 | 0.274 | 0.254 | 0.295 | 362,903 | 4,773 | 367,676 | 324 | 6,928ms |

**总体结论（locomo10_1 + locomo10_6）**：

- SQLiteVec 在我们的子集实验中稳定地将 prompt token 降低到约 1/3 到 1/4。
- 默认 top-k=10 下，答案质量的变化与样本相关；调大 top-k 可能提升召回，
  但也会增加 prompt token。

> 注：`Prompt Tokens`、`LLM Calls` 仅统计 QA 阶段 Agent 的模型调用，
> 不包含 embedding 请求与 LLM-as-Judge 调用。`平均延迟` 为端到端总耗时
> 按 #QA 平均（包含 embedding、LLM-as-Judge 以及 auto extraction）。

**子集实验 B：Temporal-only token 成本微基准**

**实验配置**：

- 数据集：LoCoMo `locomo10.json`
- 样本：`locomo10_1`
- 类别过滤：`temporal`（13 个 QA）
- 场景：`auto`
- 模型：`gpt-4o-mini`
- LLM 评判：关闭
- SQLiteVec embedding 模型：`text-embedding-3-small`

**表 5：总体指标与 token 消耗（Auto / Temporal / 13 QA）**

| 后端 | F1 | BLEU | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | 平均延迟 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 0.116 | 0.082 | 80,184 | 352 | 80,536 | 26 | 12,352ms |
| SQLiteVec | 0.116 | 0.082 | 26,483 | 353 | 26,836 | 26 | 17,817ms |

**子集实验 C：向量 top-k 扫参 + 多次检索消融（Auto / 全类别）**

**表 6：Top-k 与多次检索扫参结果（Auto / locomo10_1 / 199 QA）**

| 后端 | vector-topk | qa-search-passes | F1 | BLEU | Prompt Tokens | Avg Prompt/QA | 平均延迟 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | - | 1 | 0.299 | 0.283 | 1,322,360 | 6,645 | 3,316ms |
| SQLiteVec | 5 | 1 | 0.320 | 0.296 | 346,253 | 1,740 | 4,182ms |
| SQLiteVec | 10 | 1 | 0.343 | 0.315 | 398,751 | 2,004 | 4,352ms |
| SQLiteVec | 20 | 1 | 0.329 | 0.308 | 621,790 | 3,125 | 4,180ms |
| SQLiteVec | 40 | 1 | 0.327 | 0.303 | 965,423 | 4,851 | 4,460ms |
| SQLiteVec | 10 | 2 | 0.342 | 0.312 | 659,981 | 3,316 | 5,198ms |

**解读**：

- **top-k 并非越大越好**：top-k=20/40 虽然增加了 prompt token，但 F1/BLEU
  略有下降。QA Agent 对检索噪声较敏感。
- `qa-search-passes=2` 在部分类别上有改善（如 multi-hop），但总体 F1 无提升。

### 3.4 LongMemEval：pgvector vs Self-Hosted mem0

> **证据范围：** 本节是固定、已经观察过的 16 题开发回归，协议为
> `lme-memory-turn-pair-v2`。它验证 protocol 和 candidate 机制，但不是
> 未见 holdout。早期 protocol v1 seed48/seed137 结果只保留为历史诊断。

Protocol v2 比较评估的是生产 auto-memory 路径，而不是前文的 LoCoMo
检索变体。接受的 baseline 重放相同的 183 个 user/assistant pair，消息正文
逐字一致，日期只通过 metadata 传递，Top-K 均为 30，store 相互隔离。每个
实验臂独立回答三次，每个答案接受三个独立 semantic-judge 投票。

| 实验臂 | Memory 策略 | Primary | Majority | 正确 replicate | 不稳定 case | 错误 |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| pgvector main | 默认 reconcile；不提取 assistant result | 11/16 | 11/16 | 33/48 | 0 | 0 |
| Mem0 OSS | 固定的 self-hosted OSS runtime | 14/16 | 14/16 | 42/48 | 0 | 0 |
| provenance 排名前的 pgvector | reconcile；提取 assistant result | 15/16 | 15/16 | 46/48 | 1 | 0 |
| 最终 pgvector candidate | 相同 memory；query-aware provenance RRF | **16/16** | **16/16** | **48/48** | **0** | **0** |

前三行使用接受的全实验臂 replacement manifest。之前一次 replicate 因 Mem0
答案仍被截断而废弃，所有实验臂、所有 case 都用新的 answer/judge ledger
完整重跑。最后一行是在精确 candidate memory 快照上的 retrieval-only follow-up，
使用三个新的 answer ledger，每个答案仍是三票 judge。因此它只继承 memory-layer
成本，不继承 answer 结果。

Majority 收益覆盖六个类别：

| LongMemEval 类型 | 题数 | pgvector main | Mem0 OSS | 排名前 | 最终 candidate |
| --- | ---: | ---: | ---: | ---: | ---: |
| knowledge-update | 2 | 2 | 2 | 2 | **2** |
| multi-session | 4 | 2 | 3 | 3 | **4** |
| single-session-assistant | 2 | 0 | **2** | **2** | **2** |
| single-session-preference | 2 | **2** | **2** | **2** | **2** |
| single-session-user | 3 | **3** | **3** | **3** | **3** |
| temporal-reasoning | 3 | 2 | 2 | **3** | **3** |

以下是 provider 上报的 memory-layer usage，不含 answer/judge 调用。最终
candidate 的数据继承自逐字节稳定的排名前快照：

| 实验臂 | LLM 调用 | Prompt token | Completion token | 总 token | Cached token | Cache hit rate |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| pgvector main | 183 | 1,114,204 | 69,853 | 1,184,057 | 441,472 | 39.62% |
| Mem0 OSS | 183 | 1,667,854 | 97,377 | 1,765,231 | 1,292,992 | 77.52% |
| provenance 排名前的 pgvector | 209 | 1,520,533 | 121,276 | 1,641,809 | 558,400 | 36.72% |
| 最终 pgvector candidate | 209 | 1,520,533 | 121,276 | 1,641,809 | 558,400 | 36.72% |

| 实验臂 | Embedding 请求 | Response-cache hit | Provider 调用 | Embedding token | 最终 memory | 入库耗时 | 搜索耗时 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| pgvector main | 663 | 210 | 453 | 16,500 | 141 | 1,697.6s | 21.0s |
| Mem0 OSS | - | - | 373 | 116,822 | 492 | 1,941.4s | 21.9s |
| provenance 排名前的 pgvector | 627 | 231 | 396 | 27,946 | 311 | 2,770.2s | 0.085s |
| 最终 pgvector candidate | 627 | 231 | 396 | 27,946 | 311 | 2,770.2s | 刷新运行 |

相对 main，最终 candidate 使用 1.3866 倍 memory LLM token、1.6937 倍
embedding token、0.9457 倍逻辑 embedding 请求和 2.2057 倍最终 memory。
相对 Mem0，它少使用 6.99% memory LLM token、76.08% embedding token 和
36.79% 最终 memory，但入库耗时长 42.69%。Prompt-cache token 单列，因为
provider 计费语义不同。最终 answer follow-up 使用 54 次调用、140,568 个 answer
token，以及 144 次调用、39,997 个 judge token；所有 ledger 均从空状态开始，
cache hit 为 0。

保存的 trajectory 可以定位 baseline failure：

- `cc539528` 和 `e3fc4d6e` 是 main 的 extraction failure。Candidate 保存
  assistant 的 `Ruby, Python, PHP` 列表和实体 `Dr. Arati Prabhakar`，两道
  assistant-history 问题都是 3/3。
- `d682f1a2` 主要是 main 的 answer-context failure：retrieval 已包含三个外卖
  服务，但 main 回答 `2`，因此不支持再加 extraction 规则。
- Mem0 的 `09ba9854_abs` 把建议的 bus/taxi 价格当成 user-grounded 事实，
  错答本应 abstain 的问题。`gpt4_7abb270c` 虽召回全部六个 source session，
  但 recommendation memory 过多且时间结构不可用；两题在三次回答中都稳定失败。
- Provenance 排名前，`09ba9854_abs` 也是 candidate 唯一的 majority failure：
  assistant 估价排第 1，三次只有一次 abstain。Query-aware RRF 将用户陈述的
  “约 60 美元出租车太贵”排到该估价之前。三个新答案随后都 abstain，而两道
  明确的 assistant-history 问题仍保持 3/3。

该 retrieval 改动调整了 16 题中的 14 题顺序，但没有改变持久化 memory 或
evidence coverage。收紧 intent classifier 后，16 题的 retrieval array（包括
score 与顺序）与第一版逐字节一致。跨数据集检查在一张包含 139 条 active memory
的冻结 LoCoMo 表上，重放了 31 个唯一 query、60 条 Top-30 trajectory。该表没有
assistant-result memory，所以所有调用的新 RRF signal 都为空，并与旧三信号
merge 完全一致；继承的 target mean F1 为 0.7122。这是零 provider 调用的组件
不活跃回归，不代表 provenance ranking 能改善 LoCoMo，也不是独立样本证据。

更早的消融仍约束最终设计。完全移除保守 recovery 会让 `e3fc4d6e` 不产生
memory，因此被拒绝。公开的 history-preserving update policy 在直接 LoCoMo
消融中略低于默认 reconcile，三次重复只赢一次，因此已删除。Prompt compaction
超过其精确模型调用上限，正式 gate 仍保留为拒绝，即使后续质量诊断支持较短的
内部 prompt。这些结果支持私有、窄范围的 retrieval signal，而不是增加公共
policy 或特殊 case extraction 规则。

当前所有 LongMemEval 证据都来自已经观察的 16 题开发集。最终 candidate 只按
observed evidence 集成，不具备 promotion 资格。要主张泛化，仍需经过授权、
预注册的未见 full-haystack holdout 以及更大的 LongMemEval-M 评测。

---

## 4. 与 Python Agent 框架对比

我们在四个 Python Agent 框架——**AutoGen**、**Agno**、**ADK**、
**CrewAI**——上运行了相同的 LoCoMo 基准，均使用 GPT-4o-mini、
相同的 10 个样本（1,986 QA）及 LLM-as-Judge 评估。

外部框架采用手工写入，不受 trpc-agent-go replay bug 影响。
trpc-agent-go 优化版一行来自 legacy v3，因此涉及该行的跨框架结论
需要 v4 重跑确认。Session Recall 直接重放历史 event，协议仍然有效。

### 4.1 框架配置

| 框架 | 记忆后端 | 检索方式 | Embedding |
| --- | --- | --- | --- |
| **trpc-agent-go** | pgvector | 向量相似度（top-K）+ 多轮检索 | text-embedding-3-small |
| **AutoGen** | ChromaDB | 向量相似度（top-30） | text-embedding-3-small |
| **Agno** | SQLite | LLM 事实提取 → system prompt | 无 |
| **ADK** | 纯内存 | Agent 工具调用（LoadMemoryTool） | 内置 |
| **CrewAI** | 内置向量 | Crew 自动检索 | 内置 |

### 4.2 各框架记忆方案详解

以下按记忆存储、检索、QA 调用流程三个维度，对比五个框架的具体
实现方案。所有框架的 benchmark 代码均使用相同的 system prompt
策略（五类 QA 分策略回答）和相同的评估流水线。

**trpc-agent-go（优化版）— Auto 提取 + pgvector 混合检索：**

- **存储**：对话 turn 经 LLM 自动提取为结构化 fact/episode（包含
  content、metadata、event_time 字段），写入 pgvector。
- **存储消息角色**：后台提取器的 `ExtractionContext.Messages`
  **同时包含 user 和 assistant 两种角色的消息**（不含 tool call），
  因此对话双方的内容均可用于 LLM 记忆提取
- **检索**：Agent 通过 `memory_search` 工具调用发起 pgvector
  混合检索（向量相似度 + 关键词匹配），返回 top-30 条结构化记忆
- **QA 流程**：3 次 LLM 调用（Step 1 生成搜索 #1 的 tool call →
  Step 2 生成搜索 #2 的 tool call → Step 3 读取全部检索结果后回答）
- **优势**：提取后的记忆更精准、信息密度高；混合检索兼顾语义和
  关键词匹配
- **Token 特征**：tool-call 模式导致每步重读前序上下文，名义
  prompt token 为 ~17,182/QA。但**其中 43.9% 命中了提供商的
  prompt cache**（OpenAI `cached_tokens`），实际*新增* prompt
  成本仅 ~9,663 tokens/QA——按计费口径（大多数提供商 cache
  token 按 50% 计费）已可与单次调用方案相当
- **问题**：结构化 JSON 格式增加序列化开销；多步延迟高于
  单次调用模式

**AutoGen — ChromaDB 原始 turn 存储 + 单次 LLM 调用：**

- **存储**：原始对话 turn 以 `[SessionDate: ...] Speaker: text`
  格式直接存入 ChromaDB，仅做 embedding，不做 LLM 提取。
- **存储消息角色**：框架不自动存储——`ChromaDBVectorMemory.add()`
  是纯手动 API，由调用方决定存储内容。本评测中我们手动逐条
  `add()`，不区分 role
- **检索**：`AssistantAgent.run()` 前，`ChromaDBVectorMemory.
  update_context()` 自动以 question 为 query 检索 top-30 结果
  （score ≥ 0.3），作为 `SystemMessage` 注入 model context
- **QA 流程**：**1 次 LLM 调用**——检索结果在调用前已预注入，
  无需 tool call
- **优势**：调用次数最少（1 call/QA），token 效率最高
  （1,943 tokens/QA）
- **问题**：adversarial F1 仅 0.272（所有框架最低），对抗鲁棒性
  严重不足；依赖 ChromaDB 纯向量搜索，缺少关键词/BM25 补充

**CrewAI — ShortTermMemory + Crew 两步调用：**

- **存储**：原始对话 turn 存入 CrewAI 内置
  `ShortTermMemory`（底层为 ChromaDB 向量库），不做 LLM 提取。
- **存储消息角色**：框架存储的是**任务级执行摘要**（task
  description + agent role + expected output + 最终结果文本），
  而非逐条消息。本评测中我们绕过了框架的自动存储，手动逐条
  `stm.save()` 存入
- **检索**：通过 monkey-patch `ContextualMemory._fetch_stm_context`
  扩大检索窗口至 top-30（默认仅 top-5），格式化为
  `- [content]` 列表注入 agent 上下文
- **QA 流程**：2 次 LLM 调用——Call 1 为 Crew 内部
  formatting/planning，Call 2 带记忆上下文回答
- **优势**：存储简单（无 LLM 提取成本），检索结果格式紧凑
- **问题**：向量检索召回不足；Crew 的 Call 1（planning 步骤）
  是纯框架开销，贡献了 ~140 completion tokens/QA 但无 F1
  收益；adversarial 和 temporal 类别丢失率分别达 44.6% 和 39.6%

**ADK — InMemoryMemoryService + LoadMemoryTool 全量加载：**

- **存储**：对话 turn 作为 `Event` 存入 ADK
  `InMemoryMemoryService`（纯内存，无持久化）。
- **存储消息角色**：`add_session_to_memory()` 存储**所有**含
  `content.parts` 的 event，不按 author 过滤——**user、model、
  tool 等全类型 event 均被存储**
- **检索**：Agent 通过 `LoadMemoryTool` 工具调用加载记忆——
  **不做任何选择性检索，将全部记忆无差别注入上下文**
- **QA 流程**：2 次 LLM 调用（Step 1 调用 LoadMemoryTool →
  Step 2 读取全部记忆后回答）
- **优势**：不丢失任何记忆信息
- **问题**：**token 消耗灾难性膨胀**（49,224 tokens/QA，
  是优化版的 2.9 倍）；9 个 QA 超过 128K tokens 导致上下文
  溢出；10 个 QA 返回空预测；最大单 QA 达 252,849 tokens

**Agno — LLM 事实提取 + SQLite 全量注入：**

- **存储**：每个对话 turn 经 `MemoryManager` 调用 LLM 提取
  事实/偏好，存入 SQLite 数据库（有 LLM 提取成本，但不计入
  QA token 统计）。
- **存储消息角色**：`make_memories()` **仅处理 user message**，
  不含 assistant 或 tool 消息。`create_or_update_memories()` 内部
  也显式过滤 `m.role == 'user'`
- **检索**：`add_memories_to_context=True` 将**所有**已存储记忆
  无差别注入 system prompt 的
  `<memories_from_previous_interactions>` 标签中，不做向量搜索或
  相似度过滤
- **QA 流程**：1 次 LLM 调用（记忆已在 system prompt 中）
- **优势**：LLM 提取保留了关键事实
- **问题**：**全量注入导致 10,436 tokens/QA**；延迟最高
  （14,127ms/QA，总耗时 7h47m）；底层 DB 预留的
  `limit`/`topics` 过滤参数从未
  被 `MemoryManager` 使用，是设计缺陷

**方案对比总结：**

| 维度 | Session Recall | trpc-agent-go (优化版) | AutoGen | CrewAI | ADK | Agno |
| --- | --- | --- | --- | --- | --- | --- |
| 存储消息角色 | user + assistant 原始 session event | user + assistant 抽取成结构化 memory | 不自动存储（手动 API） | 任务级摘要（输入+输出） | 全部 event（user+model+tool） | 仅 user（assistant 被排除） |
| 评测 turn 映射 | Speaker[0]→user, [1]→assistant | Speaker[0]→user, [1]→assistant | 逐条 turn 手动 add() | 逐条 turn 手动 save() | 逐条 turn→Event, 整 session 写入 | 逐条 turn→create_user_memories() |
| 存储方式 | 原始 session events | LLM 提取结构化 memory | 原始 turn | 原始 turn | 原始 turn | LLM 提取事实 |
| 检索方式 | 对 session events 做 hybrid RRF，单次 preload | 向量+关键词 hybrid + tool call | 纯向量 top-30 | 纯向量 top-30 | **全量加载** | **全量注入** |
| LLM 调用/QA | 1（preload） | 3（tool call） | **1**（预注入） | 2（Crew 内部） | 2（tool call） | 1（预注入） |
| Tokens/QA | 3,694（有效 3,567†） | 17,182（有效 9,663‡） | **1,943** | 2,839 | 49,224 | 10,436 |

> † Session Recall 的 cache 命中率为 3.7%，实际*新增* token
> 成本约为 3,567/QA。
>
> ‡ 优化版有 43.9% 的 prompt tokens 命中提供商 prompt
> cache，实际*新增* token 成本约为 9,663/QA。
>
> 核心发现：**检索策略是区分效果的关键**。全量加载（ADK/Agno）
> 浪费 token 且效果不佳；选择性检索（Session Recall / 优化版 /
> AutoGen / CrewAI）的效果显著更好。在这些选择性检索方案里，
> Session Recall 现在在保持低 token 档位的同时给出了最高的总体
> 质量，而优化版则仍是更偏抽取式、tool-driven 的另一条路线。

### 4.3 总体结果

**表 7：Memory 场景——总体指标**

| 框架 | F1 | BLEU | LLM Score | Tokens/QA | 调用/QA | 延迟 | 总耗时 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| **trpc-agent-go (Session Recall)** | **0.549** | **0.511** | **0.609** | 3,694† | 1.0 | 6,430ms | 3h33m |
| trpc-agent-go (优化版) | 0.469 | 0.431 | 0.532 | 17,182‡ | 3.0 | 8,585ms | 4h44m |
| AutoGen | 0.457 | 0.414 | 0.540 | 1,943 | 1.0 | 3,816ms | 2h06m |
| CrewAI | 0.427 | 0.385 | 0.479 | 2,839 | 2.0 | 8,081ms | 4h27m |
| ADK | 0.362 | 0.309 | 0.476 | 49,224 | 2.0 | 5,578ms | 3h04m |
| trpc-agent-go (原版) | 0.399 | 0.371 | 0.416 | 3,056 | 2.0 | 6,659ms | 3h40m |
| Agno | 0.332 | 0.289 | 0.494 | 10,436 | 1.0 | 14,127ms | 7h47m |

> † Session Recall 的 cache 命中率为 3.7%，实际新增 token 成本
> 约为 ~3,567/QA。
>
> ‡ 优化版有 43.9% 的 prompt tokens 命中提供商 prompt
> cache，实际新增 token 成本仅 ~9,663/QA。详见 4.5 节。

> **LLM Score 聚合口径说明。** 所有框架均使用全样本分母
>（accuracy 口径：`sum(llm_score) / total_qa`）。Python 框架
> 的原始报告使用了 precision 口径（仅除以有评分的 QA 数），
> 因此 0.93 左右的值并不可直接对比，这里已统一修正。

```
Memory F1 (10 samples, 1986 QA)

trpc-agent-go (Session Recall) |====================================================| 0.549
trpc-agent-go (优化版)         |============================================        | 0.469
AutoGen                        |=========================================           | 0.457
CrewAI                         |========================================            | 0.427
trpc-agent-go (原版)           |=====================================               | 0.399
ADK                            |==================================                  | 0.362
Agno                           |===============================                     | 0.332
                               +----------------------------------------------------+
                               0.0      0.1      0.2      0.3      0.4      0.5
```

### 4.4 各类别 F1

**表 8：各类别 F1 对比**

| 类别 | Count | Session Recall | trpc-agent-go (优化版) | AutoGen | CrewAI | trpc-agent-go (原版) | ADK | Agno |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| single-hop | 282 | 0.368 | **0.396** | 0.377 | 0.322 | 0.316 | 0.299 | 0.240 |
| multi-hop | 321 | **0.554** | 0.453 | 0.512 | 0.380 | 0.096 | 0.418 | 0.283 |
| temporal | 96 | 0.174 | **0.247** | 0.176 | 0.140 | 0.088 | 0.120 | 0.076 |
| open-domain | 841 | **0.618** | 0.441 | 0.594 | 0.501 | 0.358 | 0.494 | 0.292 |
| adversarial | 446 | 0.610 | 0.626 | 0.272 | 0.448 | **0.814** | 0.163 | 0.556 |

**表 9：加权平均 F1**

| 平均方式 | Session Recall | trpc-agent-go (优化版) | AutoGen | CrewAI | trpc-agent-go (原版) | ADK | Agno |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 5 类加权 (÷1986) | **0.549** | 0.469 | 0.457 | 0.427 | 0.399 | 0.362 | 0.332 |
| 4 类加权 (÷1540) | **0.531** | 0.423 | 0.511 | 0.420 | 0.279 | 0.420 | 0.267 |

> 优化版相较原版依然有明显进步，尤其在 **single-hop** 和
> **temporal** 上改善显著；Session Recall 应被看作在这一内部
> 演进基础上的补充检索路径。
>
> 5 类加权 F1 方面，**Session Recall 以 0.549 排名第一**，
> 领先优化版（0.469）0.080，领先 AutoGen（0.457）0.092。
> 4 类加权 F1 也以 **0.531 排名第一**，超过 AutoGen 的 0.511
> 达 0.020，并显著领先其他 trpc-agent-go 方案和专用记忆系统。

### 4.5 Token 效率与延迟

**表 10：Token 效率对比**

| 框架 | F1 | Total Tokens | Tokens/QA | Cache 命中率 | 有效 Tokens/QA† | F1/十亿 Tokens |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| AutoGen | 0.457 | 3,859,412 | 1,943 | n/a | 1,943 | 118.4 |
| trpc-agent-go (Session Recall) | **0.549** | 7,353,057 | 3,694 | 3.7% | 3,567 | 74.6 |
| CrewAI | 0.427 | 5,639,085 | 2,839 | n/a | 2,839 | 75.7 |
| trpc-agent-go (原版) | 0.399 | 6,068,802 | 3,056 | n/a | 3,056 | 65.7 |
| trpc-agent-go (优化版) | 0.469 | 34,123,774 | 17,182 | **43.9%** | **9,663** | 13.7 |
| Agno | 0.332 | 20,725,728 | 10,436 | n/a | 10,436 | 16.0 |
| ADK | 0.362 | 97,759,453 | 49,224 | n/a | 49,224 | 3.7 |

> † **有效 Tokens/QA** = prompt tokens 减去 cached prompt tokens，
> 加上 completion tokens。Cached tokens 命中提供商的自动 prompt
> cache（如 OpenAI `cached_tokens`），通常按**标准 prompt 费率
> 的 50%** 计费。Python 框架的 SDK 不报告 `cached_tokens`，因此
> 它们的实际成本可能也低于表中所示；`n/a` 表示数据不可获取而非
> 无缓存。
>
> 从原始 token 数看，AutoGen 效率最高（118.4 F1/十亿 Tokens）。
> 优化版虽然名义 token 成本更高，但相较原版仍然代表了质量上的稳定
> 提升。**Session Recall 是当前 trpc-agent-go 内部最优的质量/成本
> 折中**：它以 3,694 tokens/QA 达到 0.549 F1，在显著低于
> Long-Context 和优化版 token 成本的同时，大幅超过它们的准确率。
> 优化版因为多步 tool-call 模式需要反复重读上下文，名义 token
> 显著更高；虽然 prompt cache 能缓解这部分成本，但在当前配置下
> Session Recall 仍然明显更轻量。ADK 效率最低——49,224 tokens/QA
> 仅获得 0.362 的 F1。

```
Total Evaluation Time (memory scenario, 1986 QA)

AutoGen            |====                                   | 2h06m
ADK                |======                                 | 3h04m
Session Recall     |=======                                | 3h33m
trpc (原版)        |========                               | 3h40m
CrewAI             |==========                             | 4h27m
trpc (优化版)      |==========                             | 4h44m
Agno               |===============================        | 7h47m
                   +----------------------------------------+
                   0h       2h       4h       6h       8h
```

**优化版为何更慢（4h44m vs 3h40m）：**

优化版消耗 5.6 倍的 tokens/QA（17,182 vs 3,056），单 QA 延迟增长
1.29 倍（8,585ms vs 6,659ms）。根因在于三步 Agent 工作流：

1. **Step 1 — 工具调用 #1**（~1,650 prompt tokens）：LLM 读取系统
   指令和问题后，发出第一次 `memory_search` 工具调用。这会产生一次
   LLM 往返加一次 pgvector 混合搜索（向量 + 关键词），包含 embedding
   生成。

2. **Step 2 — 工具调用 #2**（~5,900 prompt tokens）：LLM 重新读取
   所有前序上下文（系统 prompt + 问题 + 第一次工具调用 + 第一次工具
   结果），然后发出第二次 `memory_search` 工具调用以细化检索。

3. **Step 3 — 最终回答**（~10,000 prompt tokens）：LLM 重新读取完整
   对话历史（所有前序上下文 + 第二次工具调用 + 第二次工具结果），生成
   最终答案。

核心开销在于**累积上下文重读**：每一步都要重新处理所有前序步骤的内容。
仅 Step 3 就消耗了 ~10,000 prompt tokens。相比之下，原版使用 2 次调用
的 Agent 模式，但每次检索到的记忆条目更少更短（两步总计 ~3,056
tokens），因为原版存储的是原始对话 turn，而非提取后的结构化
fact/episode。

**Prompt cache 显著降低了实际成本：** 多步 tool-call 模式虽然反复
重读上下文，但恰恰因此具有极高的 cache 友好性——Step 2 和 Step 3
与前序步骤共享大量公共前缀。实际运行中，**43.9% 的 prompt tokens
（34.01M 中的 14.93M）命中了提供商的自动 prompt cache**，实际
新增 prompt 量仅为 ~19.08M tokens。按照标准 50% cache 定价，
实际可计费的 prompt 成本等效于 ~26.54M tokens 而非 34.01M——
比名义数字**低约 22%**。

尽管 token 成本更高，优化版的 F1/成本权衡显著更优：以 **5.6 倍
名义 token 成本**（计入 cache 折扣后远低于此）换取 **+17.5% F1
提升**（0.399→0.469），在重视回答质量的生产场景中是值得的。

### 4.6 ADK 失败分析

ADK（Google Agent Development Kit）使用纯内存后端，通过 Agent
工具调用（`LoadMemoryTool`）检索记忆。在本次评估中，ADK 在部分
样本上出现了上下文溢出问题：

**表 11：ADK 上下文溢出详情**

| 样本 | QA 数 | 空预测数 | >128K Tokens QA 数 | 最大单 QA Token |
| --- | ---: | ---: | ---: | ---: |
| conv-26 | 199 | 0 | 0 | 43,887 |
| conv-30 | 105 | 0 | 0 | 59,458 |
| conv-41 | 193 | 4 | 4 | 252,849 |
| conv-42 | 260 | 1 | 1 | 180,603 |
| conv-43 | 242 | 2 | 2 | 162,249 |
| conv-44 | 158 | 1 | 0 | 123,063 |
| conv-47 | 190 | 0 | 0 | 114,912 |
| conv-48 | 239 | 1 | 0 | 105,680 |
| conv-49 | 196 | 0 | 1 | 166,597 |
| conv-50 | 204 | 1 | 1 | 219,026 |
| **合计** | **1,986** | **10** | **9** | **252,849** |

- **10 个 QA（0.5%）返回空预测**，集中在对话历史较长的样本中
- **53 个 QA 的 token 用量超过 100K**，单次 QA 最高达到
  **252,849 tokens**——接近 GPT-4o-mini 的 128K 上下文窗口上限
- ADK 的 `LoadMemoryTool` 将**全部记忆**加载到上下文中，
  不做选择性检索，导致长对话场景下严重的 token 浪费
- 平均 49,224 tokens/QA 是所有框架中最高的，但 F1 仅 0.362

### 4.7 各样本 F1

**表 12：各样本 F1 对比**

| 样本 | QA 数 | Session Recall | trpc-agent-go (优化版) | AutoGen | CrewAI | trpc-agent-go (原版) | ADK | Agno |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| conv-26 | 199 | **0.530** | 0.432 | 0.384 | 0.355 | 0.331 | 0.337 | 0.296 |
| conv-30 | 105 | **0.636** | 0.422 | 0.451 | 0.439 | 0.302 | 0.379 | 0.334 |
| conv-41 | 193 | **0.644** | 0.521 | 0.513 | 0.440 | 0.432 | 0.335 | 0.387 |
| conv-42 | 260 | **0.482** | 0.447 | 0.439 | 0.408 | 0.378 | 0.343 | 0.338 |
| conv-43 | 242 | **0.542** | 0.436 | 0.486 | 0.413 | 0.451 | 0.355 | 0.341 |
| conv-44 | 158 | **0.553** | 0.505 | 0.491 | 0.509 | 0.455 | 0.384 | 0.289 |
| conv-47 | 190 | **0.530** | 0.487 | 0.496 | 0.405 | 0.407 | 0.374 | 0.321 |
| conv-48 | 239 | **0.563** | 0.492 | 0.463 | 0.432 | 0.404 | 0.392 | 0.328 |
| conv-49 | 196 | **0.508** | 0.464 | 0.418 | 0.407 | 0.383 | 0.371 | 0.302 |
| conv-50 | 204 | **0.562** | 0.478 | 0.475 | 0.487 | 0.407 | 0.363 | 0.374 |
| **平均** | **199** | **0.549** | 0.469 | 0.457 | 0.427 | 0.399 | 0.362 | 0.332 |

> Session Recall 在 10 个样本中的全部 10 个上超过 AutoGen。

---

## 5. 与外部记忆系统对比

数据来源：Mem0 论文 Table 1（Chhikara et al., 2025,
arXiv:2504.19413）。所有系统均使用 GPT-4o-mini。为跨系统可比性，
已排除 adversarial 类别（Mem0 论文未包含该类别）。

本节使用论文公开的 LoCoMo 数字，与 3.4 节在同一次运行中直接比较
self-hosted mem0 的实验相互独立。两者使用不同数据集和模型协议，
不能混用绝对分数。

> **关于表中"LoCoMo（论文基线）"的说明。** LoCoMo 既是本报告
> 使用的数据集，也是 LoCoMo 论文（Maharana et al., 2024）中
> 提出的一套记忆系统方案。该方案使用 LLM 从对话中提取事件和
> 摘要，在推理时通过 BM25 + 语义搜索组合检索。Mem0 论文在同一
> 数据集上复现了该方案并报告了 F1 数据，因此表中以"LoCoMo
> （论文基线）"标注，表示这是 LoCoMo 论文自带的记忆方案的得分，
> 而非数据集本身。

**表 13：各类别 F1（不含 adversarial）**

| 方法 | Single-Hop | Multi-Hop | Open-Domain | Temporal | 4 类加权 | 来源 |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| AutoGen | 0.377 | 0.512 | 0.594 | 0.176 | 0.511 | 本工作 |
| **trpc-agent-go (Session Recall)** | 0.368 | **0.554** | **0.618** | 0.174 | **0.531** | 本工作 |
| trpc-agent (优化版) | **0.396** | 0.453 | 0.441 | 0.247 | 0.423 | 本工作 |
| Mem0g | 0.381 | 0.243 | 0.493 | **0.516** | 0.422 | Mem0 论文 |
| Mem0 | 0.387 | 0.286 | 0.477 | 0.489 | 0.421 | Mem0 论文 |
| CrewAI | 0.322 | 0.380 | 0.501 | 0.140 | 0.420 | 本工作 |
| trpc-agent (LC) | 0.320 | 0.308 | 0.518 | 0.088 | 0.411 | 本工作 |
| ADK | 0.299 | 0.418 | 0.494 | 0.120 | 0.420 | 本工作 |
| Zep | 0.357 | 0.194 | 0.496 | 0.420 | 0.403 | Mem0 论文 |
| LangMem | 0.355 | 0.260 | 0.409 | 0.308 | 0.362 | Mem0 论文 |
| A-Mem | 0.270 | 0.121 | 0.447 | 0.459 | 0.347 | Mem0 论文 |
| OpenAI Memory | 0.343 | 0.201 | 0.393 | 0.140 | 0.328 | Mem0 论文 |
| MemGPT | 0.267 | 0.092 | 0.410 | 0.255 | 0.308 | Mem0 论文 |
| LoCoMo（论文基线） | 0.250 | 0.120 | 0.404 | 0.184 | 0.303 | Mem0 论文 |
| trpc-agent (原版) | 0.316 | 0.096 | 0.358 | 0.088 | 0.279 | 本工作 |
| Agno | 0.240 | 0.283 | 0.292 | 0.076 | 0.267 | 本工作 |
| ReadAgent | 0.092 | 0.053 | 0.097 | 0.126 | 0.089 | Mem0 论文 |
| MemoryBank | 0.050 | 0.056 | 0.066 | 0.097 | 0.063 | Mem0 论文 |

```
4-Category Weighted F1 (excluding adversarial, 1540 QA)

Session Recall      |============================================| 0.531
AutoGen             |==========================================  | 0.511
trpc-agent (优化版) |==================================          | 0.423
Mem0g               |==================================        | 0.422
Mem0                |==================================        | 0.421
CrewAI              |=================================         | 0.420
ADK                 |=================================         | 0.420
trpc-agent (LC)     |=================================         | 0.411
Zep                 |================================          | 0.403
LangMem             |=============================             | 0.362
A-Mem               |===========================               | 0.347
OpenAI Memory       |==========================                | 0.328
MemGPT              |========================                  | 0.308
LoCoMo (baseline)   |========================                  | 0.303
trpc-agent (原版)   |======================                    | 0.279
Agno                |====================                      | 0.267
                    +------------------------------------------+
                    0.0      0.1      0.2      0.3      0.4   0.5
```

> **含 adversarial 的 5 类加权 F1**（仅限有 adversarial 数据的框架）：
>
> | 方法 | 5 类加权 F1 |
> | --- | ---: |
> | **trpc-agent-go (Session Recall)** | **0.549** |
> | trpc-agent (优化版) | 0.469 |
> | AutoGen | 0.457 |
> | CrewAI | 0.427 |
> | trpc-agent (原版) | 0.399 |
> | ADK | 0.362 |
> | Agno | 0.332 |

**核心发现：**

1. **trpc-agent-go（Session Recall）** 的 4 类加权 F1 达到
   **0.531**，排名 **第一**，超过 AutoGen（0.511）0.020，
   并显著超过 Mem0g（0.422）、Mem0（0.421）、Zep（0.403）、
   LangMem（0.362）、A-Mem（0.347）等专用记忆系统
2. **open-domain 与 multi-hop 成为突出强项。**
   Session Recall 在 **multi-hop**（0.554）和
   **open-domain**（0.618）上都达到第一
3. **优化版仍然是互补方案。** 它在 **temporal**
   （0.247）和 adversarial（0.626）上仍是 trpc-agent-go
   内部最强，但总体 4 类加权 F1（0.423）明显低于 Session Recall
4. **Token 效率显著改善。** Session Recall 将 nominal
   Tokens/QA 从优化版的 17,182 和 Long-Context 的
   18,776 直接降到 **3,694**
5. 相比原始基线，优化版先将 trpc-agent-go 的 4 类加权 F1
   从 0.279 提升到 0.423，而 Session Recall 又进一步将其推到
   0.531

---

## 6. 结论

### 核心发现

1. **trpc-agent-go 的 Session Recall 已成为当前最强配置。**
   它在 **5 类加权 F1 上排名第一**（**0.549**），4 类加权 F1
   也以 **0.531** 排名第一，并超过 AutoGen。相比 Long-Context
   和优化版，它在更低 token 成本下给出了更高的总体 F1。

2. **历史 LoCoMo 产物显示出检索策略差异，但仍需 v4 确认。**
   Session Recall 在保存的产物中于 **open-domain** 和
   **multi-hop** 上最强，且其协议不受影响。优化版 Auto 在
   **temporal** 和 adversarial 上的表观优势来自 legacy replay-v3，
   不能作为当前证据；Long-Context 仍是未受影响的短单 session
   参考。

3. **Opt-in pgvector candidate 在固定 LongMemEval 开发回归中领先
   upstream main 和 self-hosted Mem0。** Protocol v2 下，candidate
   达到 16/16、48/48 个正确 answer replicate；main 为 11/16、33/48，
   Mem0 为 14/16、42/48。Assistant-result extraction 修复 assistant-history
   recall，query-aware provenance RRF 则防止这些结果在普通查询中挤掉
   user-grounded 证据。直接消融否定了单独的普通 history-preserving update
   policy。本协议尚未运行新的未见 holdout。

4. **trpc-agent-go 已明显超越专用记忆系统。** Session Recall 的
   4 类加权 F1 达到 0.531，显著高于 Mem0g（0.422）、Mem0
   （0.421）、Zep（0.403）、LangMem（0.362）、A-Mem（0.347）、
   OpenAI Memory（0.328）、MemGPT（0.308）等专用记忆系统。

5. **其他 Python 框架的局限性。**

   - **ADK**：token 消耗最为严重（49,224 tokens/QA），是优化版的
     **2.9 倍**，但 F1 仅 0.362。其 `LoadMemoryTool` 将全部记忆
     无差别加载到上下文中，导致长对话场景下严重的 token 浪费和
     上下文溢出（9 个 QA 超过 128K tokens），架构上缺乏选择性
     检索能力
   - **Agno**：F1 最低（0.332），延迟最高（14,127ms/QA，总耗时
     7h47m），且 token 消耗达 10,436/QA。与 ADK 类似，Agno 也采用
     全量加载架构——将用户的所有记忆无差别注入到 system prompt 的
     `<memories_from_previous_interactions>` 标签中，不支持向量检索
     或相似度搜索。虽然底层 DB 接口预留了 `limit`、`topics` 等
     过滤参数，但 `MemoryManager` 在实际运行中从未使用这些能力
   - **CrewAI**：其短期记忆后端存在记忆丢失问题，尤其在
     adversarial（44.6%）和 temporal（39.6%）类别上丢失比例最高
   - **AutoGen**：4 类加权 F1 达到 0.511，但其高分主要依赖
     open-domain 单一类别的突出表现（0.594）；在 adversarial 上
     仅 0.272，为所有框架最低，对抗鲁棒性严重不足

6. **Memory 仍然是生产 Agent 的必需能力。** Long-Context 在单
   session 短对话中有效，但无法跨 session 持久化知识，也无法扩展到
   超过模型上下文窗口的历史。Session Recall 提供了更好的质量/成本
   平衡，而优化版则提供了基于抽取式持久化 memory 的第二种
   路线。

7. **下一步重点是未见泛化，而不是继续调整已观察集合。** 当前 candidate
   已修复所有已观察 LongMemEval 开发 gap，保持 48/48，同时使用 main 的
   1.3866 倍 memory token、1.6937 倍 embedding token 和 2.2057 倍最终
   memory。LoCoMo replay 只证明新组件在一个冻结快照上不活跃，并不证明泛化。
   继续针对这些已观察 ID 调整质量规则会增加过拟合风险。下一步应进入预注册的
   未见 full-haystack holdout。

### 生产建议

| 使用场景 | 推荐方案 |
| --- | --- |
| 短对话单 session（< 50K tokens） | Long-Context（无需记忆） |
| 跨 session QA / 以准确率优先 | Session Recall |
| 需要持久化抽取 memory 的长期运行 Agent | 优化版 pgvector auto memory |
| 历史超出上下文窗口限制 | Session Recall 或优化版 |
| Memory 回归开发 | 固定已观察 Oracle 集 + 保存的 stage-level trace |
| Candidate 晋级 | 预注册未见 full-haystack holdout + LoCoMo 回归 gate |

---

## 附录

### A. 实验环境

| 组件 | 版本/配置 |
| --- | --- |
| 框架 | trpc-agent-go |
| 模型 | GPT-4o-mini（LoCoMo）；glm52（LongMemEval） |
| Embedding | text-embedding-3-small |
| PostgreSQL | 15+ with pgvector extension |
| 数据集 | LoCoMo-10（10 样本，1,986 QA）；LongMemEval Oracle（固定已观察 16 题开发回归集） |
| 对比后端 | 固定 self-hosted Mem0 OSS runtime + pgvector（LongMemEval） |

### B. 完整类别详情（F1 / BLEU / LLM）

| 场景 | single-hop | multi-hop | temporal | open-domain | adversarial |
| --- | --- | --- | --- | --- | --- |
| Long-Context | 0.320/0.251/0.320 | 0.308/0.273/0.260 | 0.088/0.068/0.165 | 0.518/0.457/0.662 | 0.667/0.667/0.668 |
| Session Recall | 0.368/0.304/0.445 | 0.554/0.512/0.563 | 0.174/0.138/0.311 | 0.618/0.570/0.715 | 0.610/0.610/0.608 |
| 优化版 | 0.396/0.325/0.395 | 0.453/0.415/0.519 | 0.247/0.192/0.364 | 0.441/0.398/0.552 | 0.626/0.626/0.626 |
| 原版 | 0.316/0.250/0.270 | 0.096/0.088/0.060 | 0.088/0.068/0.115 | 0.358/0.319/0.425 | 0.814/0.814/0.814 |

### C. Token 消耗——完整数据

| 场景 | Prompt Tokens | Completion Tokens | Total Tokens | LLM 调用 | 调用/QA |
| --- | ---: | ---: | ---: | ---: | ---: |
| Long-Context | 37,272,167 | 16,104 | 37,288,271 | 1,986 | 1.0 |
| Session Recall | 7,336,165 | 16,892 | 7,353,057 | 1,986 | 1.0 |
| 优化版 | 34,007,814 | 115,960 | 34,123,774 | 5,981 | 3.0 |
| 原版 | 6,011,025 | 57,777 | 6,068,802 | 3,999 | 2.0 |
| AutoGen | 3,842,576 | 16,836 | 3,859,412 | 1,986 | 1.0 |
| CrewAI | 5,360,840 | 278,245 | 5,639,085 | 3,972 | 2.0 |
| Agno | 20,694,534 | 31,194 | 20,725,728 | 1,986 | 1.0 |
| ADK | 97,691,620 | 67,833 | 97,759,453 | 4,028 | 2.0 |

### D. LongMemEval 复现与 Provenance

精确 question ID 固定在 run manifest 中，不会在重跑时重新抽样。Primary
实验臂的命令形状如下：

```bash
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -dataset ../../summary/data/longmemeval-cleaned/longmemeval_oracle.json \
  -lme-question-ids "$FROZEN_QUESTION_IDS" \
  -memory-backend pgvector,mem0 \
  -pgvector-update-policy reconcile \
  -pgvector-assistant-result-extraction=false \
  -mem0-llm-temperature 0 \
  -model glm52 \
  -eval-model glm52 \
  -embed-model text-embedding-3-small \
  -lme-judge-runs 3 \
  -lme-answer=true \
  -vector-topk 30 \
  -output ../results/lme-observed-dev16-main-mem0
```

Candidate pgvector 复用完全相同的 ID 与 protocol，并设置
`-pgvector-update-policy reconcile` 和
`-pgvector-assistant-result-extraction=true`。接受的三臂 baseline 使用默认
reconcile，并保持不可变。另外两次基于保存 retrieval 的 re-answer 使用新的独立
answer ledger；所有结果都对每个答案执行三票 judge。一个不完整 replicate 按
预先冻结的规则对所有实验臂、所有 case 完整替换。最终 candidate 只从接受的
candidate memory 快照刷新 retrieval，然后执行三轮新 answer/judge。原 replicate
manifest 在运行前冻结质量和成本 gate。

| Provenance 项 | Digest 或 revision |
| --- | --- |
| 正式三臂 benchmark | `f7cf9370057daa382db925ca67500b9f66f173da` |
| Compact 消融 benchmark | `8eb0bac316ee67938ab6ecb6052ff227f94363e0` |
| pgvector main | `0c7774187da9330144df2a038ef18ee89ef2ae1c` |
| pgvector parent candidate | `0797067f40743fbe789eff65315d74b05b7c454c` |
| pgvector 三臂 candidate | `bd6b31f92a904023df0c77c6762fa95b5e359456`（评测 tree：`eaf5f49f1fa47856ff919798bcc93a41be71f6ec`） |
| pgvector prompt-compaction candidate | `969fb16a918d6abae8bb06d52cb784490c8a2eb4` |
| pgvector 最终 reconcile candidate | `2432019572845c182d37a2872f056a6e7bee33c7` |
| 完整 reconcile 配置 benchmark | `536b0979345e607bc06e6975040c7f51336a6abe` |
| 简化实验 benchmark | `126a585b6a68530d5ec17d9c69eee33317adbf12` |
| Dataset SHA-256 | `821a2034d219ab45846873dd14c14f12cfe7776e73527a483f9dac095d38620c` |
| Selection SHA-256 | `b10651ad0caa76696a2d885da060969d0d24d2e1cdba4130308ef745f95621fb` |
| Protocol SHA-256 | `9b001708920522d7ad2cd477824208b5692eb52bcd1c205e46fb9fbb5b57b9a4` |
| Replicate manifest SHA-256 | `7baecdce61be140d5cbe3163519b8ee5503eaafdca842f37c087417e297871d2` |
| Aggregate SHA-256 | `fb5e37a2327d00802055e388c2125f564c402ccbb1261fbfffc486f8f7819974` |
| Audit SHA-256 | `17ae45dc27ffc3d89f8f1c244ac420ba73c1b9aa741fbe52c7a45cb71e2e158b` |
| Compact 消融 Audit SHA-256 | `d48ae6d6731c45ae05bc52c753df331cf204a38150896b748d7d1ac0db071981` |
| Assistant-prompt 正式 gate SHA-256 | `f82f35299d319e64a30a32a24b022aceef7e90a308f687275dffd679c9d8f335` |
| Assistant-prompt 质量诊断 SHA-256 | `902629cfdf1a924282c58300e93afacdda7a9c3c044afdc342762de5384755fa` |
| 新鲜 LoCoMo prompt 配对 audit SHA-256 | `64963ebd8b481873012631a85adb21fc1aa9d87b6491accb751a7b8d945a5d2c` |
| 固定 memory LoCoMo 重复 audit SHA-256 | `4bb1d7606099029d2cbb8ed00600ef2a38570b0bbac89ccffe2bc540d9632fbf` |
| 工程综合 audit SHA-256 | `7298a75fc436e90d84d6adcbf06cee779120fd7450ac8d10297b51b50d3423a5` |
| 最终 reconcile dev16 audit SHA-256 | `92585f5c1dd67983ed241c9ae55c885183409163d276e58b33d267b6e5ab952c` |
| 最终 reconcile dev16 checksum manifest SHA-256 | `aadc7233009a05ea6d49bfca406b049d1b5a6e2b8556960df7fa6273f45558cb` |
| LoCoMo update-policy audit SHA-256 | `15e16594cfe59cb30883c4d91911b81384d501e0389591fb0cf4806cc2cfbdd8` |
| LoCoMo update-policy checksum manifest SHA-256 | `292ff0a81b805978e7822ff5ee2b6a0bb5b22c10fdf52ee7c8976314d6017a61` |
| 最终 implementation-smoke audit SHA-256 | `0415aa9bdb973f178aadd4d83cc8db0c3caaa157d395663627a56cca2d8765aa` |
| 最终 implementation-smoke checksum manifest SHA-256 | `7a3f0b0d5e31b2dfb4880769f98f10aea62673e6f0862e1882614040b5ce6a92` |
| 接受的 replacement-manifest comparison SHA-256 | `38bd9117ef320d1d76d7ee833030e51ab2db37a589165ff1ff03b3dcffec707b` |
| 接受的 replacement audit SHA-256 | `6c64bb71f90fd5a56c2e8f3b004f9214b665e5b361d37a7846091331f3f0974a` |
| Provenance-ranking candidate | `22455426803a478535fae28a6c8c103b4f8668c7` |
| Provenance-ranking robust audit SHA-256 | `18d842890676130d3f332cbaeb37c2df48f5f017d56db0ff62f87497e8a78861` |
| 收紧 classifier 的 equivalence audit SHA-256 | `bf61ac7a005981354ac201fa2262bea0e41063096d1a252af75af596f40ac3b2` |
| LoCoMo provenance replay audit SHA-256 | `66c748d28c860832a5e28bfef2bf00201973a9aaff058153df219f40b565e898` |
| Mem0 | source `b05cce58`、runtime `9d027353`、image `81d80e337521` |

Audit 校验精确 build、完整 provider usage、零错误、隔离 store、每个实验臂
16 个 case 和 183 个 pair，并记录 `blind_holdout_authorized=false`。原始
model trace 与 store 保留为本地评测制品，报告只包含聚合数据和阶段诊断。

---

## 参考文献

1. Maharana, A., Lee, D., Tulyakov, S., Bansal, M., Barbieri, F., and Fang, Y. "Evaluating Very Long-Term Conversational Memory of LLM Agents." arXiv:2402.17753, 2024.
2. Wu, D., Wang, H., Yu, W., Zhang, Y., Chang, K.-W., and Yu, D. "LongMemEval: Benchmarking Chat Assistants on Long-Term Interactive Memory." arXiv:2410.10813, 2024.
3. Chhikara, P., Khant, D., Aryan, S., Singh, T., and Yadav, D. "Mem0: Building Production-Ready AI Agents with Scalable Long-Term Memory." arXiv:2504.19413, 2025.
4. Hu, C., et al. "Memory in the Age of AI Agents." arXiv:2512.13564, 2025.
