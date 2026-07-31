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
	"math"
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
	UsageMissingCalls   int     `json:"usage_missing_calls,omitempty"`
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
	u.UsageMissingCalls += other.UsageMissingCalls
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
	u.UsageMissingCalls = nonNegativeTokenDifference(
		u.UsageMissingCalls, other.UsageMissingCalls,
	)
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
		u.LLMCalls == 0 &&
		u.UsageMissingCalls == 0
}

func tokenUsagePtr(u lmeTokenUsage) *lmeTokenUsage {
	if u.IsZero() {
		return nil
	}
	u.setCacheHitRate()
	return &u
}

func longMemEvalLogicalUsageFromCalls(
	calls []lmeModelCallTrace,
) (lmeTokenUsage, bool) {
	if len(calls) == 0 {
		return lmeTokenUsage{}, false
	}
	var usage lmeTokenUsage
	for _, call := range calls {
		if call.LogicalTokenUsage == nil {
			return lmeTokenUsage{}, false
		}
		usage.Add(*call.LogicalTokenUsage)
	}
	return usage, true
}

type lmeEmbeddingUsage struct {
	PromptTokens                int `json:"prompt_tokens"`
	TotalTokens                 int `json:"total_tokens"`
	Calls                       int `json:"calls"`
	Requests                    int `json:"requests,omitempty"`
	ResponseCacheHits           int `json:"response_cache_hits,omitempty"`
	UsageMissingCalls           int `json:"usage_missing_calls,omitempty"`
	ProviderErrors              int `json:"provider_errors,omitempty"`
	LogicalPromptTokens         int `json:"logical_prompt_tokens,omitempty"`
	LogicalTotalTokens          int `json:"logical_total_tokens,omitempty"`
	LogicalUsageMissingRequests int `json:"logical_usage_missing_requests,omitempty"`
}

func (u *lmeEmbeddingUsage) Add(other lmeEmbeddingUsage) {
	u.PromptTokens += other.PromptTokens
	u.TotalTokens += other.TotalTokens
	u.Calls += other.Calls
	u.Requests += other.Requests
	u.ResponseCacheHits += other.ResponseCacheHits
	u.UsageMissingCalls += other.UsageMissingCalls
	u.ProviderErrors += other.ProviderErrors
	u.LogicalPromptTokens += other.LogicalPromptTokens
	u.LogicalTotalTokens += other.LogicalTotalTokens
	u.LogicalUsageMissingRequests += other.LogicalUsageMissingRequests
}

func (u lmeEmbeddingUsage) IsZero() bool {
	return u.PromptTokens == 0 && u.TotalTokens == 0 && u.Calls == 0 &&
		u.Requests == 0 && u.ResponseCacheHits == 0 &&
		u.UsageMissingCalls == 0 && u.ProviderErrors == 0 &&
		u.LogicalPromptTokens == 0 && u.LogicalTotalTokens == 0 &&
		u.LogicalUsageMissingRequests == 0
}

func normalizeLongMemEvalEmbeddingUsage(
	usage lmeEmbeddingUsage,
) lmeEmbeddingUsage {
	if usage.Requests == 0 && usage.Calls > 0 {
		usage.Requests = usage.Calls
	}
	if usage.LogicalPromptTokens != 0 ||
		usage.LogicalTotalTokens != 0 ||
		usage.LogicalUsageMissingRequests != 0 {
		return usage
	}

	// Legacy usage retains complete provider usage for cache misses. Preserve
	// that known portion and mark cache hits or failed requests as unknown.
	usage.LogicalPromptTokens = usage.PromptTokens
	usage.LogicalTotalTokens = usage.TotalTokens
	usage.LogicalUsageMissingRequests = usage.UsageMissingCalls
	knownRequests := usage.Calls + usage.ResponseCacheHits
	if usage.Requests > knownRequests {
		usage.LogicalUsageMissingRequests += usage.Requests - knownRequests
	}
	usage.LogicalUsageMissingRequests += usage.ResponseCacheHits
	return usage
}

func longMemEvalLogicalEmbeddingUsageComplete(
	usage lmeEmbeddingUsage,
) bool {
	return usage.Requests > 0 &&
		usage.LogicalPromptTokens >= 0 &&
		usage.LogicalTotalTokens > 0 &&
		usage.LogicalTotalTokens >= usage.LogicalPromptTokens &&
		usage.LogicalUsageMissingRequests == 0
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
	calls []lmeModelCallTrace
}

func (t *lmeTokenTracker) Record(u *model.Usage) {
	if t == nil || u == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usage.Add(longMemEvalModelUsage(u))
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

func (t *lmeTokenTracker) RecordCall(call lmeModelCallTrace) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.calls = append(t.calls, call)
	t.mu.Unlock()
}

func (t *lmeTokenTracker) SnapshotCalls() []lmeModelCallTrace {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	calls := append([]lmeModelCallTrace(nil), t.calls...)
	t.calls = nil
	return calls
}

type lmeTrackingModel struct {
	base          model.Model
	tracker       *lmeTokenTracker
	timeout       time.Duration
	responseCache *longMemEvalModelResponseCache
	modelName     string
	modelVariant  string
}

func (m *lmeTrackingModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	req = longMemEvalDeterministicRequest(req)
	call := lmeModelCallTrace{Source: lmeModelCallSourceModel}
	var cacheIdentity lmeModelResponseCacheIdentity
	if m.responseCache != nil {
		identity, key, err := longMemEvalModelResponseCacheKey(
			req, m.modelName, m.modelVariant,
		)
		if err != nil {
			return nil, err
		}
		cacheIdentity = identity
		call.CacheKey = key
		responses, source, logicalUsage, ok, err :=
			m.responseCache.Lookup(key)
		if err != nil {
			return nil, err
		}
		if ok {
			call.Source = source
			call.LogicalTokenUsage = logicalUsage
			return m.replayLongMemEvalModelResponses(responses, call), nil
		}
		if m.responseCache.RequireHit() {
			m.responseCache.recordError()
			call.Source = lmeModelCallSourceCacheMiss
			call.Error =
				"required LongMemEval model response cache entry is missing"
			m.tracker.RecordCall(call)
			return nil, errors.New(call.Error)
		}
	}
	var cancel context.CancelFunc
	if m.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, m.timeout)
	}
	respCh, err := m.base.GenerateContent(ctx, req)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		call.Error = err.Error()
		m.tracker.RecordCall(call)
		return nil, err
	}
	out := make(chan *model.Response)
	go func() {
		if cancel != nil {
			defer cancel()
		}
		defer close(out)
		sawError := false
		defer func() {
			m.tracker.RecordCall(call)
		}()
		var responses []*model.Response
		for resp := range respCh {
			responses = append(responses, cloneLongMemEvalModelResponse(resp))
			if resp != nil && resp.Usage != nil {
				m.tracker.Record(resp.Usage)
				addLongMemEvalModelCallUsage(&call, resp.Usage)
			}
			trackLongMemEvalModelResponse(&call, resp)
			if resp != nil && resp.Error != nil {
				sawError = true
			}
			out <- resp
		}
		if err := ctx.Err(); err != nil && !sawError {
			call.Error = lmeModelCallContextError(err, m.timeout)
			out <- &model.Response{
				Object: model.ObjectTypeError,
				Done:   true,
				Error: &model.ResponseError{
					Type:    model.ErrorTypeCancelled,
					Message: call.Error,
				},
			}
			return
		}
		if !sawError && m.responseCache != nil && len(responses) > 0 {
			if err := m.responseCache.Put(
				call.CacheKey, cacheIdentity, responses,
			); err != nil {
				call.CacheError = err.Error()
			}
		}
	}()
	return out, nil
}

func (m *lmeTrackingModel) replayLongMemEvalModelResponses(
	responses []*model.Response,
	call lmeModelCallTrace,
) <-chan *model.Response {
	out := make(chan *model.Response)
	go func() {
		defer close(out)
		defer func() {
			m.tracker.RecordCall(call)
		}()
		for _, resp := range responses {
			trackLongMemEvalModelResponse(&call, resp)
			out <- resp
		}
	}()
	return out
}

func longMemEvalModelUsage(usage *model.Usage) lmeTokenUsage {
	if usage == nil {
		return lmeTokenUsage{}
	}
	result := lmeTokenUsage{
		PromptTokens:        usage.PromptTokens,
		CompletionTokens:    usage.CompletionTokens,
		TotalTokens:         usage.TotalTokens,
		CachedTokens:        usage.PromptTokensDetails.CachedTokens,
		CacheCreationTokens: usage.PromptTokensDetails.CacheCreationTokens,
		CacheReadTokens:     usage.PromptTokensDetails.CacheReadTokens,
		ReasoningTokens:     usage.CompletionTokensDetails.ReasoningTokens,
		LLMCalls:            1,
	}
	result.setCacheHitRate()
	return result
}

func addLongMemEvalModelCallUsage(
	call *lmeModelCallTrace,
	usage *model.Usage,
) {
	if call == nil || usage == nil {
		return
	}
	current := longMemEvalModelUsage(usage)
	if call.LogicalTokenUsage == nil {
		call.LogicalTokenUsage = &current
		return
	}
	call.LogicalTokenUsage.Add(current)
}

func trackLongMemEvalModelResponse(
	call *lmeModelCallTrace,
	resp *model.Response,
) {
	if call == nil || resp == nil {
		return
	}
	if resp.Error != nil {
		call.Error = resp.Error.Message
	}
	if len(resp.Choices) == 0 {
		return
	}
	choice := resp.Choices[0]
	if choice.FinishReason != nil {
		call.FinishReason = *choice.FinishReason
	}
	msg := choice.Message
	if msg.Content != "" {
		call.Content = msg.Content
	}
	if len(msg.ToolCalls) == 0 {
		return
	}
	call.ToolCalls = make([]lmeToolCallTrace, 0, len(msg.ToolCalls))
	for _, toolCall := range msg.ToolCalls {
		call.ToolCalls = append(call.ToolCalls, lmeToolCallTrace{
			Name:      toolCall.Function.Name,
			Arguments: string(toolCall.Function.Arguments),
		})
	}
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
	base          *embeddingopenai.Embedder
	responseCache *longMemEvalEmbeddingResponseCache
	modelName     string
	mu            sync.Mutex
	usage         lmeEmbeddingUsage
}

func newLongMemEvalTrackingEmbedder(base *embeddingopenai.Embedder) *lmeTrackingEmbedder {
	return &lmeTrackingEmbedder{base: base}
}

func newLongMemEvalTrackingEmbedderWithCache(
	base *embeddingopenai.Embedder,
	cache *longMemEvalEmbeddingResponseCache,
	modelName string,
) *lmeTrackingEmbedder {
	return &lmeTrackingEmbedder{
		base:          base,
		responseCache: cache,
		modelName:     modelName,
	}
}

func (e *lmeTrackingEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	embedding, _, err := e.GetEmbeddingWithUsage(ctx, text)
	return embedding, err
}

func (e *lmeTrackingEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	e.mu.Lock()
	e.usage.Requests++
	e.mu.Unlock()

	var cacheIdentity lmeEmbeddingResponseCacheIdentity
	var cacheKey string
	if e.responseCache != nil {
		var err error
		cacheIdentity, cacheKey, err = longMemEvalEmbeddingResponseCacheKey(
			text, e.modelName, e.GetDimensions(),
		)
		if err != nil {
			return nil, nil, err
		}
		if embedding, logicalUsage, ok := e.responseCache.Lookup(cacheKey); ok {
			e.mu.Lock()
			e.usage.ResponseCacheHits++
			e.usage.addLogical(logicalUsage)
			e.mu.Unlock()
			return embedding, nil, nil
		}
		if e.responseCache.RequireHit() {
			e.responseCache.recordError()
			e.mu.Lock()
			e.usage.LogicalUsageMissingRequests++
			e.mu.Unlock()
			return nil, nil, fmt.Errorf(
				"required LongMemEval embedding response cache entry is missing",
			)
		}
	}

	embedding, usage, err := e.base.GetEmbeddingWithUsage(ctx, text)
	if err != nil {
		e.mu.Lock()
		e.usage.ProviderErrors++
		e.usage.LogicalUsageMissingRequests++
		e.mu.Unlock()
		return nil, nil, err
	}
	logicalUsage, logicalUsageComplete :=
		longMemEvalEmbeddingLogicalUsageFromProvider(usage)
	e.mu.Lock()
	e.usage.Calls++
	if logicalUsageComplete {
		e.usage.PromptTokens += logicalUsage.PromptTokens
		e.usage.TotalTokens += logicalUsage.TotalTokens
		e.usage.addLogical(logicalUsage)
	} else {
		e.usage.UsageMissingCalls++
		e.usage.LogicalUsageMissingRequests++
	}
	e.mu.Unlock()
	if e.responseCache != nil {
		var err error
		embedding, err = e.responseCache.Put(
			cacheKey, cacheIdentity, embedding, logicalUsage,
		)
		if err != nil {
			return nil, nil, err
		}
	}
	return embedding, usage, nil
}

func (u *lmeEmbeddingUsage) addLogical(
	usage *lmeEmbeddingLogicalUsage,
) {
	if usage == nil {
		u.LogicalUsageMissingRequests++
		return
	}
	u.LogicalPromptTokens += usage.PromptTokens
	u.LogicalTotalTokens += usage.TotalTokens
}

func longMemEvalEmbeddingLogicalUsageFromProvider(
	usage map[string]any,
) (*lmeEmbeddingLogicalUsage, bool) {
	prompt, promptOK := usageIntExact(usage["prompt_tokens"])
	total, totalOK := usageIntExact(usage["total_tokens"])
	if !promptOK || !totalOK || prompt < 0 || total < prompt {
		return nil, false
	}
	return &lmeEmbeddingLogicalUsage{
		PromptTokens: prompt,
		TotalTokens:  total,
	}, true
}

func usageIntExact(value any) (int, bool) {
	switch value := value.(type) {
	case int:
		return value, true
	case int32:
		return int(value), true
	case int64:
		converted := int(value)
		if int64(converted) != value {
			return 0, false
		}
		return converted, true
	case float64:
		if value != math.Trunc(value) {
			return 0, false
		}
		converted := int(value)
		if float64(converted) != value {
			return 0, false
		}
		return converted, true
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0, false
		}
		converted := int(parsed)
		if int64(converted) != parsed {
			return 0, false
		}
		return converted, true
	default:
		return 0, false
	}
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
	// Mem0 does not use the benchmark's client-side embedding response cache,
	// so legacy server payloads normalize physical calls directly to logical
	// requests and tokens.
	usage.Embedding = normalizeLongMemEvalEmbeddingUsage(usage.Embedding)
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
