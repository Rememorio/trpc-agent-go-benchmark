//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const lmeSnapshotRefreshOutput = "snapshot_refreshed_results.json"

type lmeSnapshotProvenance struct {
	SourceSessions  []string
	SourceHasAnswer bool
	AttributedTo    string
}

type lmeSnapshotProvenanceReader interface {
	ReadSnapshotProvenance(
		ctx context.Context,
		userKey memory.UserKey,
	) (map[string]lmeSnapshotProvenance, bool, error)
}

func refreshLongMemEvalMemorySnapshots(
	ctx context.Context,
	path string,
	outputDir string,
) error {
	sourceDigest, err := longMemEvalFileSHA256(path)
	if err != nil {
		return fmt.Errorf("hash snapshot refresh source: %w", err)
	}
	result, err := loadLongMemEvalResults(path)
	if err != nil {
		return err
	}
	backendNames := parseMemoryBackends(*flagMemoryBackends)
	if err := validateLongMemEvalSnapshotRefresh(result, backendNames); err != nil {
		return err
	}
	backends := make(map[string]memoryBackend, len(backendNames))
	for _, name := range backendNames {
		backend, backendErr := newLongMemEvalSnapshotBackend(name)
		if backendErr != nil {
			return backendErr
		}
		backends[name] = backend
		defer backend.Close()
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create snapshot refresh output dir: %w", err)
	}
	return refreshLongMemEvalMemorySnapshotResult(
		ctx,
		result,
		backendNames,
		backends,
		sourceDigest,
		filepath.Join(outputDir, lmeSnapshotRefreshOutput),
	)
}

func validateLongMemEvalSnapshotRefresh(result *runResult, backendNames []string) error {
	if result == nil {
		return errors.New("snapshot refresh results are nil")
	}
	if len(backendNames) == 0 {
		return errors.New("snapshot refresh requires at least one memory backend")
	}
	available := make(map[string]bool)
	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		for name, br := range cr.BackendResults {
			if br != nil {
				available[name] = true
			}
		}
	}
	for _, name := range backendNames {
		if !available[name] {
			return fmt.Errorf("snapshot refresh source has no %s results", name)
		}
		if name != "pgvector" {
			continue
		}
		recordedSuffix, _ := result.Metadata["table_suffix"].(string)
		if recordedSuffix == "" {
			return errors.New("snapshot refresh source is missing table_suffix")
		}
		if recordedSuffix != *flagTableSuffix {
			return fmt.Errorf(
				"table-suffix %q does not match snapshot source %q",
				*flagTableSuffix, recordedSuffix,
			)
		}
	}
	return nil
}

func newLongMemEvalSnapshotBackend(name string) (memoryBackend, error) {
	switch strings.TrimSpace(name) {
	case "pgvector":
		return newLongMemEvalPGVectorBackend(
			nil, pgvectorExtractionConfig{}, true, nil,
		)
	case "mem0":
		return newBackend("mem0", nil, pgvectorExtractionConfig{}, nil)
	default:
		return nil, fmt.Errorf("unsupported backend %q", name)
	}
}

func refreshLongMemEvalMemorySnapshotResult(
	ctx context.Context,
	result *runResult,
	backendNames []string,
	backends map[string]memoryBackend,
	sourceDigest string,
	outPath string,
) error {
	if result == nil {
		return errors.New("snapshot refresh results are nil")
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	refresh := map[string]any{
		"source_sha256":           sourceDigest,
		"build":                   currentLongMemEvalBuildProvenance(),
		"backends":                append([]string(nil), backendNames...),
		"completed_backend_cases": 0,
		"refreshed_at":            time.Now().UTC().Format(time.RFC3339),
		"model_calls":             0,
		"preserved":               "ingest traces, retrieval, answers, judge, and provider usage",
		"recomputed":              "final memories, snapshot completeness, evidence, failure stage, and summary",
	}
	result.Metadata["snapshot_refresh"] = refresh

	completed := 0
	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		for _, backendName := range backendNames {
			backend := backends[backendName]
			if backend == nil {
				return fmt.Errorf("snapshot refresh backend %q is nil", backendName)
			}
			br := cr.BackendResults[backendName]
			if br == nil {
				continue
			}
			if strings.TrimSpace(br.UserID) == "" {
				return fmt.Errorf("case %s is missing %s user_id", cr.QuestionID, backendName)
			}
			log.Printf("refreshing memory snapshot %s backend=%s", cr.QuestionID, backendName)
			userKey := memory.UserKey{AppName: lmeAppName, UserID: br.UserID}
			stored, truncated, readErr := backend.Read(ctx, userKey)
			if readErr != nil {
				return fmt.Errorf("read persisted memories for %s/%s: %w",
					cr.QuestionID, backendName, readErr)
			}
			if err := verifyLongMemEvalPersistedMemoriesSubset(br.FinalMemories, stored); err != nil {
				return fmt.Errorf("verify persisted memories for %s/%s: %w",
					cr.QuestionID, backendName, err)
			}
			provenance := longMemEvalSnapshotProvenance(br.FinalMemories)
			if reader, ok := backend.(lmeSnapshotProvenanceReader); ok {
				persistedProvenance, provenanceTruncated, err := reader.ReadSnapshotProvenance(ctx, userKey)
				if err != nil {
					return fmt.Errorf("read persisted provenance for %s/%s: %w",
						cr.QuestionID, backendName, err)
				}
				truncated = truncated || provenanceTruncated
				for identity, item := range persistedProvenance {
					provenance[identity] = item
				}
			}
			stored, annotateErr := annotateLongMemEvalSnapshotProvenance(
				stored, provenance, backendName == "mem0",
			)
			if annotateErr != nil {
				return fmt.Errorf("annotate persisted memories for %s/%s: %w",
					cr.QuestionID, backendName, annotateErr)
			}
			br.FinalMemories = stored
			br.SnapshotTruncated = truncated
			br.Retrieval = annotateLongMemEvalRefreshedHits(br.Retrieval, stored)
			br.PreRerankRetrieval = annotateLongMemEvalRefreshedHits(br.PreRerankRetrieval, stored)
			inst := longMemEvalInstanceFromCaseResult(cr)
			previousEvidence := br.Evidence
			br.Evidence = computeEvidenceMetrics(inst, br, *flagVectorTopK)
			if previousEvidence != nil {
				br.Evidence.HasAnswerTurnLabels = previousEvidence.HasAnswerTurnLabels
			}
			br.FailureStage = classifyFailure(inst, br)
			completed++
			refresh["completed_backend_cases"] = completed
			result.Summary = buildLongMemEvalSummary(result.Cases)
			if err := writeLongMemEvalResults(outPath, result); err != nil {
				return fmt.Errorf("checkpoint snapshot refresh results: %w", err)
			}
			log.Printf("  %s memories=%d snapshot_truncated=%v", backendName, len(stored), truncated)
		}
	}
	result.Summary = buildLongMemEvalSummary(result.Cases)
	if !*flagLMEBlindProgress {
		printLongMemEvalSummary(result)
	}
	log.Printf("LongMemEval snapshot-refreshed results written to %s", outPath)
	return nil
}

func longMemEvalInstanceFromCaseResult(cr *caseResult) *lmeInstance {
	return &lmeInstance{
		QuestionID:       cr.QuestionID,
		QuestionType:     cr.QuestionType,
		Question:         cr.Question,
		QuestionDate:     cr.QuestionDate,
		Answer:           flexString(cr.Answer),
		AnswerSessionIDs: append([]string(nil), cr.AnswerSessionIDs...),
	}
}

func verifyLongMemEvalPersistedMemoriesSubset(want, got []memorySnapshot) error {
	counts := make(map[string]int, len(got))
	for _, mem := range got {
		digest, err := longMemEvalPersistedMemoryDigest([]memorySnapshot{mem})
		if err != nil {
			return err
		}
		counts[digest]++
	}
	for _, mem := range want {
		digest, err := longMemEvalPersistedMemoryDigest([]memorySnapshot{mem})
		if err != nil {
			return err
		}
		if counts[digest] == 0 {
			return fmt.Errorf("source memory %q is missing or changed", memoryIdentity(mem))
		}
		counts[digest]--
	}
	return nil
}

func longMemEvalSnapshotProvenance(memories []memorySnapshot) map[string]lmeSnapshotProvenance {
	out := make(map[string]lmeSnapshotProvenance, len(memories))
	for _, mem := range memories {
		identity := memoryIdentity(mem)
		if identity == "" ||
			len(mem.SourceSessions) == 0 && mem.AttributedTo == "" {
			continue
		}
		out[identity] = lmeSnapshotProvenance{
			SourceSessions:  append([]string(nil), mem.SourceSessions...),
			SourceHasAnswer: mem.SourceHasAnswer,
			AttributedTo:    normalizeMemoryAttribution(mem.AttributedTo),
		}
	}
	return out
}

func annotateLongMemEvalSnapshotProvenance(
	memories []memorySnapshot,
	provenance map[string]lmeSnapshotProvenance,
	require bool,
) ([]memorySnapshot, error) {
	out := append([]memorySnapshot(nil), memories...)
	for i := range out {
		identity := memoryIdentity(out[i])
		item, ok := provenance[identity]
		if !ok || len(item.SourceSessions) == 0 {
			if require {
				return nil, fmt.Errorf("memory %q is missing source provenance", identity)
			}
			continue
		}
		attributedTo := normalizeMemoryAttribution(item.AttributedTo)
		if require && attributedTo == "" {
			return nil, fmt.Errorf("memory %q is missing attribution provenance", identity)
		}
		out[i].SourceSessions = append([]string(nil), item.SourceSessions...)
		sort.Strings(out[i].SourceSessions)
		out[i].SourceSessions = compactStrings(out[i].SourceSessions)
		out[i].SourceHasAnswer = item.SourceHasAnswer
		out[i].AttributedTo = attributedTo
	}
	return out, nil
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(out) > 0 && out[len(out)-1] == value {
			continue
		}
		out = append(out, value)
	}
	return out
}

func (b *mem0Backend) ReadSnapshotProvenance(
	ctx context.Context,
	userKey memory.UserKey,
) (map[string]lmeSnapshotProvenance, bool, error) {
	if !b.selfHosted {
		return nil, false, nil
	}
	endpoint, err := url.Parse(strings.TrimRight(b.host, "/") + "/memories")
	if err != nil {
		return nil, false, err
	}
	query := endpoint.Query()
	query.Set("user_id", userKey.UserID)
	query.Set("top_k", strconv.Itoa(lmeMem0OSSSnapshotLimit))
	endpoint.RawQuery = query.Encode()
	reqCtx, cancel := contextWithOptionalTimeout(ctx, longMemEvalMem0OSSRequestTimeout())
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, false, err
	}
	client := b.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, false, fmt.Errorf("mem0 OSS list failed: status=%d body=%s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Results []struct {
			ID       string         `json:"id"`
			Metadata map[string]any `json:"metadata"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, false, err
	}
	out := make(map[string]lmeSnapshotProvenance, len(payload.Results))
	for _, record := range payload.Results {
		if !longMemEvalMem0RecordMatchesApp(record.Metadata, userKey.AppName) {
			continue
		}
		source, _ := record.Metadata["source_session"].(string)
		source = strings.TrimSpace(source)
		if strings.TrimSpace(record.ID) == "" || source == "" {
			continue
		}
		hasAnswer, _ := record.Metadata["has_answer"].(bool)
		attributedTo, _ := record.Metadata["attributed_to"].(string)
		out[record.ID] = lmeSnapshotProvenance{
			SourceSessions:  []string{source},
			SourceHasAnswer: hasAnswer,
			AttributedTo:    normalizeMemoryAttribution(attributedTo),
		}
	}
	return out, len(payload.Results) >= lmeMem0OSSSnapshotLimit, nil
}

func longMemEvalMem0RecordMatchesApp(metadata map[string]any, appName string) bool {
	if metadata == nil {
		return true
	}
	raw, ok := metadata["trpc_app_name"]
	if !ok || raw == nil {
		return true
	}
	recorded, ok := raw.(string)
	if !ok {
		return true
	}
	return strings.TrimSpace(recorded) == appName
}
