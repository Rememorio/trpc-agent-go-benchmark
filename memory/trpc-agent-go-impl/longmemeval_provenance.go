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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	lmeAnswerPrimaryMaxTokens = 512
	lmeAnswerRetryMaxTokens   = 4096
	lmeAnswerMaxAttempts      = 2
	lmeJudgePrimaryMaxTokens  = 1024
	lmeJudgeRepairMaxTokens   = 2048

	// These versions are part of the experiment contract. Bump the relevant
	// version whenever replay, prompting, or judging semantics change.
	lmeProtocolVersion     = "lme-memory-turn-pair-v1"
	lmeAnswerPromptVersion = "lme-memory-answer-v1"
	lmeJudgePromptVersion  = "lme-official-adapted-judge-v2"
)

var (
	lmeInjectedBuildRevision string
	lmeInjectedBuildModified string
)

type lmeBuildProvenance struct {
	GoVersion string                         `json:"go_version,omitempty"`
	Revision  string                         `json:"benchmark_revision,omitempty"`
	Modified  bool                           `json:"benchmark_modified"`
	Modules   map[string]lmeModuleProvenance `json:"modules,omitempty"`
}

type lmeModuleProvenance struct {
	Version            string `json:"version,omitempty"`
	ReplacementPath    string `json:"replacement_path,omitempty"`
	ReplacementVersion string `json:"replacement_version,omitempty"`
	LocalReplacement   bool   `json:"local_replacement,omitempty"`
}

type lmeProtocolProvenance struct {
	Version             string `json:"version"`
	ReplayUnit          string `json:"replay_unit"`
	SessionOrder        string `json:"session_order"`
	ExtractionCadence   string `json:"extraction_cadence"`
	RetrievalInput      string `json:"retrieval_input"`
	AnswerInput         string `json:"answer_input"`
	AnswerPromptVersion string `json:"answer_prompt_version"`
	TopK                int    `json:"top_k"`
	MaxSessions         int    `json:"max_sessions"`
	MaxPairs            int    `json:"max_pairs"`
	IngestWait          string `json:"ingest_wait"`
	ModelCallTimeout    string `json:"model_call_timeout"`
	AnswerEnabled       bool   `json:"answer_enabled"`
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
		RetryFinishReasons: []string{"length", "max_tokens"},
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
		Version:             lmeProtocolVersion,
		ReplayUnit:          "user-assistant-pair",
		SessionOrder:        "haystack-date-ascending-stable",
		ExtractionCadence:   "after-each-pair",
		RetrievalInput:      "question-to-memory-search",
		AnswerInput:         "ranked-memories-only",
		AnswerPromptVersion: lmeAnswerPromptVersion,
		TopK:                *flagVectorTopK,
		MaxSessions:         *flagLMEMaxSessions,
		MaxPairs:            *flagLMEMaxPairs,
		IngestWait:          flagLMEIngestWait.String(),
		ModelCallTimeout:    flagLMEModelCallTimeout.String(),
		AnswerEnabled:       *flagLMEAnswer,
	}
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
	)
}

func applyLongMemEvalInjectedProvenance(
	result lmeBuildProvenance,
	revision string,
	modified string,
) lmeBuildProvenance {
	if result.Revision == "" {
		result.Revision = strings.TrimSpace(revision)
	}
	if value, err := strconv.ParseBool(strings.TrimSpace(modified)); err == nil {
		result.Modified = value
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
