# LongMemEval Benchmark Data

This directory stores the LongMemEval data used by the `summary/` benchmark suite.

## Dataset

### LongMemEval

LongMemEval evaluates long-term memory recall over realistic multi-session user/assistant dialogues. The headline experiment uses the cleaned `single-session-user` slice from `longmemeval_s_cleaned.json` for the main 70-case comparison.

Expected layout:

```text
data/
└── longmemeval-cleaned/
    ├── longmemeval_s_cleaned.json
    ├── longmemeval_m_cleaned.json      # optional / tiny placeholder in the upstream dataset
    └── longmemeval_oracle.json         # optional metadata / oracle file
```

## Download

Download LongMemEval:

```bash
./download_datasets.sh
```

Equivalent aliases:

```bash
./download_datasets.sh longmemeval
./download_datasets.sh lme
```

## References

- [LongMemEval Paper](https://arxiv.org/abs/2410.10813)
- [LongMemEval Cleaned Dataset](https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned)
