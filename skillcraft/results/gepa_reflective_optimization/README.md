# Reflective Optimization Evidence

This directory is the sanitized evidence package for the pure-Go reflective
optimizer and its five-family SkillCraft replay.

Read [`REPORT.md`](REPORT.md) or
[`REPORT.zh_CN.md`](REPORT.zh_CN.md) for the experiment design, bad-case
analysis, and scoped conclusion. The machine-readable evidence is split by the
decision it supports:

- [`evidence.json`](evidence.json) records the accepted repair of an existing
  reviewer-produced Recipe skill. Across 8 frozen holdout pairs it improved
  quality from 95.50% to 98.35% and reduced agent tokens by 6.57%, with no pass
  loss.
- [`full_matrix/generic_candidate_frozen_evidence.json`](full_matrix/generic_candidate_frozen_evidence.json)
  records a Recipe efficiency candidate that looked better on validation but
  failed untouched holdout. It was rejected.
- [`full_matrix/worldbank_candidate_frozen_evidence_v2.json`](full_matrix/worldbank_candidate_frozen_evidence_v2.json)
  records an accepted World Bank candidate: frozen holdout pass rate and
  quality remained 100%, while agent tokens fell 8.52%.
- [`full_matrix_evidence.json`](full_matrix_evidence.json) aggregates the final
  operational replay: 3 root seeds, 5 task families, 6 scales, and 3 arms, for
  270 arm-cases.
- [`recipe_candidate.json`](recipe_candidate.json) is the accepted Recipe
  candidate used as one of the runtime overlays.

The operational replay is deliberately stricter than the individual frozen
comparisons. `optimized_evolution` retained a 100% pass rate, but quality moved
by -0.08 percentage points and end-to-end tokens increased 5.79% relative to
`evolution`. It therefore failed the preregistered meaningful-benefit gate and
is not eligible for runtime promotion.

Raw model transcripts, task workspaces, endpoint configuration, credentials,
and machine-specific paths are intentionally excluded.
