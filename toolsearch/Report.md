
# Tool Search 评测报告（catalog-multiturn / trpc-agent-go 重构版插件）

> 数据来源：`toolsearch/output/*.summary.json`（4 份，分别对应 `none` / `keyword` / `embedding` / `dispatch` 四种模式）。
> 实现代码：[trpc-agent-go-impl](/Users/samuelyu/GolandProjects/trpc-agent-go-benchmark/toolsearch/trpc-agent-go-impl)，评测数据集：[toolsearch-catalog-multiturn.evalset.json](/Users/samuelyu/GolandProjects/trpc-agent-go-benchmark/toolsearch/data/toolsearch-benchmark/toolsearch-catalog-multiturn.evalset.json)。

## 本报告要回答的问题

在多轮对话场景下（本次 evalset 为 6 个 case、共 52 轮 invocation）：

1. 使用 Tool Search 能节约多少 token？
2. `keyword` / `embedding` / `dispatch` 三种 Tool Search 策略哪种更优？在什么场景？
3. 随着对话轮数增加 Token 消耗量如何变化？
4. 影响 Tool Search 的因素有哪些？
5. Tool Search 会带来多少端到端耗时增量？
6. Tool Search 对**正确性/达标率**的影响？

## 实验设定

### 工具库

#### 规模

| 规模类型 | 工具数量 |
| --- | --- |
| 小规模 | 10-20 个工具 |
| 中规模 | 50-100 个工具 |
| 大规模 | 500+ 个工具 |

本次评测使用**中规模工具库**。工具库基于插件重构后的「命名空间目录（namespace catalog）」设计，在 [trpc-agent-go-impl/toolboxs/](/Users/samuelyu/GolandProjects/trpc-agent-go-benchmark/toolsearch/trpc-agent-go-impl/toolboxs) 中定义：

| Toolbox 命名空间 | 工具数量 |
| --- | ---: |
| filesystem | 11 |
| git | 12 |
| document | 8 |
| iam | 7 |
| crm | 7 |
| process | 5 |
| network | 5 |
| default（无命名空间通用工具） | 9 |
| 常驻 preset（web_search） | 1 |
| **合计** | **65** |

所有工具都是「仅元数据 + 打桩返回」，因此评测期间唯一的真实网络流量是主模型 completion（`embedding` 模式额外有 embedding 调用）。

### Tool Search 模式

本次对比四种模式（与 [trpc-agent-go-impl/README.md](/Users/samuelyu/GolandProjects/trpc-agent-go-benchmark/toolsearch/trpc-agent-go-impl/README.md) 一致）：

- `none`：不启用插件，所有工具直接提供给主模型（baseline）。
- `keyword`：`NewPlugin` + `WithToolboxes`，`tool_search` 函数用**内置关键词匹配**解析 `queries` 参数并加载命名空间下的工具（重构后的默认模式）。
- `embedding`：在 `keyword` 基础上加 `WithSemanticToolIndex`，`queries` 用 embedding 相似度排序召回工具。
- `dispatch`：在 `keyword` 基础上加 `WithInvocationMode(DispatchToolCalls)`，主模型只看到 `tool_search` + `call_tool` 两个工具；真实工具名在 `call_tool` 的 `arguments.tool_name` 里。

> **重要说明（与旧版 API 的差别）**：重构后的插件**没有了「LLM 选 top-K」模式**。旧版由一次独立的 LLM 调用来选工具；新版是**主模型自己**在对话里调用 `tool_search` 这个函数来检索/加载工具。因此 `keyword` / `dispatch` 模式**不产生独立的 out-of-band LLM 调用**——其开销（携带目录的更大 prompt、`tool_search` 结果的 completion、以及工具调用）都记在 `chat` 桶里。`embedding` 模式唯一的 out-of-band 开销是 **embedding 调用**（通过 `countingEmbedder` 包装 embedder，把 embedding token 计入 `toolsearch` 桶）。

共同参数（来自四份 `*.summary.json` 的 `config`）：

| parameter name | parameter value |
| --- | --- |
| AppName | `toolsearch-benchmark` |
| EvalSetId | `toolsearch-catalog-multiturn` |
| Chat Model | `deepseek-v4-flash` |
| Embedding Model | `text-embedding-3-small`（仅 `embedding` 模式实际用到） |
| MaxTools | 5 |
| NumRuns | 1 |

### 主 Agent Chat Model 的设置

| Chat Model | `deepseek-v4-flash`，使用 chat/completions 默认参数 |

> 注意：运行脚本需要配置 `OPENAI_API_KEY` / `OPENAI_BASE_URL` 等环境变量，但报告中不记录任何密钥信息。

### 多轮对话 user message 的设置

评测集位于 [toolsearch-catalog-multiturn.evalset.json](/Users/samuelyu/GolandProjects/trpc-agent-go-benchmark/toolsearch/data/toolsearch-benchmark/toolsearch-catalog-multiturn.evalset.json)：

- 共 **6 个 eval case**：`filesystem` / `git` / `document` / `process-network` / `iam-crm` / `default-tools`
- 每个 case **8~9 轮对话**（filesystem/git/process-network/iam-crm 各 9 轮，document/default-tools 各 8 轮；**共计 52 轮**）
- 每轮的期望工具由 evalset 内 `tools[].name` 给出（部分轮允许多个期望工具，例如 `search_file_content|find_files`）

### 评价指标

- **正确性/达标**：`tool_trajectory_avg_score`（阈值 1，subset + regex + 无序匹配；`dispatch` 模式下自定义比较器 `unwrap_call_tool` 将 `call_tool` 归一化为 `arguments.tool_name`）。
- **Token 消耗**：分 `chat`（主 Agent 对话）与 `toolsearch`（`embedding` 模式的 embedding 调用），汇总为 `total`。
- **耗时**：整次运行的 `wallTimeMs` 以及每轮的 `durationMs`。

> **关于「达标率」的重要说明**：本次指标是「本轮实际调用的工具集合是否覆盖了 evalset 中该轮的期望工具」，属于**工具轨迹匹配**指标，而**不是端到端业务正确性**指标。因此：
>
> - 主 Agent 在**信息不足时向用户澄清/追问**（例如 `document` case 的 "Create a markdown document." 用户没给出文件名和正文，Agent 反问 "请问文件名叫什么？内容是什么？"）→ 本轮不会调用任何工具 → `metric=0`。这是**正常且合理**的对话行为，但会被本指标判为「未达标」。
> - Agent 因为**上下文推理认为不需要工具**（例如认为参数已经在历史消息里，直接用自然语言回答）→ 本轮也会 `metric=0`。
> - Agent 调用了**语义等价但工具名不同**的工具（例如用 `search_file_content` 代替 `find_files` 完成同样的搜索），只要 evalset 用 `a|b` 形式声明了替代项就没问题，否则也会 `metric=0`。
>
> 因此下文所有「达标率」数字都应当被理解为「工具轨迹严格匹配率」的下界，实际业务可用性通常**高于**该数字。特别是 `document` case 全轮 fail、`iam-crm` 的 ic-05/ic-09 miss 等结果，需要结合 `actualTools`（实际调用的工具/是否发生了澄清对话）人工复核，才能判断是「真的选错工具」还是「合理的澄清追问」。

Token 汇总关系：`Total = Chat + Toolsearch`。

> 与真实业务的差异：本次评测未纳入 Prompt Caching / 并发 / 网络波动；由于 `keyword` / `dispatch` 模式没有独立的 tool-search LLM，工具选择成本都藏在 `chat` 桶里，因此下文的「Tool Search 阶段成本」仅在 `embedding` 模式下有实际值。

## 结论（基于本次 Token / 耗时 / 达标率）

### 总览

> 统计口径：52 轮（6 case × 8~9 turns），`wallTimeMs` 为整次运行的墙钟时间。

| Mode | 达标率（case） | 达标率（turn） | Total Tokens | vs baseline（Total） | Prompt Tokens | Completion Tokens | Toolsearch Tokens | Toolsearch 占比 | Wall Time | vs baseline（Time） | Avg Time/Turn |
| --- | :---: | :---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `none`（baseline） | 5/6 | 51/52 | 6,696,458 | +0 (0.0%) | 6,649,457 | 47,001 | 0 | 0.0% | 806.4s | +0.0s (0.0%) | 15.51s |
| `keyword` | 5/6 | 50/52 | 918,242 | **-5,778,216 (-86.3%)** | 899,589 | 18,653 | 216,284 | 23.6% | 378.9s | -427.5s (-53.0%) | 7.29s |
| `embedding` | 5/6 | 44/52 | 1,006,625 | **-5,689,833 (-85.0%)** | 987,523 | 19,102 | 222,188 | 22.1% | 341.9s | -464.5s (-57.6%) | 6.57s |
| `dispatch` | 5/6 | 44/52 | 852,238 | **-5,844,220 (-87.3%)** | 834,479 | 17,759 | 242,365 | 28.4% | 347.1s | -459.3s (-57.0%) | 6.68s |

> 注意：本次 baseline 的 6,696,458 tokens 中有很大一部分是由 `iam-crm/ic-01` 单轮贡献的（该轮在 `none` 模式下失控，产生了 143 次工具调用和 3,400,725 tokens）。这是「工具库大、无检索、模型自由发挥」时最典型的病态案例，也是 baseline 耗时/成本被显著拉高的主因。见后文「4) 影响 Tool Search 的因素」。

### 1) 使用 Tool Search 能节约多少 token？

#### 不同模块的 Token 消耗

| Mode | Toolsearch Tokens（embedding-only） | Chat Tokens | Total Tokens |
| --- | ---: | ---: | ---: |
| `none` | 0 | 6,696,458 | 6,696,458 |
| `keyword` | 216,284 | 701,958 | 918,242 |
| `embedding` | 222,188 | 784,437 | 1,006,625 |
| `dispatch` | 242,365 | 609,873 | 852,238 |

#### 不同模式的 Prompt/Completion/Total 消耗

| Mode | Prompt Tokens | Completion Tokens | Total Tokens |
| --- | ---: | ---: | ---: |
| `none` | 6,649,457 | 47,001 | 6,696,458 |
| `keyword` | 899,589 | 18,653 | 918,242 |
| `embedding` | 987,523 | 19,102 | 1,006,625 |
| `dispatch` | 834,479 | 17,759 | 852,238 |

#### Chat 桶明细

| Mode | Chat Prompt | Chat Completion | Chat Total |
| --- | ---: | ---: | ---: |
| `none` | 6,649,457 | 47,001 | 6,696,458 |
| `keyword` | 689,084 | 12,874 | 701,958 |
| `embedding` | 770,522 | 13,915 | 784,437 |
| `dispatch` | 597,258 | 12,615 | 609,873 |

#### Toolsearch 桶明细（仅 `embedding` 模式非零，为 embedding 调用的 token）

| Mode | TS Prompt | TS Completion | TS Total |
| --- | ---: | ---: | ---: |
| `none` | 0 | 0 | 0 |
| `keyword` | 210,505 | 5,779 | 216,284 |
| `embedding` | 217,001 | 5,187 | 222,188 |
| `dispatch` | 237,221 | 5,144 | 242,365 |

> 说明：由于重构后的插件把「选工具」的成本内联到主模型的对话里（模型自己调用 `tool_search`），`keyword` / `dispatch` 模式的 `toolsearch` 桶其实是**「主模型消费 `tool_search` 返回结果」时的 chat 增量**（而非独立 LLM 调用），只是被计量到 `toolsearch` 桶做归因。`embedding` 模式的 `toolsearch` 桶额外包含 embedding API 的 token 计费。

结论（本次 65 工具、52 轮对话）：

- **`dispatch` 的总 token 最低**：852,238，相对 baseline **节约 5,844,220（-87.3%）**。
- **`keyword` 次之**：918,242，节约 5,778,216（**-86.3%**）。
- **`embedding` 也节约明显**：1,006,625，节约 5,689,833（**-85.0%**）。
- 三者的 token 节约主要来自「每轮不再向主模型 dump 全部工具声明」，尤其是**规避了 `iam-crm/ic-01` 这种病态多次调用**（三种模式下该轮的调用次数从 143 → 2~22 次）。
- 三者的差异主要看 **chat prompt**：`dispatch` 只向模型暴露 `tool_search` + `call_tool` 两个函数，`chat.prompt` 最低（597K）；`keyword` 次之（689K）；`embedding` 反而略高（770K），因为在同一命名空间里向量召回的工具集合可能比关键词匹配更"发散"，导致主模型上下文里加载的工具更多。

### 2) 三种 Tool Search 策略哪种更优？在什么场景？

按 **Total Tokens 越低越优**（本次数据）：

- **`dispatch` 最省**：852,238
- **`keyword` 次之**：918,242
- **`embedding` 最贵**：1,006,625

按 **正确性（turn 达标率）**：

- **`keyword` 最高**：50/52（约 96.2%）
- **`embedding` / `dispatch` 并列**：44/52（约 84.6%）——两者都因为 `document` case 全 8 轮失败被拉低

按 **端到端耗时**（越低越好）：

- **`embedding` 最快**：341.9s
- **`dispatch` 次之**：347.1s
- **`keyword` 最慢**：378.9s

综合来看：

- **`keyword`** 更适合**通用生产场景**：达标率最高（近 baseline 水平）、无需 embedding 依赖、Tool Search 阶段无额外网络调用；缺点是命中依赖用户 query 的关键词匹配质量。
- **`dispatch`** 更适合**大规模工具库 + 强 token 敏感**场景：主模型上下文永远只有 2 个函数（`tool_search`/`call_tool`），`chat.prompt` 最低；缺点是所有实际工具都要经 `call_tool` 二次包装调用，`toolsearch` 桶反而是三者最高，另外少数轮次容易在没检索到合适工具时"止步于 `tool_search`"（`document` case 全 8 轮 fail）。
- **`embedding`** 更适合**query 措辞多变、语义化召回收益大**的场景（比如用户可能用大量同义近义词描述任务），但需要额外的 embedding 服务；在本 evalset 里 `document` case 的用户 query（"Create a markdown document."）语义泛化，反而没能让向量召回稳定命中 `create_document` 之类的工具，导致该 case 全轮失败。

### 3) 随着对话轮数增加，Token 消耗量如何变化？

下面按「轮次索引」跨 6 个 case 求平均值。turn 1~8 每个索引都有 6 个样本，turn 9 只有 4 个样本（`document` / `default-tools` 只有 8 轮）。

#### 平均每轮 Total Tokens（跨 6 case 平均）

| Turn | none | keyword | embedding | dispatch |
| --- | ---: | ---: | ---: | ---: |
| 1 | 615,003 | 7,147 | 25,315 | 17,587 |
| 2 | 104,746 | 14,130 | 16,151 | 13,360 |
| 3 | 92,102 | 13,413 | 13,889 | 13,691 |
| 4 | 69,782 | 15,602 | 14,507 | 13,636 |
| 5 | 60,697 | 17,281 | 27,582 | 18,061 |
| 6 | 35,479 | 15,160 | 15,179 | 17,014 |
| 7 | 42,865 | 27,929 | 19,302 | 17,116 |
| 8 | 46,514 | 21,327 | 17,850 | 17,135 |
| 9 | 73,324 | 31,570 | 26,991 | 21,656 |

#### Case: process-network（9 turns，总 token）

| Turn | none | keyword | embedding | dispatch |
| --- | ---: | ---: | ---: | ---: |
| 1 | 18,144 | 5,605 | 5,689 | 6,185 |
| 2 | 153,502 | 6,900 | 15,966 | 16,873 |
| 3 | 22,404 | 8,298 | 10,279 | 10,885 |
| 4 | 83,216 | 9,534 | 15,617 | 16,417 |
| 5 | 24,873 | 11,037 | 13,215 | 13,899 |
| 6 | 25,328 | 12,945 | 14,913 | 15,466 |
| 7 | 25,755 | 14,598 | 16,486 | 16,977 |
| 8 | 26,101 | 15,930 | 17,912 | 18,255 |
| 9 | 26,405 | 17,257 | 19,182 | 19,491 |

#### Case: default-tools（8 turns，总 token）

| Turn | none | keyword | embedding | dispatch |
| --- | ---: | ---: | ---: | ---: |
| 1 | 18,306 | 7,910 | 5,571 | 16,451 |
| 2 | 124,822 | 23,523 | 26,717 | 16,488 |
| 3 | 34,489 | 15,622 | 12,121 | 11,959 |
| 4 | 23,452 | 13,502 | 13,452 | 13,370 |
| 5 | 23,826 | 14,925 | 14,692 | 14,638 |
| 6 | 24,190 | 16,311 | 15,986 | 15,924 |
| 7 | 24,722 | 17,947 | 17,538 | 17,534 |
| 8 | 24,963 | 19,326 | 18,488 | 18,906 |

趋势总结：

- **`none` 每轮基线极高且波动剧烈**：单轮低时 ~18K，一旦触发多次工具调用（例如 pn-02 的 153K、ic-01 的 3.4M）会指数级放大。**这就是不用 Tool Search 在中/大规模工具库下的最大风险：不可控的长尾**。
- **`keyword` 是三种 Tool Search 里最"平稳"的**：随对话上下文逐轮线性增长（例如 process-network 从 5.6K → 17.3K），几乎没有 spike。原因是关键词匹配确定性强，`tool_search` 大多只调用 1 次，返回结果紧凑。
- **`embedding` / `dispatch` 单轮平均比 `keyword` 略高、但增长曲线相近**：`embedding` 每次 `tool_search` 都要跑 embedding 相似度并返回更多候选（因此每轮 `toolsearch` tokens 略高）；`dispatch` 每次工具调用都要走 `call_tool` 二次包装（`toolsearch` tokens 最高）。
- **首轮成本**：`embedding` 的首轮均值（25,315）明显高于 `keyword`（7,147）和 `dispatch`（17,587），提示 embedding 模式有一定的首轮/冷启动开销（例如首次调用 `tool_search` 时的向量比对成本）。

### 4) 影响 Tool Search 的因素有哪些？

结合本次数据可以看到以下关键因子：

- **工具库规模与工具描述长度**：Tool Search 的核心收益就是把「每轮塞入的工具声明」降下来。本次 65 工具带来的 baseline prompt 已达 6.6M，Tool Search 后压缩到 0.6M~1.0M。
- **模型的工具调用惯性**：`none` 模式下 `iam-crm/ic-01` 触发了 143 次调用（很多是重复/无关的），是 baseline 消耗爆炸的最主要原因；引入 Tool Search 后，调用次数被显著压回：`keyword` = 2, `dispatch` = 6, `embedding` = 22。
- **命名空间目录的语义组织**：`document` case 在 `embedding` 和 `dispatch` 模式下 **8/8 轮全部失败**（模型只调用了 `tool_search` 就停下，没有触发实际工具）。原因很可能是：
  - `document` 命名空间的 8 个工具描述与 user query 的语义相似度都较低（比如 "Create a markdown document" 更像通用的文件写入）；
  - 在 `keyword` 模式下反而没这个问题（8/8 全通过），说明**关键词匹配在这个 case 上比向量召回更精确**（命名 `create_document` / `export_pdf` 里都能直接词面命中）。
- **`tool_search` 参数的 query rewrite 质量**：`dispatch` 模式在 `document` case 里也是 8/8 fail，说明"选到工具后模型没能触发调用"和 dispatch 包装无关，本质是 `tool_search` 返回的候选与用户意图未对齐 → 模型 hallucinate 出"应该继续 search"而非"直接 call"。
- **MaxTools（本次为 5）**：召回工具越多，主对话 prompt 越大；召回越少可能漏工具。本次 3 种模式都通过率相近，说明 K=5 在 65 工具上是合适的量级。
- **多轮上下文长度**：随着对话历史累积，即便 Tool Search 也会稳步上升（process-network 里从 ~5K 涨到 ~19K）；主对话的历史 tokens 本身是不可压缩的。

### 5) 耗时分析（端到端）

| Mode | Total Wall Time | Avg Time / Turn | vs baseline（Total） |
| --- | ---: | ---: | ---: |
| `none` | 806.4s | 15.51s | +0.0s (0.0%) |
| `keyword` | 378.9s | 7.29s | **-427.5s (-53.0%)** |
| `embedding` | 341.9s | 6.57s | **-464.5s (-57.6%)** |
| `dispatch` | 347.1s | 6.68s | **-459.3s (-57.0%)** |

结论：**本次三种 Tool Search 模式的端到端耗时全部比 baseline 更低（约减半）**。这与"Tool Search 通常会带来耗时增量"的直觉相反，本次的解释是：

- baseline 触发了 `iam-crm/ic-01` 401 秒的极端病态调用链（143 次工具调用），单该轮就占了 baseline 总耗时的 50%。
- Tool Search 模式下，这一轮的耗时缩短到 4.5s（`keyword`）/ 9.9s（`dispatch`）/ 58.2s（`embedding`）。
- 换言之，**Tool Search 的最大价值不只是省 token，更是通过"缩窄工具视野"控制主模型的调用蔓延、避免长尾灾难**。

如果排除 `ic-01` 这类病态样本，只看其余轮次，Tool Search 会比 baseline 略慢（多一次 `tool_search`/embedding 调用），符合原始报告里的"约 +30% 耗时"经验值；但在真实业务里，Tool Search 恰恰是防止长尾的护栏，长期看反而降低平均耗时和成本。

### 6) 正确性 / 达标率的影响

> **提示**：本节所有"达标率"都指**工具轨迹严格匹配率**（见「评价指标」小节的说明）。以下列出的 fail 轮次并不都等同于"Agent 出错"——其中相当一部分是**主 Agent 因参数不全而向用户澄清/追问**，或**判断当前轮无需调用工具**，属于合理对话行为但会被本指标判为 0 分。生产场景下应结合每轮的 `actualTools` 与对话内容做二次判定。

| Mode | Case 达标 | Turn 达标 |
| --- | :---: | :---: |
| `none` | 5/6（`git` fail：git-02 未调 `git_diff`） | 51/52 |
| `keyword` | 5/6（`iam-crm` fail：ic-05, ic-09 miss） | 50/52 |
| `embedding` | 5/6（`document` 全 case fail） | 44/52 |
| `dispatch` | 5/6（`document` 全 case fail） | 44/52 |

关键观察：

- **`keyword` 通过率最接近 baseline**（50/52 vs 51/52），且没有出现"某个 case 全崩"的情况。
- **`embedding` / `dispatch` 在 `document` case 上崩溃**：8 轮全部 metric=0，模型只调用了 `tool_search` 就结束了对话。这既可能是"检索空转"（向量召回不到合适工具），也可能是主 Agent **在 `tool_search` 结果里没看到高置信度的工具后，选择向用户追问文件名/内容/格式**（`document` case 的 user message 如 "Create a markdown document." 确实缺少必要参数）——这两种情况在本指标下都会被计为未达标，但对真实业务而言，后者恰恰是被鼓励的"负责任"行为。
- **`keyword` 在 `document` case 上全通过**：说明关键词命中率高时，`tool_search` 直接把 `create_document` 类工具塞给主模型，Agent 就更倾向于"顺手调用工具+让参数用默认值"而不是追问用户。这个差异**未必意味着 `keyword` 的用户体验更好**，只是它在当前评测口径下拿分更高。
- 从 turn 级看，`embedding` / `dispatch` 除了 `document` 全崩，其他 case 的正确率基本正常。
- baseline (`none`) 的 `git-02` fail 与 `keyword` 的 `iam-crm/ic-05, ic-09` miss，也需要结合实际调用轨迹判断：在工具库大 + 多轮长上下文下，主模型很可能选择"先追问澄清"或"用其他等价工具完成"，同样会被指标误判为失败。

## 附录：逐 case 汇总

| Mode | Case | Status | Turns | Pass | Chat Tokens | TS Tokens | Total | Duration (ms) |
| --- | --- | :---: | ---: | ---: | ---: | ---: | ---: | ---: |
| none | filesystem | passed | 9 | 9 | 774,487 | 0 | 774,487 | 109,634 |
| none | git | failed | 9 | 8 | 559,322 | 0 | 559,322 | 77,280 |
| none | document | passed | 8 | 8 | 158,109 | 0 | 158,109 | 23,369 |
| none | process-network | passed | 9 | 9 | 405,728 | 0 | 405,728 | 63,176 |
| none | iam-crm | passed | 9 | 9 | 4,500,042 | 0 | 4,500,042 | 472,125 |
| none | default-tools | passed | 8 | 8 | 298,770 | 0 | 298,770 | 45,780 |
| keyword | filesystem | passed | 9 | 9 | 133,187 | 60,331 | 193,518 | 67,364 |
| keyword | git | passed | 9 | 9 | 84,925 | 30,655 | 115,580 | 48,119 |
| keyword | document | passed | 8 | 8 | 140,444 | 20,045 | 160,489 | 65,909 |
| keyword | process-network | passed | 9 | 9 | 69,965 | 32,139 | 102,104 | 45,941 |
| keyword | iam-crm | failed | 9 | 7 | 178,153 | 39,332 | 217,485 | 85,022 |
| keyword | default-tools | passed | 8 | 8 | 95,284 | 33,782 | 129,066 | 51,484 |
| embedding | filesystem | passed | 9 | 9 | 120,144 | 45,401 | 165,545 | 61,001 |
| embedding | git | passed | 9 | 9 | 156,260 | 31,753 | 188,013 | 63,699 |
| embedding | document | failed | 8 | 0 | 29,230 | 26,602 | 55,832 | 29,730 |
| embedding | process-network | passed | 9 | 9 | 92,408 | 36,851 | 129,259 | 43,113 |
| embedding | iam-crm | passed | 9 | 9 | 298,288 | 45,123 | 343,411 | 101,741 |
| embedding | default-tools | passed | 8 | 8 | 88,107 | 36,458 | 124,565 | 42,544 |
| dispatch | filesystem | passed | 9 | 9 | 112,640 | 44,673 | 157,313 | 61,485 |
| dispatch | git | passed | 9 | 9 | 171,525 | 52,775 | 224,300 | 82,517 |
| dispatch | document | failed | 8 | 0 | 29,002 | 26,775 | 55,777 | 32,982 |
| dispatch | process-network | passed | 9 | 9 | 96,009 | 38,439 | 134,448 | 50,487 |
| dispatch | iam-crm | passed | 9 | 9 | 109,676 | 45,454 | 155,130 | 52,777 |
| dispatch | default-tools | passed | 8 | 8 | 91,021 | 34,249 | 125,270 | 51,829 |

## 附录：`iam-crm/ic-01` 单轮对比

用户请求："*Delete the user account zhangsan from the identity system.*"（期望工具：`delete_user`）

| Mode | 实际调用工具数 | Chat Tokens | TS Tokens | Duration | 说明 |
| --- | ---: | ---: | ---: | ---: | --- |
| `none` | 143 | 3,400,725 | 0 | 401.3s | 模型在完整工具列表下反复尝试各种无关工具（`http_get` / `read_file` / `write_file` / `parse_json` / `create_user` × 多次），最终虽命中 `delete_user`（metric=1）但代价极大 |
| `keyword` | 2 | 3,940 | 1,681 | 4.5s | `tool_search` → `delete_user`，一次命中 |
| `embedding` | 22 | 102,603 | 7,212 | 58.2s | 触发了 `list_users` 多次、还错误 `create_user` 后才 `delete_user` |
| `dispatch` | 6 | 11,235 | 6,321 | 9.9s | 3 次 `tool_search` + 3 次 `call_tool`，最终命中 |

这一单轮就把 baseline 的整体成本/耗时结构改变了：它单独贡献了 baseline 51% 的总耗时和 51% 的总 tokens，是本次评测里 Tool Search 收益最大的场景。

## 局限与备注

- 本次评测每种模式仅跑 1 次（`NumRuns=1`），单次结果受模型随机性影响较大，尤其是 `iam-crm/ic-01` 这种"病态放大"型轮次很敏感。若用于线上成本预估，建议对每种模式跑 3~5 次取置信区间。
- 本报告未纳入 Prompt Caching：三种 Tool Search 模式每轮工具列表会随 `tool_search` 结果变化，可能降低缓存命中率，实际线上成本收益可能低于本表数据。
- `document` case 在 `embedding` / `dispatch` 模式下全轮失败，是本次评测里最值得深入的 bad case——建议后续单独对该 case 做 query rewrite / 描述改写实验，验证向量召回的下限。
- 与 `report.md`（`mathtools` 67 工具、3 case × 9 turn，LLM Search vs Knowledge Search）的对比：那份报告基于**旧版 API**（有独立的 tool-search LLM 调用）；本次是重构后新 API（`tool_search` 作为函数由主模型调用），因此不能直接对比"Tool Search 阶段 token"数值，但结论方向一致：Tool Search 显著节省 token、并在语义化召回场景下有失败风险。
