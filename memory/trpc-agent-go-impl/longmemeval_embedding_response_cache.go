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
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	lmeEmbeddingCacheRecordHeader      = "header"
	lmeEmbeddingCacheRecordEntry       = "entry"
	lmeEmbeddingCacheRecordUsageUpdate = "usage"
	lmeEmbeddingCacheMaxRecordSize     = 64 << 20
)

type lmeEmbeddingResponseCacheIdentity struct {
	FormatVersion string `json:"format_version"`
	TextSHA256    string `json:"text_sha256"`
	Model         string `json:"model"`
	Dimensions    int    `json:"dimensions"`
}

type lmeEmbeddingResponseCacheEntry struct {
	Identity  lmeEmbeddingResponseCacheIdentity `json:"identity"`
	Embedding []float64                         `json:"embedding"`
	Usage     *lmeEmbeddingLogicalUsage         `json:"usage,omitempty"`
	CreatedAt string                            `json:"created_at"`
}

type lmeEmbeddingLogicalUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type lmeEmbeddingResponseCacheRecord struct {
	Type     string                          `json:"type"`
	Version  string                          `json:"version,omitempty"`
	LedgerID string                          `json:"ledger_id,omitempty"`
	Key      string                          `json:"key,omitempty"`
	Entry    *lmeEmbeddingResponseCacheEntry `json:"entry,omitempty"`
	Usage    *lmeEmbeddingLogicalUsage       `json:"usage,omitempty"`
}

type lmeEmbeddingUsageRecovery struct {
	done  chan struct{}
	usage *lmeEmbeddingLogicalUsage
	err   error
}

type longMemEvalEmbeddingResponseCache struct {
	mu                  sync.Mutex
	path                string
	ledgerID            string
	entries             map[string]lmeEmbeddingResponseCacheEntry
	recovering          map[string]*lmeEmbeddingUsageRecovery
	requireHit          bool
	recoverMissingUsage bool
	hits                int
	misses              int
	errors              int
	usageRecoveries     int
}

func openConfiguredLongMemEvalEmbeddingResponseCache() (
	*longMemEvalEmbeddingResponseCache,
	error,
) {
	path := strings.TrimSpace(*flagLMEEmbeddingResponseCache)
	recoverMissingUsage :=
		*flagLMEEmbeddingResponseCacheRecoverMissingUsage
	if path == "" {
		if *flagLMEEmbeddingResponseCacheRequireHit {
			return nil, fmt.Errorf(
				"-lme-embedding-response-cache-require-hit requires " +
					"-lme-embedding-response-cache",
			)
		}
		if recoverMissingUsage {
			return nil, fmt.Errorf(
				"-lme-embedding-response-cache-recover-missing-usage " +
					"requires -lme-embedding-response-cache",
			)
		}
		return nil, nil
	}
	if recoverMissingUsage && !*flagLMEEmbeddingResponseCacheRequireHit {
		return nil, fmt.Errorf(
			"-lme-embedding-response-cache-recover-missing-usage " +
				"requires -lme-embedding-response-cache-require-hit",
		)
	}
	cache, err := openLongMemEvalEmbeddingResponseCache(path)
	if err != nil {
		return nil, err
	}
	cache.requireHit = *flagLMEEmbeddingResponseCacheRequireHit
	cache.recoverMissingUsage = recoverMissingUsage
	return cache, nil
}

func openLongMemEvalEmbeddingResponseCache(
	path string,
) (*longMemEvalEmbeddingResponseCache, error) {
	path = strings.TrimSpace(path)
	cache := &longMemEvalEmbeddingResponseCache{
		path:       path,
		entries:    make(map[string]lmeEmbeddingResponseCacheEntry),
		recovering: make(map[string]*lmeEmbeddingUsageRecovery),
	}
	if path == "" {
		return cache, nil
	}

	file, err := os.Open(path)
	if os.IsNotExist(err) {
		if err := cache.initializePersistentFile(); err != nil {
			return nil, err
		}
		return cache, nil
	}
	if err != nil {
		return nil, fmt.Errorf(
			"open LongMemEval embedding response cache: %w", err,
		)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), lmeEmbeddingCacheMaxRecordSize)
	line := 0
	for scanner.Scan() {
		line++
		var record lmeEmbeddingResponseCacheRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf(
				"parse LongMemEval embedding response cache line %d: %w",
				line, err,
			)
		}
		if line == 1 {
			if err := cache.loadHeader(record); err != nil {
				return nil, err
			}
			continue
		}
		if err := cache.loadRecord(record, line); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf(
			"scan LongMemEval embedding response cache: %w", err,
		)
	}
	if line == 0 {
		return nil, fmt.Errorf(
			"LongMemEval embedding response cache is empty",
		)
	}
	return cache, nil
}

func (c *longMemEvalEmbeddingResponseCache) loadRecord(
	record lmeEmbeddingResponseCacheRecord,
	line int,
) error {
	switch record.Type {
	case lmeEmbeddingCacheRecordEntry:
		if record.Entry == nil || record.Usage != nil {
			break
		}
		if _, duplicate := c.entries[record.Key]; duplicate {
			return fmt.Errorf(
				"duplicate LongMemEval embedding response cache key %q on line %d",
				record.Key, line,
			)
		}
		if err := validateLongMemEvalEmbeddingResponseCacheEntry(
			record.Key, *record.Entry,
		); err != nil {
			return fmt.Errorf(
				"validate LongMemEval embedding response cache line %d: %w",
				line, err,
			)
		}
		c.entries[record.Key] = *record.Entry
		return nil
	case lmeEmbeddingCacheRecordUsageUpdate:
		if record.Entry != nil || record.Usage == nil {
			break
		}
		entry, ok := c.entries[record.Key]
		if !ok {
			return fmt.Errorf(
				"LongMemEval embedding usage update for unknown key %q on line %d",
				record.Key, line,
			)
		}
		if entry.Usage != nil {
			return fmt.Errorf(
				"duplicate LongMemEval embedding usage update for key %q on line %d",
				record.Key, line,
			)
		}
		if err := validateLongMemEvalEmbeddingLogicalUsage(*record.Usage); err != nil {
			return fmt.Errorf(
				"validate LongMemEval embedding usage update line %d: %w",
				line, err,
			)
		}
		usage := *record.Usage
		entry.Usage = &usage
		c.entries[record.Key] = entry
		return nil
	}
	return fmt.Errorf(
		"invalid LongMemEval embedding response cache record on line %d",
		line,
	)
}

func (c *longMemEvalEmbeddingResponseCache) initializePersistentFile() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0755); err != nil {
		return fmt.Errorf(
			"create LongMemEval embedding response cache directory: %w", err,
		)
	}
	ledgerID, err := newLongMemEvalLedgerID()
	if err != nil {
		return err
	}
	c.ledgerID = ledgerID
	record := lmeEmbeddingResponseCacheRecord{
		Type:     lmeEmbeddingCacheRecordHeader,
		Version:  lmeEmbeddingCacheFormatVersion,
		LedgerID: ledgerID,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf(
			"marshal LongMemEval embedding response cache header: %w", err,
		)
	}
	data = append(data, '\n')
	if err := os.WriteFile(c.path, data, 0644); err != nil {
		return fmt.Errorf(
			"initialize LongMemEval embedding response cache: %w", err,
		)
	}
	return nil
}

func (c *longMemEvalEmbeddingResponseCache) loadHeader(
	record lmeEmbeddingResponseCacheRecord,
) error {
	if record.Type != lmeEmbeddingCacheRecordHeader ||
		record.Version != lmeEmbeddingCacheFormatVersion {
		return fmt.Errorf(
			"unsupported LongMemEval embedding response cache header type=%q version=%q",
			record.Type, record.Version,
		)
	}
	if strings.TrimSpace(record.LedgerID) == "" {
		return fmt.Errorf(
			"LongMemEval embedding response cache is missing ledger_id",
		)
	}
	c.ledgerID = record.LedgerID
	return nil
}

func (c *longMemEvalEmbeddingResponseCache) Persistent() bool {
	return c != nil && c.path != ""
}

func (c *longMemEvalEmbeddingResponseCache) RequireHit() bool {
	return c != nil && c.requireHit
}

func (c *longMemEvalEmbeddingResponseCache) RecoverMissingUsage() bool {
	return c != nil && c.recoverMissingUsage
}

func (c *longMemEvalEmbeddingResponseCache) LedgerID() string {
	if c == nil || !c.Persistent() {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ledgerID
}

func (c *longMemEvalEmbeddingResponseCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *longMemEvalEmbeddingResponseCache) Stats() (
	hits, misses, errors int,
) {
	if c == nil {
		return 0, 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses, c.errors
}

func (c *longMemEvalEmbeddingResponseCache) UsageRecoveries() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usageRecoveries
}

func (c *longMemEvalEmbeddingResponseCache) Lookup(
	key string,
) ([]float64, *lmeEmbeddingLogicalUsage, bool) {
	if c == nil {
		return nil, nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		c.misses++
		return nil, nil, false
	}
	c.hits++
	var usage *lmeEmbeddingLogicalUsage
	if entry.Usage != nil {
		copyUsage := *entry.Usage
		usage = &copyUsage
	}
	return append([]float64(nil), entry.Embedding...), usage, true
}

func (c *longMemEvalEmbeddingResponseCache) Put(
	key string,
	identity lmeEmbeddingResponseCacheIdentity,
	embedding []float64,
	usage *lmeEmbeddingLogicalUsage,
) ([]float64, error) {
	if c == nil {
		return append([]float64(nil), embedding...), nil
	}
	var copiedUsage *lmeEmbeddingLogicalUsage
	if usage != nil {
		value := *usage
		copiedUsage = &value
	}
	entry := lmeEmbeddingResponseCacheEntry{
		Identity:  identity,
		Embedding: append([]float64(nil), embedding...),
		Usage:     copiedUsage,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := validateLongMemEvalEmbeddingResponseCacheEntry(key, entry); err != nil {
		c.recordError()
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[key]; ok {
		return append([]float64(nil), existing.Embedding...), nil
	}
	if err := c.appendRecordLocked(lmeEmbeddingResponseCacheRecord{
		Type:  lmeEmbeddingCacheRecordEntry,
		Key:   key,
		Entry: &entry,
	}); err != nil {
		return nil, err
	}
	c.entries[key] = entry
	return append([]float64(nil), entry.Embedding...), nil
}

func (c *longMemEvalEmbeddingResponseCache) RecoverUsage(
	ctx context.Context,
	key string,
	recoverUsage func() (*lmeEmbeddingLogicalUsage, error),
) (*lmeEmbeddingLogicalUsage, error) {
	if c == nil {
		return nil, fmt.Errorf("LongMemEval embedding response cache is nil")
	}
	c.mu.Lock()
	entry, ok := c.entries[key]
	if !ok {
		c.errors++
		c.mu.Unlock()
		return nil, fmt.Errorf(
			"recover LongMemEval embedding usage for unknown key %q", key,
		)
	}
	if entry.Usage != nil {
		usage := *entry.Usage
		c.mu.Unlock()
		return &usage, nil
	}
	if active, ok := c.recovering[key]; ok {
		c.mu.Unlock()
		select {
		case <-active.done:
			return copyLongMemEvalEmbeddingLogicalUsage(active.usage), active.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	active := &lmeEmbeddingUsageRecovery{done: make(chan struct{})}
	c.recovering[key] = active
	c.mu.Unlock()

	usage, err := recoverUsage()
	if err == nil {
		usage, err = c.putUsage(key, usage)
	}

	c.mu.Lock()
	active.usage = copyLongMemEvalEmbeddingLogicalUsage(usage)
	active.err = err
	delete(c.recovering, key)
	close(active.done)
	c.mu.Unlock()
	return copyLongMemEvalEmbeddingLogicalUsage(usage), err
}

func (c *longMemEvalEmbeddingResponseCache) putUsage(
	key string,
	usage *lmeEmbeddingLogicalUsage,
) (*lmeEmbeddingLogicalUsage, error) {
	if usage == nil {
		c.recordError()
		return nil, fmt.Errorf("LongMemEval embedding usage is nil")
	}
	if err := validateLongMemEvalEmbeddingLogicalUsage(*usage); err != nil {
		c.recordError()
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		c.errors++
		return nil, fmt.Errorf(
			"update LongMemEval embedding usage for unknown key %q", key,
		)
	}
	if entry.Usage != nil {
		return copyLongMemEvalEmbeddingLogicalUsage(entry.Usage), nil
	}
	copied := *usage
	if err := c.appendRecordLocked(lmeEmbeddingResponseCacheRecord{
		Type:  lmeEmbeddingCacheRecordUsageUpdate,
		Key:   key,
		Usage: &copied,
	}); err != nil {
		return &copied, err
	}
	entry.Usage = &copied
	c.entries[key] = entry
	c.usageRecoveries++
	return &copied, nil
}

func (c *longMemEvalEmbeddingResponseCache) appendRecordLocked(
	record lmeEmbeddingResponseCacheRecord,
) error {
	if !c.Persistent() {
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		c.errors++
		return fmt.Errorf(
			"marshal LongMemEval embedding response cache record: %w", err,
		)
	}
	data = append(data, '\n')
	file, err := os.OpenFile(c.path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		c.errors++
		return fmt.Errorf(
			"open LongMemEval embedding response cache for append: %w", err,
		)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		c.errors++
		return fmt.Errorf(
			"append LongMemEval embedding response cache: %w", writeErr,
		)
	}
	if closeErr != nil {
		c.errors++
		return fmt.Errorf(
			"close LongMemEval embedding response cache: %w", closeErr,
		)
	}
	return nil
}

func copyLongMemEvalEmbeddingLogicalUsage(
	usage *lmeEmbeddingLogicalUsage,
) *lmeEmbeddingLogicalUsage {
	if usage == nil {
		return nil
	}
	copied := *usage
	return &copied
}

func (c *longMemEvalEmbeddingResponseCache) recordError() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.errors++
	c.mu.Unlock()
}

func longMemEvalEmbeddingResponseCacheKey(
	text string,
	modelName string,
	dimensions int,
) (lmeEmbeddingResponseCacheIdentity, string, error) {
	sum := sha256.Sum256([]byte(text))
	identity := lmeEmbeddingResponseCacheIdentity{
		FormatVersion: lmeEmbeddingCacheFormatVersion,
		TextSHA256:    hex.EncodeToString(sum[:]),
		Model:         strings.TrimSpace(modelName),
		Dimensions:    dimensions,
	}
	key, err := longMemEvalJSONSHA256(identity)
	if err != nil {
		return lmeEmbeddingResponseCacheIdentity{}, "", fmt.Errorf(
			"hash LongMemEval embedding response cache key: %w", err,
		)
	}
	return identity, key, nil
}

func validateLongMemEvalEmbeddingResponseCacheEntry(
	key string,
	entry lmeEmbeddingResponseCacheEntry,
) error {
	wantKey, err := longMemEvalJSONSHA256(entry.Identity)
	if err != nil {
		return fmt.Errorf("hash identity: %w", err)
	}
	if key != wantKey {
		return fmt.Errorf("key mismatch: got %s, want %s", key, wantKey)
	}
	if entry.Identity.FormatVersion != lmeEmbeddingCacheFormatVersion {
		return fmt.Errorf(
			"identity format version = %q, want %q",
			entry.Identity.FormatVersion, lmeEmbeddingCacheFormatVersion,
		)
	}
	if strings.TrimSpace(entry.Identity.Model) == "" {
		return fmt.Errorf("embedding model is empty")
	}
	if entry.Identity.Dimensions <= 0 ||
		len(entry.Embedding) != entry.Identity.Dimensions {
		return fmt.Errorf(
			"embedding dimensions = %d, want %d",
			len(entry.Embedding), entry.Identity.Dimensions,
		)
	}
	for i, value := range entry.Embedding {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("embedding value %d is not finite", i)
		}
	}
	if entry.Usage != nil {
		if err := validateLongMemEvalEmbeddingLogicalUsage(
			*entry.Usage,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateLongMemEvalEmbeddingLogicalUsage(
	usage lmeEmbeddingLogicalUsage,
) error {
	if usage.PromptTokens < 0 || usage.TotalTokens < usage.PromptTokens {
		return fmt.Errorf(
			"embedding usage prompt=%d total=%d is invalid",
			usage.PromptTokens, usage.TotalTokens,
		)
	}
	return nil
}

func initializeLongMemEvalEmbeddingResponseCacheMetadata(
	metadata map[string]any,
	cache *longMemEvalEmbeddingResponseCache,
) {
	clearLongMemEvalEmbeddingResponseCacheMetadata(metadata)
	if metadata == nil || cache == nil {
		return
	}
	metadata["embedding_response_cache_format_version"] =
		lmeEmbeddingCacheFormatVersion
	metadata["embedding_response_cache_shared"] = cache.Persistent()
	if ledgerID := cache.LedgerID(); ledgerID != "" {
		metadata["embedding_response_cache_ledger_id"] = ledgerID
	}
	metadata["embedding_response_cache_initial_entries"] = cache.Len()
	metadata["embedding_response_cache_require_hit"] = cache.RequireHit()
	metadata["embedding_response_cache_recover_missing_usage"] =
		cache.RecoverMissingUsage()
	metadata["embedding_response_cache_note"] = "Identical embedding texts share an exact vector across paired runs; raw text is represented only by a hash. Embedding requests count logical embedder calls; calls and tokens count provider misses or usage-recovery calls. Usage recovery discards provider vectors and preserves the sealed cached vector. Logical tokens include cache hits when the cache entry carries usage; logical_usage_missing_requests identifies legacy or failed requests without reconstructable usage."
}

func updateLongMemEvalEmbeddingResponseCacheMetadata(
	metadata map[string]any,
	cache *longMemEvalEmbeddingResponseCache,
) {
	if metadata == nil || cache == nil {
		return
	}
	hits, misses, cacheErrors := cache.Stats()
	metadata["embedding_response_cache_final_entries"] = cache.Len()
	metadata["embedding_response_cache_hits"] = hits
	metadata["embedding_response_cache_misses"] = misses
	metadata["embedding_response_cache_errors"] = cacheErrors
	metadata["embedding_response_cache_recovered_usage_entries"] =
		cache.UsageRecoveries()
}

func clearLongMemEvalEmbeddingResponseCacheMetadata(metadata map[string]any) {
	for _, key := range []string{
		"embedding_response_cache_format_version",
		"embedding_response_cache_shared",
		"embedding_response_cache_ledger_id",
		"embedding_response_cache_initial_entries",
		"embedding_response_cache_require_hit",
		"embedding_response_cache_recover_missing_usage",
		"embedding_response_cache_final_entries",
		"embedding_response_cache_hits",
		"embedding_response_cache_misses",
		"embedding_response_cache_errors",
		"embedding_response_cache_recovered_usage_entries",
		"embedding_response_cache_note",
	} {
		delete(metadata, key)
	}
}
