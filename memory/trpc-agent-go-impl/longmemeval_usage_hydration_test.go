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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestHydrateLongMemEvalLogicalUsage(t *testing.T) {
	cachePath, cache, key := newLongMemEvalLogicalUsageTestCache(t)
	sourcePath := filepath.Join(t.TempDir(), "results.json")
	result := newLongMemEvalLogicalUsageTestResult(cache.LedgerID(), key)
	if err := writeLongMemEvalResults(sourcePath, result); err != nil {
		t.Fatalf("write source: %v", err)
	}
	sourceSHA, err := longMemEvalFileSHA256(sourcePath)
	if err != nil {
		t.Fatalf("hash source: %v", err)
	}
	cacheSHA, err := longMemEvalFileSHA256(cachePath)
	if err != nil {
		t.Fatalf("hash cache: %v", err)
	}

	originalCachePath := *flagLMEModelResponseCache
	t.Cleanup(func() {
		*flagLMEModelResponseCache = originalCachePath
	})
	*flagLMEModelResponseCache = cachePath
	outputDir := t.TempDir()
	if err := hydrateLongMemEvalLogicalUsage(sourcePath, outputDir); err != nil {
		t.Fatalf("hydrate usage: %v", err)
	}

	outputPath := filepath.Join(
		outputDir,
		lmeLogicalUsageHydrationOutput,
	)
	hydrated, err := loadLongMemEvalResults(outputPath)
	if err != nil {
		t.Fatalf("load hydrated result: %v", err)
	}
	call := hydrated.Cases[0].BackendResults["pgvector"].
		IngestTraces[0].Extraction.ModelCalls[0]
	if call.LogicalTokenUsage == nil ||
		call.LogicalTokenUsage.PromptTokens != 10 ||
		call.LogicalTokenUsage.CompletionTokens != 2 ||
		call.LogicalTokenUsage.TotalTokens != 12 ||
		call.LogicalTokenUsage.LLMCalls != 1 {
		t.Fatalf("hydrated logical usage = %#v", call.LogicalTokenUsage)
	}

	provenanceData, err := json.Marshal(
		hydrated.Metadata["logical_usage_hydration"],
	)
	if err != nil {
		t.Fatalf("marshal hydration provenance: %v", err)
	}
	var provenance lmeLogicalUsageHydrationProvenance
	if err := json.Unmarshal(provenanceData, &provenance); err != nil {
		t.Fatalf("decode hydration provenance: %v", err)
	}
	stats := provenance.BackendStats["pgvector"]
	if provenance.Version != lmeLogicalUsageHydrationVersion ||
		provenance.SourceSHA256 != sourceSHA ||
		provenance.CacheSHA256 != cacheSHA ||
		provenance.CacheLedgerID != cache.LedgerID() ||
		!provenance.AllCallsComplete ||
		stats.ModelCalls != 1 ||
		stats.Hydrated != 1 ||
		stats.Existing != 0 ||
		stats.Missing != 0 {
		t.Fatalf("hydration provenance = %#v", provenance)
	}

	unchangedSHA, err := longMemEvalFileSHA256(sourcePath)
	if err != nil {
		t.Fatalf("rehash source: %v", err)
	}
	if unchangedSHA != sourceSHA {
		t.Fatalf("source hash changed: got %s, want %s", unchangedSHA, sourceSHA)
	}
	unchanged, err := loadLongMemEvalResults(sourcePath)
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	sourceCall := unchanged.Cases[0].BackendResults["pgvector"].
		IngestTraces[0].Extraction.ModelCalls[0]
	if sourceCall.LogicalTokenUsage != nil {
		t.Fatalf("source logical usage was modified: %#v", sourceCall)
	}
}

func TestHydrateLongMemEvalLogicalUsageRejectsInvalidCache(t *testing.T) {
	cachePath, cache, key := newLongMemEvalLogicalUsageTestCache(t)
	originalCachePath := *flagLMEModelResponseCache
	t.Cleanup(func() {
		*flagLMEModelResponseCache = originalCachePath
	})
	*flagLMEModelResponseCache = cachePath

	tests := []struct {
		name      string
		ledgerID  string
		cacheKey  string
		wantError string
	}{
		{
			name:      "ledger mismatch",
			ledgerID:  "different-ledger",
			cacheKey:  key,
			wantError: "does not match cache",
		},
		{
			name:      "missing entry",
			ledgerID:  cache.LedgerID(),
			cacheKey:  strings.Repeat("0", len(key)),
			wantError: "1 of 1 extraction model calls are missing",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sourcePath := filepath.Join(t.TempDir(), "results.json")
			result := newLongMemEvalLogicalUsageTestResult(
				test.ledgerID,
				test.cacheKey,
			)
			if err := writeLongMemEvalResults(sourcePath, result); err != nil {
				t.Fatalf("write source: %v", err)
			}
			outputDir := t.TempDir()
			err := hydrateLongMemEvalLogicalUsage(sourcePath, outputDir)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("hydrate error = %v, want %q", err, test.wantError)
			}
			outputPath := filepath.Join(
				outputDir,
				lmeLogicalUsageHydrationOutput,
			)
			if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
				t.Fatalf("unexpected output file: %v", statErr)
			}
		})
	}
}

func newLongMemEvalLogicalUsageTestCache(
	t *testing.T,
) (string, *longMemEvalModelResponseCache, string) {
	t.Helper()
	cachePath := filepath.Join(t.TempDir(), "model-responses.json")
	cache, err := openLongMemEvalModelResponseCache(cachePath)
	if err != nil {
		t.Fatalf("open model response cache: %v", err)
	}
	identity := lmeModelResponseCacheIdentity{
		FormatVersion: lmeModelCacheFormatVersion,
		RequestSHA256: "request-sha",
		Model:         "glm52",
		ModelVariant:  "glm",
	}
	key, err := longMemEvalJSONSHA256(identity)
	if err != nil {
		t.Fatalf("hash model response identity: %v", err)
	}
	if err := cache.Put(key, identity, []*model.Response{{
		Done: true,
		Usage: &model.Usage{
			PromptTokens:     10,
			CompletionTokens: 2,
			TotalTokens:      12,
		},
	}}); err != nil {
		t.Fatalf("write model response cache: %v", err)
	}
	return cachePath, cache, key
}

func newLongMemEvalLogicalUsageTestResult(
	ledgerID string,
	cacheKey string,
) *runResult {
	return &runResult{
		Metadata: map[string]any{
			"model_response_cache_format_version": lmeModelCacheFormatVersion,
			"model_response_cache_ledger_id":      ledgerID,
		},
		Cases: []*caseResult{{
			QuestionID: "question-1",
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend: "pgvector",
					IngestTraces: []ingestTrace{{
						Extraction: &extractionTrace{
							ModelCalls: []lmeModelCallTrace{{
								Source:   lmeModelCallSourcePersistent,
								CacheKey: cacheKey,
							}},
						},
					}},
				},
			},
		}},
	}
}
