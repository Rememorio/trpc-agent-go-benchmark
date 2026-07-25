//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package scenarios

import (
	"context"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TokenUsage holds accumulated token usage for a single QA or sample.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// CachedTokens is the number of prompt tokens served from
	// the provider's prompt cache (e.g. OpenAI cached_tokens).
	CachedTokens        int `json:"cached_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
	ReasoningTokens     int `json:"reasoning_tokens,omitempty"`
	// LLMCalls is the number of model invocations
	// (may be >1 for tool-calling agents).
	LLMCalls int `json:"llm_calls"`
}

// Add merges another TokenUsage into the receiver.
func (u *TokenUsage) Add(other TokenUsage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
	u.CachedTokens += other.CachedTokens
	u.CacheCreationTokens += other.CacheCreationTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.ReasoningTokens += other.ReasoningTokens
	u.LLMCalls += other.LLMCalls
}

// IsZero reports whether no model usage was recorded.
func (u TokenUsage) IsZero() bool {
	return u.PromptTokens == 0 && u.CompletionTokens == 0 &&
		u.TotalTokens == 0 && u.CachedTokens == 0 &&
		u.CacheCreationTokens == 0 && u.CacheReadTokens == 0 &&
		u.ReasoningTokens == 0 && u.LLMCalls == 0
}

// CachedPromptTokens returns the provider-independent cache-read count.
// Providers report this either as CachedTokens or CacheReadTokens; max avoids
// double-counting adapters that populate both representations.
func (u TokenUsage) CachedPromptTokens() int {
	return max(u.CachedTokens, u.CacheReadTokens)
}

// TokenTracker accumulates token usage across multiple LLM calls
// in a thread-safe manner. It is designed to be wired into model
// callbacks (AfterModelCallbackStructured) so that every model
// invocation—including multi-turn tool-call loops—is captured.
type TokenTracker struct {
	mu    sync.Mutex
	usage TokenUsage
	calls []ExtractionCallTrace
}

// ExtractionMessageTrace records one non-system source message seen by the
// extractor. System prompts can contain all persisted memories and are omitted
// to keep result files focused and bounded.
type ExtractionMessageTrace struct {
	Role    model.Role `json:"role"`
	Content string     `json:"content,omitempty"`
}

// ExtractionCallTrace records one extractor model response and its memory
// operations. Tool arguments are retained so extraction and persistence misses
// can be distinguished from retrieval misses after a run.
type ExtractionCallTrace struct {
	Step                int                      `json:"step"`
	PromptTokens        int                      `json:"prompt_tokens"`
	CompletionTokens    int                      `json:"completion_tokens"`
	TotalTokens         int                      `json:"total_tokens"`
	CachedTokens        int                      `json:"cached_tokens,omitempty"`
	CacheCreationTokens int                      `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int                      `json:"cache_read_tokens,omitempty"`
	ReasoningTokens     int                      `json:"reasoning_tokens,omitempty"`
	FinishReason        string                   `json:"finish_reason,omitempty"`
	Content             string                   `json:"content,omitempty"`
	SourceMessages      []ExtractionMessageTrace `json:"source_messages,omitempty"`
	ToolCalls           []ToolCallTrace          `json:"tool_calls,omitempty"`
	Error               string                   `json:"error,omitempty"`
}

// NewTokenTracker creates a new empty tracker.
func NewTokenTracker() *TokenTracker {
	return &TokenTracker{}
}

// Record adds usage from a single model response.
func (t *TokenTracker) Record(u *model.Usage) {
	if u == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usage.Add(tokenUsageFromModelUsage(u))
}

func tokenUsageFromModelUsage(usage *model.Usage) TokenUsage {
	if usage == nil {
		return TokenUsage{}
	}
	return TokenUsage{
		PromptTokens:        usage.PromptTokens,
		CompletionTokens:    usage.CompletionTokens,
		TotalTokens:         usage.TotalTokens,
		CachedTokens:        usage.PromptTokensDetails.CachedTokens,
		CacheCreationTokens: usage.PromptTokensDetails.CacheCreationTokens,
		CacheReadTokens:     usage.PromptTokensDetails.CacheReadTokens,
		ReasoningTokens:     usage.CompletionTokensDetails.ReasoningTokens,
		LLMCalls:            1,
	}
}

// Snapshot returns a copy of the current accumulated usage and
// resets the tracker to zero. This is typically called after each
// QA question to capture per-question token usage.
func (t *TokenTracker) Snapshot() TokenUsage {
	usage, _ := t.SnapshotWithCalls()
	return usage
}

// SnapshotWithCalls returns and resets both accumulated usage and extraction
// calls. A LoCoMo sample waits for extraction completion before taking this
// snapshot, so calls cannot bleed into the following sample.
func (t *TokenTracker) SnapshotWithCalls() (
	TokenUsage,
	[]ExtractionCallTrace,
) {
	t.mu.Lock()
	defer t.mu.Unlock()
	usage := t.usage
	calls := append([]ExtractionCallTrace(nil), t.calls...)
	t.usage = TokenUsage{}
	t.calls = nil
	return usage, calls
}

// Peek returns a copy of the current accumulated usage without
// resetting. Useful for cumulative reporting.
func (t *TokenTracker) Peek() TokenUsage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.usage
}

// AfterModelCallback returns an AfterModelCallbackStructured that
// records token usage from every model response into this tracker.
func (t *TokenTracker) AfterModelCallback() model.AfterModelCallbackStructured {
	return func(
		_ context.Context,
		args *model.AfterModelArgs,
	) (*model.AfterModelResult, error) {
		if args != nil {
			t.recordCall(args.Request, args.Response, args.Error)
		}
		return nil, nil
	}
}

func (t *TokenTracker) recordCall(
	request *model.Request,
	response *model.Response,
	callErr error,
) {
	if t == nil {
		return
	}
	trace := ExtractionCallTrace{}
	if request != nil {
		for _, message := range request.Messages {
			if message.Role == model.RoleSystem {
				continue
			}
			trace.SourceMessages = append(
				trace.SourceMessages,
				ExtractionMessageTrace{
					Role:    message.Role,
					Content: message.Content,
				},
			)
		}
	}
	if callErr != nil {
		trace.Error = callErr.Error()
	}
	if response != nil && response.Error != nil {
		trace.Error = response.Error.Message
	}
	if response != nil && response.Usage != nil {
		trace.PromptTokens = response.Usage.PromptTokens
		trace.CompletionTokens = response.Usage.CompletionTokens
		trace.TotalTokens = response.Usage.TotalTokens
		trace.CachedTokens = response.Usage.PromptTokensDetails.CachedTokens
		trace.CacheCreationTokens =
			response.Usage.PromptTokensDetails.CacheCreationTokens
		trace.CacheReadTokens =
			response.Usage.PromptTokensDetails.CacheReadTokens
		trace.ReasoningTokens =
			response.Usage.CompletionTokensDetails.ReasoningTokens
	}
	if response != nil && len(response.Choices) > 0 {
		choice := response.Choices[0]
		if choice.FinishReason != nil {
			trace.FinishReason = *choice.FinishReason
		}
		trace.Content = choice.Message.Content
		for _, call := range choice.Message.ToolCalls {
			trace.ToolCalls = append(trace.ToolCalls, ToolCallTrace{
				Name: call.Function.Name,
				Args: string(call.Function.Arguments),
			})
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	callUsage := TokenUsage{LLMCalls: 1}
	if response != nil && response.Usage != nil {
		callUsage = tokenUsageFromModelUsage(response.Usage)
	}
	t.usage.Add(callUsage)
	trace.Step = len(t.calls) + 1
	t.calls = append(t.calls, trace)
}

// NewModelCallbacksWithTracker creates model.Callbacks pre-wired
// with the given tracker's AfterModelCallback.
func NewModelCallbacksWithTracker(
	tracker *TokenTracker,
) *model.Callbacks {
	cb := model.NewCallbacks()
	cb.AfterModel = append(
		cb.AfterModel, tracker.AfterModelCallback(),
	)
	return cb
}
