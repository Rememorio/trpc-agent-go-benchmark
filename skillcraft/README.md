# SkillCraft Benchmark for trpc-agent-go

This benchmark runs local [SkillCraft](https://github.com/shiqichen17/SkillCraft)
tasks with `trpc-agent-go` across four run modes:

- `baseline`: no learned skills.
- `evolution`: reuse skills extracted asynchronously by the `evolution`
  reviewer between tasks.
- `compare`: run `baseline` + `evolution` back-to-back and report deltas.
- `optimize`: run offline reflective search over one seed skill, select on a
  validation split, and make the promotion decision on an unseen holdout split.

When `compare` also receives `-optimized-skills-from`, it runs a controlled
third arm named `optimized_evolution`. Both evolution arms use the same online
reviewer, publisher, quality gates, task sequence, and model settings; only the
warm-start skill library differs. The third arm starts from
`-load-skills-from` and overlays only the changed skill folders from
`-optimized-skills-from`, so callers do not maintain two complete copies.
`-evaluation-seed` derives one task-specific
sampling seed that is reused across all arms, and odd/even root seeds reverse
the whole-arm order across repeated runs without disturbing online learning
within an arm.

`trpc-agent-go` intentionally keeps skill management on the
reviewer-driven async path. The old in-flow `skill_manage` experiment
was removed after early runs showed prompt overhead without reliable
tool usage.

## Latest Snapshot

The current reflective-optimization source of truth is the paired five-family
matrix in
[`results/gepa_reflective_optimization/REPORT.md`](results/gepa_reflective_optimization/REPORT.md)
([中文](results/gepa_reflective_optimization/REPORT.zh_CN.md)). It contains
three root seeds, all six scales, and three arms: 90 tasks per arm and 270
arm-cases total.

- Baseline: 97.78% pass rate, 95.98% quality.
- Evolution: 97.78% pass rate, 95.96% quality.
- Optimized evolution: 98.89% pass rate, 97.21% quality.

Search, review, and task execution all requested the self-deployed GLM-5.2
route (`glm52`). The global aggregate passed the fixed mechanical gate, but
promotion is decided per changed family because no-overlay task failures
dominated the global quality delta. Pooling the two changed families gives a
causal 6.77% end-to-end token reduction. Recipe is the stronger attributable
result: it passed all 18 tasks in both arms, improved quality by 0.32 percentage
points, and reduced end-to-end tokens by 14.75%, with a reduction in all three
root seeds. World Bank kept pass and quality unchanged but cost 3.29% more and
was rejected.

The evidence package also retains an earlier negative GPT-5.2 cross-model
portability replay and an important holdout rejection: a separate Recipe
candidate saved tokens on validation but lost a deliverable on untouched
holdout.

The older **v19** and **v20** batches below remain the source of historical
online-evolution behavior. They use a different model, task subset, budget, and
protocol, so their headline values are not directly comparable with the new
matrix. See [`results/REPORT.md`](results/REPORT.md) and
[`results/REPORT.zh_CN.md`](results/REPORT.zh_CN.md) for that write-up.

### v19 headline (runtime changes, no quality gate)

- [`results/multi_family_compare_v19_try1`](results/multi_family_compare_v19_try1)
- [`results/multi_family_compare_v19_try2`](results/multi_family_compare_v19_try2)
- [`results/multi_family_compare_v19_try3`](results/multi_family_compare_v19_try3)

Across these three runs:

- Baseline mean pass rate: `96.30%`. Evolution mean pass rate: `91.67%`.
- Baseline mean end-to-end tokens / task: `156,091`.
- Evolution mean end-to-end tokens / task: `132,584`
  (**-23,507, -15.1%**).
- Baseline end-to-end token stddev: `58,517`. Evolution stddev:
  `14,109` (**~24% of baseline variance**).
- `skill_load` invocation rate: baseline `0%`, evolution **`100%`**
  across all three runs.
- One evolution "win" rescues a 1.2M-token catastrophic baseline loop
  on `weather/e1` in try2.

### v20 headline (quality gate Phase A + B live)

- [`results/multi_family_compare_v20_try1`](results/multi_family_compare_v20_try1)
- [`results/multi_family_compare_v20_try2`](results/multi_family_compare_v20_try2)
- [`results/multi_family_compare_v20_try3`](results/multi_family_compare_v20_try3)
- [`results/multi_family_compare_v20_try4`](results/multi_family_compare_v20_try4)
- [`results/multi_family_compare_v20_try5`](results/multi_family_compare_v20_try5)

Across these five runs:

- Baseline mean pass rate: `95.00%`. Evolution mean pass rate:
  **`98.33%`** (**+3.33pp**).
- Baseline mean end-to-end tokens / task: `148,396`. Evolution:
  `129,408` (**-18,988, -12.8%**).
- Baseline e2e-token stddev: `84,820`. Evolution e2e-token stddev:
  `14,857` (**17.5% of baseline variance**).
- `skill_load` invocation rate: **still 100%** across all five runs.
- 47 revisions written and promoted across the five runs combined.
- 0 spec-gate rejections, 0 safety-gate rejections.
- Every promoted skill has an on-disk `managed_skills_revisions/<id>/`
  directory with immutable revisions, an append-only `audit.log`, and
  an `active.txt` rollback pointer.

Read the first three runs in isolation and you would conclude v20
slightly regressed; `try4` (baseline's `weather/e1` at 1.34M tokens
+ `agent_error`, `weather/m1` at 1.10M tokens, `recipe/e3` failed,
evolution 12/12 at 119k tokens each) flips the aggregate and
reproduces v19's pattern — evolution's benefit shows up on the
runs where baseline catastrophically loops. Effectiveness
evaluation in Phase C should therefore always use **five-run
aggregates with stddev**, never single runs or three-run means.

### Historical reference: v18 three-run batch

- [`results/full_compare_run1`](results/full_compare_run1)
- [`results/full_compare_run2`](results/full_compare_run2)
- [`results/full_compare_run3`](results/full_compare_run3)
- Aggregate JSON:
  [`results/tools/full_compare_analysis.json`](results/tools/full_compare_analysis.json)
- Aggregate script:
  [`results/tools/aggregate_runs.py`](results/tools/aggregate_runs.py)

v18 was the plateau where `skill_load` never fired and mean pass rate
tied; kept here as historical baseline only.

## Reproducing The Current Batches

From `skillcraft/trpc-agent-go-impl`:

### v19 (quality gate off)

```bash
go run . \
  -skillcraft-root "$SKILLCRAFT_ROOT" \
  -tasks "openmeteo-weather/e1,openmeteo-weather/e2,openmeteo-weather/e3,openmeteo-weather/m1,openmeteo-weather/m2,openmeteo-weather/h1,recipe-cookbook-builder/e1,recipe-cookbook-builder/e2,recipe-cookbook-builder/e3,recipe-cookbook-builder/m1,recipe-cookbook-builder/m2,recipe-cookbook-builder/h1" \
  -mode compare \
  -model gpt-4o-mini \
  -reviewer-model gpt-4o-mini \
  -max-tool-iterations 24 \
  -load-skills-from ../results/tools/seed_skills \
  -max-prompt-skills 8 \
  -output ../results/multi_family_compare_v19_tryN
```

### v20 (quality gate on)

```bash
go run . \
  -skillcraft-root "$SKILLCRAFT_ROOT" \
  -tasks "openmeteo-weather/e1,openmeteo-weather/e2,openmeteo-weather/e3,openmeteo-weather/m1,openmeteo-weather/m2,openmeteo-weather/h1,recipe-cookbook-builder/e1,recipe-cookbook-builder/e2,recipe-cookbook-builder/e3,recipe-cookbook-builder/m1,recipe-cookbook-builder/m2,recipe-cookbook-builder/h1" \
  -mode compare \
  -model gpt-4o-mini \
  -reviewer-model gpt-4o-mini \
  -max-tool-iterations 24 \
  -load-skills-from ../results/tools/seed_skills \
  -max-prompt-skills 8 \
  -enable-approval-gate \
  -output ../results/multi_family_compare_v20_tryN
```

Run each command three times with distinct output directories to
reproduce the three-run batch. The warm-start seed used by all runs
lives at [`results/tools/seed_skills`](results/tools/seed_skills)
(9 generic-parent-only skills covering all 5 task families).

The benchmark implementation pins the framework revision in its `go.mod`.
During local framework development, point that module at the framework checkout
with a temporary `go.work` file; do not commit a machine-specific replacement.

## What It Does

For each selected SkillCraft task, the runner:

1. Creates a clean task workspace and copies `initial_workspace/` if present.
2. Exposes SkillCraft local Python tools through a small MCP stdio bridge.
3. Exposes workspace file operations through the standard filesystem MCP server.
4. Runs the task with `trpc-agent-go`.
5. Invokes SkillCraft's native `evaluation/main.py` to get the official score JSON.
6. In `evolution` mode, stores learned `SKILL.md` files and makes them available to later tasks.

The MCP bridge also ships `local-write_final_json`, which writes the
final JSON deliverable directly to the workspace and recovers from
common encoding mistakes. Prompts steer the agent to prefer this tool
for the final deliverable.

## Three-Arm Operational Replay

For the five-family operational replay, run compare at least three times with
distinct root seeds and aggregate the canonical results with the checked-in
validator:

```bash
go run ./cmd/aggregate \
  -input ../results/reflective_full_matrix_601/results.json \
  -input ../results/reflective_full_matrix_602/results.json \
  -input ../results/reflective_full_matrix_603/results.json \
  -output ../results/gepa_reflective_optimization/full_matrix_evidence.json
```

The validator requires every run to contain all five families and all six
scales in all three arms, verifies that matching tasks share a sampling seed,
and rejects duplicate root seeds. Its promotion protocol is fixed in
`internal/experiment`: no overall or per-family pass-rate regression, bounded
quality non-regression, and at least one meaningful benefit (quality +0.5pp or
end-to-end tokens -5%). This full matrix is operational evidence, not a strict
holdout: candidate search may already have used some of its scales. Strict
frozen holdout remains a separate `optimize`-mode comparison below.

The checked-in aggregate fails only the meaningful-benefit gate. Because its
candidates were confirmed with GLM-5.2 and replayed with GPT-5.2, that outcome
shows the overlay did not transfer to the tested runtime. It does not replace a
same-model GLM-5.2 operational replay.

## Reflective Skill Optimization

`-mode optimize` evaluates existing skills with the pure-Go
`evolution/optimization` package. Unlike the asynchronous `evolution` mode,
it does not learn from tasks in sequence. Without an explicit candidate it runs
an offline search:

1. evaluate the seed skill on validation tasks;
2. run paired parent/child feedback tasks and ask the reflection model to
   change one skill component;
3. retain only children that strictly improve the paired feedback score;
4. select candidates on validation tasks; and
5. compare the frozen winner with the seed on unseen holdout tasks.

The reusable search, dataset, evaluator, and report code lives under
`trpc-agent-go-impl/internal/optimization`. The top-level `optimize.go` only
adapts SkillCraft workspaces, agent execution, and the official evaluator to
that internal contract.

The default scaled-task split trains on `e1,e2`, selects on `e3,m1`, and
holds out `m2,h1`. Splits must be disjoint. The included seed inputs are either
strict `SkillSpec` JSON documents or immutable evolution `Revision` documents
containing a spec. This preserves reviewer provenance without requiring a
lossy `SKILL.md` parser.

The five-family controlled inputs live under
[`seeds/reflective_full`](seeds/reflective_full/README.md). Four are direct
approval-gate revision snapshots from the online reviewer; the World Bank seed
predates revision storage and is the structured equivalent of the repository's
existing generic managed skill.

`weather_multi_city_legacy.json` is a conservative control for checking that
unhelpful rewrites are rejected instead of being promoted merely because they
are different.

`recipe_session_legacy.json` preserves a task-specific skill learned by the
existing session reviewer in the checked-in multi-family benchmark. Its fixed
dish count and endpoint set make it a realistic recovery target for testing
whether offline optimization can turn an overfit online-learning artifact into
a reusable skill; it is not a hand-written degraded control.

```bash
go run . \
  -skillcraft-root "$SKILLCRAFT_ROOT" \
  -base-task openmeteo-weather \
  -scales e1,e2,e3,m1,m2,h1 \
  -mode optimize \
  -model "$MODEL_NAME" \
  -reviewer-model "$MODEL_NAME" \
  -optimization-seed-spec ../seeds/weather_multi_city_legacy.json \
  -optimization-max-iterations 4 \
  -optimization-reflection-batch-size 2 \
  -optimization-max-metric-calls 30 \
  -max-tokens 8192 \
  -max-tool-iterations 24 \
  -output ../results/gepa_weather
```

After search selects a candidate, freeze it and repeat the comparison without
reflection by passing `-optimization-candidate-spec`. Each seed/candidate case
pair receives the same evaluator pairing seed. The adapter derives its
OpenAI-compatible model sampling seed from that pairing seed and the case ID,
so repeated cases are independent while the same case remains paired between
parent and child. Arm order alternates from a seeded starting point to reduce
order bias. Per-case measurements are retained in `results.json`. Optimization
evaluation defaults to temperature zero.
Provider-side seed and temperature handling are still best effort, so use
multiple repeats for promotion evidence.

SkillCraft does not label any scaled task as safety-critical, so per-case
critical gates are disabled by default and promotion uses aggregate validation
and holdout non-regression. Use `-optimization-critical-scales` only for
holdout scales whose individual score must never regress; ordinary stochastic
repeats should not be marked critical merely because they are in holdout.

For strict two-stage experiments, set `-optimization-holdout-scales ""` during
search. That run can select a validation winner but cannot make a promotion
decision or consume holdout cases. Supply the holdout scales only in the later
frozen comparison.

```bash
go run . \
  -skillcraft-root "$SKILLCRAFT_ROOT" \
  -base-task recipe-cookbook-builder \
  -scales e1,e2,e3,m1,m2,h1 \
  -mode optimize \
  -model "$MODEL_NAME" \
  -optimization-seed-spec ../seeds/recipe_multi_dish.json \
  -optimization-candidate-spec ../results/gepa_reflective_optimization/recipe_candidate.json \
  -optimization-repeats 3 \
  -max-tool-iterations 80 \
  -output ../results/recipe_frozen_comparison
```

The frozen comparison still uses validation as a non-regression check and
holdout for the final promotion decision. Once this confirmation run starts, it
evaluates both splits even when the validation scalar regresses; otherwise the
holdout could not expose whether the same candidate also fails outside the
selection split. The comparison never invokes the reflection model or mutates
either input spec. To leave holdout unconsumed during discovery, omit it from
the earlier search run as described above. Set the tool-iteration budget high
enough for the largest case;
`recipe-cookbook-builder/h1` alone declares 25 domain calls before skill loading,
artifact writing, validation, and completion, and its task configuration allows
up to 80 model turns for retries.

The search score preserves a hard pass/fail boundary. Among passing runs,
official SkillCraft quality dominates and normalized agent-token efficiency
is a small tie-breaker. Raw quality, pass status, tokens, tool calls,
duration, and observed `skill_load` usage remain separate objectives in the
report. Only exact duplicate tool calls (same normalized tool name and
argument hash) are reported to the reflector; argument contents are not
persisted.

The recipe evaluator's generic `Field completeness` message does not identify
which fields are absent. For feedback cases, the adapter enriches that signal
without reading evaluator source: it combines the task's declared
`meta.tools_used` with the generated cookbook JSON and reports missing
`category_dishes`, `cuisine_dishes`, or `ingredient_dishes` fields by recipe
count. This is the actionable side information needed for reflective mutation.
Validation and holdout results remain hidden from reflection.

Like the other modes, optimization writes structured data to `results.json` and
the readable decision summary to `REPORT.md`. Its mode-specific artifacts are
the validation-selected `selected_skill/`, evaluated candidate snapshots under
`optimization_candidates/`, and the full `optimization_experiments/` lineage.
The canonical result and report expose `promotion_eligible` and
`promotion_reason`, so a validation winner that regresses on holdout is explicit
rather than silently presented as deployable.
Use repeated cases and multiple optimizer seeds for effectiveness claims:
the optimizer seed makes sampling reproducible, but an OpenAI-compatible
model endpoint may still be nondeterministic.

A sanitized reflective search and operational replay record is kept under
[`results/gepa_reflective_optimization`](results/gepa_reflective_optimization/README.md),
with full reports in
[`REPORT.md`](results/gepa_reflective_optimization/REPORT.md) and
[`REPORT.zh_CN.md`](results/gepa_reflective_optimization/REPORT.zh_CN.md).
It records the five-family search, accepted and rejected frozen candidates,
and the complete three-arm matrix. The optimizer repaired one
reviewer-generated Recipe skill and accepted a World Bank efficiency candidate
under frozen confirmation, but their combined runtime overlay did not pass the
operational promotion gate. The report keeps those two conclusions separate.

## Key Flags

| Flag | Description |
|------|-------------|
| `-skillcraft-root` | Local SkillCraft checkout path |
| `-tasks` | Explicit task directories, comma-separated |
| `-base-task` | Base task family, e.g. `cat-facts-collector` |
| `-scales` | Scale list for `-base-task`, e.g. `e1,e2,e3` |
| `-mode` | `baseline`, `evolution`, `compare`, or `optimize` |
| `-model` | Agent model |
| `-reviewer-model` | Evolution reviewer model |
| `-output` | Result directory |
| `-task-timeout-seconds` | Override task timeout |
| `-max-tool-iterations` | Max tool loops per task |
| `-load-skills-from` | Warm-start the evolution arm from an existing managed-skill directory |
| `-optimized-skills-from` | In compare mode, add an optimized-evolution arm by overlaying changed skill folders on `-load-skills-from` |
| `-evaluation-seed` | Optional root seed used to derive paired per-task seeds across compare arms |
| `-evaluation-temperature` | Optional shared agent/reviewer sampling temperature for non-optimization modes |
| `-max-prompt-skills` | Cap the number of full skill summaries rendered into the prompt |
| `-enable-approval-gate` | Route reviewer output through the Phase A revision store + Phase B deterministic SpecGate / SafetyGate; writes immutable revisions and an audit log under `<output>/managed_skills_revisions/` |
| `-approval-gate-shadow` | Run the quality gate in shadow mode: still publish even when gates reject, for comparison only |
| `-optimization-seed-spec` | Seed `SkillSpec` JSON used by `-mode optimize` |
| `-optimization-candidate-spec` | Optional frozen candidate for paired A/B without reflective search |
| `-optimization-*-scales` | Disjoint feedback, validation, and holdout scale lists |
| `-optimization-critical-scales` | Optional holdout scales requiring per-case score non-regression |
| `-optimization-repeats` | Independent runs per task in every split |
| `-optimization-max-iterations` | Reflective mutation attempt limit |
| `-optimization-max-metric-calls` | Evaluated-case budget, including validation and holdout |
| `-optimization-evaluation-temperature` | Agent sampling temperature during optimization evaluation (default `0`) |

## Output Layout

The output directory contains:

- `results.json`: structured benchmark results (each task's
  `approvalGate` snapshot is recorded here when `-enable-approval-gate`
  is set).
- `REPORT.md`: readable single-run summary.
- `workspaces/`: per-task workspaces when `-keep-workspaces=true`.
- `managed_skills/`: learned skills produced during the `evolution` arm
  (this is what agents actually read).
- `optimized_managed_skills/`: isolated live library for the optional
  `optimized_evolution` arm.
- `managed_skills_revisions/`: Phase A revision store, only populated
  when `-enable-approval-gate` is set. Each SkillID gets
  `revisions/<revision-id>/{meta.json, SKILL.md}`, an append-only
  `audit.log`, and an `active.txt` pointer. Rolling back a skill is a
  one-line edit to `active.txt`.
- `selected_skill/`: validation-selected seed or candidate for `-mode optimize`;
  its presence does not imply that the holdout gate approved promotion.
- `optimization_candidates/`: content-addressed candidate snapshots evaluated
  by optimize mode.
- `optimization_experiments/`: optimizer lineage, paired seeds, feedback, and
  bounded traces used to audit selection and promotion decisions.

## Requirements

- A working SkillCraft checkout with `uv` dependencies available
- `uv` on `PATH`
- `npx` on `PATH` for `@modelcontextprotocol/server-filesystem`
- Network access for the public APIs used by the selected SkillCraft tasks
- Model credentials exposed as OpenAI-compatible environment variables
