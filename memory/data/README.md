# Memory Benchmark Datasets

This directory contains or references benchmark datasets for evaluating
long-term conversational memory.

## LoCoMo Dataset

LoCoMo data is stored in this directory.

### Download

Download the LoCoMo dataset from the official repository:

```bash
# Clone the LoCoMo repository.
git clone https://github.com/snap-research/locomo.git

# Copy the dataset files.
cp locomo/data/locomo10/*.json ./
```

### Dataset Format

The LoCoMo dataset contains long-term conversational data with the following
structure:

```json
{
  "sample_id": "1",
  "speakers": ["Alice", "Bob"],
  "conversation": [
    {
      "session_id": "1",
      "session_date": "2023-01-15",
      "turns": [
        {"speaker": "Alice", "text": "..."},
        {"speaker": "Bob", "text": "..."}
      ],
      "observation": "Key observations from this session...",
      "summary": "Summary of this session..."
    }
  ],
  "qa": [
    {
      "question_id": "1_1",
      "question": "What did Alice mention about...?",
      "answer": "...",
      "category": "single-hop",
      "evidence": ["1"]
    }
  ],
  "event_summary": {
    "Alice": "Summary of events for Alice...",
    "Bob": "Summary of events for Bob..."
  }
}
```

### QA Categories

- `single-hop`: Single-hop questions answerable from one conversation segment.
- `multi-hop`: Multi-hop questions requiring multiple conversation segments.
- `temporal`: Temporal reasoning questions involving time relationships.
- `open-domain`: Open-domain questions requiring world knowledge.
- `adversarial`: Adversarial questions designed to test robustness.

### Reference

[LoCoMo: Long-Context Conversational Memory Benchmark](https://arxiv.org/abs/2402.17753)

## LongMemEval Dataset

LongMemEval data is shared with the summary benchmark under
`summary/data/longmemeval-cleaned/` instead of being duplicated in this
directory.

### Download

Download LongMemEval from the official repository:

```bash
# Clone the LongMemEval repository.
git clone https://github.com/xiaowu0162/LongMemEval.git

# Place cleaned files under the shared benchmark data directory.
mkdir -p ../../summary/data/longmemeval-cleaned
cp LongMemEval/data/longmemeval_oracle.json ../../summary/data/longmemeval-cleaned/
```

### Dataset Format

The LongMemEval dataset contains per-question haystack conversations:

```json
{
  "question_id": "gpt4_2655b836",
  "question_type": "temporal-reasoning",
  "question": "What was the first issue...",
  "question_date": "2023/04/10 (Mon) 23:07",
  "answer": "GPS system not functioning correctly",
  "answer_session_ids": ["answer_4be1b6b4_2"],
  "haystack_dates": ["2023/04/10 (Mon) 14:47"],
  "haystack_session_ids": ["answer_4be1b6b4_3"],
  "haystack_sessions": [
    [
      {"role": "user", "content": "..."},
      {"role": "assistant", "content": "..."}
    ]
  ]
}
```

### Reference

[LongMemEval: Benchmarking Chat Assistants on Long-Term Interactive Memory](https://arxiv.org/abs/2504.19413)
