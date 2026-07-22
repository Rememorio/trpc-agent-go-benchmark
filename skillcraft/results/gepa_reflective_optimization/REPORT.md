# Reflective Skill Optimization on SkillCraft

## 1. Introduction

This report evaluates the pure-Go reflective optimizer in
`trpc-agent-go/evolution/optimization`. It separates three questions that are
easy to conflate:

1. can offline reflection discover a better skill and reject an unsafe one on
   a frozen holdout;
2. does an accepted skill remain useful inside the full asynchronous evolution
   loop on the same model that produced it; and
3. is the framework API a sound primitive even when a particular candidate is
   not worth deploying?

The final same-model GLM-5.2 replay gives a scoped positive answer:

- **Recipe is a useful and promotable candidate for this runtime.** Across 18
  paired tasks it kept pass rate at 100%, improved official quality by
  `0.32pp`, and reduced end-to-end tokens by `14.75%`. The token direction was
  favorable in all three root seeds.
- **World Bank is not promotable.** Pass rate and quality remained at 100%, but
  end-to-end tokens increased `3.29%`, with a cost increase in every root seed.
- **The global gate is not sufficient attribution.** The preregistered gate
  passed, but its `+1.25pp` quality signal was dominated by Pokémon completion
  failures even though Pokémon received no offline overlay. Across only the
  two changed families, the overlay reduced end-to-end tokens by `6.77%` and
  improved quality by `0.16pp`; decomposition shows that Recipe produced the
  benefit while World Bank was a negative contribution.
- **The optimizer API is ready for review.** It found a stable improvement,
  rejected an unsafe validation winner, and allowed a frozen winner to be
  rejected later when online evidence did not reproduce its benefit.

**Table 1: Same-model GLM-5.2 operational replay (3 runs, n = 90 per arm)**

| Metric | Baseline | Evolution | Optimized evolution |
| --- | ---: | ---: | ---: |
| Pass rate | 97.78% | 97.78% | **98.89%** |
| Official quality | 95.98% | 95.96% | **97.21%** |
| Agent tokens / task | **305,240** | 337,288 | 346,978 |
| Reviewer tokens / task | 0 | 15,683 | 15,390 |
| End-to-end tokens / task | **305,240** | 352,971 | 362,368 |

The global optimized arm improved pass rate by `1.11pp` and quality by
`1.25pp` over evolution while costing `2.66%` more end to end. Those totals are
useful for checking the fixed gate, but the family analysis below is the basis
for candidate promotion.

## 2. Experimental Design

### 2.1 Three Evidence Stages

The evaluation separates discovery, confirmation, and operational use:

1. **Search:** start from five real or structurally equivalent evolution
   revisions; mutate one skill component at a time using paired feedback cases;
   select survivors on a disjoint validation split.
2. **Frozen confirmation:** fix the candidate, disable reflection, and compare
   it with the seed on validation plus untouched holdout cases under independent
   seeds.
3. **Operational replay:** compare `baseline`, `evolution`, and
   `optimized_evolution` over the same five families and six scales used by the
   existing evolution benchmark.

Search can abstain. Frozen confirmation can reject a search winner. Operational
replay can reject a frozen winner. This is intentional: each stage answers a
stricter deployment question than the previous one.

### 2.2 Models, Tasks, and Pairing

Search, frozen confirmation, and the final operational replay all requested the
self-deployed GLM-5.2 route as `glm52`. The final matrix explicitly used
`-model glm52 -reviewer-model glm52`, temperature zero, an 8,192-token maximum
response, and 80 tool iterations. The aggregate verifies those settings rather
than inferring them from endpoint configuration.

Fresh root seeds `701`, `702`, and `703` were used. All three arms received the
same task-specific sampling seed within each run. Odd and even root seeds
reversed whole-arm order while preserving online learning order inside each
arm:

- `701`: optimized evolution → evolution → baseline;
- `702`: baseline → evolution → optimized evolution;
- `703`: optimized evolution → evolution → baseline.

The five families were `cat-facts-collector`, `openmeteo-weather`,
`pokeapi-pokedex`, `recipe-cookbook-builder`, and
`world-bank-economic-snapshot`. Every family included `e1`, `e2`, `e3`, `m1`,
`m2`, and `h1`, producing 30 tasks per arm, 90 per arm across seeds, and 270
arm-cases in total.

An earlier matrix used fresh roots `601`--`603` but requested GPT-5.2 at
runtime after GLM-5.2 candidate discovery. A routing probe confirmed that
`gpt-5.2` and `glm52` were distinct routes. That matrix remains useful as a
cross-model portability test and is reported separately in Section 5; it is
not pooled with the same-model result.

### 2.3 Preregistered Operational Gate

The checked-in `skillcraft-5-family-3-arm-v1` protocol was fixed before either
final matrix was observed. Mechanical promotion eligibility required all of the
following:

- at least three complete runs, with all 30 tasks present in every arm;
- no overall or per-family pass-rate regression;
- overall quality no worse than `0.25pp` below evolution;
- per-family quality no worse than `1.00pp` below evolution; and
- a meaningful benefit: quality at least `+0.50pp` or end-to-end tokens at
  least `-5%`.

The aggregate command also rejects duplicate root seeds, missing official
evaluations, unexpected tasks, task seeds that are not paired across arms, and
configuration drift. Its output is sanitized and excludes local paths, model
transcripts, and credentials.

The gate is a necessary aggregate safety check, not a causal attribution
method. When only some families receive an overlay, a promotion decision must
also show that the benefit occurs in those families and is stable across root
seeds.

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

Abstention matters: completing a search iteration is not evidence that a
mutation should be published.

### 3.2 Frozen Outcomes

There are three relevant frozen results.

**Table 3: Frozen candidate outcomes**

| Candidate | Split | Seed skill | Candidate | Result |
| --- | --- | ---: | ---: | --- |
| Reviewer-produced Recipe skill repair | Holdout quality | 95.50% | **98.35%** | Accept |
|  | Holdout pass rate | 100% | 100% |  |
|  | Agent tokens / case | 245,317 | **229,211 (-6.57%)** |  |
| Generic Recipe efficiency mutation | Validation tokens / case | 167,545 | **150,211 (-10.35%)** | Continue |
|  | Holdout pass rate | **100%** | 87.50% | **Reject** |
|  | Holdout quality | **95.50%** | 83.41% |  |
| World Bank efficiency mutation | Validation tokens / case | **219,299** | 221,716 (+1.10%) | Continue |
|  | Holdout pass rate / quality | 100% / 100% | 100% / 100% | Accept |
|  | Holdout tokens / case | 421,255 | **385,355 (-8.52%)** |  |

The accepted Recipe repair used two independent optimizer seeds and eight
holdout pairs. It produced four quality wins, four ties, no losses, and no pass
regression. That candidate is retained in `recipe_candidate.json` and is the
Recipe overlay used by both operational matrices.

The later generic Recipe mutation is the most important rejection. It
preserved validation quality while reducing tokens, yet one untouched `e3`
pair failed; across the untouched subset, pass rate fell from 100% to 75% and
quality fell `24.17pp`. The candidate was discarded even though pooled holdout
tokens were lower.

The first World Bank confirmation exposed a mismatch between a scalar
tie-breaker and the deployment objectives. Before collecting new confirmation
seeds, protocol v2 made official pass and quality the primary safety conditions,
required zero paired primary-metric losses, and used a 5% holdout token benefit
as the efficiency criterion. Fresh seeds `507` and `508` reproduced an `8.52%`
holdout token reduction with zero pass or quality losses. The operational replay
then tested whether that isolated benefit survived the online loop.

## 4. Same-Model GLM-5.2 Operational Replay

### 4.1 Result by Root Seed

**Table 4: Three-arm result by root seed**

| Root seed | Arm order | Baseline pass / quality / E2E | Evolution pass / quality / E2E | Optimized pass / quality / E2E | Optimized vs evolution |
| ---: | --- | --- | --- | --- | --- |
| 701 | optimized → evolution → baseline | 100% / 98.15% / 306,475 | 96.67% / 94.97% / 339,270 | 100% / 98.23% / 355,400 | +3.33pp pass, +3.26pp quality, +4.75% tokens |
| 702 | baseline → evolution → optimized | 96.67% / 94.92% / 335,723 | 100% / 97.93% / 362,645 | 96.67% / 94.90% / 368,126 | -3.33pp pass, -3.03pp quality, +1.51% tokens |
| 703 | optimized → evolution → baseline | 96.67% / 94.86% / 273,524 | 96.67% / 94.98% / 356,998 | 100% / 98.51% / 363,578 | +3.33pp pass, +3.53pp quality, +1.84% tokens |

The overall arm result is noisy because all three non-tied
evolution/optimized pass comparisons were isolated task failures in families
that received no overlay. Paired outcomes were 2 pass wins, 87 ties, and 1
loss; quality had 7
wins, 80 ties, and 3 losses. The preregistered aggregate gate passed, but the
next section determines which candidate actually caused a repeatable benefit.

### 4.2 Family-Level Attribution

Only Recipe and World Bank received an offline overlay. Cat, Weather, and
Pokémon are negative controls because evolution and optimized evolution began
with the same skill in those families.

**Table 5: Optimized evolution versus evolution by family (n = 18 per arm)**

| Family | Overlay | Pass delta | Quality delta | Agent-token delta | E2E-token delta |
| --- | --- | ---: | ---: | ---: | ---: |
| Cat facts | No | 0.00pp | 0.00pp | -16.08% | -15.11% |
| Weather | No | 0.00pp | 0.00pp | +5.39% | +5.22% |
| Pokémon | No | +5.55pp | +5.95pp | +22.93% | +22.02% |
| Recipe | **Yes** | 0.00pp | **+0.32pp** | **-14.86%** | **-14.75%** |
| World Bank | **Yes** | 0.00pp | 0.00pp | +3.27% | +3.29% |

The global quality win was dominated by Pokémon, where the two arms used the
same warm-start skill and the evolution arm happened to suffer two missing
artifacts. It cannot be attributed to either optimized candidate. Conversely,
the Recipe and World Bank rows directly compare changed and unchanged skill
libraries under the same online machinery.

Pooling only the two changed families gives 100% pass rate in both arms,
`+0.16pp` quality, `-6.80%` agent tokens, and `-6.77%` end-to-end tokens. That
attribution-aware scope independently clears the 5% meaningful-benefit
threshold. It supports the optimized overlay as a useful experimental unit,
but it does not make both revisions equally valuable: Recipe supplies all of
the saving, while World Bank makes the bundle less efficient than Recipe alone.
Because the production promotion unit is one skill revision, the more precise
decision is still to promote Recipe and reject World Bank.

### 4.3 Recipe: Repeatable Runtime Benefit

**Table 6: Recipe optimized evolution versus evolution**

| Root seed | Pass delta | Quality delta | Agent-token delta | E2E-token delta |
| ---: | ---: | ---: | ---: | ---: |
| 701 | 0.00pp | 0.00pp | -6.69% | -6.61% |
| 702 | 0.00pp | 0.00pp | -25.28% | -24.25% |
| 703 | 0.00pp | +0.95pp | -11.80% | -12.52% |
| **Aggregate** | **0.00pp** | **+0.32pp** | **-14.86%** | **-14.75%** |

Recipe passed all 18 tasks in both arms. The improvement is not a single-run
outlier: tokens fell in every root seed, by more than the 5% meaningful-benefit
threshold each time. One optimized `h1` reviewer call timed out under seed
`703`, but the agent-only comparison still improved `11.80%` in that seed and
`14.86%` overall. The promotion conclusion therefore does not depend on
omitting reviewer cost.

This is the strongest evidence in the study: the optimizer repaired the skill,
frozen confirmation accepted it, and a fresh same-model online matrix
reproduced a larger efficiency benefit without a safety regression.

### 4.4 World Bank: Frozen Winner, Runtime Rejection

World Bank also passed all 18 tasks in both arms with identical 100% quality,
but end-to-end tokens changed by `+5.63%`, `+2.55%`, and `+1.70%` in seeds
`701`, `702`, and `703`. The aggregate increase was `3.29%`. A missing
evolution reviewer result for seed `702/e3` does not reverse the conclusion:
agent-only tokens also increased `3.27%` overall.

The isolated frozen benefit therefore did not survive sequential managed-skill
state and the full online loop. The gate worked as intended at two levels:
frozen confirmation allowed a plausible candidate to continue, and operational
evidence prevented its deployment.

### 4.5 Evolution Versus Baseline

Under GLM-5.2, evolution and baseline both passed 88 of 90 tasks. Evolution
quality changed by `-0.02pp` and end-to-end tokens increased `15.64%`. Paired
outcomes contained two pass wins and two pass losses, plus five quality wins and
five losses. This matrix therefore does not establish a general benefit for the
unmodified online evolution arm under this model and budget.

That statement does not replace the older online-evolution report, where other
models and budgets showed value by avoiding rare catastrophic loops. The
experiments answer different runtime questions and must not be pooled.

## 5. Earlier GPT-5.2 Cross-Model Replay

Before the same-model matrix, the same GLM-produced overlays were replayed on a
distinct GPT-5.2 route with roots `601`--`603`.

**Table 7: GPT-5.2 operational replay (3 runs, n = 90 per arm)**

| Metric | Baseline | Evolution | Optimized evolution |
| --- | ---: | ---: | ---: |
| Pass rate | 97.78% | **100.00%** | **100.00%** |
| Official quality | 96.06% | **98.24%** | 98.16% |
| End-to-end tokens / task | **311,870** | 341,055 | 360,816 |

Relative to evolution, optimized evolution changed quality by `-0.08pp` and
increased end-to-end tokens by `5.79%`, so it failed the meaningful-benefit
gate. Recipe saved only `2.68%` end to end, while World Bank cost `6.07%` more.
This negative result is still useful: it shows that a GLM-5.2 skill improvement
is not automatically portable to GPT-5.2. It also explains why the same-model
matrix was required rather than relabeling the first replay.

## 6. Bad Cases and Limitations

### 6.1 Five Completion Failures Were Preserved

The same-model matrix had five failed arm-cases, all outside the two overlay
families:

- seed `701`, evolution Pokémon `m2`: no final artifact;
- seed `702`, baseline Cat `h1`: the final response contained a textual tool
  call instead of executing it;
- seed `702`, optimized Pokémon `e2`: the same textual-tool-call failure;
- seed `703`, evolution Pokémon `m2`: long tool output and repeated recovery
  ended without the artifact; and
- seed `703`, baseline Pokémon `h1`: the run stopped before finalization.

The two optimized/evolution Pokémon arms used the same starting skill, so their
pass difference is runtime variance rather than evidence for an overlay. No
failed case was selectively rerun.

### 6.2 Negative Controls Show Residual Trajectory Variance

No-overlay family deltas ranged from a `15.11%` token reduction for Cat to a
`22.02%` increase for Pokémon. Cat itself reversed direction under seed `702`
despite improving in the other two seeds. Reversed arm order and paired task
seeds reduce systematic bias but do not make provider sampling deterministic.
Three seeds satisfy the fixed protocol; they do not establish
model-independent statistical significance.

### 6.3 Reviewer Isolation Worked

Three reviewer calls timed out without invalidating the task result: evolution
World Bank `e3` under seed `702`, and optimized Pokémon `m2` plus Recipe `h1`
under seed `703`. Reporting agent-only and end-to-end sensitivity prevents those
missing reviewer tokens from manufacturing either the Recipe win or the World
Bank loss.

### 6.4 Tool-Response Robustness Remains a Benchmark Concern

Recoverable filesystem-MCP response errors appeared in multiple arms. The two
textual-tool-call failures also show that OpenAI-compatible endpoints may encode
an intended tool invocation as plain text. Pokémon is especially exposed to
large tool responses and long-context recovery loops. These are useful future
harness improvements, but changing them after observing the matrix would have
invalidated the frozen protocol.

## 7. Framework and API Assessment

The benchmark did not require a Python bridge to GEPA or public exposure of
optimizer internals. Its adapter supplies the public task cases, `Evaluator`,
`Dataset`, `Request`, reflection model, and options, then calls `NewGEPA` and
uses the returned one-method `Optimizer` interface. The concrete GEPA type,
candidate graphs, Pareto bookkeeping, and mutation parsing remain private. A
shared internal lifecycle owns seed and holdout evaluation, budgets, experiment
records, promotion, and optional revision submission, so another built-in
search does not need to duplicate those controls.

The public design is suitable for review because:

- optimization is opt-in and offline; it does not mutate the live skill path;
- the algorithm is selected by a typed constructor rather than a string
  registry, and each result records which implementation produced it;
- the evaluator is application-owned, so domains retain their native quality
  and cost objectives;
- validation and holdout are explicit dataset contracts;
- metric calls, iterations, time, and reflection batch size are bounded;
- revision submission is optional through the small `RevisionSubmitter`
  interface, and a successful submission still enters approval rather than
  silently becoming active;
- search can abstain and promotion policy remains application-owned; and
- the framework returns evidence even when the caller correctly rejects the
  candidate.

The result also exercises the intended extensibility boundary: the optimizer
contract stays independent of GEPA internals while the benchmark expresses
SkillCraft-specific evaluation, token accounting, frozen holdout policy, and
deployment gates externally.

## 8. Conclusions and Next Step

The complete evidence supports this bounded conclusion:

> The pure-Go reflective optimizer is useful on SkillCraft. It found a Recipe
> skill whose same-model GLM-5.2 online replay preserved 100% pass rate and
> reduced end-to-end tokens by 14.75% across three consistently favorable root
> seeds. It also rejected one unsafe Recipe mutation and prevented a World Bank
> frozen winner from being promoted after its runtime benefit failed to
> reproduce.

The correct action is:

1. move the main optimizer PR to normal code review;
2. promote or package the accepted Recipe candidate for this GLM-5.2 runtime;
3. do not promote the World Bank candidate; although the combined experimental
   overlay is positive at the changed-family scope, Recipe alone is the
   strictly better deployment choice;
4. retain GPT-5.2 as a negative portability result; and
5. treat broader model portability and Pokémon tool-response robustness as
   follow-up work, not as a prerequisite for reviewing the API.

Exact same-model aggregate values, per-run summaries, family metrics, paired
outcomes, and preregistered gate verdicts are in
[`glm_full_matrix_evidence.json`](glm_full_matrix_evidence.json). The earlier
cross-model aggregate remains in
[`full_matrix_evidence.json`](full_matrix_evidence.json).
