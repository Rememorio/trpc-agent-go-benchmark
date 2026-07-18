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
	"slices"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	lmeAnswerSourceModel      = "model"
	lmeAnswerSourceExisting   = "existing-result"
	lmeAnswerSourceCurrentRun = "current-run-cache"
	lmeAnswerSourcePersistent = "persistent-cache"
)

type lmeAnswerCacheIdentity struct {
	ProtocolVersion string                        `json:"protocol_version"`
	PromptSHA256    string                        `json:"prompt_sha256"`
	PromptVersion   string                        `json:"prompt_version"`
	Model           string                        `json:"model"`
	ModelVariant    string                        `json:"model_variant"`
	Generation      lmeAnswerGenerationProvenance `json:"generation"`
}

type lmeAnswerCacheEntry struct {
	Identity  lmeAnswerCacheIdentity `json:"identity"`
	Answer    string                 `json:"answer"`
	CreatedAt string                 `json:"created_at"`
}

type lmeAnswerCacheFile struct {
	Version   string                         `json:"version"`
	LedgerID  string                         `json:"ledger_id"`
	UpdatedAt string                         `json:"updated_at,omitempty"`
	Entries   map[string]lmeAnswerCacheEntry `json:"entries"`
}

type longMemEvalAnswerCache struct {
	path       string
	file       lmeAnswerCacheFile
	persistent map[string]struct{}
	hits       int
}

func openConfiguredLongMemEvalAnswerCache() (*longMemEvalAnswerCache, error) {
	if strings.TrimSpace(*flagLMEAnswerCache) == "" {
		return nil, nil
	}
	return openLongMemEvalAnswerCache(*flagLMEAnswerCache)
}

func openLongMemEvalAnswerCache(path string) (*longMemEvalAnswerCache, error) {
	path = strings.TrimSpace(path)
	cache := &longMemEvalAnswerCache{
		path: path,
		file: lmeAnswerCacheFile{
			Version: lmeAnswerCacheFormatVersion,
			Entries: make(map[string]lmeAnswerCacheEntry),
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
			if err := writeLongMemEvalAnswerCache(path, &cache.file); err != nil {
				return nil, err
			}
			return cache, nil
		}
		return nil, fmt.Errorf("read LongMemEval answer cache: %w", err)
	}
	if err := json.Unmarshal(data, &cache.file); err != nil {
		return nil, fmt.Errorf("parse LongMemEval answer cache: %w", err)
	}
	if cache.file.Version != lmeAnswerCacheFormatVersion {
		return nil, fmt.Errorf(
			"unsupported LongMemEval answer cache version %q, want %q",
			cache.file.Version,
			lmeAnswerCacheFormatVersion,
		)
	}
	if strings.TrimSpace(cache.file.LedgerID) == "" {
		return nil, fmt.Errorf("LongMemEval answer cache is missing ledger_id")
	}
	if cache.file.Entries == nil {
		cache.file.Entries = make(map[string]lmeAnswerCacheEntry)
	}
	for key, entry := range cache.file.Entries {
		if err := validateLongMemEvalAnswerCacheEntry(key, entry); err != nil {
			return nil, fmt.Errorf(
				"validate LongMemEval answer cache entry %s: %w", key, err,
			)
		}
		cache.persistent[key] = struct{}{}
	}
	return cache, nil
}

func (c *longMemEvalAnswerCache) Persistent() bool {
	return c != nil && c.path != ""
}

func (c *longMemEvalAnswerCache) LedgerID() string {
	if c == nil || !c.Persistent() {
		return ""
	}
	return c.file.LedgerID
}

func (c *longMemEvalAnswerCache) Len() int {
	if c == nil {
		return 0
	}
	return len(c.file.Entries)
}

func (c *longMemEvalAnswerCache) Hits() int {
	if c == nil {
		return 0
	}
	return c.hits
}

func (c *longMemEvalAnswerCache) Lookup(key string) (string, string, bool) {
	if c == nil {
		return "", "", false
	}
	entry, ok := c.file.Entries[key]
	if !ok {
		return "", "", false
	}
	source := lmeAnswerSourceCurrentRun
	if _, ok := c.persistent[key]; ok {
		source = lmeAnswerSourcePersistent
	}
	c.hits++
	return entry.Answer, source, true
}

func (c *longMemEvalAnswerCache) Put(
	key string,
	identity lmeAnswerCacheIdentity,
	answer string,
) error {
	if c == nil {
		return nil
	}
	if _, ok := c.file.Entries[key]; ok {
		return nil
	}
	entry := lmeAnswerCacheEntry{
		Identity:  identity,
		Answer:    strings.TrimSpace(answer),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := validateLongMemEvalAnswerCacheEntry(key, entry); err != nil {
		return err
	}
	c.file.Entries[key] = entry
	if !c.Persistent() {
		return nil
	}
	if err := writeLongMemEvalAnswerCache(c.path, &c.file); err != nil {
		delete(c.file.Entries, key)
		return err
	}
	return nil
}

func resolveLongMemEvalAnswer(
	ctx context.Context,
	llm model.Model,
	modelName string,
	modelVariant string,
	inst *lmeInstance,
	hits []memoryHit,
	cache *longMemEvalAnswerCache,
	existingAnswer string,
) (string, string, string, error) {
	identity, key, err := longMemEvalAnswerCacheKey(
		inst, hits, modelName, modelVariant,
	)
	if err != nil {
		return "", "", "", err
	}
	if answer, source, ok := cache.Lookup(key); ok {
		return answer, key, source, nil
	}
	if existingAnswer = strings.TrimSpace(existingAnswer); existingAnswer != "" && cache != nil {
		if err := cache.Put(key, identity, existingAnswer); err != nil {
			return existingAnswer, key, lmeAnswerSourceExisting, err
		}
		return existingAnswer, key, lmeAnswerSourceExisting, nil
	}
	answer, answerErr := answerFromMemories(ctx, llm, inst, hits)
	if answerErr == nil && strings.TrimSpace(answer) != "" {
		if err := cache.Put(key, identity, answer); err != nil {
			return answer, key, lmeAnswerSourceModel, err
		}
	}
	return answer, key, lmeAnswerSourceModel, answerErr
}

func longMemEvalAnswerCacheKey(
	inst *lmeInstance,
	hits []memoryHit,
	modelName string,
	modelVariant string,
) (lmeAnswerCacheIdentity, string, error) {
	prompt := buildLongMemEvalAnswerPrompt(inst, hits)
	promptSum := sha256.Sum256([]byte(prompt))
	identity := lmeAnswerCacheIdentity{
		ProtocolVersion: lmeProtocolVersion,
		PromptSHA256:    hex.EncodeToString(promptSum[:]),
		PromptVersion:   lmeAnswerPromptVersion,
		Model:           strings.TrimSpace(modelName),
		ModelVariant:    strings.ToLower(strings.TrimSpace(modelVariant)),
		Generation:      currentLongMemEvalAnswerGeneration(),
	}
	key, err := longMemEvalJSONSHA256(identity)
	if err != nil {
		return lmeAnswerCacheIdentity{}, "", fmt.Errorf(
			"hash LongMemEval answer cache key: %w", err,
		)
	}
	return identity, key, nil
}

func validateLongMemEvalAnswerCacheEntry(
	key string,
	entry lmeAnswerCacheEntry,
) error {
	wantKey, err := longMemEvalJSONSHA256(entry.Identity)
	if err != nil {
		return fmt.Errorf("hash identity: %w", err)
	}
	if key != wantKey {
		return fmt.Errorf("key mismatch: got %s, want %s", key, wantKey)
	}
	if strings.TrimSpace(entry.Answer) == "" {
		return fmt.Errorf("cached answer is empty")
	}
	return nil
}

func longMemEvalAnswerProvenanceMatches(
	metadata map[string]any,
	modelName string,
	modelVariant string,
) bool {
	if metadata == nil {
		return false
	}
	promptVersion, ok := lmeMetadataString(metadata, "answer_prompt_version")
	if !ok || promptVersion != lmeAnswerPromptVersion {
		return false
	}
	answerModel, ok := lmeMetadataString(metadata, "reanswer_model")
	if !ok {
		answerModel, ok = lmeMetadataString(metadata, "model")
	}
	if !ok || strings.TrimSpace(answerModel) != strings.TrimSpace(modelName) {
		return false
	}
	answerVariant, ok := lmeMetadataString(metadata, "reanswer_model_variant")
	if !ok {
		answerVariant, ok = lmeMetadataString(metadata, "model_variant")
	}
	if !ok || strings.ToLower(strings.TrimSpace(answerVariant)) !=
		strings.ToLower(strings.TrimSpace(modelVariant)) {
		return false
	}
	data, err := json.Marshal(metadata["answer_generation"])
	if err != nil {
		return false
	}
	var generation lmeAnswerGenerationProvenance
	if err := json.Unmarshal(data, &generation); err != nil {
		return false
	}
	return equalLongMemEvalAnswerGeneration(
		generation, currentLongMemEvalAnswerGeneration(),
	)
}

func equalLongMemEvalAnswerGeneration(
	a lmeAnswerGenerationProvenance,
	b lmeAnswerGenerationProvenance,
) bool {
	return a.PrimaryMaxTokens == b.PrimaryMaxTokens &&
		a.RetryMaxTokens == b.RetryMaxTokens &&
		a.MaxAttempts == b.MaxAttempts &&
		slices.Equal(a.RetryFinishReasons, b.RetryFinishReasons) &&
		a.RetryEmptyResponse == b.RetryEmptyResponse &&
		a.Temperature == b.Temperature &&
		a.ReasoningEffort == b.ReasoningEffort &&
		a.ThinkingEnabled == b.ThinkingEnabled
}

func initializeLongMemEvalAnswerCacheMetadata(
	metadata map[string]any,
	cache *longMemEvalAnswerCache,
) {
	clearLongMemEvalAnswerCacheMetadata(metadata)
	if metadata == nil || cache == nil {
		return
	}
	metadata["answer_cache_format_version"] = lmeAnswerCacheFormatVersion
	metadata["answer_cache_shared"] = cache.Persistent()
	if ledgerID := cache.LedgerID(); ledgerID != "" {
		metadata["answer_cache_ledger_id"] = ledgerID
	}
	metadata["answer_cache_initial_entries"] = cache.Len()
	metadata["answer_cache_note"] = "Identical complete answer prompts share one content-addressed model response; cache hits contribute zero answer-model calls and tokens."
}

func updateLongMemEvalAnswerCacheMetadata(
	metadata map[string]any,
	cache *longMemEvalAnswerCache,
) {
	if metadata == nil || cache == nil {
		return
	}
	metadata["answer_cache_final_entries"] = cache.Len()
	metadata["answer_cache_hits"] = cache.Hits()
}

func clearLongMemEvalAnswerCacheMetadata(metadata map[string]any) {
	for _, key := range []string{
		"answer_cache_format_version",
		"answer_cache_shared",
		"answer_cache_ledger_id",
		"answer_cache_initial_entries",
		"answer_cache_final_entries",
		"answer_cache_hits",
		"answer_cache_note",
	} {
		delete(metadata, key)
	}
}

func writeLongMemEvalAnswerCache(path string, cache *lmeAnswerCacheFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create LongMemEval answer cache directory: %w", err)
	}
	cache.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal LongMemEval answer cache: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temporary LongMemEval answer cache: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace LongMemEval answer cache: %w", err)
	}
	return nil
}
