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
	if _, err := cache.Put(key, identity, []float64{0.1, 0.2}); err != nil {
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
	got, ok := reopened.Lookup(key)
	if !ok || len(got) != 2 || got[0] != 0.1 || got[1] != 0.2 {
		t.Fatalf("replayed embedding = %#v, ok=%v", got, ok)
	}
	got[0] = 99
	again, ok := reopened.Lookup(key)
	if !ok || again[0] != 0.1 {
		t.Fatalf("cache returned mutable embedding: %#v", again)
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
		firstUsage.Calls != 1 || firstUsage.TotalTokens != 7 {
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
		secondUsage.Calls != 0 || secondUsage.TotalTokens != 0 {
		t.Fatalf("second arm usage = %#v", secondUsage)
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls = %d, want 1", providerCalls)
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
