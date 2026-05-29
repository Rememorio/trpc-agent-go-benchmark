# LongMemEval Session Summary Benchmark Results

## 1. Overview

This report evaluates session-summary strategies on LongMemEval, a realistic long-term user/assistant memory benchmark. The evaluated slice is `single-session-user` from `longmemeval_s_cleaned.json`.

The benchmark compares:

- `long_context`: full conversation history in the prompt
- `summary`: session summary plus the visible recent tail
- `summary_ondemand`: session summary plus `session_search` / `session_load` tools for hidden history

Two summary prompt configurations are compared:

- **Default prompt**: compact framework summary
- **Detailed prompt**: nine-section detailed continuity prompt with verbatim user-message preservation (`-detailed-prompt=true`)

## 2. Dataset and Setup

| Field | Value |
| ----- | ----- |
| Dataset | LongMemEval cleaned |
| File | `longmemeval_s_cleaned.json` |
| Question type | `single-session-user` |
| Cases | 70 |
| Model | `gpt-4o-mini` |
| Average long-context prompt | ~103K tokens |
| Summary trigger | `-events 40` |
| Visible recent tail | `-lme-visible-events 20` |
| LLM judge | enabled |

LongMemEval is a strong fit for long-term agent memory because it contains realistic multi-session user/assistant conversations and asks questions about facts, preferences, and experiences mentioned in prior chat history.

## 3. Aggregate Results

| Configuration | Mode | ROUGE-L | F1 | BLEU | LLMScore | Exact Match | Avg Prompt Tokens | Prompt Savings | Avg Summary Chars | Avg Query Latency |
| ------------- | ---- | ------: | -: | ---: | -------: | ----------: | ----------------: | -------------: | ----------------: | ----------------: |
| Full context | `long_context` | 0.1168 | 0.1225 | 0.0726 | 0.7357 | 0.6571 | 103,565 | — | 0 | 10,597 ms |
| Default prompt | `summary` | 0.0473 | 0.0563 | 0.0410 | 0.0771 | 0.0143 | 457 | 99.56% | 1,749 | 3,502 ms |
| Default prompt | `summary_ondemand` | 0.2486 | 0.2563 | 0.1641 | 0.8471 | 0.7286 | 6,308 | 93.90% | 1,669 | 10,581 ms |
| Detailed prompt | `summary` | **0.2965** | **0.3014** | **0.1966** | **0.9179** | **0.8000** | 17,611 | 83.00% | 74,960 | 8,303 ms |
| Detailed prompt | `summary_ondemand` | 0.2595 | 0.2660 | 0.1692 | 0.8900 | 0.7714 | 19,731 | 80.95% | 75,162 | 11,322 ms |

## 4. Main Findings

### 4.1 Default Summary is Cheap but Too Lossy

The default compact summary uses only 457 prompt tokens on average and saves 99.56% of prompt tokens versus full context, but recall quality is very low:

- ROUGE-L: 0.0473
- LLMScore: 0.0771
- Exact Match: 0.0143

This means the compact summary is not sufficient as a standalone long-term memory mechanism for LongMemEval.

### 4.2 Default Summary + On-Demand Retrieval is a Strong Low-Cost Option

Adding on-demand retrieval to the default compact summary improves quality substantially:

- ROUGE-L: 0.0473 → 0.2486
- LLMScore: 0.0771 → 0.8471
- Exact Match: 0.0143 → 0.7286

It still preserves 93.90% prompt savings versus full context, making it a strong cost-sensitive configuration.

### 4.3 Detailed Continuity Summary is the Strongest Single Mode

With `-detailed-prompt=true`, plain `summary` becomes the best-performing mode:

- ROUGE-L: 0.2965
- LLMScore: 0.9179
- Exact Match: 0.8000

Compared with default `summary`, detailed `summary` improves exact match on 55 of 70 cases and has no exact-match regressions. It also outperforms raw `long_context` while still saving 83.00% prompt tokens.

### 4.4 On-Demand Retrieval Adds Little After Detailed Summary

With the detailed summary prompt, `summary_ondemand` is close to but slightly below plain `summary`:

- ROUGE-L: 0.2965 → 0.2595
- Exact Match: 0.8000 → 0.7714

The detailed summary already preserves the key user facts, so retrieval is needed less often and can add mild noise.

## 5. Tool-Use Patterns

| Metric | Default Prompt | Detailed Prompt |
| ------ | -------------: | --------------: |
| Cases with at least one `session_search` | 69 | 14 |
| Cases with at least one `session_load` | 21 | 0 |
| Total `session_search` calls | 81 | 23 |
| Total `session_load` calls | 22 | 0 |
| Avg search calls per case | 1.16 | 0.33 |
| Avg load calls per case | 0.31 | 0.00 |
| ROUGE-L gain of on-demand vs summary | +0.2013 | -0.0370 |
| Exact-match gain of on-demand vs summary | +0.7143 | -0.0286 |

The default prompt relies heavily on retrieval. The detailed prompt makes retrieval mostly unnecessary because it preserves user facts directly in the summary.

## 6. Recommendation

For realistic long-term user/assistant memory:

1. Use **default summary + on-demand retrieval** when prompt cost is the main constraint.
2. Use **detailed continuity summary** when answer accuracy is the main goal.
3. Avoid relying on raw full context alone at ~100K-token scale; summary-based modes provide better recall with much lower prompt cost.

## 7. Conclusion

The detailed continuity prompt is stronger for LongMemEval-style long-term memory. It turns summary-only from a weak compact representation into the strongest recall mode, outperforming both raw long context and default on-demand retrieval while still saving most prompt tokens.
