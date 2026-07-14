# Evaluating Reflective Skill Optimization on the SkillCraft Benchmark

## 1. Introduction

This experiment evaluates the pure-Go reflective optimizer in
`trpc-agent-go/evolution/optimization` against two SkillCraft task families.
The central question is:

> **Can reflective search produce a skill revision that improves unseen
> SkillCraft tasks, rather than only the cases used during search?**

The short answer is **not yet**. The optimizer found a recipe candidate that
looked cheaper on validation, but the saving did not generalize to holdout.
The promotion gate therefore kept the existing skill.

| Experiment | Validation | Holdout | Result |
| --- | --- | --- | --- |
| Weather control | No candidate beat the seed | Seed and selected skill were identical | Keep seed |
| Recipe optimization | Same quality, 23.81% fewer tokens | Same quality, 60.46% more tokens | Reject candidate |

This result proves that the search and promotion-safety path works end to end:
the integration can generate revisions, reject weak mutations, detect a
validation-only win, and avoid publishing it. It does **not** yet prove that
the optimizer can produce a skill with a repeatable quality or efficiency gain.

## 2. Experimental Setup

### 2.1 Benchmark and Search Configuration

| Item | Value |
| --- | --- |
| Benchmark | SkillCraft |
| Task families | `openmeteo-weather`, `recipe-cookbook-builder` |
| Variants per family | `e1` / `e2` / `e3` / `m1` / `m2` / `h1` |
| Agent / reflection model | `glm52` |
| Scoring | SkillCraft official evaluator plus cost objectives |
| Feedback split | `e1,e2` |
| Validation split | `e3,m1` |
| Holdout split | `m2,h1` |
| Search budget | 4 mutations, batch size 2, at most 30 evaluated cases |
| Runtime limits | 8192 completion tokens, 24 tool iterations |

The three scale splits are disjoint. Reflection receives only feedback-case
outputs, evaluator feedback, and bounded traces. Validation is used for
candidate selection; holdout is evaluated only after the selected candidate is
frozen. The model service is not assumed to honor the optimizer seed, so these
are paired benchmark observations rather than a statistical significance claim.

### 2.2 Optimization Mechanism

The optimizer repeats a small, gated search loop:

1. run the current skill on a feedback batch;
2. ask reflection to propose one bounded revision;
3. keep the revision only if it beats its parent on the same feedback batch;
4. select the best surviving revision on validation; and
5. freeze that revision and promote it only if it also passes holdout.

Feedback drives mutation, validation selects, and holdout protects promotion.
The optimizer never learns from validation or holdout outputs.

### 2.3 Evaluation Protocol

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

## 3. Results

### 3.1 Weather Negative Control

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

### 3.2 Recipe Search

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

### 3.3 Frozen Candidate A/B

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

## 4. Discussion

### 4.1 Why the Candidate Failed

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

### 4.2 What Is and Is Not Proven

The experiment validates three mechanism-level properties:

1. reflection can turn run feedback into a concrete skill revision;
2. search can compare that revision with the seed on separate cases; and
3. the holdout gate can prevent a non-generalizing candidate from becoming
   active.

It does not validate the stronger product claim that reflective optimization
already improves SkillCraft outcomes. That claim needs a candidate that wins
on frozen holdout data across repeated runs.

## 5. Conclusion and Next Steps

The optimizer is useful today as an experimental search and safety framework,
but this benchmark does not justify enabling automatic promotion by default.
The next evaluation should:

1. run multiple independent searches instead of relying on one mutation path;
2. evaluate frozen candidates repeatedly and report mean and variance;
3. optimize quality first and treat tokens, tool calls, and duration as
   separate secondary objectives; and
4. expand to more task families before making a general usefulness claim.

Exact machine-readable values are in [`evidence.json`](evidence.json), and the
frozen selected revision is in
[`recipe_candidate.json`](recipe_candidate.json).
