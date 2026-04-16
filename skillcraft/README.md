# SkillCraft Benchmark for trpc-agent-go

This benchmark runs local [SkillCraft](https://github.com/shiqichen17/SkillCraft) tasks with `trpc-agent-go` and compares:

- `baseline`: no learned skills
- `evolution`: reuse skills extracted by the `evolution` service between tasks
- `compare`: run both and report the delta

## Quick Start

```bash
export SKILLCRAFT_ROOT=/path/to/SkillCraft
export OPENAI_API_KEY=your-api-key

cd skillcraft/trpc-agent-go-impl

go run . \
  -skillcraft-root "$SKILLCRAFT_ROOT" \
  -base-task cat-facts-collector \
  -scales e1,e2,e3 \
  -mode compare \
  -model gpt-4o-mini \
  -reviewer-model gpt-4o-mini \
  -output ../results/catfacts_compare
```

## What It Does

For each selected SkillCraft task, the runner:

1. Creates a clean task workspace and copies `initial_workspace/` if present.
2. Exposes SkillCraft local Python tools through a small MCP stdio bridge.
3. Exposes workspace file operations through the standard filesystem MCP server.
4. Runs the task with `trpc-agent-go`.
5. Invokes SkillCraft's native `evaluation/main.py` to get the official score JSON.
6. In `evolution` mode, stores learned `SKILL.md` files and makes them available to later tasks.

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

## Output Layout

The output directory contains:

- `results.json`: structured benchmark results
- `REPORT.md`: readable summary
- `workspaces/`: per-task workspaces when `-keep-workspaces=true`
- `managed_skills/`: learned skills produced during `evolution` runs

## Requirements

- A working SkillCraft checkout with `uv` dependencies available
- `uv` on `PATH`
- `npx` on `PATH` for `@modelcontextprotocol/server-filesystem`
- Network access for the public APIs used by the selected SkillCraft tasks
- Model credentials such as `OPENAI_API_KEY`
