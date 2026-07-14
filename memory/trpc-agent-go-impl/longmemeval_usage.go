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
	"net/http"
	"sync"
	"time"

	embeddingopenai "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const lmeMem0UsageHeader = "X-Mem0-Usage"

type lmeTokenUsage struct {
	PromptTokens        int     `json:"prompt_tokens"`
	CompletionTokens    int     `json:"completion_tokens"`
	TotalTokens         int     `json:"total_tokens"`
	CachedTokens        int     `json:"cached_tokens,omitempty"`
	CacheCreationTokens int     `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int     `json:"cache_read_tokens,omitempty"`
	ReasoningTokens     int     `json:"reasoning_tokens,omitempty"`
	LLMCalls            int     `json:"llm_calls"`
	CacheHitRate        float64 `json:"cache_hit_rate,omitempty"`
}

func (u *lmeTokenUsage) Add(other lmeTokenUsage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
	u.CachedTokens += other.CachedTokens
	u.CacheCreationTokens += other.CacheCreationTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.ReasoningTokens += other.ReasoningTokens
	u.LLMCalls += other.LLMCalls
	u.setCacheHitRate()
}

func (u *lmeTokenUsage) Sub(other lmeTokenUsage) {
	u.PromptTokens = nonNegativeTokenDifference(u.PromptTokens, other.PromptTokens)
	u.CompletionTokens = nonNegativeTokenDifference(u.CompletionTokens, other.CompletionTokens)
	u.TotalTokens = nonNegativeTokenDifference(u.TotalTokens, other.TotalTokens)
	u.CachedTokens = nonNegativeTokenDifference(u.CachedTokens, other.CachedTokens)
	u.CacheCreationTokens = nonNegativeTokenDifference(u.CacheCreationTokens, other.CacheCreationTokens)
	u.CacheReadTokens = nonNegativeTokenDifference(u.CacheReadTokens, other.CacheReadTokens)
	u.ReasoningTokens = nonNegativeTokenDifference(u.ReasoningTokens, other.ReasoningTokens)
	u.LLMCalls = nonNegativeTokenDifference(u.LLMCalls, other.LLMCalls)
	u.setCacheHitRate()
}

func nonNegativeTokenDifference(total, part int) int {
	if part >= total {
		return 0
	}
	return total - part
}

func (u *lmeTokenUsage) setCacheHitRate() {
	if u.PromptTokens <= 0 {
		u.CacheHitRate = 0
		return
	}
	u.CacheHitRate = float64(u.CachedTokens) / float64(u.PromptTokens)
}

func (u lmeTokenUsage) IsZero() bool {
	return u.PromptTokens == 0 &&
		u.CompletionTokens == 0 &&
		u.TotalTokens == 0 &&
		u.CachedTokens == 0 &&
		u.CacheCreationTokens == 0 &&
		u.CacheReadTokens == 0 &&
		u.ReasoningTokens == 0 &&
		u.LLMCalls == 0
}

func tokenUsagePtr(u lmeTokenUsage) *lmeTokenUsage {
	if u.IsZero() {
		return nil
	}
	u.setCacheHitRate()
	return &u
}

type lmeEmbeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
	Calls        int `json:"calls"`
}

func (u *lmeEmbeddingUsage) Add(other lmeEmbeddingUsage) {
	u.PromptTokens += other.PromptTokens
	u.TotalTokens += other.TotalTokens
	u.Calls += other.Calls
}

func (u lmeEmbeddingUsage) IsZero() bool {
	return u.PromptTokens == 0 && u.TotalTokens == 0 && u.Calls == 0
}

func embeddingUsagePtr(u lmeEmbeddingUsage) *lmeEmbeddingUsage {
	if u.IsZero() {
		return nil
	}
	return &u
}

type lmeProviderUsage struct {
	LLM       lmeTokenUsage     `json:"llm"`
	Embedding lmeEmbeddingUsage `json:"embedding"`
	Reported  bool              `json:"reported"`
	Error     string            `json:"error,omitempty"`
}

type lmeTokenTracker struct {
	mu    sync.Mutex
	usage lmeTokenUsage
}

func (t *lmeTokenTracker) Record(u *model.Usage) {
	if t == nil || u == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usage.PromptTokens += u.PromptTokens
	t.usage.CompletionTokens += u.CompletionTokens
	t.usage.TotalTokens += u.TotalTokens
	t.usage.CachedTokens += u.PromptTokensDetails.CachedTokens
	t.usage.CacheCreationTokens += u.PromptTokensDetails.CacheCreationTokens
	t.usage.CacheReadTokens += u.PromptTokensDetails.CacheReadTokens
	t.usage.ReasoningTokens += u.CompletionTokensDetails.ReasoningTokens
	t.usage.LLMCalls++
	t.usage.setCacheHitRate()
}

func (t *lmeTokenTracker) Snapshot() lmeTokenUsage {
	if t == nil {
		return lmeTokenUsage{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := t.usage
	t.usage = lmeTokenUsage{}
	return snap
}

type lmeTrackingModel struct {
	base    model.Model
	tracker *lmeTokenTracker
	timeout time.Duration
}

func (m *lmeTrackingModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	req = longMemEvalDeterministicRequest(req)
	var cancel context.CancelFunc
	if m.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, m.timeout)
	}
	respCh, err := m.base.GenerateContent(ctx, req)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	out := make(chan *model.Response)
	go func() {
		if cancel != nil {
			defer cancel()
		}
		defer close(out)
		sawError := false
		for resp := range respCh {
			if resp != nil && resp.Usage != nil {
				m.tracker.Record(resp.Usage)
			}
			if resp != nil && resp.Error != nil {
				sawError = true
			}
			out <- resp
		}
		if err := ctx.Err(); err != nil && !sawError {
			out <- &model.Response{
				Object: model.ObjectTypeError,
				Done:   true,
				Error: &model.ResponseError{
					Type:    model.ErrorTypeCancelled,
					Message: lmeModelCallContextError(err, m.timeout),
				},
			}
		}
	}()
	return out, nil
}

func longMemEvalDeterministicRequest(req *model.Request) *model.Request {
	if req == nil || req.Temperature != nil {
		return req
	}
	cloned := *req
	cloned.GenerationConfig = req.GenerationConfig
	temperature := 0.0
	cloned.Temperature = &temperature
	return &cloned
}

func (m *lmeTrackingModel) Info() model.Info { return m.base.Info() }

func lmeModelCallContextError(err error, timeout time.Duration) string {
	if errors.Is(err, context.DeadlineExceeded) && timeout > 0 {
		return fmt.Sprintf("model call timed out after %s", timeout)
	}
	return fmt.Sprintf("model call canceled: %v", err)
}

type lmeTrackingEmbedder struct {
	base  *embeddingopenai.Embedder
	mu    sync.Mutex
	usage lmeEmbeddingUsage
}

func newLongMemEvalTrackingEmbedder(base *embeddingopenai.Embedder) *lmeTrackingEmbedder {
	return &lmeTrackingEmbedder{base: base}
}

func (e *lmeTrackingEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	embedding, _, err := e.GetEmbeddingWithUsage(ctx, text)
	return embedding, err
}

func (e *lmeTrackingEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	embedding, usage, err := e.base.GetEmbeddingWithUsage(ctx, text)
	if err != nil {
		return nil, nil, err
	}
	e.mu.Lock()
	e.usage.Calls++
	e.usage.PromptTokens += usageInt(usage["prompt_tokens"])
	e.usage.TotalTokens += usageInt(usage["total_tokens"])
	e.mu.Unlock()
	return embedding, usage, nil
}

func (e *lmeTrackingEmbedder) GetDimensions() int { return e.base.GetDimensions() }

func (e *lmeTrackingEmbedder) Snapshot() lmeEmbeddingUsage {
	if e == nil {
		return lmeEmbeddingUsage{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	snap := e.usage
	e.usage = lmeEmbeddingUsage{}
	return snap
}

func usageInt(value any) int {
	switch value := value.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}

type lmeProviderUsageTracker struct {
	mu    sync.Mutex
	usage lmeProviderUsage
}

func (t *lmeProviderUsageTracker) RecordHeader(raw string) {
	if t == nil || raw == "" {
		return
	}
	var usage lmeProviderUsage
	if err := json.Unmarshal([]byte(raw), &usage); err != nil {
		t.mu.Lock()
		t.usage.Error = appendError(t.usage.Error, err.Error())
		t.mu.Unlock()
		return
	}
	t.mu.Lock()
	t.usage.LLM.Add(usage.LLM)
	t.usage.Embedding.Add(usage.Embedding)
	t.usage.Reported = true
	t.mu.Unlock()
}

func (t *lmeProviderUsageTracker) Snapshot() lmeProviderUsage {
	if t == nil {
		return lmeProviderUsage{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := t.usage
	t.usage = lmeProviderUsage{}
	return snap
}

type lmeMem0UsageTransport struct {
	base    http.RoundTripper
	tracker *lmeProviderUsageTracker
}

func (t *lmeMem0UsageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err == nil && resp != nil && t.tracker != nil {
		t.tracker.RecordHeader(resp.Header.Get(lmeMem0UsageHeader))
	}
	return resp, err
}

func snapshotLongMemEvalUsage(
	tracker *lmeTokenTracker,
	backend memoryBackend,
) (lmeTokenUsage, lmeEmbeddingUsage, lmeProviderUsage) {
	usage := tracker.Snapshot()
	provider := backend.SnapshotProviderUsage()
	usage.Add(provider.LLM)
	return usage, provider.Embedding, provider
}

func addLongMemEvalBackendUsage(
	result *backendResult,
	usage lmeTokenUsage,
	embedding lmeEmbeddingUsage,
	provider lmeProviderUsage,
) {
	if result == nil {
		return
	}
	if !usage.IsZero() {
		if result.TokenUsage == nil {
			result.TokenUsage = &lmeTokenUsage{}
		}
		result.TokenUsage.Add(usage)
	}
	if !embedding.IsZero() {
		if result.EmbeddingUsage == nil {
			result.EmbeddingUsage = &lmeEmbeddingUsage{}
		}
		result.EmbeddingUsage.Add(embedding)
	}
	result.ProviderUsageReported = result.ProviderUsageReported || provider.Reported
	result.ProviderUsageError = appendError(result.ProviderUsageError, provider.Error)
}
