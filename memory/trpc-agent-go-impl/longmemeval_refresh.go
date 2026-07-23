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
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	lmeRetrievalRefreshOutput = "retrieval_refreshed_results.json"
)

func refreshLongMemEvalRetrievalResults(
	ctx context.Context,
	path string,
	outputDir string,
) error {
	sourceDigest, err := longMemEvalFileSHA256(path)
	if err != nil {
		return fmt.Errorf("hash retrieval refresh source: %w", err)
	}
	result, err := loadLongMemEvalResults(path)
	if err != nil {
		return err
	}
	if err := validateLongMemEvalRetrievalRefresh(result); err != nil {
		return err
	}
	if *flagLMEAnswer {
		if err := validateLongMemEvalResultProtocol(
			result.Metadata, currentLongMemEvalProtocol(),
		); err != nil {
			return fmt.Errorf("validate LongMemEval retrieval refresh protocol: %w", err)
		}
	}
	implementation, err := longMemEvalRetrievalRefreshImplementation(result)
	if err != nil {
		return err
	}
	modelName := getModelName()
	modelVariant := getModelVariant()
	var baseLLM model.Model
	if *flagLMEAnswer {
		baseLLM, err = newEvaluationModel(modelName, modelVariant)
		if err != nil {
			return err
		}
	}
	embeddingResponseCache, err :=
		openConfiguredLongMemEvalEmbeddingResponseCache()
	if err != nil {
		return err
	}
	backend, err := newLongMemEvalPGVectorBackend(
		nil, pgvectorExtractionConfig{}, true, embeddingResponseCache,
	)
	if err != nil {
		return err
	}
	defer backend.Close()
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create retrieval refresh output dir: %w", err)
	}
	return refreshLongMemEvalRetrievalResult(
		ctx,
		result,
		backend,
		baseLLM,
		modelName,
		modelVariant,
		*flagLMEAnswer,
		implementation,
		sourceDigest,
		filepath.Join(outputDir, lmeRetrievalRefreshOutput),
		embeddingResponseCache,
	)
}

func longMemEvalRetrievalRefreshImplementation(result *runResult) (string, error) {
	source, ok := lmeMetadataString(result.Metadata, "implementation")
	if !ok || source == "" || source == "unspecified" {
		return "", errors.New(
			"retrieval refresh source is missing a specific implementation label",
		)
	}
	target := longMemEvalImplementation()
	if target == "unspecified" {
		return "", errors.New(
			"retrieval refresh requires -lme-implementation or LME_IMPLEMENTATION",
		)
	}
	if target == source {
		return "", fmt.Errorf(
			"retrieval refresh implementation %q matches the source; use a distinct label",
			target,
		)
	}
	return target, nil
}

func validateLongMemEvalRetrievalRefresh(result *runResult) error {
	if result == nil {
		return errors.New("retrieval refresh results are nil")
	}
	recordedSuffix, _ := result.Metadata["table_suffix"].(string)
	if recordedSuffix == "" {
		return errors.New("retrieval refresh source is missing table_suffix")
	}
	if recordedSuffix != *flagTableSuffix {
		return fmt.Errorf(
			"table-suffix %q does not match retrieval source %q",
			*flagTableSuffix, recordedSuffix,
		)
	}
	checks := []struct {
		key  string
		want string
	}{
		{key: "model", want: getModelName()},
		{key: "model_variant", want: getModelVariant()},
		{key: "embedding_model", want: getEmbedModelName()},
	}
	for _, check := range checks {
		raw, exists := result.Metadata[check.key]
		got, isString := raw.(string)
		if !exists || !isString {
			return fmt.Errorf("retrieval refresh source is missing %s", check.key)
		}
		if got != check.want {
			return fmt.Errorf(
				"%s %q does not match retrieval source %q",
				strings.ReplaceAll(check.key, "_", "-"), check.want, got,
			)
		}
	}
	recordedTopK, ok := longMemEvalMetadataInt(result.Metadata["top_k"])
	if !ok || recordedTopK <= 0 {
		return errors.New("retrieval refresh source has invalid top_k")
	}
	if recordedTopK != *flagVectorTopK {
		return fmt.Errorf(
			"vector-topk %d does not match retrieval source %d",
			*flagVectorTopK, recordedTopK,
		)
	}
	hasPGVector := false
	for _, cr := range result.Cases {
		if cr != nil && cr.BackendResults["pgvector"] != nil {
			hasPGVector = true
			break
		}
	}
	if !hasPGVector {
		return errors.New("retrieval refresh source has no pgvector results")
	}
	return nil
}

func longMemEvalMetadataInt(value any) (int, bool) {
	switch value := value.(type) {
	case int:
		return value, true
	case float64:
		return int(value), value == float64(int(value))
	default:
		return 0, false
	}
}

func refreshLongMemEvalRetrievalResult(
	ctx context.Context,
	result *runResult,
	backend memoryBackend,
	baseLLM model.Model,
	modelName string,
	modelVariant string,
	answerEnabled bool,
	implementation string,
	sourceDigest string,
	outPath string,
	embeddingResponseCache *longMemEvalEmbeddingResponseCache,
) error {
	if result == nil {
		return errors.New("retrieval refresh results are nil")
	}
	if backend == nil {
		return errors.New("retrieval refresh backend is nil")
	}
	if answerEnabled && baseLLM == nil {
		return errors.New("retrieval refresh model is nil")
	}
	var answerCache *longMemEvalAnswerCache
	if answerEnabled {
		var err error
		answerCache, err = openConfiguredLongMemEvalAnswerCache()
		if err != nil {
			return err
		}
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	initializeLongMemEvalEmbeddingResponseCacheMetadata(
		result.Metadata, embeddingResponseCache,
	)
	sourceImplementation, _ := lmeMetadataString(
		result.Metadata, "implementation",
	)
	refreshBuild := currentLongMemEvalBuildProvenance()
	refreshedAt := time.Now().UTC().Format(time.RFC3339)
	preservedCostScope := "ingestion and original query embedding; answer usage is replaced"
	if !answerEnabled {
		preservedCostScope = "ingestion and original query embedding; source answer usage is removed"
	}
	refresh := map[string]any{
		"backend":               backend.Name(),
		"source_implementation": sourceImplementation,
		"implementation":        implementation,
		"source_sha256":         sourceDigest,
		"build":                 refreshBuild,
		"model":                 modelName,
		"model_variant":         modelVariant,
		"embedding_model":       getEmbedModelName(),
		"table_suffix":          *flagTableSuffix,
		"top_k":                 *flagVectorTopK,
		"answer_enabled":        answerEnabled,
		"completed_cases":       0,
		"embedding_usage":       lmeEmbeddingUsage{},
		"refreshed_at":          refreshedAt,
		"memory_verification":   "canonical persisted memories match source final_memories",
		"preserved_cost_scope":  preservedCostScope,
	}
	result.Metadata["retrieval_refresh"] = refresh
	result.Metadata["implementation"] = implementation
	result.Metadata["answer_enabled"] = answerEnabled
	if answerEnabled {
		delete(result.Metadata, "retrieval_refresh_note")
		result.Metadata["reanswer_model"] = modelName
		result.Metadata["reanswer_model_variant"] = modelVariant
		result.Metadata["reanswer_build"] = refreshBuild
		result.Metadata["reanswered_at"] = refreshedAt
		result.Metadata["reanswer_note"] = "Answers regenerated from refreshed PGVector retrieval hits using the recorded answer protocol."
	} else {
		for _, key := range []string{
			"reanswer_model",
			"reanswer_model_variant",
			"reanswer_build",
			"reanswered_at",
			"reanswer_note",
		} {
			delete(result.Metadata, key)
		}
		result.Metadata["retrieval_refresh_note"] = "Retrieval-only refresh; source answers, scores, and answer usage were cleared without model calls."
	}
	if answerEnabled {
		result.Metadata["answer_generation"] = currentLongMemEvalAnswerGeneration()
		result.Metadata["answer_execution"] = currentLongMemEvalAnswerExecution()
		result.Metadata["answer_scoring"] = "raw model output; no retrieval-assisted answer post-processing"
	} else {
		delete(result.Metadata, "answer_generation")
		delete(result.Metadata, "answer_execution")
		result.Metadata["answer_scoring"] = "disabled for retrieval-only refresh"
	}
	result.Metadata["answer_prompt_version"] = lmeAnswerPromptVersion
	result.Metadata["judge_prompt_version"] = lmeJudgePromptVersion
	result.Metadata["judge_protocol_version"] = lmeJudgeProtocolVersion
	result.Metadata["judge_generation"] = currentLongMemEvalJudgeGeneration()
	initializeLongMemEvalAnswerCacheMetadata(result.Metadata, answerCache)
	clearLongMemEvalJudgeRunMetadata(result.Metadata)
	for _, cr := range result.Cases {
		if cr != nil {
			for _, br := range cr.BackendResults {
				if br != nil {
					br.Judge = nil
				}
			}
		}
	}

	var embeddingUsage lmeEmbeddingUsage
	completed := 0
	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		br := cr.BackendResults[backend.Name()]
		if br == nil {
			continue
		}
		if strings.TrimSpace(br.UserID) == "" {
			return fmt.Errorf("case %s is missing %s user_id", cr.QuestionID, backend.Name())
		}
		log.Printf("refreshing retrieval %s type=%s", cr.QuestionID, cr.QuestionType)
		userKey := memory.UserKey{AppName: lmeAppName, UserID: br.UserID}
		stored, snapshotTruncated, readErr := backend.Read(ctx, userKey)
		if readErr != nil {
			return fmt.Errorf("read persisted memories for %s: %w", cr.QuestionID, readErr)
		}
		if snapshotTruncated {
			return fmt.Errorf("verify persisted memories for %s: snapshot is truncated", cr.QuestionID)
		}
		if err := verifyLongMemEvalPersistedMemories(br.FinalMemories, stored); err != nil {
			return fmt.Errorf("verify persisted memories for %s: %w", cr.QuestionID, err)
		}
		inst := &lmeInstance{
			QuestionID:       cr.QuestionID,
			QuestionType:     cr.QuestionType,
			Question:         cr.Question,
			QuestionDate:     cr.QuestionDate,
			Answer:           flexString(cr.Answer),
			AnswerSessionIDs: append([]string(nil), cr.AnswerSessionIDs...),
		}
		searchStart := time.Now()
		hits, searchErr := backend.Search(
			ctx,
			userKey,
			cr.Question,
			*flagVectorTopK,
		)
		hits = annotateLongMemEvalRefreshedHits(hits, br.FinalMemories)
		br.SearchDuration = time.Since(searchStart).Milliseconds()
		providerUsage := backend.SnapshotProviderUsage()
		embeddingUsage.Add(providerUsage.Embedding)
		br.PreRerankRetrieval = nil
		br.RerankModelCalls = nil
		br.RerankDuration = 0
		br.RerankRaw = ""
		br.RerankError = ""
		replaceLongMemEvalRerankUsage(br, lmeTokenUsage{})
		br.Retrieval = hits
		br.Judge = nil
		clearLongMemEvalRefreshAnswer(br)
		if searchErr != nil {
			br.Error = appendError(br.Error, "refresh search: "+searchErr.Error())
			br.FailureStage = "search_error"
		} else if answerEnabled {
			tracker := &lmeTokenTracker{}
			llm := &lmeTrackingModel{
				base: baseLLM, tracker: tracker, timeout: *flagLMEModelCallTimeout,
			}
			answerStart := time.Now()
			raw, cacheKey, source, attempts, usage, answerErr :=
				resolveLongMemEvalAnswerWithRetries(
					ctx, llm, tracker, modelName, modelVariant, inst, hits,
					answerCache, "",
				)
			br.AnswerDuration = time.Since(answerStart).Milliseconds()
			br.AnswerMaxAttempts = 1 + lmeAnswerMaxExtraAttempts
			br.AnswerAttempts = attempts
			br.AnswerModelCalls = longMemEvalAnswerAttemptCalls(attempts)
			replaceLongMemEvalAnswerUsage(br, usage)
			br.RawAnswer = raw
			br.Answer = strings.TrimSpace(raw)
			br.AnswerCacheKey = cacheKey
			br.AnswerSource = source
			if answerErr != nil {
				br.AnswerError = answerErr.Error()
			}
			scoreLongMemEvalAnswer(cr, br)
			previousEvidence := br.Evidence
			br.Evidence = computeEvidenceMetrics(inst, br, *flagVectorTopK)
			if previousEvidence != nil {
				br.Evidence.HasAnswerTurnLabels =
					previousEvidence.HasAnswerTurnLabels
			}
			br.FailureStage = classifyFailure(inst, br)
		} else {
			previousEvidence := br.Evidence
			br.Evidence = computeEvidenceMetrics(inst, br, *flagVectorTopK)
			if previousEvidence != nil {
				br.Evidence.HasAnswerTurnLabels =
					previousEvidence.HasAnswerTurnLabels
			}
			br.FailureStage = "retrieval_only"
		}
		completed++
		refresh["completed_cases"] = completed
		refresh["embedding_usage"] = embeddingUsage
		updateLongMemEvalAnswerCacheMetadata(result.Metadata, answerCache)
		updateLongMemEvalEmbeddingResponseCacheMetadata(
			result.Metadata, embeddingResponseCache,
		)
		result.Summary = buildLongMemEvalSummary(result.Cases)
		if err := writeLongMemEvalResults(outPath, result); err != nil {
			return fmt.Errorf("checkpoint retrieval refresh results: %w", err)
		}
		if *flagLMEBlindProgress || !answerEnabled {
			log.Printf("  %s hits=%d embed_calls=%d err=%v",
				backend.Name(), len(hits), providerUsage.Embedding.Calls, searchErr)
		} else {
			log.Printf("  %s hits=%d answer=%q embed_calls=%d err=%v",
				backend.Name(), len(hits), truncate(br.Answer, 80),
				providerUsage.Embedding.Calls, searchErr)
		}
	}
	refresh["embedding_usage"] = embeddingUsage
	updateLongMemEvalAnswerCacheMetadata(result.Metadata, answerCache)
	updateLongMemEvalEmbeddingResponseCacheMetadata(
		result.Metadata, embeddingResponseCache,
	)
	result.Summary = buildLongMemEvalSummary(result.Cases)
	printLongMemEvalSummary(result)
	log.Printf("LongMemEval retrieval-refreshed results written to %s", outPath)
	return nil
}

func clearLongMemEvalRefreshAnswer(br *backendResult) {
	if br == nil {
		return
	}
	resetLongMemEvalAnswerError(br)
	replaceLongMemEvalAnswerUsage(br, lmeTokenUsage{})
	br.AnswerUsage = nil
	br.Answer = ""
	br.RawAnswer = ""
	br.AnswerCacheKey = ""
	br.AnswerSource = ""
	br.AnswerMaxAttempts = 0
	br.AnswerAttempts = nil
	br.AnswerModelCalls = nil
	br.AnswerDuration = 0
	br.ExactMatch = false
	br.F1 = 0
	br.BLEU = 0
}

type lmePersistedMemoryIdentity struct {
	ID           string    `json:"id"`
	Memory       string    `json:"memory"`
	Kind         string    `json:"kind,omitempty"`
	EventTime    string    `json:"event_time,omitempty"`
	Participants []string  `json:"participants,omitempty"`
	Location     string    `json:"location,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

func verifyLongMemEvalPersistedMemories(
	want []memorySnapshot,
	got []memorySnapshot,
) error {
	wantDigest, err := longMemEvalPersistedMemoryDigest(want)
	if err != nil {
		return err
	}
	gotDigest, err := longMemEvalPersistedMemoryDigest(got)
	if err != nil {
		return err
	}
	if wantDigest != gotDigest {
		return fmt.Errorf(
			"memory digest mismatch: source=%s persisted=%s",
			wantDigest, gotDigest,
		)
	}
	return nil
}

func longMemEvalPersistedMemoryDigest(
	memories []memorySnapshot,
) (string, error) {
	identities := make([]lmePersistedMemoryIdentity, 0, len(memories))
	for _, mem := range memories {
		participants := append([]string(nil), mem.Participants...)
		sort.Strings(participants)
		identities = append(identities, lmePersistedMemoryIdentity{
			ID:           mem.ID,
			Memory:       mem.Memory,
			Kind:         mem.Kind,
			EventTime:    mem.EventTime,
			Participants: participants,
			Location:     mem.Location,
			CreatedAt:    mem.CreatedAt,
			UpdatedAt:    mem.UpdatedAt,
		})
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].ID != identities[j].ID {
			return identities[i].ID < identities[j].ID
		}
		return identities[i].Memory < identities[j].Memory
	})
	return longMemEvalJSONSHA256(identities)
}

func annotateLongMemEvalRefreshedHits(
	hits []memoryHit,
	saved []memorySnapshot,
) []memoryHit {
	provenance := make(map[string]map[string]bool, len(saved))
	answerProvenance := make(map[string]bool, len(saved))
	attribution := make(map[string]string, len(saved))
	for _, mem := range saved {
		key := memoryIdentity(mem)
		if key == "" {
			continue
		}
		if len(mem.SourceSessions) > 0 {
			sources := make(map[string]bool, len(mem.SourceSessions))
			for _, source := range mem.SourceSessions {
				sources[source] = true
			}
			provenance[key] = sources
		}
		if mem.SourceHasAnswer {
			answerProvenance[key] = true
		}
		if mem.AttributedTo != "" {
			attribution[key] = mem.AttributedTo
		}
	}
	out := annotateHits(hits, provenance, answerProvenance)
	for i := range out {
		key := memoryIdentity(memorySnapshot{
			ID: out[i].ID, Memory: out[i].Memory,
		})
		if attributedTo := normalizeMemoryAttribution(attribution[key]); attributedTo != "" {
			out[i].AttributedTo = attributedTo
		}
	}
	return out
}
