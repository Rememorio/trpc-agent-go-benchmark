# Reflective Optimization Evidence

This directory records compact, sanitized evidence for the pure-Go reflective
skill optimizer. Raw model transcripts, workspaces, endpoint configuration,
and credentials are intentionally excluded.

## Revisions and configuration

- Framework revision: `c0b120728b27`
- Benchmark base revision: `1edee87`
- SkillCraft revision: `0a9ba8808ba49bbc7bd40ad2e853896b8c3d4764`
- Agent and reflection model: `glm52` through an OpenAI-compatible endpoint
- Search split: feedback `e1,e2`, validation `e3,m1`, holdout `m2,h1`
- Search budget: 4 mutations, batch size 2, at most 30 evaluated cases
- Completion/tool limits: 8192 completion tokens and 24 tool iterations

The endpoint is not assumed to honor the optimizer seed, so the results below
are evidence from paired runs, not a claim of statistical significance.

## What the runs established

### Weather control

The legacy weather seed already passed every validation and holdout case with
official quality `1.0`. Three mutations failed strict paired feedback
acceptance. A fourth mutation passed feedback but its validation score was
`0.99878106`, below the seed's `0.999025965`, so the seed was retained. The
frozen holdout comparison was therefore identical (`0.99810174` on both
sides). This is a negative control: being different was not enough for a
candidate to survive.

### Recipe search and bad-case repair

The official recipe evaluator exposed only `Field completeness`. The benchmark
adapter combined that signal with the public task's declared tools and the
generated JSON to report missing related-dish fields by recipe count. This
produced actionable reflection input without reading evaluator implementation
code or exposing validation/holdout cases.

The `description` and `when_to_use` mutations did not improve their paired
feedback batches and were rejected. The `steps` mutation in
[`recipe_candidate.json`](recipe_candidate.json) was accepted and reduced
validation agent tokens from `199455.5` to `151955.5` (`-23.81%`) while
preserving official quality `0.955` and pass rate `1.0`. However, on untouched
`m2,h1` holdout it increased tokens from `161656.5` to `259398.5` (`+60.46%`)
with no quality or pass-rate gain. It is therefore not promotion eligible.

That bad case led to two implementation fixes:

1. optimize mode now requires the agent to exercise non-conflicting candidate
   details instead of treating a minimal example schema as a reason to ignore
   the loaded skill; and
2. `optimization.Result` now always exposes `PromotionEligible` and
   `PromotionReason`, even when revision submission is disabled.

### Frozen final-code A/B

With search disabled (`max_iterations=0`), the original seed and frozen
candidate were evaluated independently by the final benchmark code using the
same optimizer seed. On `e3`, the candidate preserved quality/pass rate and
used `24.73%` fewer agent tokens. On the `m2,h1` holdout it again preserved
quality/pass rate, but used `23.59%` more tokens and 2.5 more tool calls on
average; mean duration was `18.19%` lower. The scalar score decreased by
`0.000496575`, so the candidate remains ineligible rather than being presented
as an improvement.

## Interpretation

These runs do **not** prove that the reflected recipe skill improves GLM-5.2
task quality. They prove the narrower, operationally important claim that the
optimizer can discover local changes, reject non-improving mutations, detect a
validation win that fails to generalize, and return an auditable no-promotion
decision. A positive skill-effect claim needs repeated independent runs and a
benchmark whose public output contract exposes the completeness fields it
scores.

Exact aggregate values are in [`evidence.json`](evidence.json). The command and
output contract are documented in the parent SkillCraft README.
