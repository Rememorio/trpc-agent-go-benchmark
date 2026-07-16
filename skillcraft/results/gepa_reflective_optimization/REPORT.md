# Reflective Skill Optimization on SkillCraft

## 1. Introduction

This report evaluates the pure-Go reflective optimizer in
`trpc-agent-go/evolution/optimization`. It asks two separate questions:

1. can offline reflection discover a better skill and reject unsafe candidates
   on a frozen holdout; and
2. does an accepted skill remain useful after it is placed back into the full
   asynchronous evolution loop?

The two answers are deliberately different:

- **The optimizer is useful as a gated offline search and repair primitive.** It
  repaired a reviewer-produced recipe skill, found a World Bank efficiency
  candidate, and rejected another recipe candidate that looked cheaper on
  validation but failed an untouched holdout.
- **The current optimized overlay is not eligible for promotion on the tested
  GPT-5.2 runtime.** In a preregistered 5-family, 3-seed, 3-arm replay,
  optimized evolution preserved pass rate and stayed within the quality
  tolerance, but quality changed by `-0.08pp` and end-to-end tokens increased
  by `5.79%` relative to evolution. The required meaningful benefit was
  therefore absent. This is not yet a same-model GLM-5.2 runtime verdict.

**Table 1: Full operational replay (3 runs, n = 90 per arm)**

| Metric | Baseline | Evolution | Optimized evolution |
| --- | ---: | ---: | ---: |
| Pass rate | 97.78% | **100.00%** | **100.00%** |
| Official quality | 96.06% | **98.24%** | 98.16% |
| Agent tokens / task | **311,870** | 325,887 | 344,727 |
| Reviewer tokens / task | 0 | 15,168 | 16,089 |
| End-to-end tokens / task | **311,870** | 341,055 | 360,816 |

Evolution rescued two baseline failures, but cost `9.36%` more end to end.
Adding the offline overlay did not rescue any additional failure and cost a
further `5.79%`. This supports reviewing the framework API while withholding
the tested overlay from promotion on that runtime.

## 2. Experimental Design

### 2.1 Three Evidence Stages

The evaluation separates discovery, confirmation, and operational use:

1. **Search:** start from five real or structurally equivalent evolution
   revisions; mutate one skill component at a time using paired feedback cases;
   select survivors on a disjoint validation split.
2. **Frozen confirmation:** fix the candidate, disable reflection, and compare
   it with the seed on validation plus holdout cases under independent seeds.
3. **Operational replay:** compare `baseline`, `evolution`, and
   `optimized_evolution` over the same five families and six scales used by the
   existing evolution benchmark.

The search and frozen-confirmation stages used the self-deployed GLM-5.2 route
requested as `glm52`; this is recorded in the frozen evidence. The operational
matrix instead requested `gpt-5.2` from the same internal OpenAI-compatible
endpoint. A post-run routing probe returned `gpt-5.2-2025-12-11` for that ID and
`glm52` for `glm52`, so the two IDs are distinct routes rather than aliases.

All three operational arms therefore used the same GPT-5.2 route, temperature
zero, 80 tool iterations, and task-specific paired sampling seeds. The launch
configuration set the maximum response to 8,192 tokens; the historical result
schema did not persist that flag, and the runner now records it for future
runs. Odd and even root seeds reversed whole-arm order. Provider-side sampling
seed support remains best effort, so repeated root seeds are still necessary.

This preserves the internal validity of the three-arm operational comparison,
but the complete pipeline is a cross-model transfer test: GLM-5.2 produced and
confirmed the candidates, while GPT-5.2 consumed them. It does not answer
whether those candidates improve an online GLM-5.2 evolution loop. The routing
probe is retained in
[`model_routing_evidence.json`](model_routing_evidence.json).

The five families were `cat-facts-collector`, `openmeteo-weather`,
`pokeapi-pokedex`, `recipe-cookbook-builder`, and
`world-bank-economic-snapshot`; every family included `e1`, `e2`, `e3`, `m1`,
`m2`, and `h1`.

### 2.2 Preregistered Operational Gate

The checked-in `skillcraft-5-family-3-arm-v1` protocol was fixed before the
final matrix was observed. Promotion required all of the following:

- at least three complete runs, with all 30 tasks present in every arm;
- no overall or per-family pass-rate regression;
- overall quality no worse than `0.25pp` below evolution;
- per-family quality no worse than `1.00pp` below evolution; and
- a meaningful benefit: quality at least `+0.50pp` or end-to-end tokens at
  least `-5%`.

The aggregate command also rejects duplicate root seeds, missing official
evaluations, unexpected tasks, and task seeds that are not paired across arms.
Its output contains sanitized per-run summaries without local paths or model
transcripts.

## 3. Search and Frozen Confirmation

### 3.1 Five-Family Search

The optimizer did not force every family to produce a different skill.

**Table 2: Search disposition**

| Family | Search result | Next action |
| --- | --- | --- |
| Cat facts | Seed retained | Abstain |
| Pokémon | Seed retained | Abstain |
| Weather | A mutation survived feedback, but validation retained the seed | Abstain |
| Recipe | Validation selected an efficiency mutation | Frozen comparison |
| World Bank | Validation selected an efficiency mutation | Frozen comparison |

Abstention matters: a search iteration is not evidence that a mutation should
be published. Validation and frozen holdout remain separate decisions.

### 3.2 Frozen Outcomes

There are three relevant frozen results.

**Table 3: Frozen candidate outcomes**

| Candidate | Split | Seed skill | Candidate | Result |
| --- | --- | ---: | ---: | --- |
| Reviewer-produced recipe skill repair | Holdout quality | 95.50% | **98.35%** | Accept |
|  | Holdout pass rate | 100% | 100% |  |
|  | Agent tokens / case | 245,317 | **229,211 (-6.57%)** |  |
| Generic recipe efficiency mutation | Validation tokens / case | 167,545 | **150,211 (-10.35%)** | Continue |
|  | Holdout pass rate | **100%** | 87.50% | **Reject** |
|  | Holdout quality | **95.50%** | 83.41% |  |
| World Bank efficiency mutation | Validation tokens / case | **219,299** | 221,716 (+1.10%) | Continue |
|  | Holdout pass rate / quality | 100% / 100% | 100% / 100% | Accept |
|  | Holdout tokens / case | 421,255 | **385,355 (-8.52%)** |  |

The original recipe repair used two independent optimizer seeds and eight
holdout pairs. It produced four quality wins, four ties, no losses, and no pass
regression. That accepted candidate is retained in `recipe_candidate.json` and
is the recipe overlay used by the operational replay.

The later generic recipe mutation is the most important rejection. It preserved
validation quality while reducing tokens, yet one untouched `e3` pair failed;
across the untouched subset, pass rate fell from `100%` to `75%` and quality
fell `24.17pp`. The candidate was discarded even though its pooled holdout
tokens were lower.

The first World Bank confirmation exposed a mismatch between the optimizer's
internal scalar tie-breaker and the actual deployment objectives: official
pass/quality remained perfect and holdout tokens improved, but scalar noise
blocked promotion. Before collecting new confirmation seeds, protocol v2 made
official pass and quality the primary safety conditions, required zero paired
primary-metric losses, and used a `5%` holdout token benefit as the efficiency
criterion. Fresh seeds `507` and `508` then reproduced an `8.52%` holdout token
reduction with zero pass or quality losses.

These results are stored in
[`evidence.json`](evidence.json),
[`generic_candidate_frozen_evidence.json`](full_matrix/generic_candidate_frozen_evidence.json),
and
[`worldbank_candidate_frozen_evidence_v2.json`](full_matrix/worldbank_candidate_frozen_evidence_v2.json).

## 4. Full Operational Replay

### 4.1 Result by Root Seed

**Table 4: Three-arm result by root seed**

| Root seed | Arm order | Baseline pass / quality / E2E | Evolution pass / quality / E2E | Optimized pass / quality / E2E | Optimized vs evolution |
| ---: | --- | --- | --- | --- | --- |
| 601 | optimized → evolution → baseline | 100% / 98.12% / 343,425 | 100% / 98.21% / 341,727 | 100% / 98.23% / 360,892 | +0.02pp, +5.61% tokens |
| 602 | baseline → evolution → optimized | 96.67% / 95.08% / 271,517 | 100% / 98.21% / 342,637 | 100% / 98.02% / 351,420 | -0.19pp, +2.56% tokens |
| 603 | optimized → evolution → baseline | 96.67% / 94.99% / 320,668 | 100% / 98.29% / 338,802 | 100% / 98.24% / 370,136 | -0.05pp, +9.25% tokens |

Optimized evolution was more expensive in every root seed. Only seed `601`
had a small quality increase, and it was far below the preregistered `0.50pp`
meaningful-benefit threshold.

### 4.2 Family-Level Result

Only Recipe and World Bank received an offline overlay. The other three
families are useful negative controls because `evolution` and
`optimized_evolution` started from the same skill for those families.

**Table 5: Optimized evolution versus evolution by family (n = 18 per arm)**

| Family | Overlay | Pass delta | Quality delta | E2E token delta |
| --- | --- | ---: | ---: | ---: |
| Cat facts | No | 0.00pp | 0.00pp | -4.34% |
| Weather | No | 0.00pp | 0.00pp | +5.78% |
| Pokémon | No | 0.00pp | -0.38pp | +14.43% |
| Recipe | Yes | 0.00pp | 0.00pp | -2.68% |
| World Bank | Yes | 0.00pp | 0.00pp | +6.07% |

Across the two overlay families alone, quality tied and end-to-end tokens
increased `1.46%`. Recipe retained a small cost improvement, but it was below
the `5%` threshold; the World Bank frozen benefit did not transfer and reversed
to a cost increase. The no-overlay controls also moved substantially in both
directions, showing that one operational trajectory is too noisy to attribute
small token changes to a skill mutation.

### 4.3 Evolution Versus Baseline

The same run provides an updated evolution result under this model and the
hardened task-completion harness:

- evolution improved overall pass rate by `2.22pp` and quality by `2.18pp`;
- both pass wins were Pokémon artifact-completion failures: baseline
  `m1` under seed `602` and baseline `m2` under seed `603` did not produce
  `pokedex_entries.json`, while both evolution arms completed them;
- evolution had no pass losses, but end-to-end tokens increased `9.36%` after
  reviewer cost was included; and
- the other four families were already at 100% pass rate, so evolution's
  reliability benefit was concentrated rather than universal.

This is not directly comparable to the older `gpt-4o-mini` headline in the
main evolution report. It uses a different model, a higher and symmetric task
budget, entity-serial checkpoints, and end-to-end reviewer accounting.

## 5. Bad Cases and What Changed

### 5.1 Validation-Only Efficiency Was Unsafe

The rejected recipe mutation is exactly the failure mode a holdout is meant to
catch: validation quality tied and tokens improved, but an untouched scale lost
the final artifact. A selector based only on the validation scalar would have
published it.

### 5.2 Frozen GLM-5.2 Benefit Did Not Transfer to the GPT-5.2 Loop

World Bank improved on isolated frozen holdout but regressed on cost in the
full online loop. That replay changed both the execution setting and the model:
it added sequential managed-skill state, reviewer calls, more task scales, and
GPT-5.2 trajectories to a candidate confirmed with GLM-5.2. Frozen confirmation
is therefore necessary evidence, not sufficient evidence for a default runtime
overlay or for cross-model portability. The experiment cannot isolate which of
those changes caused the transfer failure.

### 5.3 Completion Failures Were Real Reliability Failures

The two baseline Pokémon failures had valid intermediate work but no final
artifact or completion signal. During experimentation, the benchmark was
hardened to process long tasks one entity at a time, overwrite a compact
`working_notes.json`, and allow a finalize-only recovery that may read notes
but may not call domain APIs again. All arms received the same fix and the same
budgets; the two remaining baseline failures are preserved rather than rerun.

### 5.4 Control Families Exposed Residual Variance

Cat, Weather, and Pokémon had no optimized overlay, yet their optimized arm
token deltas ranged from `-4.34%` to `+14.43%`. Reversed arm order and paired
task seeds reduce systematic bias but cannot eliminate provider and trajectory
variance. Three root seeds are sufficient for the fixed gate used here, not
for a claim of model-independent statistical significance.

## 6. Framework and API Assessment

The benchmark did not require a Python bridge to GEPA or public exposure of
optimizer internals. Its adapter only supplies the public task cases,
`Evaluator`, `Dataset`, `Request`, reflection model, and options, then calls
`New` and `Optimize`. Candidate graphs, Pareto bookkeeping, mutation parsing,
experiment storage, and promotion logic remain internal.

The public design is suitable for review because:

- optimization is opt-in and offline; it does not mutate the live skill path;
- the evaluator is application-owned, so domains can retain native quality and
  cost objectives;
- validation and holdout are explicit dataset contracts;
- metric calls, iterations, time, and reflection batch size are bounded;
- revision submission is optional through a small `RevisionSubmitter`
  interface and successful submissions still enter approval rather than
  silently becoming active; and
- the framework can return a useful candidate even when the caller's promotion
  policy rejects it.

The main framework PR is therefore ready for code review as an API primitive.
That conclusion is separate from whether these two benchmark candidates should
be deployed.

## 7. Conclusions and Next Step

The benchmark supports a bounded claim:

> Reflective optimization is useful for offline skill repair, candidate search,
> and evidence-based rejection under GLM-5.2. The accepted candidates did not
> produce a beneficial transfer to the tested GPT-5.2 runtime. A same-model
> GLM-5.2 operational replay has not yet been run.

The correct action for the current evidence is:

1. review the pure-Go optimizer API;
2. keep the current Recipe and World Bank overlay out of default promotion on
   the tested GPT-5.2 runtime;
3. retain the accepted candidates as research artifacts; and
4. before making a GLM-5.2 runtime claim, repeat the operational protocol with
   explicit `-model glm52 -reviewer-model glm52` and fresh root seeds rather
   than relabeling or tuning against seeds `601`--`603`.

Exact aggregate values, per-run summaries, family metrics, paired outcomes,
and gate verdicts are in
[`full_matrix_evidence.json`](full_matrix_evidence.json).
