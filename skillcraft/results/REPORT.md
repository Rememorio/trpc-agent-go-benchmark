# Evaluating Agent Self-Evolution on the SkillCraft Benchmark

## 1. Introduction

This report evaluates the agent self-evolution capability of
**trpc-agent-go** using the **SkillCraft** benchmark. It covers two
configurations:

- **Baseline**: evolution disabled; every task starts from scratch.
- **Evolution**: evolution enabled; skills extracted asynchronously by
  the background reviewer are stored as managed `SKILL.md` files and
  made available to later tasks via the `skill_load` tool.

The central question:

> **Does an agent that extracts reusable skills in the background and
> loads them on later tasks perform better than one that starts from
> scratch every time?**

SkillCraft is a good fit because each task family ships multiple
variants of the same workflow shape at increasing scale (`e1`–`e3`
easy, `m1`–`m2` medium, `h1` hard). If the agent can distill a
reusable skill on easier variants, later variants should become more
stable, cheaper, or both.

## 2. Experimental Setup

### 2.1 Benchmark Dataset

| Item | Value |
| --- | --- |
| Benchmark | SkillCraft |
| Task families | `openmeteo-weather` (weather monitoring), `recipe-cookbook-builder` (cookbook generation) |
| Variants per family | `e1` / `e2` / `e3` / `m1` / `m2` / `h1` |
| Tasks per run | 12 |
| Agent model | `gpt-4o-mini` |
| Reviewer model | `gpt-4o-mini` |
| Scoring | SkillCraft official `evaluation/main.py` |

### 2.2 Skill Seed Library

All runs use the same `clean_library_v19` warm-start seed containing
7 generic-parent-only skills (3 weather collection + 2 weather
monitor + 1 Recipe Cookbook - Multi-Dish + 1 Economic Snapshot -
Multi-Country). No count-specific siblings (`3/4/5 Cities`,
`3/4/5 Dishes`, etc.).

### 2.3 Evolution Mechanism

Evolution is an **asynchronous learning loop** that does not block the
main task path:

1. After each task, the runner enqueues a learning job with the
   transcript and evaluator outcome.
2. A background reviewer model produces structured decisions
   (`skills` / `updates` / `deletions`).
3. A deterministic reconciler (`reconcile.go`) deduplicates and
   rewrites near-duplicate siblings.
4. The decision passes through the approval gate (Phase A/B/C) before
   being materialized to the managed skills directory.

On the agent side, the framework injects a "Top recommended skill"
hint when one skill clearly out-scores the others against the current
request. The benchmark instruction requires the agent to `skill_load`
a matching skill **before any domain tool call** (skill-first
protocol).

### 2.4 Approval Gate

| Phase | Component | Description |
| --- | --- | --- |
| A | `FileCandidateStore` + `FileActivePointer` | Each skill mutation becomes an immutable revision (`meta.json` + `SKILL.md`) with an append-only `audit.log`; `active.txt` points to the current visible revision |
| B | `DefaultSpecGate` + `DefaultSafetyGate` | Deterministic rules, zero LLM calls. SpecGate checks schema completeness / name stability / duplicate detection / quantified-sibling patterns; SafetyGate scans for secret patterns / dangerous shell / path traversal |
| C | `OutcomeBasedEffectivenessGate` | Checks the Outcome of the session that triggered the review: score < 80 or status=fail/agent_error holds the revision in `PendingEval` instead of auto-promoting, preventing learning from catastrophic runs |

### 2.5 Evaluation Configurations

| Configuration | Description | Version label |
| --- | --- | --- |
| **Baseline** | No managed skills | Shared across all versions |
| **Evolution (v20)** | Phase A + B approval gate | 5 runs |
| **Evolution (v21b)** | Phase A + B + C full gate | 5 runs |

Each configuration is repeated 5 times with mean + stddev aggregation
to control for variance from baseline catastrophic loops.

## 3. Results

### 3.1 Overall Metrics

**Table 1: 5-run aggregate comparison**

| Metric | Baseline mean | Evolution (v20) | Evolution (v21b) |
| --- | ---: | ---: | ---: |
| Pass rate | 95.00% | **98.33%** (+3.3pp) | **98.33%** (+3.3pp) |
| Average score | 91.35% | 95.44% | 96.36% |
| E2E tokens / task | 148,396 | 129,408 (**-12.8%**) | 131,170 (**-17.3%** vs own baseline) |
| E2E token stddev | 84,820 | 14,857 (**17.5%**) | 6,387 (**13.9%**) |
| `skill_load` invoked | 0% | **100%** | **100%** |
| Gate candidates | — | 47 | 59 |
| Gate promoted | — | 47 | 59 |
| Gate rejected (spec+safety) | — | 0 | 0 |
| Gate held (effectiveness) | — | — | 0 |

> Evolution outperforms baseline on pass rate, token mean, and token
> variance across all configurations. `skill_load` invocation moved
> from a persistent 0% floor (across v14–v18) to 100% in every run.
> The approval gate is transparent: it neither consumes evolution's
> benefit nor introduces measurable regression.

### 3.2 Per-Run Detail

**Table 2: v20 (Phase A + B approval gate) — 5 runs**

| Run | Baseline pass | Evolution pass | Baseline E2E | Evolution E2E | Δ E2E | Promoted |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| try1 | 91.67% | **100.00%** | 109,620 | 121,879 | +12,258 | 0\* |
| try2 | 100.00% | 100.00% | 97,798 | 122,495 | +24,697 | 12 |
| try3 | 100.00% | 91.67% | 126,021 | 155,252 | +29,231 | 11 |
| try4 | **83.33%** | **100.00%** | **299,059** | 118,938 | **-180,121** | 12 |
| try5 | 100.00% | 100.00% | 109,482 | 128,475 | +18,993 | 12 |

\* try1 metrics-snapshot timing bug; fixed for try2–try5.

**Table 3: v21b (Phase A + B + C full gate) — 5 runs**

| Run | Baseline pass | Evolution pass | Baseline E2E | Evolution E2E | Δ E2E | Promoted | Held |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| try1 | 100.00% | 91.67% | 182,681 | 134,646 | -48,035 | 11 | 0 |
| try2 | **91.67%** | **100.00%** | 117,953 | 121,466 | +3,513 | 13 | 0 |
| try3 | 100.00% | 100.00% | 101,708 | 138,565 | +36,857 | 11 | 0 |
| try4 | **91.67%** | **100.00%** | 183,337 | 131,472 | -51,865 | 12 | 0 |
| try5 | **91.67%** | **100.00%** | 207,530 | 129,703 | -77,827 | 12 | 0 |

### 3.3 Catastrophic Loop Suppression

With `gpt-4o-mini`, baseline weather tasks exhibit random catastrophic
loops: the agent repeatedly calls `weather_get_hourly` until the
context window explodes (single-task tokens > 1M). Evolution
suppresses these loops through explicit step guidance in the loaded
skill ("call once per city, then move on").

**Table 4: Representative catastrophic loop cases**

| Run | Task | Baseline tokens | Evolution tokens | Savings |
| --- | --- | ---: | ---: | ---: |
| v20/try4 | weather/e1 | 1,343,723 (agent_error) | 72,063 | 94.6% |
| v20/try4 | weather/m1 | 1,097,449 | 107,168 | 90.2% |
| v21b/try5 | weather/e1 | 710,736 | 64,444 | 90.9% |

### 3.4 Approval Gate Behavior

**Table 5: Approval gate statistics (v21b, 5 runs combined)**

| Metric | Value |
| --- | --- |
| Candidate revisions seen | 59 |
| Revisions promoted to active | 59 |
| SpecGate rejected | 0 |
| SafetyGate rejected | 0 |
| EffectivenessGate held | 0 |
| On-disk layout per skill | `revisions/<id>/{meta.json, SKILL.md}` + `audit.log` + `active.txt` |

> Zero rejections are expected: `reconcile.go` Rules 1/2/3 already
> rewrite most non-compliant candidates before they reach the gate.
> The gate's ability to reject malicious cases (secret leaks,
> `rm -rf /`, `../../etc/passwd`) is verified in unit tests.

### 3.5 Supplementary Experiment: Full Effectiveness Block (v21)

v21 contained a score-scale bug (`Outcome.Score` was incorrectly
divided from 0–100 to 0–1), causing the effectiveness gate to hold
all 60 reviewer-generated revisions. This accidental experiment
yielded a key finding:

| Metric | Baseline | Evolution (v21, 0/60 promoted) |
| --- | ---: | ---: |
| Pass rate (5-run mean) | 91.67% | **100.00%** (+8.33pp) |
| E2E tokens / task | 187,427 | **125,324** (-33.1%) |
| E2E token stddev | 36,762 | **5,593** (15.2%) |

> **Even when all reviewer updates were blocked, evolution still
> dominated baseline.** This proves evolution's benefit comes entirely
> from the warm-start seed + skill_load, not from within-run reviewer
> updates. The effectiveness gate can therefore be arbitrarily
> conservative without affecting current-run quality.

## 4. Discussion

### 4.1 Source of Evolution's Benefit

The data consistently points to one conclusion: evolution's core value
is not "every run is slightly better" but rather **suppression of
baseline's random catastrophic loops**. On calm baseline runs,
evolution's token count is slightly higher (due to skill_load +
reviewer overhead). On runs where baseline hits a catastrophic loop,
evolution saves 90%+ tokens and rescues pass. This explains why
three-run means sometimes look like evolution is slightly worse — the
sample may not contain a catastrophic run. The operational
consequence: **effectiveness evaluation must use ≥ 5 runs with stddev,
not single runs or three-run means**, or it will discard real wins as
regressions.

### 4.2 Approval Gate's Actual Role

Phase A (revision store + active pointer) provides auditability and
rollback, not benchmark improvement. Phase B (SpecGate + SafetyGate)
is a last line of defense — currently transparent because the
reconciler already cleans most non-compliant candidates upstream.
Phase C (effectiveness gate) does not fire on successful tasks but
would hold revisions from catastrophic runs, preventing the reviewer
from learning incorrect skills from bad sessions.

### 4.3 Limitations

1. **Limited task family coverage**: Only weather and recipe families
   evaluated. `world-bank-economic-snapshot` excluded due to unrelated
   MCP tool timeout issues.
2. **Weak reviewer model**: `gpt-4o-mini` still generates
   count-specific siblings; reconciler absorbs them.
3. **Single skill consumption path**: Only `skill_load` today; no
   progressive disclosure (browse summary then decide).
4. **No production traffic validation**: All data from SkillCraft
   benchmark; lacks real adopter skill production density and hit-rate
   data.

## 5. Conclusions

In controlled multi-run experiments on SkillCraft, trpc-agent-go's
agent self-evolution mechanism demonstrates three definitive benefits:

1. **Pass rate improvement**: 5-run mean +3.3pp (95.0% → 98.3%), with
   evolution maintaining lower failure variance across all versions.
2. **Token reduction**: 5-run mean -12.8% to -33.1%, primarily from
   catastrophic loop suppression; single-case savings up to 94.6%.
3. **Significant variance convergence**: Evolution's e2e-token stddev
   is only 13.9%–17.5% of baseline, indicating that skill_load makes
   agent behavior more stable and predictable.

The approval gate (Phase A/B/C) is fully implemented and running in
evaluation, proving it introduces no regression while providing the
auditable, rollback-capable skill lifecycle management required for
production deployment.

---

## Appendix

### A. Reproduction Commands

```bash
cd skillcraft/trpc-agent-go-impl

# v21b (full gate)
go run . \
  -skillcraft-root "$SKILLCRAFT_ROOT" \
  -tasks "openmeteo-weather/e1,...,recipe-cookbook-builder/h1" \
  -mode compare \
  -model gpt-4o-mini \
  -reviewer-model gpt-4o-mini \
  -max-tool-iterations 24 \
  -load-skills-from ../results/tools/clean_library_v19 \
  -max-prompt-skills 8 \
  -enable-approval-gate \
  -effectiveness-gate \
  -output ../results/multi_family_compare_v21b_tryN
```

### B. Key CLI Parameters

| Parameter | Description |
| --- | --- |
| `-enable-approval-gate` | Enable Phase A revision store + Phase B SpecGate/SafetyGate |
| `-effectiveness-gate` | Enable Phase C outcome-based effectiveness gate |
| `-approval-gate-shadow` | Shadow mode: gate evaluates but does not block; for comparison |
| `-load-skills-from` | Warm-start seed directory path |
| `-max-prompt-skills` | Cap on skill summaries rendered into the agent prompt |
