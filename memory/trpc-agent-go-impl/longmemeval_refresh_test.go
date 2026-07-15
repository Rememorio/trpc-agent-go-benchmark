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
	"strings"
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
	restoreBoolFlag(t, flagLMERefreshRerank, false)

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
					Evidence: &evidenceMetrics{HasAnswerTurnLabels: true},
					Judge:    &lmeJudgeResult{Correct: false},
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
	if br.Evidence == nil || !br.Evidence.RetrievalRecallAll ||
		!br.Evidence.HasAnswerTurnLabels {
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

func TestRefreshLongMemEvalRetrievalResultWithRerank(t *testing.T) {
	restoreStringFlag(t, flagTableSuffix, "_rerank_test")
	restoreIntFlag(t, flagVectorTopK, 30)
	restoreBoolFlag(t, flagLMERefreshRerank, true)
	restoreIntFlag(t, flagLMERerankTopN, 1)

	relevant := memorySnapshot{ID: "memory-1", Memory: "Visited the Science Museum."}
	irrelevant := memorySnapshot{ID: "memory-2", Memory: "Assistant recommended a database course."}
	savedRelevant := relevant
	savedRelevant.SourceSessions = []string{"answer-session"}
	savedRelevant.SourceHasAnswer = true
	backend := &refreshTestBackend{
		hits: []memoryHit{
			{ID: irrelevant.ID, Memory: irrelevant.Memory, Score: 0.95},
			{ID: relevant.ID, Memory: relevant.Memory, Score: 0.8},
		},
		stored: []memorySnapshot{relevant, irrelevant},
	}
	stop := "stop"
	llm := &queuedAnswerModel{responses: []*model.Response{
		{
			Choices: []model.Choice{{
				Message:      model.NewAssistantMessage(`{"indices":[2]}`),
				FinishReason: &stop,
			}},
			Usage: &model.Usage{PromptTokens: 8, CompletionTokens: 2, TotalTokens: 10},
		},
		{
			Choices: []model.Choice{{
				Message:      model.NewAssistantMessage("Science Museum"),
				FinishReason: &stop,
			}},
			Usage: &model.Usage{PromptTokens: 4, CompletionTokens: 1, TotalTokens: 5},
		},
	}}
	result := &runResult{
		Metadata: map[string]any{},
		Cases: []*caseResult{{
			QuestionID:       "q1",
			QuestionType:     "single-session-user",
			Question:         "Which museum did I visit?",
			Answer:           "Science Museum",
			AnswerSessionIDs: []string{"answer-session"},
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:     "pgvector",
					UserID:      "user-1",
					TokenUsage:  tokenUsagePtr(lmeTokenUsage{TotalTokens: 100, LLMCalls: 2}),
					AnswerUsage: tokenUsagePtr(lmeTokenUsage{TotalTokens: 20, LLMCalls: 1}),
					FinalMemories: []memorySnapshot{
						savedRelevant, irrelevant,
					},
				},
			},
		}},
	}
	if err := refreshLongMemEvalRetrievalResult(
		context.Background(), result, backend, llm,
		"answer-model", "variant", "source-digest", t.TempDir()+"/reranked.json",
	); err != nil {
		t.Fatalf("refresh retrieval result with rerank: %v", err)
	}

	br := result.Cases[0].BackendResults["pgvector"]
	if len(br.PreRerankRetrieval) != 2 || br.PreRerankRetrieval[0].ID != "memory-2" {
		t.Fatalf("pre-rerank retrieval = %#v", br.PreRerankRetrieval)
	}
	if len(br.Retrieval) != 1 || br.Retrieval[0].ID != "memory-1" {
		t.Fatalf("reranked retrieval = %#v", br.Retrieval)
	}
	if br.RerankRaw != `{"indices":[2]}` || br.RerankError != "" ||
		len(br.RerankModelCalls) != 1 {
		t.Fatalf("rerank trace = raw %q error %q calls %#v", br.RerankRaw, br.RerankError, br.RerankModelCalls)
	}
	if br.RerankUsage == nil || br.RerankUsage.TotalTokens != 10 ||
		br.RerankUsage.LLMCalls != 1 {
		t.Fatalf("rerank usage = %#v", br.RerankUsage)
	}
	if br.TokenUsage == nil || br.TokenUsage.TotalTokens != 95 ||
		br.TokenUsage.LLMCalls != 3 {
		t.Fatalf("combined token usage = %#v", br.TokenUsage)
	}
	if br.Answer != "Science Museum" || !br.ExactMatch {
		t.Fatalf("reranked answer = %q, exact = %v", br.Answer, br.ExactMatch)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("model requests = %d", len(llm.requests))
	}
	for _, message := range llm.requests[1].Messages {
		if strings.Contains(message.Content, irrelevant.Memory) {
			t.Fatalf("answer prompt included filtered memory: %s", message.Content)
		}
	}
	refresh, ok := result.Metadata["retrieval_refresh"].(map[string]any)
	if !ok || refresh["rerank_enabled"] != true ||
		refresh["rerank_prompt_version"] != lmeRerankPromptVersion ||
		refresh["rerank_top_n"] != 1 {
		t.Fatalf("retrieval refresh metadata = %#v", result.Metadata["retrieval_refresh"])
	}
}

func TestLongMemEvalRetrievalRefreshOutputName(t *testing.T) {
	restoreBoolFlag(t, flagLMERefreshRerank, false)
	if got := longMemEvalRetrievalRefreshOutputName(); got != lmeRetrievalRefreshOutput {
		t.Fatalf("refresh output = %q", got)
	}
	*flagLMERefreshRerank = true
	if got := longMemEvalRetrievalRefreshOutputName(); got != lmeRetrievalRerankOutput {
		t.Fatalf("rerank output = %q", got)
	}
}

func TestLongMemEvalJudgedOutputName(t *testing.T) {
	tests := map[string]string{
		"results.json":                     "judged_results.json",
		"reanswered_results.json":          "reanswered_judged_results.json",
		"retrieval_refreshed_results.json": "retrieval_refreshed_judged_results.json",
		"retrieval_reranked_results.json":  "retrieval_reranked_judged_results.json",
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

func restoreBoolFlag(t *testing.T, target *bool, value bool) {
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
