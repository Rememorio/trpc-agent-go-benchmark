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

### Legacy LoCoMo Results

The LoCoMo tables in this section are retained historical results. The
trpc-agent-go `auto` and `agentic` runs used replay protocol v3, which executed
a placeholder agent turn after seeding each historical session. That could add
a synthetic assistant response and, when a session ended with the transport
`assistant` role, duplicate its latest user turn. Long-context, Session Recall,
and manually seeded external-framework results were not affected. Current
trpc-agent-go Auto runs use exact-replay-v4; the legacy Auto/Agentic numbers
below must be rerun before they can gate the current memory candidate.

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

**LongMemEval Oracle (Protocol-v2 Observed Development Regression)**:

**Configuration**: Model=glm52, Embedding=text-embedding-3-small, Top-K=30.

| Arm | Primary | Majority | Correct replicates | Memory LLM tokens | Final memories |
|-----|--------:|---------:|-------------------:|------------------:|---------------:|
| pgvector main | 11/16 | 11/16 | 33/48 | 1,184,057 | 141 |
| Mem0 OSS | 14/16 | 14/16 | 42/48 | 1,765,231 | 492 |
| pgvector candidate before provenance ranking | 15/16 | 15/16 | 46/48 | 1,641,809 | 311 |
| pgvector final candidate | **16/16** | **16/16** | **48/48** | 1,641,809* | 311* |

LongMemEval replays 183 user/assistant pairs per arm through the production
auto-memory path, then generates three independent answers with three judge
votes each. The final retrieval-only change adds query-aware provenance as a
fourth RRF signal: explicit historical-assistant questions favor preserved
assistant results, while other questions favor user-grounded memories. It
repairs the sole unstable candidate case without filtering either source.
`*` Ingestion, usage, and storage are inherited from an exact byte-stable
memory snapshot; only retrieval order and fresh answer/judge runs changed.
The accepted baseline replaced one whole answer replicate across every arm
after a truncated Mem0 response, rather than selectively resampling a case.
This fixed 16-question set was already observed during development. It is not
unseen evidence or a full-dataset significance claim; see the full English or
Chinese report for costs, provenance, bad cases, LoCoMo regression, and
limitations.

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

New LoCoMo Auto runs use exact-replay-v4: sessions are replayed chronologically
and extracted once per session.
Because both participants are humans, each session's opening speaker is mapped
to the transport `user` role and the other speaker to `assistant`; speaker names
remain in the message text. Historical replay writes only these mapped dataset
turns into the session: it does not execute a placeholder agent or append a
synthetic response/current-turn duplicate. This keeps every opening turn on
strict chat APIs while preserving the source transcript exactly once.
The legacy result tables above predate this protocol correction.

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
triggers memory extraction after each role-aware replay unit. A user turn
immediately followed by an assistant turn forms one unit; leading assistant
turns, repeated roles, and other unmatched turns remain singleton units in
source order. This prevents malformed or assistant-leading sessions from
combining unrelated messages. The pgvector backend uses
`memory.Service.EnqueueAutoMemoryJob` and waits for its session completion
marker before continuing. Reported asynchronous extraction or persistence
errors stop that backend immediately and are retained in the replay trace;
self-hosted mem0 sends the same raw unit to its memory API. Session dates are
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
provenance. Set `LME_AGENT_REPLACEMENT=<module-path>@<version>` to build an arm
from a deterministic temporary modfile without editing the worktree; the
resolved module versions and both temporary manifest digests are recorded. A
replacement also requires an explicit `LME_AGENT_PROFILE=candidate|upstream`,
because its module path does not establish the experiment role.
`LME_AGENT_PROFILE=upstream` compiles the runner without candidate-only
extractor options and rejects non-default extraction settings; without a
replacement, the profile defaults to `candidate`. Plain `go run .` remains
useful for local smoke tests, but its output may omit formal provenance and is
then intentionally rejected by strict comparison.
LongMemEval PGVector ingestion requires an explicit `-table-suffix` so separate
runs cannot share physical storage accidentally. Retrieval refresh preserves
that suffix. The opt-in `-lme-allow-shared-table-refresh` exists only for
audited legacy results without one; it additionally requires an explicit
recorded user scope and validates every PGVector user ID against its question
before accessing the shared table.

```bash
export PGVECTOR_DSN="postgres://user:password@localhost:5432/vectordb?sslmode=disable"
export MEM0_HOST="http://localhost:8888"
export MEM0_IMPLEMENTATION="mem0-oss-<source-commit-or-image-digest>"
export LME_ANSWER_MODEL="<answer-model>"
export LME_JUDGE_MODEL="<judge-model>"
export LME_MODEL_VARIANT="<openai-compatible-provider-variant>"
export LME_EMBED_MODEL="text-embedding-3-small"

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

# Inspect and pre-register a stratified selection without initializing any
# model, embedding, database, or Mem0 provider. Use the formal runner so the
# manifest records a clean benchmark revision and pinned modules. The manifest
# contains only question IDs, types, sampling parameters, and provenance
# digests. Protocol v2 also binds the logical answer, embedding, and judge
# configurations shown below without contacting those providers. Keep the
# frozen exclusion set in a one-question-ID-per-line file; IDs are merged with
# any CSV exclusions, deduplicated, sorted, and checked against the selected
# dataset before sampling.
LME_AGENT_PROFILE=candidate \
LME_AGENT_REPLACEMENT="trpc.group/trpc-go/trpc-agent-go@<candidate-pseudo-version>" \
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -dataset ../../summary/data/longmemeval-cleaned/longmemeval_oracle.json \
  -lme-per-type 2 \
  -lme-abstention-count 4 \
  -lme-exclude-question-ids-file ../results/lme-observed-question-ids.txt \
  -lme-sample-seed 48 \
  -model "$LME_ANSWER_MODEL" \
  -eval-model "$LME_JUDGE_MODEL" \
  -model-variant "$LME_MODEL_VARIANT" \
  -embed-model "$LME_EMBED_MODEL" \
  -lme-judge-runs 3 \
  -lme-answer=true \
  -vector-topk 30 \
  -lme-selection-only \
  > ../results/lme-holdout-selection.json

# Before any provider call, validate the frozen manifest against the exact
# ingestion protocol by combining -lme-preregistered-selection with
# -lme-selection-only. Keep the dataset, exclusion file, model, embedding,
# Top-K, timeout, replay, and answer flags identical to the ingestion command.
# The runner verifies the dataset, cohort, exclusions, protocol digest, and
# clean build provenance, prints the validated manifest, and exits before
# initializing a model, embedding client, database, or Mem0 backend.
# Run the exact ingestion command below once with -lme-selection-only added
# and redirect its output to a validation artifact. Then remove only that flag
# for the provider-backed execution.

# Execute exactly the preregistered selection. Dataset, protocol, exclusion
# set, case metadata, and benchmark revision must still match the manifest.
# Additional case filters, resampling, max-tasks truncation, and model,
# embedding, answer-generation, or judge drift are rejected before provider
# initialization. Use a fresh answer ledger for each replicate and share that
# ledger across all three arms. Give the two source runs independent, initially
# empty model and embedding ledgers while keeping one explicit user scope. None
# of these paths may exist before its first run.
LME_BLIND_ANSWER_CACHE=../results/lme-holdout-answer-replicate-1-cache.json
LME_BLIND_USER_SCOPE=lme-holdout-replicate-1
LME_UPSTREAM_MODEL_CACHE=../results/lme-holdout-upstream-model-cache.jsonl
LME_UPSTREAM_EMBEDDING_CACHE=../results/lme-holdout-upstream-embedding-cache.jsonl
LME_CANDIDATE_MODEL_CACHE=../results/lme-holdout-candidate-model-cache.jsonl
LME_CANDIDATE_EMBEDDING_CACHE=../results/lme-holdout-candidate-embedding-cache.jsonl
LME_AGENT_PROFILE=upstream \
LME_AGENT_REPLACEMENT="trpc.group/trpc-go/trpc-agent-go@<upstream-pseudo-version>" \
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -dataset ../../summary/data/longmemeval-cleaned/longmemeval_oracle.json \
  -memory-backend pgvector,mem0 \
  -lme-preregistered-selection ../results/lme-holdout-selection.json \
  -lme-exclude-question-ids-file ../results/lme-observed-question-ids.txt \
  -lme-implementation upstream-holdout-<commit> \
  -lme-blind-progress=true \
  -pgvector-update-policy reconcile \
  -pgvector-assistant-result-extraction=false \
  -model "$LME_ANSWER_MODEL" \
  -eval-model "$LME_JUDGE_MODEL" \
  -model-variant "$LME_MODEL_VARIANT" \
  -embed-model "$LME_EMBED_MODEL" \
  -lme-judge-runs 3 \
  -lme-answer=true \
  -lme-answer-cache "$LME_BLIND_ANSWER_CACHE" \
  -lme-user-scope "$LME_BLIND_USER_SCOPE" \
  -lme-model-response-cache "$LME_UPSTREAM_MODEL_CACHE" \
  -lme-embedding-response-cache "$LME_UPSTREAM_EMBEDDING_CACHE" \
  -mem0-llm-temperature 0 \
  -vector-topk 30 \
  -table-suffix _lme_holdout_upstream \
  -output ../results/lme-holdout-upstream

# Run candidate pgvector against the same manifest and answer ledger. Mem0 is
# already frozen in the upstream run and must not be rerun for this comparison.
LME_AGENT_PROFILE=candidate \
LME_AGENT_REPLACEMENT="trpc.group/trpc-go/trpc-agent-go@<candidate-pseudo-version>" \
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -dataset ../../summary/data/longmemeval-cleaned/longmemeval_oracle.json \
  -memory-backend pgvector \
  -lme-preregistered-selection ../results/lme-holdout-selection.json \
  -lme-exclude-question-ids-file ../results/lme-observed-question-ids.txt \
  -lme-implementation candidate-holdout-<commit> \
  -lme-blind-progress=true \
  -model "$LME_ANSWER_MODEL" \
  -eval-model "$LME_JUDGE_MODEL" \
  -model-variant "$LME_MODEL_VARIANT" \
  -embed-model "$LME_EMBED_MODEL" \
  -lme-judge-runs 3 \
  -lme-answer=true \
  -lme-answer-cache "$LME_BLIND_ANSWER_CACHE" \
  -lme-user-scope "$LME_BLIND_USER_SCOPE" \
  -lme-model-response-cache "$LME_CANDIDATE_MODEL_CACHE" \
  -lme-embedding-response-cache "$LME_CANDIDATE_EMBEDDING_CACHE" \
  -pgvector-update-policy reconcile \
  -pgvector-assistant-result-extraction=true \
  -vector-topk 30 \
  -table-suffix _lme_holdout_candidate \
  -output ../results/lme-holdout-candidate

# Blind progress hides question/session identifiers and outcome content from
# the console and per-case logs, while retaining operational counts, latency,
# and usage. Blind per-case log names use ordinal case numbers. Raw results.json
# files still contain questions, references, retrievals, answers, and metrics.
# Keep them sealed until every arm in the replicate finishes. Then judge all
# arm outputs with one new shared judge cache. Do not inspect results or run
# adaptive retries between arms, and use fresh answer and judge ledgers for
# each answer replicate.

# Stratified 16-question development baseline plus a frozen Mem0 reference arm.
LME_AGENT_REPLACEMENT="trpc.group/trpc-go/trpc-agent-go@<upstream-pseudo-version>" \
LME_AGENT_PROFILE=upstream \
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -dataset ../../summary/data/longmemeval-cleaned/longmemeval_oracle.json \
  -memory-backend pgvector,mem0 \
  -lme-per-type 2 \
  -lme-abstention-count 4 \
  -lme-sample-seed 48 \
  -lme-implementation upstream-main-<commit> \
  -pgvector-update-policy reconcile \
  -pgvector-assistant-result-extraction=false \
  -lme-answer=true \
  -lme-answer-cache ../results/lme-answer-cache.json \
  -mem0-llm-temperature 0 \
  -vector-topk 30 \
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
  -pgvector-update-policy reconcile \
  -pgvector-assistant-result-extraction=true \
  -lme-answer=true \
  -lme-answer-cache ../results/lme-answer-cache.json \
  -vector-topk 30 \
  -table-suffix _lme_candidate \
  -output ../results/lme-candidate

# Add semantic-judge results to a completed run.
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -lme-judge-results ../results/lme-upstream/results.json \
  -model "$LME_ANSWER_MODEL" \
  -eval-model "$LME_JUDGE_MODEL" \
  -model-variant "$LME_MODEL_VARIANT" \
  -embed-model "$LME_EMBED_MODEL" \
  -lme-judge-runs 3 \
  -lme-answer=true \
  -vector-topk 30 \
  -lme-judge-cache ../results/lme-judge-cache.json \
  -output ../results/lme-upstream

# Audit a completed result before reading scores or comparing arms. This makes
# no provider calls. It verifies the stored build and dataset digests, explicit
# backend isolation, every canonical replay message/date against the source
# dataset, memory attribution, Mem0 provider usage, retrieval bounds, complete
# snapshots, error-free answer/judge execution, and a recomputed summary.
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -dataset ../../summary/data/longmemeval-cleaned/longmemeval_oracle.json \
  -lme-audit-results ../results/lme-upstream/judged_results.json \
  -output ../results/lme-upstream

# Generate an independent answer replicate from saved retrieval hits under the
# exact frozen protocol, then judge that output. Both commands validate the
# recorded protocol hash before initializing a model.
# Legacy protocol-v1 results remain available to offline analysis and comparison,
# but cannot be re-answered or judged as protocol-v2 results.
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -lme-reanswer-results ../results/lme-upstream/results.json \
  -model "$LME_ANSWER_MODEL" \
  -eval-model "$LME_JUDGE_MODEL" \
  -model-variant "$LME_MODEL_VARIANT" \
  -embed-model "$LME_EMBED_MODEL" \
  -lme-judge-runs 3 \
  -lme-answer=true \
  -vector-topk 30 \
  -lme-reanswer-reuse-source-answers=false \
  -lme-answer-cache ../results/lme-answer-replicate-2-cache.json \
  -output ../results/lme-upstream
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -lme-judge-results ../results/lme-upstream/reanswered_results.json \
  -model "$LME_ANSWER_MODEL" \
  -eval-model "$LME_JUDGE_MODEL" \
  -model-variant "$LME_MODEL_VARIANT" \
  -embed-model "$LME_EMBED_MODEL" \
  -lme-judge-runs 3 \
  -lme-answer=true \
  -vector-topk 30 \
  -lme-judge-cache ../results/lme-judge-cache.json \
  -output ../results/lme-upstream

# Re-run pgvector retrieval and answers against the exact persisted memories
# from a completed run, without paying the ingestion cost again.
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -lme-refresh-retrieval-results ../results/lme-candidate/results.json \
  -table-suffix _lme_candidate \
  -model "$LME_ANSWER_MODEL" \
  -eval-model "$LME_JUDGE_MODEL" \
  -model-variant "$LME_MODEL_VARIANT" \
  -embed-model "$LME_EMBED_MODEL" \
  -lme-judge-runs 3 \
  -lme-answer=true \
  -vector-topk 30 \
  -output ../results/lme-candidate

# Refresh final memory snapshots and evidence without model calls. This is
# useful when an older benchmark build recorded only a bounded snapshot. The
# command verifies that every recorded memory still exists unchanged, preserves
# retrieval/answers/judges/usage, and writes snapshot_refreshed_results.json.
MEM0_HOST=http://localhost:8888 ./run-longmemeval.sh \
  -dataset-format longmemeval \
  -memory-backend mem0 \
  -lme-refresh-memory-snapshots ../results/lme-upstream/judged_results.json \
  -vector-topk 30 \
  -output ../results/lme-upstream

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

# Aggregate three independently answered and judged three-arm comparisons.
go run . \
  -dataset-format longmemeval \
  -lme-compare-replicates ../results/lme-holdout/replicates.json \
  -output ../results/lme-holdout
```

The replicate manifest freezes the statistical and cost promotion gate alongside
the input files. Paths are resolved relative to the manifest:

```json
{
  "schema_version": 2,
  "replicates": [
    {
      "name": "primary",
      "kind": "primary",
      "baseline_results": "primary/baseline/judged_results.json",
      "candidate_results": "primary/candidate/judged_results.json"
    },
    {
      "name": "answer-2",
      "kind": "independent-reanswer",
      "baseline_results": "answer-2/baseline/reanswered_judged_results.json",
      "candidate_results": "answer-2/candidate/reanswered_judged_results.json"
    },
    {
      "name": "answer-3",
      "kind": "independent-reanswer",
      "baseline_results": "answer-3/baseline/reanswered_judged_results.json",
      "candidate_results": "answer-3/candidate/reanswered_judged_results.json"
    }
  ],
  "gate": {
    "expected_cases": 16,
    "judge_runs": 3,
    "per_type_max_deficit": 0,
    "memory_llm_token_ratio_maximum": 1.55,
    "memory_llm_uncached_token_ratio_maximum": 1.55,
    "memory_embedding_request_ratio_maximum": 2.0,
    "memory_embedding_token_ratio_maximum": 2.5,
    "final_memory_count_ratio_maximum": 3.0
  }
}
```

The first entry is the primary ingestion run. Later entries must be produced
with `-lme-reanswer-reuse-source-answers=false`; every entry must start with
empty, distinct answer and judge cache ledgers. The baseline and candidate
memory source runs must also use separate, initially empty model and embedding
response ledgers. This prevents arm order from changing provider-observed
memory cost; cache-independent logical usage remains the promotion-gate basis.
Judging a `reanswered_results.json` artifact writes
`reanswered_judged_results.json`, which is the file referenced by each
independent-reanswer manifest entry.
The aggregator verifies that ingestion,
persisted memories, retrieval hits, and memory-layer usage are byte-stable after
normalizing answer and judge fields. It then reports primary accuracy, majority
accuracy, total correct answer replicates, per-type results, instability, and
source-run cost for the fixed pgvector-main, Mem0, and pgvector-candidate arms.
The candidate passes only when both majority and replicate totals strictly beat
both baselines, category deficits stay bounded, all usage is reported, and the
pre-registered cost ratios hold. Embedding cost is gated on logical requests,
which are independent of shared response-ledger execution order; provider calls
and tokens remain visible as realized cache-sensitive cost. When
`memory_llm_uncached_token_ratio_maximum` is positive, the gate also bounds logical
total tokens minus cached prompt tokens; omitting it preserves manifests
created before this dimension was introduced. A positive
`memory_embedding_token_ratio_maximum` independently bounds realized embedding
tokens, so fewer requests cannot hide substantially longer embedded content.
Formal replicate
aggregation also rejects incomplete answer or judge logical usage. The JSON,
TSV, and Markdown outputs retain input hashes and gate details for audit. Each
JSON gate check is classified as `integrity`, `outcome`, or `cost`, with
separate `integrity_passed`, `outcome_passed`, and `cost_passed` summaries. A
valid negative quality result therefore remains distinguishable from a broken
or incomparable experiment.

The comparison JSON and Markdown also report paired majority outcomes for the
candidate against each baseline: candidate wins, baseline wins, ties, accuracy
delta, discordant question count, and the two-sided exact McNemar p-value.
The inference unit is one question after majority voting. Answer replicates
measure stability and contribute to the promotion gate, but are not treated as
independent samples for significance. This distinction is especially important
for small holdouts, where a positive accuracy delta may still have weak
statistical evidence.

`-lme-answer-cache` supplies a shared, content-addressed answer ledger. Its key
covers the exact ordered memories and metadata shown to the answer model, the
question prompt, model, variant, generation settings, and protocol versions;
storage IDs and similarity scores that the model cannot see do not affect the
key. Cache hits record their source and key, contribute zero provider calls or
tokens, and replay the cached model-call traces and logical token usage.
Re-answering a result may seed the ledger from an existing successful answer
only when the recorded model, variant, prompt version, and
generation settings all match. A strict comparison rejects runs that record
different answer ledgers or do not both use a persistent shared ledger.
Set `-lme-reanswer-reuse-source-answers=false` with a new empty cache when an
independent answer replicate is required. The result records this choice, and
the model must answer every cache miss instead of seeding from the source run.
An answer records every execution attempt. A response that ends because of a
length limit is regenerated once with a concise retry prompt and the configured
larger token budget; an answer that is still empty or truncated permits up to
two bounded replacement attempts. Failed answers are never cached as successful
entries. Formal replicate replacement is all-arms and all-cases:
if one backend invalidates a replicate, create a fresh answer and judge ledger
and replace that complete replicate rather than resampling only the failed
case.

For causal ablations of ingestion behavior, `-lme-model-response-cache` can
share complete primary-run model response streams between sequential runs. Its
key covers the deterministic request, tool declarations, model, and variant;
the ledger stores only the request hash and responses, not request messages or
headers. Both arms must also set the same explicit `-lme-user-scope`; otherwise
run-specific user IDs change generated memory IDs embedded in later extraction
prompts. The runner rejects a persistent model-response cache without that
scope. Run the control first to populate the ledger, then run the candidate with
the same cache and scope, normally with `-lme-answer=false` so answer evaluation
stays separate. Use a fresh scope and isolated stores for each experiment.
Exact request matches replay with zero model calls and tokens, while requests
changed by an earlier candidate state remain honest cache misses. A strict
comparison rejects different or implicit user scopes, different or ephemeral
model-response ledgers, and any recorded cache error. This mode controls
model-response variance for mechanism attribution. If the ablation can change
how often an identical embedding text reaches the provider, also supply one
shared `-lme-embedding-response-cache` ledger. It fixes the exact vector for
each text hash, model, and dimension across both arms, preventing provider-side
vector drift from changing retrieval order before model replay. Embedding usage
then records logical `requests`, ledger `response_cache_hits`, and real provider
`calls`/tokens separately. Strict comparison verifies that both arms used the
same persistent embedding ledger without cache errors. Use an independent
uncached run for production cost.

The judge command checkpoints `judged_results.json` after each case. An odd
`-lme-judge-runs` value greater than one records every independent vote and
uses a strict majority. Each requested run is a valid vote; transient or
malformed attempts are retained and permit up to two bounded replacement
attempts. The command checkpoints diagnostics and fails if it still cannot
collect the requested vote count. `-lme-judge-cache` supplies a shared,
content-addressed verdict ledger. The key covers the exact judge prompt, model,
variant, generation and retry settings, protocol version, and vote count, so
identical answers across backends or result files receive the same verdict.
Incomplete consensuses are never reused or cached. Cache reuse is recorded per
result and does not double-count judge tokens. When resuming from that file
with the same keyed judge contract, it keeps validated verdicts and retries
only missing or invalid ones. Analysis treats a valid
semantic-judge result as the primary correctness signal and falls back to exact
match when no judge result is available. It writes `analysis.md` and
`bad_cases.tsv`, including raw pipeline stages, evidence status, backend
disagreements, and answer-gap diagnostics. Comparison uses the same correctness
rule and rejects runs whose dataset, selection, replay protocol, retrieval
depth, answer model, embedding model, prompt versions, or judge configuration
differ. It writes `comparison.md` and `comparison.tsv`, compares upstream and
candidate pgvector quality and cost, and presents Mem0 from the upstream run as
a frozen third arm. Pass the same persistent judge-cache file to every formal
arm; judged comparison verifies its stable ledger ID and rejects results from
different or ephemeral caches.
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

Provider token counters cover model and embedding calls made by this process,
including pgvector extraction, retrieval, and answer generation. Answer and
judge summaries additionally report logical token counters, which retain the
original prompt, completion, prompt-cache, and reasoning usage when a
content-addressed cache avoids a repeated provider call. A self-hosted mem0 can
return internal LLM, cached-token, and embedding usage in `X-Mem0-Usage`;
`provider_usage_reported` and the analysis coverage column show whether that
usage was included. Stock mem0 servers do not return it, so their missing
internal usage must not be interpreted as zero-cost usage.
The runner also reads and records the sanitized self-hosted mem0 runtime
configuration. `-mem0-llm-temperature` changes that configuration only when it
is non-negative; the default keeps the server value while still recording it.
Both LoCoMo and LongMemEval resolve `-model-variant` through the same provider
adapter and record the resolved variant. This is required for provider-specific
thinking controls and response fields to be encoded consistently.

LoCoMo memory QA explicitly disables model thinking, uses low reasoning effort,
and reserves 512 output tokens for tool calls and the final short answer. Empty,
truncated, multiline, or overlong answers receive one recovery call constrained
to a `submit_answer` tool with a 2048-token budget. The larger recovery budget
leaves room for providers that emit hidden or visible reasoning before the
forced tool call. Recovery input deduplicates repeated search hits and removes
storage-only fields while preserving memory text and semantic metadata.
If that one call still fails validation, the evaluator records the failure and
uses the standard unavailable answer as a deterministic fallback. Terminal
empty responses are not retried, and their call, finish reason, prompt-cache,
reasoning-token, and total-token usage remain in the QA trace.
During fresh auto-memory replay, LoCoMo waits for each session's extraction
completion marker before enqueueing the next session. This preserves session
order and prevents later queued jobs from mutating an isolated experiment table
after an earlier extraction has failed. `-auto-extraction-timeout` bounds the
whole sample replay, while `-auto-memory-job-timeout` bounds each session's
extraction; both effective values are recorded in result metadata.
`-locomo-reuse-memories` skips only the auto
extraction phase and requires an explicit `-table-suffix`; it fails when the
selected table has no memories for the sample. The QA searches and answers are
still regenerated, and the result metadata records the table, reuse mode, and
build provenance.

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
| `-pgvector-update-policy` | reconcile            | `reconcile`, `history-preserving`, or `add-only` |
| `-pgvector-assistant-result-extraction` | false | Retain concrete assistant results   |
| `-mysql-dsn`        | (env)                  | MySQL DSN for mysql backend            |
| `-embed-model`      | text-embedding-3-small | Embedding model for vector backends    |
| `-vector-topk`      | 30                     | Top-k results for vector backends      |
| `-qa-history-turns` | 0                      | Inject N conversation turns as context |
| `-qa-search-passes` | 2                      | memory_search calls per QA             |
| `-auto-extraction-timeout` | derived from session count | Total auto-memory replay timeout |
| `-auto-memory-job-timeout` | 2m              | Per-session auto-memory job timeout    |
| `-locomo-reuse-memories` | false             | Run QA against an explicit existing table |
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
| `-lme-exclude-question-ids` |      | Exclude IDs before filtering and sampling    |
| `-lme-exclude-question-ids-file` | | Exclude one `question_id` per non-empty line |
| `-lme-question-types`    |         | Comma-separated `question_type` filter       |
| `-lme-per-type`          | 0       | Stratified sample count per question type    |
| `-lme-abstention-count`  | 0       | Additional abstention questions to sample    |
| `-lme-sample-seed`       | 42      | Sampling seed                                |
| `-lme-selection-only`    | false   | Print selection provenance, then exit        |
| `-lme-preregistered-selection` |   | Execute and verify an exact selection manifest |
| `-lme-max-sessions`      | 0       | Max haystack sessions per case               |
| `-lme-max-pairs`         | 0       | Max user/assistant pairs per case            |
| `-lme-user-scope`        |         | Stable user-ID scope for paired primary runs |
| `-lme-ingest-wait`       | 250ms   | Extra delay after completed pair ingestion   |
| `-lme-model-call-timeout` | 5m      | Model timeout; mem0 OSS allows 1m overhead   |
| `-lme-answer`            | true    | Generate answers from retrieved memories     |
| `-lme-answer-cache`      |         | Shared content-addressed answer cache         |
| `-lme-answer-cache-require-hit` | false | Fail before an uncached answer provider call |
| `-lme-model-response-cache` |      | Persistent primary-run model response ledger  |
| `-lme-model-response-cache-require-hit` | false | Fail before an uncached model provider call |
| `-lme-embedding-response-cache` |  | Persistent exact-vector embedding ledger      |
| `-lme-embedding-response-cache-require-hit` | false | Fail before an uncached embedding provider call |
| `-lme-blind-progress`    | false   | Hide identifiers and outcome content from progress and case logs |
| `-lme-implementation`    | (build) | Label; flag/env override pinned build identity |
| `-lme-reanswer-results`   |         | Re-answer using saved ranked retrieval hits  |
| `-lme-reanswer-reuse-source-answers` | true | Seed cache from compatible source answers |
| `-lme-refresh-memory-snapshots` | | Refresh final snapshots without model calls  |
| `-lme-refresh-retrieval-results` | | Refresh persisted pgvector retrieval         |
| `-lme-allow-shared-table-refresh` | false | Refresh an audited legacy shared table |
| `-lme-rerank-results`     |         | Rerank saved hits for every result backend   |
| `-lme-rerank-topn`        | 12      | Maximum memories selected by the reranker    |
| `-lme-judge-results`     |         | Add semantic judge results to `results.json` |
| `-lme-judge-runs`        | 1       | Odd number of independent semantic votes     |
| `-lme-judge-cache`       |         | Shared content-addressed judge verdict cache  |
| `-lme-judge-cache-require-hit` | false | Fail before an uncached judge provider call |
| `-lme-analyze-results`   |         | Analyze one saved LongMemEval `results.json` |
| `-lme-compare-results`   |         | Compare baseline,candidate `results.json`    |
| `-lme-compare-replicates` |        | Aggregate a preregistered replicate manifest |
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
| `LME_IMPLEMENTATION`        | LongMemEval implementation label override |

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
