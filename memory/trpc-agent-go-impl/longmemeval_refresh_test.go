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
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestValidateLongMemEvalRetrievalRefresh(t *testing.T) {
	restoreStringFlag(t, flagTableSuffix, "_refresh_test")
	restoreBoolFlag(t, flagLMEAllowSharedTableRefresh, false)
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

func TestValidateLongMemEvalRetrievalRefreshSharedTable(t *testing.T) {
	restoreStringFlag(t, flagTableSuffix, "")
	restoreBoolFlag(t, flagLMEAllowSharedTableRefresh, false)
	restoreStringFlag(t, flagModel, "answer-model")
	restoreStringFlag(t, flagModelVariant, "variant")
	restoreStringFlag(t, flagEmbedModel, "embedding-model")
	restoreIntFlag(t, flagVectorTopK, 30)

	result := &runResult{
		Metadata: map[string]any{
			"table_suffix":        "",
			"user_scope":          "legacy-run",
			"user_scope_explicit": true,
			"model":               getModelName(),
			"model_variant":       getModelVariant(),
			"embedding_model":     getEmbedModelName(),
			"top_k":               float64(30),
		},
		Cases: []*caseResult{{
			QuestionID: "question-1",
			BackendResults: map[string]*backendResult{
				"pgvector": {
					UserID: "pgvector-question-1-legacy-run",
				},
			},
		}},
	}
	if err := validateLongMemEvalRetrievalRefresh(result); err == nil {
		t.Fatal("shared-table refresh did not require explicit opt-in")
	}

	*flagLMEAllowSharedTableRefresh = true
	if err := validateLongMemEvalRetrievalRefresh(result); err != nil {
		t.Fatalf("validate audited shared-table refresh: %v", err)
	}

	result.Metadata["user_scope_explicit"] = false
	if err := validateLongMemEvalRetrievalRefresh(result); err == nil {
		t.Fatal("shared-table refresh accepted an implicit user scope")
	}
	result.Metadata["user_scope_explicit"] = true

	result.Cases[0].BackendResults["pgvector"].UserID = "pgvector-question-1-other"
	if err := validateLongMemEvalRetrievalRefresh(result); err == nil {
		t.Fatal("shared-table refresh accepted a user outside its scope")
	}
}

func TestLongMemEvalRetrievalRefreshImplementation(t *testing.T) {
	restoreStringFlag(t, flagLMEImplementation, "refreshed-retrieval")
	result := &runResult{Metadata: map[string]any{
		"implementation": "source-ingestion-and-retrieval",
	}}

	got, err := longMemEvalRetrievalRefreshImplementation(result)
	if err != nil {
		t.Fatalf("resolve retrieval refresh implementation: %v", err)
	}
	if got != "refreshed-retrieval" {
		t.Fatalf("implementation = %q, want refreshed-retrieval", got)
	}

	*flagLMEImplementation = "source-ingestion-and-retrieval"
	if _, err := longMemEvalRetrievalRefreshImplementation(result); err == nil {
		t.Fatal("accepted the source implementation as the refresh implementation")
	}

	*flagLMEImplementation = ""
	if _, err := longMemEvalRetrievalRefreshImplementation(result); err == nil {
		t.Fatal("accepted an unspecified refresh implementation")
	}
}

func TestRefreshLongMemEvalRetrievalResultsRejectsProtocolDriftBeforeProvider(
	t *testing.T,
) {
	restoreStringFlag(t, flagTableSuffix, "_refresh_protocol")
	restoreStringFlag(t, flagModel, "answer-model")
	restoreStringFlag(t, flagModelVariant, "variant")
	restoreStringFlag(t, flagEvalModel, "judge-model")
	restoreStringFlag(t, flagEmbedModel, "embedding-model")
	restoreStringFlag(t, flagLMEImplementation, "target-refresh")
	restoreIntFlag(t, flagVectorTopK, 30)
	restoreIntFlag(t, flagLMEJudgeRuns, 3)
	restoreBoolFlag(t, flagLMEAnswer, true)

	recorded := currentLongMemEvalProtocol()
	digest, err := longMemEvalJSONSHA256(recorded)
	if err != nil {
		t.Fatalf("hash recorded protocol: %v", err)
	}
	result := &runResult{
		Metadata: map[string]any{
			"implementation":   "source-run",
			"table_suffix":     *flagTableSuffix,
			"model":            getModelName(),
			"model_variant":    getModelVariant(),
			"embedding_model":  getEmbedModelName(),
			"top_k":            *flagVectorTopK,
			"protocol":         recorded,
			"protocol_version": recorded.Version,
			"protocol_sha256":  digest,
		},
		Cases: []*caseResult{{
			BackendResults: map[string]*backendResult{"pgvector": {}},
		}},
	}
	sourcePath := filepath.Join(t.TempDir(), "results.json")
	if err := writeLongMemEvalResults(sourcePath, result); err != nil {
		t.Fatalf("write source results: %v", err)
	}

	*flagLMEJudgeRuns = 1
	err = refreshLongMemEvalRetrievalResults(
		context.Background(), sourcePath, t.TempDir(),
	)
	if err == nil || !strings.Contains(
		err.Error(), "validate LongMemEval retrieval refresh protocol",
	) {
		t.Fatalf("protocol drift error = %v", err)
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
		Metadata: map[string]any{"implementation": "source-implementation"},
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
		"answer-model", "variant", true, "refreshed-implementation",
		"source-digest", outPath, nil,
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
	if result.Metadata["implementation"] != "refreshed-implementation" ||
		refresh["source_implementation"] != "source-implementation" ||
		refresh["implementation"] != "refreshed-implementation" {
		t.Fatalf("retrieval implementation metadata = %#v", result.Metadata)
	}
	if result.Metadata["reanswer_model"] != "answer-model" ||
		result.Metadata["reanswer_model_variant"] != "variant" ||
		result.Metadata["reanswer_build"] == nil ||
		result.Metadata["reanswered_at"] != refresh["refreshed_at"] {
		t.Fatalf("retrieval re-answer metadata = %#v", result.Metadata)
	}
	if _, err := loadLongMemEvalResults(outPath); err != nil {
		t.Fatalf("load retrieval refresh checkpoint: %v", err)
	}
}

func TestRefreshLongMemEvalRetrievalResultWithoutAnswers(t *testing.T) {
	restoreStringFlag(t, flagTableSuffix, "_refresh_test")
	restoreIntFlag(t, flagVectorTopK, 30)

	persisted := memorySnapshot{
		ID: "memory-1", Memory: "Visited the Science Museum.",
		SourceSessions: []string{"answer-session"},
	}
	backend := &refreshTestBackend{
		hits: []memoryHit{{
			ID: persisted.ID, Memory: persisted.Memory, Score: 0.9,
		}},
		stored: []memorySnapshot{{
			ID: persisted.ID, Memory: persisted.Memory,
		}},
	}
	result := &runResult{
		Metadata: map[string]any{
			"implementation":                           "source-implementation",
			"reanswer_model":                           "stale-model",
			"answer_cache_initial_entries":             1,
			"embedding_response_cache_initial_entries": 1,
		},
		Cases: []*caseResult{{
			QuestionID:       "q1",
			QuestionType:     "single-session-user",
			Question:         "Which museum did I visit?",
			Answer:           "Science Museum",
			AnswerSessionIDs: []string{"answer-session"},
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:          "pgvector",
					UserID:           "user-1",
					FinalMemories:    []memorySnapshot{persisted},
					Retrieval:        []memoryHit{{Memory: "stale"}},
					Answer:           "stale answer",
					RawAnswer:        "stale answer",
					AnswerCacheKey:   "stale-key",
					AnswerSource:     "model",
					AnswerModelCalls: []lmeModelCallTrace{{Content: "stale answer"}},
					TokenUsage:       tokenUsagePtr(lmeTokenUsage{TotalTokens: 100}),
					AnswerUsage:      tokenUsagePtr(lmeTokenUsage{TotalTokens: 20}),
					ExactMatch:       true,
					F1:               1,
					BLEU:             1,
					Judge:            &lmeJudgeResult{Correct: true},
					Evidence:         &evidenceMetrics{HasAnswerTurnLabels: true},
				},
			},
		}},
	}
	outPath := t.TempDir() + "/refreshed.json"
	if err := refreshLongMemEvalRetrievalResult(
		context.Background(), result, backend, nil,
		"answer-model", "variant", false, "retrieval-only",
		"source-digest", outPath, nil,
	); err != nil {
		t.Fatalf("refresh retrieval result without answers: %v", err)
	}

	br := result.Cases[0].BackendResults["pgvector"]
	if br.Answer != "" || br.RawAnswer != "" || br.AnswerCacheKey != "" ||
		br.AnswerSource != "" || len(br.AnswerModelCalls) != 0 ||
		br.AnswerUsage != nil || br.ExactMatch || br.F1 != 0 || br.BLEU != 0 {
		t.Fatalf("answer state was not cleared: %#v", br)
	}
	if br.TokenUsage == nil || br.TokenUsage.TotalTokens != 80 {
		t.Fatalf("token usage = %#v, want ingestion-only total 80", br.TokenUsage)
	}
	if br.FailureStage != "retrieval_only" || br.Judge != nil {
		t.Fatalf("retrieval-only state = stage %q judge %#v", br.FailureStage, br.Judge)
	}
	if len(br.Retrieval) != 1 || br.Retrieval[0].ID != persisted.ID ||
		br.Evidence == nil || !br.Evidence.RetrievalRecallAll {
		t.Fatalf("refreshed retrieval evidence = %#v %#v", br.Retrieval, br.Evidence)
	}
	refresh, ok := result.Metadata["retrieval_refresh"].(map[string]any)
	if !ok || refresh["answer_enabled"] != false ||
		result.Metadata["answer_enabled"] != false ||
		result.Metadata["answer_scoring"] != "disabled for retrieval-only refresh" {
		t.Fatalf("retrieval-only metadata = %#v", result.Metadata)
	}
	if _, ok := result.Metadata["reanswer_model"]; ok {
		t.Fatalf("stale reanswer metadata retained: %#v", result.Metadata)
	}
	if _, ok := result.Metadata["answer_cache_initial_entries"]; ok {
		t.Fatalf("stale answer cache metadata retained: %#v", result.Metadata)
	}
	if _, ok := result.Metadata["embedding_response_cache_initial_entries"]; ok {
		t.Fatalf("stale embedding cache metadata retained: %#v", result.Metadata)
	}
	if _, err := loadLongMemEvalResults(outPath); err != nil {
		t.Fatalf("load retrieval-only refresh checkpoint: %v", err)
	}
}

func TestRefreshLongMemEvalRetrievalResultRecordsEmbeddingCache(t *testing.T) {
	restoreStringFlag(t, flagTableSuffix, "_refresh_cache_test")
	restoreIntFlag(t, flagVectorTopK, 30)

	cache, err := openLongMemEvalEmbeddingResponseCache(
		filepath.Join(t.TempDir(), "embedding-cache.jsonl"),
	)
	if err != nil {
		t.Fatalf("open embedding cache: %v", err)
	}
	identity, key, err := longMemEvalEmbeddingResponseCacheKey(
		"Which museum did I visit?", "embedding-model", 2,
	)
	if err != nil {
		t.Fatalf("build embedding cache key: %v", err)
	}
	if _, err := cache.Put(key, identity, []float64{0.25, 0.75}); err != nil {
		t.Fatalf("seed embedding cache: %v", err)
	}

	persisted := memorySnapshot{ID: "memory-1", Memory: "Visited the Science Museum."}
	backend := &refreshTestBackend{
		hits:   []memoryHit{{ID: persisted.ID, Memory: persisted.Memory, Score: 0.9}},
		stored: []memorySnapshot{persisted},
		onSearch: func() {
			if _, ok := cache.Lookup(key); !ok {
				t.Error("seeded embedding cache entry was not found")
			}
		},
	}
	result := &runResult{
		Metadata: map[string]any{
			"implementation": "source-implementation",
			"embedding_response_cache_initial_entries": 99,
		},
		Cases: []*caseResult{{
			QuestionID: "q1",
			Question:   "Which museum did I visit?",
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:       "pgvector",
					UserID:        "user-1",
					FinalMemories: []memorySnapshot{persisted},
				},
			},
		}},
	}
	if err := refreshLongMemEvalRetrievalResult(
		context.Background(), result, backend, nil,
		"answer-model", "variant", false, "retrieval-only-cache",
		"source-digest", filepath.Join(t.TempDir(), "refreshed.json"), cache,
	); err != nil {
		t.Fatalf("refresh retrieval result with embedding cache: %v", err)
	}

	if result.Metadata["embedding_response_cache_shared"] != true ||
		result.Metadata["embedding_response_cache_ledger_id"] != cache.LedgerID() ||
		result.Metadata["embedding_response_cache_initial_entries"] != 1 ||
		result.Metadata["embedding_response_cache_final_entries"] != 1 ||
		result.Metadata["embedding_response_cache_hits"] != 1 ||
		result.Metadata["embedding_response_cache_misses"] != 0 ||
		result.Metadata["embedding_response_cache_errors"] != 0 {
		t.Fatalf("embedding cache metadata = %#v", result.Metadata)
	}
}

func TestLongMemEvalJudgedOutputName(t *testing.T) {
	tests := map[string]string{
		"results.json":                     "judged_results.json",
		"reanswered_results.json":          "reanswered_judged_results.json",
		"retrieval_refreshed_results.json": "retrieval_refreshed_judged_results.json",
		"reranked_results.json":            "reranked_judged_results.json",
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
	hits     []memoryHit
	stored   []memorySnapshot
	queries  []string
	onSearch func()
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
	if b.onSearch != nil {
		b.onSearch()
	}
	return append([]memoryHit(nil), b.hits...), nil
}

func (b *refreshTestBackend) Read(
	context.Context,
	memory.UserKey,
) ([]memorySnapshot, bool, error) {
	return append([]memorySnapshot(nil), b.stored...), false, nil
}

func (*refreshTestBackend) SnapshotProviderUsage() lmeProviderUsage {
	return lmeProviderUsage{
		Embedding: lmeEmbeddingUsage{Calls: 1, PromptTokens: 4, TotalTokens: 4},
		Reported:  true,
	}
}

func (*refreshTestBackend) Close() error { return nil }
