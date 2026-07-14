# Reflective Optimization Evidence

This directory contains the sanitized evidence package for the pure-Go
reflective skill optimizer evaluated on SkillCraft.

- [`REPORT.md`](REPORT.md): full English evaluation report.
- [`REPORT.zh_CN.md`](REPORT.zh_CN.md): 完整中文评测报告。
- [`evidence.json`](evidence.json): machine-readable configuration and aggregate
  measurements.
- [`recipe_candidate.json`](recipe_candidate.json): the validation-selected
  recipe candidate used in the frozen A/B check.

Raw model transcripts, task workspaces, endpoint configuration, and credentials
are intentionally excluded. The recorded result is a no-promotion decision: it
validates the search and holdout-gating mechanism, but does not claim that the
candidate improves task quality.
