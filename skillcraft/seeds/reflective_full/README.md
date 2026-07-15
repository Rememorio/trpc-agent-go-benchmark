# Five-Family Reflective Optimization Seeds

This directory contains the controlled legacy inputs for the five-family
reflective optimizer experiment.

Four seeds are immutable revision documents produced by the existing online
`evolution` reviewer and approval-gate pipeline while replaying `e1,m1` tasks:

- `cat_revision.json`
- `pokemon_revision.json`
- `recipe_revision.json`
- `weather_revision.json`

Each document retains the reviewer source, revision identity, gate reports,
timestamp, and exact `SkillSpec`. Optimize mode accepts either a direct
`SkillSpec` JSON document or one of these revision documents, so no lossy
`SKILL.md` parser or manual prompt rewrite sits between online evolution and
offline optimization.

`worldbank_legacy.json` is the exact structured equivalent of the generic
World Bank skill already present in
[`multi_family_compare`](../../results/multi_family_compare/managed_skills/create-economic-snapshots-for-multiple-countries/SKILL.md).
That historical run predated immutable revision storage, so it has no revision
envelope.

`legacy_skills/` contains the same five inputs in the filesystem repository
format consumed by the three-arm compare runner. Search outputs are not written
here: selected candidates live in their own result directories until frozen
comparison approves them.

The search protocol exposes only `e1,m1` to reflection and uses `e2,m2` for
selection. It does not execute `e3,h1`. Operational replay later covers the
complete 5 × 6 matrix, while strict frozen comparison reports `e3,h1`
separately. Recipe `h1` is labeled as a known regression case because earlier
development used it; it is never presented as untouched holdout evidence.
