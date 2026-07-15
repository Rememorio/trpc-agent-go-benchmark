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
	modelName := getModelName()
	modelVariant := getModelVariant()
	baseLLM, err := newLongMemEvalModel(modelName, modelVariant)
	if err != nil {
		return err
	}
	backend, err := newLongMemEvalPGVectorBackend(
		nil, lmePGVectorExtractionConfig{}, true,
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
		sourceDigest,
		filepath.Join(outputDir, lmeRetrievalRefreshOutput),
	)
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
	sourceDigest string,
	outPath string,
) error {
	if result == nil {
		return errors.New("retrieval refresh results are nil")
	}
	if backend == nil {
		return errors.New("retrieval refresh backend is nil")
	}
	if baseLLM == nil {
		return errors.New("retrieval refresh model is nil")
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	refresh := map[string]any{
		"backend":              backend.Name(),
		"source_sha256":        sourceDigest,
		"build":                currentLongMemEvalBuildProvenance(),
		"model":                modelName,
		"model_variant":        modelVariant,
		"embedding_model":      getEmbedModelName(),
		"table_suffix":         *flagTableSuffix,
		"top_k":                *flagVectorTopK,
		"completed_cases":      0,
		"embedding_usage":      lmeEmbeddingUsage{},
		"refreshed_at":         time.Now().UTC().Format(time.RFC3339),
		"memory_verification":  "canonical persisted memories match source final_memories",
		"preserved_cost_scope": "ingestion and original query embedding; answer usage is replaced",
	}
	result.Metadata["retrieval_refresh"] = refresh
	result.Metadata["answer_generation"] = currentLongMemEvalAnswerGeneration()
	result.Metadata["answer_prompt_version"] = lmeAnswerPromptVersion
	result.Metadata["judge_prompt_version"] = lmeJudgePromptVersion
	result.Metadata["judge_generation"] = currentLongMemEvalJudgeGeneration()
	result.Metadata["answer_scoring"] = "raw model output; no retrieval-assisted answer post-processing"
	for _, key := range []string{
		"judge_model", "judge_model_variant", "judge_build", "judge_runs",
		"judged_at", "judge_note",
	} {
		delete(result.Metadata, key)
	}
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
		stored, readErr := backend.Read(ctx, userKey, len(br.FinalMemories)+1)
		if readErr != nil {
			return fmt.Errorf("read persisted memories for %s: %w", cr.QuestionID, readErr)
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
		if searchErr != nil {
			br.Error = appendError(br.Error, "refresh search: "+searchErr.Error())
			br.FailureStage = "search_error"
		} else {
			tracker := &lmeTokenTracker{}
			llm := &lmeTrackingModel{
				base: baseLLM, tracker: tracker, timeout: *flagLMEModelCallTimeout,
			}
			answerStart := time.Now()
			raw, answerErr := answerFromMemories(ctx, llm, inst, hits)
			br.AnswerDuration = time.Since(answerStart).Milliseconds()
			br.AnswerModelCalls = tracker.SnapshotCalls()
			usage := tracker.Snapshot()
			replaceLongMemEvalAnswerUsage(br, usage)
			br.RawAnswer = raw
			br.Answer = strings.TrimSpace(raw)
			if answerErr != nil {
				br.Error = appendError(br.Error, "refresh answer: "+answerErr.Error())
			}
			scoreLongMemEvalAnswer(cr, br)
			previousEvidence := br.Evidence
			br.Evidence = computeEvidenceMetrics(inst, br, *flagVectorTopK)
			if previousEvidence != nil {
				br.Evidence.HasAnswerTurnLabels =
					previousEvidence.HasAnswerTurnLabels
			}
			br.FailureStage = classifyFailure(inst, br)
			if answerErr != nil {
				br.FailureStage = "answer_error"
			}
		}
		completed++
		refresh["completed_cases"] = completed
		refresh["embedding_usage"] = embeddingUsage
		result.Summary = buildLongMemEvalSummary(result.Cases)
		if err := writeLongMemEvalResults(outPath, result); err != nil {
			return fmt.Errorf("checkpoint retrieval refresh results: %w", err)
		}
		log.Printf(
			"  %s hits=%d answer=%q embed_calls=%d err=%v",
			backend.Name(), len(hits), truncate(br.Answer, 80),
			providerUsage.Embedding.Calls, searchErr,
		)
	}
	refresh["embedding_usage"] = embeddingUsage
	result.Summary = buildLongMemEvalSummary(result.Cases)
	printLongMemEvalSummary(result)
	log.Printf("LongMemEval retrieval-refreshed results written to %s", outPath)
	return nil
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
	}
	return annotateHits(hits, provenance, answerProvenance)
}
