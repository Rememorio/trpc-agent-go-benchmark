# LongMemEval Evaluation Results

This directory stores the LongMemEval benchmark results and analysis for session summarization, on-demand retrieval, and detailed continuity summaries.

## Reports

| File | Description |
|------|-------------|
| [REPORT.md](REPORT.md) | Full LongMemEval evaluation report (English) |
| [REPORT.zh_CN.md](REPORT.zh_CN.md) | Full LongMemEval evaluation report (Chinese) |

## Benchmark Summary

The current report focuses on LongMemEval `single-session-user` (70 cases), a realistic multi-session user/assistant memory workload at ~103K prompt tokens in long-context mode.

## Key Results

| Configuration | Mode | ROUGE-L | LLMScore | Exact Match | Avg Prompt Tokens | Prompt Savings |
|---------------|------|--------:|---------:|------------:|------------------:|---------------:|
| Full context | `long_context` | 0.1168 | 0.7357 | 0.6571 | 103,565 | — |
| Default prompt | `summary` | 0.0473 | 0.0771 | 0.0143 | 457 | 99.56% |
| Default prompt | `summary_ondemand` | 0.2486 | 0.8471 | 0.7286 | 6,308 | 93.90% |
| Detailed prompt | `summary` | **0.2965** | **0.9179** | **0.8000** | 17,611 | 83.00% |
| Detailed prompt | `summary_ondemand` | 0.2595 | 0.8900 | 0.7714 | 19,731 | 80.95% |

## Key Insights

1. The default prompt is extremely token-efficient but too lossy for LongMemEval summary-only recall.
2. Default summary + on-demand retrieval is a strong low-token option: ROUGE-L 0.2486 with 93.90% prompt savings.
3. The detailed continuity prompt makes summary-only the best-performing mode: ROUGE-L 0.2965, LLMScore 0.9179, and 80% exact match.
4. With the detailed prompt, on-demand retrieval adds little because the summary already preserves the key user facts.
