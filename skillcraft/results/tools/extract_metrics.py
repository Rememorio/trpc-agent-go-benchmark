#!/usr/bin/env python3
"""Extract reproducible metrics from a SkillCraft benchmark results.json.

Usage:
    python3 extract_metrics.py <run_dir> [--format json|md]

Where <run_dir> is a directory produced by the benchmark runner that
contains both:
    results.json
    managed_skills/   (only present in runs that exercised `evolution`)

With --format json (default) the script emits a single JSON document
with every number the report cites. With --format md the script emits
the markdown table snippets used by REPORT.md / REPORT.zh_CN.md so the
reports never depend on hand transcription.
"""

from __future__ import annotations

import json
import os
import sys
from collections import defaultdict
from typing import Any


def _passed(case: dict[str, Any]) -> bool:
    return bool(((case.get("evaluation") or {}).get("passed")))


def _score(case: dict[str, Any]) -> float:
    return float(
        ((case.get("evaluation") or {}).get("score") or {}).get("percent", 0.0)
    )


def _agent_tokens(case: dict[str, Any]) -> int:
    return int(case.get("totalTokens") or 0)


def _e2e_tokens(case: dict[str, Any]) -> int:
    return int(case.get("endToEndTotalTokens") or 0)


def summarise_arm(arm: dict[str, Any]) -> dict[str, Any]:
    summary = arm.get("summary") or {}
    cases = arm.get("cases") or []
    by_family: dict[str, dict[str, list]] = defaultdict(
        lambda: {"pass": 0, "n": 0, "scores": [], "agent": []}
    )
    per_task: list[dict[str, Any]] = []
    catastrophic: list[dict[str, Any]] = []

    for case in cases:
        fam = case.get("baseTask") or "unknown"
        passed = _passed(case)
        score = _score(case)
        agent = _agent_tokens(case)
        e2e = _e2e_tokens(case)

        by_family[fam]["n"] += 1
        if passed:
            by_family[fam]["pass"] += 1
        by_family[fam]["scores"].append(score)
        by_family[fam]["agent"].append(agent)

        per_task.append(
            {
                "taskId": case.get("taskId"),
                "baseTask": fam,
                "passed": passed,
                "scorePercent": round(score, 2),
                "agentTokens": agent,
                "endToEndTokens": e2e,
            }
        )
        if agent > 300_000:
            catastrophic.append(
                {
                    "taskId": case.get("taskId"),
                    "agentTokens": agent,
                    "passed": passed,
                }
            )

    family_summary = {}
    for fam, agg in by_family.items():
        family_summary[fam] = {
            "tasks": agg["n"],
            "passed": agg["pass"],
            "passRate": round(agg["pass"] / agg["n"] * 100, 2),
            "avgScorePercent": round(sum(agg["scores"]) / agg["n"], 2),
            "avgAgentTokens": round(sum(agg["agent"]) / agg["n"]),
        }

    return {
        "tasks": summary.get("tasks"),
        "passedTasks": summary.get("passedTasks"),
        "passRate": summary.get("passRate"),
        "averageScorePercent": summary.get("averageScorePercent"),
        "averageDurationSeconds": summary.get("averageDurationSeconds"),
        "averageTotalTokens": summary.get("averageTotalTokens"),
        "averageReviewerTokens": summary.get("averageReviewerTokens"),
        "averageEndToEndTokens": summary.get("averageEndToEndTokens"),
        "claimDoneRate": summary.get("claimDoneRate"),
        "agentErrorCount": summary.get("agentErrorCount"),
        "skillsGenerated": summary.get("skillsGenerated"),
        "finalSkillNames": summary.get("finalSkillNames"),
        "warmStart": summary.get("warmStart"),
        "coldStart": summary.get("coldStart"),
        "byFamily": family_summary,
        "perTask": per_task,
        "catastrophic": catastrophic,
    }


def compute_delta(base: dict[str, Any], evo: dict[str, Any]) -> dict[str, Any]:
    def pct_change(new: float, old: float) -> float:
        if not old:
            return 0.0
        return round((new - old) / old * 100, 2)

    return {
        "passDelta": evo["passedTasks"] - base["passedTasks"],
        "passRatePPDelta": round(evo["passRate"] - base["passRate"], 2),
        "scorePPDelta": round(
            evo["averageScorePercent"] - base["averageScorePercent"], 2
        ),
        "agentTokensPctChange": pct_change(
            evo["averageTotalTokens"], base["averageTotalTokens"]
        ),
        "endToEndTokensPctChange": pct_change(
            evo["averageEndToEndTokens"], base["averageEndToEndTokens"]
        ),
        "durationPctChange": pct_change(
            evo["averageDurationSeconds"], base["averageDurationSeconds"]
        ),
        "claimDoneRatePPDelta": round(
            evo["claimDoneRate"] - base["claimDoneRate"], 2
        ),
    }


def count_skill_dirs(run_dir: str) -> int:
    skills_dir = os.path.join(run_dir, "managed_skills")
    if not os.path.isdir(skills_dir):
        return 0
    return sum(
        1
        for name in os.listdir(skills_dir)
        if os.path.isdir(os.path.join(skills_dir, name))
    )


def render_overall_table(base: dict, evo: dict, delta: dict) -> str:
    """Emit Table 1 (Baseline vs Evolution overall metrics)."""
    lines = [
        "| Scenario | Pass / Total | Pass Rate | Avg Score | Agent Tokens/Task | Reviewer Tokens/Task | End-to-End Tokens/Task | Avg Duration | Claim-done Rate |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
        f"| Baseline | {base['passedTasks']} / {base['tasks']} | {base['passRate']:.2f}% | {base['averageScorePercent']:.2f} | {base['averageTotalTokens']:,.0f} | – | {base['averageEndToEndTokens']:,.0f} | {base['averageDurationSeconds']:.1f}s | {base['claimDoneRate']:.2f}% |",
        f"| **Evolution** | **{evo['passedTasks']} / {evo['tasks']}** | **{evo['passRate']:.2f}%** | **{evo['averageScorePercent']:.2f}** | **{evo['averageTotalTokens']:,.0f}** | {evo['averageReviewerTokens']:,.0f} | **{evo['averageEndToEndTokens']:,.0f}** | **{evo['averageDurationSeconds']:.1f}s** | **{evo['claimDoneRate']:.2f}%** |",
        f"| **Δ (Evo − Base)** | **+{delta['passDelta']}** | **+{delta['passRatePPDelta']:.2f}pp** | **+{delta['scorePPDelta']:.2f}** | **{delta['agentTokensPctChange']:+.2f}%** | – | **{delta['endToEndTokensPctChange']:+.2f}%** | **{delta['durationPctChange']:+.2f}%** | **+{delta['claimDoneRatePPDelta']:.2f}pp** |",
    ]
    return "\n".join(lines)


def render_phase_table(evo: dict) -> str:
    """Emit Table 2 (warm-start / cold-start)."""
    cold = evo["coldStart"]
    warm = evo["warmStart"]
    lines = [
        "| Phase | Tasks | Pass Rate | Avg Score | End-to-End Tokens/Task | Avg Duration |",
        "| --- | ---: | ---: | ---: | ---: | ---: |",
        f"| Cold-start (task #1) | {cold['tasks']} | {cold['passRate']:.2f}% | {cold['averageScorePercent']:.2f} | {cold['averageEndToEndTokens']:,.0f} | {cold['averageDurationSeconds']:.1f}s |",
        f"| Warm-start (remaining {warm['tasks']}) | {warm['tasks']} | {warm['passRate']:.2f}% | {warm['averageScorePercent']:.2f} | {warm['averageEndToEndTokens']:,.0f} | {warm['averageDurationSeconds']:.1f}s |",
    ]
    return "\n".join(lines)


def render_family_table(base: dict, evo: dict) -> str:
    """Emit Table 3 (per-family breakdown)."""
    lines = [
        "| Family | Tasks | Baseline Pass | Baseline Avg Score | Baseline Avg Agent Tokens | Evolution Pass | Evolution Avg Score | Evolution Avg Agent Tokens |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for fam in sorted(base["byFamily"]):
        b = base["byFamily"][fam]
        e = evo["byFamily"][fam]
        lines.append(
            f"| `{fam}` | {b['tasks']} | {b['passed']}/{b['tasks']} | "
            f"{b['avgScorePercent']:.2f} | {b['avgAgentTokens']:,} | "
            f"**{e['passed']}/{e['tasks']}** | **{e['avgScorePercent']:.2f}** | "
            f"**{e['avgAgentTokens']:,}** |"
        )
    return "\n".join(lines)


def render_per_task_table(base: dict, evo: dict) -> str:
    """Emit Table 4 (per-task comparison) in deterministic order."""
    lines = [
        "| Task | B Pass | B Score | B Tokens | E Pass | E Score | E Tokens |",
        "| --- | :---: | ---: | ---: | :---: | ---: | ---: |",
    ]
    base_by_id = {t["taskId"]: t for t in base["perTask"]}
    evo_by_id = {t["taskId"]: t for t in evo["perTask"]}
    for tid in sorted(base_by_id):
        b = base_by_id[tid]
        e = evo_by_id[tid]
        b_mark = "✓" if b["passed"] else "×"
        e_mark = "✓" if e["passed"] else "×"
        lines.append(
            f"| {tid} | {b_mark} | {b['scorePercent']:.1f} | {b['agentTokens']:,} | "
            f"{e_mark} | {e['scorePercent']:.1f} | {e['agentTokens']:,} |"
        )
    return "\n".join(lines)


def render_skills_block(evo: dict) -> str:
    names = evo.get("finalSkillNames") or []
    return "\n".join(names)


def render_markdown(base: dict, evo: dict, delta: dict) -> str:
    return "\n\n".join(
        [
            "## Table 1 — Overall metrics",
            render_overall_table(base, evo, delta),
            "## Table 2 — Warm-start vs Cold-start (Evolution)",
            render_phase_table(evo),
            "## Table 3 — Per task family",
            render_family_table(base, evo),
            "## Table 4 — Per task",
            render_per_task_table(base, evo),
            "## Skills produced",
            "```",
            render_skills_block(evo),
            "```",
        ]
    )


def main() -> int:
    args = sys.argv[1:]
    fmt = "json"
    if "--format" in args:
        i = args.index("--format")
        fmt = args[i + 1]
        del args[i : i + 2]
    if len(args) != 1:
        print(__doc__, file=sys.stderr)
        return 2

    run_dir = args[0]
    results_path = os.path.join(run_dir, "results.json")
    with open(results_path, encoding="utf-8") as fh:
        data = json.load(fh)

    base = summarise_arm(data["baseline"])
    evo = summarise_arm(data["evolution"])
    delta = compute_delta(base, evo)

    if fmt == "md":
        sys.stdout.write(render_markdown(base, evo, delta) + "\n")
        return 0

    out = {
        "runDir": run_dir,
        "skillDirsOnDisk": count_skill_dirs(run_dir),
        "baseline": base,
        "evolution": evo,
        "delta": delta,
    }
    json.dump(out, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
