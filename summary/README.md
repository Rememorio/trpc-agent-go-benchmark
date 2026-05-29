# LongMemEval Session Summary Benchmark

This benchmark suite evaluates session-summary behavior for realistic long-term user/assistant memory using LongMemEval.

## What This Benchmark Measures

LongMemEval is used to compare three modes on realistic multi-session chat histories:

- `long_context`: full conversation history in the prompt
- `summary`: session summary only, with a visible recent tail
- `summary_ondemand`: session summary plus `session_search` / `session_load` tools for hidden history

The current headline experiment focuses on the `single-session-user` slice from `longmemeval_s_cleaned.json` (70 cases, ~103K prompt tokens on average in long-context mode).

## Repository Layout

```text
summary/
├── README.md
├── data/
│   ├── download_datasets.sh
│   └── README.md
├── results/
└── trpc-agent-go-impl/
    ├── main.go
    ├── config.go
    ├── benchmark.go
    ├── longmemeval.go
    └── evaluation/
        └── dataset/
```

## Setup

The benchmark module is wired to a trpc-agent-go version through `summary/trpc-agent-go-impl/go.mod`.

## Quick Start

### 1. Download LongMemEval

```bash
cd summary/data
./download_datasets.sh
```

### 2. Run LongMemEval with detailed continuity summary

```bash
cd ../trpc-agent-go-impl
PGVECTOR_DSN='postgres://USER:PASSWORD@HOST:5432/DB?sslmode=disable' \
go run . \
  -dataset ../data/longmemeval-cleaned/longmemeval_s_cleaned.json \
  -dataset-format longmemeval \
  -lme-question-types single-session-user \
  -num-cases 70 \
  -events 40 \
  -lme-visible-events 20 \
  -detailed-prompt=true \
  -llm-eval \
  -output ../results/lme_single_session_user_detailed
```

### 3. Run the compact-summary baseline

```bash
cd ../trpc-agent-go-impl
PGVECTOR_DSN='postgres://USER:PASSWORD@HOST:5432/DB?sslmode=disable' \
go run . \
  -dataset ../data/longmemeval-cleaned/longmemeval_s_cleaned.json \
  -dataset-format longmemeval \
  -lme-question-types single-session-user \
  -num-cases 70 \
  -events 40 \
  -lme-visible-events 20 \
  -detailed-prompt=false \
  -llm-eval \
  -output ../results/lme_single_session_user_default
```

## CLI Overview

| Flag | Default | Description |
|------|---------|-------------|
| `-model` | `gpt-4o-mini` | Model name (`MODEL_NAME` overrides default) |
| `-dataset` | `../data/mt-bench-101` | Dataset path; set to LongMemEval JSON for this benchmark |
| `-dataset-format` | auto | Use `longmemeval` |
| `-num-cases` | `0` | Number of cases to run (`0` = all) |
| `-output` | `../results` | Output directory |
| `-events` | `2` | Summary trigger event threshold |
| `-detailed-prompt` | `true` | Enable the nine-section detailed continuity prompt and verbatim user-message appendix |
| `-llm-eval` | `false` | Enable LLM-based evaluation |
| `-resume` | `false` | Resume from checkpoint |
| `-lme-question-types` | `""` | LongMemEval question types to include (empty = all) |
| `-lme-visible-events` | `20` | Number of most recent turns kept directly visible in summary modes |
| `-pgvector-dsn` | env | PostgreSQL DSN for `summary` / `summary_ondemand` |
| `-embed-model` | env / `text-embedding-3-small` | Embedding model name |

## Results

Results are written to the chosen output directory as:

- `results.json`
- `checkpoint.json`
- per-case `*.log`

See [results/REPORT.md](results/REPORT.md) and [results/REPORT.zh_CN.md](results/REPORT.zh_CN.md) for the latest analysis.
