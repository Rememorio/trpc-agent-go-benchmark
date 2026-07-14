# Evaluating Reflective Skill Optimization on SkillCraft

## 1. Introduction

This experiment evaluates the pure-Go reflective optimizer in
`trpc-agent-go/evolution/optimization` on SkillCraft's
`recipe-cookbook-builder` family. It asks a narrower question than the main
evolution report:

> **Can the optimizer repair a real skill produced by the existing evolution
> service, freeze the result, and improve later paired runs?**

For this case, the answer is **yes**. The optimizer recovered missing artifact
rules from evaluator feedback, then learned a guardrail for an expensive hard
case. In two independent frozen comparisons, the final candidate passed both
validation and holdout gates. Across the eight holdout pairs, it had four
quality wins, four ties, no losses, and no pass-rate regressions.

**Table 1: Pooled frozen holdout result (2 optimizer seeds, 8 paired cases)**

| Metric | Existing skill | Optimized skill | Delta |
| --- | ---: | ---: | ---: |
| Official quality | 95.50% | **98.35%** | **+2.85pp** |
| Pass rate | 100% | 100% | 0.00pp |
| Agent tokens / case | 245,317 | **229,211** | **-6.57%** |
| Tool calls / case | 25.13 | **24.13** | **-3.98%** |
| Duration / case | 137.61 s | **133.59 s** | **-2.92%** |
| Scalar score | 0.988997 | **0.994573** | **+0.005576** |

This is evidence for a scoped product claim: reflective optimization is useful
as an offline repair and consolidation step for a legacy skill. It is not yet
evidence that every skill family will improve or that automatic promotion
should be enabled without a validation and holdout policy.

## 2. Experimental Setup

### 2.1 Benchmark and Seed Provenance

| Item | Value |
| --- | --- |
| Benchmark | SkillCraft |
| Task family | `recipe-cookbook-builder` |
| Agent / reflection model | `glm52` through an OpenAI-compatible endpoint |
| Starting skill | Exact `SkillSpec` conversion of a checked-in reviewer-generated session skill |
| Scoring | SkillCraft official quality plus separately retained cost objectives |
| Repeats | 2 paired runs per task scale |
| Evaluation temperature | 0 |
| Maximum tool iterations | 80 |

The starting point is not a deliberately weakened prompt. The file
[`recipe_session_legacy.json`](../../seeds/recipe_session_legacy.json) preserves
the name, description, usage guidance, steps, and pitfalls of an artifact
already produced by the current evolution service. The frozen output is
[`recipe_candidate.json`](recipe_candidate.json).

### 2.2 Optimization Mechanism

The optimizer runs a small, auditable search:

1. evaluate a parent skill on a paired feedback batch;
2. give only that batch's outputs, evaluator feedback, and bounded traces to
   the reflection model;
3. accept one-field mutations only when they beat the parent on the same
   paired batch;
4. select surviving candidates on a separate validation split; and
5. freeze the selected candidate before comparing it with the original skill.

The final candidate accumulated four general guardrails:

- preserve the exact related-dish keys required by the artifact contract;
- use the domain tools declared by the current task instead of a fixed
  skill-level endpoint list;
- write the requested artifact and signal completion; and
- reuse an earlier result instead of repeating an identical tool call.

The framework keeps candidate lineage, feedback decisions, multi-objective
measurements, and the promotion reason. Reflection cannot see validation or
holdout outputs, and frozen comparison performs no reflection or mutation.

### 2.3 Pairing and Split Discipline

Each baseline/candidate pair receives the same deterministic case seed. Run
order alternates to reduce systematic first-run effects. Quality, pass status,
tokens, tool calls, duration, and observed skill loading remain separate; the
scalar score gives pass/fail a hard boundary, makes official quality dominant,
and uses token efficiency only as a small tie-breaker.

The experiment followed a repair lifecycle rather than presenting every final
case as untouched:

- `e1,m1` first drove legacy-skill recovery;
- `e2,m2` selected candidates and later served as new-seed regression cases;
- `e3` never appeared in reflection and remained the untouched task-scale
  holdout;
- an early `h1` frozen run exposed an incomplete large artifact, so `h1` was
  deliberately rolled into feedback and later rerun with unseen case seeds as
  a hard-case regression set.

This distinction matters: `e3` tests scale generalization, while final `h1`
tests whether the discovered failure was actually repaired. A future broader
claim still needs new task families or a fresh hard scale.

## 3. Search and Repair Results

### 3.1 Recovering the Legacy Skill

The initial search used `e1,m1` for feedback and `e2,m2` for validation. Two of
four proposed mutations survived paired feedback evaluation. The selected
candidate generalized the legacy recipe description beyond a fixed number of
dishes and learned the artifact, task-tool, and completion guardrails.

**Table 2: Initial validation result**

| Metric | Legacy skill | Selected candidate | Delta |
| --- | ---: | ---: | ---: |
| Official quality | 95.50% | **99.175%** | **+3.675pp** |
| Pass rate | 100% | 100% | 0.00pp |
| Agent tokens / case | 137,331 | 193,249 | +40.72% |
| Scalar score | 0.990077 | **0.996500** | **+0.006423** |

This was a quality-for-cost tradeoff, not a free efficiency win. The search
evaluated 44 cases, retained three candidates including the seed, used
7,200,446 agent tokens, and used 22,547 reflection tokens.

### 3.2 Bad Case and Targeted Repair

An early multi-seed frozen check found a hard-case regression: one `h1` run
attempted an oversized structured write, failed to produce the required file,
and failed the task. That result was treated as a development failure, not
hidden in an average.

The repair search used `h1` as feedback and `e2,m2` as validation. The first
proposal was rejected. The accepted mutation preserved every earlier
guardrail and added only one rule: do not repeat a tool call with identical
arguments. On its paired hard feedback batch it kept quality and pass rate at
100% while reducing tokens by 19.33%. On validation it improved quality from
98.575% to 100%, with a 14.39% token increase. The quality-first selector
therefore kept it for frozen testing.

This failure also improved the framework-level reflection prompt. It now asks
for the smallest sufficient mutation, preserves cumulative guardrails unless
the evidence contradicts them, avoids hard-coding case-specific endpoint
names, and favors compact valid artifacts when long outputs approach the
response budget.

## 4. Frozen Confirmation

The selected skill was then fixed as an input: search iterations were disabled,
and no comparison output could alter it. Two independent optimizer seeds ran
the same `e2,m2` validation set and `e3,h1` holdout set, with two paired repeats
per scale.

**Table 3: Frozen result by optimizer seed**

| Seed | Split | Legacy quality | Optimized quality | Legacy tokens | Optimized tokens | Gate |
| ---: | --- | ---: | ---: | ---: | ---: | --- |
| 191 | Validation | 95.50% | **96.925%** | 137,977 | 182,186 | pass |
| 191 | Holdout | 95.50% | **98.35%** | 233,337 | **223,487** | pass |
| 197 | Validation | 95.50% | **99.175%** | 161,380 | 169,395 | pass |
| 197 | Holdout | 95.50% | **98.35%** | 257,297 | **234,935** | pass |

Both seeds independently produced a promotable decision. The pooled validation
result was quality `95.50% -> 98.05%`; its token cost increased `17.45%`, which
is acceptable under the configured quality-first objective. On pooled frozen
holdout, quality increased `2.85pp` while token, tool-call, and wall-clock costs
all decreased.

At case level, all four `e3` pairs tied on quality. All four `h1` pairs improved
from `0.943` to `1.000`. There were no quality losses and no pass regressions.
The candidate did sometimes spend more tokens on an individual run, but the
paired aggregate improved and the variance did not create a quality failure.

## 5. Additional Control

A separate exploratory search started from the repository's already-good
generic recipe seed. Its validation quality and pass rate remained unchanged,
while tokens decreased 13.58% and duration decreased 41.71%. Because that run
did not include a frozen holdout, it is an efficiency-search signal only and is
not part of the promotion evidence above.

This control is useful for a different reason: when quality is already
saturated, the same optimizer can search for a smaller execution cost, but a
validation-only result must not be marketed as a deployable improvement.

## 6. What the Benchmark Establishes

The experiment now supports these claims:

1. evaluator findings can be converted into bounded, general skill mutations;
2. a real reviewer-generated legacy skill can be repaired rather than replaced
   by a hand-authored benchmark prompt;
3. failed mutations and a validation winner with a frozen hard-case failure are
   rejected or fed into the next repair cycle;
4. the final frozen candidate improves paired holdout quality without reducing
   pass rate, and the result repeats under two independent seeds; and
5. the public API is sufficient for a benchmark adapter without exporting the
   optimizer's candidate graph, reflection protocol, Pareto logic, or storage
   internals.

It does not establish universal improvement across task families, statistical
significance for all model endpoints, or safety of ungated online promotion.
Those require more families, more independent runs, and a deployment-specific
promotion policy.

## 7. Conclusion

The pure-Go optimizer is useful for the tested workflow and is ready for code
review as an opt-in evolution primitive. Its strongest current use is offline
skill repair: search from real session evidence, freeze a candidate, and let
validation plus holdout decide whether the caller should promote it. Automatic
promotion remains a caller-owned policy rather than an optimizer side effect.

Exact machine-readable values are in [`evidence.json`](evidence.json).
