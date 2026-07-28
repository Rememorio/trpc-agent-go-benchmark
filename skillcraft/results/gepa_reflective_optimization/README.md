# Reflective Optimization Evidence

This directory is the sanitized evidence package for the pure-Go reflective
optimizer and its five-family SkillCraft replay.

Read [`REPORT.md`](REPORT.md) or
[`REPORT.zh_CN.md`](REPORT.zh_CN.md) for the experiment design, bad-case
analysis, and scoped conclusion. The machine-readable evidence is split by the
decision it supports:

- [`evidence.json`](evidence.json) records the accepted repair of an existing
  reviewer-produced Recipe skill. Across eight frozen holdout pairs it improved
  quality from 95.50% to 98.35% and reduced agent tokens by 6.57%, with no pass
  loss.
- [`full_matrix/generic_candidate_frozen_evidence.json`](full_matrix/generic_candidate_frozen_evidence.json)
  records a Recipe efficiency candidate that looked better on validation but
  failed untouched holdout. It was rejected.
- [`full_matrix/worldbank_candidate_frozen_evidence_v2.json`](full_matrix/worldbank_candidate_frozen_evidence_v2.json)
  records a World Bank candidate that passed frozen confirmation but was later
  rejected by the same-model operational replay.
- [`glm_full_matrix_evidence.json`](glm_full_matrix_evidence.json) aggregates
  the final same-model GLM-5.2 replay: three root seeds, five task families, six
  scales, and three arms, for 270 arm-cases.
- [`full_matrix_evidence.json`](full_matrix_evidence.json) retains the earlier
  GPT-5.2 runtime replay as a negative cross-model portability result.
- [`model_routing_evidence.json`](model_routing_evidence.json) records the check
  showing that `gpt-5.2` and `glm52` are distinct routes on the configured
  OpenAI-compatible endpoint.
- [`recipe_candidate.json`](recipe_candidate.json) is the accepted Recipe
  candidate used by both operational replays.

Both operational aggregates include per-root family summaries and sanitized
failed-case records so the report's attribution and bad-case analysis can be
audited without publishing raw task workspaces or model transcripts.

Search, frozen confirmation, and the final same-model replay requested the
self-deployed GLM-5.2 route (`glm52`). In that replay, Recipe preserved a 100%
pass rate, improved quality by 0.32 percentage points, and reduced end-to-end
tokens by 14.75% across three consistently favorable root seeds. World Bank
preserved pass and quality but increased tokens by 3.29%, so it was not
promoted.

The global aggregate marks the preregistered mechanical gate as eligible, but
candidate attribution remains essential: most of the aggregate quality delta
came from no-overlay Pokémon completion variance. Across the two changed
families, the overlay reduced end-to-end tokens by 6.77%, but Recipe supplied
all of the saving while World Bank added cost. The report therefore promotes
Recipe and rejects World Bank as individual skill revisions.

Raw model transcripts, task workspaces, endpoint configuration, credentials,
and machine-specific paths are intentionally excluded.
