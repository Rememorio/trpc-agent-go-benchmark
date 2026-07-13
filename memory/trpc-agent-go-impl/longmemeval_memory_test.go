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

func TestFilterCasesByQuestionIDs(t *testing.T) {
	oldID := *flagLMEQuestionID
	oldIDs := *flagLMEQuestionIDs
	oldTypes := *flagLMEQuestionTypes
	oldPerType := *flagLMEPerType
	oldAbstention := *flagLMEAbstentionCount
	oldMaxTasks := *flagMaxTasks
	defer func() {
		*flagLMEQuestionID = oldID
		*flagLMEQuestionIDs = oldIDs
		*flagLMEQuestionTypes = oldTypes
		*flagLMEPerType = oldPerType
		*flagLMEAbstentionCount = oldAbstention
		*flagMaxTasks = oldMaxTasks
	}()

	*flagLMEQuestionID = "q1"
	*flagLMEQuestionIDs = "q3, q2"
	*flagLMEQuestionTypes = ""
	*flagLMEPerType = 0
	*flagLMEAbstentionCount = 0
	*flagMaxTasks = 0

	instances := []*lmeInstance{
		{QuestionID: "q1", QuestionType: "single-session-user"},
		{QuestionID: "skip", QuestionType: "single-session-user"},
		{QuestionID: "q2", QuestionType: "multi-session"},
		{QuestionID: "q3", QuestionType: "temporal-reasoning"},
		nil,
	}
	got := filterCases(instances)
	want := []string{"q1", "q2", "q3"}
	if len(got) != len(want) {
		t.Fatalf("unexpected case count: got %d want %d", len(got), len(want))
	}
	for i, inst := range got {
		if inst.QuestionID != want[i] {
			t.Fatalf("case %d: got %q want %q", i, inst.QuestionID, want[i])
		}
	}
}

func TestBuildLongMemEvalAnswerPromptPreferenceGuidance(t *testing.T) {
	inst := &lmeInstance{
		QuestionID:   "q-pref",
		QuestionType: "single-session-preference",
		QuestionDate: "2023/05/27 (Sat) 09:00",
		Question:     "Can you recommend events this weekend?",
	}
	prompt := buildLongMemEvalAnswerPrompt(inst, []memoryHit{{
		Memory: "Interested in language exchange events focused on French and Spanish practice.",
	}})

	if !strings.Contains(prompt, "Question type: single-session-preference") {
		t.Fatalf("missing question type: %s", prompt)
	}
	if !strings.Contains(prompt, "LongMemEval expects the user's preference profile") {
		t.Fatalf("missing preference guidance: %s", prompt)
	}
	if !strings.Contains(prompt, "Do not answer \"I don't know\" merely because") {
		t.Fatalf("missing unknown-answer guard: %s", prompt)
	}
	if !strings.Contains(prompt, "Do not answer\nas a recommendation list") {
		t.Fatalf("missing recommendation-list guard: %s", prompt)
	}
	if !strings.Contains(prompt, "The user would prefer") {
		t.Fatalf("missing preference answer shape: %s", prompt)
	}
}

func TestBuildLongMemEvalAnswerPromptNonPreference(t *testing.T) {
	inst := &lmeInstance{
		QuestionID:   "q-fact",
		QuestionType: "single-session-assistant",
		Question:     "What was the fifth bottle?",
	}
	prompt := buildLongMemEvalAnswerPrompt(inst, nil)

	if strings.Contains(prompt, "preference profile") {
		t.Fatalf("unexpected preference guidance: %s", prompt)
	}
	if !strings.Contains(prompt, "(no memories retrieved)") {
		t.Fatalf("missing empty-memory marker: %s", prompt)
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
			QuestionType: "single-session-preference",
			Question:     "Where did I meet Sophia?",
			Answer:       "The user would prefer coffee shops with quiet seating. They would not prefer crowded restaurants.",
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:      "pgvector",
					FailureStage: "answer_miss",
					Answer:       "The user likes coffee.",
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
		if !strings.Contains(string(data), "missing=") {
			t.Fatalf("%s missing answer gap diagnosis: %s", name, data)
		}
		if !strings.Contains(string(data), "negative_preference") {
			t.Fatalf("%s missing preference slot diagnosis: %s", name, data)
		}
	}
}

func TestCompareLongMemEvalResults(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	candidateDir := t.TempDir()
	outputDir := t.TempDir()
	basePath := filepath.Join(baseDir, "results.json")
	candidatePath := filepath.Join(candidateDir, "results.json")
	base := &runResult{
		Summary: &runSummary{TotalCases: 1},
		Cases: []*caseResult{{
			QuestionID:   "q1",
			QuestionType: "single-session-assistant",
			Question:     "What was the fifth bottle?",
			Answer:       "Absinthe",
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:      "pgvector",
					FailureStage: "answer_miss",
					Answer:       "I don't know.",
					F1:           0,
					BLEU:         0,
				},
			},
		}},
	}
	candidate := &runResult{
		Summary: &runSummary{TotalCases: 1},
		Cases: []*caseResult{{
			QuestionID:   "q1",
			QuestionType: "single-session-assistant",
			Question:     "What was the fifth bottle?",
			Answer:       "Absinthe",
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:      "pgvector",
					FailureStage: "ok",
					ExactMatch:   true,
					Answer:       "Absinthe",
					F1:           1,
					BLEU:         1,
				},
			},
		}},
	}
	saveLongMemEvalResults(baseDir, base)
	saveLongMemEvalResults(candidateDir, candidate)

	if err := compareLongMemEvalResults(basePath, candidatePath, outputDir); err != nil {
		t.Fatalf("compare results: %v", err)
	}
	for _, name := range []string{"comparison.md", "comparison.tsv"} {
		data, err := os.ReadFile(filepath.Join(outputDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(data)
		if !strings.Contains(text, "q1") {
			t.Fatalf("%s missing question id: %s", name, text)
		}
		if !strings.Contains(text, "+1.0000") {
			t.Fatalf("%s missing delta: %s", name, text)
		}
	}
}

func TestParseLongMemEvalComparePaths(t *testing.T) {
	base, candidate, err := parseLongMemEvalComparePaths("base.json, candidate.json")
	if err != nil {
		t.Fatalf("parse compare paths: %v", err)
	}
	if base != "base.json" || candidate != "candidate.json" {
		t.Fatalf("unexpected paths: %q %q", base, candidate)
	}
	if _, _, err := parseLongMemEvalComparePaths("only-one.json"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestMissingReferenceKeywords(t *testing.T) {
	got := missingReferenceKeywords(
		"The user would prefer cultural events.",
		"The user would prefer cultural events with Spanish language practice and learning resources.",
		4,
	)
	want := []string{"language", "learning", "practice", "resources"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected missing keywords: got %v want %v", got, want)
	}
}
