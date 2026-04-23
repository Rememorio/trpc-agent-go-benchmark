# SkillCraft Benchmark for trpc-agent-go

This benchmark runs local [SkillCraft](https://github.com/shiqichen17/SkillCraft)
tasks with `trpc-agent-go` and compares:

- `baseline`: no learned skills.
- `evolution`: reuse skills extracted asynchronously by the `evolution`
  reviewer between tasks.
- `compare`: run `baseline` + `evolution` back-to-back and report deltas.

`trpc-agent-go` intentionally keeps skill management on the
reviewer-driven async path. The old in-flow `skill_manage` experiment
was removed after early runs showed prompt overhead without reliable
tool usage.

## Latest Snapshot

The current source of truth is the latest **three-run full compare
batch**:

- [`results/full_compare_run1`](results/full_compare_run1)
- [`results/full_compare_run2`](results/full_compare_run2)
- [`results/full_compare_run3`](results/full_compare_run3)
- Aggregate JSON:
  [`results/tools/full_compare_analysis.json`](results/tools/full_compare_analysis.json)
- Aggregate script:
  [`results/tools/aggregate_runs.py`](results/tools/aggregate_runs.py)

Across these three runs:

- Baseline mean pass rate: `90.74%` with `8.49pp` stddev.
- Evolution mean pass rate: `90.74%` with `3.20pp` stddev.
- Baseline mean end-to-end tokens/task: `169,888.61`.
- Evolution mean end-to-end tokens/task: `145,980.13`.
- Mean end-to-end delta: `-23,908.48` tokens/task, but with high
  variance because one baseline run hit catastrophic weather loops.
- In all three runs, evolution had `SkillsOffered = 100%` and
  `skill_load` invocation rate `0%`.

The practical interpretation is:

- the old weather failure is no longer a stable deterministic
  evolution-only bug;
- the agent still does **not** explicitly load skills;
- the most reproducible failure cluster has shifted toward
  `world-bank-economic-snapshot`, where local MCP tools occasionally
  time out.

See [`results/REPORT.md`](results/REPORT.md) for the detailed write-up.

## Reproducing The Current Batch

From `skillcraft/trpc-agent-go-impl`:

```bash
go run . \
  -skillcraft-root "$SKILLCRAFT_ROOT" \
  -tasks "openmeteo-weather/e1,openmeteo-weather/e2,openmeteo-weather/e3,openmeteo-weather/m1,openmeteo-weather/m2,openmeteo-weather/h1,recipe-cookbook-builder/e1,recipe-cookbook-builder/e2,recipe-cookbook-builder/e3,recipe-cookbook-builder/m1,recipe-cookbook-builder/m2,recipe-cookbook-builder/h1,world-bank-economic-snapshot/e1,world-bank-economic-snapshot/e2,world-bank-economic-snapshot/e3,world-bank-economic-snapshot/m1,world-bank-economic-snapshot/m2,world-bank-economic-snapshot/h1" \
  -mode compare \
  -model gpt-4o-mini \
  -reviewer-model gpt-4o-mini \
  -max-tool-iterations 24 \
  -load-skills-from ../results/tools/clean_skill_seed \
  -max-prompt-skills 8 \
  -output ../results/full_compare_runX
```

Run that command three times with different `runX` output directories,
then aggregate:

```bash
python3 ../results/tools/aggregate_runs.py \
  ../results/full_compare_run1 \
  ../results/full_compare_run2 \
  ../results/full_compare_run3 \
  > ../results/tools/full_compare_analysis.json
```

The frozen warm-start seed used by these runs lives at
[`results/tools/clean_skill_seed`](results/tools/clean_skill_seed).

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

## Key Flags

| Flag | Description |
|------|-------------|
| `-skillcraft-root` | Local SkillCraft checkout path |
| `-tasks` | Explicit task directories, comma-separated |
| `-base-task` | Base task family, e.g. `cat-facts-collector` |
| `-scales` | Scale list for `-base-task`, e.g. `e1,e2,e3` |
| `-mode` | `baseline`, `evolution`, or `compare` |
| `-model` | Agent model |
| `-reviewer-model` | Evolution reviewer model |
| `-output` | Result directory |
| `-task-timeout-seconds` | Override task timeout |
| `-max-tool-iterations` | Max tool loops per task |
| `-load-skills-from` | Warm-start the evolution arm from an existing managed-skill directory |
| `-max-prompt-skills` | Cap the number of full skill summaries rendered into the prompt |

## Output Layout

The output directory contains:

- `results.json`: structured benchmark results
- `REPORT.md`: readable single-run summary
- `workspaces/`: per-task workspaces when `-keep-workspaces=true`
- `managed_skills/`: learned skills produced during the `evolution` arm

## Requirements

- A working SkillCraft checkout with `uv` dependencies available
- `uv` on `PATH`
- `npx` on `PATH` for `@modelcontextprotocol/server-filesystem`
- Network access for the public APIs used by the selected SkillCraft tasks
- Model credentials exposed as OpenAI-compatible environment variables
