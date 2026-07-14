# Reflective Skill Optimization on SkillCraft

## Executive Summary

This experiment evaluates the pure-Go reflective optimizer in
`trpc-agent-go/evolution/optimization` against two SkillCraft task families.
The optimizer can propose and select skill revisions, but promotion is decided
only after a paired comparison on a holdout split that reflection never sees.

The result is intentionally negative:

- the weather control retained its seed;
- the recipe search found a candidate that was cheaper on validation while
  preserving official quality and pass rate;
- the same recipe candidate became more expensive on holdout, again without a
  quality or pass-rate gain; and
- the optimizer correctly returned `promotion_eligible=false`.

This establishes that the integration can discover local changes, reject weak
mutations, catch a validation-only win, and make an auditable no-promotion
decision. It does **not** establish that the reflected skill improves GLM-5.2
task quality.

## Experimental Setup

| Item | Value |
| --- | --- |
| Recorded date | 2026-07-13 |
| Agent / reflection model | `glm52` through an OpenAI-compatible endpoint |
| Framework revision | `c0b120728b27` |
| Benchmark base revision | `1edee87` |
| SkillCraft revision | `0a9ba8808ba49bbc7bd40ad2e853896b8c3d4764` |
| Feedback split | `e1,e2` |
| Validation split | `e3,m1` |
| Holdout split | `m2,h1` |
| Search budget | 4 mutations, batch size 2, at most 30 evaluated cases |
| Runtime limits | 8192 completion tokens, 24 tool iterations |

The three scale splits are disjoint. Reflection receives only feedback-case
outputs, evaluator feedback, and bounded traces. Validation is used for
candidate selection; holdout is evaluated only after the selected candidate is
frozen. The endpoint is not assumed to honor the optimizer seed, so these are
paired operational observations rather than a statistical significance claim.

## What Was Measured

Each evaluation retains separate objectives for official quality, pass status,
agent tokens, tool calls, duration, and observed skill loading. Candidate
selection uses a scalar score with a hard pass/fail boundary. Among passing
runs, official quality dominates and token efficiency is only a small
tie-breaker.

The evaluator also turns safe, public run evidence into actionable feedback.
For recipe tasks, it combines the task's declared tools with the generated JSON
artifact to identify missing `category_dishes`, `cuisine_dishes`, and
`ingredient_dishes` fields. It does not inspect evaluator source or expose
validation and holdout cases to reflection.

## Results

### Weather negative control

The legacy weather seed already achieved official quality `1.0` across the
selected validation and holdout cases. Three mutations failed strict paired
feedback acceptance. One mutation was accepted on feedback, but its validation
score was lower than the seed, so selection retained the seed.

| Metric | Seed | Accepted candidate / selected | Decision |
| --- | ---: | ---: | --- |
| Validation score | 0.999025965 | 0.998781060 | retain seed |
| Holdout score | 0.998101740 | 0.998101740 | identical because seed remained selected |
| Evaluated cases |  | 22 |  |
| Accepted candidates including seed |  | 2 |  |
| Search agent tokens |  | 2,062,391 |  |
| Reflection tokens |  | 11,763 |  |

This is the expected behavior for a control: novelty alone is insufficient for
selection or promotion.

### Recipe search

The recipe search accepted a steps mutation after rejecting description and
`when_to_use` changes that did not improve their paired feedback batches. On
validation, the selected candidate preserved official quality and pass rate
while using fewer tokens.

| Validation metric | Seed | Selected | Delta |
| --- | ---: | ---: | ---: |
| Scalar score | 0.989455445 | 0.989930445 | +0.000475000 |
| Official quality | 0.955 | 0.955 | 0.000 |
| Pass rate | 1.000 | 1.000 | 0.000 |
| Agent tokens | 199,455.5 | 151,955.5 | -47,500.0 (-23.81%) |
| Tool calls | 20.5 | 19.5 | -1.0 |

The untouched holdout reversed the efficiency result:

| Holdout metric | Seed | Selected | Delta |
| --- | ---: | ---: | ---: |
| Scalar score | 0.987553435 | 0.986576015 | -0.000977420 |
| Official quality | 0.943 | 0.943 | 0.000 |
| Pass rate | 1.000 | 1.000 | 0.000 |
| Agent tokens | 161,656.5 | 259,398.5 | +97,742.0 (+60.46%) |
| Tool calls | 25.5 | 30.5 | +5.0 |

The holdout delta was below the configured non-regression threshold, so the
candidate was not eligible for promotion. The search evaluated 26 cases,
accepted 3 candidates including the seed, consumed 3,819,019 agent tokens, and
used 11,537 reflection tokens.

### Frozen final-code A/B

To separate search behavior from the final comparison, the seed and selected
recipe candidate were evaluated with search disabled (`max_iterations=0`) and
optimizer seed `29`.

| Split / metric | Seed | Candidate | Delta |
| --- | ---: | ---: | ---: |
| Validation `e3` score | 0.992066490 | 0.992477840 | +0.000411350 |
| Validation agent tokens | 166,351.0 | 125,216.0 | -24.73% |
| Holdout `m2,h1` score | 0.987064640 | 0.986568065 | -0.000496575 |
| Holdout agent tokens | 210,536.0 | 260,193.5 | +23.59% |
| Holdout tool calls | 27.0 | 29.5 | +2.5 |
| Holdout duration | 160.36 s | 131.19 s | -18.19% |

Official quality and pass rate were unchanged on both splits. The candidate was
faster in wall-clock time on holdout, but it spent more tokens and tool calls
and reduced the scalar score. It therefore remained ineligible.

## Bad-Case Analysis

The candidate overfit an efficiency signal visible on the selected validation
cases. Its added checklist made the required related-dish fields explicit and
helped `e3`, but the extra procedure caused more work on the larger `m2,h1`
tasks. Because quality was already flat, the additional work had no compensating
benefit.

This is precisely why validation selection and holdout promotion are separate:
a locally useful instruction can fail to generalize as task scale changes. The
optimizer records both the selected revision and the rejected promotion
decision instead of presenting the validation winner as a deployable
improvement.

## Conclusion and Next Evidence

The experiment supports the mechanism-level claim that reflective skill search
and holdout gating work end to end. It does not support a positive recipe-skill
effect claim. Such a claim would require:

1. multiple independent optimizer and model runs;
2. aggregate means and variance on frozen holdout cases;
3. a public output contract that directly exposes every completeness field the
   evaluator scores; and
4. a promotion rule that treats quality as primary and reports cost objectives
   separately when they disagree.

Exact machine-readable values are in [`evidence.json`](evidence.json), and the
frozen selected revision is in
[`recipe_candidate.json`](recipe_candidate.json).
