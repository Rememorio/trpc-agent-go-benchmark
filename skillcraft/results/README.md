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
fresh root seeds produce 90 tasks per arm and 270 arm-cases in total. Search,
review, and task execution all requested the self-deployed GLM-5.2 route
(`glm52`).

| Metric | Baseline | Evolution | Optimized evolution |
| --- | ---: | ---: | ---: |
| Pass rate | 97.78% | 97.78% | **98.89%** |
| Official quality | 95.98% | 95.96% | **97.21%** |
| End-to-end tokens / task | **305,240** | 352,971 | 362,368 |

The global optimized arm passed the fixed aggregate gate, but that verdict is
not sufficient to promote every overlay: most of its quality delta came from
Pokémon completion variance even though Pokémon received no optimized skill.
Pooling the two changed families gives a causal `6.77%` end-to-end token
reduction. The individual candidate result is clearer still: Recipe passed all
18 tasks in both arms, improved quality by 0.32 percentage points, and reduced
end-to-end tokens by 14.75%, with a token reduction in every root seed. World
Bank also kept pass and quality unchanged, but cost 3.29% more and was rejected.

An earlier GPT-5.2 runtime matrix is retained as a negative cross-model
portability test. It is not pooled with the same-model result. The offline
process also caught and rejected a separate Recipe candidate that saved tokens
on validation but regressed badly on untouched holdout.

The exact per-run, per-family, paired, and gate results are in
[`gepa_reflective_optimization/glm_full_matrix_evidence.json`](gepa_reflective_optimization/glm_full_matrix_evidence.json).

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
|   |-- glm_full_matrix_evidence.json # same-model GLM-5.2 aggregate
|   |-- full_matrix_evidence.json     # earlier GPT-5.2 aggregate
|   +-- full_matrix/
|       |-- generic_candidate_frozen_evidence.json
|       |-- worldbank_candidate_frozen_evidence_v1.json
|       +-- worldbank_candidate_frozen_evidence_v2.json
|-- reflective_full_matrix_601/       # canonical result, raw runs ignored
|-- reflective_full_matrix_602/
|-- reflective_full_matrix_603/
|-- reflective_glm_full_matrix_701/
|-- reflective_glm_full_matrix_702/
|-- reflective_glm_full_matrix_703/
|-- tools/
|-- full_compare_run1/                # older online-evolution batches
|-- full_compare_run2/
+-- full_compare_run3/
```

Older `multi_family_compare_*` and `full_compare_*` directories remain useful
for regression history. They use different models, budgets, task sets, or
protocols and must not be pooled with the current three-arm matrix.
