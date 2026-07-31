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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	embeddingopenai "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

func TestLongMemEvalEmbeddingResponseCachePersistsWithoutRawText(
	t *testing.T,
) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "embeddings.jsonl")
	cache, err := openLongMemEvalEmbeddingResponseCache(path)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	const privateText = "private embedding input"
	identity, key, err := longMemEvalEmbeddingResponseCacheKey(
		privateText, "text-embedding-3-small", 2,
	)
	if err != nil {
		t.Fatalf("cache key: %v", err)
	}
	usage := &lmeEmbeddingLogicalUsage{
		PromptTokens: 7,
		TotalTokens:  7,
	}
	if _, err := cache.Put(
		key, identity, []float64{0.1, 0.2}, usage,
	); err != nil {
		t.Fatalf("put cache entry: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if strings.Contains(string(data), privateText) {
		t.Fatalf("cache leaked raw embedding input")
	}

	reopened, err := openLongMemEvalEmbeddingResponseCache(path)
	if err != nil {
		t.Fatalf("reopen cache: %v", err)
	}
	got, gotUsage, ok := reopened.Lookup(key)
	if !ok || len(got) != 2 || got[0] != 0.1 || got[1] != 0.2 {
		t.Fatalf("replayed embedding = %#v, ok=%v", got, ok)
	}
	if gotUsage == nil || *gotUsage != *usage {
		t.Fatalf("replayed usage = %#v, want %#v", gotUsage, usage)
	}
	got[0] = 99
	gotUsage.TotalTokens = 99
	again, againUsage, ok := reopened.Lookup(key)
	if !ok || again[0] != 0.1 {
		t.Fatalf("cache returned mutable embedding: %#v", again)
	}
	if againUsage == nil || *againUsage != *usage {
		t.Fatalf("cache returned mutable usage: %#v", againUsage)
	}
	if reopened.LedgerID() == "" || reopened.Len() != 1 {
		t.Fatalf(
			"cache provenance ledger=%q entries=%d",
			reopened.LedgerID(), reopened.Len(),
		)
	}
	hits, misses, cacheErrors := reopened.Stats()
	if hits != 2 || misses != 0 || cacheErrors != 0 {
		t.Fatalf(
			"cache stats hits=%d misses=%d errors=%d",
			hits, misses, cacheErrors,
		)
	}
}

func TestLongMemEvalTrackingEmbedderSeparatesLogicalAndProviderCalls(
	t *testing.T,
) {
	t.Parallel()

	providerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			providerCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{
  "object":"list",
  "data":[{"object":"embedding","embedding":[0.1,0.2],"index":0}],
  "model":"text-embedding-3-small",
  "usage":{"prompt_tokens":7,"total_tokens":7}
}`)
		},
	))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "embeddings.jsonl")
	cache, err := openLongMemEvalEmbeddingResponseCache(path)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	newBase := func() *embeddingopenai.Embedder {
		return embeddingopenai.New(
			embeddingopenai.WithAPIKey("test"),
			embeddingopenai.WithBaseURL(server.URL),
			embeddingopenai.WithModel("text-embedding-3-small"),
			embeddingopenai.WithDimensions(2),
		)
	}
	first := newLongMemEvalTrackingEmbedderWithCache(
		newBase(), cache, "text-embedding-3-small",
	)
	for i := 0; i < 2; i++ {
		if _, err := first.GetEmbedding(context.Background(), "same text"); err != nil {
			t.Fatalf("first arm embedding %d: %v", i, err)
		}
	}
	firstUsage := first.Snapshot()
	if firstUsage.Requests != 2 || firstUsage.ResponseCacheHits != 1 ||
		firstUsage.Calls != 1 || firstUsage.TotalTokens != 7 ||
		firstUsage.LogicalTotalTokens != 14 ||
		firstUsage.LogicalUsageMissingRequests != 0 {
		t.Fatalf("first arm usage = %#v", firstUsage)
	}

	reopened, err := openLongMemEvalEmbeddingResponseCache(path)
	if err != nil {
		t.Fatalf("reopen cache: %v", err)
	}
	second := newLongMemEvalTrackingEmbedderWithCache(
		newBase(), reopened, "text-embedding-3-small",
	)
	if _, err := second.GetEmbedding(context.Background(), "same text"); err != nil {
		t.Fatalf("second arm embedding: %v", err)
	}
	secondUsage := second.Snapshot()
	if secondUsage.Requests != 1 || secondUsage.ResponseCacheHits != 1 ||
		secondUsage.Calls != 0 || secondUsage.TotalTokens != 0 ||
		secondUsage.LogicalTotalTokens != 7 ||
		secondUsage.LogicalUsageMissingRequests != 0 {
		t.Fatalf("second arm usage = %#v", secondUsage)
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls = %d, want 1", providerCalls)
	}
}

func TestLongMemEvalTrackingEmbedderRecordsUsageBeforeCacheWriteFailure(
	t *testing.T,
) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{
  "object":"list",
  "data":[{"object":"embedding","embedding":[0.1,0.2],"index":0}],
  "model":"text-embedding-3-small",
  "usage":{"prompt_tokens":7,"total_tokens":7}
}`)
		},
	))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "embeddings.jsonl")
	cache, err := openLongMemEvalEmbeddingResponseCache(path)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove cache ledger: %v", err)
	}
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatalf("replace cache ledger with directory: %v", err)
	}

	base := embeddingopenai.New(
		embeddingopenai.WithAPIKey("test"),
		embeddingopenai.WithBaseURL(server.URL),
		embeddingopenai.WithModel("text-embedding-3-small"),
		embeddingopenai.WithDimensions(2),
	)
	tracker := newLongMemEvalTrackingEmbedderWithCache(
		base, cache, "text-embedding-3-small",
	)
	if _, err := tracker.GetEmbedding(
		context.Background(), "uncached text",
	); err == nil || !strings.Contains(err.Error(), "for append") {
		t.Fatalf("cache write error = %v", err)
	}
	usage := tracker.Snapshot()
	if usage.Requests != 1 || usage.Calls != 1 ||
		usage.PromptTokens != 7 || usage.TotalTokens != 7 ||
		usage.LogicalPromptTokens != 7 ||
		usage.LogicalTotalTokens != 7 ||
		usage.LogicalUsageMissingRequests != 0 {
		t.Fatalf("usage after cache write failure = %#v", usage)
	}
	_, _, cacheErrors := cache.Stats()
	if cacheErrors != 1 {
		t.Fatalf("cache errors = %d, want 1", cacheErrors)
	}
}

func TestLongMemEvalTrackingEmbedderMarksLegacyCacheUsageMissing(
	t *testing.T,
) {
	t.Parallel()

	providerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			providerCalls++
			w.WriteHeader(http.StatusInternalServerError)
		},
	))
	defer server.Close()

	cache, err := openLongMemEvalEmbeddingResponseCache(
		filepath.Join(t.TempDir(), "embeddings.jsonl"),
	)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	identity, key, err := longMemEvalEmbeddingResponseCacheKey(
		"legacy text", "text-embedding-3-small", 2,
	)
	if err != nil {
		t.Fatalf("cache key: %v", err)
	}
	if _, err := cache.Put(
		key, identity, []float64{0.1, 0.2}, nil,
	); err != nil {
		t.Fatalf("put legacy cache entry: %v", err)
	}

	base := embeddingopenai.New(
		embeddingopenai.WithAPIKey("test"),
		embeddingopenai.WithBaseURL(server.URL),
		embeddingopenai.WithModel("text-embedding-3-small"),
		embeddingopenai.WithDimensions(2),
	)
	tracker := newLongMemEvalTrackingEmbedderWithCache(
		base, cache, "text-embedding-3-small",
	)
	if _, err := tracker.GetEmbedding(
		context.Background(), "legacy text",
	); err != nil {
		t.Fatalf("legacy cache hit: %v", err)
	}
	usage := tracker.Snapshot()
	if usage.Requests != 1 || usage.ResponseCacheHits != 1 ||
		usage.Calls != 0 || usage.LogicalTotalTokens != 0 ||
		usage.LogicalUsageMissingRequests != 1 {
		t.Fatalf("legacy cache usage = %#v", usage)
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls = %d, want 0", providerCalls)
	}
}

func TestNormalizeLongMemEvalEmbeddingUsageMarksUnknownLegacyCosts(
	t *testing.T,
) {
	t.Parallel()

	got := normalizeLongMemEvalEmbeddingUsage(lmeEmbeddingUsage{
		PromptTokens:      8,
		TotalTokens:       8,
		Calls:             2,
		Requests:          5,
		ResponseCacheHits: 2,
		UsageMissingCalls: 1,
	})
	if got.LogicalPromptTokens != 8 || got.LogicalTotalTokens != 8 ||
		got.LogicalUsageMissingRequests != 4 {
		t.Fatalf("normalized legacy usage = %#v", got)
	}

	explicit := lmeEmbeddingUsage{
		PromptTokens:                8,
		TotalTokens:                 8,
		Calls:                       2,
		Requests:                    5,
		ResponseCacheHits:           2,
		LogicalPromptTokens:         20,
		LogicalTotalTokens:          20,
		LogicalUsageMissingRequests: 0,
	}
	if got := normalizeLongMemEvalEmbeddingUsage(explicit); got != explicit {
		t.Fatalf("explicit logical usage changed: got %#v want %#v",
			got, explicit)
	}
	if !longMemEvalLogicalEmbeddingUsageComplete(explicit) {
		t.Fatalf("explicit logical usage is incomplete: %#v", explicit)
	}
	explicit.LogicalTotalTokens = explicit.LogicalPromptTokens - 1
	if longMemEvalLogicalEmbeddingUsageComplete(explicit) {
		t.Fatalf("invalid logical token ordering accepted: %#v", explicit)
	}
}

func TestUsageIntExactRejectsLossyValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value any
		want  int
		ok    bool
	}{
		{name: "int", value: 7, want: 7, ok: true},
		{name: "int64", value: int64(7), want: 7, ok: true},
		{name: "integral float", value: float64(7), want: 7, ok: true},
		{name: "fraction", value: 7.5},
		{name: "overflow", value: 1e100},
		{name: "string", value: "7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := usageIntExact(test.value)
			if got != test.want || ok != test.ok {
				t.Fatalf("usageIntExact(%v) = (%d, %t), want (%d, %t)",
					test.value, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestLongMemEvalTrackingEmbedderRequireHitBlocksProviderCall(
	t *testing.T,
) {
	t.Parallel()

	providerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			providerCalls++
			w.WriteHeader(http.StatusInternalServerError)
		},
	))
	defer server.Close()

	cache, err := openLongMemEvalEmbeddingResponseCache(
		filepath.Join(t.TempDir(), "embeddings.jsonl"),
	)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	cache.requireHit = true
	base := embeddingopenai.New(
		embeddingopenai.WithAPIKey("test"),
		embeddingopenai.WithBaseURL(server.URL),
		embeddingopenai.WithModel("text-embedding-3-small"),
		embeddingopenai.WithDimensions(2),
	)
	tracker := newLongMemEvalTrackingEmbedderWithCache(
		base, cache, "text-embedding-3-small",
	)
	if _, err := tracker.GetEmbedding(
		context.Background(), "uncached text",
	); err == nil || !strings.Contains(err.Error(), "cache entry is missing") {
		t.Fatalf("required cache miss error = %v", err)
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls = %d, want 0", providerCalls)
	}
	hits, misses, cacheErrors := cache.Stats()
	if hits != 0 || misses != 1 || cacheErrors != 1 {
		t.Fatalf(
			"cache stats hits=%d misses=%d errors=%d",
			hits, misses, cacheErrors,
		)
	}
}

func TestConfiguredLongMemEvalEmbeddingCacheRequireHitNeedsPath(
	t *testing.T,
) {
	restoreStringFlag(t, flagLMEEmbeddingResponseCache, "")
	restoreBoolFlag(t, flagLMEEmbeddingResponseCacheRequireHit, true)

	if _, err := openConfiguredLongMemEvalEmbeddingResponseCache(); err == nil ||
		!strings.Contains(err.Error(), "requires -lme-embedding-response-cache") {
		t.Fatalf("missing required cache path error = %v", err)
	}
}

func TestValidateLongMemEvalComparisonRequiresSharedEmbeddingResponseLedger(
	t *testing.T,
) {
	t.Parallel()

	newResult := func(implementation string) *runResult {
		metadata := testLongMemEvalComparisonMetadata(implementation)
		metadata["embedding_response_cache_format_version"] =
			lmeEmbeddingCacheFormatVersion
		metadata["embedding_response_cache_shared"] = true
		metadata["embedding_response_cache_ledger_id"] =
			"shared-embedding-ledger"
		metadata["embedding_response_cache_errors"] = 0
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
				metadata["embedding_response_cache_ledger_id"] =
					"different-ledger"
			},
			wantError: "embedding_response_cache_ledger_id",
		},
		{
			name: "ephemeral cache",
			mutate: func(metadata map[string]any) {
				metadata["embedding_response_cache_shared"] = false
			},
			wantError: "embedding_response_cache_shared",
		},
		{
			name: "cache error",
			mutate: func(metadata map[string]any) {
				metadata["embedding_response_cache_errors"] = 1
			},
			wantError: "embedding_response_cache_errors",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := newResult("upstream-main")
			candidate := newResult("candidate-2196")
			test.mutate(candidate.Metadata)
			err := validateLongMemEvalComparison(baseline, candidate)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf(
					"shared embedding ledger error = %v, want %q",
					err, test.wantError,
				)
			}
		})
	}

	baseline := newResult("upstream-main")
	candidate := newResult("candidate-2196")
	baseline.Metadata["embedding_response_cache_shared"] = false
	candidate.Metadata["embedding_response_cache_shared"] = false
	err := validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(
		err.Error(), "requires a shared embedding response cache",
	) {
		t.Fatalf("unshared embedding response cache error = %v", err)
	}

	baseline = newResult("upstream-main")
	candidate = newResult("candidate-2196")
	baseline.Metadata["embedding_response_cache_errors"] = float64(1)
	candidate.Metadata["embedding_response_cache_errors"] = float64(1)
	err = validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "requires zero") {
		t.Fatalf("non-zero embedding response cache error = %v", err)
	}
}
