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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestLongMemEvalModelResponseCacheKeyIsStable(t *testing.T) {
	t.Parallel()

	first := newLongMemEvalModelCacheTestRequest(false)
	second := newLongMemEvalModelCacheTestRequest(true)
	firstIdentity, firstKey, err := longMemEvalModelResponseCacheKey(
		first, "glm52", "GLM",
	)
	if err != nil {
		t.Fatalf("first cache key: %v", err)
	}
	secondIdentity, secondKey, err := longMemEvalModelResponseCacheKey(
		second, "glm52", "glm",
	)
	if err != nil {
		t.Fatalf("second cache key: %v", err)
	}
	if firstKey != secondKey || firstIdentity != secondIdentity {
		t.Fatalf(
			"equivalent requests produced different identities:\nfirst=%#v %s\nsecond=%#v %s",
			firstIdentity, firstKey, secondIdentity, secondKey,
		)
	}
}

func TestLongMemEvalModelResponseCacheKeyCoversRequestSemantics(t *testing.T) {
	t.Parallel()

	base := newLongMemEvalModelCacheTestRequest(false)
	_, baseKey, err := longMemEvalModelResponseCacheKey(base, "glm52", "glm")
	if err != nil {
		t.Fatalf("base cache key: %v", err)
	}
	tests := map[string]struct {
		request      *model.Request
		modelName    string
		modelVariant string
	}{
		"message": {
			request:      newLongMemEvalModelCacheTestRequest(false),
			modelName:    "glm52",
			modelVariant: "glm",
		},
		"generation": {
			request:      newLongMemEvalModelCacheTestRequest(false),
			modelName:    "glm52",
			modelVariant: "glm",
		},
		"header": {
			request:      newLongMemEvalModelCacheTestRequest(false),
			modelName:    "glm52",
			modelVariant: "glm",
		},
		"extra field": {
			request:      newLongMemEvalModelCacheTestRequest(false),
			modelName:    "glm52",
			modelVariant: "glm",
		},
		"tool declaration": {
			request:      newLongMemEvalModelCacheTestRequest(false),
			modelName:    "glm52",
			modelVariant: "glm",
		},
		"model": {
			request:      newLongMemEvalModelCacheTestRequest(false),
			modelName:    "glm53",
			modelVariant: "glm",
		},
		"variant": {
			request:      newLongMemEvalModelCacheTestRequest(false),
			modelName:    "glm52",
			modelVariant: "openai",
		},
	}
	tests["message"].request.Messages[0].Content = "different prompt"
	maxTokens := 99
	tests["generation"].request.MaxTokens = &maxTokens
	tests["header"].request.Headers["Authorization"] = "different secret"
	tests["extra field"].request.ExtraFields["reasoning"] = "high"
	tests["tool declaration"].request.Tools["memory_add"] = lmeCacheTestTool{
		declaration: &tool.Declaration{
			Name:        "memory_add",
			Description: "A changed declaration",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, key, err := longMemEvalModelResponseCacheKey(
				test.request, test.modelName, test.modelVariant,
			)
			if err != nil {
				t.Fatalf("cache key: %v", err)
			}
			if key == baseKey {
				t.Fatalf("%s did not change the cache key", name)
			}
		})
	}
}

func TestLongMemEvalTrackingModelPersistsAndReplaysResponse(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "model-responses.json")
	cache, err := openLongMemEvalModelResponseCache(path)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	finishReason := "tool_calls"
	base := &lmeCacheTestModel{responses: []*model.Response{{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "stored response",
				ToolCalls: []model.ToolCall{{
					Function: model.FunctionDefinitionParam{
						Name:      "memory_add",
						Arguments: []byte(`{"memory":"Likes tea"}`),
					},
				}},
			},
			FinishReason: &finishReason,
		}},
		Usage: &model.Usage{
			PromptTokens:     10,
			CompletionTokens: 2,
			TotalTokens:      12,
		},
	}}}
	request := newLongMemEvalModelCacheTestRequest(false)
	tracker := &lmeTokenTracker{}
	wrapped := &lmeTrackingModel{
		base:          base,
		tracker:       tracker,
		responseCache: cache,
		modelName:     "glm52",
		modelVariant:  "glm",
	}
	responses := consumeLongMemEvalModelResponses(t, wrapped, request)
	if base.calls != 1 || len(responses) != 1 || responses[0].Usage == nil {
		t.Fatalf(
			"first call: base calls=%d responses=%d usage=%#v",
			base.calls, len(responses), responses[0].Usage,
		)
	}
	usage := tracker.Snapshot()
	if usage.LLMCalls != 1 || usage.TotalTokens != 12 {
		t.Fatalf("first-call usage = %#v", usage)
	}
	firstCalls := tracker.SnapshotCalls()
	if len(firstCalls) != 1 || firstCalls[0].Source != lmeModelCallSourceModel ||
		firstCalls[0].CacheKey == "" || firstCalls[0].CacheError != "" {
		t.Fatalf("first-call trace = %#v", firstCalls)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	for _, forbidden := range []string{
		"private user prompt", "Bearer private-header-secret",
	} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("cache leaked request material %q", forbidden)
		}
	}

	reopened, err := openLongMemEvalModelResponseCache(path)
	if err != nil {
		t.Fatalf("reopen cache: %v", err)
	}
	replayBase := &lmeCacheTestModel{err: errors.New("base model must not run")}
	replayTracker := &lmeTokenTracker{}
	replay := &lmeTrackingModel{
		base:          replayBase,
		tracker:       replayTracker,
		responseCache: reopened,
		modelName:     "glm52",
		modelVariant:  "glm",
	}
	replayed := consumeLongMemEvalModelResponses(t, replay, request)
	if replayBase.calls != 0 || len(replayed) != 1 {
		t.Fatalf(
			"replay: base calls=%d responses=%d",
			replayBase.calls, len(replayed),
		)
	}
	if replayed[0].Usage != nil ||
		replayed[0].Choices[0].Message.Content != "stored response" ||
		string(replayed[0].Choices[0].Message.ToolCalls[0].Function.Arguments) !=
			`{"memory":"Likes tea"}` {
		t.Fatalf("replayed response = %#v", replayed[0])
	}
	if got := replayTracker.Snapshot(); !got.IsZero() {
		t.Fatalf("replay usage = %#v, want zero", got)
	}
	replayCalls := replayTracker.SnapshotCalls()
	if len(replayCalls) != 1 ||
		replayCalls[0].Source != lmeModelCallSourcePersistent ||
		replayCalls[0].CacheKey != firstCalls[0].CacheKey ||
		replayCalls[0].ToolCalls[0].Name != "memory_add" {
		t.Fatalf("replay trace = %#v", replayCalls)
	}
	hits, misses, cacheErrors := reopened.Stats()
	if hits != 1 || misses != 0 || cacheErrors != 0 {
		t.Fatalf(
			"replay stats = hits:%d misses:%d errors:%d",
			hits, misses, cacheErrors,
		)
	}
}

func TestLongMemEvalTrackingModelDoesNotCacheErrors(t *testing.T) {
	t.Parallel()

	cache, err := openLongMemEvalModelResponseCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	base := &lmeCacheTestModel{responses: []*model.Response{{
		Error: &model.ResponseError{Message: "rate limited"},
	}}}
	tracker := &lmeTokenTracker{}
	wrapped := &lmeTrackingModel{
		base:          base,
		tracker:       tracker,
		responseCache: cache,
		modelName:     "glm52",
		modelVariant:  "glm",
	}
	consumeLongMemEvalModelResponses(
		t, wrapped, newLongMemEvalModelCacheTestRequest(false),
	)
	if cache.Len() != 0 {
		t.Fatalf("cache entries = %d, want zero", cache.Len())
	}
	calls := tracker.SnapshotCalls()
	if len(calls) != 1 || calls[0].Error != "rate limited" {
		t.Fatalf("error trace = %#v", calls)
	}
}

func TestOpenLongMemEvalModelResponseCacheRejectsMismatchedKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "model-responses.json")
	cache, err := openLongMemEvalModelResponseCache(path)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	identity, key, err := longMemEvalModelResponseCacheKey(
		newLongMemEvalModelCacheTestRequest(false), "glm52", "glm",
	)
	if err != nil {
		t.Fatalf("cache key: %v", err)
	}
	if err := cache.Put(key, identity, []*model.Response{{Done: true}}); err != nil {
		t.Fatalf("put cache entry: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	corrupted := strings.Replace(
		string(data), key, strings.Repeat("0", len(key)), 1,
	)
	if err := os.WriteFile(path, []byte(corrupted), 0644); err != nil {
		t.Fatalf("corrupt cache: %v", err)
	}
	if _, err := openLongMemEvalModelResponseCache(path); err == nil ||
		!strings.Contains(err.Error(), "key mismatch") {
		t.Fatalf("reopen error = %v, want key mismatch", err)
	}
}

func TestValidateLongMemEvalComparisonRequiresSharedModelResponseLedger(t *testing.T) {
	t.Parallel()

	newResult := func(implementation string) *runResult {
		metadata := testLongMemEvalComparisonMetadata(implementation)
		metadata["model_response_cache_format_version"] = lmeModelCacheFormatVersion
		metadata["model_response_cache_shared"] = true
		metadata["model_response_cache_ledger_id"] = "shared-model-ledger"
		metadata["model_response_cache_errors"] = 0
		metadata["user_scope"] = "paired-ablation"
		metadata["user_scope_explicit"] = true
		return &runResult{Metadata: metadata}
	}
	for _, test := range []struct {
		name      string
		mutate    func(map[string]any)
		wantError string
	}{
		{
			name: "different ledger",
			mutate: func(metadata map[string]any) {
				metadata["model_response_cache_ledger_id"] = "different-ledger"
			},
			wantError: "model_response_cache_ledger_id",
		},
		{
			name: "missing provenance",
			mutate: func(metadata map[string]any) {
				delete(metadata, "model_response_cache_format_version")
			},
			wantError: "model_response_cache_format_version",
		},
		{
			name: "ephemeral cache",
			mutate: func(metadata map[string]any) {
				metadata["model_response_cache_shared"] = false
			},
			wantError: "model_response_cache_shared",
		},
		{
			name: "cache error",
			mutate: func(metadata map[string]any) {
				metadata["model_response_cache_errors"] = 1
			},
			wantError: "model_response_cache_errors",
		},
		{
			name: "different user scope",
			mutate: func(metadata map[string]any) {
				metadata["user_scope"] = "different-scope"
			},
			wantError: "user_scope",
		},
		{
			name: "implicit user scope",
			mutate: func(metadata map[string]any) {
				metadata["user_scope_explicit"] = false
			},
			wantError: "user_scope_explicit",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := newResult("upstream-main")
			candidate := newResult("candidate-2196")
			test.mutate(candidate.Metadata)
			err := validateLongMemEvalComparison(baseline, candidate)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("shared model ledger error = %v, want %q", err, test.wantError)
			}
		})
	}

	baseline := newResult("upstream-main")
	candidate := newResult("candidate-2196")
	baseline.Metadata["model_response_cache_shared"] = false
	candidate.Metadata["model_response_cache_shared"] = false
	err := validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "requires a shared model response cache") {
		t.Fatalf("unshared model response cache error = %v", err)
	}

	baseline = newResult("upstream-main")
	candidate = newResult("candidate-2196")
	baseline.Metadata["model_response_cache_errors"] = float64(1)
	candidate.Metadata["model_response_cache_errors"] = float64(1)
	err = validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "requires zero") {
		t.Fatalf("non-zero model response cache error = %v", err)
	}

	baseline = newResult("upstream-main")
	candidate = newResult("candidate-2196")
	baseline.Metadata["user_scope_explicit"] = false
	candidate.Metadata["user_scope_explicit"] = false
	err = validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "explicit shared user scope") {
		t.Fatalf("implicit user scope error = %v", err)
	}
}

func newLongMemEvalModelCacheTestRequest(reverse bool) *model.Request {
	temperature := 0.0
	request := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("private user prompt"),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			Stream:      true,
			Stop:        []string{"stop"},
		},
		ExtraFields: map[string]any{
			"reasoning": "low",
			"nested": map[string]any{
				"enabled": true,
			},
		},
		Headers: map[string]string{
			"Authorization":  "Bearer private-header-secret",
			"X-Request-Mode": "benchmark",
		},
		Tools: make(map[string]tool.Tool),
	}
	add := lmeCacheTestTool{declaration: &tool.Declaration{
		Name:        "memory_add",
		Description: "Add a memory",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"memory": {Type: "string"},
			},
			Required: []string{"memory"},
		},
	}}
	update := lmeCacheTestTool{declaration: &tool.Declaration{
		Name:        "memory_update",
		Description: "Update a memory",
		InputSchema: &tool.Schema{Type: "object"},
	}}
	if reverse {
		request.Tools["memory_update"] = update
		request.Tools["memory_add"] = add
	} else {
		request.Tools["memory_add"] = add
		request.Tools["memory_update"] = update
	}
	return request
}

func consumeLongMemEvalModelResponses(
	t *testing.T,
	llm model.Model,
	request *model.Request,
) []*model.Response {
	t.Helper()
	responseChannel, err := llm.GenerateContent(context.Background(), request)
	if err != nil {
		t.Fatalf("generate content: %v", err)
	}
	var responses []*model.Response
	for response := range responseChannel {
		responses = append(responses, response)
	}
	return responses
}

type lmeCacheTestTool struct {
	declaration *tool.Declaration
}

func (t lmeCacheTestTool) Declaration() *tool.Declaration {
	return t.declaration
}

type lmeCacheTestModel struct {
	responses []*model.Response
	err       error
	calls     int
}

func (m *lmeCacheTestModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	responses := make(chan *model.Response, len(m.responses))
	for _, response := range m.responses {
		responses <- response
	}
	close(responses)
	return responses, nil
}

func (*lmeCacheTestModel) Info() model.Info {
	return model.Info{Name: "glm52"}
}
