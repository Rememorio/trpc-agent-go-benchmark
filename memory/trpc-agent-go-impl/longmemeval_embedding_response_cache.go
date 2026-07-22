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
	lmeEmbeddingCacheRecordHeader  = "header"
	lmeEmbeddingCacheRecordEntry   = "entry"
	lmeEmbeddingCacheMaxRecordSize = 64 << 20
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
	CreatedAt string                            `json:"created_at"`
}

type lmeEmbeddingResponseCacheRecord struct {
	Type     string                          `json:"type"`
	Version  string                          `json:"version,omitempty"`
	LedgerID string                          `json:"ledger_id,omitempty"`
	Key      string                          `json:"key,omitempty"`
	Entry    *lmeEmbeddingResponseCacheEntry `json:"entry,omitempty"`
}

type longMemEvalEmbeddingResponseCache struct {
	mu       sync.Mutex
	path     string
	ledgerID string
	entries  map[string]lmeEmbeddingResponseCacheEntry
	hits     int
	misses   int
	errors   int
}

func openConfiguredLongMemEvalEmbeddingResponseCache() (
	*longMemEvalEmbeddingResponseCache,
	error,
) {
	if strings.TrimSpace(*flagLMEEmbeddingResponseCache) == "" {
		return nil, nil
	}
	return openLongMemEvalEmbeddingResponseCache(
		*flagLMEEmbeddingResponseCache,
	)
}

func openLongMemEvalEmbeddingResponseCache(
	path string,
) (*longMemEvalEmbeddingResponseCache, error) {
	path = strings.TrimSpace(path)
	cache := &longMemEvalEmbeddingResponseCache{
		path:    path,
		entries: make(map[string]lmeEmbeddingResponseCacheEntry),
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
		if record.Type != lmeEmbeddingCacheRecordEntry || record.Entry == nil {
			return nil, fmt.Errorf(
				"invalid LongMemEval embedding response cache record on line %d",
				line,
			)
		}
		if _, duplicate := cache.entries[record.Key]; duplicate {
			return nil, fmt.Errorf(
				"duplicate LongMemEval embedding response cache key %q on line %d",
				record.Key, line,
			)
		}
		if err := validateLongMemEvalEmbeddingResponseCacheEntry(
			record.Key, *record.Entry,
		); err != nil {
			return nil, fmt.Errorf(
				"validate LongMemEval embedding response cache line %d: %w",
				line, err,
			)
		}
		cache.entries[record.Key] = *record.Entry
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

func (c *longMemEvalEmbeddingResponseCache) Lookup(
	key string,
) ([]float64, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		c.misses++
		return nil, false
	}
	c.hits++
	return append([]float64(nil), entry.Embedding...), true
}

func (c *longMemEvalEmbeddingResponseCache) Put(
	key string,
	identity lmeEmbeddingResponseCacheIdentity,
	embedding []float64,
) ([]float64, error) {
	if c == nil {
		return append([]float64(nil), embedding...), nil
	}
	entry := lmeEmbeddingResponseCacheEntry{
		Identity:  identity,
		Embedding: append([]float64(nil), embedding...),
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
	if c.Persistent() {
		record := lmeEmbeddingResponseCacheRecord{
			Type:  lmeEmbeddingCacheRecordEntry,
			Key:   key,
			Entry: &entry,
		}
		data, err := json.Marshal(record)
		if err != nil {
			c.errors++
			return nil, fmt.Errorf(
				"marshal LongMemEval embedding response cache entry: %w", err,
			)
		}
		data = append(data, '\n')
		file, err := os.OpenFile(c.path, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			c.errors++
			return nil, fmt.Errorf(
				"open LongMemEval embedding response cache for append: %w", err,
			)
		}
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil {
			c.errors++
			return nil, fmt.Errorf(
				"append LongMemEval embedding response cache: %w", writeErr,
			)
		}
		if closeErr != nil {
			c.errors++
			return nil, fmt.Errorf(
				"close LongMemEval embedding response cache: %w", closeErr,
			)
		}
	}
	c.entries[key] = entry
	return append([]float64(nil), entry.Embedding...), nil
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
	metadata["embedding_response_cache_note"] = "Identical embedding texts share an exact vector across paired runs; raw text is represented only by a hash. Embedding usage requests count logical embedder calls, calls and tokens count provider misses, and response_cache_hits count ledger hits."
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
}

func clearLongMemEvalEmbeddingResponseCacheMetadata(metadata map[string]any) {
	for _, key := range []string{
		"embedding_response_cache_format_version",
		"embedding_response_cache_shared",
		"embedding_response_cache_ledger_id",
		"embedding_response_cache_initial_entries",
		"embedding_response_cache_final_entries",
		"embedding_response_cache_hits",
		"embedding_response_cache_misses",
		"embedding_response_cache_errors",
		"embedding_response_cache_note",
	} {
		delete(metadata, key)
	}
}
