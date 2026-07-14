# LongMemEval Memory Evaluation

## Scope

This report compares the self-hosted mem0 backend with the trpc-agent-go
pgvector memory service on production-style LongMemEval replay. It is a focused
32-question evaluation, not a claim about the full 500-question dataset.

Each 16-question subset contains two non-abstention questions from every
LongMemEval type plus four abstention questions:

- knowledge-update
- multi-session
- single-session-assistant
- single-session-preference
- single-session-user
- temporal-reasoning

Sessions are replayed chronologically. Memory extraction runs after every
user/assistant pair, each question uses an isolated user and run scope, and the
answer model sees only retrieved memories. Both backends use the same answer
protocol, `glm52` answer model, `text-embedding-3-small` embeddings, and top-k
50 retrieval. Self-hosted mem0 ran with LLM temperature 0.

## Results

| Subset | Role | Backend | Judge | EM | Avg F1 |
| --- | --- | --- | ---: | ---: | ---: |
| seed48 | development | mem0 | 11/16 | 3/16 | 0.300 |
| seed48 | development | pgvector before fallback | 11/16 | 2/16 | 0.192 |
| seed48 | development | pgvector final, raw judge | 12/16 | 3/16 | 0.282 |
| seed48 | development | pgvector final, drift-normalized | **13/16** | 3/16 | 0.282 |
| seed137 | blind holdout | mem0 | 14/16 | 5/16 | 0.453 |
| seed137 | blind holdout | pgvector final | **15/16** | **7/16** | **0.553** |

The seed48 comparison ignores one conflicting semantic-judge verdict because
the baseline and candidate answers, questions, and references were identical.
After this normalization, the final pgvector run has two improvements and no
behavioral regressions. Its raw judge score remains 12/16.

Across both subsets, raw judge correctness is 27/32 for pgvector and 25/32 for
mem0. Applying the identical-answer judge-drift rule gives 28/32 for pgvector.
The independent seed137 holdout shares no question IDs with the 50 formal
development, holdout, and targeted-validation IDs used while developing the
memory changes. Pgvector therefore retains a one-question strict advantage on
previously unseen questions.

The sample is intentionally small. The result supports continued evaluation
and a larger confirmatory run, but does not establish statistical significance
over the complete LongMemEval dataset.

## Holdout Cost

Seed137 replayed 144 user/assistant pairs per backend. Judge usage is reported
separately and is not included in either backend row.

| Backend | LLM Calls | LLM Tokens | Cached Tokens | Embedding Calls | Embedding Tokens |
| --- | ---: | ---: | ---: | ---: | ---: |
| mem0 | 160 | 1,446,726 | 1,107,264 | 297 | 112,771 |
| pgvector | 161 | 1,322,812 | 688,384 | 873 | 54,882 |
| semantic judge | 32 | 8,324 | 832 | 0 | 0 |

Pgvector used 8.6% fewer total LLM tokens and 51.3% fewer embedding tokens than
mem0 on seed137. Cached tokens are included in total tokens; cache accounting
must remain separate because provider billing policies can differ.

## Validated Changes

The evaluation led to the following memory-service changes:

1. Preserve historical state transitions instead of collapsing an old and new
   state into an undirected preference.
2. Reconcile near-duplicate additions while retaining distinct historical
   facts and recommendation lists.
3. Extract assistant-authored memories and preserve long structured assistant
   output when model extraction returns no operation.
4. Propagate Add, Update, Delete, and Clear persistence failures from the auto
   memory worker. Async failures are exposed in session state and no longer
   advance the extraction completion marker.
5. Remove a benchmark-like extraction example and replace it with a synthetic
   example to prevent prompt leakage.

The benchmark also records per-pair extraction operations, memory diffs,
retrieval hits, provider-reported LLM and embedding usage, cached tokens,
sanitized mem0 runtime configuration, and exact build provenance. Comparison
ignores judge drift for identical normalized inputs and outputs.

## Bad Cases

The structured-output fallback fixed seed48 question `e3fc4d6e`: pgvector had
previously extracted no memory from an assistant-authored entity list and
answered `I don't know`; the final run extracted and retrieved the evidence and
answered correctly.

Seed137 leaves one pgvector-only failure, `8a2466db`. The source assistant
provided four categories of Adobe Premiere Pro learning resources. Mem0 stored
the resource list, while pgvector stored only the user's interest in advanced
Premiere Pro settings. Retrieval contained the preference but not the resource
list, so pgvector correctly refused to invent an answer. This is a partial
assistant-output extraction miss, not a retrieval-ranking failure. It is the
next general optimization target, but changing the extractor for this case
requires a new unseen holdout before claiming another gain.

The two mem0-only failures on seed137 also had full retrieval and occurred at
answer generation. They were not attributed to extraction or search.

## Reproduction

Use the LongMemEval runner documented in the parent [README](../README.md).
The evaluated subset shape is:

```bash
go run . \
  -dataset-format longmemeval \
  -dataset ../../summary/data/longmemeval-cleaned/longmemeval_oracle.json \
  -memory-backend pgvector,mem0 \
  -lme-per-type 2 \
  -lme-abstention-count 4 \
  -lme-sample-seed 137 \
  -mem0-llm-temperature 0 \
  -vector-topk 50 \
  -debug-dump-memories \
  -debug-qa-limit 16 \
  -output ../results/lme-seed137
```

The seed137 replay used benchmark revision `fc0469392299248e281cb9dfc48bd023b54f369f`
and trpc-agent-go revision `ce3dd3b76ca0`. Generated result directories remain
ignored because they contain large traces and model outputs.
