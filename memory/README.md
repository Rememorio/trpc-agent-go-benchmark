# Memory Evaluation Benchmark

This benchmark evaluates the long-term conversational memory capabilities of
trpc-agent-go using LoCoMo and LongMemEval.

## Overview

Based on:

- [LoCoMo: Long-Context Conversational Memory](https://arxiv.org/abs/2402.17753)
- [LongMemEval: Benchmarking Chat Assistants on Long-Term Interactive Memory](https://arxiv.org/abs/2410.10813)
- [Memory in the Age of AI Agents](https://arxiv.org/abs/2512.13564)

## Reports

| File | Description |
|------|-------------|
| [REPORT.md](results/REPORT.md) | Full evaluation report (English) |
| [REPORT.zh_CN.md](results/REPORT.zh_CN.md) | Full evaluation report (Chinese) |

## Key Results

**Configuration**: Model=gpt-4o-mini, 10 samples, 1,986 QA pairs.

**Overall Results (No History Injection)**:

| Scenario | Backend | F1 | LLM Score |
|----------|---------|----:|----------:|
| Long-Context | - | **0.472** | **0.523** |
| Auto | pgvector | 0.357 | 0.366 |
| Auto | MySQL | 0.347 | 0.362 |
| Agentic | pgvector | 0.294 | 0.287 |
| Agentic | MySQL | 0.286 | 0.285 |

**History Injection Effect (Auto pgvector)**:

| History | F1 | LLM Score | Adversarial F1 | Open-domain LLM |
|---------|----:|----------:|---------------:|----------------:|
| None | **0.357** | 0.366 | **0.771** | 0.355 |
| +300 turns | 0.296 | 0.414 | 0.514 | 0.539 |
| +700 turns | 0.288 | **0.464** | 0.418 | **0.685** |

**Key Insights**:
1. Auto extraction with pgvector achieves the best memory-based F1 (75.6%
   of long-context baseline).
2. History injection improves semantic quality (LLM Score +0.10~0.18) but
   hurts token-level precision (F1 -0.02~0.07) due to adversarial
   robustness degradation.
3. Structured memory extraction outperforms brute-force history injection
   for factual recall tasks.
4. pgvector > MySQL for retrieval quality; gap vanishes with history
   injection.

**LongMemEval Oracle (Two Stratified 16-Question Subsets)**:

| Subset | Role | mem0 | pgvector |
|--------|------|-----:|---------:|
| seed48 | Development | 11/16 | 13/16 drift-normalized (12/16 raw) |
| seed137 | Historical holdout (now regression) | 14/16 | **15/16** |

LongMemEval replays each user/assistant pair through the production
auto-memory path. The 32-question result is diagnostic rather than a
full-dataset significance claim; see the full English or Chinese report for
the method, usage, failure-stage analysis, and limitations.

## SQLite vs SQLiteVec (Subset)

This is a set of subset runs to compare local SQLite keyword matching
(`sqlite`) vs sqlite-vec semantic search (`sqlitevec`).

**Subset run A (end-to-end QA)**:

- Model: gpt-4o-mini
- Scenario: auto
- Sample: locomo10_1 (199 QA, all categories)
- LLM Judge: enabled

| Backend | #QA | F1 | LLM Score | Prompt Tokens | Avg Prompt/QA | Avg Latency |
|---------|---:|---:|----------:|--------------:|--------------:|------------:|
| sqlite | 199 | 0.327 | 0.370 | 1,287,813 | 6,471 | 5,805ms |
| sqlitevec | 199 | 0.307 | 0.325 | 407,969 | 2,050 | 6,327ms |

Note: `Prompt Tokens` and `Avg Prompt/QA` count only QA agent calls.
They exclude embedding requests and LLM-as-Judge calls.

We also rerun the same configuration on `locomo10_6` (158 QA):

| Backend | #QA | F1 | Prompt Tokens | Avg Prompt/QA |
|---------|---:|---:|--------------:|--------------:|
| sqlite | 158 | 0.269 | 1,296,580 | 8,206 |
| sqlitevec | 158 | 0.274 | 362,903 | 2,297 |

**Subset run B (temporal token-cost micro-run)**:

- Model: gpt-4o-mini
- Scenario: auto
- Sample: locomo10_1
- Category filter: temporal (13 QA)
- LLM Judge: disabled

| Backend | F1 | Prompt Tokens | Avg Prompt/QA |
|---------|---:|--------------:|--------------:|
| sqlite | 0.116 | 80,184 | 6,168 |
| sqlitevec | 0.116 | 26,483 | 2,037 |

**Subset run C (top-k sweep + multi-search ablation)**:

To study whether "retrieving more memories" (higher top-k) or "searching more
times" (multiple `memory_search` calls) improves answer quality, we run a small
sweep on `locomo10_1` (LLM Judge disabled; F1/BLEU only).

| Backend | vector-topk | qa-search-passes | F1 | Prompt Tokens | Avg Prompt/QA |
|---------|------------:|-----------------:|---:|--------------:|--------------:|
| sqlite | - | 1 | 0.299 | 1,322,360 | 6,645 |
| sqlitevec | 5 | 1 | 0.320 | 346,253 | 1,740 |
| sqlitevec | 10 | 1 | 0.343 | 398,751 | 2,004 |
| sqlitevec | 20 | 1 | 0.329 | 621,790 | 3,125 |
| sqlitevec | 40 | 1 | 0.327 | 965,423 | 4,851 |
| sqlitevec | 10 | 2 | 0.342 | 659,981 | 3,316 |

Takeaway: top-k does not monotonically improve quality in this setup; higher
top-k increases tokens and can slightly reduce F1. See `results/REPORT.md` for
details.

## Evaluation Metrics

Aligned with LoCoMo paper and industry standards (Mem0, MemMachine):

| Metric     | Description                      |
| ---------- | -------------------------------- |
| F1 Score   | Token-level F1 (LoCoMo standard) |
| BLEU Score | N-gram overlap                   |
| LLM-score  | LLM-as-Judge evaluation          |

## QA Categories

| Category    | Description                                        |
| ----------- | -------------------------------------------------- |
| single-hop  | Single-hop questions from one conversation segment |
| multi-hop   | Multi-hop questions requiring multiple segments    |
| temporal    | Temporal reasoning questions                       |
| open-domain | Open-domain questions requiring world knowledge    |
| adversarial | Adversarial questions testing robustness           |

## Evaluation Scenarios

### 1. Long-Context (Baseline)

Full conversation as context, evaluates model's native long-context ability.

```bash
go run . -scenario long_context
```

### 2. Agentic (Memory Tools)

Agent uses memory tools to add and search memories. The agent processes each
conversation session separately and decides what to store.

```bash
go run . -scenario agentic
```

### 3. Auto (Memory Extractor + Search)

Auto mode uses the built-in memory extractor to generate memories in the
background. The QA stage only performs memory search.

```bash
go run . -scenario auto
```

Memory backends apply to `agentic` and `auto` scenarios.
Auto mode uses the built-in extractor provided by the memory service.

### 4. All Scenarios

Run all scenarios for comparison.

```bash
go run . -scenario all

# Run all scenarios on both backends.
go run . -scenario all -memory-backend inmemory,pgvector
```

### 5. Comma-Separated Scenarios

Run specific combinations of scenarios.

```bash
# Run agentic and auto only.
go run . -scenario agentic,auto -memory-backend pgvector,mysql
```

### 6. LongMemEval Memory Runner

LongMemEval uses a separate dataset format because each question carries its
own haystack sessions. The runner replays sessions in chronological order and
triggers memory extraction after each user/assistant pair. The pgvector backend
uses `memory.Service.EnqueueAutoMemoryJob` and waits for its session completion
marker before continuing. Reported asynchronous extraction or persistence
errors stop that backend immediately and are retained in the pair trace;
self-hosted mem0 sends the same raw pair to its memory API. Session dates are
transported separately: pgvector receives the date through extraction context,
while mem0 receives an ISO date in `metadata.observation_date`. The Mem0 V3
runtime used for a comparison must pass that metadata value to its existing
`Observation Date` prompt field and identify this transport-only patch in
`MEM0_IMPLEMENTATION`. Prefixing dates to message content or silently using the
server's current date produces a different protocol and is not comparable.
`results.json` records
pgvector extraction operations, per-pair memory
diffs, retrieval hits, answer text, LLM and embedding token usage, prompt-cache
usage, timings, evidence recall, and build provenance for the benchmark and
memory modules. The result metadata also fingerprints the dataset, selected
question manifest, replay protocol, and prompt versions with SHA-256 digests.
This separates extraction, persistence, retrieval, and answer failures and
prevents configuration drift from being reported as an algorithm improvement.

The unified [English](results/REPORT.md) and
[Chinese](results/REPORT.zh_CN.md) reports include the current pgvector and
self-hosted mem0 comparison.

Use `./run-longmemeval.sh` for every formal ingestion, answer, rerank, refresh,
or judge run. It rejects a modified benchmark worktree and injects the exact
benchmark commit and module manifest/checksum digests into the result
provenance. Set `LME_AGENT_REPLACEMENT=<module-path>@<version>` to build an
upstream arm from a deterministic temporary modfile without editing the
worktree; the resolved module versions and both temporary manifest digests are
recorded. Plain `go run .` remains useful for local smoke tests, but its output
may omit formal provenance and is then intentionally rejected by strict
comparison.

```bash
export PGVECTOR_DSN="postgres://user:password@localhost:5432/vectordb?sslmode=disable"
export MEM0_HOST="http://localhost:8888"
export MEM0_IMPLEMENTATION="mem0-oss-<source-commit-or-image-digest>"

# One-case smoke test.
go run . \
  -dataset-format longmemeval \
  -dataset ../../summary/data/longmemeval-cleaned/longmemeval_oracle.json \
  -memory-backend pgvector,mem0 \
  -lme-question-id 08f4fc43 \
  -lme-implementation local-smoke \
  -table-suffix _lme_smoke \
  -output ../results/lme-smoke

# Targeted bad-case replay.
go run . \
  -dataset-format longmemeval \
  -dataset ../../summary/data/longmemeval-cleaned/longmemeval_oracle.json \
  -memory-backend pgvector,mem0 \
  -lme-question-ids 07b6f563,35a27287,gpt4_0a05b494 \
  -lme-implementation local-badcase \
  -table-suffix _lme_badcase \
  -output ../results/lme-badcase

# Stratified 16-question development baseline plus a frozen Mem0 reference arm.
LME_AGENT_REPLACEMENT="trpc.group/trpc-go/trpc-agent-go@<upstream-pseudo-version>" \
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -dataset ../../summary/data/longmemeval-cleaned/longmemeval_oracle.json \
  -memory-backend pgvector,mem0 \
  -lme-per-type 2 \
  -lme-abstention-count 4 \
  -lme-sample-seed 48 \
  -lme-implementation upstream-main-<commit> \
  -mem0-llm-temperature 0 \
  -table-suffix _lme_upstream \
  -output ../results/lme-upstream

# Run candidate pgvector on the exact same selection. Do not rerun Mem0: the
# comparison command reuses the frozen reference arm from the upstream run.
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -dataset ../../summary/data/longmemeval-cleaned/longmemeval_oracle.json \
  -memory-backend pgvector \
  -lme-per-type 2 \
  -lme-abstention-count 4 \
  -lme-sample-seed 48 \
  -lme-implementation candidate-<commit> \
  -table-suffix _lme_candidate \
  -output ../results/lme-candidate

# Add semantic-judge results to a completed run.
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -lme-judge-results ../results/lme-upstream/results.json \
  -lme-judge-runs 3 \
  -output ../results/lme-upstream

# Regenerate only the answers from saved retrieval hits after changing the
# shared answer protocol, then judge that output.
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -lme-reanswer-results ../results/lme-upstream/results.json \
  -output ../results/lme-upstream
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -lme-judge-results ../results/lme-upstream/reanswered_results.json \
  -lme-judge-runs 3 \
  -output ../results/lme-upstream

# Re-run pgvector retrieval and answers against the exact persisted memories
# from a completed run, without paying the ingestion cost again.
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -lme-refresh-retrieval-results ../results/lme-candidate/results.json \
  -table-suffix _lme_candidate \
  -vector-topk 30 \
  -output ../results/lme-candidate

# Optionally add one model-based relevance-selection pass over every backend's
# saved top-k. Running this on a combined PGVector/Mem0 result applies the same
# model, prompt, and Top-N to both arms. Pre-rerank hits, selected hits, model
# calls, errors, latency, and token usage are retained for diagnosis. Treat this
# as an ablation: compare reranked arms only when their recorded rerank protocol
# matches, and do not mix them with the frozen non-reranked baseline.
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -lme-rerank-results ../results/lme-upstream/results.json \
  -lme-rerank-topn 12 \
  -output ../results/lme-upstream

# Analyze judged results without making model calls.
go run . \
  -dataset-format longmemeval \
  -lme-analyze-results ../results/lme-upstream/judged_results.json \
  -output ../results/lme-upstream

# Compare two LongMemEval runs without making model calls.
go run . \
  -dataset-format longmemeval \
  -lme-compare-results ../results/lme-upstream/judged_results.json,../results/lme-candidate/judged_results.json \
  -output ../results/lme-candidate
```

The judge command checkpoints `judged_results.json` after each case. An odd
`-lme-judge-runs` value greater than one records every independent vote and
uses a strict majority. When resuming from that file with the same judge model
and run count, it keeps validated verdicts and retries only missing or invalid
ones. Analysis treats a valid
semantic-judge result as the primary correctness signal and falls back to exact
match when no judge result is available. It writes `analysis.md` and
`bad_cases.tsv`, including raw pipeline stages, evidence status, backend
disagreements, and answer-gap diagnostics. Comparison uses the same correctness
rule and rejects runs whose dataset, selection, replay protocol, retrieval
depth, answer model, embedding model, prompt versions, or judge configuration
differ. It writes `comparison.md` and `comparison.tsv`, compares upstream and
candidate pgvector quality and cost, and presents Mem0 from the upstream run as
a frozen third arm.
When normalized questions, references, and answers are identical, comparison
treats conflicting judge verdicts as unchanged and reports the ignored judge
drift instead of a model regression.
Analysis stage labels are judge-aware for answer correctness. `results.json`
retains the pre-judge pipeline label, which is also exposed as `raw_stage` for
bad cases.
The scored answer is the raw model response: the runner does not complete
truncated entities from retrieval hits, extract a preferred list, or perform
arithmetic as an answer post-processing step. The answer prompt preserves each
backend's saved retrieval order but omits backend-specific similarity scores,
whose scales are not comparable. Re-answering writes checkpointed
`reanswered_results.json`, replaces the prior answer-call usage in aggregate
token counters, clears stale judge results, and does not rerun ingestion or
retrieval.
Retrieval refresh first verifies that canonical memories in the recorded
pgvector table exactly match the source run, then writes
`retrieval_refreshed_results.json`. It preserves the recorded ingestion and
original query-embedding cost and replaces answer usage. Saved-result reranking
applies the same relevance-selection protocol to every backend in the source
file and writes `reranked_results.json`; a malformed or failed rerank call falls
back to that backend's original hits while retaining the failure trace. It
preserves ingestion and embedding usage and replaces prior answer and rerank
usage.

Token counters cover model and embedding calls made by this process, including
pgvector extraction, retrieval, and answer generation. A self-hosted mem0 can
return internal LLM, cached-token, and embedding usage in `X-Mem0-Usage`;
`provider_usage_reported` and the analysis coverage column show whether that
usage was included. Stock mem0 servers do not return it, so their missing
internal usage must not be interpreted as zero-cost usage.
The runner also reads and records the sanitized self-hosted mem0 runtime
configuration. `-mem0-llm-temperature` changes that configuration only when it
is non-negative; the default keeps the server value while still recording it.

## Command-Line Options

| Option              | Default                | Description                            |
| ------------------- | ---------------------- | -------------------------------------- |
| `-model`            | gpt-4o-mini            | Model name                             |
| `-model-variant`    |                        | OpenAI-compatible model variant        |
| `-eval-model`       | same as model          | Evaluation model for LLM judge         |
| `-dataset`          | ../data                | Dataset directory                      |
| `-data-file`        | locomo10.json          | Dataset file name                      |
| `-output`           | ../results             | Output directory                       |
| `-scenario`         | long_context           | Evaluation scenario (comma-separated)  |
| `-memory-backend`   | inmemory               | Memory backend (comma-separated)       |
| `-pgvector-dsn`     | (env)                  | PostgreSQL DSN for pgvector            |
| `-mysql-dsn`        | (env)                  | MySQL DSN for mysql backend            |
| `-embed-model`      | text-embedding-3-small | Embedding model for vector backends    |
| `-vector-topk`      | 30                     | Top-k results for vector backends      |
| `-qa-history-turns` | 0                      | Inject N conversation turns as context |
| `-qa-search-passes` | 2                      | memory_search calls per QA             |
| `-sample-id`        |                        | Filter by sample ID                    |
| `-max-tasks`        | 0                      | Maximum tasks (0=all)                  |
| `-llm-judge`        | false                  | Enable LLM-as-Judge                    |
| `-verbose`          | false                  | Verbose output                         |
| `-resume`           | false                  | Resume from checkpoint                 |

LongMemEval-specific options:

| Option                   | Default | Description                                  |
| ------------------------ | ------- | -------------------------------------------- |
| `-dataset-format`        | locomo  | Use `longmemeval` for LongMemEval JSON       |
| `-lme-question-id`       |         | Run one LongMemEval question                 |
| `-lme-question-ids`      |         | Comma-separated `question_id` filter         |
| `-lme-question-types`    |         | Comma-separated `question_type` filter       |
| `-lme-per-type`          | 0       | Stratified sample count per question type    |
| `-lme-abstention-count`  | 0       | Additional abstention questions to sample    |
| `-lme-sample-seed`       | 42      | Sampling seed                                |
| `-lme-max-sessions`      | 0       | Max haystack sessions per case               |
| `-lme-max-pairs`         | 0       | Max user/assistant pairs per case            |
| `-lme-ingest-wait`       | 250ms   | Extra delay after completed pair ingestion   |
| `-lme-model-call-timeout` | 3m      | Model timeout and mem0 OSS request cap       |
| `-lme-answer`            | true    | Generate answers from retrieved memories     |
| `-lme-implementation`    | (env)   | Reproducible implementation label            |
| `-lme-reanswer-results`   |         | Re-answer using saved ranked retrieval hits  |
| `-lme-refresh-retrieval-results` | | Refresh persisted pgvector retrieval         |
| `-lme-rerank-results`     |         | Rerank saved hits for every result backend   |
| `-lme-rerank-topn`        | 12      | Maximum memories selected by the reranker    |
| `-lme-judge-results`     |         | Add semantic judge results to `results.json` |
| `-lme-judge-runs`        | 1       | Odd number of independent semantic votes     |
| `-lme-analyze-results`   |         | Analyze one saved LongMemEval `results.json` |
| `-lme-compare-results`   |         | Compare baseline,candidate `results.json`    |
| `-mem0-host`             | (env)   | Self-hosted mem0 OSS host                    |
| `-mem0-implementation`   | (env)   | Mem0 source revision or image digest         |
| `-mem0-cloud`            | false   | Use hosted mem0 API semantics                |
| `-mem0-llm-temperature`  | -1      | Set OSS mem0 LLM temperature; -1 keeps it    |

## Environment Variables

| Variable                    | Description                               |
| --------------------------- | ----------------------------------------- |
| `MODEL_NAME`                | Default model name                        |
| `EVAL_MODEL_NAME`           | Evaluation model name                     |
| `OPENAI_API_KEY`            | OpenAI API key                            |
| `PGVECTOR_DSN`              | PostgreSQL DSN for pgvector backend       |
| `MYSQL_DSN`                 | MySQL DSN for mysql backend               |
| `SQLITE_DSN`                | SQLite DSN for sqlite backend (optional)  |
| `SQLITEVEC_DSN`             | SQLite DSN for sqlitevec backend (optional) |
| `EMBED_MODEL_NAME`          | Embedding model for vector backends       |
| `OPENAI_EMBEDDING_API_KEY`  | API key for embedding model (optional)    |
| `OPENAI_EMBEDDING_BASE_URL` | Base URL for embedding API (optional)     |
| `MEM0_HOST`                 | Self-hosted mem0 OSS host                 |
| `MEM0_IMPLEMENTATION`       | Mem0 source revision or image digest      |
| `LME_IMPLEMENTATION`        | LongMemEval implementation label          |

## Dataset Setup

1. Download the LoCoMo dataset:

```bash
git clone https://github.com/snap-research/locomo.git
cp locomo/data/locomo10/*.json ../data/
```

2. Or use the sample data for testing:

```bash
# Sample data should be in ../data/locomo_sample.json.
```

## Running the Benchmark

```bash
cd benchmark/memory/trpc-agent-go-impl

# Install dependencies.
go mod tidy

# Run with default settings (long_context + inmemory).
go run .

# Run with LLM judge enabled.
go run . -llm-judge -model gpt-4o

# Run agentic evaluation with pgvector backend.
export PGVECTOR_DSN="postgres://user:password@localhost:5432/memory_eval\
?sslmode=disable"
go run . -scenario agentic -memory-backend pgvector

# Run auto evaluation with sqlite backend.
go run . -scenario auto -memory-backend sqlite

# Run auto evaluation with sqlitevec backend (requires embeddings).
go run . -scenario auto -memory-backend sqlitevec

# Run auto evaluation with sqlite backend.
go run main.go -scenario auto -memory-backend sqlite

# Run auto evaluation with sqlitevec backend (requires embeddings).
go run main.go -scenario auto -memory-backend sqlitevec

# Run all scenarios.
go run . -scenario all -output ../results/full_eval

# Run with history injection (300 turns).
go run . \
  -scenario agentic,auto \
  -memory-backend pgvector,mysql \
  -qa-history-turns 300 \
  -llm-judge \
  -output ../results/history300
```

## Output Format

Results are saved in JSON format:

```json
{
  "metadata": {
    "framework": "trpc-agent-go",
    "model": "gpt-4o-mini",
    "scenario": "agentic",
    "memory_backend": "pgvector"
  },
  "summary": {
    "total_questions": 200,
    "overall_f1": 0.412,
    "overall_bleu": 0.156
  },
  "by_category": {
    "single-hop": { "count": 60, "f1": 0.523, "bleu": 0.182 },
    "multi-hop": { "count": 50, "f1": 0.384, "bleu": 0.145 }
  }
}
```

## Comparison with Baselines

| System             | F1   | LLM-score |
| ------------------ | ---- | --------- |
| GPT-4 (4K context) | 32.1 | -         |
| GPT-3.5-16K        | 37.8 | -         |
| Mem0               | -    | 0.80      |
| MemMachine         | 91.2 | 0.91      |

## Memory Backend Comparison

| Backend  | Pros                                | Cons                                         |
| -------- | ----------------------------------- | -------------------------------------------- |
| inmemory | Fast, no setup required             | No vector similarity, keyword-based matching |
| pgvector | Vector similarity search, scalable  | Requires PostgreSQL setup                    |
| mysql    | App-layer BM25-style keyword search | Requires MySQL setup                         |

### Expected Results

- **pgvector** should outperform **inmemory** for semantic retrieval tasks.
- For exact-match questions, both backends may perform similarly.
- pgvector is recommended for production and realistic evaluation.
- With history injection, backend differences diminish.
