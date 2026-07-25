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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	lmeJudgeVerdictSourceModel      = "model"
	lmeJudgeVerdictSourceExisting   = "existing-result"
	lmeJudgeVerdictSourceCurrentRun = "current-run-cache"
	lmeJudgeVerdictSourcePersistent = "persistent-cache"
)

type lmeJudgeCacheIdentity struct {
	ProtocolVersion string                       `json:"protocol_version"`
	PromptSHA256    string                       `json:"prompt_sha256"`
	PromptVersion   string                       `json:"prompt_version"`
	Model           string                       `json:"model"`
	ModelVariant    string                       `json:"model_variant"`
	Generation      lmeJudgeGenerationProvenance `json:"generation"`
	Runs            int                          `json:"runs"`
}

type lmeJudgeCacheEntry struct {
	Identity  lmeJudgeCacheIdentity `json:"identity"`
	Judge     lmeJudgeResult        `json:"judge"`
	CreatedAt string                `json:"created_at"`
}

type lmeJudgeCacheFile struct {
	Version   string                        `json:"version"`
	LedgerID  string                        `json:"ledger_id"`
	UpdatedAt string                        `json:"updated_at,omitempty"`
	Entries   map[string]lmeJudgeCacheEntry `json:"entries"`
}

type longMemEvalJudgeCache struct {
	path                    string
	file                    lmeJudgeCacheFile
	persistent              map[string]struct{}
	hits                    int
	logicalUsageHits        int
	logicalUsageMissingHits int
}

func openLongMemEvalJudgeCache(path string) (*longMemEvalJudgeCache, error) {
	path = strings.TrimSpace(path)
	cache := &longMemEvalJudgeCache{
		path: path,
		file: lmeJudgeCacheFile{
			Version: lmeJudgeCacheFormatVersion,
			Entries: make(map[string]lmeJudgeCacheEntry),
		},
		persistent: make(map[string]struct{}),
	}
	if path == "" {
		return cache, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			ledgerID, err := newLongMemEvalLedgerID()
			if err != nil {
				return nil, err
			}
			cache.file.LedgerID = ledgerID
			if err := writeLongMemEvalJudgeCache(path, &cache.file); err != nil {
				return nil, err
			}
			return cache, nil
		}
		return nil, fmt.Errorf("read LongMemEval judge cache: %w", err)
	}
	if err := json.Unmarshal(data, &cache.file); err != nil {
		return nil, fmt.Errorf("parse LongMemEval judge cache: %w", err)
	}
	if cache.file.Version != lmeJudgeCacheFormatVersion {
		return nil, fmt.Errorf(
			"unsupported LongMemEval judge cache version %q, want %q",
			cache.file.Version,
			lmeJudgeCacheFormatVersion,
		)
	}
	if strings.TrimSpace(cache.file.LedgerID) == "" {
		return nil, fmt.Errorf("LongMemEval judge cache is missing ledger_id")
	}
	if cache.file.Entries == nil {
		cache.file.Entries = make(map[string]lmeJudgeCacheEntry)
	}
	for key, entry := range cache.file.Entries {
		if err := validateLongMemEvalJudgeCacheEntry(key, entry); err != nil {
			return nil, fmt.Errorf("validate LongMemEval judge cache entry %s: %w", key, err)
		}
		cache.persistent[key] = struct{}{}
	}
	return cache, nil
}

func (c *longMemEvalJudgeCache) Persistent() bool {
	return c != nil && c.path != ""
}

func (c *longMemEvalJudgeCache) LedgerID() string {
	if c == nil || !c.Persistent() {
		return ""
	}
	return c.file.LedgerID
}

func (c *longMemEvalJudgeCache) Len() int {
	if c == nil {
		return 0
	}
	return len(c.file.Entries)
}

func (c *longMemEvalJudgeCache) Hits() int {
	if c == nil {
		return 0
	}
	return c.hits
}

func (c *longMemEvalJudgeCache) Lookup(key string) (*lmeJudgeResult, string, bool) {
	if c == nil {
		return nil, "", false
	}
	entry, ok := c.file.Entries[key]
	if !ok {
		return nil, "", false
	}
	if !completeLongMemEvalJudgeConsensus(&entry.Judge) {
		return nil, "", false
	}
	source := lmeJudgeVerdictSourceCurrentRun
	if _, ok := c.persistent[key]; ok {
		source = lmeJudgeVerdictSourcePersistent
	}
	c.hits++
	if entry.Judge.LogicalUsageComplete {
		c.logicalUsageHits++
	} else {
		c.logicalUsageMissingHits++
	}
	return reusedLongMemEvalJudgeResult(entry.Judge, source), source, true
}

func (c *longMemEvalJudgeCache) Put(
	key string,
	identity lmeJudgeCacheIdentity,
	judge *lmeJudgeResult,
) error {
	if c == nil || judge == nil {
		return nil
	}
	if !completeLongMemEvalJudgeConsensus(judge) {
		return nil
	}
	previous, existed := c.file.Entries[key]
	if existed && completeLongMemEvalJudgeConsensus(&previous.Judge) {
		return nil
	}
	cloned, err := cloneLongMemEvalJudgeResult(judge)
	if err != nil {
		return err
	}
	entry := lmeJudgeCacheEntry{
		Identity:  identity,
		Judge:     *cloned,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := validateLongMemEvalJudgeCacheEntry(key, entry); err != nil {
		return err
	}
	c.file.Entries[key] = entry
	if !c.Persistent() {
		return nil
	}
	if err := writeLongMemEvalJudgeCache(c.path, &c.file); err != nil {
		if existed {
			c.file.Entries[key] = previous
		} else {
			delete(c.file.Entries, key)
		}
		return err
	}
	return nil
}

func resolveLongMemEvalJudge(
	ctx context.Context,
	baseLLM model.Model,
	modelName string,
	modelVariant string,
	cr *caseResult,
	br *backendResult,
	runs int,
	cache *longMemEvalJudgeCache,
) (*lmeJudgeResult, string, error) {
	identity, key, err := longMemEvalJudgeCacheKey(
		cr,
		br.Answer,
		modelName,
		modelVariant,
		runs,
	)
	if err != nil {
		return nil, "", err
	}
	if judge, source, ok := cache.Lookup(key); ok {
		return judge, source, nil
	}
	if shouldReuseLongMemEvalJudge(br, modelName, runs, key) {
		if br.Judge.VerdictSource == "" {
			br.Judge.VerdictSource = lmeJudgeVerdictSourceExisting
		}
		if err := cache.Put(key, identity, br.Judge); err != nil {
			return nil, "", err
		}
		return br.Judge, lmeJudgeVerdictSourceExisting, nil
	}
	judge := judgeLongMemEvalConsensus(
		ctx,
		baseLLM,
		modelName,
		cr,
		br.Answer,
		runs,
	)
	judge.CacheKey = key
	judge.VerdictSource = lmeJudgeVerdictSourceModel
	if completeLongMemEvalJudgeConsensus(judge) {
		if err := cache.Put(key, identity, judge); err != nil {
			return nil, "", err
		}
	}
	return judge, lmeJudgeVerdictSourceModel, nil
}

func longMemEvalJudgeCacheKey(
	cr *caseResult,
	answer string,
	modelName string,
	modelVariant string,
	runs int,
) (lmeJudgeCacheIdentity, string, error) {
	prompt := buildLongMemEvalJudgePrompt(cr, strings.TrimSpace(answer))
	promptSum := sha256.Sum256([]byte(prompt))
	identity := lmeJudgeCacheIdentity{
		ProtocolVersion: lmeJudgeProtocolVersion,
		PromptSHA256:    hex.EncodeToString(promptSum[:]),
		PromptVersion:   lmeJudgePromptVersion,
		Model:           strings.TrimSpace(modelName),
		ModelVariant:    strings.ToLower(strings.TrimSpace(modelVariant)),
		Generation:      currentLongMemEvalJudgeGeneration(),
		Runs:            runs,
	}
	key, err := longMemEvalJSONSHA256(identity)
	if err != nil {
		return lmeJudgeCacheIdentity{}, "", fmt.Errorf("hash LongMemEval judge cache key: %w", err)
	}
	return identity, key, nil
}

func validateLongMemEvalJudgeCacheEntry(key string, entry lmeJudgeCacheEntry) error {
	wantKey, err := longMemEvalJSONSHA256(entry.Identity)
	if err != nil {
		return fmt.Errorf("hash identity: %w", err)
	}
	if key != wantKey {
		return fmt.Errorf("key mismatch: got %s, want %s", key, wantKey)
	}
	judge := entry.Judge
	if judge.CacheKey != key {
		return fmt.Errorf("judge cache key %q does not match entry key", judge.CacheKey)
	}
	if judge.Model != entry.Identity.Model {
		return fmt.Errorf("judge model %q does not match identity model %q", judge.Model, entry.Identity.Model)
	}
	if _, valid := longMemEvalJudgeCorrect(&backendResult{Judge: &judge}); !valid {
		return fmt.Errorf("cached judge verdict is incomplete or inconsistent")
	}
	logicalUsage, logicalUsageComplete, err :=
		validateLongMemEvalJudgeAttemptUsage(judge.Attempts)
	if err != nil {
		return err
	}
	if judge.LogicalUsageComplete != logicalUsageComplete {
		return fmt.Errorf(
			"judge logical usage completeness does not match attempts",
		)
	}
	if logicalUsageComplete {
		if judge.LogicalTokenUsage == nil {
			return fmt.Errorf(
				"judge logical usage is complete but token usage is missing",
			)
		}
		if *judge.LogicalTokenUsage != logicalUsage {
			return fmt.Errorf(
				"judge logical token usage does not match attempts",
			)
		}
	} else if judge.LogicalTokenUsage != nil {
		return fmt.Errorf(
			"judge logical token usage is present but marked incomplete",
		)
	}
	return nil
}

func validateLongMemEvalJudgeAttemptUsage(
	attempts []lmeJudgeAttempt,
) (lmeTokenUsage, bool, error) {
	if len(attempts) == 0 {
		return lmeTokenUsage{}, false, nil
	}
	var total lmeTokenUsage
	for i, attempt := range attempts {
		usage, complete :=
			longMemEvalLogicalUsageFromCalls(attempt.ModelCalls)
		if attempt.LogicalUsageComplete != complete {
			return lmeTokenUsage{}, false, fmt.Errorf(
				"judge attempt %d logical usage completeness "+
					"does not match model calls",
				i,
			)
		}
		if !complete {
			if attempt.LogicalTokenUsage != nil {
				return lmeTokenUsage{}, false, fmt.Errorf(
					"judge attempt %d logical token usage is "+
						"present but marked incomplete",
					i,
				)
			}
			return lmeTokenUsage{}, false, nil
		}
		if attempt.LogicalTokenUsage == nil ||
			*attempt.LogicalTokenUsage != usage {
			return lmeTokenUsage{}, false, fmt.Errorf(
				"judge attempt %d logical token usage does not "+
					"match model calls",
				i,
			)
		}
		total.Add(usage)
	}
	return total, true, nil
}

func reusedLongMemEvalJudgeResult(
	judge lmeJudgeResult,
	source string,
) *lmeJudgeResult {
	result := judge
	result.VerdictSource = source
	result.TokenUsage = nil
	result.DurationMs = 0
	result.Attempts = append([]lmeJudgeAttempt(nil), judge.Attempts...)
	for i := range result.Attempts {
		result.Attempts[i].ModelCalls =
			cloneLongMemEvalModelCallTraces(
				result.Attempts[i].ModelCalls,
			)
		for j := range result.Attempts[i].ModelCalls {
			result.Attempts[i].ModelCalls[j].Source = source
		}
		result.Attempts[i].TokenUsage = nil
		result.Attempts[i].DurationMs = 0
	}
	return &result
}

func cloneLongMemEvalJudgeResult(judge *lmeJudgeResult) (*lmeJudgeResult, error) {
	data, err := json.Marshal(judge)
	if err != nil {
		return nil, fmt.Errorf("marshal LongMemEval judge cache result: %w", err)
	}
	var result lmeJudgeResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("clone LongMemEval judge cache result: %w", err)
	}
	return &result, nil
}

func clearLongMemEvalJudgeRunMetadata(metadata map[string]any) {
	for _, key := range []string{
		"judge_model",
		"judge_model_variant",
		"judge_build",
		"judge_runs",
		"judge_cache_format_version",
		"judge_cache_shared",
		"judge_cache_ledger_id",
		"judge_cache_initial_entries",
		"judge_cache_final_entries",
		"judge_cache_hits",
		"judge_cache_logical_usage_hits",
		"judge_cache_logical_usage_missing_hits",
		"judged_at",
		"judge_note",
	} {
		delete(metadata, key)
	}
}

func writeLongMemEvalJudgeCache(path string, cache *lmeJudgeCacheFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create LongMemEval judge cache directory: %w", err)
	}
	cache.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal LongMemEval judge cache: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temporary LongMemEval judge cache: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace LongMemEval judge cache: %w", err)
	}
	return nil
}
