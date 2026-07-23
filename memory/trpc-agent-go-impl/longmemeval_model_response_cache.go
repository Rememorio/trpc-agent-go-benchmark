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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	lmeModelCallSourceModel      = "model"
	lmeModelCallSourceCurrentRun = "current-run-cache"
	lmeModelCallSourcePersistent = "persistent-cache"
)

type lmeModelResponseCacheIdentity struct {
	FormatVersion string `json:"format_version"`
	RequestSHA256 string `json:"request_sha256"`
	Model         string `json:"model"`
	ModelVariant  string `json:"model_variant"`
}

type lmeModelResponseCacheEntry struct {
	Identity  lmeModelResponseCacheIdentity `json:"identity"`
	Responses []*model.Response             `json:"responses"`
	CreatedAt string                        `json:"created_at"`
}

type lmeModelResponseCacheFile struct {
	Version   string                                `json:"version"`
	LedgerID  string                                `json:"ledger_id"`
	UpdatedAt string                                `json:"updated_at,omitempty"`
	Entries   map[string]lmeModelResponseCacheEntry `json:"entries"`
}

type longMemEvalModelResponseCache struct {
	mu         sync.Mutex
	path       string
	file       lmeModelResponseCacheFile
	persistent map[string]struct{}
	hits       int
	misses     int
	errors     int
}

type lmeModelRequestTool struct {
	Key         string            `json:"key"`
	Declaration *tool.Declaration `json:"declaration"`
}

type lmeModelRequestFingerprint struct {
	Messages         []model.Message         `json:"messages"`
	Generation       model.GenerationConfig  `json:"generation"`
	StructuredOutput *model.StructuredOutput `json:"structured_output,omitempty"`
	ExtraFields      map[string]any          `json:"extra_fields,omitempty"`
	Headers          map[string]string       `json:"headers,omitempty"`
	Tools            []lmeModelRequestTool   `json:"tools,omitempty"`
}

func openConfiguredLongMemEvalModelResponseCache() (
	*longMemEvalModelResponseCache,
	error,
) {
	if strings.TrimSpace(*flagLMEModelResponseCache) == "" {
		return nil, nil
	}
	return openLongMemEvalModelResponseCache(*flagLMEModelResponseCache)
}

func openLongMemEvalModelResponseCache(
	path string,
) (*longMemEvalModelResponseCache, error) {
	path = strings.TrimSpace(path)
	cache := &longMemEvalModelResponseCache{
		path: path,
		file: lmeModelResponseCacheFile{
			Version: lmeModelCacheFormatVersion,
			Entries: make(map[string]lmeModelResponseCacheEntry),
		},
		persistent: make(map[string]struct{}),
	}
	if path == "" {
		return cache, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read LongMemEval model response cache: %w", err)
		}
		ledgerID, err := newLongMemEvalLedgerID()
		if err != nil {
			return nil, err
		}
		cache.file.LedgerID = ledgerID
		if err := writeLongMemEvalModelResponseCache(path, &cache.file); err != nil {
			return nil, err
		}
		return cache, nil
	}
	if err := json.Unmarshal(data, &cache.file); err != nil {
		return nil, fmt.Errorf("parse LongMemEval model response cache: %w", err)
	}
	if cache.file.Version != lmeModelCacheFormatVersion {
		return nil, fmt.Errorf(
			"unsupported LongMemEval model response cache version %q, want %q",
			cache.file.Version, lmeModelCacheFormatVersion,
		)
	}
	if strings.TrimSpace(cache.file.LedgerID) == "" {
		return nil, fmt.Errorf(
			"LongMemEval model response cache is missing ledger_id",
		)
	}
	if cache.file.Entries == nil {
		cache.file.Entries = make(map[string]lmeModelResponseCacheEntry)
	}
	for key, entry := range cache.file.Entries {
		if err := validateLongMemEvalModelResponseCacheEntry(key, entry); err != nil {
			return nil, fmt.Errorf(
				"validate LongMemEval model response cache entry %s: %w",
				key, err,
			)
		}
		cache.persistent[key] = struct{}{}
	}
	return cache, nil
}

func (c *longMemEvalModelResponseCache) Persistent() bool {
	return c != nil && c.path != ""
}

func (c *longMemEvalModelResponseCache) LedgerID() string {
	if c == nil || !c.Persistent() {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.file.LedgerID
}

func (c *longMemEvalModelResponseCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.file.Entries)
}

func (c *longMemEvalModelResponseCache) Stats() (hits, misses, errors int) {
	if c == nil {
		return 0, 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses, c.errors
}

func (c *longMemEvalModelResponseCache) LogicalUsage(
	key string,
) (lmeTokenUsage, bool) {
	if c == nil {
		return lmeTokenUsage{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.file.Entries[key]
	if !ok {
		return lmeTokenUsage{}, false
	}
	usage := longMemEvalModelResponseUsage(entry.Responses)
	return usage, !usage.IsZero()
}

func (c *longMemEvalModelResponseCache) Lookup(
	key string,
) ([]*model.Response, string, *lmeTokenUsage, bool, error) {
	if c == nil {
		return nil, "", nil, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.file.Entries[key]
	if !ok {
		c.misses++
		return nil, "", nil, false, nil
	}
	logicalUsage := longMemEvalModelResponseUsage(entry.Responses)
	responses, err := cloneLongMemEvalModelResponses(entry.Responses, true)
	if err != nil {
		c.errors++
		return nil, "", nil, false, fmt.Errorf(
			"clone LongMemEval cached model responses: %w", err,
		)
	}
	source := lmeModelCallSourceCurrentRun
	if _, ok := c.persistent[key]; ok {
		source = lmeModelCallSourcePersistent
	}
	c.hits++
	return responses, source, tokenUsagePtr(logicalUsage), true, nil
}

func (c *longMemEvalModelResponseCache) Put(
	key string,
	identity lmeModelResponseCacheIdentity,
	responses []*model.Response,
) error {
	if c == nil {
		return nil
	}
	cloned, err := cloneLongMemEvalModelResponses(responses, false)
	if err != nil {
		c.recordError()
		return fmt.Errorf("clone LongMemEval model responses: %w", err)
	}
	entry := lmeModelResponseCacheEntry{
		Identity:  identity,
		Responses: cloned,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := validateLongMemEvalModelResponseCacheEntry(key, entry); err != nil {
		c.recordError()
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.file.Entries[key]; ok {
		return nil
	}
	c.file.Entries[key] = entry
	if !c.Persistent() {
		return nil
	}
	if err := writeLongMemEvalModelResponseCache(c.path, &c.file); err != nil {
		delete(c.file.Entries, key)
		c.errors++
		return err
	}
	return nil
}

func (c *longMemEvalModelResponseCache) recordError() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.errors++
	c.mu.Unlock()
}

func longMemEvalModelResponseCacheKey(
	req *model.Request,
	modelName string,
	modelVariant string,
) (lmeModelResponseCacheIdentity, string, error) {
	requestSHA256, err := longMemEvalModelRequestSHA256(req)
	if err != nil {
		return lmeModelResponseCacheIdentity{}, "", fmt.Errorf(
			"hash LongMemEval model request: %w", err,
		)
	}
	identity := lmeModelResponseCacheIdentity{
		FormatVersion: lmeModelCacheFormatVersion,
		RequestSHA256: requestSHA256,
		Model:         strings.TrimSpace(modelName),
		ModelVariant:  strings.ToLower(strings.TrimSpace(modelVariant)),
	}
	key, err := longMemEvalJSONSHA256(identity)
	if err != nil {
		return lmeModelResponseCacheIdentity{}, "", fmt.Errorf(
			"hash LongMemEval model response cache key: %w", err,
		)
	}
	return identity, key, nil
}

func longMemEvalModelRequestSHA256(req *model.Request) (string, error) {
	if req == nil {
		return longMemEvalJSONSHA256(nil)
	}
	tools := make([]lmeModelRequestTool, 0, len(req.Tools))
	for key, configuredTool := range req.Tools {
		var declaration *tool.Declaration
		if configuredTool != nil {
			declaration = configuredTool.Declaration()
		}
		tools = append(tools, lmeModelRequestTool{
			Key:         key,
			Declaration: declaration,
		})
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Key < tools[j].Key
	})
	return longMemEvalJSONSHA256(lmeModelRequestFingerprint{
		Messages:         req.Messages,
		Generation:       req.GenerationConfig,
		StructuredOutput: req.StructuredOutput,
		ExtraFields:      req.ExtraFields,
		Headers:          req.Headers,
		Tools:            tools,
	})
}

func validateLongMemEvalModelResponseCacheEntry(
	key string,
	entry lmeModelResponseCacheEntry,
) error {
	wantKey, err := longMemEvalJSONSHA256(entry.Identity)
	if err != nil {
		return fmt.Errorf("hash identity: %w", err)
	}
	if key != wantKey {
		return fmt.Errorf("key mismatch: got %s, want %s", key, wantKey)
	}
	if entry.Identity.FormatVersion != lmeModelCacheFormatVersion {
		return fmt.Errorf(
			"identity format version = %q, want %q",
			entry.Identity.FormatVersion, lmeModelCacheFormatVersion,
		)
	}
	if len(entry.Responses) == 0 {
		return fmt.Errorf("cached model response stream is empty")
	}
	var nonNilResponse, completed bool
	for _, response := range entry.Responses {
		if response == nil {
			continue
		}
		nonNilResponse = true
		completed = completed || response.Done
		if response.Error != nil {
			return fmt.Errorf("cached model response stream contains an error")
		}
	}
	if !nonNilResponse {
		return fmt.Errorf("cached model response stream contains only nil responses")
	}
	if !completed {
		return fmt.Errorf("cached model response stream is incomplete")
	}
	return nil
}

func cloneLongMemEvalModelResponse(response *model.Response) *model.Response {
	if response == nil {
		return nil
	}
	return response.Clone()
}

func cloneLongMemEvalModelResponses(
	responses []*model.Response,
	stripUsage bool,
) ([]*model.Response, error) {
	data, err := json.Marshal(responses)
	if err != nil {
		return nil, err
	}
	var cloned []*model.Response
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	if stripUsage {
		for _, response := range cloned {
			if response != nil {
				response.Usage = nil
			}
		}
	}
	return cloned, nil
}

func longMemEvalModelResponseUsage(
	responses []*model.Response,
) lmeTokenUsage {
	var usage lmeTokenUsage
	for _, response := range responses {
		if response != nil && response.Usage != nil {
			usage.Add(longMemEvalModelUsage(response.Usage))
		}
	}
	return usage
}

func initializeLongMemEvalModelResponseCacheMetadata(
	metadata map[string]any,
	cache *longMemEvalModelResponseCache,
) {
	clearLongMemEvalModelResponseCacheMetadata(metadata)
	if metadata == nil || cache == nil {
		return
	}
	metadata["model_response_cache_format_version"] = lmeModelCacheFormatVersion
	metadata["model_response_cache_shared"] = cache.Persistent()
	if ledgerID := cache.LedgerID(); ledgerID != "" {
		metadata["model_response_cache_ledger_id"] = ledgerID
	}
	metadata["model_response_cache_initial_entries"] = cache.Len()
	metadata["model_response_cache_note"] = "Identical primary-run model requests share a content-addressed response stream; request prompts and headers are represented only by a hash. Cache hits contribute zero provider calls and tokens, while each model-call trace retains the cached response's original logical_token_usage."
}

func updateLongMemEvalModelResponseCacheMetadata(
	metadata map[string]any,
	cache *longMemEvalModelResponseCache,
) {
	if metadata == nil || cache == nil {
		return
	}
	hits, misses, cacheErrors := cache.Stats()
	metadata["model_response_cache_final_entries"] = cache.Len()
	metadata["model_response_cache_hits"] = hits
	metadata["model_response_cache_misses"] = misses
	metadata["model_response_cache_errors"] = cacheErrors
}

func clearLongMemEvalModelResponseCacheMetadata(metadata map[string]any) {
	for _, key := range []string{
		"model_response_cache_format_version",
		"model_response_cache_shared",
		"model_response_cache_ledger_id",
		"model_response_cache_initial_entries",
		"model_response_cache_final_entries",
		"model_response_cache_hits",
		"model_response_cache_misses",
		"model_response_cache_errors",
		"model_response_cache_note",
	} {
		delete(metadata, key)
	}
}

func writeLongMemEvalModelResponseCache(
	path string,
	cache *lmeModelResponseCacheFile,
) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf(
			"create LongMemEval model response cache directory: %w", err,
		)
	}
	cache.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal LongMemEval model response cache: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf(
			"write temporary LongMemEval model response cache: %w", err,
		)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace LongMemEval model response cache: %w", err)
	}
	return nil
}
