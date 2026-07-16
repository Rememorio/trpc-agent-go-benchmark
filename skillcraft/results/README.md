# Evaluation Results — SkillCraft

This directory contains two complementary evaluation tracks:

- the online asynchronous [`evolution` report](REPORT.md), with a
  [Chinese version](REPORT.zh_CN.md); and
- the pure-Go reflective optimizer's
  [five-family report](gepa_reflective_optimization/REPORT.md), with a
  [Chinese version](gepa_reflective_optimization/REPORT.zh_CN.md).

They answer different questions. Online evolution learns between tasks. The
optimizer searches over a frozen skill offline, validates candidates, and uses
an untouched holdout before any candidate can be promoted.

## Current Reflective-Optimization Result

The latest experiment uses the same five SkillCraft families and six scales in
three paired arms: `baseline`, `evolution`, and `optimized_evolution`. Three
root seeds produce 90 tasks per arm and 270 arm-cases in total.

| Metric | Baseline | Evolution | Optimized evolution |
| --- | ---: | ---: | ---: |
| Pass rate | 97.78% | **100.00%** | **100.00%** |
| Official quality | 96.06% | **98.24%** | 98.16% |
| End-to-end tokens / task | **311,870** | 341,055 | 360,816 |

Online evolution rescued two baseline failures and improved quality, at a
9.36% end-to-end token cost. The optimized overlay preserved pass rate but did
not improve quality and cost another 5.79%, so it failed the fixed
meaningful-benefit gate on the tested runtime. Search and frozen confirmation
used the self-deployed GLM-5.2 route (`glm52`), while this operational matrix
requested GPT-5.2. The result is therefore a negative cross-model transfer
result, not a same-model GLM-5.2 runtime verdict or a failure of holdout gating:
the offline process also caught and rejected a Recipe candidate that regressed
badly outside validation.

The exact per-run, per-family, paired, and gate results are in
[`gepa_reflective_optimization/full_matrix_evidence.json`](gepa_reflective_optimization/full_matrix_evidence.json).

## Evidence Layout

```text
results/
|-- README.md
|-- REPORT.md                         # online evolution report
|-- REPORT.zh_CN.md
|-- gepa_reflective_optimization/
|   |-- README.md
|   |-- REPORT.md                     # current optimizer report
|   |-- REPORT.zh_CN.md
|   |-- evidence.json                 # accepted Recipe repair
|   |-- recipe_candidate.json
|   |-- model_routing_evidence.json   # proves gpt-5.2 != glm52 routing
|   |-- full_matrix_evidence.json     # 3 x 5 x 6 x 3 aggregate
|   +-- full_matrix/
|       |-- generic_candidate_frozen_evidence.json
|       |-- worldbank_candidate_frozen_evidence_v1.json
|       +-- worldbank_candidate_frozen_evidence_v2.json
|-- reflective_full_matrix_601/       # canonical result, raw runs ignored
|-- reflective_full_matrix_602/
|-- reflective_full_matrix_603/
|-- tools/
|-- full_compare_run1/                # older online-evolution batches
|-- full_compare_run2/
+-- full_compare_run3/
```

Older `multi_family_compare_*` and `full_compare_*` directories remain useful
for regression history. They use different models, budgets, task sets, or
protocols and must not be pooled with the current three-arm matrix.
