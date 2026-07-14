# Reflective Optimization Evidence

This directory contains the sanitized SkillCraft evidence package for the
pure-Go reflective optimizer.

- [`REPORT.md`](REPORT.md): full English evaluation report.
- [`REPORT.zh_CN.md`](REPORT.zh_CN.md): 完整中文评测报告。
- [`evidence.json`](evidence.json): machine-readable protocol and measurements.
- [`recipe_candidate.json`](recipe_candidate.json): frozen candidate used in
  the final paired comparisons.

The candidate starts from an existing reviewer-generated session skill. Across
two independent frozen comparisons (8 holdout pairs), it produced 4 quality
wins, 4 ties, no losses, and no pass-rate regressions. Pooled holdout quality
improved from 95.50% to 98.35%, while agent tokens decreased 6.57%.

Raw model transcripts, task workspaces, endpoint configuration, and credentials
are intentionally excluded. The reports distinguish untouched scale holdout
from hard-case regression runs and do not claim universal improvement.
