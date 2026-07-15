//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestValidateLongMemEvalRetrievalRefresh(t *testing.T) {
	restoreStringFlag(t, flagTableSuffix, "_refresh_test")
	restoreStringFlag(t, flagModel, "answer-model")
	restoreStringFlag(t, flagModelVariant, "variant")
	restoreStringFlag(t, flagEmbedModel, "embedding-model")
	restoreIntFlag(t, flagVectorTopK, 30)

	result := &runResult{
		Metadata: map[string]any{
			"table_suffix":    "_refresh_test",
			"model":           getModelName(),
			"model_variant":   getModelVariant(),
			"embedding_model": getEmbedModelName(),
			"top_k":           float64(30),
		},
		Cases: []*caseResult{{
			BackendResults: map[string]*backendResult{"pgvector": {}},
		}},
	}
	if err := validateLongMemEvalRetrievalRefresh(result); err != nil {
		t.Fatalf("validate retrieval refresh: %v", err)
	}

	result.Metadata["table_suffix"] = "_other"
	if err := validateLongMemEvalRetrievalRefresh(result); err == nil {
		t.Fatal("validate retrieval refresh accepted a different table")
	}
}

func TestRefreshLongMemEvalRetrievalResult(t *testing.T) {
	restoreStringFlag(t, flagTableSuffix, "_refresh_test")
	restoreIntFlag(t, flagVectorTopK, 30)

	persisted := memorySnapshot{ID: "memory-1", Memory: "Visited the Science Museum."}
	saved := persisted
	saved.SourceSessions = []string{"answer-session"}
	saved.SourceHasAnswer = true
	backend := &refreshTestBackend{
		hits:   []memoryHit{{ID: persisted.ID, Memory: persisted.Memory, Score: 0.9}},
		stored: []memorySnapshot{persisted},
	}
	stop := "stop"
	llm := &queuedAnswerModel{responses: []*model.Response{{
		Choices: []model.Choice{{
			Message:      model.NewAssistantMessage("Science Museum"),
			FinishReason: &stop,
		}},
	}}}
	result := &runResult{
		Metadata: map[string]any{},
		Cases: []*caseResult{{
			QuestionID:   "q1",
			QuestionType: "single-session-user",
			Question:     "Which museum did I visit?",
			Answer:       "Science Museum",
			AnswerSessionIDs: []string{
				"answer-session",
			},
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:     "pgvector",
					UserID:      "user-1",
					TokenUsage:  tokenUsagePtr(lmeTokenUsage{TotalTokens: 100}),
					AnswerUsage: tokenUsagePtr(lmeTokenUsage{TotalTokens: 20}),
					Retrieval:   []memoryHit{{Memory: "stale"}},
					FinalMemories: []memorySnapshot{
						saved,
					},
					Judge: &lmeJudgeResult{Correct: false},
				},
			},
		}},
	}
	outPath := t.TempDir() + "/refreshed.json"
	if err := refreshLongMemEvalRetrievalResult(
		context.Background(), result, backend, llm,
		"answer-model", "variant", "source-digest", outPath,
	); err != nil {
		t.Fatalf("refresh retrieval result: %v", err)
	}

	br := result.Cases[0].BackendResults["pgvector"]
	if br.Answer != "Science Museum" || !br.ExactMatch {
		t.Fatalf("refreshed answer = %q, exact = %v", br.Answer, br.ExactMatch)
	}
	if len(br.Retrieval) != 1 || br.Retrieval[0].ID != "memory-1" {
		t.Fatalf("refreshed retrieval = %#v", br.Retrieval)
	}
	if len(br.Retrieval[0].SourceSessions) != 1 ||
		br.Retrieval[0].SourceSessions[0] != "answer-session" ||
		!br.Retrieval[0].SourceHasAnswer {
		t.Fatalf("refreshed retrieval provenance = %#v", br.Retrieval[0])
	}
	if br.Evidence == nil || !br.Evidence.RetrievalRecallAll {
		t.Fatalf("refreshed evidence = %#v", br.Evidence)
	}
	if br.Judge != nil {
		t.Fatalf("judge was not cleared: %#v", br.Judge)
	}
	if br.TokenUsage == nil || br.TokenUsage.TotalTokens != 80 {
		t.Fatalf("token usage = %#v, want preserved ingestion total 80", br.TokenUsage)
	}
	if len(backend.queries) != 1 || backend.queries[0] != result.Cases[0].Question {
		t.Fatalf("search queries = %#v", backend.queries)
	}
	refresh, ok := result.Metadata["retrieval_refresh"].(map[string]any)
	if !ok || refresh["source_sha256"] != "source-digest" {
		t.Fatalf("retrieval refresh metadata = %#v", result.Metadata["retrieval_refresh"])
	}
	if _, err := loadLongMemEvalResults(outPath); err != nil {
		t.Fatalf("load retrieval refresh checkpoint: %v", err)
	}
}

func TestLongMemEvalJudgedOutputName(t *testing.T) {
	tests := map[string]string{
		"results.json":                     "judged_results.json",
		"reanswered_results.json":          "reanswered_judged_results.json",
		"retrieval_refreshed_results.json": "retrieval_refreshed_judged_results.json",
		"candidate.json":                   "candidate_judged.json",
	}
	for input, want := range tests {
		if got := longMemEvalJudgedOutputName(input); got != want {
			t.Errorf("longMemEvalJudgedOutputName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestVerifyLongMemEvalPersistedMemories(t *testing.T) {
	want := []memorySnapshot{
		{ID: "2", Memory: "second", SourceSessions: []string{"b", "a"}},
		{ID: "1", Memory: "first", Participants: []string{"Dana", "Alice"}},
	}
	got := []memorySnapshot{
		{ID: "1", Memory: "first", Participants: []string{"Alice", "Dana"}},
		{ID: "2", Memory: "second", SourceSessions: []string{"a", "b"}},
	}
	if err := verifyLongMemEvalPersistedMemories(want, got); err != nil {
		t.Fatalf("verify equivalent memory sets: %v", err)
	}
	got[0].Memory = "changed"
	if err := verifyLongMemEvalPersistedMemories(want, got); err == nil {
		t.Fatal("verify persisted memories accepted changed content")
	}
}

func restoreStringFlag(t *testing.T, target *string, value string) {
	t.Helper()
	original := *target
	*target = value
	t.Cleanup(func() { *target = original })
}

func restoreIntFlag(t *testing.T, target *int, value int) {
	t.Helper()
	original := *target
	*target = value
	t.Cleanup(func() { *target = original })
}

type refreshTestBackend struct {
	hits    []memoryHit
	stored  []memorySnapshot
	queries []string
}

func (*refreshTestBackend) Name() string { return "pgvector" }

func (*refreshTestBackend) IngestPair(
	context.Context,
	*session.Session,
	ingestMeta,
) (*extractionTrace, error) {
	panic("unexpected ingestion during retrieval refresh")
}

func (*refreshTestBackend) Flush(context.Context) error { return nil }

func (b *refreshTestBackend) Search(
	_ context.Context,
	_ memory.UserKey,
	query string,
	_ int,
) ([]memoryHit, error) {
	b.queries = append(b.queries, query)
	return append([]memoryHit(nil), b.hits...), nil
}

func (b *refreshTestBackend) Read(
	context.Context,
	memory.UserKey,
	int,
) ([]memorySnapshot, error) {
	return append([]memorySnapshot(nil), b.stored...), nil
}

func (*refreshTestBackend) SnapshotProviderUsage() lmeProviderUsage {
	return lmeProviderUsage{
		Embedding: lmeEmbeddingUsage{Calls: 1, PromptTokens: 4, TotalTokens: 4},
		Reported:  true,
	}
}

func (*refreshTestBackend) Close() error { return nil }
