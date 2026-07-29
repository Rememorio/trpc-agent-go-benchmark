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
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/scenarios"
)

const (
	locomoReplicateManifestSchemaVersion   = 1
	locomoReplicateComparisonSchemaVersion = 1
	locomoReplicateKindFresh               = "fresh"
	locomoReplicateKindFixedMemory         = "fixed-memory"
	locomoReplicateRoleMain                = "main"
	locomoReplicateRoleCandidate           = "candidate"
	locomoReplicateBootstrapSeed           = int64(20260730)
	locomoReplicateBootstrapResamples      = 10000
)

type locomoReplicateManifest struct {
	SchemaVersion int                       `json:"schema_version"`
	Experiment    string                    `json:"experiment"`
	Selection     string                    `json:"selection"`
	Protocol      locomoReplicateProtocol   `json:"protocol"`
	Arms          []locomoReplicateArmSpec  `json:"arms"`
	Gate          locomoReplicateGateConfig `json:"gate"`
}

type locomoReplicateProtocol struct {
	ExpectedSamples    int      `json:"expected_samples"`
	ExpectedQuestions  int      `json:"expected_questions"`
	ExpectedReplicates int      `json:"expected_replicates"`
	ExpectedSampleIDs  []string `json:"expected_sample_ids"`
	ExpectedCategories []string `json:"expected_categories"`
	BenchmarkRevision  string   `json:"benchmark_revision"`
	ReplayProtocol     string   `json:"replay_protocol"`
	RoleMapping        string   `json:"role_mapping"`
	Model              string   `json:"model"`
	ModelVariant       string   `json:"model_variant"`
	EvalModel          string   `json:"eval_model"`
	QAPromptVersion    string   `json:"qa_prompt_version"`
	QASearchStrategy   string   `json:"qa_search_strategy"`
	QASearchPasses     int      `json:"qa_search_passes"`
	VectorTopK         int      `json:"vector_topk"`
}

type locomoReplicateArmSpec struct {
	Name                        string                     `json:"name"`
	Role                        string                     `json:"role"`
	BuildProfile                string                     `json:"build_profile"`
	ModuleReplacementVersion    string                     `json:"module_replacement_version"`
	UpdatePolicy                string                     `json:"update_policy"`
	AssistantResultExtraction   bool                       `json:"assistant_result_extraction"`
	AssistantResultUpdatePolicy string                     `json:"assistant_result_update_policy,omitempty"`
	TableSuffix                 string                     `json:"table_suffix"`
	TableStats                  string                     `json:"table_stats"`
	MemorySnapshot              string                     `json:"memory_snapshot"`
	MemorySnapshotSHA256        string                     `json:"memory_snapshot_sha256"`
	AllowedExtractionOperations []string                   `json:"allowed_extraction_operations"`
	Replicates                  []locomoReplicateInputSpec `json:"replicates"`
}

type locomoReplicateInputSpec struct {
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	Results           string `json:"results"`
	SnapshotUnchanged string `json:"snapshot_unchanged,omitempty"`
}

type locomoReplicateGateConfig struct {
	OverallNoninferiorityMargin     float64 `json:"overall_noninferiority_margin"`
	PerCategoryNoninferiorityMargin float64 `json:"per_category_noninferiority_margin"`
	MemoryCountRatioMaximum         float64 `json:"memory_count_ratio_maximum"`
	ExtractionTokenRatioMaximum     float64 `json:"extraction_token_ratio_maximum"`
	EmbeddingTokenRatioMaximum      float64 `json:"embedding_token_ratio_maximum"`
	IngestDurationRatioMaximum      float64 `json:"ingest_duration_ratio_maximum"`
}

type locomoReplicateSelection struct {
	Questions []locomoReplicateSelectedQuestion `json:"questions"`
}

type locomoReplicateSelectedQuestion struct {
	QuestionID string `json:"question_id"`
}

type locomoReplicateTableStats struct {
	TotalRecords                    int `json:"total_records"`
	ActiveRecords                   int `json:"active_records"`
	DeletedRecords                  int `json:"deleted_records"`
	NormalizedDuplicateExtraRecords int `json:"normalized_duplicate_extra_records"`
}

type locomoLoadedReplicateArm struct {
	Spec                 locomoReplicateArmSpec
	Results              []*EvaluationResult
	ResultSHA256         []string
	TableStats           locomoReplicateTableStats
	TableStatsSHA        string
	SnapshotSHA          string
	MarkerSHA256         []string
	ModuleManifestSHA256 string
	ModuleSumSHA256      string
}

type locomoReplicateComparison struct {
	SchemaVersion   int                           `json:"schema_version"`
	CreatedAt       string                        `json:"created_at"`
	Experiment      string                        `json:"experiment"`
	ManifestSHA256  string                        `json:"manifest_sha256"`
	SelectionSHA256 string                        `json:"selection_sha256"`
	Inputs          []locomoReplicateInputAudit   `json:"inputs"`
	Arms            map[string]locomoReplicateArm `json:"arms"`
	Questions       []locomoReplicateQuestion     `json:"questions"`
	PairedBootstrap locomoReplicateBootstrap      `json:"paired_bootstrap"`
	Gate            locomoReplicateGateResult     `json:"gate"`
	Interpretation  string                        `json:"interpretation"`
}

type locomoReplicateInputAudit struct {
	Arm                  string   `json:"arm"`
	Role                 string   `json:"role"`
	ResultSHA256         []string `json:"result_sha256"`
	TableStatsSHA256     string   `json:"table_stats_sha256"`
	MemorySnapshotSHA256 string   `json:"memory_snapshot_sha256"`
	SnapshotMarkerSHA256 []string `json:"snapshot_marker_sha256"`
	ModuleManifestSHA256 string   `json:"module_manifest_sha256"`
	ModuleSumSHA256      string   `json:"module_sum_sha256"`
}

type locomoReplicateArm struct {
	Name                  string                               `json:"name"`
	Role                  string                               `json:"role"`
	PrimaryMeanF1         float64                              `json:"primary_mean_f1"`
	PrimaryMeanBLEU       float64                              `json:"primary_mean_bleu"`
	Replicates            []locomoReplicateMetric              `json:"replicates"`
	Categories            []locomoReplicateCategory            `json:"categories"`
	FreshExtraction       locomoReplicateExtraction            `json:"fresh_extraction"`
	Persistence           locomoReplicateTableStats            `json:"persistence"`
	ExtractionDiagnostics locomoReplicateExtractionDiagnostics `json:"extraction_diagnostics"`
}

type locomoReplicateMetric struct {
	Name             string  `json:"name"`
	Kind             string  `json:"kind"`
	OverallF1        float64 `json:"overall_f1"`
	OverallBLEU      float64 `json:"overall_bleu"`
	TotalTimeMs      int64   `json:"total_time_ms"`
	IngestDurationMs int64   `json:"ingest_duration_ms"`
	QADurationMs     int64   `json:"qa_duration_ms"`
	LLMCalls         int     `json:"llm_calls"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CachedTokens     int     `json:"cached_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	EmbeddingCalls   int     `json:"embedding_calls"`
	EmbeddingTokens  int     `json:"embedding_tokens"`
}

type locomoReplicateCategory struct {
	Category  string  `json:"category"`
	Questions int     `json:"questions"`
	MeanF1    float64 `json:"mean_f1"`
	MeanBLEU  float64 `json:"mean_bleu"`
}

type locomoReplicateExtraction struct {
	TokenUsage       scenarios.TokenUsage     `json:"token_usage"`
	EmbeddingUsage   scenarios.EmbeddingUsage `json:"embedding_usage"`
	IngestDurationMs int64                    `json:"ingest_duration_ms"`
}

type locomoReplicateExtractionDiagnostics struct {
	ModelCalls       int            `json:"model_calls"`
	ModelCallErrors  int            `json:"model_call_errors"`
	ToolCalls        int            `json:"tool_calls"`
	ToolCallsByName  map[string]int `json:"tool_calls_by_name"`
	CallsWithoutTool int            `json:"calls_without_tool"`
	SourceMessages   int            `json:"source_messages"`
}

type locomoReplicateQuestion struct {
	QuestionID           string    `json:"question_id"`
	Category             string    `json:"category"`
	MainMeanF1           float64   `json:"main_mean_f1"`
	CandidateMeanF1      float64   `json:"candidate_mean_f1"`
	F1Delta              float64   `json:"candidate_minus_main_f1"`
	MainF1               []float64 `json:"main_f1_by_replicate"`
	CandidateF1          []float64 `json:"candidate_f1_by_replicate"`
	MainSearchCalls      []int     `json:"main_search_calls"`
	CandidateSearchCalls []int     `json:"candidate_search_calls"`
}

type locomoReplicateBootstrap struct {
	Unit            string  `json:"unit"`
	QuestionCount   int     `json:"question_count"`
	Seed            int64   `json:"seed"`
	Resamples       int     `json:"resamples"`
	Confidence      float64 `json:"confidence"`
	PointEstimate   float64 `json:"point_estimate"`
	Lower           float64 `json:"lower"`
	Upper           float64 `json:"upper"`
	DescriptiveOnly bool    `json:"descriptive_only"`
}

type locomoReplicateGateResult struct {
	Passed          bool                       `json:"passed"`
	IntegrityPassed bool                       `json:"integrity_passed"`
	QualityPassed   bool                       `json:"quality_passed"`
	CostPassed      bool                       `json:"cost_passed"`
	Checks          []locomoReplicateGateCheck `json:"checks"`
}

type locomoReplicateGateCheck struct {
	Dimension   string `json:"dimension"`
	Name        string `json:"name"`
	Passed      bool   `json:"passed"`
	Actual      string `json:"actual"`
	Requirement string `json:"requirement"`
}

type locomoQuestionObservation struct {
	Category    string
	F1          []float64
	BLEU        []float64
	SearchCalls []int
}

func compareLoCoMoReplicates(manifestPath, outputDir string) error {
	manifest, manifestSHA, selectionSHA, arms, err :=
		loadLoCoMoReplicates(manifestPath)
	if err != nil {
		return err
	}
	comparison, err := aggregateLoCoMoReplicates(
		manifest, manifestSHA, selectionSHA, arms,
	)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	resultPath := filepath.Join(
		outputDir, "locomo_replicate_comparison.json",
	)
	if err := writeLoCoMoReplicateJSON(resultPath, comparison); err != nil {
		return err
	}
	badCasesPath := filepath.Join(
		outputDir, "locomo_replicate_bad_cases.tsv",
	)
	if err := os.WriteFile(
		badCasesPath, []byte(formatLoCoMoReplicateBadCases(comparison)), 0o600,
	); err != nil {
		return fmt.Errorf("write LoCoMo bad cases: %w", err)
	}
	fmt.Printf("LoCoMo replicate comparison: %s\n", resultPath)
	fmt.Printf("LoCoMo replicate bad cases: %s\n", badCasesPath)
	return nil
}

func loadLoCoMoReplicates(
	manifestPath string,
) (
	locomoReplicateManifest,
	string,
	string,
	[]locomoLoadedReplicateArm,
	error,
) {
	manifest, err := decodeLoCoMoReplicateManifest(manifestPath)
	if err != nil {
		return locomoReplicateManifest{}, "", "", nil, err
	}
	if err := validateLoCoMoReplicateManifest(manifest); err != nil {
		return locomoReplicateManifest{}, "", "", nil, err
	}
	manifestSHA, err := sha256File(manifestPath)
	if err != nil {
		return locomoReplicateManifest{}, "", "", nil, err
	}
	baseDir := filepath.Dir(manifestPath)
	selectionPath := resolveLoCoMoReplicatePath(baseDir, manifest.Selection)
	selection, selectionSHA, err := loadLoCoMoReplicateSelection(selectionPath)
	if err != nil {
		return locomoReplicateManifest{}, "", "", nil, err
	}
	expectedIDs, err := validateLoCoMoReplicateSelection(
		selection, manifest.Protocol.ExpectedQuestions,
	)
	if err != nil {
		return locomoReplicateManifest{}, "", "", nil, err
	}
	arms := make([]locomoLoadedReplicateArm, 0, len(manifest.Arms))
	for _, armSpec := range manifest.Arms {
		arm, err := loadLoCoMoReplicateArm(
			baseDir, manifest.Protocol, expectedIDs, armSpec,
		)
		if err != nil {
			return locomoReplicateManifest{}, "", "", nil, err
		}
		arms = append(arms, arm)
	}
	return manifest, manifestSHA, selectionSHA, arms, nil
}

func decodeLoCoMoReplicateManifest(
	path string,
) (locomoReplicateManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return locomoReplicateManifest{}, fmt.Errorf(
			"open LoCoMo replicate manifest: %w", err,
		)
	}
	defer file.Close()
	var manifest locomoReplicateManifest
	decoder := json.NewDecoder(bufio.NewReader(file))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return locomoReplicateManifest{}, fmt.Errorf(
			"decode LoCoMo replicate manifest: %w", err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return locomoReplicateManifest{}, fmt.Errorf(
			"decode LoCoMo replicate manifest: trailing JSON value",
		)
	}
	return manifest, nil
}

func validateLoCoMoReplicateManifest(
	manifest locomoReplicateManifest,
) error {
	if manifest.SchemaVersion != locomoReplicateManifestSchemaVersion {
		return fmt.Errorf(
			"LoCoMo replicate manifest schema version = %d, want %d",
			manifest.SchemaVersion, locomoReplicateManifestSchemaVersion,
		)
	}
	if strings.TrimSpace(manifest.Experiment) == "" {
		return fmt.Errorf("LoCoMo replicate experiment is empty")
	}
	if strings.TrimSpace(manifest.Selection) == "" {
		return fmt.Errorf("LoCoMo replicate selection is empty")
	}
	protocol := manifest.Protocol
	if protocol.ExpectedSamples <= 0 ||
		protocol.ExpectedQuestions <= 0 ||
		protocol.ExpectedReplicates != 3 ||
		len(protocol.ExpectedSampleIDs) != protocol.ExpectedSamples ||
		len(protocol.ExpectedCategories) == 0 ||
		strings.TrimSpace(protocol.BenchmarkRevision) == "" ||
		strings.TrimSpace(protocol.ReplayProtocol) == "" ||
		strings.TrimSpace(protocol.RoleMapping) == "" ||
		strings.TrimSpace(protocol.Model) == "" ||
		strings.TrimSpace(protocol.EvalModel) == "" ||
		strings.TrimSpace(protocol.QAPromptVersion) == "" ||
		strings.TrimSpace(protocol.QASearchStrategy) == "" ||
		protocol.QASearchPasses <= 0 ||
		protocol.VectorTopK <= 0 {
		return fmt.Errorf("LoCoMo replicate protocol is incomplete")
	}
	if hasDuplicateStrings(protocol.ExpectedSampleIDs) ||
		hasDuplicateStrings(protocol.ExpectedCategories) {
		return fmt.Errorf("LoCoMo replicate protocol contains duplicate values")
	}
	if len(manifest.Arms) != 2 {
		return fmt.Errorf(
			"LoCoMo replicate arm count = %d, want 2", len(manifest.Arms),
		)
	}
	roles := map[string]bool{}
	names := map[string]bool{}
	for _, arm := range manifest.Arms {
		if err := validateLoCoMoReplicateArmSpec(
			arm, protocol.ExpectedReplicates,
		); err != nil {
			return err
		}
		if roles[arm.Role] {
			return fmt.Errorf("duplicate LoCoMo replicate role: %s", arm.Role)
		}
		if names[arm.Name] {
			return fmt.Errorf("duplicate LoCoMo replicate arm: %s", arm.Name)
		}
		roles[arm.Role] = true
		names[arm.Name] = true
	}
	if !roles[locomoReplicateRoleMain] ||
		!roles[locomoReplicateRoleCandidate] {
		return fmt.Errorf("LoCoMo replicate arms must include main and candidate")
	}
	gate := manifest.Gate
	if gate.OverallNoninferiorityMargin < 0 ||
		gate.PerCategoryNoninferiorityMargin < 0 ||
		gate.MemoryCountRatioMaximum <= 0 ||
		gate.ExtractionTokenRatioMaximum <= 0 ||
		gate.EmbeddingTokenRatioMaximum <= 0 ||
		gate.IngestDurationRatioMaximum <= 0 {
		return fmt.Errorf("LoCoMo replicate gate is invalid")
	}
	return nil
}

func validateLoCoMoReplicateArmSpec(
	arm locomoReplicateArmSpec,
	expectedReplicates int,
) error {
	if strings.TrimSpace(arm.Name) == "" ||
		(arm.Role != locomoReplicateRoleMain &&
			arm.Role != locomoReplicateRoleCandidate) ||
		strings.TrimSpace(arm.BuildProfile) == "" ||
		strings.TrimSpace(arm.ModuleReplacementVersion) == "" ||
		strings.TrimSpace(arm.UpdatePolicy) == "" ||
		strings.TrimSpace(arm.TableSuffix) == "" ||
		strings.TrimSpace(arm.TableStats) == "" ||
		strings.TrimSpace(arm.MemorySnapshot) == "" ||
		strings.TrimSpace(arm.MemorySnapshotSHA256) == "" ||
		len(arm.AllowedExtractionOperations) == 0 {
		return fmt.Errorf("LoCoMo replicate arm %q is incomplete", arm.Name)
	}
	if len(arm.Replicates) != expectedReplicates {
		return fmt.Errorf(
			"LoCoMo replicate arm %q count = %d, want %d",
			arm.Name, len(arm.Replicates), expectedReplicates,
		)
	}
	if arm.Replicates[0].Kind != locomoReplicateKindFresh {
		return fmt.Errorf("LoCoMo replicate arm %q starts without a fresh run", arm.Name)
	}
	seenNames := map[string]bool{}
	for index, replicate := range arm.Replicates {
		if strings.TrimSpace(replicate.Name) == "" ||
			strings.TrimSpace(replicate.Results) == "" {
			return fmt.Errorf(
				"LoCoMo replicate arm %q has an incomplete replicate",
				arm.Name,
			)
		}
		if seenNames[replicate.Name] {
			return fmt.Errorf(
				"LoCoMo replicate arm %q has duplicate replicate %q",
				arm.Name, replicate.Name,
			)
		}
		seenNames[replicate.Name] = true
		if index == 0 {
			if strings.TrimSpace(replicate.SnapshotUnchanged) != "" {
				return fmt.Errorf(
					"fresh LoCoMo replicate %q has a snapshot marker",
					replicate.Name,
				)
			}
			continue
		}
		if replicate.Kind != locomoReplicateKindFixedMemory ||
			strings.TrimSpace(replicate.SnapshotUnchanged) == "" {
			return fmt.Errorf(
				"LoCoMo replicate %q must be fixed-memory with a snapshot marker",
				replicate.Name,
			)
		}
	}
	if hasDuplicateStrings(arm.AllowedExtractionOperations) {
		return fmt.Errorf(
			"LoCoMo replicate arm %q has duplicate allowed operations",
			arm.Name,
		)
	}
	return nil
}

func loadLoCoMoReplicateSelection(
	path string,
) (locomoReplicateSelection, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return locomoReplicateSelection{}, "", fmt.Errorf(
			"read LoCoMo replicate selection: %w", err,
		)
	}
	var selection locomoReplicateSelection
	if err := json.Unmarshal(data, &selection); err != nil {
		return locomoReplicateSelection{}, "", fmt.Errorf(
			"decode LoCoMo replicate selection: %w", err,
		)
	}
	digest, err := sha256File(path)
	if err != nil {
		return locomoReplicateSelection{}, "", err
	}
	return selection, digest, nil
}

func validateLoCoMoReplicateSelection(
	selection locomoReplicateSelection,
	expectedQuestions int,
) (map[string]bool, error) {
	if len(selection.Questions) != expectedQuestions {
		return nil, fmt.Errorf(
			"LoCoMo selection questions = %d, want %d",
			len(selection.Questions), expectedQuestions,
		)
	}
	ids := make(map[string]bool, len(selection.Questions))
	for _, question := range selection.Questions {
		id := strings.TrimSpace(question.QuestionID)
		if id == "" {
			return nil, fmt.Errorf("LoCoMo selection contains an empty question ID")
		}
		if ids[id] {
			return nil, fmt.Errorf("LoCoMo selection contains duplicate question %q", id)
		}
		ids[id] = true
	}
	return ids, nil
}

func loadLoCoMoReplicateArm(
	baseDir string,
	protocol locomoReplicateProtocol,
	expectedIDs map[string]bool,
	spec locomoReplicateArmSpec,
) (locomoLoadedReplicateArm, error) {
	arm := locomoLoadedReplicateArm{Spec: spec}
	tableStatsPath := resolveLoCoMoReplicatePath(baseDir, spec.TableStats)
	if err := readLoCoMoReplicateJSON(
		tableStatsPath, &arm.TableStats,
	); err != nil {
		return locomoLoadedReplicateArm{}, fmt.Errorf(
			"load LoCoMo table stats for %s: %w", spec.Name, err,
		)
	}
	if arm.TableStats.TotalRecords <= 0 ||
		arm.TableStats.ActiveRecords <= 0 ||
		arm.TableStats.ActiveRecords+arm.TableStats.DeletedRecords !=
			arm.TableStats.TotalRecords ||
		arm.TableStats.NormalizedDuplicateExtraRecords < 0 {
		return locomoLoadedReplicateArm{}, fmt.Errorf(
			"invalid LoCoMo table stats for %s", spec.Name,
		)
	}
	var err error
	arm.TableStatsSHA, err = sha256File(tableStatsPath)
	if err != nil {
		return locomoLoadedReplicateArm{}, err
	}
	snapshotPath := resolveLoCoMoReplicatePath(
		baseDir, spec.MemorySnapshot,
	)
	snapshotDigest, err := sha256File(snapshotPath)
	if err != nil {
		return locomoLoadedReplicateArm{}, fmt.Errorf(
			"hash LoCoMo memory snapshot for %s: %w", spec.Name, err,
		)
	}
	snapshotSHAPath := resolveLoCoMoReplicatePath(
		baseDir, spec.MemorySnapshotSHA256,
	)
	arm.SnapshotSHA, err = readLoCoMoSnapshotSHA(snapshotSHAPath)
	if err != nil {
		return locomoLoadedReplicateArm{}, fmt.Errorf(
			"load LoCoMo memory snapshot digest for %s: %w", spec.Name, err,
		)
	}
	if snapshotDigest != arm.SnapshotSHA {
		return locomoLoadedReplicateArm{}, fmt.Errorf(
			"LoCoMo memory snapshot digest mismatch for %s", spec.Name,
		)
	}
	for index, replicate := range spec.Replicates {
		resultPath := resolveLoCoMoReplicatePath(baseDir, replicate.Results)
		var result EvaluationResult
		if err := readLoCoMoReplicateJSON(resultPath, &result); err != nil {
			return locomoLoadedReplicateArm{}, fmt.Errorf(
				"load LoCoMo %s/%s: %w",
				spec.Name, replicate.Name, err,
			)
		}
		if err := validateLoCoMoReplicateResult(
			protocol, expectedIDs, spec, replicate, &result,
		); err != nil {
			return locomoLoadedReplicateArm{}, fmt.Errorf(
				"validate LoCoMo %s/%s: %w",
				spec.Name, replicate.Name, err,
			)
		}
		if index == 0 {
			arm.ModuleManifestSHA256 =
				result.Metadata.Build.ModuleManifestSHA256
			arm.ModuleSumSHA256 = result.Metadata.Build.ModuleSumSHA256
		} else if result.Metadata.Build.ModuleManifestSHA256 !=
			arm.ModuleManifestSHA256 ||
			result.Metadata.Build.ModuleSumSHA256 != arm.ModuleSumSHA256 {
			return locomoLoadedReplicateArm{}, fmt.Errorf(
				"LoCoMo build manifest changed for %s/%s",
				spec.Name, replicate.Name,
			)
		}
		digest, err := sha256File(resultPath)
		if err != nil {
			return locomoLoadedReplicateArm{}, err
		}
		arm.Results = append(arm.Results, &result)
		arm.ResultSHA256 = append(arm.ResultSHA256, digest)
		if index == 0 {
			continue
		}
		markerPath := resolveLoCoMoReplicatePath(
			baseDir, replicate.SnapshotUnchanged,
		)
		var unchanged bool
		if err := readLoCoMoReplicateJSON(markerPath, &unchanged); err != nil {
			return locomoLoadedReplicateArm{}, fmt.Errorf(
				"load LoCoMo snapshot marker for %s/%s: %w",
				spec.Name, replicate.Name, err,
			)
		}
		if !unchanged {
			return locomoLoadedReplicateArm{}, fmt.Errorf(
				"LoCoMo snapshot changed for %s/%s",
				spec.Name, replicate.Name,
			)
		}
		digest, err = sha256File(markerPath)
		if err != nil {
			return locomoLoadedReplicateArm{}, err
		}
		arm.MarkerSHA256 = append(arm.MarkerSHA256, digest)
	}
	return arm, nil
}

func validateLoCoMoReplicateResult(
	protocol locomoReplicateProtocol,
	expectedIDs map[string]bool,
	arm locomoReplicateArmSpec,
	replicate locomoReplicateInputSpec,
	result *EvaluationResult,
) error {
	if result == nil || result.Metadata == nil || result.Summary == nil {
		return fmt.Errorf("result metadata or summary is missing")
	}
	metadata := result.Metadata
	summary := result.Summary
	if metadata.Framework != "trpc-agent-go" ||
		metadata.Model != protocol.Model ||
		metadata.ModelVariant != protocol.ModelVariant ||
		metadata.EvalModel != protocol.EvalModel ||
		metadata.Scenario != string(scenarios.ScenarioAuto) ||
		metadata.MemoryBackend != "pgvector" ||
		metadata.QAPromptVersion != protocol.QAPromptVersion ||
		metadata.QASearchStrategy != protocol.QASearchStrategy ||
		metadata.QASearchPasses != protocol.QASearchPasses ||
		metadata.VectorTopK != protocol.VectorTopK ||
		metadata.ReplayProtocol != protocol.ReplayProtocol ||
		metadata.RoleMapping != protocol.RoleMapping ||
		metadata.TableSuffix != arm.TableSuffix ||
		metadata.LLMJudge {
		return fmt.Errorf("result protocol metadata does not match the manifest")
	}
	if metadata.Build.Revision != protocol.BenchmarkRevision ||
		metadata.Build.Modified ||
		metadata.Build.BuildProfile != arm.BuildProfile ||
		!validSHA256(metadata.Build.ModuleManifestSHA256) ||
		!validSHA256(metadata.Build.ModuleSumSHA256) {
		return fmt.Errorf("result build provenance does not match the manifest")
	}
	for _, modulePath := range []string{
		"trpc.group/trpc-go/trpc-agent-go",
		"trpc.group/trpc-go/trpc-agent-go/memory/pgvector",
	} {
		module, ok := metadata.Build.Modules[modulePath]
		if !ok ||
			module.ReplacementVersion != arm.ModuleReplacementVersion ||
			module.LocalReplacement {
			return fmt.Errorf(
				"result module %s does not match the manifest", modulePath,
			)
		}
	}
	if metadata.PGVectorExtraction == nil ||
		string(metadata.PGVectorExtraction.UpdatePolicy) != arm.UpdatePolicy ||
		metadata.PGVectorExtraction.AssistantResultExtraction !=
			arm.AssistantResultExtraction ||
		metadata.PGVectorExtraction.AssistantResultUpdatePolicy !=
			arm.AssistantResultUpdatePolicy {
		return fmt.Errorf("result extraction configuration does not match the manifest")
	}
	if summary.TotalSamples != protocol.ExpectedSamples ||
		summary.FailedSamples != 0 ||
		summary.TotalQuestions != protocol.ExpectedQuestions ||
		summary.ProtocolViolations != 0 ||
		summary.TotalTimeMs <= 0 ||
		len(result.Failures) != 0 {
		return fmt.Errorf("result summary is incomplete")
	}
	var sampleIngestDurationMs int64
	var sampleQADurationMs int64
	for _, sample := range result.SampleResults {
		if sample == nil {
			continue
		}
		sampleIngestDurationMs += sample.IngestDurationMs
		sampleQADurationMs += sample.QADurationMs
	}
	if summary.IngestDurationMs != sampleIngestDurationMs ||
		summary.QADurationMs != sampleQADurationMs {
		return fmt.Errorf(
			"phase duration rollup mismatch: ingest=%d/%d QA=%d/%d",
			summary.IngestDurationMs, sampleIngestDurationMs,
			summary.QADurationMs, sampleQADurationMs,
		)
	}
	if err := validateLoCoMoUsageRollups(result); err != nil {
		return err
	}
	reuse := replicate.Kind == locomoReplicateKindFixedMemory
	if metadata.ReuseMemories != reuse {
		return fmt.Errorf(
			"reuse_memories = %t, want %t", metadata.ReuseMemories, reuse,
		)
	}
	if reuse {
		if !loCoMoTokenUsageIsZero(summary.ExtractionTokenUsage) ||
			!loCoMoEmbeddingUsageIsZero(summary.ExtractionEmbeddingUsage) ||
			summary.IngestDurationMs != 0 {
			return fmt.Errorf("fixed-memory replicate performed ingestion")
		}
	} else {
		if summary.ExtractionTokenUsage == nil ||
			summary.ExtractionTokenUsage.LLMCalls <= 0 ||
			summary.ExtractionEmbeddingUsage == nil ||
			summary.ExtractionEmbeddingUsage.Calls <= 0 ||
			summary.IngestDurationMs <= 0 {
			return fmt.Errorf("fresh replicate has incomplete ingestion usage")
		}
	}
	if summary.QATokenUsage == nil ||
		summary.QATokenUsage.LLMCalls <= 0 ||
		summary.QAEmbeddingUsage == nil ||
		summary.QAEmbeddingUsage.Calls <= 0 ||
		summary.QADurationMs <= 0 {
		return fmt.Errorf("replicate has incomplete QA usage")
	}
	return validateLoCoMoReplicateCases(
		protocol, expectedIDs, arm, reuse, result.SampleResults,
	)
}

func validateLoCoMoReplicateCases(
	protocol locomoReplicateProtocol,
	expectedIDs map[string]bool,
	arm locomoReplicateArmSpec,
	reuse bool,
	samples []*scenarios.SampleResult,
) error {
	if len(samples) != protocol.ExpectedSamples {
		return fmt.Errorf(
			"sample result count = %d, want %d",
			len(samples), protocol.ExpectedSamples,
		)
	}
	expectedSamples := make(map[string]bool, len(protocol.ExpectedSampleIDs))
	for _, sampleID := range protocol.ExpectedSampleIDs {
		expectedSamples[sampleID] = true
	}
	seenSamples := map[string]bool{}
	seenQuestions := map[string]bool{}
	categoryCounts := map[string]int{}
	allowedOperations := make(
		map[string]bool, len(arm.AllowedExtractionOperations),
	)
	for _, operation := range arm.AllowedExtractionOperations {
		allowedOperations[operation] = true
	}
	for _, sample := range samples {
		if sample == nil || !expectedSamples[sample.SampleID] ||
			seenSamples[sample.SampleID] {
			return fmt.Errorf("unexpected or duplicate sample result")
		}
		seenSamples[sample.SampleID] = true
		if reuse {
			if len(sample.ExtractionCalls) != 0 ||
				sample.IngestDurationMs != 0 ||
				!loCoMoTokenUsageIsZero(sample.ExtractionTokenUsage) ||
				!loCoMoEmbeddingUsageIsZero(
					sample.ExtractionEmbeddingUsage,
				) {
				return fmt.Errorf(
					"fixed-memory sample %s performed ingestion",
					sample.SampleID,
				)
			}
		} else {
			if sample.IngestDurationMs <= 0 ||
				len(sample.ExtractionCalls) == 0 {
				return fmt.Errorf(
					"fresh sample %s lacks ingestion diagnostics",
					sample.SampleID,
				)
			}
			for _, call := range sample.ExtractionCalls {
				if strings.TrimSpace(call.Error) != "" {
					return fmt.Errorf(
						"sample %s has an extraction call error",
						sample.SampleID,
					)
				}
				for _, toolCall := range call.ToolCalls {
					if !allowedOperations[toolCall.Name] {
						return fmt.Errorf(
							"sample %s used unregistered operation %q",
							sample.SampleID, toolCall.Name,
						)
					}
				}
			}
		}
		for _, qa := range sample.QAResults {
			if qa == nil || !expectedIDs[qa.QuestionID] ||
				seenQuestions[qa.QuestionID] {
				return fmt.Errorf("unexpected or duplicate QA result")
			}
			if !containsString(
				protocol.ExpectedCategories, qa.Category,
			) {
				return fmt.Errorf(
					"question %s has unexpected category %q",
					qa.QuestionID, qa.Category,
				)
			}
			if strings.TrimSpace(qa.ProtocolError) != "" ||
				qa.SearchCalls != protocol.QASearchPasses ||
				qa.TokenUsage == nil ||
				qa.TokenUsage.LLMCalls <= 0 ||
				!finiteUnitInterval(qa.Metrics.F1) ||
				!finiteUnitInterval(qa.Metrics.BLEU) {
				return fmt.Errorf(
					"question %s has incomplete QA diagnostics",
					qa.QuestionID,
				)
			}
			for _, step := range qa.Steps {
				if strings.TrimSpace(step.Error) != "" {
					return fmt.Errorf(
						"question %s has a QA step error", qa.QuestionID,
					)
				}
			}
			seenQuestions[qa.QuestionID] = true
			categoryCounts[qa.Category]++
		}
	}
	if len(seenSamples) != len(expectedSamples) ||
		len(seenQuestions) != len(expectedIDs) {
		return fmt.Errorf("sample or question coverage is incomplete")
	}
	expectedPerCategory := protocol.ExpectedQuestions /
		len(protocol.ExpectedCategories)
	if expectedPerCategory*len(protocol.ExpectedCategories) !=
		protocol.ExpectedQuestions {
		return fmt.Errorf("questions are not divisible by category count")
	}
	for _, category := range protocol.ExpectedCategories {
		if categoryCounts[category] != expectedPerCategory {
			return fmt.Errorf(
				"category %s questions = %d, want %d",
				category, categoryCounts[category], expectedPerCategory,
			)
		}
	}
	return nil
}

func readLoCoMoReplicateJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	return nil
}

func readLoCoMoSnapshotSHA(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 || !validSHA256(fields[0]) {
		return "", fmt.Errorf("invalid SHA-256 file")
	}
	return fields[0], nil
}

func resolveLoCoMoReplicatePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(baseDir, filepath.Clean(path))
}

func loCoMoTokenUsageIsZero(usage *scenarios.TokenUsage) bool {
	return usage == nil || usage.IsZero()
}

func loCoMoEmbeddingUsageIsZero(
	usage *scenarios.EmbeddingUsage,
) bool {
	return usage == nil || usage.IsZero()
}

func validateLoCoMoUsageRollups(result *EvaluationResult) error {
	var extractionTokens scenarios.TokenUsage
	var qaTokens scenarios.TokenUsage
	var extractionEmbeddings scenarios.EmbeddingUsage
	var qaEmbeddings scenarios.EmbeddingUsage
	for _, sample := range result.SampleResults {
		if sample == nil ||
			sample.TokenUsage == nil ||
			sample.QATokenUsage == nil ||
			sample.EmbeddingUsage == nil ||
			sample.QAEmbeddingUsage == nil {
			return fmt.Errorf("sample usage rollup is missing")
		}
		var qaTokenSum scenarios.TokenUsage
		for _, qa := range sample.QAResults {
			if qa == nil || qa.TokenUsage == nil {
				return fmt.Errorf("question token usage is missing")
			}
			qaTokenSum.Add(*qa.TokenUsage)
		}
		if qaTokenSum != *sample.QATokenUsage {
			return fmt.Errorf("sample QA token usage rollup mismatch")
		}
		sampleTotalTokens := valueOrZero(sample.ExtractionTokenUsage)
		sampleTotalTokens.Add(*sample.QATokenUsage)
		if sampleTotalTokens != *sample.TokenUsage {
			return fmt.Errorf("sample total token usage rollup mismatch")
		}
		sampleTotalEmbeddings := embeddingValueOrZero(
			sample.ExtractionEmbeddingUsage,
		)
		sampleTotalEmbeddings.Add(*sample.QAEmbeddingUsage)
		if sampleTotalEmbeddings != *sample.EmbeddingUsage {
			return fmt.Errorf("sample embedding usage rollup mismatch")
		}
		extractionTokens.Add(valueOrZero(sample.ExtractionTokenUsage))
		qaTokens.Add(*sample.QATokenUsage)
		extractionEmbeddings.Add(embeddingValueOrZero(
			sample.ExtractionEmbeddingUsage,
		))
		qaEmbeddings.Add(*sample.QAEmbeddingUsage)
	}
	summary := result.Summary
	if extractionTokens != valueOrZero(summary.ExtractionTokenUsage) ||
		qaTokens != valueOrZero(summary.QATokenUsage) ||
		extractionEmbeddings !=
			embeddingValueOrZero(summary.ExtractionEmbeddingUsage) ||
		qaEmbeddings != embeddingValueOrZero(summary.QAEmbeddingUsage) {
		return fmt.Errorf("summary phase usage rollup mismatch")
	}
	totalTokens := extractionTokens
	totalTokens.Add(qaTokens)
	totalEmbeddings := extractionEmbeddings
	totalEmbeddings.Add(qaEmbeddings)
	if summary.TokenUsage == nil ||
		*summary.TokenUsage != totalTokens ||
		summary.EmbeddingUsage == nil ||
		*summary.EmbeddingUsage != totalEmbeddings ||
		summary.TotalPromptTokens != totalTokens.PromptTokens ||
		summary.TotalCompletionTokens != totalTokens.CompletionTokens ||
		summary.TotalTokens != totalTokens.TotalTokens ||
		summary.TotalCachedTokens != totalTokens.CachedPromptTokens() ||
		summary.TotalLLMCalls != totalTokens.LLMCalls {
		return fmt.Errorf("summary total usage rollup mismatch")
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func finiteUnitInterval(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) &&
		value >= 0 && value <= 1
}

func hasDuplicateStrings(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func aggregateLoCoMoReplicates(
	manifest locomoReplicateManifest,
	manifestSHA string,
	selectionSHA string,
	loaded []locomoLoadedReplicateArm,
) (*locomoReplicateComparison, error) {
	armSummaries := make(
		map[string]locomoReplicateArm, len(loaded),
	)
	observations := make(
		map[string]map[string]locomoQuestionObservation, len(loaded),
	)
	inputs := make([]locomoReplicateInputAudit, 0, len(loaded))
	for _, arm := range loaded {
		armObservations, err := collectLoCoMoQuestionObservations(arm)
		if err != nil {
			return nil, err
		}
		observations[arm.Spec.Role] = armObservations
		summary, err := summarizeLoCoMoReplicateArm(
			manifest.Protocol, arm, armObservations,
		)
		if err != nil {
			return nil, err
		}
		armSummaries[arm.Spec.Role] = summary
		inputs = append(inputs, locomoReplicateInputAudit{
			Arm:                  arm.Spec.Name,
			Role:                 arm.Spec.Role,
			ResultSHA256:         arm.ResultSHA256,
			TableStatsSHA256:     arm.TableStatsSHA,
			MemorySnapshotSHA256: arm.SnapshotSHA,
			SnapshotMarkerSHA256: arm.MarkerSHA256,
			ModuleManifestSHA256: arm.ModuleManifestSHA256,
			ModuleSumSHA256:      arm.ModuleSumSHA256,
		})
	}
	mainObservations := observations[locomoReplicateRoleMain]
	candidateObservations := observations[locomoReplicateRoleCandidate]
	questionIDs := make([]string, 0, len(mainObservations))
	for questionID := range mainObservations {
		questionIDs = append(questionIDs, questionID)
	}
	sort.Strings(questionIDs)
	questions := make([]locomoReplicateQuestion, 0, len(questionIDs))
	differences := make([]float64, 0, len(questionIDs))
	for _, questionID := range questionIDs {
		mainQuestion := mainObservations[questionID]
		candidateQuestion, ok := candidateObservations[questionID]
		if !ok || candidateQuestion.Category != mainQuestion.Category {
			return nil, fmt.Errorf(
				"LoCoMo candidate question %s is missing or changed category",
				questionID,
			)
		}
		mainMean := meanFloat64(mainQuestion.F1)
		candidateMean := meanFloat64(candidateQuestion.F1)
		delta := candidateMean - mainMean
		differences = append(differences, delta)
		questions = append(questions, locomoReplicateQuestion{
			QuestionID:           questionID,
			Category:             mainQuestion.Category,
			MainMeanF1:           mainMean,
			CandidateMeanF1:      candidateMean,
			F1Delta:              delta,
			MainF1:               mainQuestion.F1,
			CandidateF1:          candidateQuestion.F1,
			MainSearchCalls:      mainQuestion.SearchCalls,
			CandidateSearchCalls: candidateQuestion.SearchCalls,
		})
	}
	bootstrap := bootstrapLoCoMoDifferences(differences)
	gate := evaluateLoCoMoReplicateGate(
		manifest,
		armSummaries[locomoReplicateRoleMain],
		armSummaries[locomoReplicateRoleCandidate],
	)
	return &locomoReplicateComparison{
		SchemaVersion:   locomoReplicateComparisonSchemaVersion,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Experiment:      manifest.Experiment,
		ManifestSHA256:  manifestSHA,
		SelectionSHA256: selectionSHA,
		Inputs:          inputs,
		Arms:            armSummaries,
		Questions:       questions,
		PairedBootstrap: bootstrap,
		Gate:            gate,
		Interpretation: "This historically exposed cross-dataset regression " +
			"may veto a candidate but cannot promote or tune one.",
	}, nil
}

func collectLoCoMoQuestionObservations(
	arm locomoLoadedReplicateArm,
) (map[string]locomoQuestionObservation, error) {
	observations := map[string]locomoQuestionObservation{}
	for replicateIndex, result := range arm.Results {
		seen := map[string]bool{}
		for _, sample := range result.SampleResults {
			for _, qa := range sample.QAResults {
				if seen[qa.QuestionID] {
					return nil, fmt.Errorf(
						"LoCoMo %s replicate %d duplicates question %s",
						arm.Spec.Name, replicateIndex+1, qa.QuestionID,
					)
				}
				seen[qa.QuestionID] = true
				observation := observations[qa.QuestionID]
				if observation.Category != "" &&
					observation.Category != qa.Category {
					return nil, fmt.Errorf(
						"LoCoMo %s changes category for question %s",
						arm.Spec.Name, qa.QuestionID,
					)
				}
				observation.Category = qa.Category
				observation.F1 = append(
					observation.F1, qa.Metrics.F1,
				)
				observation.BLEU = append(
					observation.BLEU, qa.Metrics.BLEU,
				)
				observation.SearchCalls = append(
					observation.SearchCalls, qa.SearchCalls,
				)
				observations[qa.QuestionID] = observation
			}
		}
	}
	for questionID, observation := range observations {
		if len(observation.F1) != len(arm.Results) {
			return nil, fmt.Errorf(
				"LoCoMo %s question %s observations = %d, want %d",
				arm.Spec.Name, questionID, len(observation.F1),
				len(arm.Results),
			)
		}
	}
	return observations, nil
}

func summarizeLoCoMoReplicateArm(
	protocol locomoReplicateProtocol,
	arm locomoLoadedReplicateArm,
	observations map[string]locomoQuestionObservation,
) (locomoReplicateArm, error) {
	metricsByReplicate := make(
		[]locomoReplicateMetric, 0, len(arm.Results),
	)
	for index, result := range arm.Results {
		tokenUsage := valueOrZero(result.Summary.TokenUsage)
		embeddingUsage := embeddingValueOrZero(
			result.Summary.EmbeddingUsage,
		)
		metricsByReplicate = append(
			metricsByReplicate,
			locomoReplicateMetric{
				Name:             arm.Spec.Replicates[index].Name,
				Kind:             arm.Spec.Replicates[index].Kind,
				OverallF1:        result.Summary.OverallF1,
				OverallBLEU:      result.Summary.OverallBLEU,
				TotalTimeMs:      result.Summary.TotalTimeMs,
				IngestDurationMs: result.Summary.IngestDurationMs,
				QADurationMs:     result.Summary.QADurationMs,
				LLMCalls:         tokenUsage.LLMCalls,
				PromptTokens:     tokenUsage.PromptTokens,
				CompletionTokens: tokenUsage.CompletionTokens,
				CachedTokens:     tokenUsage.CachedPromptTokens(),
				TotalTokens:      tokenUsage.TotalTokens,
				EmbeddingCalls:   embeddingUsage.Calls,
				EmbeddingTokens:  embeddingUsage.TotalTokens,
			},
		)
	}
	categories := make(
		[]locomoReplicateCategory, 0,
		len(protocol.ExpectedCategories),
	)
	for _, category := range protocol.ExpectedCategories {
		var f1Values []float64
		var bleuValues []float64
		for _, observation := range observations {
			if observation.Category != category {
				continue
			}
			f1Values = append(f1Values, meanFloat64(observation.F1))
			bleuValues = append(bleuValues, meanFloat64(observation.BLEU))
		}
		if len(f1Values) == 0 {
			return locomoReplicateArm{}, fmt.Errorf(
				"LoCoMo %s category %s is empty",
				arm.Spec.Name, category,
			)
		}
		categories = append(categories, locomoReplicateCategory{
			Category:  category,
			Questions: len(f1Values),
			MeanF1:    meanFloat64(f1Values),
			MeanBLEU:  meanFloat64(bleuValues),
		})
	}
	allF1 := make([]float64, 0, len(observations))
	allBLEU := make([]float64, 0, len(observations))
	for _, observation := range observations {
		allF1 = append(allF1, meanFloat64(observation.F1))
		allBLEU = append(allBLEU, meanFloat64(observation.BLEU))
	}
	fresh := arm.Results[0]
	return locomoReplicateArm{
		Name:            arm.Spec.Name,
		Role:            arm.Spec.Role,
		PrimaryMeanF1:   meanFloat64(allF1),
		PrimaryMeanBLEU: meanFloat64(allBLEU),
		Replicates:      metricsByReplicate,
		Categories:      categories,
		FreshExtraction: locomoReplicateExtraction{
			TokenUsage: valueOrZero(
				fresh.Summary.ExtractionTokenUsage,
			),
			EmbeddingUsage: embeddingValueOrZero(
				fresh.Summary.ExtractionEmbeddingUsage,
			),
			IngestDurationMs: fresh.Summary.IngestDurationMs,
		},
		Persistence: arm.TableStats,
		ExtractionDiagnostics: summarizeLoCoMoExtractionDiagnostics(
			fresh.SampleResults,
		),
	}, nil
}

func summarizeLoCoMoExtractionDiagnostics(
	samples []*scenarios.SampleResult,
) locomoReplicateExtractionDiagnostics {
	diagnostics := locomoReplicateExtractionDiagnostics{
		ToolCallsByName: map[string]int{},
	}
	for _, sample := range samples {
		if sample == nil {
			continue
		}
		for _, call := range sample.ExtractionCalls {
			diagnostics.ModelCalls++
			if strings.TrimSpace(call.Error) != "" {
				diagnostics.ModelCallErrors++
			}
			diagnostics.SourceMessages += len(call.SourceMessages)
			if len(call.ToolCalls) == 0 {
				diagnostics.CallsWithoutTool++
			}
			for _, toolCall := range call.ToolCalls {
				diagnostics.ToolCalls++
				diagnostics.ToolCallsByName[toolCall.Name]++
			}
		}
	}
	return diagnostics
}

func evaluateLoCoMoReplicateGate(
	manifest locomoReplicateManifest,
	main locomoReplicateArm,
	candidate locomoReplicateArm,
) locomoReplicateGateResult {
	checks := []locomoReplicateGateCheck{{
		Dimension:   "integrity",
		Name:        "all_registered_inputs_valid",
		Passed:      true,
		Actual:      "2 arms with 3 validated replicates each",
		Requirement: "all manifest, protocol, build, usage, trace, and snapshot checks pass",
	}}
	gate := manifest.Gate
	checks = append(checks, locomoReplicateGateCheck{
		Dimension: "quality",
		Name:      "overall_f1_noninferiority",
		Passed: candidate.PrimaryMeanF1 >=
			main.PrimaryMeanF1-gate.OverallNoninferiorityMargin,
		Actual: fmt.Sprintf(
			"candidate=%.6f main=%.6f delta=%.6f",
			candidate.PrimaryMeanF1, main.PrimaryMeanF1,
			candidate.PrimaryMeanF1-main.PrimaryMeanF1,
		),
		Requirement: fmt.Sprintf(
			"candidate-main >= -%.6f",
			gate.OverallNoninferiorityMargin,
		),
	})
	mainCategories := indexLoCoMoCategories(main.Categories)
	candidateCategories := indexLoCoMoCategories(candidate.Categories)
	for _, category := range manifest.Protocol.ExpectedCategories {
		mainValue := mainCategories[category].MeanF1
		candidateValue := candidateCategories[category].MeanF1
		checks = append(checks, locomoReplicateGateCheck{
			Dimension: "quality",
			Name:      "category_f1_noninferiority_" + category,
			Passed: candidateValue >=
				mainValue-gate.PerCategoryNoninferiorityMargin,
			Actual: fmt.Sprintf(
				"candidate=%.6f main=%.6f delta=%.6f",
				candidateValue, mainValue, candidateValue-mainValue,
			),
			Requirement: fmt.Sprintf(
				"candidate-main >= -%.6f",
				gate.PerCategoryNoninferiorityMargin,
			),
		})
	}
	checks = append(
		checks,
		locomoRatioGateCheck(
			"candidate_memory_count_vs_main",
			int64(candidate.Persistence.ActiveRecords),
			int64(main.Persistence.ActiveRecords),
			gate.MemoryCountRatioMaximum,
		),
		locomoRatioGateCheck(
			"candidate_extraction_tokens_vs_main",
			int64(candidate.FreshExtraction.TokenUsage.TotalTokens),
			int64(main.FreshExtraction.TokenUsage.TotalTokens),
			gate.ExtractionTokenRatioMaximum,
		),
		locomoRatioGateCheck(
			"candidate_embedding_tokens_vs_main",
			int64(candidate.FreshExtraction.EmbeddingUsage.TotalTokens),
			int64(main.FreshExtraction.EmbeddingUsage.TotalTokens),
			gate.EmbeddingTokenRatioMaximum,
		),
		locomoRatioGateCheck(
			"candidate_ingest_duration_vs_main",
			candidate.FreshExtraction.IngestDurationMs,
			main.FreshExtraction.IngestDurationMs,
			gate.IngestDurationRatioMaximum,
		),
	)
	result := locomoReplicateGateResult{
		IntegrityPassed: true,
		QualityPassed:   true,
		CostPassed:      true,
		Checks:          checks,
	}
	for _, check := range checks {
		switch check.Dimension {
		case "integrity":
			result.IntegrityPassed = result.IntegrityPassed && check.Passed
		case "quality":
			result.QualityPassed = result.QualityPassed && check.Passed
		case "cost":
			result.CostPassed = result.CostPassed && check.Passed
		}
	}
	result.Passed = result.IntegrityPassed &&
		result.QualityPassed && result.CostPassed
	return result
}

func locomoRatioGateCheck(
	name string,
	numerator int64,
	denominator int64,
	maximum float64,
) locomoReplicateGateCheck {
	passed := denominator > 0 && numerator >= 0 &&
		float64(numerator) <= float64(denominator)*maximum
	actual := fmt.Sprintf(
		"candidate=%d main=%d ratio=undefined", numerator, denominator,
	)
	if denominator > 0 {
		actual = fmt.Sprintf(
			"candidate=%d main=%d ratio=%.6f",
			numerator, denominator, float64(numerator)/float64(denominator),
		)
	}
	return locomoReplicateGateCheck{
		Dimension:   "cost",
		Name:        name,
		Passed:      passed,
		Actual:      actual,
		Requirement: fmt.Sprintf("ratio <= %.6f", maximum),
	}
}

func indexLoCoMoCategories(
	categories []locomoReplicateCategory,
) map[string]locomoReplicateCategory {
	indexed := make(
		map[string]locomoReplicateCategory, len(categories),
	)
	for _, category := range categories {
		indexed[category.Category] = category
	}
	return indexed
}

func bootstrapLoCoMoDifferences(
	differences []float64,
) locomoReplicateBootstrap {
	bootstrap := locomoReplicateBootstrap{
		Unit:            "question",
		QuestionCount:   len(differences),
		Seed:            locomoReplicateBootstrapSeed,
		Resamples:       locomoReplicateBootstrapResamples,
		Confidence:      0.95,
		PointEstimate:   meanFloat64(differences),
		DescriptiveOnly: true,
	}
	if len(differences) == 0 {
		return bootstrap
	}
	values := make([]float64, locomoReplicateBootstrapResamples)
	state := locomoReplicateBootstrapSeed
	const modulus int64 = 2147483647
	const multiplier int64 = 48271
	for replicate := range values {
		var sum float64
		for range differences {
			state = state * multiplier % modulus
			index := int(
				float64(state) / float64(modulus) *
					float64(len(differences)),
			)
			if index == len(differences) {
				index--
			}
			sum += differences[index]
		}
		values[replicate] = sum / float64(len(differences))
	}
	sort.Float64s(values)
	bootstrap.Lower = values[249]
	bootstrap.Upper = values[9749]
	return bootstrap
}

func meanFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func formatLoCoMoReplicateBadCases(
	comparison *locomoReplicateComparison,
) string {
	if comparison == nil {
		return ""
	}
	questions := append(
		[]locomoReplicateQuestion(nil), comparison.Questions...,
	)
	sort.Slice(questions, func(i, j int) bool {
		if questions[i].F1Delta == questions[j].F1Delta {
			return questions[i].QuestionID < questions[j].QuestionID
		}
		return questions[i].F1Delta < questions[j].F1Delta
	})
	var builder strings.Builder
	builder.WriteString(
		"question_id\tcategory\tcandidate_mean_f1\tmain_mean_f1\tcandidate_minus_main_f1\n",
	)
	for _, question := range questions {
		builder.WriteString(strings.Join([]string{
			question.QuestionID,
			question.Category,
			strconv.FormatFloat(
				question.CandidateMeanF1, 'f', 6, 64,
			),
			strconv.FormatFloat(question.MainMeanF1, 'f', 6, 64),
			strconv.FormatFloat(question.F1Delta, 'f', 6, 64),
		}, "\t"))
		builder.WriteByte('\n')
	}
	return builder.String()
}

func writeLoCoMoReplicateJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}
