# LongMemEval 会话摘要评测结果

## 1. 概览

本报告评估 LongMemEval 上的会话摘要策略。评测切片为 `longmemeval_s_cleaned.json` 中的 `single-session-user`。

对比模式包括：

- `long_context`：完整对话历史进入 prompt
- `summary`：会话摘要 + 最近可见尾部历史
- `summary_ondemand`：会话摘要 + `session_search` / `session_load` 检索隐藏历史

对比两种 summary prompt：

- **默认 prompt**：框架默认紧凑摘要
- **九段式 prompt**：带九段式 continuity 结构和原始用户消息保留的 detailed prompt（`-detailed-prompt=true`）

## 2. 数据集与配置

| 字段 | 值 |
| ---- | -- |
| 数据集 | LongMemEval cleaned |
| 文件 | `longmemeval_s_cleaned.json` |
| 问题类型 | `single-session-user` |
| Case 数 | 70 |
| 模型 | `gpt-4o-mini` |
| Long context 平均 prompt | ~103K tokens |
| Summary 触发 | `-events 40` |
| 最近可见尾部 | `-lme-visible-events 20` |
| LLM judge | 开启 |

LongMemEval 很适合评估长期 agent memory：它包含真实多 session 的 user/assistant 对话，问题也直接询问用户曾经提到的事实、偏好和经历。

## 3. 汇总结果

| 配置 | 模式 | ROUGE-L | F1 | BLEU | LLMScore | Exact Match | 平均 Prompt Tokens | Prompt 节省 | 平均 Summary Chars | 平均 Query Latency |
| ---- | ---- | ------: | -: | ---: | -------: | ----------: | -----------------: | ----------: | -----------------: | -----------------: |
| Full context | `long_context` | 0.1168 | 0.1225 | 0.0726 | 0.7357 | 0.6571 | 103,565 | — | 0 | 10,597 ms |
| 默认 prompt | `summary` | 0.0473 | 0.0563 | 0.0410 | 0.0771 | 0.0143 | 457 | 99.56% | 1,749 | 3,502 ms |
| 默认 prompt | `summary_ondemand` | 0.2486 | 0.2563 | 0.1641 | 0.8471 | 0.7286 | 6,308 | 93.90% | 1,669 | 10,581 ms |
| 九段式 prompt | `summary` | **0.2965** | **0.3014** | **0.1966** | **0.9179** | **0.8000** | 17,611 | 83.00% | 74,960 | 8,303 ms |
| 九段式 prompt | `summary_ondemand` | 0.2595 | 0.2660 | 0.1692 | 0.8900 | 0.7714 | 19,731 | 80.95% | 75,162 | 11,322 ms |

## 4. 主要结论

### 4.1 默认 Summary 很省，但过度压缩

默认紧凑 summary 平均只使用 457 prompt tokens，相比 full context 节省 99.56%，但长期记忆召回质量很低：

- ROUGE-L：0.0473
- LLMScore：0.0771
- Exact Match：0.0143

这说明默认 summary 不适合作为 LongMemEval 的 standalone 长期记忆机制。

### 4.2 默认 Summary + On-Demand 是强低成本方案

在默认 summary 上增加 on-demand retrieval 后，质量显著提升：

- ROUGE-L：0.0473 → 0.2486
- LLMScore：0.0771 → 0.8471
- Exact Match：0.0143 → 0.7286

同时仍然相对 full context 保留 93.90% prompt 节省，因此这是成本优先时的强方案。

### 4.3 九段式 Summary 是最强单模式

开启 `-detailed-prompt=true` 后，纯 `summary` 成为表现最好的模式：

- ROUGE-L：0.2965
- LLMScore：0.9179
- Exact Match：0.8000

相比默认 `summary`，九段式 `summary` 让 55/70 个 case 的 Exact Match 从 false 变成 true，并且没有 Exact Match 回退。它也超过 raw `long_context`，同时仍节省 83.00% prompt tokens。

### 4.4 九段式后 On-Demand 价值变小

在九段式 summary 下，`summary_ondemand` 接近但略低于纯 `summary`：

- ROUGE-L：0.2965 → 0.2595
- Exact Match：0.8000 → 0.7714

原因是九段式 summary 已经保留了关键用户事实，检索不再经常必要，甚至可能带来轻微干扰。

## 5. 工具调用模式

| 指标 | 默认 prompt | 九段式 prompt |
| ---- | ----------: | ------------: |
| 至少调用一次 `session_search` | 69 | 14 |
| 至少调用一次 `session_load` | 21 | 0 |
| `session_search` 总调用数 | 81 | 23 |
| `session_load` 总调用数 | 22 | 0 |
| 平均 search 次数/case | 1.16 | 0.33 |
| 平均 load 次数/case | 0.31 | 0.00 |
| On-demand 相对 summary 的 ROUGE-L 增益 | +0.2013 | -0.0370 |
| On-demand 相对 summary 的 Exact Match 增益 | +0.7143 | -0.0286 |

默认 prompt 依赖检索恢复事实；九段式 prompt 直接在 summary 中保留用户事实，因此大多数问题不再需要检索。

## 6. 建议

对于真实长期 user/assistant memory：

1. 如果最关心 token 成本，使用 **默认 summary + on-demand retrieval**。
2. 如果最关心回答准确率，使用 **九段式 continuity summary**。
3. 在 ~100K-token 级别，不建议只依赖 raw full context；summary-based 模式召回更好且成本更低。

## 7. 结论

对于 LongMemEval 这类长期记忆任务，九段式 continuity prompt 明显更强。它把 summary-only 从弱压缩表示变成最强召回模式，在保留大部分 prompt 节省的同时，超过 raw long context 和默认 on-demand retrieval。
