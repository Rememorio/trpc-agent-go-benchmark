# Evaluating Agent Self-Evolution on the SkillCraft Benchmark

## 1. Introduction

This report documents the **full SkillCraft evaluation arc** for
`trpc-agent-go`'s self-evolution mechanism, rather than a single run in
isolation.

SkillCraft is a good fit for this question because each task family
ships multiple variants of the same workflow shape at increasing scale
(`e1` ... `e3`, `m1` ... `m2`, `h1`). If the agent can distill a
reusable skill on easier variants, later variants should become more
stable, cheaper, or both.

The central question is unchanged:

> **Does an agent that extracts `SKILL.md` files in the background and
> reuses them on later tasks actually do better than one that starts
> from scratch every time?**

What *has* changed is the answer's level of certainty. The earliest
single-run result looked strongly positive. Later controlled reruns
showed that the story is more nuanced: some gains are real, but
variance, reviewer quality, and runtime behavior all matter.

This report therefore treats the experiment as a sequence:

1. an early milestone run that established feasibility;
2. a later three-run controlled batch that became the main source of
   truth for the current runtime;
3. a stronger-reviewer spot check (`gpt-5.2`) that tests whether a
   better reviewer can clean up the skill library without changing the
   agent runtime.

## 2. Experimental Setup

### 2.1 Benchmark and task families

| Item | Value |
| --- | --- |
| Benchmark | SkillCraft |
| Task families | `openmeteo-weather`, `recipe-cookbook-builder`, `world-bank-economic-snapshot` |
| Variants per family | `e1` / `e2` / `e3` / `m1` / `m2` / `h1` |
| Total tasks per full run | 18 |
| Scoring | SkillCraft official `evaluation/main.py` |
| Execution mode | `compare` (`baseline` then `evolution`) |

The three families cover sequential API orchestration, structured
content generation, and multi-entity economic aggregation, so the
results are not dominated by a single workload shape.

### 2.2 Configurations

| Configuration | Description |
| --- | --- |
| **Baseline** | `evolution` disabled; every task starts from scratch |
| **Evolution** | `evolution` enabled; learned skills are stored as `SKILL.md` and made visible to later tasks |

Across the controlled runs, baseline and evolution share the same task
set, agent runtime, tools, prompts, and evaluator. The variable is
whether `evolution` is on and, in the latest spot check, whether the
reviewer model is upgraded.

### 2.3 Evolution in `trpc-agent-go`

`evolution` is an **asynchronous learning loop**. The main task path is
not blocked by learning; review happens after the session completes.

1. the runner enqueues a learning job with transcript and outcome;
2. a reviewer proposes `skills` / `updates` / `deletions`;
3. deterministic post-processing (`reconcile.go`) de-duplicates and
   rewrites obvious near-duplicates;
4. accepted skills are published to the managed skill directory and
   become visible to later runs.

The runtime also exposes skill summaries to the agent and allows
explicit loading through `skill_load`. A recurring theme in the
controlled experiments is that **skills are offered but explicit skill
loading still does not happen**.

### 2.4 Evidence sources

This report uses four primary artefacts:

- historical milestone run:
  [`multi_family_compare`](multi_family_compare)
- controlled three-run batch:
  [`full_compare_run1`](full_compare_run1),
  [`full_compare_run2`](full_compare_run2),
  [`full_compare_run3`](full_compare_run3)
- aggregated three-run analysis:
  [`tools/full_compare_analysis.json`](tools/full_compare_analysis.json)
- stronger-reviewer spot check:
  [`full_compare_reviewer_gpt52_run1`](full_compare_reviewer_gpt52_run1)

All numbers quoted below come from `results.json` files or the checked-in
aggregate analysis generated from them.

---

## 3. Experimental Progression

### 3.1 Phase A: early milestone run

The original milestone run,
[`multi_family_compare`](multi_family_compare), established that the
mechanism could work at all:

| Metric | Baseline | Evolution | Δ |
| --- | ---: | ---: | ---: |
| Pass rate | 83.33% | 100.00% | +16.67pp |
| Average score | 80.46 | 97.68 | +17.22 |
| Avg end-to-end tokens / task | 185,590.44 | 128,913.22 | -56,677.21 |
| Avg duration | 98.93s | 79.68s | -19.24s |
| Learned skills | – | 16 | – |

That result was important: it showed that asynchronous skill extraction
plus `SKILL.md` reuse could materially improve completion behavior on
SkillCraft.

### 3.2 Phase B: controlled three-run batch

Later, the runtime was tightened: managed-skill prompting was narrowed,
token tailoring was added, a frozen clean warm-start seed was used, and
the evaluation was rerun three times with the same full-18 setup:

- [`full_compare_run1`](full_compare_run1)
- [`full_compare_run2`](full_compare_run2)
- [`full_compare_run3`](full_compare_run3)

The aggregate result is more cautious:

| Metric | Baseline Mean | Evolution Mean | Δ |
| --- | ---: | ---: | ---: |
| Pass rate | 90.74% | 90.74% | 0.00pp |
| Pass-rate stddev | 8.49pp | 3.20pp | – |
| Avg end-to-end tokens / task | 169,888.61 | 145,980.13 | -23,908.48 |
| End-to-end token stddev | 81,007.55 | 24,363.25 | – |

This batch changed the interpretation of the experiment:

- the old "evolution clearly wins" headline was too strong;
- the current runtime is better described as **variance-reducing** than
  as uniformly pass-rate-improving;
- explicit `skill_load` remained at `0%`;
- the recurring failure cluster shifted toward
  `world-bank-economic-snapshot/e2` and local MCP timeouts.

### 3.3 Phase C: stronger-reviewer spot check (`gpt-5.2`)

The latest spot check keeps the agent runtime fixed and upgrades only
the reviewer model:

- [`full_compare_reviewer_gpt52_run1`](full_compare_reviewer_gpt52_run1)

Summary:

| Metric | Baseline | Evolution | Δ |
| --- | ---: | ---: | ---: |
| Pass rate | 100.00% | 100.00% | 0.00pp |
| Average score | 97.19 | 97.13 | -0.05 |
| Avg duration | 131.10s | 79.53s | -51.56s |
| Avg end-to-end tokens / task | 158,005.67 | 152,715.39 | -5,290.27 |
| Learned skills | – | 11 | – |
| `skill_load` invoked | 0.00% | 0.00% | 0.00pp |

The important point is not that evolution "won" this run on pass rate.
Baseline also went 18/18. The more useful signal is that the final
library stayed much cleaner: the evolution arm produced an 11-skill
library with no `Weather Monitor - 3/4/5 Cities with APIs` siblings.

---

## 4. Main Results

### 4.1 What is firmly established

Three conclusions are now on solid ground.

1. **Agent self-evolution can help on SkillCraft.** The early milestone
   run was not a fluke in the sense of "nothing useful is happening":
   learned skills can clearly eliminate some catastrophic loops.
2. **The current runtime does not yet prove stable pass-rate gains.**
   The controlled three-run batch averaged out to a pass-rate tie.
3. **Explicit skill reuse is still absent.** Across the controlled
   batch and the `gpt-5.2` spot check, skills were offered but
   `skill_load` was never invoked.

### 4.2 Where evolution helps today

The strongest evidence for practical value is still in catastrophic-loop
avoidance.

In the three-run controlled batch:

- `openmeteo-weather/e1` remained `3/3` pass in both arms, but
  baseline averaged `489,459` end-to-end tokens versus `80,644` for
  evolution because one baseline run exploded;
- `openmeteo-weather/e2` was `T,F,T` in baseline and `T,T,T` in
  evolution;
- evolution reduced variance materially even when mean pass rate tied.

The `gpt-5.2` spot check showed the same pattern on a single run:

- `openmeteo-weather/e1` improved by `-508,453` end-to-end tokens;
- `world-bank-economic-snapshot/e3` improved by `-159,046`;
- but some tasks regressed, most notably
  `recipe-cookbook-builder/h1` (`+348,835` end-to-end tokens).

So the benefit is real, but uneven.

### 4.3 What is still missing

The most important missing piece is still **real skill consumption**.

The current controlled evidence says:

- skills are visible in the prompt (`SkillsOffered = 100%`);
- final skill libraries are being produced;
- but `skill_load` remains unused.

This means the current gains come mostly from **better catalog exposure
and better reviewer outputs**, not from a mature progressive-disclosure
loop where the agent explicitly chooses, loads, and applies a skill.

### 4.4 Reviewer quality matters, but it does not solve everything

The `gpt-5.2` reviewer spot check is the clearest sign so far that
reviewer quality is part of the bottleneck:

- final skill count dropped from the recent 13–14 range to **11**;
- the count-specific weather API siblings disappeared in that run;
- end-to-end tokens stayed slightly below baseline while pass rate tied.

But even with the stronger reviewer:

- `skill_load` was still never called;
- pass rate did not improve over a strong baseline;
- the result is still only a **single run**, so it is evidence for
  better library cleanliness, not yet proof of a new overall headline.

---

## 5. Conclusions

The right way to summarize the full experiment today is:

1. **The original positive result was meaningful, but not sufficient as
   a final claim.** It showed that self-evolution can work.
2. **The controlled reruns are now the main source of truth for the
   current runtime.** Under that lens, evolution currently looks more
   like a stabilizer than a clear pass-rate booster.
3. **Reviewer quality appears to improve library cleanliness.** The
   `gpt-5.2` spot check is promising because it preserves a compact,
   generic 11-skill library.
4. **The project is not done.** The next decisive questions are still:
   why `skill_load` remains unused, and how to reduce the remaining
   timeout / long-loop failure modes.

In other words: the experiment has moved from "can this idea work at
all?" to "under what reviewer/runtime conditions does it become stable
enough to trust by default?"

---

## Appendix

### A. Current key artefacts

| Artefact | Role |
| --- | --- |
| [`multi_family_compare`](multi_family_compare) | Historical milestone run |
| [`full_compare_run1`](full_compare_run1) | Controlled batch, run 1 |
| [`full_compare_run2`](full_compare_run2) | Controlled batch, run 2 |
| [`full_compare_run3`](full_compare_run3) | Controlled batch, run 3 |
| [`tools/full_compare_analysis.json`](tools/full_compare_analysis.json) | Frozen three-run aggregate |
| [`full_compare_reviewer_gpt52_run1`](full_compare_reviewer_gpt52_run1) | Stronger-reviewer spot check |

### B. Reproducing the latest reviewer spot check

```bash
cd skillcraft/trpc-agent-go-impl

go run . \
  -skillcraft-root "$SKILLCRAFT_ROOT" \
  -tasks "openmeteo-weather/e1,openmeteo-weather/e2,openmeteo-weather/e3,openmeteo-weather/m1,openmeteo-weather/m2,openmeteo-weather/h1,recipe-cookbook-builder/e1,recipe-cookbook-builder/e2,recipe-cookbook-builder/e3,recipe-cookbook-builder/m1,recipe-cookbook-builder/m2,recipe-cookbook-builder/h1,world-bank-economic-snapshot/e1,world-bank-economic-snapshot/e2,world-bank-economic-snapshot/e3,world-bank-economic-snapshot/m1,world-bank-economic-snapshot/m2,world-bank-economic-snapshot/h1" \
  -mode compare \
  -model gpt-4o-mini \
  -reviewer-model gpt-5.2 \
  -max-tool-iterations 24 \
  -load-skills-from ../results/tools/clean_skill_seed \
  -max-prompt-skills 8 \
  -output ../results/full_compare_reviewer_gpt52_run1
```

### C. Current interpretation rule

If a new reader wants a single sentence to orient themselves:

- use [`multi_family_compare`](multi_family_compare) to understand why
  the idea was worth pursuing;
- use [`full_compare_run1`](full_compare_run1),
  [`full_compare_run2`](full_compare_run2),
  [`full_compare_run3`](full_compare_run3), and
  [`tools/full_compare_analysis.json`](tools/full_compare_analysis.json)
  to understand the current runtime truth;
- use [`full_compare_reviewer_gpt52_run1`](full_compare_reviewer_gpt52_run1)
  as a promising reviewer-quality spot check, not yet as a new final
  headline.
