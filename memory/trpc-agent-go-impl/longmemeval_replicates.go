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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	lmeReplicateComparisonSchemaVersion = 1
	lmeReplicateKindPrimary             = "primary"
	lmeReplicateKindIndependentReanswer = "independent-reanswer"

	lmeReplicateArmPGVectorMain      = "pgvector_main"
	lmeReplicateArmMem0OSS           = "mem0_oss"
	lmeReplicateArmPGVectorCandidate = "pgvector_candidate"
)

type lmeReplicateComparisonManifest struct {
	SchemaVersion int                          `json:"schema_version"`
	Replicates    []lmeReplicateComparisonPair `json:"replicates"`
	Gate          lmeReplicatePromotionGate    `json:"gate"`
}

type lmeReplicateComparisonPair struct {
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	BaselineResults  string `json:"baseline_results"`
	CandidateResults string `json:"candidate_results"`
}

type lmeReplicatePromotionGate struct {
	ExpectedCases                    int     `json:"expected_cases"`
	JudgeRuns                        int     `json:"judge_runs"`
	PerTypeMaxDeficit                int     `json:"per_type_max_deficit"`
	MemoryLLMTokenRatioMaximum       float64 `json:"memory_llm_token_ratio_maximum"`
	MemoryEmbeddingTokenRatioMaximum float64 `json:"memory_embedding_token_ratio_maximum"`
	FinalMemoryCountRatioMaximum     float64 `json:"final_memory_count_ratio_maximum"`
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
	SchemaVersion  int                         `json:"schema_version"`
	CreatedAt      string                      `json:"created_at"`
	ManifestSHA256 string                      `json:"manifest_sha256"`
	ReplicateCount int                         `json:"replicate_count"`
	Inputs         []lmeReplicateInputAudit    `json:"inputs"`
	Arms           map[string]*lmeReplicateArm `json:"arms"`
	Cases          []lmeReplicateCase          `json:"cases"`
	Gate           lmeReplicateGateResult      `json:"gate"`
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
	MemoryEmbeddingUsage       lmeEmbeddingUsage                   `json:"memory_embedding_usage"`
	FinalMemories              int                                 `json:"final_memories"`
	IngestDurationMs           int64                               `json:"ingest_duration_ms"`
	SearchDurationMs           int64                               `json:"search_duration_ms"`
	ByType                     map[string]*lmeReplicateTypeSummary `json:"by_type"`
}

type lmeReplicateTypeSummary struct {
	Cases                 int `json:"cases"`
	PrimaryCorrect        int `json:"primary_correct"`
	MajorityCorrect       int `json:"majority_correct"`
	CorrectReplicates     int `json:"correct_replicates"`
	TotalAnswerReplicates int `json:"total_answer_replicates"`
}

type lmeReplicateCase struct {
	QuestionID   string                                `json:"question_id"`
	QuestionType string                                `json:"question_type"`
	Arms         map[string]lmeReplicateCaseArmSummary `json:"arms"`
}

type lmeReplicateCaseArmSummary struct {
	PrimaryCorrect    bool     `json:"primary_correct"`
	MajorityCorrect   bool     `json:"majority_correct"`
	CorrectReplicates int      `json:"correct_replicates"`
	Stages            []string `json:"stages"`
}

type lmeReplicateGateResult struct {
	Passed bool                    `json:"passed"`
	Checks []lmeReplicateGateCheck `json:"checks"`
}

type lmeReplicateGateCheck struct {
	Name        string `json:"name"`
	Passed      bool   `json:"passed"`
	Actual      string `json:"actual"`
	Requirement string `json:"requirement"`
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
		if err := validateLongMemEvalComparison(baseline, candidate); err != nil {
			return manifest, manifestDigest, nil, fmt.Errorf("validate replicate %q comparison: %w", spec.Name, err)
		}
		if err := validateLongMemEvalReplicateKind(spec, baseline, candidate); err != nil {
			return manifest, manifestDigest, nil, err
		}
		if err := validateLongMemEvalReplicateFreshCaches(spec.Name, baseline); err != nil {
			return manifest, manifestDigest, nil, err
		}
		if err := validateLongMemEvalReplicateJudges(spec.Name, manifest.Gate.JudgeRuns, baseline, candidate); err != nil {
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

func validateLongMemEvalReplicateManifest(manifest lmeReplicateComparisonManifest) error {
	if manifest.SchemaVersion != lmeReplicateComparisonSchemaVersion {
		return fmt.Errorf("LongMemEval replicate schema version is %d, want %d",
			manifest.SchemaVersion, lmeReplicateComparisonSchemaVersion)
	}
	if len(manifest.Replicates) < 3 || len(manifest.Replicates)%2 == 0 {
		return fmt.Errorf("LongMemEval replicate manifest requires an odd count of at least 3, got %d", len(manifest.Replicates))
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
		wantKind := lmeReplicateKindIndependentReanswer
		if index == 0 {
			wantKind = lmeReplicateKindPrimary
		}
		if replicate.Kind != wantKind {
			return fmt.Errorf("LongMemEval replicate %q kind is %q, want %q", replicate.Name, replicate.Kind, wantKind)
		}
	}
	gate := manifest.Gate
	if gate.ExpectedCases <= 0 || gate.JudgeRuns <= 1 || gate.JudgeRuns%2 == 0 || gate.PerTypeMaxDeficit < 0 ||
		gate.MemoryLLMTokenRatioMaximum <= 0 || gate.MemoryEmbeddingTokenRatioMaximum <= 0 ||
		gate.FinalMemoryCountRatioMaximum <= 0 {
		return fmt.Errorf("LongMemEval replicate manifest has invalid promotion gate: %+v", gate)
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

func validateLongMemEvalReplicateFreshCaches(replicate string, baseline *runResult) error {
	if baseline == nil {
		return fmt.Errorf("replicate %q baseline is nil", replicate)
	}
	for _, key := range []string{"answer_cache_initial_entries", "judge_cache_initial_entries"} {
		value, ok := longMemEvalMetadataInt(baseline.Metadata[key])
		if !ok {
			return fmt.Errorf("replicate %q baseline metadata %q is not an integer", replicate, key)
		}
		if value != 0 {
			return fmt.Errorf("replicate %q baseline %s is %d, want 0", replicate, key, value)
		}
	}
	return nil
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

func aggregateLongMemEvalReplicates(
	manifestDigest string,
	manifest lmeReplicateComparisonManifest,
	pairs []lmeLoadedReplicateComparisonPair,
) (*lmeReplicateComparison, error) {
	if len(pairs) != len(manifest.Replicates) {
		return nil, errors.New("LongMemEval loaded replicate count does not match manifest")
	}
	comparison := &lmeReplicateComparison{
		SchemaVersion:  lmeReplicateComparisonSchemaVersion,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		ManifestSHA256: manifestDigest,
		ReplicateCount: len(pairs),
		Inputs:         make([]lmeReplicateInputAudit, 0, len(pairs)),
		Arms:           make(map[string]*lmeReplicateArm, 3),
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
	bindings := []armBinding{
		{name: lmeReplicateArmPGVectorMain, backend: "pgvector"},
		{name: lmeReplicateArmMem0OSS, backend: "mem0"},
		{name: lmeReplicateArmPGVectorCandidate, backend: "pgvector", candidate: true},
	}
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
	comparison.Gate = evaluateLongMemEvalReplicateGate(comparison, manifest.Gate)
	return comparison, nil
}

func aggregateLongMemEvalReplicateArm(
	armName, backend string,
	candidate bool,
	pairs []lmeLoadedReplicateComparisonPair,
) (*lmeReplicateArm, []lmeReplicateCase, error) {
	arm := &lmeReplicateArm{Name: armName, Backend: backend, ByType: make(map[string]*lmeReplicateTypeSummary)}
	caseCorrect := make(map[string]int)
	caseStages := make(map[string][]string)
	caseTypes := make(map[string]string)
	casePrimary := make(map[string]bool)
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
			if replicateIndex == 0 {
				casePrimary[cr.QuestionID] = correct
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
					CorrectReplicates: correct, Stages: caseStages[id],
				},
			},
		})
	}
	return arm, cases, nil
}

func longMemEvalReplicateSourceDigest(result *runResult, backend string) (string, error) {
	if result == nil {
		return "", errors.New("results are nil")
	}
	metadata := make(map[string]any)
	for key, value := range result.Metadata {
		if strings.HasPrefix(key, "answer_cache_") || strings.HasPrefix(key, "reanswer_") ||
			strings.HasPrefix(key, "judge_") || key == "reanswered_at" || key == "judged_at" {
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
		copyBR.AnswerModelCalls = nil
		memoryUsage, err := longMemEvalReplicateMemoryLayerUsage(br)
		if err != nil {
			return "", fmt.Errorf("case %q backend %q usage: %w", cr.QuestionID, backend, err)
		}
		copyBR.TokenUsage = tokenUsagePtr(memoryUsage)
		copyBR.AnswerUsage = nil
		copyBR.FailureStage = ""
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
	if br.AnswerUsage == nil {
		return lmeTokenUsage{}, errors.New("answer_token_usage is missing")
	}
	if br.EmbeddingUsage == nil {
		return lmeTokenUsage{}, errors.New("embedding_usage is missing")
	}
	usage := *br.TokenUsage
	answer := *br.AnswerUsage
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

func addLongMemEvalReplicateSourceCost(arm *lmeReplicateArm, result *runResult, backend string) error {
	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		br := cr.BackendResults[backend]
		if br == nil {
			return fmt.Errorf("source case %q is missing backend %q", cr.QuestionID, backend)
		}
		memoryUsage, err := longMemEvalReplicateMemoryLayerUsage(br)
		if err != nil {
			return fmt.Errorf("source case %q backend %q usage: %w", cr.QuestionID, backend, err)
		}
		arm.MemoryTokenUsage.Add(memoryUsage)
		arm.MemoryEmbeddingUsage.Add(*br.EmbeddingUsage)
		arm.FinalMemories += len(br.FinalMemories)
		arm.IngestDurationMs += br.IngestDuration
		arm.SearchDurationMs += br.SearchDuration
		if br.ProviderUsageReported {
			arm.ProviderUsageReportedCases++
		}
	}
	return nil
}

func evaluateLongMemEvalReplicateGate(
	comparison *lmeReplicateComparison,
	gate lmeReplicatePromotionGate,
) lmeReplicateGateResult {
	result := lmeReplicateGateResult{Passed: true}
	add := func(name string, passed bool, actual, requirement string) {
		result.Checks = append(result.Checks, lmeReplicateGateCheck{
			Name: name, Passed: passed, Actual: actual, Requirement: requirement,
		})
		result.Passed = result.Passed && passed
	}
	main := comparison.Arms[lmeReplicateArmPGVectorMain]
	mem0 := comparison.Arms[lmeReplicateArmMem0OSS]
	candidate := comparison.Arms[lmeReplicateArmPGVectorCandidate]
	for _, arm := range []*lmeReplicateArm{main, mem0, candidate} {
		add(arm.Name+"_cases", arm.Cases == gate.ExpectedCases,
			strconv.Itoa(arm.Cases), strconv.Itoa(gate.ExpectedCases))
		add(arm.Name+"_errors", arm.BackendErrors+arm.AnswerErrors+arm.JudgeErrors == 0,
			fmt.Sprintf("backend=%d answer=%d judge=%d", arm.BackendErrors, arm.AnswerErrors, arm.JudgeErrors),
			"all zero")
		add(arm.Name+"_provider_usage", arm.ProviderUsageReportedCases == arm.Cases &&
			arm.MemoryTokenUsage.UsageMissingCalls == 0 && arm.MemoryEmbeddingUsage.UsageMissingCalls == 0,
			fmt.Sprintf("reported=%d/%d llm_missing=%d embedding_missing=%d",
				arm.ProviderUsageReportedCases, arm.Cases,
				arm.MemoryTokenUsage.UsageMissingCalls, arm.MemoryEmbeddingUsage.UsageMissingCalls),
			"reported for every case with zero missing calls")
	}
	add("candidate_majority_vs_main", candidate.MajorityCorrect > main.MajorityCorrect,
		fmt.Sprintf("%d > %d", candidate.MajorityCorrect, main.MajorityCorrect), "strictly greater")
	add("candidate_majority_vs_mem0", candidate.MajorityCorrect > mem0.MajorityCorrect,
		fmt.Sprintf("%d > %d", candidate.MajorityCorrect, mem0.MajorityCorrect), "strictly greater")
	add("candidate_replicates_vs_main", candidate.CorrectReplicates > main.CorrectReplicates,
		fmt.Sprintf("%d > %d", candidate.CorrectReplicates, main.CorrectReplicates), "strictly greater")
	add("candidate_replicates_vs_mem0", candidate.CorrectReplicates > mem0.CorrectReplicates,
		fmt.Sprintf("%d > %d", candidate.CorrectReplicates, mem0.CorrectReplicates), "strictly greater")

	types := make(map[string]bool)
	for typ := range main.ByType {
		types[typ] = true
	}
	for typ := range mem0.ByType {
		types[typ] = true
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
		mem0Correct := lmeReplicateTypeMajority(mem0.ByType[typ])
		candidateCorrect := lmeReplicateTypeMajority(candidate.ByType[typ])
		stronger := max(mainCorrect, mem0Correct)
		deficit := stronger - candidateCorrect
		add("category_"+typ, deficit <= gate.PerTypeMaxDeficit && !(candidateCorrect == 0 && stronger > 0),
			fmt.Sprintf("candidate=%d main=%d mem0=%d deficit=%d", candidateCorrect, mainCorrect, mem0Correct, deficit),
			fmt.Sprintf("deficit <= %d and candidate nonzero when a baseline is nonzero", gate.PerTypeMaxDeficit))
	}
	addLongMemEvalReplicateRatioCheck(&result, "candidate_memory_llm_tokens_vs_main",
		candidate.MemoryTokenUsage.TotalTokens, main.MemoryTokenUsage.TotalTokens,
		gate.MemoryLLMTokenRatioMaximum)
	addLongMemEvalReplicateRatioCheck(&result, "candidate_memory_embedding_tokens_vs_main",
		candidate.MemoryEmbeddingUsage.TotalTokens, main.MemoryEmbeddingUsage.TotalTokens,
		gate.MemoryEmbeddingTokenRatioMaximum)
	addLongMemEvalReplicateRatioCheck(&result, "candidate_final_memories_vs_main",
		candidate.FinalMemories, main.FinalMemories, gate.FinalMemoryCountRatioMaximum)
	return result
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
	result.Checks = append(result.Checks, lmeReplicateGateCheck{
		Name: name, Passed: passed, Actual: actual, Requirement: fmt.Sprintf("<= %.6f", maximum),
	})
	result.Passed = result.Passed && passed
}

func formatLongMemEvalReplicateComparisonTSV(comparison *lmeReplicateComparison) string {
	var b strings.Builder
	b.WriteString("question_id\tquestion_type\tmain_primary\tmain_correct_replicates\tmain_majority\tmem0_primary\tmem0_correct_replicates\tmem0_majority\tcandidate_primary\tcandidate_correct_replicates\tcandidate_majority\n")
	for _, item := range comparison.Cases {
		main := item.Arms[lmeReplicateArmPGVectorMain]
		mem0 := item.Arms[lmeReplicateArmMem0OSS]
		candidate := item.Arms[lmeReplicateArmPGVectorCandidate]
		fmt.Fprintf(&b, "%s\t%s\t%t\t%d\t%t\t%t\t%d\t%t\t%t\t%d\t%t\n",
			item.QuestionID, item.QuestionType,
			main.PrimaryCorrect, main.CorrectReplicates, main.MajorityCorrect,
			mem0.PrimaryCorrect, mem0.CorrectReplicates, mem0.MajorityCorrect,
			candidate.PrimaryCorrect, candidate.CorrectReplicates, candidate.MajorityCorrect)
	}
	return b.String()
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
	b.WriteString("| Arm | Primary | Majority | Correct replicates | Unstable | Memory LLM tokens | Embedding tokens | Memories |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, name := range []string{lmeReplicateArmPGVectorMain, lmeReplicateArmMem0OSS, lmeReplicateArmPGVectorCandidate} {
		arm := comparison.Arms[name]
		fmt.Fprintf(&b, "| %s | %d/%d | %d/%d | %d/%d | %d | %d | %d | %d |\n",
			name, arm.PrimaryCorrect, arm.Cases, arm.MajorityCorrect, arm.Cases,
			arm.CorrectReplicates, arm.TotalAnswerReplicates, arm.UnstableCases,
			arm.MemoryTokenUsage.TotalTokens, arm.MemoryEmbeddingUsage.TotalTokens, arm.FinalMemories)
	}
	b.WriteString("\n## Gate\n\n")
	for _, check := range comparison.Gate.Checks {
		mark := "x"
		if !check.Passed {
			mark = " "
		}
		fmt.Fprintf(&b, "- [%s] `%s`: %s; required %s\n", mark, check.Name, check.Actual, check.Requirement)
	}
	b.WriteString("\n## Cases\n\n")
	b.WriteString("| Question | Type | Main | Mem0 | Candidate |\n")
	b.WriteString("| --- | --- | ---: | ---: | ---: |\n")
	for _, item := range comparison.Cases {
		fmt.Fprintf(&b, "| `%s` | %s | %d/%d | %d/%d | %d/%d |\n",
			item.QuestionID, item.QuestionType,
			item.Arms[lmeReplicateArmPGVectorMain].CorrectReplicates, comparison.ReplicateCount,
			item.Arms[lmeReplicateArmMem0OSS].CorrectReplicates, comparison.ReplicateCount,
			item.Arms[lmeReplicateArmPGVectorCandidate].CorrectReplicates, comparison.ReplicateCount)
	}
	return b.String()
}
