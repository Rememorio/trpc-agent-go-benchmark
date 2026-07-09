//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// countingEmbedder wraps an embedder.Embedder and folds every embedding call's
// token usage into the collector, attributed to the current session/turn read
// from the context.
//
// In the refactored plugin the tool_search function is called inline by the main
// model as an ordinary tool, so the keyword and calltool modes add NO separate
// out-of-band LLM call — their overhead is already captured in the chat bucket
// (a larger prompt carrying the catalog + the tool_search result, plus the extra
// completion for the tool call). Embedding is the ONLY out-of-band cost, and it
// only occurs in knowledge mode. Counting it at the embedder is therefore both
// sufficient and robust: it does not depend on the plugin's internal per-turn
// usage accumulator, which is re-seeded every model call.
type countingEmbedder struct {
	inner     embedder.Embedder
	collector *Collector
}

func newCountingEmbedder(inner embedder.Embedder, collector *Collector) *countingEmbedder {
	return &countingEmbedder{inner: inner, collector: collector}
}

func (e *countingEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	vec, usage, err := e.inner.GetEmbeddingWithUsage(ctx, text)
	e.record(ctx, usage)
	return vec, err
}

func (e *countingEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	vec, usage, err := e.inner.GetEmbeddingWithUsage(ctx, text)
	e.record(ctx, usage)
	return vec, usage, err
}

func (e *countingEmbedder) GetDimensions() int { return e.inner.GetDimensions() }

// record attributes an embedder usage map to the current turn's tool-search
// bucket. Embedders may report token counts as int, int64, or float64.
func (e *countingEmbedder) record(ctx context.Context, m map[string]any) {
	if m == nil || e.collector == nil {
		return
	}
	sessionID, turnIndex, ok := sessionTurnFromContext(ctx)
	if !ok {
		return
	}
	usage := &model.Usage{
		PromptTokens: usageTokens(m["prompt_tokens"]),
		TotalTokens:  usageTokens(m["total_tokens"]),
	}
	// Some embedding backends only report total_tokens; mirror it into prompt so
	// the split stays meaningful.
	if usage.PromptTokens == 0 && usage.TotalTokens > 0 {
		usage.PromptTokens = usage.TotalTokens
	}
	if usage.TotalTokens == 0 && usage.PromptTokens > 0 {
		usage.TotalTokens = usage.PromptTokens
	}
	e.collector.AddToolSearchUsage(sessionID, turnIndex, usage)
}

// usageTokens coerces an embedder usage value (int / int64 / float64) to int.
func usageTokens(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

var _ embedder.Embedder = (*countingEmbedder)(nil)
