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
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

const (
	lmeAgentModulePath    = "trpc.group/trpc-go/trpc-agent-go"
	lmePGVectorModulePath = "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"

	lmeAnswerPrimaryMaxTokens = 1024
	lmeAnswerRetryMaxTokens   = 1024
	lmeAnswerMaxAttempts      = 2
	lmeJudgePrimaryMaxTokens  = 1024
	lmeJudgeRepairMaxTokens   = 2048

	// These versions are part of the experiment contract. Bump the relevant
	// version whenever replay, prompting, or judging semantics change.
	lmeProtocolVersion          = "lme-memory-turn-pair-v2"
	lmeAnswerPromptVersion      = "lme-memory-answer-v10"
	lmeJudgePromptVersion       = "lme-official-superset-judge-v3"
	lmeJudgeProtocolVersion     = "lme-content-addressed-verdict-v1"
	lmeJudgeCacheFormatVersion  = "lme-judge-cache-v1"
	lmeAnswerCacheFormatVersion = "lme-answer-cache-v1"
	lmeModelCacheFormatVersion  = "lme-model-response-cache-v1"
)

var (
	lmeInjectedBuildRevision        string
	lmeInjectedBuildModified        string
	lmeInjectedBuildProfile         string
	lmeInjectedModuleManifestSHA256 string
	lmeInjectedModuleSumSHA256      string
)

type lmeBuildProvenance struct {
	GoVersion            string                         `json:"go_version,omitempty"`
	Revision             string                         `json:"benchmark_revision,omitempty"`
	Modified             bool                           `json:"benchmark_modified"`
	BuildProfile         string                         `json:"build_profile,omitempty"`
	ModuleManifestSHA256 string                         `json:"module_manifest_sha256,omitempty"`
	ModuleSumSHA256      string                         `json:"module_sum_sha256,omitempty"`
	Modules              map[string]lmeModuleProvenance `json:"modules,omitempty"`
}

type lmeModuleProvenance struct {
	Version            string `json:"version,omitempty"`
	ReplacementPath    string `json:"replacement_path,omitempty"`
	ReplacementVersion string `json:"replacement_version,omitempty"`
	LocalReplacement   bool   `json:"local_replacement,omitempty"`
}

type lmeProtocolProvenance struct {
	Version              string                        `json:"version"`
	ReplayUnit           string                        `json:"replay_unit"`
	SessionOrder         string                        `json:"session_order"`
	ExtractionCadence    string                        `json:"extraction_cadence"`
	RetrievalInput       string                        `json:"retrieval_input"`
	AnswerInput          string                        `json:"answer_input"`
	AnswerModel          string                        `json:"answer_model"`
	AnswerModelVariant   string                        `json:"answer_model_variant"`
	ModelTemperature     float64                       `json:"model_temperature"`
	AnswerPromptVersion  string                        `json:"answer_prompt_version"`
	AnswerGeneration     lmeAnswerGenerationProvenance `json:"answer_generation"`
	EmbeddingModel       string                        `json:"embedding_model"`
	JudgeModel           string                        `json:"judge_model"`
	JudgeModelVariant    string                        `json:"judge_model_variant"`
	JudgeRuns            int                           `json:"judge_runs"`
	JudgePromptVersion   string                        `json:"judge_prompt_version"`
	JudgeProtocolVersion string                        `json:"judge_protocol_version"`
	JudgeGeneration      lmeJudgeGenerationProvenance  `json:"judge_generation"`
	TopK                 int                           `json:"top_k"`
	MaxSessions          int                           `json:"max_sessions"`
	MaxPairs             int                           `json:"max_pairs"`
	IngestWait           string                        `json:"ingest_wait"`
	ModelCallTimeout     string                        `json:"model_call_timeout"`
	AnswerEnabled        bool                          `json:"answer_enabled"`
}

type lmeAnswerGenerationProvenance struct {
	PrimaryMaxTokens   int      `json:"primary_max_tokens"`
	RetryMaxTokens     int      `json:"retry_max_tokens"`
	MaxAttempts        int      `json:"max_attempts"`
	RetryFinishReasons []string `json:"retry_finish_reasons"`
	RetryEmptyResponse bool     `json:"retry_empty_response"`
	Temperature        float64  `json:"temperature"`
	ReasoningEffort    string   `json:"reasoning_effort"`
	ThinkingEnabled    bool     `json:"thinking_enabled"`
}

type lmeJudgeGenerationProvenance struct {
	PrimaryMaxTokens int     `json:"primary_max_tokens"`
	RepairMaxTokens  int     `json:"repair_max_tokens"`
	Temperature      float64 `json:"temperature"`
	ReasoningEffort  string  `json:"reasoning_effort"`
	ThinkingEnabled  bool    `json:"thinking_enabled"`
}

type lmeRerankGenerationProvenance struct {
	InitialMaxTokens   int      `json:"initial_max_tokens"`
	RetryMaxTokens     int      `json:"retry_max_tokens"`
	MaxAttempts        int      `json:"max_attempts"`
	RetryFinishReasons []string `json:"retry_finish_reasons"`
	Temperature        float64  `json:"temperature"`
	ReasoningEffort    string   `json:"reasoning_effort"`
	ThinkingEnabled    bool     `json:"thinking_enabled"`
	ResponseFormat     string   `json:"response_format"`
}

func currentLongMemEvalAnswerGeneration() lmeAnswerGenerationProvenance {
	return lmeAnswerGenerationProvenance{
		PrimaryMaxTokens:   lmeAnswerPrimaryMaxTokens,
		RetryMaxTokens:     lmeAnswerRetryMaxTokens,
		MaxAttempts:        lmeAnswerMaxAttempts,
		RetryFinishReasons: []string{},
		RetryEmptyResponse: true,
		Temperature:        0,
		ReasoningEffort:    "low",
		ThinkingEnabled:    false,
	}
}

func currentLongMemEvalJudgeGeneration() lmeJudgeGenerationProvenance {
	return lmeJudgeGenerationProvenance{
		PrimaryMaxTokens: lmeJudgePrimaryMaxTokens,
		RepairMaxTokens:  lmeJudgeRepairMaxTokens,
		Temperature:      0,
		ReasoningEffort:  "low",
		ThinkingEnabled:  false,
	}
}

func currentLongMemEvalRerankGeneration() lmeRerankGenerationProvenance {
	return lmeRerankGenerationProvenance{
		InitialMaxTokens:   lmeRerankInitialTokens,
		RetryMaxTokens:     lmeRerankRetryTokens,
		MaxAttempts:        lmeRerankMaxAttempts,
		RetryFinishReasons: []string{"length", "max_tokens", "max_output_tokens"},
		Temperature:        0,
		ReasoningEffort:    "low",
		ThinkingEnabled:    false,
		ResponseFormat:     "json_object",
	}
}

func currentLongMemEvalProtocol() lmeProtocolProvenance {
	return lmeProtocolProvenance{
		Version:              lmeProtocolVersion,
		ReplayUnit:           "user-assistant-pair",
		SessionOrder:         "haystack-date-ascending-stable",
		ExtractionCadence:    "after-each-pair",
		RetrievalInput:       "question-to-memory-search",
		AnswerInput:          "ranked-memories-only",
		AnswerModel:          getModelName(),
		AnswerModelVariant:   getModelVariant(),
		ModelTemperature:     0,
		AnswerPromptVersion:  lmeAnswerPromptVersion,
		AnswerGeneration:     currentLongMemEvalAnswerGeneration(),
		EmbeddingModel:       getEmbedModelName(),
		JudgeModel:           getEvalModelName(),
		JudgeModelVariant:    getModelVariant(),
		JudgeRuns:            *flagLMEJudgeRuns,
		JudgePromptVersion:   lmeJudgePromptVersion,
		JudgeProtocolVersion: lmeJudgeProtocolVersion,
		JudgeGeneration:      currentLongMemEvalJudgeGeneration(),
		TopK:                 *flagVectorTopK,
		MaxSessions:          *flagLMEMaxSessions,
		MaxPairs:             *flagLMEMaxPairs,
		IngestWait:           flagLMEIngestWait.String(),
		ModelCallTimeout:     flagLMEModelCallTimeout.String(),
		AnswerEnabled:        *flagLMEAnswer,
	}
}

func validateLongMemEvalProtocol(protocol lmeProtocolProvenance) error {
	if protocol.Version != lmeProtocolVersion {
		return fmt.Errorf(
			"LongMemEval protocol version is %q, current version is %q",
			protocol.Version, lmeProtocolVersion,
		)
	}
	for name, value := range map[string]string{
		"answer model":           protocol.AnswerModel,
		"embedding model":        protocol.EmbeddingModel,
		"judge model":            protocol.JudgeModel,
		"answer prompt version":  protocol.AnswerPromptVersion,
		"judge prompt version":   protocol.JudgePromptVersion,
		"judge protocol version": protocol.JudgeProtocolVersion,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("LongMemEval protocol %s is missing", name)
		}
	}
	if protocol.JudgeRuns <= 0 || protocol.JudgeRuns%2 == 0 {
		return fmt.Errorf(
			"LongMemEval protocol judge runs must be a positive odd number, got %d",
			protocol.JudgeRuns,
		)
	}
	if protocol.TopK <= 0 {
		return fmt.Errorf(
			"LongMemEval protocol top-k must be positive, got %d",
			protocol.TopK,
		)
	}
	if protocol.MaxSessions < 0 || protocol.MaxPairs < 0 {
		return errors.New(
			"LongMemEval protocol session and pair limits must not be negative",
		)
	}
	if protocol.AnswerGeneration.PrimaryMaxTokens <= 0 ||
		protocol.AnswerGeneration.RetryMaxTokens <= 0 ||
		protocol.AnswerGeneration.MaxAttempts <= 0 {
		return errors.New("LongMemEval answer generation contract is invalid")
	}
	if protocol.JudgeGeneration.PrimaryMaxTokens <= 0 ||
		protocol.JudgeGeneration.RepairMaxTokens <= 0 {
		return errors.New("LongMemEval judge generation contract is invalid")
	}
	return nil
}

func validateLongMemEvalResultProtocol(
	metadata map[string]any,
	current lmeProtocolProvenance,
) error {
	if metadata == nil {
		return errors.New("LongMemEval result metadata is missing")
	}
	value, ok := metadata["protocol"]
	if !ok {
		return errors.New("LongMemEval result protocol is missing")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal LongMemEval result protocol: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var recorded lmeProtocolProvenance
	if err := decoder.Decode(&recorded); err != nil {
		return fmt.Errorf("decode LongMemEval result protocol: %w", err)
	}
	if err := validateLongMemEvalProtocol(recorded); err != nil {
		return fmt.Errorf("invalid recorded LongMemEval protocol: %w", err)
	}
	declaredVersion, ok := metadata["protocol_version"].(string)
	if !ok || declaredVersion != recorded.Version {
		return fmt.Errorf(
			"LongMemEval result protocol version is %q, payload version is %q",
			declaredVersion, recorded.Version,
		)
	}
	recordedDigest, err := longMemEvalJSONSHA256(recorded)
	if err != nil {
		return fmt.Errorf("hash recorded LongMemEval protocol: %w", err)
	}
	declaredDigest, ok := metadata["protocol_sha256"].(string)
	if !ok || declaredDigest != recordedDigest {
		return fmt.Errorf(
			"LongMemEval result protocol digest is %q, payload digest is %q",
			declaredDigest, recordedDigest,
		)
	}
	if err := validateLongMemEvalProtocol(current); err != nil {
		return fmt.Errorf("invalid current LongMemEval protocol: %w", err)
	}
	currentDigest, err := longMemEvalJSONSHA256(current)
	if err != nil {
		return fmt.Errorf("hash current LongMemEval protocol: %w", err)
	}
	if currentDigest != recordedDigest {
		return fmt.Errorf(
			"current LongMemEval protocol digest is %q, result requires %q",
			currentDigest, recordedDigest,
		)
	}
	return nil
}

func longMemEvalImplementation() string {
	if value := strings.TrimSpace(*flagLMEImplementation); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("LME_IMPLEMENTATION")); value != "" {
		return value
	}
	return "unspecified"
}

func longMemEvalMem0Implementation() string {
	if value := strings.TrimSpace(*flagMem0Implementation); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("MEM0_IMPLEMENTATION")); value != "" {
		return value
	}
	return "unspecified"
}

func longMemEvalFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func longMemEvalJSONSHA256(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func newLongMemEvalLedgerID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate LongMemEval ledger id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func longMemEvalExperimentDigests(
	datasetPath string,
	cases []*lmeInstance,
	protocol lmeProtocolProvenance,
) (dataset, selection, protocolDigest string, err error) {
	dataset, err = longMemEvalFileSHA256(datasetPath)
	if err != nil {
		return "", "", "", fmt.Errorf("hash LongMemEval dataset: %w", err)
	}
	selection, err = longMemEvalJSONSHA256(questionIDs(cases))
	if err != nil {
		return "", "", "", fmt.Errorf("hash LongMemEval selection: %w", err)
	}
	protocolDigest, err = longMemEvalJSONSHA256(protocol)
	if err != nil {
		return "", "", "", fmt.Errorf("hash LongMemEval protocol: %w", err)
	}
	return dataset, selection, protocolDigest, nil
}

func currentLongMemEvalBuildProvenance() lmeBuildProvenance {
	info, ok := debug.ReadBuildInfo()
	result := longMemEvalBuildProvenance(info, ok)
	return applyLongMemEvalInjectedProvenance(
		result,
		lmeInjectedBuildRevision,
		lmeInjectedBuildModified,
		lmeInjectedBuildProfile,
		lmeInjectedModuleManifestSHA256,
		lmeInjectedModuleSumSHA256,
	)
}

func longMemEvalBuildProvenanceIssue(build lmeBuildProvenance) string {
	if strings.TrimSpace(build.Revision) == "" {
		return "benchmark revision is missing"
	}
	if build.Modified {
		return "benchmark worktree was modified at build time"
	}
	if build.BuildProfile != "candidate" && build.BuildProfile != "upstream" {
		return "build profile is missing or unsupported"
	}
	if strings.TrimSpace(build.ModuleManifestSHA256) == "" {
		return "module manifest digest is missing"
	}
	if strings.TrimSpace(build.ModuleSumSHA256) == "" {
		return "module checksum digest is missing"
	}
	if err := validateLongMemEvalMemoryModules(build.Modules); err != nil {
		return err.Error()
	}
	return ""
}

func validateLongMemEvalMemoryModules(
	modules map[string]lmeModuleProvenance,
) error {
	for _, path := range []string{lmeAgentModulePath, lmePGVectorModulePath} {
		module, ok := modules[path]
		if !ok {
			return fmt.Errorf("missing module provenance for %s", path)
		}
		if module.LocalReplacement {
			return fmt.Errorf(
				"module %s uses an unpinned local replacement",
				path,
			)
		}
		version := strings.TrimSpace(module.ReplacementVersion)
		if version == "" {
			version = strings.TrimSpace(module.Version)
		}
		if version == "" || version == "(devel)" {
			return fmt.Errorf(
				"module %s is missing a pinned version",
				path,
			)
		}
	}
	return nil
}

func applyLongMemEvalInjectedProvenance(
	result lmeBuildProvenance,
	revision string,
	modified string,
	buildProfile string,
	moduleManifestSHA256 string,
	moduleSumSHA256 string,
) lmeBuildProvenance {
	if result.Revision == "" {
		result.Revision = strings.TrimSpace(revision)
	}
	if value, err := strconv.ParseBool(strings.TrimSpace(modified)); err == nil {
		result.Modified = value
	}
	if result.BuildProfile == "" {
		result.BuildProfile = strings.TrimSpace(buildProfile)
	}
	if result.ModuleManifestSHA256 == "" {
		result.ModuleManifestSHA256 = strings.TrimSpace(moduleManifestSHA256)
	}
	if result.ModuleSumSHA256 == "" {
		result.ModuleSumSHA256 = strings.TrimSpace(moduleSumSHA256)
	}
	return result
}

func longMemEvalBuildProvenance(info *debug.BuildInfo, ok bool) lmeBuildProvenance {
	if !ok || info == nil {
		return lmeBuildProvenance{}
	}
	result := lmeBuildProvenance{
		GoVersion: info.GoVersion,
		Modules:   make(map[string]lmeModuleProvenance),
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			result.Revision = setting.Value
		case "vcs.modified":
			result.Modified = setting.Value == "true"
		}
	}
	for _, module := range info.Deps {
		if module == nil || (module.Path != lmeAgentModulePath && module.Path != lmePGVectorModulePath) {
			continue
		}
		provenance := lmeModuleProvenance{Version: module.Version}
		if module.Replace != nil {
			if isLocalLongMemEvalModuleReplacement(module.Replace) {
				provenance.LocalReplacement = true
			} else {
				provenance.ReplacementPath = module.Replace.Path
				provenance.ReplacementVersion = module.Replace.Version
			}
		}
		result.Modules[module.Path] = provenance
	}
	if len(result.Modules) == 0 {
		result.Modules = nil
	}
	return result
}

func isLocalLongMemEvalModuleReplacement(replacement *debug.Module) bool {
	if replacement == nil {
		return false
	}
	version := strings.TrimSpace(replacement.Version)
	if version == "" || version == "(devel)" {
		return true
	}
	path := strings.TrimSpace(replacement.Path)
	return filepath.IsAbs(path) || path == "." || strings.HasPrefix(path, "./") ||
		strings.HasPrefix(path, "../")
}
