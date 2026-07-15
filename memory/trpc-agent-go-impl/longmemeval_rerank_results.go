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

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const lmeRerankedResultsOutput = "reranked_results.json"

func rerankLongMemEvalResults(
	ctx context.Context,
	path string,
	outputDir string,
) error {
	sourceDigest, err := longMemEvalFileSHA256(path)
	if err != nil {
		return fmt.Errorf("hash rerank source: %w", err)
	}
	result, err := loadLongMemEvalResults(path)
	if err != nil {
		return err
	}
	modelName := getModelName()
	modelVariant := getModelVariant()
	baseLLM, err := newLongMemEvalModel(modelName, modelVariant)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create rerank output dir: %w", err)
	}
	return rerankLongMemEvalResult(
		ctx,
		result,
		baseLLM,
		modelName,
		modelVariant,
		sourceDigest,
		filepath.Join(outputDir, lmeRerankedResultsOutput),
	)
}

func rerankLongMemEvalResult(
	ctx context.Context,
	result *runResult,
	baseLLM model.Model,
	modelName string,
	modelVariant string,
	sourceDigest string,
	outPath string,
) error {
	if result == nil {
		return errors.New("rerank results are nil")
	}
	if baseLLM == nil {
		return errors.New("rerank model is nil")
	}
	if *flagLMERerankTopN <= 0 {
		return fmt.Errorf(
			"lme-rerank-topn must be positive, got %d",
			*flagLMERerankTopN,
		)
	}
	sourceTopK, err := validateLongMemEvalRerankSource(result)
	if err != nil {
		return err
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	rerankBuild := currentLongMemEvalBuildProvenance()
	rerankGeneration := currentLongMemEvalRerankGeneration()
	rerankedAt := time.Now().UTC().Format(time.RFC3339)
	rerankMetadata := map[string]any{
		"source_sha256":        sourceDigest,
		"build":                rerankBuild,
		"model":                modelName,
		"model_variant":        modelVariant,
		"prompt_version":       lmeRerankPromptVersion,
		"generation":           rerankGeneration,
		"top_n":                *flagLMERerankTopN,
		"backend_scope":        "all saved backend retrieval hits",
		"completed_backends":   0,
		"reranked_at":          rerankedAt,
		"preserved_cost_scope": "ingestion and embedding usage; answer and rerank usage are replaced",
	}
	result.Metadata["retrieval_rerank"] = rerankMetadata
	result.Metadata["rerank_model"] = modelName
	result.Metadata["rerank_model_variant"] = modelVariant
	result.Metadata["rerank_build"] = rerankBuild
	result.Metadata["rerank_prompt_version"] = lmeRerankPromptVersion
	result.Metadata["rerank_generation"] = rerankGeneration
	result.Metadata["rerank_top_n"] = *flagLMERerankTopN
	result.Metadata["reanswer_model"] = modelName
	result.Metadata["reanswer_model_variant"] = modelVariant
	result.Metadata["reanswer_build"] = rerankBuild
	result.Metadata["reanswered_at"] = rerankedAt
	result.Metadata["reanswer_note"] = "Answers regenerated from the reranked saved hits using the recorded rerank model and answer protocol."
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
		if cr == nil {
			continue
		}
		for _, br := range cr.BackendResults {
			if br != nil {
				br.Judge = nil
			}
		}
	}

	completed := 0
	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		inst := &lmeInstance{
			QuestionID:       cr.QuestionID,
			QuestionType:     cr.QuestionType,
			Question:         cr.Question,
			QuestionDate:     cr.QuestionDate,
			Answer:           flexString(cr.Answer),
			AnswerSessionIDs: append([]string(nil), cr.AnswerSessionIDs...),
		}
		backendNames := make([]string, 0, len(cr.BackendResults))
		for backendName := range cr.BackendResults {
			backendNames = append(backendNames, backendName)
		}
		sort.Strings(backendNames)
		for _, backendName := range backendNames {
			br := cr.BackendResults[backendName]
			if br == nil {
				continue
			}
			log.Printf("reranking %s backend=%s type=%s",
				cr.QuestionID, backendName, cr.QuestionType)
			sourceHits := br.Retrieval
			if len(br.PreRerankRetrieval) > 0 {
				sourceHits = br.PreRerankRetrieval
			}
			br.PreRerankRetrieval = append([]memoryHit(nil), sourceHits...)
			br.RerankModelCalls = nil
			br.RerankDuration = 0
			br.RerankRaw = ""
			br.RerankError = ""

			rerankTracker := &lmeTokenTracker{}
			rerankLLM := &lmeTrackingModel{
				base: baseLLM, tracker: rerankTracker, timeout: *flagLMEModelCallTimeout,
			}
			rerankStart := time.Now()
			reranked, raw, rerankErr := rerankLongMemEvalHits(
				ctx, rerankLLM, inst, sourceHits, *flagLMERerankTopN,
			)
			br.RerankDuration = time.Since(rerankStart).Milliseconds()
			br.RerankModelCalls = rerankTracker.SnapshotCalls()
			rerankUsage := rerankTracker.Snapshot()
			replaceLongMemEvalRerankUsage(br, rerankUsage)
			br.RerankRaw = raw
			if rerankErr != nil {
				br.RerankError = rerankErr.Error()
				br.Retrieval = append([]memoryHit(nil), sourceHits...)
			} else {
				br.Retrieval = reranked
			}

			answerTracker := &lmeTokenTracker{}
			answerLLM := &lmeTrackingModel{
				base: baseLLM, tracker: answerTracker, timeout: *flagLMEModelCallTimeout,
			}
			answerStart := time.Now()
			rawAnswer, answerErr := answerFromMemories(
				ctx, answerLLM, inst, br.Retrieval,
			)
			br.AnswerDuration = time.Since(answerStart).Milliseconds()
			br.AnswerModelCalls = answerTracker.SnapshotCalls()
			replaceLongMemEvalAnswerUsage(br, answerTracker.Snapshot())
			br.RawAnswer = rawAnswer
			br.Answer = strings.TrimSpace(rawAnswer)
			br.Judge = nil
			if answerErr != nil {
				br.Error = appendError(br.Error, "rerank answer: "+answerErr.Error())
			}
			scoreLongMemEvalAnswer(cr, br)
			previousEvidence := br.Evidence
			br.Evidence = computeEvidenceMetrics(inst, br, sourceTopK)
			if previousEvidence != nil {
				br.Evidence.HasAnswerTurnLabels =
					previousEvidence.HasAnswerTurnLabels
			}
			br.FailureStage = classifyFailure(inst, br)
			if answerErr != nil {
				br.FailureStage = "answer_error"
			}
			completed++
			rerankMetadata["completed_backends"] = completed
			result.Summary = buildLongMemEvalSummary(result.Cases)
			if err := writeLongMemEvalResults(outPath, result); err != nil {
				return fmt.Errorf("checkpoint reranked results: %w", err)
			}
			log.Printf("  %s hits=%d calls=%d tokens=%d answer=%q rerank_error=%q",
				backendName,
				len(br.Retrieval),
				rerankUsage.LLMCalls,
				rerankUsage.TotalTokens,
				truncate(br.Answer, 80),
				br.RerankError,
			)
		}
	}
	result.Summary = buildLongMemEvalSummary(result.Cases)
	if err := writeLongMemEvalResults(outPath, result); err != nil {
		return err
	}
	printLongMemEvalSummary(result)
	log.Printf("LongMemEval reranked results written to %s", outPath)
	return nil
}

func validateLongMemEvalRerankSource(result *runResult) (int, error) {
	if result == nil {
		return 0, errors.New("rerank results are nil")
	}
	topK, ok := longMemEvalMetadataInt(result.Metadata["top_k"])
	if !ok || topK <= 0 {
		return 0, errors.New("rerank source has invalid top_k")
	}
	hasBackend := false
	for _, cr := range result.Cases {
		if cr != nil && len(cr.BackendResults) > 0 {
			hasBackend = true
			break
		}
	}
	if !hasBackend {
		return 0, errors.New("rerank source has no backend results")
	}
	return topK, nil
}
