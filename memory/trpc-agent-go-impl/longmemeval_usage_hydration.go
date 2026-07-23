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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	lmeLogicalUsageHydrationVersion = "lme-logical-usage-hydration-v1"
	lmeLogicalUsageHydrationOutput  = "logical_usage_hydrated_results.json"
)

type lmeLogicalUsageHydrationStats struct {
	ModelCalls int `json:"model_calls"`
	Hydrated   int `json:"hydrated"`
	Existing   int `json:"existing"`
	Missing    int `json:"missing"`
}

type lmeLogicalUsageHydrationProvenance struct {
	Version          string                                   `json:"version"`
	HydratedAt       string                                   `json:"hydrated_at"`
	SourceSHA256     string                                   `json:"source_sha256"`
	CacheSHA256      string                                   `json:"model_response_cache_sha256"`
	CacheLedgerID    string                                   `json:"model_response_cache_ledger_id"`
	HydrationBuild   lmeBuildProvenance                       `json:"hydration_build"`
	BackendStats     map[string]lmeLogicalUsageHydrationStats `json:"backend_stats"`
	AllCallsComplete bool                                     `json:"all_calls_complete"`
}

func hydrateLongMemEvalLogicalUsage(
	sourcePath string,
	outputDir string,
) error {
	cachePath := strings.TrimSpace(*flagLMEModelResponseCache)
	if cachePath == "" {
		return errors.New(
			"lme-hydrate-logical-usage-results requires " +
				"-lme-model-response-cache",
		)
	}
	sourceSHA, err := longMemEvalFileSHA256(sourcePath)
	if err != nil {
		return fmt.Errorf("hash logical usage source: %w", err)
	}
	cacheSHA, err := longMemEvalFileSHA256(cachePath)
	if err != nil {
		return fmt.Errorf("hash model response cache: %w", err)
	}
	result, err := loadLongMemEvalResults(sourcePath)
	if err != nil {
		return err
	}
	cache, err := openLongMemEvalModelResponseCache(cachePath)
	if err != nil {
		return err
	}
	if err := validateLongMemEvalLogicalUsageCache(result, cache); err != nil {
		return err
	}

	stats := make(map[string]lmeLogicalUsageHydrationStats)
	totalCalls := 0
	totalMissing := 0
	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		for backend, br := range cr.BackendResults {
			if br == nil {
				continue
			}
			backendStats := stats[backend]
			for traceIndex := range br.IngestTraces {
				extraction := br.IngestTraces[traceIndex].Extraction
				if extraction == nil {
					continue
				}
				for callIndex := range extraction.ModelCalls {
					call := &extraction.ModelCalls[callIndex]
					backendStats.ModelCalls++
					totalCalls++
					if call.LogicalTokenUsage != nil {
						backendStats.Existing++
						continue
					}
					usage, ok := cache.LogicalUsage(call.CacheKey)
					if !ok {
						backendStats.Missing++
						totalMissing++
						continue
					}
					call.LogicalTokenUsage = tokenUsagePtr(usage)
					backendStats.Hydrated++
				}
			}
			stats[backend] = backendStats
		}
	}
	if totalCalls == 0 {
		return errors.New(
			"LongMemEval result has no extraction model-call traces",
		)
	}
	if totalMissing != 0 {
		return fmt.Errorf(
			"hydrate LongMemEval logical usage: %d of %d extraction "+
				"model calls are missing from the response cache",
			totalMissing, totalCalls,
		)
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	result.Metadata["logical_usage_hydration"] =
		lmeLogicalUsageHydrationProvenance{
			Version:          lmeLogicalUsageHydrationVersion,
			HydratedAt:       time.Now().UTC().Format(time.RFC3339),
			SourceSHA256:     sourceSHA,
			CacheSHA256:      cacheSHA,
			CacheLedgerID:    cache.LedgerID(),
			HydrationBuild:   currentLongMemEvalBuildProvenance(),
			BackendStats:     stats,
			AllCallsComplete: true,
		}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf(
			"create logical usage hydration output directory: %w",
			err,
		)
	}
	outputPath := filepath.Join(
		outputDir,
		lmeLogicalUsageHydrationOutput,
	)
	if err := writeLongMemEvalResults(outputPath, result); err != nil {
		return err
	}
	return nil
}

func validateLongMemEvalLogicalUsageCache(
	result *runResult,
	cache *longMemEvalModelResponseCache,
) error {
	if result == nil || result.Metadata == nil {
		return errors.New(
			"LongMemEval logical usage source metadata is missing",
		)
	}
	if cache == nil || !cache.Persistent() {
		return errors.New(
			"LongMemEval logical usage hydration requires a persistent cache",
		)
	}
	format, ok := lmeMetadataString(
		result.Metadata,
		"model_response_cache_format_version",
	)
	if !ok || format != lmeModelCacheFormatVersion {
		return fmt.Errorf(
			"LongMemEval source model response cache format is %q, want %q",
			format,
			lmeModelCacheFormatVersion,
		)
	}
	ledgerID, ok := lmeMetadataString(
		result.Metadata,
		"model_response_cache_ledger_id",
	)
	if !ok || ledgerID == "" {
		return errors.New(
			"LongMemEval source model response cache ledger ID is missing",
		)
	}
	if ledgerID != cache.LedgerID() {
		return fmt.Errorf(
			"LongMemEval source model response cache ledger ID %q "+
				"does not match cache %q",
			ledgerID,
			cache.LedgerID(),
		)
	}
	return nil
}
