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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestResolveLongMemEvalAnswerDeduplicatesIdenticalPrompts(t *testing.T) {
	t.Parallel()

	cache, err := openLongMemEvalAnswerCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	llm := &queuedJudgeModel{responses: []string{"Option B", "Option A"}}
	inst := &lmeInstance{
		QuestionID: "q-answer-cache",
		Question:   "Which option?",
		Answer:     flexString("Option B"),
	}
	hits := []memoryHit{{Memory: "Option B was selected.", Score: 0.9}}

	first, firstKey, source, err := resolveLongMemEvalAnswer(
		context.Background(), llm, "answer-model", "glm", inst, hits, cache, "",
	)
	if err != nil {
		t.Fatalf("resolve first answer: %v", err)
	}
	if first != "Option B" || firstKey == "" || source != lmeAnswerSourceModel {
		t.Fatalf("first answer = %q key=%q source=%q", first, firstKey, source)
	}

	// Similarity scores and storage IDs are not shown to the answer model and
	// therefore must not split otherwise identical answer inputs.
	secondHits := []memoryHit{{
		ID: "different-storage-id", Memory: hits[0].Memory, Score: 0.1,
	}}
	second, secondKey, source, err := resolveLongMemEvalAnswer(
		context.Background(), llm, "answer-model", "glm", inst, secondHits, cache, "",
	)
	if err != nil {
		t.Fatalf("resolve duplicate answer: %v", err)
	}
	if second != first || secondKey != firstKey ||
		source != lmeAnswerSourceCurrentRun || llm.calls != 1 {
		t.Fatalf(
			"duplicate answer = %q key=%q source=%q calls=%d",
			second, secondKey, source, llm.calls,
		)
	}

	changed, changedKey, source, err := resolveLongMemEvalAnswer(
		context.Background(), llm, "answer-model", "glm", inst,
		[]memoryHit{{Memory: "Option A was selected."}}, cache, "",
	)
	if err != nil {
		t.Fatalf("resolve changed answer input: %v", err)
	}
	if changed != "Option A" || changedKey == firstKey ||
		source != lmeAnswerSourceModel || llm.calls != 2 {
		t.Fatalf(
			"changed answer = %q key=%q source=%q calls=%d",
			changed, changedKey, source, llm.calls,
		)
	}
}

func TestLongMemEvalAnswerCachePersists(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "answer-cache.json")
	cache, err := openLongMemEvalAnswerCache(path)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	inst := &lmeInstance{Question: "Which option?"}
	hits := []memoryHit{{Memory: "Option B was selected."}}
	firstLLM := &queuedJudgeModel{responses: []string{"Option B"}}
	first, key, _, err := resolveLongMemEvalAnswer(
		context.Background(), firstLLM, "answer-model", "glm",
		inst, hits, cache, "",
	)
	if err != nil {
		t.Fatalf("resolve persisted answer: %v", err)
	}

	loaded, err := openLongMemEvalAnswerCache(path)
	if err != nil {
		t.Fatalf("reload cache: %v", err)
	}
	secondLLM := &queuedJudgeModel{responses: []string{"Option A"}}
	second, secondKey, source, err := resolveLongMemEvalAnswer(
		context.Background(), secondLLM, "answer-model", "glm",
		inst, hits, loaded, "",
	)
	if err != nil {
		t.Fatalf("resolve loaded answer: %v", err)
	}
	if second != first || secondKey != key || source != lmeAnswerSourcePersistent ||
		secondLLM.calls != 0 || loaded.LedgerID() != cache.LedgerID() {
		t.Fatalf(
			"persistent answer = %q key=%q source=%q calls=%d ledger=%q",
			second, secondKey, source, secondLLM.calls, loaded.LedgerID(),
		)
	}
}

func TestLongMemEvalAnswerCacheSeparatesProviderAndLogicalUsage(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "answer-cache.json")
	cache, err := openLongMemEvalAnswerCache(path)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	inst := &lmeInstance{Question: "Which option?"}
	hits := []memoryHit{{Memory: "Option B was selected."}}
	usage := &model.Usage{
		PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12,
	}
	firstBase := &queuedJudgeModel{
		responses: []string{"Option B"},
		usage:     usage,
	}
	firstTracker := &lmeTokenTracker{}
	firstLLM := &lmeTrackingModel{
		base: firstBase, tracker: firstTracker,
	}
	first, key, source, attempts, providerUsage, err :=
		resolveLongMemEvalAnswerWithRetries(
			context.Background(), firstLLM, firstTracker,
			"answer-model", "glm", inst, hits, cache, "",
		)
	if err != nil {
		t.Fatalf("resolve first answer: %v", err)
	}
	if first != "Option B" || key == "" ||
		source != lmeAnswerSourceModel || firstBase.calls != 1 {
		t.Fatalf(
			"first answer=%q key=%q source=%q calls=%d",
			first, key, source, firstBase.calls,
		)
	}
	if len(attempts) != 1 || attempts[0].TokenUsage == nil ||
		attempts[0].TokenUsage.TotalTokens != 12 ||
		attempts[0].LogicalTokenUsage == nil ||
		attempts[0].LogicalTokenUsage.TotalTokens != 12 ||
		!attempts[0].LogicalUsageComplete ||
		providerUsage.TotalTokens != 12 {
		t.Fatalf(
			"first attempts=%#v provider=%+v",
			attempts, providerUsage,
		)
	}

	loaded, err := openLongMemEvalAnswerCache(path)
	if err != nil {
		t.Fatalf("reload cache: %v", err)
	}
	secondBase := &queuedJudgeModel{responses: []string{"Option A"}, usage: usage}
	secondTracker := &lmeTokenTracker{}
	secondLLM := &lmeTrackingModel{
		base: secondBase, tracker: secondTracker,
	}
	second, secondKey, source, attempts, providerUsage, err :=
		resolveLongMemEvalAnswerWithRetries(
			context.Background(), secondLLM, secondTracker,
			"answer-model", "glm", inst, hits, loaded, "",
		)
	if err != nil {
		t.Fatalf("resolve cached answer: %v", err)
	}
	if second != first || secondKey != key ||
		source != lmeAnswerSourcePersistent || secondBase.calls != 0 {
		t.Fatalf(
			"cached answer=%q key=%q source=%q calls=%d",
			second, secondKey, source, secondBase.calls,
		)
	}
	if !providerUsage.IsZero() || len(attempts) != 1 ||
		attempts[0].TokenUsage != nil ||
		attempts[0].LogicalTokenUsage == nil ||
		attempts[0].LogicalTokenUsage.TotalTokens != 12 ||
		!attempts[0].LogicalUsageComplete ||
		len(attempts[0].ModelCalls) != 1 ||
		attempts[0].ModelCalls[0].Source != lmeAnswerSourcePersistent {
		t.Fatalf(
			"cached attempts=%#v provider=%+v",
			attempts, providerUsage,
		)
	}
	if loaded.logicalUsageHits != 1 ||
		loaded.logicalUsageMissingHits != 0 {
		t.Fatalf(
			"logical cache counters: hits=%d missing=%d",
			loaded.logicalUsageHits, loaded.logicalUsageMissingHits,
		)
	}
	entry := loaded.file.Entries[key]
	tamperedUsage := *entry.LogicalTokenUsage
	tamperedUsage.TotalTokens++
	entry.LogicalTokenUsage = &tamperedUsage
	if err := validateLongMemEvalAnswerCacheEntry(key, entry); err == nil ||
		!strings.Contains(err.Error(), "does not match model calls") {
		t.Fatalf("tampered logical usage error = %v", err)
	}
}

func TestResolveLongMemEvalAnswerDoesNotCacheFailures(t *testing.T) {
	t.Parallel()

	cache, err := openLongMemEvalAnswerCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	_, key, source, err := resolveLongMemEvalAnswer(
		context.Background(), failingAnswerModel{}, "answer-model", "glm",
		&lmeInstance{Question: "Which option?"}, nil, cache, "",
	)
	if err == nil || key == "" || source != lmeAnswerSourceModel || cache.Len() != 0 {
		t.Fatalf("failed answer key=%q source=%q entries=%d err=%v", key, source, cache.Len(), err)
	}
}

func TestResolveLongMemEvalAnswerRetriesTruncatedRepair(t *testing.T) {
	t.Parallel()

	cache, err := openLongMemEvalAnswerCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	length := "length"
	usage := func() *model.Usage {
		return &model.Usage{
			PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12,
		}
	}
	base := &queuedAnswerModel{responses: []*model.Response{
		{
			Choices: []model.Choice{{
				Message:      model.NewAssistantMessage("partial one"),
				FinishReason: &length,
			}},
			Usage: usage(),
		},
		{
			Choices: []model.Choice{{
				Message:      model.NewAssistantMessage("partial two"),
				FinishReason: &length,
			}},
			Usage: usage(),
		},
		{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("complete answer"),
			}},
			Usage: usage(),
		},
	}}
	tracker := &lmeTokenTracker{}
	llm := &lmeTrackingModel{base: base, tracker: tracker}
	raw, key, source, attempts, total, err :=
		resolveLongMemEvalAnswerWithRetries(
			context.Background(), llm, tracker, "answer-model", "glm",
			&lmeInstance{Question: "Which option?"}, nil, cache, "",
		)
	if err != nil {
		t.Fatalf("resolve retried answer: %v", err)
	}
	if raw != "complete answer" || key == "" || source != lmeAnswerSourceModel {
		t.Fatalf("answer=%q key=%q source=%q", raw, key, source)
	}
	if len(attempts) != 2 || attempts[0].Error == "" ||
		attempts[1].Error != "" {
		t.Fatalf("attempts = %#v", attempts)
	}
	if len(attempts[0].ModelCalls) != 2 ||
		len(attempts[1].ModelCalls) != 1 ||
		len(longMemEvalAnswerAttemptCalls(attempts)) != 3 ||
		total.LLMCalls != 3 || total.TotalTokens != 36 {
		t.Fatalf("attempt usage=%#v total=%+v", attempts, total)
	}
	if len(base.requests) != 3 || cache.Len() != 1 {
		t.Fatalf("requests=%d cache entries=%d", len(base.requests), cache.Len())
	}
}

func TestResolveLongMemEvalAnswerBoundsTruncatedRepairRetries(t *testing.T) {
	t.Parallel()

	cache, err := openLongMemEvalAnswerCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	length := "length"
	usage := func() *model.Usage {
		return &model.Usage{
			PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12,
		}
	}
	responses := make([]*model.Response, 0, 2*(1+lmeAnswerMaxExtraAttempts))
	for i := range cap(responses) {
		responses = append(responses, &model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage(
					fmt.Sprintf("partial %d", i+1),
				),
				FinishReason: &length,
			}},
			Usage: usage(),
		})
	}
	base := &queuedAnswerModel{responses: responses}
	tracker := &lmeTokenTracker{}
	llm := &lmeTrackingModel{base: base, tracker: tracker}
	raw, _, _, attempts, total, err := resolveLongMemEvalAnswerWithRetries(
		context.Background(), llm, tracker, "answer-model", "glm",
		&lmeInstance{Question: "Which option?"}, nil, cache, "",
	)
	if !errors.Is(err, errLongMemEvalAnswerTruncated) {
		t.Fatalf("resolve truncated answer: %v", err)
	}
	if raw != "partial 6" {
		t.Fatalf("answer = %q", raw)
	}
	if len(attempts) != 1+lmeAnswerMaxExtraAttempts {
		t.Fatalf("attempts = %#v", attempts)
	}
	for i, attempt := range attempts {
		if attempt.Error == "" || len(attempt.ModelCalls) != 2 {
			t.Fatalf("attempt %d = %#v", i, attempt)
		}
	}
	if len(base.requests) != 6 ||
		len(longMemEvalAnswerAttemptCalls(attempts)) != 6 ||
		total.LLMCalls != 6 || total.TotalTokens != 72 ||
		cache.Len() != 0 {
		t.Fatalf(
			"requests=%d attempts=%#v total=%+v cache entries=%d",
			len(base.requests), attempts, total, cache.Len(),
		)
	}
}

func TestResolveLongMemEvalAnswerRetriesTransientFailure(t *testing.T) {
	t.Parallel()

	base := &queuedAnswerModel{responses: []*model.Response{
		{Error: &model.ResponseError{Message: "temporary failure"}},
		{Choices: []model.Choice{{
			Message: model.NewAssistantMessage("complete answer"),
		}}},
	}}
	tracker := &lmeTokenTracker{}
	llm := &lmeTrackingModel{base: base, tracker: tracker}
	cache, err := openLongMemEvalAnswerCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	raw, _, _, attempts, _, err := resolveLongMemEvalAnswerWithRetries(
		context.Background(), llm, tracker, "answer-model", "glm",
		&lmeInstance{Question: "Which option?"}, nil, cache, "",
	)
	if err != nil || raw != "complete answer" {
		t.Fatalf("answer = %q, err = %v", raw, err)
	}
	if len(attempts) != 2 || attempts[0].Error == "" ||
		attempts[1].Error != "" || len(base.requests) != 2 {
		t.Fatalf("attempts = %#v, requests = %d", attempts, len(base.requests))
	}
}

func TestResolveLongMemEvalAnswerRetriesMalformedRepair(t *testing.T) {
	t.Parallel()

	length := "length"
	stop := "stop"
	base := &queuedAnswerModel{responses: []*model.Response{
		{Choices: []model.Choice{{
			Message:      model.NewAssistantMessage("partial"),
			FinishReason: &length,
		}}},
		{Choices: []model.Choice{{
			Message:      model.NewAssistantMessage("not JSON"),
			FinishReason: &stop,
		}}},
		{Choices: []model.Choice{{
			Message: model.NewAssistantMessage("late answer"),
		}}},
	}}
	tracker := &lmeTokenTracker{}
	llm := &lmeTrackingModel{base: base, tracker: tracker}
	cache, err := openLongMemEvalAnswerCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	raw, _, _, attempts, _, err := resolveLongMemEvalAnswerWithRetries(
		context.Background(), llm, tracker, "answer-model", "glm",
		&lmeInstance{Question: "Which option?"}, nil, cache, "",
	)
	if raw != "late answer" || err != nil {
		t.Fatalf("answer = %q, err = %v", raw, err)
	}
	if len(attempts) != 2 || attempts[0].Error == "" ||
		attempts[1].Error != "" || len(base.requests) != 3 ||
		len(base.responses) != 0 || cache.Len() != 1 {
		t.Fatalf("attempts=%#v requests=%d responses=%d cache=%d",
			attempts, len(base.requests), len(base.responses), cache.Len())
	}
}

func TestResolveLongMemEvalAnswerRetriesEmptyAttempts(t *testing.T) {
	t.Parallel()

	base := &queuedAnswerModel{responses: []*model.Response{{}, {}, {
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("late answer"),
		}},
	}}}
	tracker := &lmeTokenTracker{}
	llm := &lmeTrackingModel{base: base, tracker: tracker}
	cache, err := openLongMemEvalAnswerCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	raw, _, _, attempts, _, err := resolveLongMemEvalAnswerWithRetries(
		context.Background(), llm, tracker, "answer-model", "glm",
		&lmeInstance{Question: "Which option?"}, nil, cache, "",
	)
	if raw != "late answer" || err != nil {
		t.Fatalf("answer = %q, err = %v", raw, err)
	}
	if len(attempts) != 2 || attempts[0].Error == "" ||
		attempts[1].Error != "" || len(base.requests) != 3 ||
		len(base.responses) != 0 || cache.Len() != 1 {
		t.Fatalf("attempts=%#v requests=%d responses=%d cache=%d",
			attempts, len(base.requests), len(base.responses), cache.Len())
	}
}

func TestReanswerLongMemEvalResultSeedsCompatibleAnswerCache(t *testing.T) {
	t.Parallel()

	backend := func(name string) *backendResult {
		return &backendResult{
			Backend:   name,
			Answer:    "Option B",
			RawAnswer: "Option B",
			Retrieval: []memoryHit{{Memory: "Option B was selected."}},
			TokenUsage: &lmeTokenUsage{
				PromptTokens: 20, CompletionTokens: 2, TotalTokens: 22, LLMCalls: 1,
			},
			AnswerUsage: &lmeTokenUsage{
				PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12, LLMCalls: 1,
			},
		}
	}
	result := &runResult{
		Metadata: map[string]any{
			"model":                 "answer-model",
			"model_variant":         "glm",
			"answer_prompt_version": lmeAnswerPromptVersion,
			"answer_generation":     currentLongMemEvalAnswerGeneration(),
		},
		Cases: []*caseResult{{
			QuestionID: "q-seed",
			Question:   "Which option?",
			Answer:     "Option B",
			BackendResults: map[string]*backendResult{
				"mem0":     backend("mem0"),
				"pgvector": backend("pgvector"),
			},
		}},
	}
	cache, err := openLongMemEvalAnswerCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	llm := &queuedJudgeModel{responses: []string{"Option A"}}
	outPath := filepath.Join(t.TempDir(), "reanswered_results.json")
	if err := reanswerLongMemEvalResult(
		context.Background(), result, llm, "answer-model", "glm", cache, true, outPath,
	); err != nil {
		t.Fatalf("seed answer cache: %v", err)
	}
	if llm.calls != 0 || cache.Len() != 1 || cache.Hits() != 1 {
		t.Fatalf("seed calls=%d entries=%d hits=%d", llm.calls, cache.Len(), cache.Hits())
	}
	mem0 := result.Cases[0].BackendResults["mem0"]
	pgvector := result.Cases[0].BackendResults["pgvector"]
	if mem0.AnswerSource != lmeAnswerSourceExisting ||
		pgvector.AnswerSource != lmeAnswerSourceCurrentRun ||
		mem0.AnswerCacheKey == "" || pgvector.AnswerCacheKey != mem0.AnswerCacheKey {
		t.Fatalf("seeded answer provenance: mem0=%+v pgvector=%+v", mem0, pgvector)
	}
	if mem0.AnswerUsage != nil || mem0.TokenUsage == nil ||
		mem0.TokenUsage.TotalTokens != 10 || mem0.TokenUsage.LLMCalls != 0 {
		t.Fatalf("seeded answer usage: total=%+v answer=%+v", mem0.TokenUsage, mem0.AnswerUsage)
	}
	if result.Metadata["answer_cache_initial_entries"] != 0 ||
		result.Metadata["answer_cache_final_entries"] != 1 ||
		result.Metadata["answer_cache_hits"] != 1 {
		t.Fatalf("answer cache metadata: %#v", result.Metadata)
	}
	if result.Metadata["reanswer_reuse_source_answers"] != true {
		t.Fatalf("source-answer reuse metadata: %#v", result.Metadata)
	}
}

func TestReanswerLongMemEvalResultCanDisableSourceAnswerReuse(t *testing.T) {
	t.Parallel()

	result := &runResult{
		Metadata: map[string]any{
			"model":                 "answer-model",
			"model_variant":         "glm",
			"answer_prompt_version": lmeAnswerPromptVersion,
			"answer_generation":     currentLongMemEvalAnswerGeneration(),
		},
		Cases: []*caseResult{{
			QuestionID: "q-independent-answer",
			Question:   "Which option?",
			Answer:     "Option A",
			BackendResults: map[string]*backendResult{
				"mem0": {
					Backend:   "mem0",
					Answer:    "Option B",
					RawAnswer: "Option B",
					Retrieval: []memoryHit{{Memory: "Option A was selected."}},
				},
			},
		}},
	}
	cache, err := openLongMemEvalAnswerCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	llm := &queuedJudgeModel{responses: []string{"Option A"}}
	outPath := filepath.Join(t.TempDir(), "reanswered_results.json")
	if err := reanswerLongMemEvalResult(
		context.Background(), result, llm, "answer-model", "glm", cache, false, outPath,
	); err != nil {
		t.Fatalf("independent re-answer: %v", err)
	}

	backend := result.Cases[0].BackendResults["mem0"]
	if llm.calls != 1 || backend.Answer != "Option A" ||
		backend.AnswerSource != lmeAnswerSourceModel ||
		cache.Len() != 1 || cache.Hits() != 0 {
		t.Fatalf("independent answer calls=%d backend=%+v entries=%d hits=%d",
			llm.calls, backend, cache.Len(), cache.Hits())
	}
	if result.Metadata["reanswer_reuse_source_answers"] != false {
		t.Fatalf("source-answer reuse metadata: %#v", result.Metadata)
	}
}

func TestLongMemEvalAnswerProvenanceMatchesStrictly(t *testing.T) {
	t.Parallel()

	compatible := func() map[string]any {
		return map[string]any{
			"model":                 "answer-model",
			"model_variant":         "GLM",
			"answer_prompt_version": lmeAnswerPromptVersion,
			"answer_generation":     currentLongMemEvalAnswerGeneration(),
		}
	}
	if !longMemEvalAnswerProvenanceMatches(compatible(), "answer-model", "glm") {
		t.Fatal("compatible answer provenance did not match")
	}
	for _, mutate := range []func(map[string]any){
		func(metadata map[string]any) { metadata["model"] = "other-model" },
		func(metadata map[string]any) { metadata["model_variant"] = "openai" },
		func(metadata map[string]any) { metadata["answer_top_k"] = float64(10) },
		func(metadata map[string]any) { metadata["answer_prompt_version"] = "old-prompt" },
		func(metadata map[string]any) {
			generation := currentLongMemEvalAnswerGeneration()
			generation.PrimaryMaxTokens++
			metadata["answer_generation"] = generation
		},
		func(metadata map[string]any) { delete(metadata, "answer_generation") },
	} {
		metadata := compatible()
		mutate(metadata)
		if longMemEvalAnswerProvenanceMatches(metadata, "answer-model", "glm") {
			t.Fatalf("incompatible answer provenance matched: %#v", metadata)
		}
	}
}

func TestOpenLongMemEvalAnswerCacheRejectsInvalidFiles(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		value     any
		wantError string
	}{
		{name: "invalid json", value: []byte("{"), wantError: "parse LongMemEval answer cache"},
		{
			name: "legacy version",
			value: lmeAnswerCacheFile{
				Version: "lme-answer-cache-v1", LedgerID: "ledger",
				Entries: map[string]lmeAnswerCacheEntry{},
			},
			wantError: "unsupported LongMemEval answer cache version",
		},
		{
			name: "unsupported version",
			value: lmeAnswerCacheFile{
				Version: "future-version", LedgerID: "ledger",
				Entries: map[string]lmeAnswerCacheEntry{},
			},
			wantError: "unsupported LongMemEval answer cache version",
		},
		{
			name: "missing ledger id",
			value: lmeAnswerCacheFile{
				Version: lmeAnswerCacheFormatVersion,
				Entries: map[string]lmeAnswerCacheEntry{},
			},
			wantError: "missing ledger_id",
		},
		{
			name: "invalid entry key",
			value: lmeAnswerCacheFile{
				Version:  lmeAnswerCacheFormatVersion,
				LedgerID: "ledger",
				Entries: map[string]lmeAnswerCacheEntry{
					"wrong-key": {
						Identity: lmeAnswerCacheIdentity{Model: "answer-model"},
						Answer:   "Option B",
					},
				},
			},
			wantError: "key mismatch",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "answer-cache.json")
			var data []byte
			if raw, ok := test.value.([]byte); ok {
				data = raw
			} else {
				var err error
				data, err = json.Marshal(test.value)
				if err != nil {
					t.Fatalf("marshal cache: %v", err)
				}
			}
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatalf("write cache: %v", err)
			}
			_, err := openLongMemEvalAnswerCache(path)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("open invalid cache error = %v, want %q", err, test.wantError)
			}
		})
	}
}

type failingAnswerModel struct{}

func (failingAnswerModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	return nil, errors.New("answer failed")
}

func (failingAnswerModel) Info() model.Info { return model.Info{} }
