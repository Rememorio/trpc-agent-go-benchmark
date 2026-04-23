# Evaluating Agent Self-Evolution on SkillCraft — Three-Run Full Compare Update

## 1. Scope

This report replaces the old single-run headline with the latest
**three-run full compare batch**:

- [`full_compare_run1`](full_compare_run1)
- [`full_compare_run2`](full_compare_run2)
- [`full_compare_run3`](full_compare_run3)

All numbers below were derived from those three `results.json` files by
[`tools/aggregate_runs.py`](tools/aggregate_runs.py), with the frozen
output checked in at
[`tools/full_compare_analysis.json`](tools/full_compare_analysis.json).

Configuration:

- Agent model: `gpt-4o-mini`
- Reviewer model: `gpt-4o-mini`
- Tasks: `openmeteo-weather`, `recipe-cookbook-builder`,
  `world-bank-economic-snapshot`
- Task count per run: `18`
- Max tool iterations: `24`
- Warm-start seed:
  [`tools/clean_skill_seed`](tools/clean_skill_seed)
- Prompt overview cap: `8`

## 2. Per-Run Results

| Run | Baseline Pass | Evolution Pass | Pass Δ | Baseline E2E Tokens | Evolution E2E Tokens | E2E Δ |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Run 1 | 15 / 18 | 16 / 18 | +1 | 130,308.06 | 138,603.72 | +8,295.66 |
| Run 2 | 16 / 18 | 16 / 18 | 0 | 116,280.94 | 126,157.50 | +9,876.56 |
| Run 3 | 18 / 18 | 17 / 18 | -1 | 263,076.83 | 173,179.17 | -89,897.66 |

The important point is not any single row; it is the variance:

- Run 1 says evolution helped slightly on pass rate but cost more
  end-to-end tokens.
- Run 2 says evolution was pass-neutral and still cost more
  end-to-end tokens.
- Run 3 says evolution lost one pass but saved a huge amount of tokens
  because baseline hit catastrophic weather loops.

That is exactly why the single-run claim was no longer trustworthy.

## 3. Aggregate Metrics

| Metric | Baseline | Evolution | Δ (Evolution − Baseline) |
| --- | ---: | ---: | ---: |
| Mean pass rate | 90.74% | 90.74% | 0.00pp |
| Pass-rate stddev | 8.49pp | 3.20pp | - |
| Mean end-to-end tokens/task | 169,888.61 | 145,980.13 | -23,908.48 |
| End-to-end token stddev | 81,007.55 | 24,363.25 | 57,153.77 |
| Mean agent tokens/task | 169,888.61 | 131,990.93 | -37,897.68 |
| Mean duration delta | - | - | +21.14s |
| Mean claim-done delta | - | - | 0.00pp |

Interpretation:

1. **Pass rate is now a wash.** Across three full runs, evolution and
   baseline have the same mean pass rate.
2. **Variance is smaller under evolution.** Baseline swings from `15/18`
   to `18/18`; evolution stays in the `16/18` to `17/18` band.
3. **Mean token savings exist, but they are conditional.** The mean
   end-to-end reduction comes mostly from Run 3, where baseline
   exploded on weather tasks. In the other two runs, evolution still
   cost more end-to-end tokens.

## 4. What Changed In The Latest Runtime

Relative to the older single-run discussion, the latest runtime has
already changed the shape of the problem:

- reviewer JSON parsing and secret redaction no longer show up as live
  blockers;
- the benchmark now warm-starts from a frozen clean 11-skill seed;
- the reconciler already contains quantified-sibling ->
  generic-parent rewrites, and the logs show that path firing;
- the weather failure mode is no longer a stable evolution-only OOM.

This means the docs and the internal plan should no longer talk as if
the quantified-sibling rewrite were still hypothetical or as if
`openmeteo-weather/e1` always explodes under evolution.

## 5. Focus Tasks: `e1`, `e2`, `m1`

The current plan explicitly keeps tracing
`openmeteo-weather/e1,e2,m1`. The aggregate script confirms:

| Task | Baseline Passes | Evolution Passes | Baseline Mean E2E | Evolution Mean E2E | Baseline Mean Hourly Calls | Evolution Mean Hourly Calls |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| `e1` | `T,T,T` | `T,T,T` | 489,459.00 | 80,643.67 | 21.00 | 4.00 |
| `e2` | `T,F,T` | `T,T,T` | 514,047.67 | 189,678.00 | 19.00 | 7.00 |
| `m1` | `T,T,T` | `T,T,T` | 107,458.33 | 215,112.33 | 4.00 | 5.33 |

Two concrete conclusions follow.

### 5.1 `skill_load` is still not the reason these tasks improved

Across all three runs, every one of these focus tasks had:

- `hadAvailableSkills = true` in the evolution arm;
- `skillToolInvoked = false`;
- `loadedSkillNames = []`.

So the current gain is still coming from the **overview itself**,
not from explicit progressive disclosure.

### 5.2 The weather loop became stochastic, not solved

`e1` and `e2` show two different regimes:

- In Run 1 and Run 2, both arms stayed near the normal tool path.
- In Run 3, baseline blew up to `1,324,132` and `1,407,941`
  end-to-end tokens on `e1/e2`, with `62` tool calls and `54/51`
  hourly calls.
- Evolution also became more expensive in Run 3, but stayed far below
  baseline: `103,163` on `e1`, `388,807` on `e2`.

So the old "evolution causes the weather loop" story is now too coarse.
The better statement is:

- the loop is **highly stochastic**;
- evolution can damp it on `e1/e2`;
- but evolution still adds cost on `m1`, where baseline already behaves.

## 6. Current Failure Cluster

The recurring failure cluster is now concentrated in
`world-bank-economic-snapshot`, especially `e2`:

- `evolution` fails `world-bank-economic-snapshot/e2` in **all three**
  runs;
- `evolution` fails `world-bank-economic-snapshot/e3` in Run 1 and
  Run 2 but not Run 3;
- `baseline` failures drift across weather, recipe, and world-bank and
  do not pin to a single task.

The Run 3 log shows the likely reason: repeated 60-second MCP tool
timeouts on `worldbank_economic_snapshot`, `worldbank_gdp`, and
`worldbank_population`. Importantly, the agent can sometimes recover
from those timeouts (`m1` and `h1` still pass), so the issue is not a
hard deterministic evaluator bug; it is a runtime stability issue on
the local World Bank tool path.

## 7. Skill Library Behavior

Final evolution library sizes across the three runs were:

- Run 1: `14`
- Run 2: `13`
- Run 3: `14`

The resulting library is much more stable than the old expansion
pattern, but it is still not fully generic:

- count-specific `Weather Monitor - 3/4/5 Cities with APIs` skills
  still survive when no matching generic API parent exists in the seed;
- when a generic parent *does* exist, the reconciler logs the
  quantified-sibling rewrite and collapses the candidate into an
  update against that parent.

That means the current reviewer/reconciler stack is already stronger
than the earlier internal plan claimed, but it still depends on the
shape of the seed library.

## 8. Bottom Line

The latest three-run batch changes the evaluation story substantially:

1. The right headline is no longer "evolution clearly wins" or
   "evolution clearly loses". On pass rate, it currently averages out
   to a tie.
2. Evolution still does **not** demonstrate explicit skill reuse,
   because `skill_load` was never called.
3. The old weather loop is no longer the sole or even the main story.
   The benchmark's dominant recurring failure point has shifted toward
   `world-bank-economic-snapshot/e2` plus local MCP tool timeouts.
4. The most promising next runtime questions are therefore:
   "how do we get real skill loading?" and
   "how do we make the world-bank tool path less timeout-prone?"
