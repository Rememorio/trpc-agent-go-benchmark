# LongMemEval Memory Research Archive

Archived on 2026-08-26. Development and evaluation are intentionally stopped,
and no merge is requested for either companion pull request:

- [`trpc-group/trpc-agent-go#2196`](https://github.com/trpc-group/trpc-agent-go/pull/2196)
- [`trpc-group/trpc-agent-go-benchmark#17`](https://github.com/trpc-group/trpc-agent-go-benchmark/pull/17)

This page separates completed evidence from the final incomplete confirmation
run. It does not reinterpret intermediate scores as final results.

## Accepted Evidence

The accepted evidence remains the completed program documented in
[REPORT.md](REPORT.md) and [REPORT.zh_CN.md](REPORT.zh_CN.md):

| Phase | Scope | Terminal result |
| --- | --- | --- |
| Oracle development | 16 observed questions, 183 pairs per arm | Candidate 16/16 majority and 48/48 answer replicates |
| Unseen non-target gate | 8 preregistered full-haystack questions, 1,954 pairs per arm | Candidate 8/8 and 24/24; control 8/8 and 23/24; integrity, outcome, and cost passed |
| Observed same-size regression | 48 full-haystack questions, 11,839 pairs per arm | Main 24/48, self-hosted Mem0 OSS 41/48, candidate 43/48; integrity and outcome passed, frozen cost gate failed |

For the observed48 comparison, candidate versus main had 19 wins and no
losses. Candidate versus Mem0 had four wins and two losses; that difference
was descriptive rather than statistically significant. The candidate was
frozen after missing the preregistered uncached-memory-token limit by reaching
1.5699x main against a 1.55x threshold.

The evaluated revisions and audit digests in the reports are authoritative.
The final PR heads contain later integration work and must not be relabeled as
the evaluated builds.

## Stopped Confirmation Run

The final run was registered on 2026-08-20 to compare current upstream
pgvector, the assistant-fallback candidate, and self-hosted Mem0 OSS on the
exact historical observed48 selection under one current protocol.

| Item | Registered value |
| --- | --- |
| Dataset | Cleaned LongMemEval-S full haystack |
| Selection | 48 questions across all six types; 11,839 replay pairs per arm |
| Replay | Chronological, role-aware user/assistant replay with extraction after every round |
| Answers and judges | Three answer replicates; three judge votes per answer |
| Benchmark | `f3c01f8d2d60bf4ff7935b0ca2f577da3abb9cbb` |
| Upstream pgvector | `5fba9e5f62d28f4e007cea5e556fef707cd0917b` |
| Candidate pgvector | `5ba5ce7e5ab6045f7e4a9b70c6e8c4ed7ce602c1` |
| Mem0 OSS | `42986025d5d41877cc6ff7c37b96d188cf3aea01` |

Both allowed baseline attempts completed all 48 case containers but failed the
runtime-integrity requirement:

| Attempt | PGVector pairs | Mem0 pairs | Runtime errors | Terminal status |
| --- | ---: | ---: | ---: | --- |
| Baseline 1 | 11,692 | 11,839 | 1 | Rejected |
| Baseline 2 | 11,624 | 11,839 | 3 | Rejected |

The recorded failed units were:

| Attempt | Case | Question ID | Backend | Failure |
| --- | ---: | --- | --- | --- |
| 1 | 27 | `gpt4_f420262d` | pgvector | Embedding provider request failed |
| 2 | 2 | `7527f7e2` | pgvector | Embedding provider request failed |
| 2 | 29 | `gpt4_483dd43c` | Mem0 OSS | Model answer remained truncated after repair |
| 2 | 46 | `7a8d0b71` | pgvector | Embedding provider request failed |

The run stopped on 2026-08-26 rather than performing more recovery or reruns.
The candidate, re-answer, judge, audit, and aggregate stages never started.
Consequently, this run is operational evidence about long-running benchmark
reliability only. It provides no new comparison among pgvector main, Mem0, and
the candidate.

An operational progress query inadvertently exposed an interim baseline
summary before shutdown. The candidate revision and selection had already
been frozen, candidate results did not exist, and no adaptive tuning was
performed. This incident is retained in the compact artifact manifest.

## Artifact Policy

A compact, checksummed archive is published with the matching release in the
author's benchmark fork. It contains the registered plan, selection metadata,
terminal attempt statuses, failed-unit summary, sanitized failure excerpts,
and orchestration diagnostics.

The 2.3 GB local execution directory is intentionally not published. Roughly
1.7 GB is provider embedding cache, 408 MB is non-canonical result snapshots,
and the remainder is model cache, temporary stores, and verbose traces. These
files add substantial size without changing the evidence boundary. API
credentials, machine-specific paths, provider caches, databases, raw memory
snapshots, and incomplete quality outputs are excluded from the archive.

Release:
[`memory-lme-archive-20260826`](https://github.com/Rememorio/trpc-agent-go-benchmark/releases/tag/memory-lme-archive-20260826)

The machine-readable terminal record is
[ARCHIVE_MANIFEST.json](ARCHIVE_MANIFEST.json).

<details>
<summary>中文归档说明</summary>

本研究方向于 2026-08-26 停止，不再请求合入两个关联 PR。此前已经完成并通过
完整性校验的 Oracle16、未见 non-target8 和 observed48 正式实验继续作为有效
证据；最后一次确认性 observed48 实验不覆盖这些结论。

最后一次实验的两轮 baseline 都跑完了 48 个 case，但分别包含 1 个和 3 个
运行时错误。第二轮中 pgvector 只完成 11,624/11,839 个 pair，Mem0 完成
11,839/11,839 个 pair。由于 candidate、重复回答和 judge 均未启动，本轮不能
形成新的 pgvector、Mem0、candidate 三方比较结论。实验按决定停止，不再进行
单 case 恢复或全量重跑。

公开归档只保留注册配置、版本、checksum、失败单元、脱敏日志和终止状态。
2.3 GB 本地目录中的 provider cache、数据库、原始 memory snapshot 和不完整质量
输出不上传。

</details>
