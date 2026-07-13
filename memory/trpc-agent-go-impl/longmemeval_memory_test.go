//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLongMemEvalDateHelpers(t *testing.T) {
	t.Parallel()

	ts, ok := lmeUnixTimestamp("2023/04/10 (Mon) 14:47")
	if !ok {
		t.Fatal("expected date to parse")
	}
	if ts != 1681138020 {
		t.Fatalf("unexpected timestamp: got %d", ts)
	}

	if _, ok := lmeUnixTimestamp("not-a-date"); ok {
		t.Fatal("invalid date parsed")
	}
}

func TestWithObservationDate(t *testing.T) {
	t.Parallel()

	got := withObservationDate("The Fitbit has been used for 9 months.", "2023/04/10 (Mon) 14:47")
	if !strings.HasPrefix(got, "Observation date: 2023/04/10 (Mon) 14:47\n") {
		t.Fatalf("missing observation date prefix: %q", got)
	}
	if !strings.Contains(got, "The Fitbit has been used for 9 months.") {
		t.Fatalf("missing original content: %q", got)
	}
	if out := withObservationDate("content", "  "); out != "content" {
		t.Fatalf("empty date should leave content unchanged: %q", out)
	}
}

func TestAnalyzeLongMemEvalResults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "results.json")
	result := &runResult{
		Summary: &runSummary{TotalCases: 1},
		Cases: []*caseResult{{
			QuestionID:   "q1",
			QuestionType: "single-session-user",
			Question:     "Where did I meet Sophia?",
			Answer:       "a coffee shop",
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:      "pgvector",
					FailureStage: "answer_miss",
					Answer:       "I don't know.",
					F1:           0,
					BLEU:         0,
					Evidence: &evidenceMetrics{
						HasEvidenceLabels:  true,
						ExtractRecallAny:   true,
						RetrievalRecallAny: true,
						RetrievalRecallAll: true,
					},
				},
			},
		}},
	}
	saveLongMemEvalResults(dir, result)
	if err := analyzeLongMemEvalResults(resultsPath, dir); err != nil {
		t.Fatalf("analyze results: %v", err)
	}
	for _, name := range []string{"analysis.md", "bad_cases.tsv"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(data), "q1") {
			t.Fatalf("%s missing question id: %s", name, data)
		}
	}
}
