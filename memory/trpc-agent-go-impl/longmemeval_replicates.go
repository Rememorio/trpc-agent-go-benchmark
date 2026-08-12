//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

const (
	lmeReplicateManifestSchemaVersion   = 2
	lmeReplicateComparisonSchemaVersion = 6
	lmeReplicateKindPrimary             = "primary"
	lmeReplicateKindIndependentReanswer = "independent-reanswer"
	lmeReplicateMemoryCachesIndependent = "independent"
	lmeReplicateMemoryCachesShared      = "shared"
	lmeReplicateScopeThreeArmPromotion  = "three-arm-promotion"
	lmeReplicateScopePairwise           = "pairwise-nonregression"

	lmeReplicateArmPGVectorMain      = "pgvector_main"
	lmeReplicateArmMem0OSS           = "mem0_oss"
	lmeReplicateArmPGVectorCandidate = "pgvector_candidate"
)

type lmeReplicateComparisonManifest struct {
	SchemaVersion           int                          `json:"schema_version"`
	MemoryResponseCacheMode string                       `json:"memory_response_cache_mode,omitempty"`
	ComparisonScope         string                       `json:"comparison_scope,omitempty"`
	Replicates              []lmeReplicateComparisonPair `json:"replicates"`
	Gate                    lmeReplicatePromotionGate    `json:"gate"`
}

type lmeReplicateComparisonPair struct {
	Name                       string   `json:"name"`
	Kind                       string   `json:"kind"`
	BaselineResults            string   `json:"baseline_results"`
	CandidateResults           string   `json:"candidate_results"`
	AnswerCacheTimelineResults []string `json:"answer_cache_timeline_results,omitempty"`
	JudgeCacheTimelineResults  []string `json:"judge_cache_timeline_results,omitempty"`
}

type lmeReplicatePromotionGate struct {
	ExpectedCases                      int     `json:"expected_cases"`
	JudgeRuns                          int     `json:"judge_runs"`
	PerTypeMaxDeficit                  int     `json:"per_type_max_deficit"`
	MemoryLLMTokenRatioMaximum         float64 `json:"memory_llm_token_ratio_maximum"`
	MemoryLLMUncachedTokenRatioMaximum float64 `json:"memory_llm_uncached_token_ratio_maximum,omitempty"`
	MemoryEmbeddingRequestRatioMaximum float64 `json:"memory_embedding_request_ratio_maximum"`
	MemoryEmbeddingTokenRatioMaximum   float64 `json:"memory_embedding_token_ratio_maximum,omitempty"`
	FinalMemoryCountRatioMaximum       float64 `json:"final_memory_count_ratio_maximum"`
	MemoryIngestDurationRatioMaximum   float64 `json:"memory_ingest_duration_ratio_maximum,omitempty"`
	MemorySearchDurationRatioMaximum   float64 `json:"memory_search_duration_ratio_maximum,omitempty"`
	MemorySearchMinimumAllowanceMs     int64   `json:"memory_search_minimum_allowance_ms,omitempty"`
	MemorySearchP95MaximumMs           int64   `json:"memory_search_p95_maximum_ms,omitempty"`
}

type lmeLoadedReplicateComparisonPair struct {
	Spec            lmeReplicateComparisonPair
	Baseline        *runResult
	Candidate       *runResult
	BaselineSHA256  string
	CandidateSHA256 string
	AnswerLedgerID  string
	JudgeLedgerID   string
}

type lmeReplicateComparison struct {
	SchemaVersion           int                         `json:"schema_version"`
	CreatedAt               string                      `json:"created_at"`
	ManifestSHA256          string                      `json:"manifest_sha256"`
	MemoryResponseCacheMode string                      `json:"memory_response_cache_mode"`
	ComparisonScope         string                      `json:"comparison_scope"`
	ReplicateCount          int                         `json:"replicate_count"`
	Inputs                  []lmeReplicateInputAudit    `json:"inputs"`
	Arms                    map[string]*lmeReplicateArm `json:"arms"`
	Cases                   []lmeReplicateCase          `json:"cases"`
	Pairwise                []lmeReplicatePairwise      `json:"pairwise"`
	Gate                    lmeReplicateGateResult      `json:"gate"`
}

type lmeReplicateInputAudit struct {
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	BaselineSHA256  string `json:"baseline_results_sha256"`
	CandidateSHA256 string `json:"candidate_results_sha256"`
	AnswerLedgerID  string `json:"answer_cache_ledger_id"`
	JudgeLedgerID   string `json:"judge_cache_ledger_id"`
}

type lmeReplicateArm struct {
	Name                       string                              `json:"name"`
	Backend                    string                              `json:"backend"`
	SourceDigest               string                              `json:"source_digest"`
	Cases                      int                                 `json:"cases"`
	PrimaryCorrect             int                                 `json:"primary_correct"`
	MajorityCorrect            int                                 `json:"majority_correct"`
	CorrectReplicates          int                                 `json:"correct_replicates"`
	TotalAnswerReplicates      int                                 `json:"total_answer_replicates"`
	UnstableCases              int                                 `json:"unstable_cases"`
	BackendErrors              int                                 `json:"backend_errors"`
	AnswerErrors               int                                 `json:"answer_errors"`
	JudgeErrors                int                                 `json:"judge_errors"`
	ProviderUsageReportedCases int                                 `json:"provider_usage_reported_cases"`
	MemoryTokenUsage           lmeTokenUsage                       `json:"memory_token_usage"`
	MemoryTokenUsageBasis      string                              `json:"memory_token_usage_basis"`
	MemoryLogicalTokenUsage    lmeTokenUsage                       `json:"memory_logical_token_usage"`
	MemoryLogicalUsageComplete bool                                `json:"memory_logical_usage_complete"`
	MemoryLogicalUsageMissing  int                                 `json:"memory_logical_usage_missing_calls"`
	AnswerLogicalTokenUsage    lmeTokenUsage                       `json:"answer_logical_token_usage"`
	JudgeLogicalTokenUsage     lmeTokenUsage                       `json:"judge_logical_token_usage"`
	ModelCacheInitialEntries   int                                 `json:"model_response_cache_initial_entries"`
	ModelCacheInitialKnown     bool                                `json:"model_response_cache_initial_entries_known"`
	MemoryModelRequests        int                                 `json:"memory_model_requests"`
	MemoryModelCacheHits       int                                 `json:"memory_model_response_cache_hits"`
	MemoryEmbeddingUsage       lmeEmbeddingUsage                   `json:"memory_embedding_usage"`
	IngestedPairs              int                                 `json:"ingested_pairs"`
	FinalMemories              int                                 `json:"final_memories"`
	ExtractionDiagnostics      lmeReplicateExtractionDiagnostics   `json:"extraction_diagnostics"`
	FinalMemoriesByAttribution lmeReplicateAttributionCounts       `json:"final_memories_by_attribution"`
	IngestDurationMs           int64                               `json:"ingest_duration_ms"`
	SearchDurationMs           int64                               `json:"search_duration_ms"`
	ByType                     map[string]*lmeReplicateTypeSummary `json:"by_type"`
}

type lmeReplicateExtractionDiagnostics struct {
	TracedPairs                       int                           `json:"traced_pairs"`
	OperationPairs                    int                           `json:"operation_pairs"`
	ZeroOperationPairs                int                           `json:"zero_operation_pairs"`
	Operations                        int                           `json:"operations"`
	OperationsByStage                 map[string]int                `json:"operations_by_stage"`
	OperationsByType                  map[string]int                `json:"operations_by_type"`
	PostPolicyObservedPairs           int                           `json:"post_policy_observed_pairs"`
	PostPolicyOperationPairs          int                           `json:"post_policy_operation_pairs"`
	PostPolicyZeroOperationPairs      int                           `json:"post_policy_zero_operation_pairs"`
	PostPolicyOperations              int                           `json:"post_policy_operations"`
	PostPolicyOperationsByStage       map[string]int                `json:"post_policy_operations_by_stage"`
	PostPolicyOperationsByType        map[string]int                `json:"post_policy_operations_by_type"`
	MultiCallPairs                    int                           `json:"multi_call_pairs"`
	AdditionalModelRequests           int                           `json:"additional_model_requests"`
	PersistenceTracedOperations       int                           `json:"persistence_traced_operations"`
	PersistenceByStatus               map[string]int                `json:"persistence_by_status"`
	PersistenceByEffect               map[string]int                `json:"persistence_by_effect"`
	PostPolicyPersistenceTraced       int                           `json:"post_policy_persistence_traced_operations"`
	PostPolicyPersistenceByStatus     map[string]int                `json:"post_policy_persistence_by_status"`
	PostPolicyPersistenceByEffect     map[string]int                `json:"post_policy_persistence_by_effect"`
	PersistedNewMemoriesByAttribution lmeReplicateAttributionCounts `json:"persisted_new_memories_by_attribution"`
}

type lmeReplicateAttributionCounts struct {
	User      int `json:"user"`
	Assistant int `json:"assistant"`
	Unknown   int `json:"unknown"`
}

type lmeReplicateTypeSummary struct {
	Cases                          int               `json:"cases"`
	PrimaryCorrect                 int               `json:"primary_correct"`
	MajorityCorrect                int               `json:"majority_correct"`
	CorrectReplicates              int               `json:"correct_replicates"`
	TotalAnswerReplicates          int               `json:"total_answer_replicates"`
	MemoryLogicalTokenUsage        lmeTokenUsage     `json:"memory_logical_token_usage"`
	MemoryLogicalUsageMissingCalls int               `json:"memory_logical_usage_missing_calls"`
	MemoryEmbeddingUsage           lmeEmbeddingUsage `json:"memory_embedding_usage"`
	AnswerLogicalTokenUsage        lmeTokenUsage     `json:"answer_logical_token_usage"`
	JudgeLogicalTokenUsage         lmeTokenUsage     `json:"judge_logical_token_usage"`
	FinalMemories                  int               `json:"final_memories"`
	IngestDurationMs               int64             `json:"ingest_duration_ms"`
	SearchDurationMs               int64             `json:"search_duration_ms"`
}

type lmeReplicateCase struct {
	QuestionID   string                                `json:"question_id"`
	QuestionType string                                `json:"question_type"`
	Arms         map[string]lmeReplicateCaseArmSummary `json:"arms"`
}

type lmeReplicateCaseArmSummary struct {
	PrimaryCorrect                 bool              `json:"primary_correct"`
	MajorityCorrect                bool              `json:"majority_correct"`
	CorrectReplicates              int               `json:"correct_replicates"`
	IngestedPairs                  int               `json:"ingested_pairs"`
	Stages                         []string          `json:"stages"`
	MemoryLogicalTokenUsage        lmeTokenUsage     `json:"memory_logical_token_usage"`
	MemoryLogicalUsageMissingCalls int               `json:"memory_logical_usage_missing_calls"`
	MemoryEmbeddingUsage           lmeEmbeddingUsage `json:"memory_embedding_usage"`
	AnswerLogicalTokenUsage        lmeTokenUsage     `json:"answer_logical_token_usage"`
	JudgeLogicalTokenUsage         lmeTokenUsage     `json:"judge_logical_token_usage"`
	FinalMemories                  int               `json:"final_memories"`
	IngestDurationMs               int64             `json:"ingest_duration_ms"`
	SearchDurationMs               int64             `json:"search_duration_ms"`
}

type lmeReplicatePairwise struct {
	Name               string  `json:"name"`
	InferenceUnit      string  `json:"inference_unit"`
	CandidateArm       string  `json:"candidate_arm"`
	BaselineArm        string  `json:"baseline_arm"`
	Cases              int     `json:"cases"`
	CandidateCorrect   int     `json:"candidate_correct"`
	BaselineCorrect    int     `json:"baseline_correct"`
	CandidateWins      int     `json:"candidate_wins"`
	BaselineWins       int     `json:"baseline_wins"`
	Ties               int     `json:"ties"`
	DiscordantCases    int     `json:"discordant_cases"`
	AccuracyDelta      float64 `json:"accuracy_delta"`
	ExactMcNemarPValue float64 `json:"exact_mcnemar_p_value"`
}

type lmeReplicateGateResult struct {
	Passed          bool                    `json:"passed"`
	IntegrityPassed bool                    `json:"integrity_passed"`
	OutcomePassed   bool                    `json:"outcome_passed"`
	CostPassed      bool                    `json:"cost_passed"`
	Checks          []lmeReplicateGateCheck `json:"checks"`
}

type lmeReplicateGateDimension string

const (
	lmeReplicateGateDimensionIntegrity lmeReplicateGateDimension = "integrity"
	lmeReplicateGateDimensionOutcome   lmeReplicateGateDimension = "outcome"
	lmeReplicateGateDimensionCost      lmeReplicateGateDimension = "cost"
)

type lmeReplicateGateCheck struct {
	Dimension   lmeReplicateGateDimension `json:"dimension"`
	Name        string                    `json:"name"`
	Passed      bool                      `json:"passed"`
	Actual      string                    `json:"actual"`
	Requirement string                    `json:"requirement"`
}

type lmeReplicateStableCase struct {
	QuestionID       string        `json:"question_id"`
	QuestionType     string        `json:"question_type"`
	Question         string        `json:"question"`
	QuestionDate     string        `json:"question_date,omitempty"`
	Answer           string        `json:"answer"`
	AnswerSessionIDs []string      `json:"answer_session_ids,omitempty"`
	NumSessions      int           `json:"num_sessions"`
	Backend          backendResult `json:"backend"`
}

type lmeReplicateStableSource struct {
	Metadata map[string]any           `json:"metadata"`
	Cases    []lmeReplicateStableCase `json:"cases"`
}

func compareLongMemEvalReplicates(manifestPath, outputDir string) error {
	manifest, manifestDigest, pairs, err := loadLongMemEvalReplicateComparison(manifestPath)
	if err != nil {
		return err
	}
	comparison, err := aggregateLongMemEvalReplicates(manifestDigest, manifest, pairs)
	if err != nil {
		return err
	}
	if outputDir == "" {
		outputDir = filepath.Dir(manifestPath)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create replicate comparison output dir: %w", err)
	}
	data, err := json.MarshalIndent(comparison, "", "  ")
	if err != nil {
		return fmt.Errorf("encode LongMemEval replicate comparison: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(outputDir, "replicate_comparison.json"), data, 0644); err != nil {
		return fmt.Errorf("write replicate_comparison.json: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(outputDir, "replicate_comparison.tsv"),
		[]byte(formatLongMemEvalReplicateComparisonTSV(comparison)),
		0644,
	); err != nil {
		return fmt.Errorf("write replicate_comparison.tsv: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(outputDir, "replicate_bad_cases.tsv"),
		[]byte(formatLongMemEvalReplicateBadCasesTSV(comparison)),
		0644,
	); err != nil {
		return fmt.Errorf("write replicate_bad_cases.tsv: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(outputDir, "replicate_comparison.md"),
		[]byte(formatLongMemEvalReplicateComparisonMarkdown(comparison)),
		0644,
	); err != nil {
		return fmt.Errorf("write replicate_comparison.md: %w", err)
	}
	fmt.Printf("LongMemEval replicate comparison written to %s (pass=%v)\n",
		outputDir, comparison.Gate.Passed)
	return nil
}

func loadLongMemEvalReplicateComparison(
	manifestPath string,
) (lmeReplicateComparisonManifest, string, []lmeLoadedReplicateComparisonPair, error) {
	file, err := os.Open(manifestPath)
	if err != nil {
		return lmeReplicateComparisonManifest{}, "", nil,
			fmt.Errorf("open LongMemEval replicate manifest: %w", err)
	}
	decoder := json.NewDecoder(bufio.NewReader(file))
	decoder.DisallowUnknownFields()
	var manifest lmeReplicateComparisonManifest
	if err := decoder.Decode(&manifest); err != nil {
		file.Close()
		return manifest, "", nil, fmt.Errorf("decode LongMemEval replicate manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		file.Close()
		if err == nil {
			return manifest, "", nil, errors.New("decode LongMemEval replicate manifest: multiple JSON values")
		}
		return manifest, "", nil, fmt.Errorf("decode trailing LongMemEval replicate manifest data: %w", err)
	}
	if err := file.Close(); err != nil {
		return manifest, "", nil, fmt.Errorf("close LongMemEval replicate manifest: %w", err)
	}
	manifestDigest, err := longMemEvalFileSHA256(manifestPath)
	if err != nil {
		return manifest, "", nil, fmt.Errorf("hash LongMemEval replicate manifest: %w", err)
	}
	if err := validateLongMemEvalReplicateManifest(manifest); err != nil {
		return manifest, manifestDigest, nil, err
	}

	baseDir := filepath.Dir(manifestPath)
	pairs := make([]lmeLoadedReplicateComparisonPair, 0, len(manifest.Replicates))
	answerLedgers := make(map[string]string, len(manifest.Replicates))
	judgeLedgers := make(map[string]string, len(manifest.Replicates))
	for _, spec := range manifest.Replicates {
		baselinePath := resolveLongMemEvalReplicatePath(baseDir, spec.BaselineResults)
		candidatePath := resolveLongMemEvalReplicatePath(baseDir, spec.CandidateResults)
		baseline, err := loadLongMemEvalResults(baselinePath)
		if err != nil {
			return manifest, manifestDigest, nil, fmt.Errorf("load replicate %q baseline: %w", spec.Name, err)
		}
		candidate, err := loadLongMemEvalResults(candidatePath)
		if err != nil {
			return manifest, manifestDigest, nil, fmt.Errorf("load replicate %q candidate: %w", spec.Name, err)
		}
		if err := validateLongMemEvalReplicateComparisonMode(
			baseline, candidate, manifest.MemoryResponseCacheMode,
		); err != nil {
			return manifest, manifestDigest, nil, fmt.Errorf("validate replicate %q comparison: %w", spec.Name, err)
		}
		if err := validateLongMemEvalReplicateKind(spec, baseline, candidate); err != nil {
			return manifest, manifestDigest, nil, err
		}
		if len(spec.AnswerCacheTimelineResults) > 0 {
			if err := validateLongMemEvalReplicateRegisteredCacheTimelines(
				baseDir, spec,
			); err != nil {
				return manifest, manifestDigest, nil, err
			}
		} else {
			if err := validateLongMemEvalReplicateFreshCaches(
				spec.Name, baseline, candidate,
			); err != nil {
				return manifest, manifestDigest, nil, err
			}
		}
		if err := validateLongMemEvalReplicateJudges(spec.Name, manifest.Gate.JudgeRuns, baseline, candidate); err != nil {
			return manifest, manifestDigest, nil, err
		}
		if err := validateLongMemEvalReplicateLogicalUsage(
			spec.Name, baseline, candidate,
		); err != nil {
			return manifest, manifestDigest, nil, err
		}
		answerLedger, ok := lmeMetadataString(baseline.Metadata, "answer_cache_ledger_id")
		if !ok || strings.TrimSpace(answerLedger) == "" {
			return manifest, manifestDigest, nil, fmt.Errorf("replicate %q is missing answer_cache_ledger_id", spec.Name)
		}
		if previous, exists := answerLedgers[answerLedger]; exists {
			return manifest, manifestDigest, nil, fmt.Errorf(
				"replicates %q and %q share answer cache ledger %q",
				previous, spec.Name, answerLedger,
			)
		}
		answerLedgers[answerLedger] = spec.Name
		judgeLedger, ok := lmeMetadataString(baseline.Metadata, "judge_cache_ledger_id")
		if !ok || strings.TrimSpace(judgeLedger) == "" {
			return manifest, manifestDigest, nil, fmt.Errorf("replicate %q is missing judge_cache_ledger_id", spec.Name)
		}
		if previous, exists := judgeLedgers[judgeLedger]; exists {
			return manifest, manifestDigest, nil, fmt.Errorf(
				"replicates %q and %q share judge cache ledger %q",
				previous, spec.Name, judgeLedger,
			)
		}
		judgeLedgers[judgeLedger] = spec.Name
		baselineDigest, err := longMemEvalFileSHA256(baselinePath)
		if err != nil {
			return manifest, manifestDigest, nil, fmt.Errorf("hash replicate %q baseline: %w", spec.Name, err)
		}
		candidateDigest, err := longMemEvalFileSHA256(candidatePath)
		if err != nil {
			return manifest, manifestDigest, nil, fmt.Errorf("hash replicate %q candidate: %w", spec.Name, err)
		}
		pairs = append(pairs, lmeLoadedReplicateComparisonPair{
			Spec: spec, Baseline: baseline, Candidate: candidate,
			BaselineSHA256: baselineDigest, CandidateSHA256: candidateDigest,
			AnswerLedgerID: answerLedger, JudgeLedgerID: judgeLedger,
		})
	}
	return manifest, manifestDigest, pairs, nil
}

func validateLongMemEvalReplicateComparison(
	baseline, candidate *runResult,
) error {
	return validateLongMemEvalReplicateComparisonMode(
		baseline, candidate, lmeReplicateMemoryCachesIndependent,
	)
}

func validateLongMemEvalReplicateComparisonMode(
	baseline, candidate *runResult,
	cacheMode string,
) error {
	comparisonMode := lmeMemoryResponseCachesIndependent
	if cacheMode == lmeReplicateMemoryCachesShared {
		comparisonMode = lmeMemoryResponseCachesShared
	} else {
		if cacheMode != "" &&
			cacheMode != lmeReplicateMemoryCachesIndependent {
			return fmt.Errorf(
				"unsupported LongMemEval replicate memory response "+
					"cache mode %q",
				cacheMode,
			)
		}
		if err := validateLongMemEvalIndependentMemoryResponseCaches(
			baseline, candidate,
		); err != nil {
			return err
		}
	}
	return validateLongMemEvalComparisonWithMemoryResponseCaches(
		baseline, candidate, comparisonMode,
	)
}

func validateLongMemEvalIndependentMemoryResponseCaches(
	baseline, candidate *runResult,
) error {
	if baseline == nil || candidate == nil {
		return errors.New("LongMemEval replicate comparison results must not be nil")
	}
	for _, prefix := range []string{
		"model_response_cache",
		"embedding_response_cache",
	} {
		ledgerKey := prefix + "_ledger_id"
		baselineLedger, baselineOK := lmeMetadataString(
			baseline.Metadata, ledgerKey,
		)
		candidateLedger, candidateOK := lmeMetadataString(
			candidate.Metadata, ledgerKey,
		)
		if !baselineOK || strings.TrimSpace(baselineLedger) == "" ||
			!candidateOK || strings.TrimSpace(candidateLedger) == "" {
			return fmt.Errorf(
				"strict LongMemEval replicate comparison requires "+
					"metadata %q in both results",
				ledgerKey,
			)
		}
		if baselineLedger == candidateLedger {
			return fmt.Errorf(
				"strict LongMemEval replicate comparison requires "+
					"independent %s ledgers, both use %q",
				strings.ReplaceAll(prefix, "_", " "), baselineLedger,
			)
		}
		initialKey := prefix + "_initial_entries"
		for _, result := range []struct {
			name     string
			metadata map[string]any
		}{
			{name: "baseline", metadata: baseline.Metadata},
			{name: "candidate", metadata: candidate.Metadata},
		} {
			initial, ok := longMemEvalMetadataInt(
				result.metadata[initialKey],
			)
			if !ok {
				return fmt.Errorf(
					"strict LongMemEval replicate comparison "+
						"metadata %q is not an integer in %s",
					initialKey, result.name,
				)
			}
			if initial != 0 {
				return fmt.Errorf(
					"strict LongMemEval replicate comparison "+
						"requires %s %s to be 0, got %d",
					result.name, initialKey, initial,
				)
			}
		}
	}
	return nil
}

func validateLongMemEvalReplicateManifest(manifest lmeReplicateComparisonManifest) error {
	if manifest.SchemaVersion != lmeReplicateManifestSchemaVersion {
		return fmt.Errorf("LongMemEval replicate schema version is %d, want %d",
			manifest.SchemaVersion, lmeReplicateManifestSchemaVersion)
	}
	if len(manifest.Replicates) < 3 || len(manifest.Replicates)%2 == 0 {
		return fmt.Errorf("LongMemEval replicate manifest requires an odd count of at least 3, got %d", len(manifest.Replicates))
	}
	if manifest.MemoryResponseCacheMode != "" &&
		manifest.MemoryResponseCacheMode !=
			lmeReplicateMemoryCachesIndependent &&
		manifest.MemoryResponseCacheMode != lmeReplicateMemoryCachesShared {
		return fmt.Errorf(
			"LongMemEval replicate memory response cache mode is %q, "+
				"want %q or %q",
			manifest.MemoryResponseCacheMode,
			lmeReplicateMemoryCachesIndependent,
			lmeReplicateMemoryCachesShared,
		)
	}
	if manifest.ComparisonScope != "" &&
		manifest.ComparisonScope != lmeReplicateScopeThreeArmPromotion &&
		manifest.ComparisonScope != lmeReplicateScopePairwise {
		return fmt.Errorf(
			"LongMemEval replicate comparison scope is %q, want %q or %q",
			manifest.ComparisonScope,
			lmeReplicateScopeThreeArmPromotion,
			lmeReplicateScopePairwise,
		)
	}
	seen := make(map[string]bool, len(manifest.Replicates))
	for index, replicate := range manifest.Replicates {
		if strings.TrimSpace(replicate.Name) == "" || seen[replicate.Name] {
			return fmt.Errorf("LongMemEval replicate name %q is empty or duplicated", replicate.Name)
		}
		seen[replicate.Name] = true
		if strings.TrimSpace(replicate.BaselineResults) == "" || strings.TrimSpace(replicate.CandidateResults) == "" {
			return fmt.Errorf("LongMemEval replicate %q has an empty results path", replicate.Name)
		}
		answerTimeline := len(replicate.AnswerCacheTimelineResults)
		judgeTimeline := len(replicate.JudgeCacheTimelineResults)
		if (answerTimeline == 0) != (judgeTimeline == 0) {
			return fmt.Errorf(
				"LongMemEval replicate %q must register both answer and "+
					"judge cache timelines",
				replicate.Name,
			)
		}
		if answerTimeline > 0 {
			if answerTimeline < 2 || judgeTimeline < 2 {
				return fmt.Errorf(
					"LongMemEval replicate %q cache timelines must "+
						"contain at least two results",
					replicate.Name,
				)
			}
			for label, paths := range map[string][]string{
				"answer": replicate.AnswerCacheTimelineResults,
				"judge":  replicate.JudgeCacheTimelineResults,
			} {
				seenPaths := make(map[string]bool, len(paths))
				for _, path := range paths {
					path = filepath.Clean(strings.TrimSpace(path))
					if path == "." || seenPaths[path] {
						return fmt.Errorf(
							"LongMemEval replicate %q %s cache "+
								"timeline path %q is empty or duplicated",
							replicate.Name, label, path,
						)
					}
					seenPaths[path] = true
				}
			}
		}
		if index == 0 {
			if replicate.Kind != lmeReplicateKindPrimary &&
				replicate.Kind != lmeReplicateKindIndependentReanswer {
				return fmt.Errorf(
					"LongMemEval first replicate %q kind is %q, want %q or %q",
					replicate.Name,
					replicate.Kind,
					lmeReplicateKindPrimary,
					lmeReplicateKindIndependentReanswer,
				)
			}
			continue
		}
		if replicate.Kind != lmeReplicateKindIndependentReanswer {
			return fmt.Errorf(
				"LongMemEval replicate %q kind is %q, want %q",
				replicate.Name,
				replicate.Kind,
				lmeReplicateKindIndependentReanswer,
			)
		}
	}
	gate := manifest.Gate
	if gate.ExpectedCases <= 0 || gate.JudgeRuns <= 1 || gate.JudgeRuns%2 == 0 || gate.PerTypeMaxDeficit < 0 ||
		gate.MemoryLLMTokenRatioMaximum <= 0 ||
		gate.MemoryLLMUncachedTokenRatioMaximum < 0 ||
		gate.MemoryEmbeddingRequestRatioMaximum <= 0 ||
		gate.MemoryEmbeddingTokenRatioMaximum < 0 ||
		gate.FinalMemoryCountRatioMaximum <= 0 ||
		gate.MemoryIngestDurationRatioMaximum < 0 ||
		gate.MemorySearchDurationRatioMaximum < 0 ||
		gate.MemorySearchMinimumAllowanceMs < 0 ||
		gate.MemorySearchP95MaximumMs < 0 ||
		(gate.MemorySearchMinimumAllowanceMs > 0 &&
			gate.MemorySearchDurationRatioMaximum <= 0) {
		return fmt.Errorf("LongMemEval replicate manifest has invalid promotion gate: %+v", gate)
	}
	if manifest.MemoryResponseCacheMode == lmeReplicateMemoryCachesShared &&
		longMemEvalReplicateLatencyGateEnabled(gate) {
		return errors.New(
			"LongMemEval shared memory response caches cannot gate " +
				"ingest or search latency; use independent initially empty " +
				"ledgers for latency comparison",
		)
	}
	return nil
}

func resolveLongMemEvalReplicatePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}

func validateLongMemEvalReplicateKind(
	spec lmeReplicateComparisonPair,
	baseline, candidate *runResult,
) error {
	for label, result := range map[string]*runResult{"baseline": baseline, "candidate": candidate} {
		reanswerModel, hasReanswerModel := lmeMetadataString(result.Metadata, "reanswer_model")
		hasReanswerMetadata := false
		for key := range result.Metadata {
			if strings.HasPrefix(key, "reanswer") {
				hasReanswerMetadata = true
				break
			}
		}
		reuse, hasReuse := result.Metadata["reanswer_reuse_source_answers"].(bool)
		switch spec.Kind {
		case lmeReplicateKindPrimary:
			if hasReanswerMetadata || hasReuse {
				return fmt.Errorf("replicate %q %s is primary but records re-answer metadata", spec.Name, label)
			}
		case lmeReplicateKindIndependentReanswer:
			if !hasReanswerModel || reanswerModel == "" || !hasReuse || reuse {
				return fmt.Errorf("replicate %q %s must record reanswer_reuse_source_answers=false", spec.Name, label)
			}
		}
	}
	return nil
}

func validateLongMemEvalReplicateFreshCaches(
	replicate string,
	baseline, candidate *runResult,
) error {
	if baseline == nil || candidate == nil {
		return fmt.Errorf("replicate %q comparison results must not be nil", replicate)
	}
	type cachePosition struct {
		name    string
		initial int
		final   int
	}
	for _, prefix := range []string{"answer_cache", "judge_cache"} {
		positions := make([]cachePosition, 0, 2)
		for _, result := range []struct {
			name   string
			result *runResult
		}{
			{name: "baseline", result: baseline},
			{name: "candidate", result: candidate},
		} {
			initialKey := prefix + "_initial_entries"
			initial, ok := longMemEvalMetadataInt(
				result.result.Metadata[initialKey],
			)
			if !ok {
				return fmt.Errorf(
					"replicate %q %s metadata %q is not an integer",
					replicate, result.name, initialKey,
				)
			}
			finalKey := prefix + "_final_entries"
			final, ok := longMemEvalMetadataInt(
				result.result.Metadata[finalKey],
			)
			if !ok {
				return fmt.Errorf(
					"replicate %q %s metadata %q is not an integer",
					replicate, result.name, finalKey,
				)
			}
			if initial < 0 || final < initial {
				return fmt.Errorf(
					"replicate %q %s %s cache entries are not monotonic: "+
						"initial=%d final=%d",
					replicate, result.name, prefix, initial, final,
				)
			}
			positions = append(positions, cachePosition{
				name: result.name, initial: initial, final: final,
			})
		}
		first, second := positions[0], positions[1]
		baselineFirst := first.initial == 0 &&
			first.final == second.initial
		candidateFirst := second.initial == 0 &&
			second.final == first.initial
		if !baselineFirst && !candidateFirst {
			return fmt.Errorf(
				"replicate %q %s cache does not form a fresh contiguous "+
					"timeline: %s initial=%d final=%d, "+
					"%s initial=%d final=%d",
				replicate, prefix,
				first.name, first.initial, first.final,
				second.name, second.initial, second.final,
			)
		}
	}
	return nil
}

func validateLongMemEvalReplicateRegisteredCacheTimelines(
	baseDir string,
	spec lmeReplicateComparisonPair,
) error {
	for _, timeline := range []struct {
		prefix string
		paths  []string
	}{
		{
			prefix: "answer_cache",
			paths:  spec.AnswerCacheTimelineResults,
		},
		{
			prefix: "judge_cache",
			paths:  spec.JudgeCacheTimelineResults,
		},
	} {
		if err := validateLongMemEvalReplicateCacheTimeline(
			baseDir,
			spec.Name,
			timeline.prefix,
			timeline.paths,
			[]string{spec.BaselineResults, spec.CandidateResults},
		); err != nil {
			return err
		}
	}
	return nil
}

func validateLongMemEvalReplicateCacheTimeline(
	baseDir, replicate, prefix string,
	paths, requiredPaths []string,
) error {
	required := make(map[string]bool, len(requiredPaths))
	requiredAnswerDigests := make(map[string][]string, len(requiredPaths))
	for _, path := range requiredPaths {
		resolved := resolveLongMemEvalReplicatePath(baseDir, path)
		required[resolved] = false
		if prefix != "answer_cache" {
			continue
		}
		result, err := loadLongMemEvalResults(resolved)
		if err != nil {
			return fmt.Errorf(
				"load replicate %q comparison result %q: %w",
				replicate, resolved, err,
			)
		}
		digest, err := longMemEvalReplicateAnswerTimelineDigest(result)
		if err != nil {
			return fmt.Errorf(
				"hash replicate %q comparison result %q: %w",
				replicate, resolved, err,
			)
		}
		requiredAnswerDigests[digest] = append(
			requiredAnswerDigests[digest], resolved,
		)
	}
	ledgerKey := prefix + "_ledger_id"
	initialKey := prefix + "_initial_entries"
	finalKey := prefix + "_final_entries"
	ledgerID := ""
	previousFinal := 0
	for index, path := range paths {
		resolved := resolveLongMemEvalReplicatePath(baseDir, path)
		result, err := loadLongMemEvalResults(resolved)
		if err != nil {
			return fmt.Errorf(
				"load replicate %q %s timeline item %d: %w",
				replicate, prefix, index, err,
			)
		}
		if _, ok := required[resolved]; ok {
			required[resolved] = true
		}
		if prefix == "answer_cache" {
			digest, err := longMemEvalReplicateAnswerTimelineDigest(result)
			if err != nil {
				return fmt.Errorf(
					"hash replicate %q %s timeline item %d: %w",
					replicate, prefix, index, err,
				)
			}
			for _, requiredPath := range requiredAnswerDigests[digest] {
				required[requiredPath] = true
			}
		}
		currentLedger, ok := lmeMetadataString(
			result.Metadata, ledgerKey,
		)
		if !ok || strings.TrimSpace(currentLedger) == "" {
			return fmt.Errorf(
				"replicate %q %s timeline item %d is missing %q",
				replicate, prefix, index, ledgerKey,
			)
		}
		if ledgerID == "" {
			ledgerID = currentLedger
		} else if currentLedger != ledgerID {
			return fmt.Errorf(
				"replicate %q %s timeline changes ledger from %q to %q",
				replicate, prefix, ledgerID, currentLedger,
			)
		}
		initial, initialOK := longMemEvalMetadataInt(
			result.Metadata[initialKey],
		)
		final, finalOK := longMemEvalMetadataInt(
			result.Metadata[finalKey],
		)
		if !initialOK || !finalOK {
			return fmt.Errorf(
				"replicate %q %s timeline item %d has invalid entry "+
					"metadata: initial=%v final=%v",
				replicate, prefix, index,
				result.Metadata[initialKey], result.Metadata[finalKey],
			)
		}
		if initial < 0 || final < initial {
			return fmt.Errorf(
				"replicate %q %s timeline item %d is not monotonic: "+
					"initial=%d final=%d",
				replicate, prefix, index, initial, final,
			)
		}
		if (index == 0 && initial != 0) ||
			(index > 0 && initial != previousFinal) {
			return fmt.Errorf(
				"replicate %q %s timeline is not fresh and contiguous "+
					"at item %d: initial=%d previous_final=%d",
				replicate, prefix, index, initial, previousFinal,
			)
		}
		previousFinal = final
	}
	for path, present := range required {
		if !present {
			return fmt.Errorf(
				"replicate %q %s timeline does not contain comparison "+
					"result %q",
				replicate, prefix, path,
			)
		}
	}
	return nil
}

func longMemEvalReplicateAnswerTimelineDigest(result *runResult) (string, error) {
	if result == nil {
		return "", errors.New("results are nil")
	}
	stable := runResult{Metadata: make(map[string]any, len(result.Metadata))}
	for key, value := range result.Metadata {
		if strings.HasPrefix(key, "judge_") || key == "judged_at" {
			continue
		}
		stable.Metadata[key] = value
	}
	stable.Cases = make([]*caseResult, 0, len(result.Cases))
	for _, cr := range result.Cases {
		if cr == nil {
			stable.Cases = append(stable.Cases, nil)
			continue
		}
		copyCR := *cr
		copyCR.BackendResults = make(map[string]*backendResult, len(cr.BackendResults))
		for backend, br := range cr.BackendResults {
			if br == nil {
				copyCR.BackendResults[backend] = nil
				continue
			}
			copyBR := *br
			copyBR.Judge = nil
			copyBR.EvaluatedFailureStage = ""
			copyCR.BackendResults[backend] = &copyBR
		}
		stable.Cases = append(stable.Cases, &copyCR)
	}
	return longMemEvalJSONSHA256(stable)
}

func validateLongMemEvalReplicateJudges(
	replicate string,
	judgeRuns int,
	results ...*runResult,
) error {
	for _, result := range results {
		for _, cr := range result.Cases {
			if cr == nil {
				return fmt.Errorf("replicate %q contains a nil case", replicate)
			}
			for backend, br := range cr.BackendResults {
				if br == nil || br.Judge == nil || br.Judge.RequestedRuns != judgeRuns || br.Judge.ValidRuns != judgeRuns {
					return fmt.Errorf("replicate %q case %q backend %q does not have %d valid judge votes",
						replicate, cr.QuestionID, backend, judgeRuns)
				}
				if _, ok := longMemEvalJudgeCorrect(br); !ok {
					return fmt.Errorf("replicate %q case %q backend %q has an invalid judge consensus",
						replicate, cr.QuestionID, backend)
				}
			}
		}
	}
	return nil
}

func validateLongMemEvalReplicateLogicalUsage(
	replicate string,
	results ...*runResult,
) error {
	for _, result := range results {
		for _, key := range []string{
			"answer_cache_logical_usage_missing_hits",
			"judge_cache_logical_usage_missing_hits",
		} {
			missing, ok := longMemEvalMetadataInt(result.Metadata[key])
			if !ok || missing != 0 {
				return fmt.Errorf(
					"replicate %q metadata %q must be integer zero",
					replicate, key,
				)
			}
		}
		for _, cr := range result.Cases {
			if cr == nil {
				continue
			}
			for backend, br := range cr.BackendResults {
				if br == nil {
					continue
				}
				answerUsage, answerComplete :=
					longMemEvalAnswerAttemptLogicalUsage(
						br.AnswerAttempts,
					)
				if !answerComplete ||
					br.AnswerLogicalUsage == nil ||
					*br.AnswerLogicalUsage != answerUsage {
					return fmt.Errorf(
						"replicate %q case %q backend %q has "+
							"incomplete answer logical usage",
						replicate, cr.QuestionID, backend,
					)
				}
				judgeUsage, judgeComplete, err :=
					validateLongMemEvalJudgeAttemptUsage(
						br.Judge.Attempts,
					)
				if err != nil {
					return fmt.Errorf(
						"replicate %q case %q backend %q "+
							"judge logical usage: %w",
						replicate, cr.QuestionID, backend, err,
					)
				}
				if !judgeComplete ||
					!br.Judge.LogicalUsageComplete ||
					br.Judge.LogicalTokenUsage == nil ||
					*br.Judge.LogicalTokenUsage != judgeUsage {
					return fmt.Errorf(
						"replicate %q case %q backend %q has "+
							"incomplete judge logical usage",
						replicate, cr.QuestionID, backend,
					)
				}
			}
		}
	}
	return nil
}

func aggregateLongMemEvalReplicates(
	manifestDigest string,
	manifest lmeReplicateComparisonManifest,
	pairs []lmeLoadedReplicateComparisonPair,
) (*lmeReplicateComparison, error) {
	if len(pairs) != len(manifest.Replicates) {
		return nil, errors.New("LongMemEval loaded replicate count does not match manifest")
	}
	comparison := &lmeReplicateComparison{
		SchemaVersion:           lmeReplicateComparisonSchemaVersion,
		CreatedAt:               time.Now().UTC().Format(time.RFC3339),
		ManifestSHA256:          manifestDigest,
		MemoryResponseCacheMode: manifest.MemoryResponseCacheMode,
		ComparisonScope:         manifest.ComparisonScope,
		ReplicateCount:          len(pairs),
		Inputs:                  make([]lmeReplicateInputAudit, 0, len(pairs)),
		Arms:                    make(map[string]*lmeReplicateArm, 3),
	}
	if comparison.MemoryResponseCacheMode == "" {
		comparison.MemoryResponseCacheMode =
			lmeReplicateMemoryCachesIndependent
	}
	if comparison.ComparisonScope == "" {
		comparison.ComparisonScope = lmeReplicateScopeThreeArmPromotion
	}
	for _, pair := range pairs {
		comparison.Inputs = append(comparison.Inputs, lmeReplicateInputAudit{
			Name: pair.Spec.Name, Kind: pair.Spec.Kind,
			BaselineSHA256: pair.BaselineSHA256, CandidateSHA256: pair.CandidateSHA256,
			AnswerLedgerID: pair.AnswerLedgerID, JudgeLedgerID: pair.JudgeLedgerID,
		})
	}

	type armBinding struct {
		name      string
		backend   string
		candidate bool
	}
	bindings := []armBinding{{
		name: lmeReplicateArmPGVectorMain, backend: "pgvector",
	}}
	if comparison.ComparisonScope == lmeReplicateScopeThreeArmPromotion {
		bindings = append(bindings, armBinding{
			name: lmeReplicateArmMem0OSS, backend: "mem0",
		})
	}
	bindings = append(bindings, armBinding{
		name:    lmeReplicateArmPGVectorCandidate,
		backend: "pgvector", candidate: true,
	})
	caseMap := make(map[string]*lmeReplicateCase)
	caseOrder := make([]string, 0)
	for _, binding := range bindings {
		arm, cases, err := aggregateLongMemEvalReplicateArm(binding.name, binding.backend, binding.candidate, pairs)
		if err != nil {
			return nil, err
		}
		comparison.Arms[binding.name] = arm
		for _, item := range cases {
			row := caseMap[item.QuestionID]
			if row == nil {
				row = &lmeReplicateCase{
					QuestionID: item.QuestionID, QuestionType: item.QuestionType,
					Arms: make(map[string]lmeReplicateCaseArmSummary, 3),
				}
				caseMap[item.QuestionID] = row
				caseOrder = append(caseOrder, item.QuestionID)
			} else if row.QuestionType != item.QuestionType {
				return nil, fmt.Errorf("replicate question %q type drifted from %q to %q",
					item.QuestionID, row.QuestionType, item.QuestionType)
			}
			row.Arms[binding.name] = item.Arms[binding.name]
		}
	}
	sort.Strings(caseOrder)
	for _, id := range caseOrder {
		comparison.Cases = append(comparison.Cases, *caseMap[id])
	}
	baselineArms := []string{lmeReplicateArmPGVectorMain}
	if comparison.ComparisonScope == lmeReplicateScopeThreeArmPromotion {
		baselineArms = append(baselineArms, lmeReplicateArmMem0OSS)
	}
	for _, baselineArm := range baselineArms {
		pairwise, err := analyzeLongMemEvalPairwise(
			comparison.Cases,
			lmeReplicateArmPGVectorCandidate,
			baselineArm,
		)
		if err != nil {
			return nil, err
		}
		comparison.Pairwise = append(comparison.Pairwise, pairwise)
	}
	comparison.Gate = evaluateLongMemEvalReplicateGate(comparison, manifest.Gate)
	return comparison, nil
}

func analyzeLongMemEvalPairwise(
	cases []lmeReplicateCase,
	candidateArm string,
	baselineArm string,
) (lmeReplicatePairwise, error) {
	result := lmeReplicatePairwise{
		Name:          "candidate_vs_" + baselineArm,
		InferenceUnit: "question-majority",
		CandidateArm:  candidateArm,
		BaselineArm:   baselineArm,
		Cases:         len(cases),
	}
	for _, item := range cases {
		candidate, ok := item.Arms[candidateArm]
		if !ok {
			return result, fmt.Errorf(
				"replicate question %q is missing candidate arm %q",
				item.QuestionID,
				candidateArm,
			)
		}
		baseline, ok := item.Arms[baselineArm]
		if !ok {
			return result, fmt.Errorf(
				"replicate question %q is missing baseline arm %q",
				item.QuestionID,
				baselineArm,
			)
		}
		if candidate.MajorityCorrect {
			result.CandidateCorrect++
		}
		if baseline.MajorityCorrect {
			result.BaselineCorrect++
		}
		switch {
		case candidate.MajorityCorrect && !baseline.MajorityCorrect:
			result.CandidateWins++
		case !candidate.MajorityCorrect && baseline.MajorityCorrect:
			result.BaselineWins++
		default:
			result.Ties++
		}
	}
	result.DiscordantCases = result.CandidateWins + result.BaselineWins
	if result.Cases > 0 {
		result.AccuracyDelta = float64(
			result.CandidateCorrect-result.BaselineCorrect,
		) / float64(result.Cases)
	}
	result.ExactMcNemarPValue = exactMcNemarPValue(
		result.CandidateWins,
		result.BaselineWins,
	)
	return result, nil
}

func exactMcNemarPValue(candidateWins, baselineWins int) float64 {
	discordant := candidateWins + baselineWins
	if discordant <= 0 {
		return 1
	}
	tail := min(candidateWins, baselineWins)
	term := math.Ldexp(1, -discordant)
	probability := term
	for successes := 1; successes <= tail; successes++ {
		term *= float64(discordant-successes+1) / float64(successes)
		probability += term
	}
	return min(1, 2*probability)
}

func aggregateLongMemEvalReplicateArm(
	armName, backend string,
	candidate bool,
	pairs []lmeLoadedReplicateComparisonPair,
) (*lmeReplicateArm, []lmeReplicateCase, error) {
	arm := &lmeReplicateArm{
		Name:    armName,
		Backend: backend,
		ByType:  make(map[string]*lmeReplicateTypeSummary),
		ExtractionDiagnostics: lmeReplicateExtractionDiagnostics{
			OperationsByStage:             make(map[string]int),
			OperationsByType:              make(map[string]int),
			PostPolicyOperationsByStage:   make(map[string]int),
			PostPolicyOperationsByType:    make(map[string]int),
			PersistenceByStatus:           make(map[string]int),
			PersistenceByEffect:           make(map[string]int),
			PostPolicyPersistenceByStatus: make(map[string]int),
			PostPolicyPersistenceByEffect: make(map[string]int),
		},
	}
	caseCorrect := make(map[string]int)
	caseStages := make(map[string][]string)
	caseTypes := make(map[string]string)
	casePrimary := make(map[string]bool)
	caseIngestedPairs := make(map[string]int)
	caseMemoryUsage := make(map[string]lmeTokenUsage)
	caseMemoryUsageMissing := make(map[string]int)
	caseEmbeddingUsage := make(map[string]lmeEmbeddingUsage)
	caseAnswerUsage := make(map[string]lmeTokenUsage)
	caseJudgeUsage := make(map[string]lmeTokenUsage)
	caseFinalMemories := make(map[string]int)
	caseIngestDuration := make(map[string]int64)
	caseSearchDuration := make(map[string]int64)
	var sourceDigest string
	for replicateIndex, pair := range pairs {
		result := pair.Baseline
		if candidate {
			result = pair.Candidate
		}
		digest, err := longMemEvalReplicateSourceDigest(result, backend)
		if err != nil {
			return nil, nil, fmt.Errorf("replicate %q arm %q source digest: %w", pair.Spec.Name, armName, err)
		}
		if replicateIndex == 0 {
			sourceDigest = digest
			arm.SourceDigest = digest
			if err := addLongMemEvalReplicateSourceCost(arm, result, backend); err != nil {
				return nil, nil, err
			}
		} else if digest != sourceDigest {
			return nil, nil, fmt.Errorf("replicate %q arm %q changed immutable ingestion or retrieval source: %s != %s",
				pair.Spec.Name, armName, digest, sourceDigest)
		}
		for _, cr := range result.Cases {
			if cr == nil {
				continue
			}
			br := cr.BackendResults[backend]
			if br == nil {
				return nil, nil, fmt.Errorf("replicate %q case %q is missing backend %q", pair.Spec.Name, cr.QuestionID, backend)
			}
			correct, ok := longMemEvalJudgeCorrect(br)
			if !ok {
				return nil, nil, fmt.Errorf("replicate %q case %q backend %q has no valid judge", pair.Spec.Name, cr.QuestionID, backend)
			}
			if correct {
				caseCorrect[cr.QuestionID]++
			}
			if br.AnswerLogicalUsage == nil {
				return nil, nil, fmt.Errorf(
					"replicate %q case %q backend %q is missing "+
						"answer logical usage",
					pair.Spec.Name, cr.QuestionID, backend,
				)
			}
			answerUsage := caseAnswerUsage[cr.QuestionID]
			answerUsage.Add(*br.AnswerLogicalUsage)
			caseAnswerUsage[cr.QuestionID] = answerUsage
			arm.AnswerLogicalTokenUsage.Add(*br.AnswerLogicalUsage)
			if br.Judge == nil || br.Judge.LogicalTokenUsage == nil {
				return nil, nil, fmt.Errorf(
					"replicate %q case %q backend %q is missing "+
						"judge logical usage",
					pair.Spec.Name, cr.QuestionID, backend,
				)
			}
			judgeUsage := caseJudgeUsage[cr.QuestionID]
			judgeUsage.Add(*br.Judge.LogicalTokenUsage)
			caseJudgeUsage[cr.QuestionID] = judgeUsage
			arm.JudgeLogicalTokenUsage.Add(*br.Judge.LogicalTokenUsage)
			if replicateIndex == 0 {
				casePrimary[cr.QuestionID] = correct
				caseIngestedPairs[cr.QuestionID] = br.IngestedPairs
				memoryUsage, missing, err :=
					longMemEvalReplicateCaseMemoryLogicalUsage(
						br, backend,
					)
				if err != nil {
					return nil, nil, fmt.Errorf(
						"replicate %q case %q backend %q "+
							"memory logical usage: %w",
						pair.Spec.Name, cr.QuestionID, backend, err,
					)
				}
				if br.EmbeddingUsage == nil {
					return nil, nil, fmt.Errorf(
						"replicate %q case %q backend %q is missing "+
							"embedding usage",
						pair.Spec.Name, cr.QuestionID, backend,
					)
				}
				embeddingUsage := normalizeLongMemEvalEmbeddingUsage(
					*br.EmbeddingUsage,
				)
				caseMemoryUsage[cr.QuestionID] = memoryUsage
				caseMemoryUsageMissing[cr.QuestionID] = missing
				caseEmbeddingUsage[cr.QuestionID] = embeddingUsage
				caseFinalMemories[cr.QuestionID] = len(br.FinalMemories)
				caseIngestDuration[cr.QuestionID] = br.IngestDuration
				caseSearchDuration[cr.QuestionID] = br.SearchDuration
			}
			caseTypes[cr.QuestionID] = cr.QuestionType
			caseStages[cr.QuestionID] = append(caseStages[cr.QuestionID],
				evaluatedFailureStage(br, normalizedFailureStage(br), correct, true))
			if strings.TrimSpace(br.Error) != "" {
				arm.BackendErrors++
			}
			if strings.TrimSpace(br.AnswerError) != "" {
				arm.AnswerErrors++
			}
			if br.Judge == nil || strings.TrimSpace(br.Judge.Error) != "" {
				arm.JudgeErrors++
			}
		}
	}

	ids := make([]string, 0, len(caseTypes))
	for id := range caseTypes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	arm.Cases = len(ids)
	arm.TotalAnswerReplicates = len(ids) * len(pairs)
	cases := make([]lmeReplicateCase, 0, len(ids))
	majorityThreshold := len(pairs)/2 + 1
	for _, id := range ids {
		correct := caseCorrect[id]
		majority := correct >= majorityThreshold
		if casePrimary[id] {
			arm.PrimaryCorrect++
		}
		if majority {
			arm.MajorityCorrect++
		}
		if correct > 0 && correct < len(pairs) {
			arm.UnstableCases++
		}
		arm.CorrectReplicates += correct
		typeSummary := arm.ByType[caseTypes[id]]
		if typeSummary == nil {
			typeSummary = &lmeReplicateTypeSummary{}
			arm.ByType[caseTypes[id]] = typeSummary
		}
		typeSummary.Cases++
		typeSummary.TotalAnswerReplicates += len(pairs)
		typeSummary.CorrectReplicates += correct
		typeSummary.MemoryLogicalTokenUsage.Add(caseMemoryUsage[id])
		typeSummary.MemoryLogicalUsageMissingCalls +=
			caseMemoryUsageMissing[id]
		typeSummary.MemoryEmbeddingUsage.Add(caseEmbeddingUsage[id])
		typeSummary.AnswerLogicalTokenUsage.Add(caseAnswerUsage[id])
		typeSummary.JudgeLogicalTokenUsage.Add(caseJudgeUsage[id])
		typeSummary.FinalMemories += caseFinalMemories[id]
		typeSummary.IngestDurationMs += caseIngestDuration[id]
		typeSummary.SearchDurationMs += caseSearchDuration[id]
		if casePrimary[id] {
			typeSummary.PrimaryCorrect++
		}
		if majority {
			typeSummary.MajorityCorrect++
		}
		cases = append(cases, lmeReplicateCase{
			QuestionID: id, QuestionType: caseTypes[id],
			Arms: map[string]lmeReplicateCaseArmSummary{
				armName: {
					PrimaryCorrect: casePrimary[id], MajorityCorrect: majority,
					CorrectReplicates:              correct,
					IngestedPairs:                  caseIngestedPairs[id],
					Stages:                         caseStages[id],
					MemoryLogicalTokenUsage:        caseMemoryUsage[id],
					MemoryLogicalUsageMissingCalls: caseMemoryUsageMissing[id],
					MemoryEmbeddingUsage:           caseEmbeddingUsage[id],
					AnswerLogicalTokenUsage:        caseAnswerUsage[id],
					JudgeLogicalTokenUsage:         caseJudgeUsage[id],
					FinalMemories:                  caseFinalMemories[id],
					IngestDurationMs:               caseIngestDuration[id],
					SearchDurationMs:               caseSearchDuration[id],
				},
			},
		})
	}
	if err := validateLongMemEvalReplicateResourceRollup(arm); err != nil {
		return nil, nil, err
	}
	return arm, cases, nil
}

func validateLongMemEvalReplicateResourceRollup(
	arm *lmeReplicateArm,
) error {
	var total lmeReplicateTypeSummary
	for _, summary := range arm.ByType {
		if summary == nil {
			continue
		}
		total.Cases += summary.Cases
		total.MemoryLogicalTokenUsage.Add(
			summary.MemoryLogicalTokenUsage,
		)
		total.MemoryLogicalUsageMissingCalls +=
			summary.MemoryLogicalUsageMissingCalls
		total.MemoryEmbeddingUsage.Add(summary.MemoryEmbeddingUsage)
		total.AnswerLogicalTokenUsage.Add(
			summary.AnswerLogicalTokenUsage,
		)
		total.JudgeLogicalTokenUsage.Add(
			summary.JudgeLogicalTokenUsage,
		)
		total.FinalMemories += summary.FinalMemories
		total.IngestDurationMs += summary.IngestDurationMs
		total.SearchDurationMs += summary.SearchDurationMs
	}
	if total.Cases != arm.Cases ||
		total.MemoryLogicalTokenUsage != arm.MemoryLogicalTokenUsage ||
		total.MemoryLogicalUsageMissingCalls !=
			arm.MemoryLogicalUsageMissing ||
		total.MemoryEmbeddingUsage != arm.MemoryEmbeddingUsage ||
		total.AnswerLogicalTokenUsage != arm.AnswerLogicalTokenUsage ||
		total.JudgeLogicalTokenUsage != arm.JudgeLogicalTokenUsage ||
		total.FinalMemories != arm.FinalMemories ||
		total.IngestDurationMs != arm.IngestDurationMs ||
		total.SearchDurationMs != arm.SearchDurationMs {
		return fmt.Errorf(
			"replicate arm %q resource rollup does not match type totals",
			arm.Name,
		)
	}
	return nil
}

func longMemEvalReplicateSourceDigest(result *runResult, backend string) (string, error) {
	if result == nil {
		return "", errors.New("results are nil")
	}
	metadata := make(map[string]any)
	for key, value := range result.Metadata {
		if strings.HasPrefix(key, "answer_cache_") || key == "answer_execution" ||
			strings.HasPrefix(key, "reanswer_") ||
			strings.HasPrefix(key, "judge_") ||
			key == "logical_usage_hydration" ||
			key == "reanswered_at" || key == "judged_at" {
			continue
		}
		metadata[key] = value
	}
	stable := lmeReplicateStableSource{Metadata: metadata}
	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		br := cr.BackendResults[backend]
		if br == nil {
			return "", fmt.Errorf("case %q is missing backend %q", cr.QuestionID, backend)
		}
		copyBR := *br
		copyBR.Answer = ""
		copyBR.RawAnswer = ""
		copyBR.AnswerCacheKey = ""
		copyBR.AnswerSource = ""
		copyBR.AnswerMaxAttempts = 0
		copyBR.AnswerAttempts = nil
		copyBR.AnswerModelCalls = nil
		memoryUsage, err := longMemEvalReplicateMemoryLayerUsage(br)
		if err != nil {
			return "", fmt.Errorf("case %q backend %q usage: %w", cr.QuestionID, backend, err)
		}
		copyBR.TokenUsage = tokenUsagePtr(memoryUsage)
		copyBR.AnswerUsage = nil
		copyBR.AnswerLogicalUsage = nil
		copyBR.FailureStage = ""
		copyBR.EvaluatedFailureStage = ""
		copyBR.Judge = nil
		copyBR.ExactMatch = false
		copyBR.F1 = 0
		copyBR.BLEU = 0
		copyBR.AnswerDuration = 0
		copyBR.AnswerError = ""
		stable.Cases = append(stable.Cases, lmeReplicateStableCase{
			QuestionID: cr.QuestionID, QuestionType: cr.QuestionType,
			Question: cr.Question, QuestionDate: cr.QuestionDate, Answer: cr.Answer,
			AnswerSessionIDs: cr.AnswerSessionIDs, NumSessions: cr.NumSessions,
			Backend: copyBR,
		})
	}
	return longMemEvalJSONSHA256(stable)
}

func longMemEvalReplicateMemoryLayerUsage(br *backendResult) (lmeTokenUsage, error) {
	if br == nil {
		return lmeTokenUsage{}, errors.New("backend result is nil")
	}
	if br.TokenUsage == nil {
		return lmeTokenUsage{}, errors.New("token_usage is missing")
	}
	if br.EmbeddingUsage == nil {
		return lmeTokenUsage{}, errors.New("embedding_usage is missing")
	}
	usage := *br.TokenUsage
	answer := lmeTokenUsage{}
	if br.AnswerUsage != nil {
		answer = *br.AnswerUsage
	} else {
		switch br.AnswerSource {
		case lmeAnswerSourceCurrentRun, lmeAnswerSourcePersistent:
			if br.AnswerLogicalUsage == nil {
				return lmeTokenUsage{}, errors.New(
					"answer_logical_token_usage is missing for cached answer",
				)
			}
		default:
			return lmeTokenUsage{}, errors.New("answer_token_usage is missing")
		}
	}
	for _, field := range []struct {
		name          string
		total, answer int
	}{
		{name: "prompt_tokens", total: usage.PromptTokens, answer: answer.PromptTokens},
		{name: "completion_tokens", total: usage.CompletionTokens, answer: answer.CompletionTokens},
		{name: "total_tokens", total: usage.TotalTokens, answer: answer.TotalTokens},
		{name: "cached_tokens", total: usage.CachedTokens, answer: answer.CachedTokens},
		{name: "cache_creation_tokens", total: usage.CacheCreationTokens, answer: answer.CacheCreationTokens},
		{name: "cache_read_tokens", total: usage.CacheReadTokens, answer: answer.CacheReadTokens},
		{name: "reasoning_tokens", total: usage.ReasoningTokens, answer: answer.ReasoningTokens},
		{name: "llm_calls", total: usage.LLMCalls, answer: answer.LLMCalls},
		{name: "usage_missing_calls", total: usage.UsageMissingCalls, answer: answer.UsageMissingCalls},
	} {
		if field.answer > field.total {
			return lmeTokenUsage{}, fmt.Errorf(
				"answer %s %d exceeds total %d", field.name, field.answer, field.total,
			)
		}
	}
	usage.Sub(answer)
	return usage, nil
}

func longMemEvalReplicateCaseMemoryLogicalUsage(
	br *backendResult,
	backend string,
) (lmeTokenUsage, int, error) {
	if backend == "mem0" {
		usage, err := longMemEvalReplicateMemoryLayerUsage(br)
		return usage, 0, err
	}
	if br == nil {
		return lmeTokenUsage{}, 0, errors.New("backend result is nil")
	}
	var usage lmeTokenUsage
	missing := 0
	for _, trace := range br.IngestTraces {
		if trace.Extraction == nil {
			continue
		}
		for _, call := range trace.Extraction.ModelCalls {
			if call.LogicalTokenUsage == nil {
				missing++
				continue
			}
			usage.Add(*call.LogicalTokenUsage)
		}
	}
	return usage, missing, nil
}

func addLongMemEvalReplicateSourceCost(arm *lmeReplicateArm, result *runResult, backend string) error {
	arm.MemoryTokenUsageBasis = "provider-observed"
	arm.ModelCacheInitialEntries, arm.ModelCacheInitialKnown =
		longMemEvalMetadataInt(
			result.Metadata["model_response_cache_initial_entries"],
		)
	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		br := cr.BackendResults[backend]
		if br == nil {
			return fmt.Errorf("source case %q is missing backend %q", cr.QuestionID, backend)
		}
		if br.IngestedPairs <= 0 {
			return fmt.Errorf(
				"source case %q backend %q has no ingested pairs",
				cr.QuestionID, backend,
			)
		}
		if br.IngestedPairs != len(br.IngestTraces) {
			return fmt.Errorf(
				"source case %q backend %q records %d ingested pairs "+
					"but has %d ingestion traces",
				cr.QuestionID, backend,
				br.IngestedPairs, len(br.IngestTraces),
			)
		}
		memoryUsage, err := longMemEvalReplicateMemoryLayerUsage(br)
		if err != nil {
			return fmt.Errorf("source case %q backend %q usage: %w", cr.QuestionID, backend, err)
		}
		arm.MemoryTokenUsage.Add(memoryUsage)
		for _, trace := range br.IngestTraces {
			addLongMemEvalReplicateTraceDiagnostics(
				&arm.ExtractionDiagnostics,
				trace,
			)
			if trace.Extraction == nil {
				continue
			}
			for _, call := range trace.Extraction.ModelCalls {
				arm.MemoryModelRequests++
				if call.LogicalTokenUsage == nil {
					arm.MemoryLogicalUsageMissing++
				} else {
					arm.MemoryLogicalTokenUsage.Add(
						*call.LogicalTokenUsage,
					)
				}
				switch call.Source {
				case lmeModelCallSourceCurrentRun,
					lmeModelCallSourcePersistent:
					arm.MemoryModelCacheHits++
				}
			}
		}
		embeddingUsage := normalizeLongMemEvalEmbeddingUsage(
			*br.EmbeddingUsage,
		)
		arm.MemoryEmbeddingUsage.Add(embeddingUsage)
		arm.IngestedPairs += br.IngestedPairs
		arm.FinalMemories += len(br.FinalMemories)
		for _, snapshot := range br.FinalMemories {
			arm.FinalMemoriesByAttribution.Add(snapshot.AttributedTo)
		}
		arm.IngestDurationMs += br.IngestDuration
		arm.SearchDurationMs += br.SearchDuration
		if br.ProviderUsageReported {
			arm.ProviderUsageReportedCases++
		}
	}
	// Mem0 reports complete usage from its own server and is not served by
	// the benchmark's model-response cache. PGVector logical usage is
	// reconstructed per request from either the provider response or the
	// original usage retained in a response-cache entry.
	if backend == "mem0" {
		arm.MemoryLogicalTokenUsage = arm.MemoryTokenUsage
		arm.MemoryLogicalUsageComplete = true
	} else {
		arm.MemoryLogicalUsageComplete =
			arm.MemoryModelRequests > 0 &&
				arm.MemoryLogicalUsageMissing == 0
	}
	return nil
}

func addLongMemEvalReplicateTraceDiagnostics(
	diagnostics *lmeReplicateExtractionDiagnostics,
	trace ingestTrace,
) {
	for _, snapshot := range trace.NewMemories {
		diagnostics.PersistedNewMemoriesByAttribution.Add(
			snapshot.AttributedTo,
		)
	}
	if trace.Extraction == nil {
		return
	}

	diagnostics.TracedPairs++
	operations := trace.Extraction.Operations
	if len(operations) == 0 {
		diagnostics.ZeroOperationPairs++
	} else {
		diagnostics.OperationPairs++
	}
	diagnostics.Operations += len(operations)
	if diagnostics.OperationsByStage == nil {
		diagnostics.OperationsByStage = make(map[string]int)
	}
	if diagnostics.OperationsByType == nil {
		diagnostics.OperationsByType = make(map[string]int)
	}
	if diagnostics.PostPolicyOperationsByStage == nil {
		diagnostics.PostPolicyOperationsByStage = make(map[string]int)
	}
	if diagnostics.PostPolicyOperationsByType == nil {
		diagnostics.PostPolicyOperationsByType = make(map[string]int)
	}
	if diagnostics.PersistenceByStatus == nil {
		diagnostics.PersistenceByStatus = make(map[string]int)
	}
	if diagnostics.PersistenceByEffect == nil {
		diagnostics.PersistenceByEffect = make(map[string]int)
	}
	if diagnostics.PostPolicyPersistenceByStatus == nil {
		diagnostics.PostPolicyPersistenceByStatus = make(map[string]int)
	}
	if diagnostics.PostPolicyPersistenceByEffect == nil {
		diagnostics.PostPolicyPersistenceByEffect = make(map[string]int)
	}
	addLongMemEvalOperationCounts(
		operations,
		diagnostics.OperationsByStage,
		diagnostics.OperationsByType,
	)
	if trace.Extraction.PostPolicyObserved {
		diagnostics.PostPolicyObservedPairs++
		postPolicyOperations := trace.Extraction.PostPolicyOperations
		if len(postPolicyOperations) == 0 {
			diagnostics.PostPolicyZeroOperationPairs++
		} else {
			diagnostics.PostPolicyOperationPairs++
		}
		diagnostics.PostPolicyOperations += len(postPolicyOperations)
		addLongMemEvalOperationCounts(
			postPolicyOperations,
			diagnostics.PostPolicyOperationsByStage,
			diagnostics.PostPolicyOperationsByType,
		)
	}
	addLongMemEvalPersistenceCounts(
		trace.Extraction.Persistence,
		&diagnostics.PersistenceTracedOperations,
		diagnostics.PersistenceByStatus,
		diagnostics.PersistenceByEffect,
	)
	addLongMemEvalPersistenceCounts(
		trace.Extraction.PostPolicyPersistence,
		&diagnostics.PostPolicyPersistenceTraced,
		diagnostics.PostPolicyPersistenceByStatus,
		diagnostics.PostPolicyPersistenceByEffect,
	)

	modelRequests := len(trace.Extraction.ModelCalls)
	if modelRequests > 1 {
		diagnostics.MultiCallPairs++
		diagnostics.AdditionalModelRequests += modelRequests - 1
	}
}

func addLongMemEvalOperationCounts(
	operations []extractionOperation,
	byStage map[string]int,
	byType map[string]int,
) {
	for _, operation := range operations {
		stage := strings.TrimSpace(operation.Stage)
		if stage == "" {
			stage = "unspecified"
		}
		byStage[stage]++

		operationType := strings.TrimSpace(string(operation.Type))
		if operationType == "" {
			operationType = "unspecified"
		}
		byType[operationType]++
	}
}

func addLongMemEvalPersistenceCounts(
	persistenceTraces []extractionPersistenceTrace,
	traced *int,
	byStatus map[string]int,
	byEffect map[string]int,
) {
	for _, persistence := range persistenceTraces {
		(*traced)++
		status := strings.TrimSpace(persistence.Status)
		if status == "" {
			status = lmePersistenceUnverifiable
		}
		byStatus[status]++
		if effect := strings.TrimSpace(persistence.Effect); effect != "" {
			byEffect[effect]++
		}
	}
}

func (counts *lmeReplicateAttributionCounts) Add(attributedTo string) {
	switch strings.ToLower(strings.TrimSpace(attributedTo)) {
	case lmeAttributionUser:
		counts.User++
	case lmeAttributionAssistant:
		counts.Assistant++
	default:
		counts.Unknown++
	}
}

func evaluateLongMemEvalReplicateGate(
	comparison *lmeReplicateComparison,
	gate lmeReplicatePromotionGate,
) lmeReplicateGateResult {
	result := newLongMemEvalReplicateGateResult()
	main := comparison.Arms[lmeReplicateArmPGVectorMain]
	mem0 := comparison.Arms[lmeReplicateArmMem0OSS]
	candidate := comparison.Arms[lmeReplicateArmPGVectorCandidate]
	arms := []*lmeReplicateArm{main, candidate}
	if comparison.ComparisonScope == lmeReplicateScopeThreeArmPromotion {
		arms = []*lmeReplicateArm{main, mem0, candidate}
	}
	for _, arm := range arms {
		result.add(
			lmeReplicateGateDimensionIntegrity,
			arm.Name+"_cases", arm.Cases == gate.ExpectedCases,
			strconv.Itoa(arm.Cases), strconv.Itoa(gate.ExpectedCases))
		result.add(
			lmeReplicateGateDimensionIntegrity,
			arm.Name+"_errors",
			arm.BackendErrors+arm.AnswerErrors+arm.JudgeErrors == 0,
			fmt.Sprintf("backend=%d answer=%d judge=%d", arm.BackendErrors, arm.AnswerErrors, arm.JudgeErrors),
			"all zero")
		result.add(
			lmeReplicateGateDimensionIntegrity,
			arm.Name+"_provider_usage",
			arm.ProviderUsageReportedCases == arm.Cases &&
				arm.MemoryTokenUsage.UsageMissingCalls == 0 &&
				arm.MemoryEmbeddingUsage.UsageMissingCalls == 0 &&
				arm.MemoryEmbeddingUsage.ProviderErrors == 0,
			fmt.Sprintf("reported=%d/%d llm_missing=%d embedding_missing=%d embedding_errors=%d",
				arm.ProviderUsageReportedCases, arm.Cases,
				arm.MemoryTokenUsage.UsageMissingCalls,
				arm.MemoryEmbeddingUsage.UsageMissingCalls,
				arm.MemoryEmbeddingUsage.ProviderErrors),
			"reported for every case with zero missing calls and provider errors")
	}
	if comparison.ComparisonScope == lmeReplicateScopePairwise {
		addLongMemEvalReplicateIngestionChecks(
			&result, comparison, main, candidate,
		)
	} else {
		addLongMemEvalReplicateIngestionChecks(
			&result, comparison, main, mem0, candidate,
		)
	}
	if longMemEvalReplicateLatencyGateEnabled(gate) {
		addLongMemEvalReplicateLatencyIntegrityCheck(
			&result,
			comparison.Cases,
			gate,
		)
	}
	if comparison.ComparisonScope == lmeReplicateScopePairwise {
		result.add(
			lmeReplicateGateDimensionOutcome,
			"candidate_majority_vs_baseline",
			candidate.MajorityCorrect >= main.MajorityCorrect,
			fmt.Sprintf(
				"%d >= %d",
				candidate.MajorityCorrect, main.MajorityCorrect,
			),
			"non-regression",
		)
		result.add(
			lmeReplicateGateDimensionOutcome,
			"candidate_replicates_vs_baseline",
			candidate.CorrectReplicates >= main.CorrectReplicates,
			fmt.Sprintf(
				"%d >= %d",
				candidate.CorrectReplicates, main.CorrectReplicates,
			),
			"non-regression",
		)
	} else {
		result.add(
			lmeReplicateGateDimensionOutcome,
			"candidate_majority_vs_main",
			candidate.MajorityCorrect > main.MajorityCorrect,
			fmt.Sprintf(
				"%d > %d",
				candidate.MajorityCorrect, main.MajorityCorrect,
			),
			"strictly greater",
		)
		result.add(
			lmeReplicateGateDimensionOutcome,
			"candidate_majority_vs_mem0",
			candidate.MajorityCorrect > mem0.MajorityCorrect,
			fmt.Sprintf(
				"%d > %d",
				candidate.MajorityCorrect, mem0.MajorityCorrect,
			),
			"strictly greater",
		)
		result.add(
			lmeReplicateGateDimensionOutcome,
			"candidate_replicates_vs_main",
			candidate.CorrectReplicates > main.CorrectReplicates,
			fmt.Sprintf(
				"%d > %d",
				candidate.CorrectReplicates, main.CorrectReplicates,
			),
			"strictly greater",
		)
		result.add(
			lmeReplicateGateDimensionOutcome,
			"candidate_replicates_vs_mem0",
			candidate.CorrectReplicates > mem0.CorrectReplicates,
			fmt.Sprintf(
				"%d > %d",
				candidate.CorrectReplicates, mem0.CorrectReplicates,
			),
			"strictly greater",
		)
	}

	types := make(map[string]bool)
	for typ := range main.ByType {
		types[typ] = true
	}
	if comparison.ComparisonScope == lmeReplicateScopeThreeArmPromotion {
		for typ := range mem0.ByType {
			types[typ] = true
		}
	}
	for typ := range candidate.ByType {
		types[typ] = true
	}
	typeNames := make([]string, 0, len(types))
	for typ := range types {
		typeNames = append(typeNames, typ)
	}
	sort.Strings(typeNames)
	for _, typ := range typeNames {
		mainCorrect := lmeReplicateTypeMajority(main.ByType[typ])
		candidateCorrect := lmeReplicateTypeMajority(candidate.ByType[typ])
		if comparison.ComparisonScope == lmeReplicateScopePairwise {
			deficit := mainCorrect - candidateCorrect
			result.add(
				lmeReplicateGateDimensionOutcome,
				"category_"+typ,
				deficit <= gate.PerTypeMaxDeficit,
				fmt.Sprintf(
					"candidate=%d baseline=%d deficit=%d",
					candidateCorrect, mainCorrect, deficit,
				),
				fmt.Sprintf("deficit <= %d", gate.PerTypeMaxDeficit),
			)
			continue
		}
		mem0Correct := lmeReplicateTypeMajority(mem0.ByType[typ])
		stronger := max(mainCorrect, mem0Correct)
		deficit := stronger - candidateCorrect
		result.add(
			lmeReplicateGateDimensionOutcome,
			"category_"+typ,
			deficit <= gate.PerTypeMaxDeficit &&
				!(candidateCorrect == 0 && stronger > 0),
			fmt.Sprintf("candidate=%d main=%d mem0=%d deficit=%d", candidateCorrect, mainCorrect, mem0Correct, deficit),
			fmt.Sprintf("deficit <= %d and candidate nonzero when a baseline is nonzero", gate.PerTypeMaxDeficit))
	}
	if main.MemoryLogicalUsageComplete &&
		candidate.MemoryLogicalUsageComplete {
		addLongMemEvalReplicateRatioCheck(
			&result,
			"candidate_memory_llm_tokens_vs_main",
			candidate.MemoryLogicalTokenUsage.TotalTokens,
			main.MemoryLogicalTokenUsage.TotalTokens,
			gate.MemoryLLMTokenRatioMaximum,
		)
		if gate.MemoryLLMUncachedTokenRatioMaximum > 0 {
			addLongMemEvalReplicateRatioCheck(
				&result,
				"candidate_memory_llm_uncached_tokens_vs_main",
				nonNegativeTokenDifference(
					candidate.MemoryLogicalTokenUsage.TotalTokens,
					candidate.MemoryLogicalTokenUsage.CachedTokens,
				),
				nonNegativeTokenDifference(
					main.MemoryLogicalTokenUsage.TotalTokens,
					main.MemoryLogicalTokenUsage.CachedTokens,
				),
				gate.MemoryLLMUncachedTokenRatioMaximum,
			)
		}
	} else {
		result.add(
			lmeReplicateGateDimensionCost,
			"candidate_memory_llm_tokens_vs_main",
			false,
			fmt.Sprintf(
				"logical usage incomplete: main missing=%d/%d; "+
					"candidate missing=%d/%d",
				main.MemoryLogicalUsageMissing,
				main.MemoryModelRequests,
				candidate.MemoryLogicalUsageMissing,
				candidate.MemoryModelRequests,
			),
			"complete logical token usage for both PGVector arms",
		)
	}
	addLongMemEvalReplicateRatioCheck(&result, "candidate_memory_embedding_requests_vs_main",
		candidate.MemoryEmbeddingUsage.Requests, main.MemoryEmbeddingUsage.Requests,
		gate.MemoryEmbeddingRequestRatioMaximum)
	if gate.MemoryEmbeddingTokenRatioMaximum > 0 {
		if longMemEvalLogicalEmbeddingUsageComplete(
			main.MemoryEmbeddingUsage,
		) && longMemEvalLogicalEmbeddingUsageComplete(
			candidate.MemoryEmbeddingUsage,
		) {
			addLongMemEvalReplicateRatioCheck(
				&result,
				"candidate_memory_embedding_logical_tokens_vs_main",
				candidate.MemoryEmbeddingUsage.LogicalTotalTokens,
				main.MemoryEmbeddingUsage.LogicalTotalTokens,
				gate.MemoryEmbeddingTokenRatioMaximum,
			)
		} else {
			result.add(
				lmeReplicateGateDimensionCost,
				"candidate_memory_embedding_logical_tokens_vs_main",
				false,
				fmt.Sprintf(
					"logical usage incomplete: "+
						"main prompt=%d total=%d missing=%d/%d; "+
						"candidate prompt=%d total=%d missing=%d/%d",
					main.MemoryEmbeddingUsage.
						LogicalPromptTokens,
					main.MemoryEmbeddingUsage.
						LogicalTotalTokens,
					main.MemoryEmbeddingUsage.
						LogicalUsageMissingRequests,
					main.MemoryEmbeddingUsage.Requests,
					candidate.MemoryEmbeddingUsage.
						LogicalPromptTokens,
					candidate.MemoryEmbeddingUsage.
						LogicalTotalTokens,
					candidate.MemoryEmbeddingUsage.
						LogicalUsageMissingRequests,
					candidate.MemoryEmbeddingUsage.Requests,
				),
				"complete logical embedding token usage for both "+
					"PGVector arms",
			)
		}
	}
	addLongMemEvalReplicateRatioCheck(&result, "candidate_final_memories_vs_main",
		candidate.FinalMemories, main.FinalMemories, gate.FinalMemoryCountRatioMaximum)
	if gate.MemoryIngestDurationRatioMaximum > 0 {
		addLongMemEvalReplicateDurationRatioCheck(
			&result,
			"candidate_memory_ingest_duration_vs_main",
			candidate.IngestDurationMs,
			main.IngestDurationMs,
			gate.MemoryIngestDurationRatioMaximum,
		)
	}
	if gate.MemorySearchDurationRatioMaximum > 0 {
		addLongMemEvalReplicateDurationAllowanceCheck(
			&result,
			"candidate_memory_search_duration_vs_main",
			candidate.SearchDurationMs,
			main.SearchDurationMs,
			gate.MemorySearchDurationRatioMaximum,
			gate.MemorySearchMinimumAllowanceMs,
		)
	}
	if gate.MemorySearchP95MaximumMs > 0 {
		addLongMemEvalReplicateSearchP95Check(
			&result,
			comparison.Cases,
			gate.MemorySearchP95MaximumMs,
		)
	}
	return result
}

func longMemEvalReplicateLatencyGateEnabled(
	gate lmeReplicatePromotionGate,
) bool {
	return gate.MemoryIngestDurationRatioMaximum > 0 ||
		gate.MemorySearchDurationRatioMaximum > 0 ||
		gate.MemorySearchP95MaximumMs > 0
}

func addLongMemEvalReplicateLatencyIntegrityCheck(
	result *lmeReplicateGateResult,
	cases []lmeReplicateCase,
	gate lmeReplicatePromotionGate,
) {
	complete := len(cases) == gate.ExpectedCases
	checked := 0
	for _, item := range cases {
		main, mainOK := item.Arms[lmeReplicateArmPGVectorMain]
		candidate, candidateOK :=
			item.Arms[lmeReplicateArmPGVectorCandidate]
		if !mainOK || !candidateOK {
			complete = false
			continue
		}
		checked++
		if gate.MemoryIngestDurationRatioMaximum > 0 &&
			(main.IngestDurationMs <= 0 ||
				candidate.IngestDurationMs <= 0) {
			complete = false
		}
		if (gate.MemorySearchDurationRatioMaximum > 0 ||
			gate.MemorySearchP95MaximumMs > 0) &&
			(main.SearchDurationMs < 0 ||
				candidate.SearchDurationMs < 0) {
			complete = false
		}
	}
	result.add(
		lmeReplicateGateDimensionIntegrity,
		"latency_duration_completeness",
		complete,
		fmt.Sprintf(
			"complete=%t checked=%d/%d",
			complete,
			checked,
			gate.ExpectedCases,
		),
		"main and candidate arms exist for every case with positive ingest "+
			"and non-negative search durations",
	)
}

func newLongMemEvalReplicateGateResult() lmeReplicateGateResult {
	return lmeReplicateGateResult{
		Passed:          true,
		IntegrityPassed: true,
		OutcomePassed:   true,
		CostPassed:      true,
	}
}

func (r *lmeReplicateGateResult) add(
	dimension lmeReplicateGateDimension,
	name string,
	passed bool,
	actual string,
	requirement string,
) {
	r.Checks = append(r.Checks, lmeReplicateGateCheck{
		Dimension: dimension,
		Name:      name, Passed: passed, Actual: actual,
		Requirement: requirement,
	})
	switch dimension {
	case lmeReplicateGateDimensionIntegrity:
		r.IntegrityPassed = r.IntegrityPassed && passed
	case lmeReplicateGateDimensionOutcome:
		r.OutcomePassed = r.OutcomePassed && passed
	case lmeReplicateGateDimensionCost:
		r.CostPassed = r.CostPassed && passed
	default:
		r.IntegrityPassed = false
	}
	r.Passed = r.IntegrityPassed && r.OutcomePassed && r.CostPassed
}

func addLongMemEvalReplicateIngestionChecks(
	result *lmeReplicateGateResult,
	comparison *lmeReplicateComparison,
	main *lmeReplicateArm,
	others ...*lmeReplicateArm,
) {
	add := func(name string, passed bool, actual, requirement string) {
		result.add(
			lmeReplicateGateDimensionIntegrity,
			name, passed, actual, requirement,
		)
	}
	mainInvalid := 0
	for _, item := range comparison.Cases {
		source, ok := item.Arms[main.Name]
		if !ok || source.IngestedPairs <= 0 {
			mainInvalid++
		}
	}
	add(
		main.Name+"_ingested_pairs",
		main.IngestedPairs > 0 && mainInvalid == 0,
		fmt.Sprintf(
			"total=%d nonpositive_or_missing_cases=%d",
			main.IngestedPairs, mainInvalid,
		),
		"positive for every case",
	)
	for _, arm := range others {
		mismatched := 0
		for _, item := range comparison.Cases {
			source, sourceOK := item.Arms[main.Name]
			actual, actualOK := item.Arms[arm.Name]
			if !sourceOK || !actualOK ||
				actual.IngestedPairs != source.IngestedPairs {
				mismatched++
			}
		}
		add(
			arm.Name+"_ingested_pairs",
			arm.IngestedPairs == main.IngestedPairs &&
				mismatched == 0,
			fmt.Sprintf(
				"total=%d main_total=%d mismatched_cases=%d",
				arm.IngestedPairs, main.IngestedPairs, mismatched,
			),
			"same total and per-case counts as pgvector_main",
		)
	}
}

func lmeReplicateTypeMajority(summary *lmeReplicateTypeSummary) int {
	if summary == nil {
		return 0
	}
	return summary.MajorityCorrect
}

func addLongMemEvalReplicateRatioCheck(
	result *lmeReplicateGateResult,
	name string,
	numerator, denominator int,
	maximum float64,
) {
	passed := denominator > 0 && float64(numerator)/float64(denominator) <= maximum
	actual := "undefined"
	if denominator > 0 {
		actual = fmt.Sprintf("%.6f (%d/%d)", float64(numerator)/float64(denominator), numerator, denominator)
	}
	result.add(
		lmeReplicateGateDimensionCost,
		name, passed, actual, fmt.Sprintf("<= %.6f", maximum),
	)
}

func addLongMemEvalReplicateDurationRatioCheck(
	result *lmeReplicateGateResult,
	name string,
	numerator, denominator int64,
	maximum float64,
) {
	passed := denominator > 0 &&
		float64(numerator)/float64(denominator) <= maximum
	actual := "undefined"
	if denominator > 0 {
		actual = fmt.Sprintf(
			"%.6f (%dms/%dms)",
			float64(numerator)/float64(denominator),
			numerator,
			denominator,
		)
	}
	result.add(
		lmeReplicateGateDimensionCost,
		name, passed, actual, fmt.Sprintf("<= %.6f", maximum),
	)
}

func addLongMemEvalReplicateDurationAllowanceCheck(
	result *lmeReplicateGateResult,
	name string,
	actualMs, baselineMs int64,
	maximumRatio float64,
	minimumAllowanceMs int64,
) {
	allowedMs := minimumAllowanceMs
	if baselineMs > 0 {
		ratioAllowance := int64(math.Ceil(
			float64(baselineMs) * maximumRatio,
		))
		allowedMs = max(allowedMs, ratioAllowance)
	}
	passed := baselineMs >= 0 && actualMs >= 0 && allowedMs > 0 &&
		actualMs <= allowedMs
	result.add(
		lmeReplicateGateDimensionCost,
		name,
		passed,
		fmt.Sprintf(
			"candidate=%dms main=%dms allowed=%dms",
			actualMs,
			baselineMs,
			allowedMs,
		),
		fmt.Sprintf(
			"candidate <= max(main * %.6f, %dms)",
			maximumRatio,
			minimumAllowanceMs,
		),
	)
}

func addLongMemEvalReplicateSearchP95Check(
	result *lmeReplicateGateResult,
	cases []lmeReplicateCase,
	maximumMs int64,
) {
	durations := make([]int64, 0, len(cases))
	complete := true
	for _, item := range cases {
		arm, ok := item.Arms[lmeReplicateArmPGVectorCandidate]
		if !ok || arm.SearchDurationMs < 0 {
			complete = false
			continue
		}
		durations = append(durations, arm.SearchDurationMs)
	}
	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})
	p95 := int64(0)
	if len(durations) > 0 {
		index := int(math.Ceil(float64(len(durations))*0.95)) - 1
		p95 = durations[index]
	}
	passed := complete && len(durations) == len(cases) &&
		len(durations) > 0 && p95 <= maximumMs
	result.add(
		lmeReplicateGateDimensionCost,
		"candidate_memory_search_duration_p95",
		passed,
		fmt.Sprintf(
			"p95=%dms complete=%t cases=%d",
			p95,
			complete,
			len(durations),
		),
		fmt.Sprintf("p95 <= %dms with every case present", maximumMs),
	)
}

func formatLongMemEvalReplicateComparisonTSV(comparison *lmeReplicateComparison) string {
	var b strings.Builder
	b.WriteString("question_id\tquestion_type")
	armOrder := longMemEvalReplicateArmOrder(comparison)
	for _, armName := range armOrder {
		prefix := longMemEvalReplicateArmColumnPrefix(armName)
		fmt.Fprintf(
			&b,
			"\t%s_primary\t%s_correct_replicates\t%s_majority\t"+
				"%s_embedding_requests\t%s_embedding_provider_calls\t"+
				"%s_embedding_provider_tokens\t"+
				"%s_embedding_logical_tokens\t"+
				"%s_embedding_logical_missing_requests",
			prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix,
		)
	}
	b.WriteByte('\n')
	for _, item := range comparison.Cases {
		fmt.Fprintf(&b, "%s\t%s", item.QuestionID, item.QuestionType)
		for _, armName := range armOrder {
			arm := item.Arms[armName]
			fmt.Fprintf(
				&b, "\t%t\t%d\t%t\t%d\t%d\t%d\t%d\t%d",
				arm.PrimaryCorrect,
				arm.CorrectReplicates,
				arm.MajorityCorrect,
				arm.MemoryEmbeddingUsage.Requests,
				arm.MemoryEmbeddingUsage.Calls,
				arm.MemoryEmbeddingUsage.TotalTokens,
				arm.MemoryEmbeddingUsage.LogicalTotalTokens,
				arm.MemoryEmbeddingUsage.LogicalUsageMissingRequests,
			)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func formatLongMemEvalReplicateBadCasesTSV(
	comparison *lmeReplicateComparison,
) string {
	var b strings.Builder
	b.WriteString(
		"question_id\tquestion_type\tarm\tprimary_correct\t" +
			"correct_replicates\ttotal_replicates\tmajority_correct\t" +
			"stability\tearliest_failure_stage\tstage_counts\n",
	)
	for _, item := range comparison.Cases {
		for _, armName := range longMemEvalReplicateArmOrder(comparison) {
			arm := item.Arms[armName]
			stability := longMemEvalReplicateStability(
				arm.CorrectReplicates,
				comparison.ReplicateCount,
			)
			stage, counts := longMemEvalReplicateStageSummary(arm.Stages)
			if stability == "stable_correct" && stage == "ok" {
				continue
			}
			fmt.Fprintf(
				&b,
				"%s\t%s\t%s\t%t\t%d\t%d\t%t\t%s\t%s\t%s\n",
				item.QuestionID,
				item.QuestionType,
				armName,
				arm.PrimaryCorrect,
				arm.CorrectReplicates,
				comparison.ReplicateCount,
				arm.MajorityCorrect,
				stability,
				stage,
				counts,
			)
		}
	}
	return b.String()
}

func longMemEvalReplicateArmOrder(
	comparison *lmeReplicateComparison,
) []string {
	order := []string{
		lmeReplicateArmPGVectorMain,
		lmeReplicateArmMem0OSS,
		lmeReplicateArmPGVectorCandidate,
	}
	result := make([]string, 0, len(order))
	for _, name := range order {
		if comparison.Arms[name] != nil {
			result = append(result, name)
		}
	}
	return result
}

func longMemEvalReplicateArmColumnPrefix(name string) string {
	switch name {
	case lmeReplicateArmPGVectorMain:
		return "main"
	case lmeReplicateArmMem0OSS:
		return "mem0"
	case lmeReplicateArmPGVectorCandidate:
		return "candidate"
	default:
		return name
	}
}

func longMemEvalReplicateStability(correct, total int) string {
	switch {
	case total <= 0:
		return "invalid"
	case correct == total:
		return "stable_correct"
	case correct == 0:
		return "stable_incorrect"
	default:
		return "unstable"
	}
}

func longMemEvalReplicateStageSummary(stages []string) (string, string) {
	counts := make(map[string]int)
	for _, stage := range stages {
		stage = strings.TrimSpace(stage)
		if stage == "" {
			stage = "unknown"
		}
		counts[stage]++
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	formatted := make([]string, 0, len(names))
	for _, name := range names {
		formatted = append(formatted, fmt.Sprintf("%s=%d", name, counts[name]))
	}
	return earliestLongMemEvalReplicateFailureStage(counts),
		strings.Join(formatted, ";")
}

func earliestLongMemEvalReplicateFailureStage(
	counts map[string]int,
) string {
	for _, stage := range []string{
		"backend_error",
		"missing",
		"extraction_turn_miss",
		"extraction_session_miss",
		"retrieval_turn_miss",
		"retrieval_session_miss",
		"answer_error",
		"abstention_answered",
		"evidence_or_answer_miss",
		"unknown",
	} {
		if counts[stage] > 0 {
			return stage
		}
	}
	remaining := make([]string, 0, len(counts))
	for stage := range counts {
		if stage != "ok" && stage != "ok_abstention" {
			remaining = append(remaining, stage)
		}
	}
	if len(remaining) > 0 {
		sort.Strings(remaining)
		return remaining[0]
	}
	return "ok"
}

func formatLongMemEvalReplicateComparisonMarkdown(comparison *lmeReplicateComparison) string {
	var b strings.Builder
	b.WriteString("# LongMemEval Replicate Comparison\n\n")
	fmt.Fprintf(&b, "- Manifest SHA-256: `%s`\n", comparison.ManifestSHA256)
	fmt.Fprintf(&b, "- Answer replicates: %d\n", comparison.ReplicateCount)
	gateStatus := "FAIL"
	if comparison.Gate.Passed {
		gateStatus = "PASS"
	}
	fmt.Fprintf(&b, "- Promotion gate: **%s**\n\n", gateStatus)
	b.WriteString("| Arm | Primary | Majority | Correct replicates | Unstable | Pairs | Provider-observed memory LLM tokens | Logical memory LLM tokens | Logical answer tokens | Logical judge tokens | Memory model cache hits | Logical memory cost complete | Embedding requests | Embedding provider calls | Embedding provider tokens | Logical embedding tokens | Missing logical embedding usage | Memories | Ingest ms | Search ms |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | :---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, name := range longMemEvalReplicateArmOrder(comparison) {
		arm := comparison.Arms[name]
		fmt.Fprintf(&b, "| %s | %d/%d | %d/%d | %d/%d | %d | %d | %d | %d | %d | %d | %d | %t | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			name, arm.PrimaryCorrect, arm.Cases, arm.MajorityCorrect, arm.Cases,
			arm.CorrectReplicates, arm.TotalAnswerReplicates, arm.UnstableCases,
			arm.IngestedPairs,
			arm.MemoryTokenUsage.TotalTokens,
			arm.MemoryLogicalTokenUsage.TotalTokens,
			arm.AnswerLogicalTokenUsage.TotalTokens,
			arm.JudgeLogicalTokenUsage.TotalTokens,
			arm.MemoryModelCacheHits, arm.MemoryLogicalUsageComplete,
			arm.MemoryEmbeddingUsage.Requests,
			arm.MemoryEmbeddingUsage.Calls, arm.MemoryEmbeddingUsage.TotalTokens,
			arm.MemoryEmbeddingUsage.LogicalTotalTokens,
			arm.MemoryEmbeddingUsage.LogicalUsageMissingRequests,
			arm.FinalMemories, arm.IngestDurationMs, arm.SearchDurationMs)
	}
	b.WriteString("\n## Resource Accounting by Type\n\n")
	b.WriteString("| Arm | Type | Cases | Logical memory tokens | Missing memory usages | Embedding requests | Embedding provider tokens | Logical embedding tokens | Missing logical embedding usage | Logical answer tokens | Logical judge tokens | Memories | Ingest ms | Search ms |\n")
	b.WriteString("| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, name := range longMemEvalReplicateArmOrder(comparison) {
		arm := comparison.Arms[name]
		typeNames := make([]string, 0, len(arm.ByType))
		for typeName := range arm.ByType {
			typeNames = append(typeNames, typeName)
		}
		sort.Strings(typeNames)
		for _, typeName := range typeNames {
			summary := arm.ByType[typeName]
			fmt.Fprintf(
				&b,
				"| %s | %s | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n",
				name,
				typeName,
				summary.Cases,
				summary.MemoryLogicalTokenUsage.TotalTokens,
				summary.MemoryLogicalUsageMissingCalls,
				summary.MemoryEmbeddingUsage.Requests,
				summary.MemoryEmbeddingUsage.TotalTokens,
				summary.MemoryEmbeddingUsage.LogicalTotalTokens,
				summary.MemoryEmbeddingUsage.LogicalUsageMissingRequests,
				summary.AnswerLogicalTokenUsage.TotalTokens,
				summary.JudgeLogicalTokenUsage.TotalTokens,
				summary.FinalMemories,
				summary.IngestDurationMs,
				summary.SearchDurationMs,
			)
		}
	}
	b.WriteString("\n## Pairwise Majority Outcomes\n\n")
	b.WriteString("| Comparison | Candidate | Baseline | Wins | Losses | Ties | Accuracy delta | Discordant cases | Exact McNemar p |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, pairwise := range comparison.Pairwise {
		fmt.Fprintf(
			&b,
			"| %s | %d/%d | %d/%d | %d | %d | %d | %+.3f | %d | %.6f |\n",
			pairwise.Name,
			pairwise.CandidateCorrect,
			pairwise.Cases,
			pairwise.BaselineCorrect,
			pairwise.Cases,
			pairwise.CandidateWins,
			pairwise.BaselineWins,
			pairwise.Ties,
			pairwise.AccuracyDelta,
			pairwise.DiscordantCases,
			pairwise.ExactMcNemarPValue,
		)
	}
	b.WriteString("\n## Extraction Diagnostics\n\n")
	b.WriteString("| Arm | Traced pairs | Operation pairs | Zero-op pairs | Operations | Primary ops | Assistant-result ops | Add ops | Update ops | Multi-call pairs | Additional model requests | New user | New assistant | Final user | Final assistant | Unknown final |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, name := range longMemEvalReplicateArmOrder(comparison) {
		arm := comparison.Arms[name]
		diagnostics := arm.ExtractionDiagnostics
		fmt.Fprintf(
			&b,
			"| %s | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			name,
			diagnostics.TracedPairs,
			diagnostics.OperationPairs,
			diagnostics.ZeroOperationPairs,
			diagnostics.Operations,
			diagnostics.OperationsByStage["primary"],
			diagnostics.OperationsByStage["assistant_result"],
			diagnostics.OperationsByType[string(extractor.OperationAdd)],
			diagnostics.OperationsByType[string(extractor.OperationUpdate)],
			diagnostics.MultiCallPairs,
			diagnostics.AdditionalModelRequests,
			diagnostics.PersistedNewMemoriesByAttribution.User,
			diagnostics.PersistedNewMemoriesByAttribution.Assistant,
			arm.FinalMemoriesByAttribution.User,
			arm.FinalMemoriesByAttribution.Assistant,
			arm.FinalMemoriesByAttribution.Unknown,
		)
	}
	b.WriteString("\n## Post-Policy Diagnostics\n\n")
	b.WriteString("| Arm | Covered pairs | Operation pairs | Zero-op pairs | Operations | Primary ops | Assistant-result ops | Add ops | Update ops | Delete ops | Clear ops | Raw-to-post delta |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, name := range longMemEvalReplicateArmOrder(comparison) {
		diagnostics := comparison.Arms[name].ExtractionDiagnostics
		fmt.Fprintf(
			&b,
			"| %s | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			name,
			diagnostics.PostPolicyObservedPairs,
			diagnostics.PostPolicyOperationPairs,
			diagnostics.PostPolicyZeroOperationPairs,
			diagnostics.PostPolicyOperations,
			diagnostics.PostPolicyOperationsByStage["primary"],
			diagnostics.PostPolicyOperationsByStage["assistant_result"],
			diagnostics.PostPolicyOperationsByType[string(extractor.OperationAdd)],
			diagnostics.PostPolicyOperationsByType[string(extractor.OperationUpdate)],
			diagnostics.PostPolicyOperationsByType[string(extractor.OperationDelete)],
			diagnostics.PostPolicyOperationsByType[string(extractor.OperationClear)],
			diagnostics.Operations-diagnostics.PostPolicyOperations,
		)
	}
	b.WriteString("\n## Raw Persistence Diagnostics\n\n")
	b.WriteString("| Arm | Covered operations | Observed | Already satisfied | Not observed | Unverifiable | Add effects | Update effects | Delete effects | Clear effects |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, name := range longMemEvalReplicateArmOrder(comparison) {
		diagnostics := comparison.Arms[name].ExtractionDiagnostics
		fmt.Fprintf(
			&b,
			"| %s | %d/%d | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			name,
			diagnostics.PersistenceTracedOperations,
			diagnostics.Operations,
			diagnostics.PersistenceByStatus[lmePersistenceObserved],
			diagnostics.PersistenceByStatus[lmePersistenceAlreadySatisfied],
			diagnostics.PersistenceByStatus[lmePersistenceNotObserved],
			diagnostics.PersistenceByStatus[lmePersistenceUnverifiable],
			diagnostics.PersistenceByEffect[string(extractor.OperationAdd)],
			diagnostics.PersistenceByEffect[string(extractor.OperationUpdate)],
			diagnostics.PersistenceByEffect[string(extractor.OperationDelete)],
			diagnostics.PersistenceByEffect[string(extractor.OperationClear)],
		)
	}
	b.WriteString("\n## Post-Policy Persistence Diagnostics\n\n")
	b.WriteString("| Arm | Covered operations | Observed | Already satisfied | Not observed | Unverifiable | Add effects | Update effects | Delete effects | Clear effects |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, name := range longMemEvalReplicateArmOrder(comparison) {
		diagnostics := comparison.Arms[name].ExtractionDiagnostics
		fmt.Fprintf(
			&b,
			"| %s | %d/%d | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			name,
			diagnostics.PostPolicyPersistenceTraced,
			diagnostics.PostPolicyOperations,
			diagnostics.PostPolicyPersistenceByStatus[lmePersistenceObserved],
			diagnostics.PostPolicyPersistenceByStatus[lmePersistenceAlreadySatisfied],
			diagnostics.PostPolicyPersistenceByStatus[lmePersistenceNotObserved],
			diagnostics.PostPolicyPersistenceByStatus[lmePersistenceUnverifiable],
			diagnostics.PostPolicyPersistenceByEffect[string(extractor.OperationAdd)],
			diagnostics.PostPolicyPersistenceByEffect[string(extractor.OperationUpdate)],
			diagnostics.PostPolicyPersistenceByEffect[string(extractor.OperationDelete)],
			diagnostics.PostPolicyPersistenceByEffect[string(extractor.OperationClear)],
		)
	}
	b.WriteString("\n## Gate\n\n")
	fmt.Fprintf(&b, "- Integrity: **%t**\n", comparison.Gate.IntegrityPassed)
	fmt.Fprintf(&b, "- Outcome: **%t**\n", comparison.Gate.OutcomePassed)
	fmt.Fprintf(&b, "- Cost: **%t**\n\n", comparison.Gate.CostPassed)
	for _, check := range comparison.Gate.Checks {
		mark := "x"
		if !check.Passed {
			mark = " "
		}
		fmt.Fprintf(&b, "- [%s] `%s/%s`: %s; required %s\n",
			mark, check.Dimension, check.Name, check.Actual, check.Requirement)
	}
	b.WriteString("\n## Case Failure Attribution\n\n")
	b.WriteString(
		"The earliest stage is deterministic pipeline attribution from the " +
			"saved per-replicate stages; stage counts retain answer and judge " +
			"instability without making new model calls.\n\n",
	)
	b.WriteString("| Question | Type | Arm | Correct | Stability | Earliest stage | Stages |\n")
	b.WriteString("| --- | --- | --- | ---: | --- | --- | --- |\n")
	for _, item := range comparison.Cases {
		for _, armName := range longMemEvalReplicateArmOrder(comparison) {
			arm := item.Arms[armName]
			stability := longMemEvalReplicateStability(
				arm.CorrectReplicates,
				comparison.ReplicateCount,
			)
			stage, counts := longMemEvalReplicateStageSummary(arm.Stages)
			if stability == "stable_correct" && stage == "ok" {
				continue
			}
			fmt.Fprintf(
				&b,
				"| `%s` | %s | %s | %d/%d | %s | %s | %s |\n",
				item.QuestionID,
				item.QuestionType,
				armName,
				arm.CorrectReplicates,
				comparison.ReplicateCount,
				stability,
				stage,
				counts,
			)
		}
	}
	b.WriteString("\n## Cases\n\n")
	armOrder := longMemEvalReplicateArmOrder(comparison)
	b.WriteString("| Question | Type")
	for _, armName := range armOrder {
		fmt.Fprintf(&b, " | %s", armName)
	}
	b.WriteString(" |\n| --- | ---")
	for range armOrder {
		b.WriteString(" | ---:")
	}
	b.WriteString(" |\n")
	for _, item := range comparison.Cases {
		fmt.Fprintf(&b, "| `%s` | %s", item.QuestionID, item.QuestionType)
		for _, armName := range armOrder {
			fmt.Fprintf(
				&b, " | %d/%d",
				item.Arms[armName].CorrectReplicates,
				comparison.ReplicateCount,
			)
		}
		b.WriteString(" |\n")
	}
	return b.String()
}
