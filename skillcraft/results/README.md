# Evaluation Results — SkillCraft

This directory stores the SkillCraft benchmark evaluation results for
[trpc-agent-go](https://github.com/trpc-group/trpc-agent-go)'s **agent
self-evolution** capability (the `evolution/` package). The question we
are answering: *does automatically learned `SKILL.md` actually help
downstream tasks?*

## Reports

| File | Description |
|------|-------------|
| [REPORT.md](REPORT.md) | Full evaluation report (English) |
| [REPORT.zh_CN.md](REPORT.zh_CN.md) | Full evaluation report (Chinese) |

## Headline Numbers

**Configuration**

- Agent / Reviewer model: `gpt-4o-mini`
- Task families: `openmeteo-weather`, `recipe-cookbook-builder`,
  `world-bank-economic-snapshot`
- Variants per family: `e1, e2, e3, m1, m2, h1` (6 each, 18 total)
- Max tool iterations: 16
- Scoring: SkillCraft's official `evaluation/main.py`

**Main results** (extracted by
[`tools/extract_metrics.py`](tools/extract_metrics.py) from
[`multi_family_compare/results.json`](multi_family_compare/results.json),
snapshot at [`tools/metrics.json`](tools/metrics.json))

| Scenario | Pass / Total | Pass Rate | Avg Score | Agent Tokens/Task | End-to-End Tokens/Task | Avg Duration |
|----------|---:|---:|---:|---:|---:|---:|
| Baseline (no skills) | 15 / 18 | 83.33% | 80.46 | 185,590 | 185,590 | 98.9s |
| **Evolution (skills reused)** | **18 / 18** | **100.00%** | **97.68** | **118,670** | **128,913** | **79.7s** |
| Δ (Evolution − Baseline) | +3 | **+16.67 pp** | **+17.22** | **−36.06%** | **−30.54%** | **−19.46%** |

Notes:

- `Agent Tokens` counts the main LLM's prompt + completion tokens for
  solving the task.
- `End-to-End Tokens` additionally includes the reviewer's LLM tokens
  in the `evolution` arm (10,243 tokens/task on average). Even paying
  for the reviewer, evolution uses **30.54% fewer total tokens** than
  baseline.
- Cold-start (the first task, no learned skill yet) and warm-start
  (the remaining 17) are both 100% pass; warm-start benefits compound
  as the skill repository grows. See §3.1 of the report.

**Per-family summary**

| Family | Baseline Pass | Baseline Score | Evolution Pass | Evolution Score |
|---|---:|---:|---:|---:|
| `openmeteo-weather` | 5/6 | 81.28 | 6/6 | 97.95 |
| `recipe-cookbook-builder` | 6/6 | 93.43 | 6/6 | 95.10 |
| `world-bank-economic-snapshot` | 4/6 | 66.67 | 6/6 | 100.00 |

## Why It Works

The evolution arm does **not** win by squeezing extra points on easy
tasks. It wins by **eliminating the catastrophic failure mode** that
plagues baseline.

Baseline fails 3 tasks — not by giving wrong answers but by burning
**308k / 350k / 714k tokens** in retry loops until
`max tool iterations`. Evolution finishes the same tasks in 85k–130k
tokens because the previously-learned `SKILL.md` spells out the call
order and pitfalls, keeping the agent off the loopy branch.

## Directory Layout

```
results/
|-- README.md                       # This file
|-- REPORT.md                       # Full report (EN)
|-- REPORT.zh_CN.md                 # Full report (ZH)
|-- tools/
|   |-- extract_metrics.py          # Pulls all metrics from results.json
|   +-- metrics.json                # Frozen extraction snapshot
+-- multi_family_compare/           # The run referenced by the reports
    |-- results.json                # Structured benchmark results
    |-- REPORT.md                   # Auto-generated single-run summary
    |-- managed_skills/             # 16 SKILL.md files learned in the run
    +-- workspaces/                 # Per-task workspaces with agent deliverables
```

## Reproducing the Numbers

```bash
export SKILLCRAFT_ROOT=/path/to/SkillCraft
export OPENAI_API_KEY=...

cd skillcraft/trpc-agent-go-impl

go run . \
  -skillcraft-root "$SKILLCRAFT_ROOT" \
  -tasks "openmeteo-weather/e1,openmeteo-weather/e2,openmeteo-weather/e3,openmeteo-weather/h1,openmeteo-weather/m1,openmeteo-weather/m2,recipe-cookbook-builder/e1,recipe-cookbook-builder/e2,recipe-cookbook-builder/e3,recipe-cookbook-builder/h1,recipe-cookbook-builder/m1,recipe-cookbook-builder/m2,world-bank-economic-snapshot/e1,world-bank-economic-snapshot/e2,world-bank-economic-snapshot/e3,world-bank-economic-snapshot/h1,world-bank-economic-snapshot/m1,world-bank-economic-snapshot/m2" \
  -mode compare \
  -model gpt-4o-mini \
  -reviewer-model gpt-4o-mini \
  -max-tool-iterations 16 \
  -output ../results/multi_family_compare
```

Then re-run the extraction:

```bash
python3 skillcraft/results/tools/extract_metrics.py \
    skillcraft/results/multi_family_compare           # JSON (default)

python3 skillcraft/results/tools/extract_metrics.py \
    skillcraft/results/multi_family_compare --format md
```

Requirements: `uv` on `PATH`, `npx` available for
`@modelcontextprotocol/server-filesystem`, `OPENAI_API_KEY` set, and a
working [SkillCraft](https://github.com/shiqichen17/SkillCraft)
checkout at `$SKILLCRAFT_ROOT`.

## Result Format

Each `results.json` follows this shape (fields actually populated by
the runner; see
[`tools/extract_metrics.py`](tools/extract_metrics.py) for what the
report cites):

```json
{
  "baseline": {
    "summary": {
      "tasks": 18,
      "passedTasks": 15,
      "passRate": 83.33,
      "averageScorePercent": 80.46,
      "averageTotalTokens": 185590.44,
      "averageEndToEndTokens": 185590.44,
      "claimDoneRate": 77.78
    },
    "cases": [ /* per-task records */ ]
  },
  "evolution": {
    "summary": {
      "tasks": 18,
      "passedTasks": 18,
      "passRate": 100.00,
      "averageScorePercent": 97.68,
      "averageTotalTokens": 118670.06,
      "averageReviewerTokens": 10243.17,
      "averageEndToEndTokens": 128913.22,
      "claimDoneRate": 100,
      "skillsGenerated": 16,
      "finalSkillNames": [ "..." ],
      "warmStart": { "tasks": 17, "passRate": 100, ... },
      "coldStart": { "tasks": 1,  "passRate": 100, ... }
    },
    "cases": [ /* per-task records */ ]
  }
}
```

Per-task records include `taskId`, `baseTask`, `status`
(`ok` / `partial` / `fail` / `agent_error`), `evaluation.passed`,
`evaluation.score.percent`, tool-call counts, prompt / completion /
total / reviewer / end-to-end token usage, `claimDoneCalled`, and the
final `feedback` string from SkillCraft's official evaluator.
