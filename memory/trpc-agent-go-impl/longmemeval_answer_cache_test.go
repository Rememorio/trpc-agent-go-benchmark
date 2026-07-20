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
