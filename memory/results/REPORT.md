# Long-Term Conversational Memory Evaluation on LoCoMo and LongMemEval

> [!NOTE]
> **Research archive (2026-08-26).** This report preserves the completed
> formal evaluations and their original interpretation. A later confirmatory
> observed48 run stopped after two runtime-incomplete baseline attempts; the
> candidate and judges were never run, so that attempt adds no comparative
> quality result. No merge is requested for the companion draft PRs. See
> [ARCHIVE.md](ARCHIVE.md) for the terminal status and artifact policy.

## 1. Introduction

This report evaluates the long-term conversational memory of
**trpc-agent-go** on two complementary benchmarks:

- **LoCoMo** measures broad conversational QA quality over 1,986
  questions and supports comparisons with agent frameworks and
  published memory systems.
- **LongMemEval** isolates the production memory lifecycle across
  extraction, persistence, retrieval, and answer generation. The
  evaluation directly compares the trpc-agent-go pgvector memory
  service with a self-hosted mem0 deployment.

The LoCoMo evaluation covers a long-context upper bound and three memory
strategies:

- **Long-Context**: Full transcript in the model context
- **trpc-agent-go (original)**: Baseline version (Auto extraction + pgvector)
- **trpc-agent-go (optimized)**: After multiple rounds of optimization
  including contextualized memory extraction, episodic memory
  classification, hybrid search, and multi-pass retrieval
  (see Section 2.3 for details)
- **Session Recall**: Query-time retrieval over persisted raw session events

These approaches are compared against four Python agent frameworks
(AutoGen, Agno, ADK, CrewAI) and ten external memory systems
(Mem0, Zep, etc.). LongMemEval is evaluated as a staged evidence program:

1. a fixed, already-observed 16-question Oracle set for development and
   mechanism ablation;
2. a preregistered 8-question, full-haystack unseen non-target gate; and
3. a 48-question, full-haystack same-size regression on the previously
   observed selection used by the baseline.

On the 48-question regression, the final pgvector candidate reaches 43/48
majority-correct questions and 127/144 correct answer replicates, compared
with 41/48 and 124/144 for self-hosted Mem0 OSS and 24/48 and 73/144 for
upstream main. This is a descriptive lead over Mem0, not a statistically
significant one (exact McNemar p=0.6875). The quality gate passes, but the
overall promotion gate does not: uncached memory LLM tokens are 1.5699x main
against a preregistered 1.55x limit. The result therefore supports the
candidate's quality mechanisms while explicitly retaining a cost limitation.

The LoCoMo tables retain historical artifacts. The trpc-agent-go `Original`,
`Optimized`, and Agentic runs used auto-replay-v3, which executed a placeholder
agent turn after each historical session. That path could append a synthetic
assistant response and duplicate the latest user turn when the source session
ended with the transport `assistant` role. Exact-replay-v4 now writes each
mapped dataset turn once and does not execute an agent. Long-Context, Session
Recall, and manually seeded external-framework runs were unaffected; the
legacy trpc-agent-go Auto/Agentic values are descriptive only until rerun under
v4.

## 2. Experimental Setup

### 2.1 Benchmarks

| Benchmark | Scope | Categories | Model | Embedding |
| --- | --- | --- | --- | --- |
| LoCoMo-10 | 10 conversations, 1,986 QA | single-hop (282), multi-hop (321), temporal (96), open-domain (841), adversarial (446) | GPT-4o-mini (inference + judge) | text-embedding-3-small |
| LongMemEval Oracle development | Fixed observed 16-question development regression from 500 questions; 183 replayed pairs per arm | all six LongMemEval types; four selected abstention questions | glm52 (memory, answer, and judge) | text-embedding-3-small |
| LongMemEval-S full-haystack | Preregistered unseen non-target 8-case gate plus observed same-size 48-case regression; 1,954 and 11,839 replayed pairs per arm | all available unseen non-target types in the 8-case gate; all six types in the 48-case regression | glm52 (memory, answer, and judge) | text-embedding-3-small |

LoCoMo is the broad quality and cross-system benchmark. LongMemEval Oracle
removes the large irrelevant-session haystack so that failures can be
attributed more precisely to extraction, persistence, retrieval, or answer
generation. LongMemEval-S then restores each selected question's complete
haystack. Here, "full-haystack 48" means 48 selected questions with all of
their sessions, not the complete 500-question dataset. Earlier protocol-v1
subsets remain useful for diagnosis, but their date transport differed
between backends and they no longer support the formal cross-backend claim.

### 2.2 LoCoMo Scenarios

| Scenario | Description |
| --- | --- |
| **Long-Context** | Full transcript as LLM context (upper bound) |
| **Session Recall** | Query-time search over persisted raw historical session events |
| **Original** | Auto extraction + pgvector baseline; background extractor writes memories and retrieves them at query time |
| **Optimized** | Optimized memory extraction strategy and multi-pass retrieval over extracted memories |

New Auto runs use `chronological-session-sequential-auto-v4`: each mapped
dataset user/assistant turn is written exactly once, then one extraction job is
run for the complete session. The historical Original/Optimized artifacts in
this report used v3 and cannot gate the current candidate. Session Recall
already used direct event replay and is not affected by this correction.

### 2.3 Memory Optimizations

For LoCoMo, the optimized version builds on the original baseline with a series
of targeted improvements across the memory extraction, storage, and
retrieval pipeline:

1. **Contextualized Memory Extraction** — The original extractor
   produces flat, unstructured memory strings. The optimized version
   uses a comprehensive extraction prompt that enforces **atomicity**
   (one fact per memory), **completeness** (all speakers, all
   details), and **specificity** (exact names, dates, quantities).
   This significantly improves information density and recall.

2. **Episodic Memory Classification** — Each extracted memory is
   classified as either a **Fact** (stable attributes, preferences,
   relationships) or an **Episode** (time-anchored events with
   `event_time`, `participants`, and `location` metadata). This
   structured schema enables temporal filtering and event-time
   ordering during retrieval, which is critical for multi-hop and
   temporal questions.

3. **Absolute Date Resolution** — Relative time expressions in
   conversations ("yesterday", "last month") are resolved to
   absolute ISO 8601 dates using the session's reference date
   before being stored. This prevents temporal drift and enables
   accurate date-based queries.

4. **Topic Tagging** — Each memory is tagged with descriptive
   topics (e.g., `["hiking", "Mt. Fuji", "travel"]`), and the
   extractor is instructed to reuse existing topic names rather
   than inventing synonyms. Topics improve retrieval relevance
   and enable future topic-based filtering.

5. **Hybrid Search (Vector + Keyword)** — The original uses
   pure vector similarity search. The optimized version adds
   **hybrid search** that combines vector cosine similarity with
   PostgreSQL full-text search (`tsvector/tsquery`), merged via
   **Reciprocal Rank Fusion (RRF)**. This improves recall for
   queries containing specific entity names, book titles, or
   exact-match terms that vector embeddings alone may not rank
   highly.

6. **Multi-Pass Retrieval** — Instead of a single search, the
   QA agent performs **2–3 search passes** with different query
   strategies (e.g., keyword-style query, entity-focused query,
   broad name query). Each pass uses different angles to maximize
   recall before the final answer.

7. **Kind Fallback** — When a kind-filtered search (e.g.,
   episodes only) returns too few results (< 3), the system
   automatically falls back to an unfiltered search and merges
   both result sets, prioritizing the requested kind. This
   prevents missed results when kind classification is uncertain.

8. **Content Deduplication** — Near-duplicate memories (> 80%
   word-level Jaccard similarity) are deduplicated, keeping only
   the highest-scored version. This reduces redundant context
   in the retrieval results.

LongMemEval then exposed a different set of production-path reliability
problems. The resulting changes retain concrete assistant answers and
structured deliverables, carry observation times through extraction, and
retry malformed structured extraction output. If extraction still produces
no operation, qualifying long assistant output is stored through a
conservative fallback.

The final candidate keeps the second-pass structured-result recovery narrow:
it receives the current dated user/assistant pair, while persistence still
uses the existing-memory snapshot for duplicate and conflict handling. This
removes an unnecessary copy of all existing memories from the recovery prompt
without weakening downstream reconciliation.

The current candidate additionally compacts the assistant-result extraction
instructions and gives the private assistant-result tool a focused schema with
required `memory` and optional `topics` fields. This does not add public API or
change the opt-in boundary. It reduces repeated prompt text while retaining the
rules for source attribution, exact values, and cohesive structured results.

Extraction context also retains cumulative observation times so that a later
turn cannot erase when an earlier state was observed. Focused source passages
preserve the concrete entity or list that triggered assistant-result recovery,
and temporal retrieval keeps a bounded tail of dated events alongside hybrid
search results. These mechanisms are opt-in for the candidate arm; ordinary
user-memory updates continue to use the compatible default Merge Similar
reconciliation behavior.

The final retrieval stage uses the source marker already stored with assistant
results. Explicit references to a past assistant answer add an
assistant-result ranking to RRF; ordinary fact, preference, and current-advice
queries add a user-grounded ranking instead. This is a soft fourth signal, not
a filter, and it does not change similarity scores, persisted memories, or the
public API. The intent classifier is deliberately narrow: a bare "remind me,"
generic follow-up, or current recommendation request is not treated as an
assistant-history query.

An earlier candidate exposed a parallel history-preserving policy surface for
ordinary memories. A fresh LoCoMo policy ablation did not support selecting it
for this candidate: the default Merge Similar behavior scored slightly higher
overall and won two of three answer repeats. Upstream subsequently introduced
the canonical `MergeSimilar`, `PreserveHistory`, and `AppendOnly` enum. The
integrated implementation adopts that API and removes the candidate's
duplicate names and policy plumbing; the LongMemEval candidate remains on
`MergeSimilar`. Strict preservation remains private to assistant-result
memories, where rewriting a cited answer or structured deliverable would lose
the evidence the feature is intended to retain.

Automatic Add reconciliation now rewrites only high-confidence
near-duplicates; related plans, recommendations, events, and entity lists
remain distinct. Add, Update, Delete, and Clear failures are propagated
from asynchronous jobs, exposed through session state, and no longer
advance the extraction completion marker. A benchmark-like extraction
example was also replaced with synthetic content to avoid prompt leakage.

### 2.4 LongMemEval Replay and Fairness

Each LongMemEval question owns a separate user and run scope. Haystack
sessions are sorted chronologically and replayed one user/assistant pair at
a time. After every pair, pgvector invokes the production
`memory.Service.EnqueueAutoMemoryJob` path and waits for completion; mem0
receives the same raw pair through its public API. The source session date is
transported outside message content and fills each backend's observation-date
context. The answer model sees only searched memories, never the raw
transcript.

All arms use glm52 at temperature 0, `text-embedding-3-small`, and top-k 30
retrieval. The accepted frozen baseline compares upstream-main pgvector with
default Merge Similar, the candidate with default Merge Similar plus
assistant-result extraction, and a pinned self-hosted Mem0 OSS image backed by
pgvector. The
final provenance-ranking refinement refreshes retrieval from the candidate's
exact persisted-memory snapshot and then runs fresh answers and judges. This
separates a retrieval change from extraction variance and allows ingestion
usage to be inherited only after byte-stable memory verification. The runner
records extraction operations, memory diffs, retrieval hits, evidence
provenance, errors, timings, LLM and embedding usage, cached tokens, build
revisions, and sanitized Mem0 configuration.

Every evaluated arm answers from its saved top-k three times, and each answer
receives three independent semantic-judge votes. An empty content-addressed
answer and judge ledger is shared across arms within a replicate; different
replicates use distinct ledgers. Exact match, F1, and BLEU remain secondary
diagnostics.

The three LongMemEval phases have different roles:

- **Observed Oracle16 development:** two answerable questions from each type
  plus four abstention questions, with 183 replayed pairs per arm. It is used
  for bad-case analysis and direct mechanism ablation, not promotion.
- **Preregistered unseen non-target8 gate:** two questions each from
  knowledge-update, multi-session, single-session-user, and
  temporal-reasoning, with 1,954 replayed pairs per arm. It excludes 373
  historically exposed IDs, forbids adaptive tuning, and compares the final
  named-example candidate against its immediate control. All 56
  single-session-assistant and all 30 single-session-preference questions had
  already been exposed, so this phase can establish only non-target
  non-regression, not unseen assistant-result benefit.
- **Observed full-haystack48 regression:** the exact prior baseline selection,
  containing 9 knowledge-update, 9 multi-session, 9
  single-session-assistant, 3 single-session-preference, 9
  single-session-user, and 9 temporal-reasoning questions. Each arm replays
  11,839 pairs. Its evidence scope is post-blind observed same-size
  regression, not an unseen holdout.

Dataset, selection, protocol, prompt, model, build, and Mem0 runtime digests
are checked before comparison. Gates are frozen before provider calls and
cover majority quality, correct answer replicates, category regressions,
provider usage, backend errors, LLM and embedding cost, and memory count.
Shared model and embedding response caches are content-addressed and applied
symmetrically; consequently wall-clock latency is diagnostic rather than a
fairness claim.

The 48-case candidate run had one transient extraction timeout. Recovery was
limited to that failed backend/case unit in an isolated table, under the same
dataset, build, protocol, and cache lineage. The earliest technically valid
retry replaced the failed unit; complete arms were not rerun or cherry-picked.
Fresh answer and judge ledgers were then used for all three repetitions, and
all final integrity audits and checksums passed.

## 3. Results

Sections 3.1-3.3 report legacy LoCoMo artifacts. Their recorded values are left
unchanged for provenance, but comparisons involving trpc-agent-go Auto,
Agentic, SQLite, or SQLiteVec require an exact-replay-v4 rerun. Section 3.4 is a
separate LongMemEval experiment and is unaffected.

### 3.1 Internal Scenario Comparison (Legacy LoCoMo Replay)

**Table 1: Overall Metrics**

| Scenario | F1 | BLEU | LLM Score | Tokens/QA | Calls/QA | Latency | Total Time |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Long-Context | 0.469 | 0.426 | 0.526 | 18,776 | 1.0 | 2,607ms | 1h26m |
| Session Recall | **0.549** | **0.511** | **0.609** | 3,694 | 1.0 | 6,430ms | 3h33m |
| Optimized | **0.469** | **0.431** | **0.532** | 17,182 | 3.0 | 8,585ms | 4h44m |
| Original | 0.399 | 0.371 | 0.416 | 3,056 | 2.0 | 6,659ms | 3h40m |

> The optimized version's F1 improved from 0.399 to **0.469**
> (+17.5%), reaching **99.9%** of Long-Context F1 (up from 85.1%
> for original). Although the nominal Tokens/QA (17,182) is higher,
> **43.9% are served from prompt cache**, making the effective new
> token cost ~9,663/QA (see Section 4.5).
>
> As a supplemental retrieval path, Session Recall now pushes
> overall F1 to **0.549** while keeping Tokens/QA at **3,694**.
> Compared with Long-Context, it uses **80.3% fewer tokens** per QA;
> compared with the optimized version, it uses **78.5% fewer tokens**.

**Table 2: F1 by Category**

| Category | Count | Long-Context | Session Recall | Optimized | Original |
| --- | ---: | ---: | ---: | ---: | ---: |
| single-hop | 282 | 0.320 | 0.368 | **0.396** | 0.316 |
| multi-hop | 321 | 0.308 | **0.554** | 0.453 | 0.096 |
| temporal | 96 | 0.088 | 0.174 | **0.247** | 0.088 |
| open-domain | 841 | 0.518 | **0.618** | 0.441 | 0.358 |
| adversarial | 446 | 0.667 | 0.610 | 0.626 | **0.814** |

**Table 3: Weighted Average F1**

| Average | Long-Context | Session Recall | Optimized | Original |
| --- | ---: | ---: | ---: | ---: |
| 5-category weighted (÷1986) | 0.469 | **0.549** | 0.469 | 0.399 |
| 4-category weighted (÷1540, excl. adversarial) | 0.411 | **0.531** | 0.423 | 0.279 |

> The optimized version still achieves improvements across all four
> knowledge categories. Multi-hop improved from 0.096 to 0.453
> (+372%), the most significant gain. Temporal improved from
> 0.088 to 0.247 (+181%), the second largest gain. Adversarial
> decreased (0.814 → 0.626) as the original had an overly
> aggressive refusal tendency.
>
> As a supplement, Session Recall now changes the trade-off profile
> much more substantially. It is best on **multi-hop** and
> **open-domain**, improves **temporal** to 0.174, and raises
> 4-category weighted F1 to **0.531**. The optimized version remains
> stronger on **single-hop** and **temporal**, while Long-Context and
> the optimized version still retain a small edge on **adversarial**.

**Table 4: Per-Sample F1**

| Sample | #QA | Long-Context | Session Recall | Optimized | Original |
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
| **Average** | **199** | 0.469 | **0.549** | 0.469 | 0.399 |

> The optimized version improves on all 10 samples vs original, and
> surpasses Long-Context on 6 samples.
>
> As a supplement, Session Recall now beats Long-Context on all 10
> samples and beats the optimized version on all 10 samples, with the
> largest gains on `locomo10_2`, `locomo10_3`, and `locomo10_5`.

### 3.2 Retrieval Strategies vs Long-Context

Long-Context places the full transcript into a single LLM call.
It is effective for short single-session histories, but the two
retrieval-based strategies expose different production trade-offs:

| Dimension | Long-Context | Session Recall | Optimized |
| --- | --- | --- | --- |
| **Cross-session source** | None | Searches raw historical session events at query time | Searches extracted persistent memories |
| **Context window** | Bounded by model limit (128K for GPT-4o-mini) | Unbounded — injects only recalled events | Unbounded — injects only retrieved memories |
| **Scaling** | Cost grows linearly with transcript length | Cost stays near-constant (top-K retrieval) | Cost grows with tool-call steps and retrieved memory payload |
| **Overall F1** | 0.469 | **0.549** | 0.469 |
| **4-category weighted F1** | 0.411 | **0.531** | 0.423 |
| **Tokens/QA** | 18,776 | **3,694** | 17,182 |
| **Best strengths** | Adversarial robustness | Overall accuracy, open-domain, and multi-hop | Temporal and adversarial balance |

---

### 3.3 SQLite vs SQLiteVec (Subset Run)

This subsection compares `sqlite` (keyword matching) and `sqlitevec`
(semantic vector search via sqlite-vec) on a few controlled subset runs.

**Subset run A: End-to-end QA (Auto / Full categories)**

This run keeps the same end-to-end pipeline and evaluation settings as the
main experiments, but limits to a single sample to control cost.

**Configuration**:

- Dataset: LoCoMo `locomo10.json`
- Sample: `locomo10_1` (199 QA, all categories)
- Scenario: `auto`
- Model: `gpt-4o-mini`
- LLM Judge: enabled
- Embedding model (SQLiteVec): `text-embedding-3-small`
- SQLiteVec retrieval top-k: 10 (default)

**End-to-end results: Overall Metrics and Token Usage (Auto / 199 QA)**

| Backend | #QA | F1 | BLEU | LLM Score | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | Avg Latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 199 | 0.327 | 0.301 | 0.370 | 1,287,813 | 5,624 | 1,293,437 | 398 | 5,805ms |
| SQLiteVec | 199 | 0.307 | 0.285 | 0.325 | 407,969 | 5,556 | 413,525 | 396 | 6,327ms |

**Interpretation (locomo10_1)**:

- **SQLiteVec reduces prompt tokens by ~3.2x** (bounded top-k retrieval),
  but **F1/BLEU/LLM Score are slightly lower** on this sample at the
  default top-k=10 setting.
- Category-level behavior differs: `sqlitevec` improves `adversarial`
  (more correct refusals), but underperforms on other categories when the
  needed evidence is not retrieved within top-k.

We also rerun the same configuration on another representative sample.

- Sample: `locomo10_6` (158 QA, all categories)

**End-to-end results: Overall Metrics and Token Usage (Auto / 158 QA)**

| Backend | #QA | F1 | BLEU | LLM Score | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | Avg Latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 158 | 0.269 | 0.243 | 0.289 | 1,296,580 | 5,103 | 1,301,683 | 340 | 6,359ms |
| SQLiteVec | 158 | 0.274 | 0.254 | 0.295 | 362,903 | 4,773 | 367,676 | 324 | 6,928ms |

**Overall takeaway (locomo10_1 + locomo10_6)**:

- SQLiteVec consistently reduces prompt tokens by ~3x-4x in our runs.
- Answer quality changes are sample-dependent at the default top-k=10;
  increasing top-k can improve recall but will also increase prompt tokens.

> Note: `Prompt Tokens`, `LLM Calls` count only the QA agent model calls.
> They exclude embedding requests and LLM-as-Judge calls. `Avg Latency`
> reflects end-to-end time averaged by #QA (including embeddings, judge,
> and auto extraction).

**Subset run B: Temporal-only token-cost micro-run**

**Configuration**:

- Dataset: LoCoMo `locomo10.json`
- Sample: `locomo10_1`
- Category filter: `temporal` (13 QA)
- Scenario: `auto`
- Model: `gpt-4o-mini`
- LLM Judge: disabled
- Embedding model (SQLiteVec): `text-embedding-3-small`

**Table 5: Overall Metrics and Token Usage (Auto / Temporal / 13 QA)**

| Backend | F1 | BLEU | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | Avg Latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 0.116 | 0.082 | 80,184 | 352 | 80,536 | 26 | 12,352ms |
| SQLiteVec | 0.116 | 0.082 | 26,483 | 353 | 26,836 | 26 | 17,817ms |

**Subset run C: Vector top-k sweep + multi-search ablation (Auto / Full categories)**

**Table 6: Top-k and Multi-search Sweep (Auto / locomo10_1 / 199 QA)**

| Backend | vector-topk | qa-search-passes | F1 | BLEU | Prompt Tokens | Avg Prompt/QA | Avg Latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | - | 1 | 0.299 | 0.283 | 1,322,360 | 6,645 | 3,316ms |
| SQLiteVec | 5 | 1 | 0.320 | 0.296 | 346,253 | 1,740 | 4,182ms |
| SQLiteVec | 10 | 1 | 0.343 | 0.315 | 398,751 | 2,004 | 4,352ms |
| SQLiteVec | 20 | 1 | 0.329 | 0.308 | 621,790 | 3,125 | 4,180ms |
| SQLiteVec | 40 | 1 | 0.327 | 0.303 | 965,423 | 4,851 | 4,460ms |
| SQLiteVec | 10 | 2 | 0.342 | 0.312 | 659,981 | 3,316 | 5,198ms |

**Interpretation**:

- **Increasing top-k does not monotonically improve quality**: top-k=20/40
  increases prompt tokens but slightly lowers F1/BLEU. The QA agent can
  be sensitive to noise in retrieved memories.
- `qa-search-passes=2` improves some categories (e.g. multi-hop) but does
  not improve overall F1, and increases both tokens and latency.

### 3.4 LongMemEval: pgvector vs Self-Hosted mem0

> **Evidence scope:** The results below deliberately separate observed
> development, preregistered unseen non-target non-regression, and observed
> full-haystack regression. The 48-case result is not a claim about all 500
> LongMemEval questions, and the 8 unseen cases do not test the target
> assistant-history categories.

The protocol-v2 comparison measures the production auto-memory path rather
than the LoCoMo retrieval variants above. Its evidence ladder is:

| Phase | Selection | Purpose | Result |
| --- | --- | --- | --- |
| Oracle development | 16 observed questions, Oracle sessions, 183 pairs/arm | Find bad cases and isolate mechanisms | Final candidate 16/16 majority, 48/48 answer replicates |
| Unseen non-target gate | 8 preregistered questions, full haystack, 1,954 pairs/arm | Detect regressions outside the target categories | PASS: candidate 8/8 and 24/24; control 8/8 and 23/24 |
| Same-size regression | 48 previously observed questions, full haystack, 11,839 pairs/arm | Compare final candidate with frozen main and Mem0 baselines | Quality PASS; cost FAIL; overall gate FAIL |

**Observed development and mechanism selection.** On Oracle16, upstream main
scored 11/16 majority and 33/48 answer replicates, Mem0 scored 14/16 and 42/48,
and assistant-result extraction without provenance ranking scored 15/16 and
46/48. Query-aware provenance RRF over the exact same candidate memory snapshot
reached 16/16 and 48/48. The last change corrected an abstention failure by
ranking user-grounded evidence above an assistant estimate while retaining 3/3
accuracy on the explicit assistant-history cases.

These observed ablations determined the final shape rather than adding every
candidate mechanism. Conservative assistant-result recovery was retained
because removing it caused a zero-memory extraction failure. Preserve History
was not selected for the candidate after the default Merge Similar policy
scored slightly higher in a fresh LoCoMo ablation and won two of three
repetitions. The post-experiment integration adopts upstream's canonical
policy enum while keeping strict assistant-result preservation private and
applying a narrow, query-aware retrieval signal.

The evaluated artifacts predate the final upstream merge. Their scores remain
tied to the revisions and digests below; `ed61e8956932` is the post-evaluation
integration head, not a relabeling of an evaluated build. The merge preserves
the selected algorithm, replaces duplicate policy APIs with upstream's API,
and is covered by unit and package tests, but it has not been re-run as another
48-case experiment.

**Preregistered unseen non-target gate.** Before provider calls, the experiment
froze seed 20260812, excluded 373 historically exposed question IDs, prohibited
adaptive tuning, and selected two questions from each of the four categories
that still had unexposed examples. Candidate and control both reached 8/8
majority; the candidate improved replicate stability from 23/24 to 24/24.
Every category tied 2/2. Candidate/control cost ratios were 1.0087 for logical
memory LLM tokens, 1.0137 for uncached tokens, 1.0140 for embedding requests,
1.0106 for logical embedding tokens, and 1.0287 for final memories, all below
the frozen 1.25 limit. Six answer/judge integrity audits and the final checksum
manifest passed. This result supports non-target non-regression only.

**Observed full-haystack48 quality.** The final comparison uses three fresh
answers per arm and three judge votes per answer:

| Arm | Primary | Majority | Correct replicates | Unstable | Backend/answer/judge errors |
| --- | ---: | ---: | ---: | ---: | ---: |
| pgvector main | 24/48 | 24/48 | 73/144 | 1 | 0/0/0 |
| Mem0 OSS | 40/48 | 41/48 | 124/144 | 5 | 0/0/0 |
| final pgvector candidate | **43/48** | **43/48** | **127/144** | **2** | **0/0/0** |

At question-majority level, candidate versus main has 19 wins, 0 losses, and
29 ties, a +39.58 percentage-point delta with exact McNemar p=0.0000038147.
Candidate versus Mem0 has 4 wins, 2 losses, and 42 ties, a +4.17 point delta
with p=0.6875. The candidate therefore clearly improves main, but its lead over
Mem0 is descriptive and statistically inconclusive at this sample size.

| LongMemEval type | Cases | pgvector main | Mem0 OSS | Candidate |
| --- | ---: | ---: | ---: | ---: |
| knowledge-update | 9 | 6 | **7** | **7** |
| multi-session | 9 | 5 | **9** | **9** |
| single-session-assistant | 9 | 1 | 7 | **8** |
| single-session-preference | 3 | 1 | 2 | **3** |
| single-session-user | 9 | 6 | **9** | **9** |
| temporal-reasoning | 9 | 5 | **7** | **7** |

The candidate wins two category-majority rows and ties Mem0 on four; no
category regresses. Replicate-level differences inside tied categories remain:
candidate/Mem0 are 21/21 on knowledge-update, 27/27 on multi-session, 26/27 on
single-session-user, and 20/21 on temporal-reasoning.

**Memory-layer resource accounting.** Values below exclude answer and judge
calls. Logical totals include response-cache hits so that each arm is charged
for equivalent work; cached and uncached tokens are shown separately. Ingest
time is diagnostic because shared caches and sequential execution make latency
order-dependent.

| Arm | LLM calls | Logical LLM tokens | Cached | Uncached | Embedding requests | Logical embedding tokens | Final memories | Ingest hours |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| pgvector main | 11,839 | 73,513,494 | 46,871,488 | 26,642,006 | 38,806 | 1,286,558 | 10,020 | 18.10 |
| Mem0 OSS | 11,873 | 118,491,412 | 92,562,432 | 25,928,980 | 22,643 | 6,464,761 | 26,413 | 22.51 |
| final pgvector candidate | 12,874 | 102,369,375 | 60,543,936 | 41,825,439 | 34,819 | 2,292,799 | 21,330 | 27.83 |

Relative to Mem0, the candidate uses 13.6% fewer total logical LLM tokens,
64.5% fewer logical embedding tokens, and 19.2% fewer final memories, but 61.3%
more uncached LLM tokens. Relative to main, the frozen cost gate evaluates:

| Cost check vs main | Candidate ratio | Limit | Result |
| --- | ---: | ---: | --- |
| Logical memory LLM tokens | 1.3925x | 1.55x | PASS |
| Uncached memory LLM tokens | **1.5699x** | **1.55x** | **FAIL** |
| Embedding requests | 0.8973x | 2.00x | PASS |
| Logical embedding tokens | 1.7821x | 2.50x | PASS |
| Final memories | 2.1287x | 3.00x | PASS |

The only failed check exceeds its allowed token cap by about 530,330 tokens,
or 1.28% of that cap. This is small enough to motivate later cost work, but it
must not be rounded into a pass or tuned away on the already-observed 48 IDs.

**Bad-case attribution.** Automated labels mark all five stable candidate
failures as `evidence_or_answer_miss` because the relevant source sessions are
present in extraction and retrieval. Manual trajectory inspection gives a more
useful diagnosis:

| Question | Type | Candidate / Mem0 / main | Earliest actionable failure |
| --- | --- | ---: | --- |
| `6071bd76` | knowledge-update | 0/3 / 3/3 / 0/3 | Reconcile updates the French-press ratio from 6 to 5 ounces but removes the old value, so the directional change cannot be answered. |
| `6e984301` | temporal-reasoning | 0/3 / 2/3 / 0/3 | Extraction normalizes the sculpting-class start and tool-purchase dates into a 9-week interval instead of the referenced 3 weeks. |
| `9ea5eabc` | knowledge-update | 0/3 / 1/3 / 0/3 | Paris, Hawaii, and Japan memories are retrieved, but inconsistent event dating and ranking select an older family trip as the latest. |
| `eaca4986` | single-session-assistant | 0/3 / 0/3 / 0/3 | Assistant-result compression stores a different song's patterns but omits the exact chorus sequence for the second song. |
| `gpt4_e414231e` | temporal-reasoning | 0/3 / 0/3 / 0/3 | Both bike events are retrieved, but both are normalized to 2023-03-15, producing 0 days instead of 4 (or 5 inclusive). |

These failures point to state-transition representation, temporal grounding,
and lossless structured-result extraction. They do not justify increasing
top-k or adding a generic similarity heuristic: the source sessions were
already retrieved in every case.

**Recovery and decision.** One candidate unit (`77eafa52`) timed out during
the original 48-case extraction. Three isolated retries all completed 265/265
pairs; an orchestration validator initially rejected them because omitted Go
`omitempty` zero fields were interpreted as nonzero. A deterministic amendment
selected the earliest technically valid retry, retained later retries only as
operational overhead, and merged no other case. One continuation stopped
before model calls because its output directory was absent, and another
completed judging but invoked audit without the explicit dataset; the final
continuation bound the exact dataset and reused already-valid artifacts. All
three final candidate audits, source/result hashes, and aggregate checksums
pass.

The research iteration can now be closed without another observed-set tuning
round. The candidate has a robust, large improvement over main and a small
descriptive lead over Mem0, while the report preserves the non-significant
Mem0 comparison and the failed uncached-token gate. A future promotion study
should freeze this candidate, reduce uncached cost on independently selected
data, and use a genuinely hidden target-category set or a new benchmark split;
the current LongMemEval-S pool no longer contains unexposed assistant or
preference questions.

---

## 4. Comparison with Python Agent Frameworks

We ran the same LoCoMo benchmark on four Python agent frameworks —
**AutoGen**, **Agno**, **ADK**, **CrewAI** — all using GPT-4o-mini,
the same 10 samples (1,986 QA), and LLM-as-Judge evaluation.

The external frameworks were manually seeded and are unaffected by the
trpc-agent-go replay bug. The trpc-agent-go optimized row is a legacy v3
artifact, so cross-framework conclusions involving that row require a v4
rerun. Session Recall used direct historical-event replay and remains
protocol-valid.

### 4.1 Framework Configurations

| Framework | Memory Backend | Retrieval | Embedding |
| --- | --- | --- | --- |
| **trpc-agent-go** | pgvector | Vector similarity (top-K) + multi-pass | text-embedding-3-small |
| **AutoGen** | ChromaDB | Vector similarity (top-30) | text-embedding-3-small |
| **Agno** | SQLite | LLM fact extraction → system prompt | N/A |
| **ADK** | In-memory | Agent tool call (LoadMemoryTool) | Internal |
| **CrewAI** | Built-in vector | Auto-retrieve by Crew | Internal |

### 4.2 Framework Memory Approaches

Below is a detailed breakdown of each framework's memory storage,
retrieval, and QA call flow. All benchmark implementations share
the same system prompt strategy (five-category QA answering rules)
and evaluation pipeline.

**trpc-agent-go (optimized) — Auto extraction + pgvector hybrid:**

- **Storage**: Conversation turns are processed by an LLM extractor
  into structured facts/episodes (content + metadata + event_time),
  stored in pgvector.
- **Stored message roles**: The extractor's
  `ExtractionContext.Messages` includes **both user and assistant
  messages** (excluding tool calls), so both sides of the conversation
  are available for LLM memory extraction.
- **Retrieval**: The agent issues a `memory_search` tool call that
  triggers pgvector hybrid search (vector similarity + keyword
  matching), returning up to 30 structured memory entries.
- **QA flow**: 3 LLM calls (Step 1 emits tool call for search #1 →
  Step 2 emits tool call for search #2 → Step 3 reads all results
  and answers).
- **Strengths**: Extracted memories are precise, high information
  density; hybrid search covers both semantic and keyword matches.
- **Token profile**: The tool-call pattern re-reads prior context
  at each step, resulting in ~17,182 prompt tokens/QA. However,
  **43.9% of prompt tokens are served from the provider's prompt
  cache** (OpenAI `cached_tokens`), so the effective *new* prompt
  cost is ~9,663 tokens/QA — comparable to single-call approaches
  when measured by billable cost (cached tokens are billed at 50%
  on most providers).
- **Issues**: Structured JSON format adds serialization overhead;
  multi-step latency is higher than single-call patterns.

**AutoGen — Raw turns in ChromaDB + single LLM call:**

- **Storage**: Raw conversation turns stored as
  `[SessionDate: ...] Speaker: text` in ChromaDB; embedding only,
  no LLM extraction.
- **Stored message roles**: No auto-storage — `ChromaDBVectorMemory.
  add()` is a purely manual API; the caller decides what to store.
  In our benchmark, we manually `add()` each turn without role
  distinction.
- **Retrieval**: Before `AssistantAgent.run()`, the
  `ChromaDBVectorMemory.update_context()` method queries ChromaDB
  with the question, retrieves top-30 results (score ≥ 0.3), and
  injects them as a `SystemMessage` into the model context.
- **QA flow**: **1 LLM call** — retrieval results are pre-injected
  before the call; no tool call needed.
- **Strengths**: Fewest calls (1/QA), highest token efficiency
  (1,943 tokens/QA).
- **Issues**: Adversarial F1 only 0.272 (lowest among all
  frameworks), severe adversarial robustness deficiency; relies on
  pure vector search with no keyword/BM25 supplement.

**CrewAI — ShortTermMemory + Crew two-step call:**

- **Storage**: Raw conversation turns stored in CrewAI's built-in
  `ShortTermMemory` (ChromaDB-based vector store); no LLM
  extraction.
- **Stored message roles**: The framework stores **task-level
  execution summaries** (task description + agent role + expected
  output + final result), not individual messages. In our benchmark,
  we bypass this and manually `stm.save()` each turn.
- **Retrieval**: Monkey-patched `ContextualMemory._fetch_stm_context`
  widens the search window to top-30 (default is only top-5);
  results formatted as `- [content]` list injected into agent
  context.
- **QA flow**: 2 LLM calls — Call 1 is Crew's internal
  formatting/planning step, Call 2 answers with memory context.
- **Strengths**: Simple storage (no LLM extraction cost), compact
  retrieval format.
- **Issues**: Insufficient vector retrieval recall; Crew's Call 1
  (planning step) is pure framework overhead contributing ~140
  completion tokens/QA with no F1 benefit; adversarial and temporal
  categories show 44.6% and 39.6% loss rates respectively.

**ADK — InMemoryMemoryService + LoadMemoryTool full load:**

- **Storage**: Conversation turns stored as `Event` objects in ADK's
  `InMemoryMemoryService` (pure in-memory, no persistence).
- **Stored message roles**: `add_session_to_memory()` stores **all**
  events with `content.parts` — **user, model, and tool events are
  all included** without filtering by author.
- **Retrieval**: The agent calls `LoadMemoryTool` which loads
  **all memories indiscriminately into context** — no selective
  retrieval whatsoever.
- **QA flow**: 2 LLM calls (Step 1 calls LoadMemoryTool → Step 2
  reads all memories and answers).
- **Strengths**: No memory loss.
- **Issues**: **Catastrophic token inflation** (49,224 tokens/QA,
  3.0x the optimized version); 9 QA exceeded 128K tokens causing
  context overflow; 10 QA returned empty predictions; single QA
  peak at 252,849 tokens.

**Agno — LLM fact extraction + SQLite full injection:**

- **Storage**: Each conversation turn is processed by
  `MemoryManager` which calls an LLM to extract facts/preferences,
  stored in SQLite (LLM extraction cost excluded from QA token
  counts).
- **Stored message roles**: `make_memories()` processes **only user
  messages** — assistant and tool messages are excluded.
  `create_or_update_memories()` also filters `m.role == 'user'`
  explicitly.
- **Retrieval**: With `add_memories_to_context=True`, **all**
  stored memories are injected into the system prompt under
  `<memories_from_previous_interactions>` — no vector search or
  similarity filtering.
- **QA flow**: 1 LLM call (memories already in system prompt).
- **Strengths**: LLM extraction preserves key facts.
- **Issues**: **Full injection inflates to 10,436 tokens/QA**;
  highest latency (14,127ms/QA, 7h47m total); the underlying
  DB interface's `limit`/`topics` filtering parameters are
  never used by `MemoryManager` — a design gap.

**Approach comparison summary:**

| Dimension | Session Recall | trpc-agent-go (optimized) | AutoGen | CrewAI | ADK | Agno |
| --- | --- | --- | --- | --- | --- | --- |
| Stored message roles | user + assistant raw session events | user + assistant extracted into structured memories | No auto-storage (manual API) | Task-level summary (input + output) | All events (user + model + tool) | User only (assistant excluded) |
| Benchmark turn mapping | Speaker[0]→user, [1]→assistant | Speaker[0]→user, [1]→assistant | Per-turn manual add() | Per-turn manual save() | Per-turn→Event, whole session write | Per-turn→create_user_memories() |
| Storage | Raw session events | LLM-extracted structured memories | Raw turns | Raw turns | Raw turns | LLM-extracted facts |
| Retrieval | Hybrid RRF over session events, preloaded once | Vector+keyword hybrid via tool calls | Vector top-30 | Vector top-30 | **Full load** | **Full injection** |
| LLM calls/QA | 1 (preload) | 3 (tool call) | **1** (pre-inject) | 2 (Crew internal) | 2 (tool call) | 1 (pre-inject) |
| Tokens/QA | 3,694 (3,567 effective†) | 17,182 (9,663 effective‡) | **1,943** | 2,839 | 49,224 | 10,436 |

> † Session Recall cache hit rate is 3.7%, giving an effective new
> token cost of ~3,567/QA.
>
> ‡ 43.9% of optimized prompt tokens are served from the
> provider's prompt cache — the effective *new* token cost is
> ~9,663/QA.
>
> Key insight: **retrieval strategy is the primary differentiator**.
> Full-load approaches (ADK/Agno) waste tokens with poor results;
> selective retrieval (Session Recall / optimized / AutoGen /
> CrewAI) performs significantly better. Within selective retrieval,
> Session Recall now delivers the strongest absolute quality while
> staying in the low-token tier, while the optimized version remains
> the more extraction-heavy, tool-driven alternative.

### 4.3 Overall Results

**Table 7: Memory Scenario — Overall Metrics**

| Framework | F1 | BLEU | LLM Score | Tokens/QA | Calls/QA | Latency | Total Time |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| **trpc-agent-go (Session Recall)** | **0.549** | **0.511** | **0.609** | 3,694† | 1.0 | 6,430ms | 3h33m |
| trpc-agent-go (optimized) | 0.469 | 0.431 | 0.532 | 17,182‡ | 3.0 | 8,585ms | 4h44m |
| AutoGen | 0.457 | 0.414 | 0.540 | 1,943 | 1.0 | 3,816ms | 2h06m |
| CrewAI | 0.427 | 0.385 | 0.479 | 2,839 | 2.0 | 8,081ms | 4h27m |
| ADK | 0.362 | 0.309 | 0.476 | 49,224 | 2.0 | 5,578ms | 3h04m |
| trpc-agent-go (original) | 0.399 | 0.371 | 0.416 | 3,056 | 2.0 | 6,659ms | 3h40m |
| Agno | 0.332 | 0.289 | 0.494 | 10,436 | 1.0 | 14,127ms | 7h47m |

> † Session Recall cache hit rate is 3.7%; effective new token cost
> is ~3,567/QA.
>
> ‡ 43.9% of optimized prompt tokens hit the provider's prompt
> cache; effective new token cost is ~9,663/QA. See Section 4.5 for
> details.

> **LLM Score aggregation note.** All frameworks now use the same
> all-sample denominator (accuracy-style: `sum(llm_score) / total_qa`).
> Python frameworks originally reported precision-style scores
> (~0.93) that excluded non-scored QAs from the denominator; those
> values have been recalculated here for fair cross-framework
> comparison.

```
Memory F1 (10 samples, 1986 QA)

trpc-agent-go (Session Recall) |====================================================| 0.549
trpc-agent-go (optimized)      |============================================        | 0.469
AutoGen                        |=========================================           | 0.457
CrewAI                         |========================================            | 0.427
trpc-agent-go (original)       |=====================================               | 0.399
ADK                            |==================================                  | 0.362
Agno                           |===============================                     | 0.332
                               +----------------------------------------------------+
                               0.0      0.1      0.2      0.3      0.4      0.5
```

### 4.4 Category-Level F1

**Table 8: F1 by Category**

| Category | Count | Session Recall | trpc-agent-go (optimized) | AutoGen | CrewAI | trpc-agent-go (original) | ADK | Agno |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| single-hop | 282 | 0.368 | **0.396** | 0.377 | 0.322 | 0.316 | 0.299 | 0.240 |
| multi-hop | 321 | **0.554** | 0.453 | 0.512 | 0.380 | 0.096 | 0.418 | 0.283 |
| temporal | 96 | 0.174 | **0.247** | 0.176 | 0.140 | 0.088 | 0.120 | 0.076 |
| open-domain | 841 | **0.618** | 0.441 | 0.594 | 0.501 | 0.358 | 0.494 | 0.292 |
| adversarial | 446 | 0.610 | 0.626 | 0.272 | 0.448 | **0.814** | 0.163 | 0.556 |

**Table 9: Weighted Average F1**

| Average | Session Recall | trpc-agent-go (optimized) | AutoGen | CrewAI | trpc-agent-go (original) | ADK | Agno |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 5-category weighted (÷1986) | **0.549** | 0.469 | 0.457 | 0.427 | 0.399 | 0.362 | 0.332 |
| 4-category weighted (÷1540) | **0.531** | 0.423 | 0.511 | 0.420 | 0.279 | 0.420 | 0.267 |

> The optimized version still materially improves on the original
> memory baseline, especially on **single-hop** and **temporal**
> questions, while Session Recall should be read as a supplemental
> retrieval path on top of that internal evolution.
>
> 5-category weighted F1: **Session Recall ranks first at 0.549**,
> leading the optimized version (0.469) by 0.080 and AutoGen (0.457)
> by 0.092. 4-category weighted F1 also ranks **#1 at 0.531**,
> beating AutoGen's 0.511 by 0.020 while clearly leading all other
> trpc-agent-go variants and dedicated memory systems.

### 4.5 Token Efficiency and Latency

**Table 10: Token Efficiency Comparison**

| Framework | F1 | Total Tokens | Tokens/QA | Cache Hit | Effective Tokens/QA† | F1/Billion Tokens |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| AutoGen | 0.457 | 3,859,412 | 1,943 | n/a | 1,943 | 118.4 |
| trpc-agent-go (Session Recall) | **0.549** | 7,353,057 | 3,694 | 3.7% | 3,567 | 74.6 |
| CrewAI | 0.427 | 5,639,085 | 2,839 | n/a | 2,839 | 75.7 |
| trpc-agent-go (original) | 0.399 | 6,068,802 | 3,056 | n/a | 3,056 | 65.7 |
| trpc-agent-go (optimized) | 0.469 | 34,123,774 | 17,182 | **43.9%** | **9,663** | 13.7 |
| Agno | 0.332 | 20,725,728 | 10,436 | n/a | 10,436 | 16.0 |
| ADK | 0.362 | 97,759,453 | 49,224 | n/a | 49,224 | 3.7 |

> † **Effective Tokens/QA** = prompt tokens minus cached prompt
> tokens, plus completion tokens. Cached tokens hit the provider's
> automatic prompt cache (e.g. OpenAI `cached_tokens`) and are
> typically billed at **50% of the standard prompt rate**. The
> Python frameworks do not report `cached_tokens` in their SDKs,
> so their effective cost may also be lower than shown; the `n/a`
> entries indicate data not available rather than zero caching.
>
> By raw token count, AutoGen still achieves the best efficiency
> (118.4 F1/billion tokens). The optimized version remains a
> meaningful improvement over the original memory baseline despite
> its higher nominal token cost. **Session Recall is the strongest
> accuracy/efficiency compromise inside trpc-agent-go**: it reaches
> 0.549 F1 with 3,694 tokens/QA, far below Long-Context and the
> optimized version while substantially outperforming them in
> accuracy. The optimized version remains far more expensive in
> nominal tokens because of the multi-step tool-call pattern where
> each step re-reads prior context; prompt caching mitigates that
> cost, but Session Recall is still much leaner in the current
> setup. ADK remains the least efficient — 49,224 tokens/QA for only
> 0.362 F1.

```
Total Evaluation Time (memory scenario, 1986 QA)

AutoGen            |====                                   | 2h06m
ADK                |======                                 | 3h04m
Session Recall     |=======                                | 3h33m
trpc (original)    |========                               | 3h40m
CrewAI             |==========                             | 4h27m
trpc (optimized)   |==========                             | 4h44m
Agno               |===============================        | 7h47m
                   +----------------------------------------+
                   0h       2h       4h       6h       8h
```

**Why the optimized version is slower (4h44m vs 3h40m):**

The optimized version consumes 5.6x more tokens/QA (17,182 vs 3,056)
and takes 1.29x longer per QA (8,585ms vs 6,659ms). The root cause
is the three-step agentic workflow:

1. **Step 1 — Tool call #1** (~1,650 prompt tokens): The LLM reads
   the system instruction + question, then emits the first
   `memory_search` tool call. This incurs one LLM round-trip plus a
   pgvector hybrid search (vector + keyword) with embedding generation.

2. **Step 2 — Tool call #2** (~5,900 prompt tokens): The LLM
   re-reads all prior context (system prompt + question + first tool
   call + first tool results), then emits a second `memory_search`
   tool call to refine the search.

3. **Step 3 — Final answer** (~10,000 prompt tokens): The LLM
   re-reads the entire conversation (all prior context + second tool
   call + second tool results) and generates the final answer.

The key overhead is **cumulative context re-reading**: each step
re-processes everything from all prior steps. Step 3 alone accounts
for ~10,000 prompt tokens. In contrast, the original version uses a
2-call agentic pattern with far fewer/shorter memory entries (~3,056
tokens total for both steps), because its memories are stored as
raw conversation turns rather than extracted structured
facts/episodes.

**Prompt cache mitigates the cost:** Despite re-reading prior
context at each step, the multi-turn pattern is highly
cache-friendly — Steps 2 and 3 share a long common prefix with
their predecessors. In practice, **43.9% of all prompt tokens
(14.93M out of 34.01M) are served from the provider's automatic
prompt cache**, reducing the effective new prompt volume to
~19.08M tokens. At the standard 50% cache pricing, the actual
billable prompt cost is equivalent to ~26.54M tokens rather than
34.01M — a **~22% reduction** from the nominal figure.

Despite the higher token cost, the optimized version achieves a
significantly better F1/cost trade-off: **+17.5% F1** (0.399→0.469)
for **5.6x nominal token cost** (significantly less after cache
discounts), making it worthwhile for production use where answer
quality matters more than token budget.

### 4.6 ADK Failure Analysis

ADK (Google Agent Development Kit) uses an in-memory backend with
agent tool calls (`LoadMemoryTool`) for memory retrieval. In this
evaluation, ADK encountered context overflow issues on some samples:

**Table 11: ADK Context Overflow Details**

| Sample | #QA | Empty Predictions | QA with >128K Tokens | Max Tokens |
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
| **Total** | **1,986** | **10** | **9** | **252,849** |

- **10 QA (0.5%) returned empty predictions**, concentrated in
  samples with longer conversation histories
- **53 QA exceeded 100K tokens**, with the single highest reaching
  **252,849 tokens** — approaching GPT-4o-mini's 128K context
  window limit
- ADK's `LoadMemoryTool` loads **all memories** into context
  without selective retrieval, causing severe token waste on
  longer conversations
- Average 49,224 tokens/QA (highest among all frameworks) for
  only 0.362 F1

### 4.7 Per-Sample F1

**Table 12: Per-Sample F1 Comparison**

| Sample | #QA | Session Recall | trpc-agent-go (optimized) | AutoGen | CrewAI | trpc-agent-go (original) | ADK | Agno |
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
| **Average** | **199** | **0.549** | 0.469 | 0.457 | 0.427 | 0.399 | 0.362 | 0.332 |

> Session Recall beats AutoGen on all 10 samples.

---

## 5. Comparison with External Memory Systems

Source: Mem0 Table 1 (Chhikara et al., 2025, arXiv:2504.19413).
All systems use GPT-4o-mini. Adversarial category excluded for
cross-system comparability (Mem0 paper does not include it).

This section uses published LoCoMo numbers and is separate from the direct,
same-run self-hosted mem0 comparison in Section 3.4. The two evaluations use
different datasets and model protocols, so their absolute scores must not be
mixed.

> **About "LoCoMo (paper baseline)" in the table.** LoCoMo is
> both the dataset used in this report and a memory system
> proposed in the LoCoMo paper (Maharana et al., 2024). That
> system extracts events and summaries from conversations via
> LLM and retrieves them at query time using BM25 + semantic
> search. The Mem0 paper reproduced this approach on the same
> dataset and reported the F1 scores shown here. The table entry
> "LoCoMo (paper baseline)" thus refers to the memory system's
> performance, not the dataset itself.

**Table 13: F1 by Category (Excluding Adversarial)**

| Method | Single-Hop | Multi-Hop | Open-Domain | Temporal | 4-cat Weighted | Source |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| AutoGen | 0.377 | 0.512 | 0.594 | 0.176 | 0.511 | This work |
| **trpc-agent-go (Session Recall)** | 0.368 | **0.554** | **0.618** | 0.174 | **0.531** | This work |
| trpc-agent (optimized) | **0.396** | 0.453 | 0.441 | 0.247 | 0.423 | This work |
| Mem0g | 0.381 | 0.243 | 0.493 | **0.516** | 0.422 | Mem0 paper |
| Mem0 | 0.387 | 0.286 | 0.477 | 0.489 | 0.421 | Mem0 paper |
| CrewAI | 0.322 | 0.380 | 0.501 | 0.140 | 0.420 | This work |
| trpc-agent (LC) | 0.320 | 0.308 | 0.518 | 0.088 | 0.411 | This work |
| ADK | 0.299 | 0.418 | 0.494 | 0.120 | 0.420 | This work |
| Zep | 0.357 | 0.194 | 0.496 | 0.420 | 0.403 | Mem0 paper |
| LangMem | 0.355 | 0.260 | 0.409 | 0.308 | 0.362 | Mem0 paper |
| A-Mem | 0.270 | 0.121 | 0.447 | 0.459 | 0.347 | Mem0 paper |
| OpenAI Memory | 0.343 | 0.201 | 0.393 | 0.140 | 0.328 | Mem0 paper |
| MemGPT | 0.267 | 0.092 | 0.410 | 0.255 | 0.308 | Mem0 paper |
| LoCoMo (paper baseline) | 0.250 | 0.120 | 0.404 | 0.184 | 0.303 | Mem0 paper |
| trpc-agent (original) | 0.316 | 0.096 | 0.358 | 0.088 | 0.279 | This work |
| Agno | 0.240 | 0.283 | 0.292 | 0.076 | 0.267 | This work |
| ReadAgent | 0.092 | 0.053 | 0.097 | 0.126 | 0.089 | Mem0 paper |
| MemoryBank | 0.050 | 0.056 | 0.066 | 0.097 | 0.063 | Mem0 paper |

```
4-Category Weighted F1 (excluding adversarial, 1540 QA)

Session Recall      |============================================| 0.531
AutoGen             |==========================================  | 0.511
trpc-agent (optimized) |==================================       | 0.423
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
trpc-agent (original) |======================                  | 0.279
Agno                |====================                      | 0.267
                    +------------------------------------------+
                    0.0      0.1      0.2      0.3      0.4   0.5
```

> **5-category weighted F1** (for frameworks with adversarial data):
>
> | Method | 5-cat Weighted F1 |
> | --- | ---: |
> | **trpc-agent-go (Session Recall)** | **0.549** |
> | trpc-agent (optimized) | 0.469 |
> | AutoGen | 0.457 |
> | CrewAI | 0.427 |
> | trpc-agent (original) | 0.399 |
> | ADK | 0.362 |
> | Agno | 0.332 |

**Key takeaways:**

1. **trpc-agent-go (Session Recall)** reaches a 4-category weighted F1 of
   **0.531**, ranking **#1 overall** and surpassing AutoGen
   (0.511) by 0.020. It clearly surpasses Mem0g (0.422), Mem0
   (0.421), Zep (0.403), LangMem (0.362), A-Mem (0.347), and other
   dedicated memory systems.
2. **Open-domain and multi-hop are now standout strengths.** Session
   Recall ranks **#1 in multi-hop** (0.554) and **#1 in open-domain**
   (0.618), ahead of AutoGen on both categories.
3. **The optimized version remains a complementary strategy.** It is still the
   strongest trpc-agent-go variant on **temporal** (0.247) and offers
   better adversarial robustness (0.626), but its overall 4-category
   weighted F1 (0.423) is well below Session Recall.
4. **Token efficiency improved dramatically.** Session Recall cuts
   nominal Tokens/QA from 17,182 (optimized) and 18,776
   (Long-Context) down to **3,694**, while also improving F1.
5. Compared with the original baseline, the optimized version first
   moved trpc-agent-go from 0.279 to 0.423 in 4-category weighted F1,
   and Session Recall then pushed that further to 0.531.

---

## 6. Conclusion

### Key Findings

1. **trpc-agent-go Session Recall is now the strongest overall
   configuration.** It ranks **#1 in 5-category weighted F1** at
   **0.549** and **#1 in 4-category weighted F1** at **0.531**,
   beating AutoGen on both metrics. Compared with Long-Context and
   the optimized version, it improves overall F1 while using far
   fewer tokens.

2. **The historical LoCoMo run suggests retrieval trade-offs that require
   v4 confirmation.** Session Recall is best on **open-domain** and
   **multi-hop** in the saved artifacts and remains protocol-valid. The
   optimized Auto version's apparent **temporal** and adversarial strengths
   came from legacy replay-v3 and are not current evidence. Long-Context
   remains an unaffected reference for short single-session histories.

3. **The opt-in pgvector candidate strongly improves upstream main and
   descriptively leads self-hosted Mem0 on the 48-case full-haystack
   regression.** It reaches 43/48 majority and 127/144 correct answer
   replicates, versus main's 24/48 and 73/144 and Mem0's 41/48 and 124/144.
   The main comparison is significant (exact McNemar p=0.0000038147); the
   Mem0 comparison is not (p=0.6875). Assistant-result extraction repairs
   assistant-history recall, while query-aware provenance RRF prevents those
   results from displacing user-grounded evidence on ordinary queries. A
   preregistered unseen 8-case gate also passes, but it covers only non-target
   categories and must not be presented as unseen target benefit.

4. **trpc-agent-go now surpasses dedicated memory systems by a wide
   margin.** Session Recall's 4-category weighted F1 of 0.531 is well
   above Mem0g (0.422), Mem0 (0.421), Zep (0.403), LangMem (0.362),
   A-Mem (0.347), OpenAI Memory (0.328), MemGPT (0.308), and other
   purpose-built memory systems.

5. **Limitations of other Python frameworks.**

   - **ADK**: Highest token consumption (49,224 tokens/QA) — **2.9x**
     that of the optimized version — yet only achieves 0.362 F1. Its
     `LoadMemoryTool` loads all memories indiscriminately into
     context, causing severe token waste and context overflow (9 QA
     exceeded 128K tokens) in longer conversations, lacking any
     selective retrieval capability
   - **Agno**: Lowest F1 (0.332), highest latency (14,127ms/QA,
     7h47m total), with token consumption of 10,436/QA. Like ADK,
     Agno employs a full-loading architecture — injecting all user
     memories into the system prompt under a
     `<memories_from_previous_interactions>` tag with no vector
     search or similarity retrieval. Although the underlying DB
     interface exposes `limit`, `topics`, and other filtering
     parameters, the `MemoryManager` never utilizes them at runtime
   - **CrewAI**: Memory loss in its short-term memory
     backend — particularly severe in adversarial (44.6%) and
     temporal (39.6%) categories
   - **AutoGen**: While achieving 0.511 in 4-category weighted F1,
     this is largely driven by a single outstanding category
     (open-domain at 0.594); its adversarial score of 0.272 is the
     lowest among all frameworks, revealing a critical adversarial
     robustness deficiency

6. **Memory is essential for production agents.** Long-Context is
   effective for short single-session scenarios, but cannot persist
   knowledge across sessions or scale beyond the model's context
   window. Session Recall delivers a stronger quality/cost balance,
   while the optimized version provides a second memory strategy built on
   extracted persistent memories.

7. **The current iteration should be frozen, not tuned to the observed gate.**
   The full-haystack48 outcome and integrity gates pass, but the overall gate
   fails because uncached memory tokens are 1.5699x main against a 1.55x
   limit. The overage is only 1.28% of the allowed cap, yet tuning these known
   IDs to cross the threshold would weaken the evidence. The next promotion
   step is cost reduction on independently selected data followed by a hidden
   target-category evaluation. The current LongMemEval-S pool cannot supply
   that holdout because every assistant and preference question has already
   been exposed.

### Production Recommendations

| Use Case | Recommended Approach |
| --- | --- |
| Short single-session (< 50K tokens) | Long-context (no memory needed) |
| Cross-session QA / best accuracy | Session Recall |
| Long-running agents with durable extracted memory | Optimized pgvector auto memory |
| History exceeding context window | Session Recall or optimized |
| Memory regression development | Fixed observed Oracle set with saved stage-level traces |
| Non-target safety gate | Preregistered unexposed full-haystack sample with frozen cost bounds |
| Candidate promotion | Frozen candidate, independent cost validation, hidden target-category evaluation, and LoCoMo regression gate |

---

## Appendix

### A. Experimental Environment

| Component | Version/Config |
| --- | --- |
| Framework | trpc-agent-go |
| Models | GPT-4o-mini (LoCoMo); glm52 (LongMemEval) |
| Embedding | text-embedding-3-small |
| PostgreSQL | 15+ with pgvector extension |
| Datasets | LoCoMo-10 (10 samples, 1,986 QA); LongMemEval Oracle observed16; LongMemEval-S unseen non-target8 and observed full-haystack48 |
| Comparison backend | Pinned self-hosted Mem0 OSS runtime with pgvector (LongMemEval) |

### B. Full Category Breakdown (F1 / BLEU / LLM)

| Scenario | single-hop | multi-hop | temporal | open-domain | adversarial |
| --- | --- | --- | --- | --- | --- |
| Long-Context | 0.320/0.251/0.320 | 0.308/0.273/0.260 | 0.088/0.068/0.165 | 0.518/0.457/0.662 | 0.667/0.667/0.668 |
| Session Recall | 0.368/0.304/0.445 | 0.554/0.512/0.563 | 0.174/0.138/0.311 | 0.618/0.570/0.715 | 0.610/0.610/0.608 |
| Optimized | 0.396/0.325/0.395 | 0.453/0.415/0.519 | 0.247/0.192/0.364 | 0.441/0.398/0.552 | 0.626/0.626/0.626 |
| Original | 0.316/0.250/0.270 | 0.096/0.088/0.060 | 0.088/0.068/0.115 | 0.358/0.319/0.425 | 0.814/0.814/0.814 |

### C. Token Usage — Full Breakdown

| Scenario | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | Calls/QA |
| --- | ---: | ---: | ---: | ---: | ---: |
| Long-Context | 37,272,167 | 16,104 | 37,288,271 | 1,986 | 1.0 |
| Session Recall | 7,336,165 | 16,892 | 7,353,057 | 1,986 | 1.0 |
| Optimized | 34,007,814 | 115,960 | 34,123,774 | 5,981 | 3.0 |
| Original | 6,011,025 | 57,777 | 6,068,802 | 3,999 | 2.0 |
| AutoGen | 3,842,576 | 16,836 | 3,859,412 | 1,986 | 1.0 |
| CrewAI | 5,360,840 | 278,245 | 5,639,085 | 3,972 | 2.0 |
| Agno | 20,694,534 | 31,194 | 20,725,728 | 1,986 | 1.0 |
| ADK | 97,691,620 | 67,833 | 97,759,453 | 4,028 | 2.0 |

### D. LongMemEval Reproduction and Provenance

The exact question IDs are frozen in the run manifest rather than resampled.
The primary arm shape is:

```bash
./run-longmemeval.sh \
  -dataset-format longmemeval \
  -dataset ../../summary/data/longmemeval-cleaned/longmemeval_oracle.json \
  -lme-question-ids "$FROZEN_QUESTION_IDS" \
  -memory-backend pgvector,mem0 \
  -pgvector-update-policy merge_similar \
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

Candidate pgvector reuses the exact IDs and protocol with
`-pgvector-update-policy merge_similar` and
`-pgvector-assistant-result-extraction=true`. Archived artifacts record the
same default policy under the earlier `reconcile` label; they remain immutable.
Two saved-retrieval re-answer runs use fresh, distinct answer ledgers; all
result sets use three judge votes
per answer. One incomplete development replicate was replaced across all arms
and all cases, as frozen before replacement calls. The final development
candidate refreshes only retrieval from the exact accepted memory snapshot,
then performs three fresh answer/judge repetitions.

The later full-haystack experiments use the cleaned LongMemEval-S dataset. The
unseen non-target8 plan was registered before provider calls, froze seed
20260812 and 373 excluded IDs, prohibited adaptive tuning, and required six
integrity audits. The observed48 comparison reuses the exact previous baseline
selection and freezes three answer repetitions, three judge votes per answer,
the three-arm quality rules, and all cost thresholds. Each phase keeps isolated
stores, explicit dataset and selection digests, content-addressed cache lineage,
fresh answer/judge ledgers, and a final checksum manifest.

| Formal phase | Dataset/selection SHA-256 | Aggregate SHA-256 | Final status |
| --- | --- | --- | --- |
| Unseen non-target8 | dataset `d6f21ea9d60a0d56f34a05b609c79c88a451d2ae03597821ea3d5a9678c3a442`; selection `51bfb910ff00239cdd3709c94148d42be8ae64d8a189c2db34b27795c93f5738` | `04a69a5d31ef851f9489f55858bbf124cdcfd3e0e330c31b2eaeeab9b161842e` | Integrity, outcome, and cost PASS |
| Observed full-haystack48 recovery | manifest `48b5d98ebc8ae1a16f8a203e70c5d926cbc0828d49ed43f59a89abb3766daa0e` | `10f379ba140a09285f912ef92c3420be93553b955780a40329149dacf4123f09` | Integrity PASS; outcome PASS; cost FAIL |

The full-haystack48 recovery replaces only the timed-out candidate unit with
the earliest valid isolated retry. Its three final candidate audit digests are
`46f92b60fbdbfdd433dd2f08b663bc8a92cbbad72071c73a3b0308bcb09477ad`,
`eccb711f167931f0c31a36de4b63174c58f13e9b3e045daa0c79fe64e069dffc`,
and `5622ed2b8f047ccccb7d0a1fc24fa2cf97c626d2c040748e52ced4367d79a274`.
The exhaustive table below preserves the earlier Oracle16 development and
mechanism-ablation lineage.

| Provenance item | Digest or revision |
| --- | --- |
| Post-evaluation integrated trpc-agent-go head (not an evaluated artifact) | `ed61e8956932fa17740aa473d9a49017bc435d42` |
| Current benchmark module replacement | `v0.0.0-20260817035228-ed61e8956932` |
| Formal three-arm benchmark | `f7cf9370057daa382db925ca67500b9f66f173da` |
| Compact-ablation benchmark | `8eb0bac316ee67938ab6ecb6052ff227f94363e0` |
| pgvector main | `0c7774187da9330144df2a038ef18ee89ef2ae1c` |
| pgvector parent candidate | `0797067f40743fbe789eff65315d74b05b7c454c` |
| pgvector three-arm candidate | `bd6b31f92a904023df0c77c6762fa95b5e359456` (evaluated tree: `eaf5f49f1fa47856ff919798bcc93a41be71f6ec`) |
| pgvector prompt-compaction candidate | `969fb16a918d6abae8bb06d52cb784490c8a2eb4` |
| pgvector final reconcile candidate | `2432019572845c182d37a2872f056a6e7bee33c7` |
| Full reconcile-configuration benchmark | `536b0979345e607bc06e6975040c7f51336a6abe` |
| Simplification-run benchmark | `126a585b6a68530d5ec17d9c69eee33317adbf12` |
| Dataset SHA-256 | `821a2034d219ab45846873dd14c14f12cfe7776e73527a483f9dac095d38620c` |
| Selection SHA-256 | `b10651ad0caa76696a2d885da060969d0d24d2e1cdba4130308ef745f95621fb` |
| Protocol SHA-256 | `9b001708920522d7ad2cd477824208b5692eb52bcd1c205e46fb9fbb5b57b9a4` |
| Replicate manifest SHA-256 | `7baecdce61be140d5cbe3163519b8ee5503eaafdca842f37c087417e297871d2` |
| Aggregate SHA-256 | `fb5e37a2327d00802055e388c2125f564c402ccbb1261fbfffc486f8f7819974` |
| Audit SHA-256 | `17ae45dc27ffc3d89f8f1c244ac420ba73c1b9aa741fbe52c7a45cb71e2e158b` |
| Compact-ablation audit SHA-256 | `d48ae6d6731c45ae05bc52c753df331cf204a38150896b748d7d1ac0db071981` |
| Assistant-prompt formal gate SHA-256 | `f82f35299d319e64a30a32a24b022aceef7e90a308f687275dffd679c9d8f335` |
| Assistant-prompt quality diagnostic SHA-256 | `902629cfdf1a924282c58300e93afacdda7a9c3c044afdc342762de5384755fa` |
| Fresh LoCoMo prompt-pair audit SHA-256 | `64963ebd8b481873012631a85adb21fc1aa9d87b6491accb751a7b8d945a5d2c` |
| Repeated fixed-memory LoCoMo audit SHA-256 | `4bb1d7606099029d2cbb8ed00600ef2a38570b0bbac89ccffe2bc540d9632fbf` |
| Engineering synthesis audit SHA-256 | `7298a75fc436e90d84d6adcbf06cee779120fd7450ac8d10297b51b50d3423a5` |
| Final reconcile dev16 audit SHA-256 | `92585f5c1dd67983ed241c9ae55c885183409163d276e58b33d267b6e5ab952c` |
| Final reconcile dev16 checksum-manifest SHA-256 | `aadc7233009a05ea6d49bfca406b049d1b5a6e2b8556960df7fa6273f45558cb` |
| LoCoMo update-policy audit SHA-256 | `15e16594cfe59cb30883c4d91911b81384d501e0389591fb0cf4806cc2cfbdd8` |
| LoCoMo update-policy checksum-manifest SHA-256 | `292ff0a81b805978e7822ff5ee2b6a0bb5b22c10fdf52ee7c8976314d6017a61` |
| Final implementation-smoke audit SHA-256 | `0415aa9bdb973f178aadd4d83cc8db0c3caaa157d395663627a56cca2d8765aa` |
| Final implementation-smoke checksum-manifest SHA-256 | `7a3f0b0d5e31b2dfb4880769f98f10aea62673e6f0862e1882614040b5ce6a92` |
| Accepted replacement-manifest comparison SHA-256 | `38bd9117ef320d1d76d7ee833030e51ab2db37a589165ff1ff03b3dcffec707b` |
| Accepted replacement audit SHA-256 | `6c64bb71f90fd5a56c2e8f3b004f9214b665e5b361d37a7846091331f3f0974a` |
| Provenance-ranking candidate | `22455426803a478535fae28a6c8c103b4f8668c7` |
| Provenance-ranking robust audit SHA-256 | `18d842890676130d3f332cbaeb37c2df48f5f017d56db0ff62f87497e8a78861` |
| Tightened-classifier equivalence audit SHA-256 | `bf61ac7a005981354ac201fa2262bea0e41063096d1a252af75af596f40ac3b2` |
| LoCoMo provenance replay audit SHA-256 | `66c748d28c860832a5e28bfef2bf00201973a9aaff058153df219f40b565e898` |
| Mem0 | source `b05cce58`, runtime `9d027353`, image `81d80e337521` |

Audits verify exact builds, complete provider usage, isolated stores, per-case
pair equality, cache lineage, and zero final backend/answer/judge errors.
Recovery attempts that are not selected remain recorded as operational
overhead and do not contribute to canonical quality or cost. Raw model traces
and stores remain evaluation artifacts; the report contains only aggregates,
digests, and stage-level diagnostics.

---

## References

1. Maharana, A., Lee, D., Tulyakov, S., Bansal, M., Barbieri, F., and Fang, Y. "Evaluating Very Long-Term Conversational Memory of LLM Agents." arXiv:2402.17753, 2024.
2. Wu, D., Wang, H., Yu, W., Zhang, Y., Chang, K.-W., and Yu, D. "LongMemEval: Benchmarking Chat Assistants on Long-Term Interactive Memory." arXiv:2410.10813, 2024.
3. Chhikara, P., Khant, D., Aryan, S., Singh, T., and Yadav, D. "Mem0: Building Production-Ready AI Agents with Scalable Long-Term Memory." arXiv:2504.19413, 2025.
4. Hu, C., et al. "Memory in the Age of AI Agents." arXiv:2512.13564, 2025.
