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
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/scenarios"
)

type failingLoCoMoEvaluator struct {
	result *scenarios.SampleResult
	err    error
}

func (e failingLoCoMoEvaluator) Name() string {
	return "failing"
}

func (e failingLoCoMoEvaluator) Evaluate(
	context.Context,
	*dataset.LoCoMoSample,
) (*scenarios.SampleResult, error) {
	return e.result, e.err
}

func TestFilterLoCoMoQuestions(t *testing.T) {
	samples := []*dataset.LoCoMoSample{
		nil,
		{
			SampleID: "sample-1",
			QA: []dataset.QAItem{
				{QuestionID: "q1"},
				{QuestionID: "q2"},
			},
		},
		{
			SampleID: "sample-2",
			QA: []dataset.QAItem{
				{QuestionID: "q3"},
			},
		},
	}
	got, err := filterLoCoMoQuestions(
		samples, []string{"q3", "q1", "q1"},
	)
	if err != nil {
		t.Fatalf("filter questions: %v", err)
	}
	if len(got) != 2 || got[0].SampleID != "sample-1" ||
		got[1].SampleID != "sample-2" {
		t.Fatalf("samples = %+v", got)
	}
	if len(got[0].QA) != 1 || got[0].QA[0].QuestionID != "q1" {
		t.Fatalf("sample-1 QA = %+v", got[0].QA)
	}
	if len(got[1].QA) != 1 || got[1].QA[0].QuestionID != "q3" {
		t.Fatalf("sample-2 QA = %+v", got[1].QA)
	}
}

func TestFilterLoCoMoQuestionsRejectsMissingIDs(t *testing.T) {
	_, err := filterLoCoMoQuestions(
		[]*dataset.LoCoMoSample{{
			SampleID: "sample-1",
			QA:       []dataset.QAItem{{QuestionID: "q1"}},
		}},
		[]string{"missing-b", "q1", "missing-a"},
	)
	if err == nil || !strings.Contains(
		err.Error(), "missing-a, missing-b",
	) {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildEvaluationResultRecordsMemoryQAPromptVersion(t *testing.T) {
	for _, scenario := range []scenarios.ScenarioType{
		scenarios.ScenarioAuto,
		scenarios.ScenarioAgentic,
	} {
		result := buildEvaluationResult(
			scenarios.Config{Scenario: scenario},
			"pgvector",
			time.Now(),
			nil,
			metrics.NewCategoryAggregator(),
			0,
			scenarios.TokenUsage{},
		)
		if got := result.Metadata.QAPromptVersion; got != scenarios.MemoryQAPromptVersion {
			t.Fatalf(
				"scenario %s prompt version = %q, want %q",
				scenario, got, scenarios.MemoryQAPromptVersion,
			)
		}
		if got := result.Metadata.QASearchStrategy; got != scenarios.MemoryQASearchStrategy {
			t.Fatalf(
				"scenario %s search strategy = %q, want %q",
				scenario, got, scenarios.MemoryQASearchStrategy,
			)
		}
		if got := result.Metadata.QARecoveryMaxTokens; got != scenarios.MemoryQARecoveryMaxTokens {
			t.Fatalf(
				"scenario %s recovery max tokens = %d, want %d",
				scenario, got, scenarios.MemoryQARecoveryMaxTokens,
			)
		}
	}
}

func TestBuildEvaluationResultRecordsModelVariant(t *testing.T) {
	restoreStringFlag(t, flagModelVariant, "glm")
	result := buildEvaluationResult(
		scenarios.Config{Scenario: scenarios.ScenarioAuto},
		"pgvector",
		time.Now(),
		nil,
		metrics.NewCategoryAggregator(),
		0,
		scenarios.TokenUsage{},
	)
	if got := result.Metadata.ModelVariant; got != "glm" {
		t.Fatalf("model variant = %q, want glm", got)
	}
}

func TestBuildEvaluationResultRecordsAutoExtractionTimeout(t *testing.T) {
	previousJobTimeout := *flagAutoMemoryJobTimeout
	*flagAutoMemoryJobTimeout = 5 * time.Minute
	t.Cleanup(func() {
		*flagAutoMemoryJobTimeout = previousJobTimeout
	})
	configured := buildEvaluationResult(
		scenarios.Config{
			Scenario:              scenarios.ScenarioAuto,
			AutoExtractionTimeout: 20 * time.Minute,
		},
		"pgvector",
		time.Now(),
		nil,
		metrics.NewCategoryAggregator(),
		0,
		scenarios.TokenUsage{},
	)
	if got := configured.Metadata.AutoExtractionTimeout; got != "20m0s" {
		t.Fatalf("configured extraction timeout = %q, want 20m0s", got)
	}
	if got := configured.Metadata.AutoMemoryJobTimeout; got != "5m0s" {
		t.Fatalf("memory job timeout = %q, want 5m0s", got)
	}

	derived := buildEvaluationResult(
		scenarios.Config{Scenario: scenarios.ScenarioAuto},
		"pgvector",
		time.Now(),
		nil,
		metrics.NewCategoryAggregator(),
		0,
		scenarios.TokenUsage{},
	)
	if got := derived.Metadata.AutoExtractionTimeout; got != "derived-from-session-count" {
		t.Fatalf("derived extraction timeout = %q", got)
	}
}

func TestEvalMetadataRecordsDisabledMemoryReuse(t *testing.T) {
	result := buildEvaluationResult(
		scenarios.Config{Scenario: scenarios.ScenarioAuto},
		"pgvector",
		time.Now(),
		nil,
		metrics.NewCategoryAggregator(),
		0,
		scenarios.TokenUsage{},
	)
	data, err := json.Marshal(result.Metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if !strings.Contains(string(data), `"reuse_memories":false`) {
		t.Fatalf("metadata does not record disabled reuse: %s", data)
	}
}

func TestRunEvaluationRecordsFailedSampleAndCost(t *testing.T) {
	extractionUsage := scenarios.TokenUsage{
		PromptTokens: 100,
		TotalTokens:  120,
		LLMCalls:     2,
	}
	extractionEmbeddingUsage := scenarios.EmbeddingUsage{
		PromptTokens: 30,
		TotalTokens:  30,
		Calls:        3,
	}
	failureResult := &scenarios.SampleResult{
		SampleID:                 "sample-1",
		TotalTimeMs:              1234,
		TokenUsage:               &extractionUsage,
		ExtractionTokenUsage:     &extractionUsage,
		EmbeddingUsage:           &extractionEmbeddingUsage,
		ExtractionEmbeddingUsage: &extractionEmbeddingUsage,
		ExtractionCalls: []scenarios.ExtractionCallTrace{{
			Step: 1,
		}},
	}

	result, err := runEvaluation(
		context.Background(),
		[]*dataset.LoCoMoSample{{SampleID: "sample-1"}},
		failingLoCoMoEvaluator{
			result: failureResult,
			err:    errors.New("extraction timed out"),
		},
		scenarios.Config{Scenario: scenarios.ScenarioAuto},
		"pgvector",
		t.TempDir(),
	)
	if err == nil || !strings.Contains(err.Error(), "sample-1") {
		t.Fatalf("run error = %v", err)
	}
	if result.Summary.TotalSamples != 0 || result.Summary.FailedSamples != 1 {
		t.Fatalf("sample counts = successful %d, failed %d",
			result.Summary.TotalSamples, result.Summary.FailedSamples)
	}
	if len(result.Failures) != 1 ||
		result.Failures[0].Error != "extraction timed out" ||
		len(result.Failures[0].ExtractionCalls) != 1 {
		t.Fatalf("failures = %+v", result.Failures)
	}
	if result.Summary.TotalTokens != 120 ||
		result.Summary.ExtractionTokenUsage == nil ||
		result.Summary.ExtractionTokenUsage.TotalTokens != 120 {
		t.Fatalf("token summary = %+v", result.Summary)
	}
	if result.Summary.ExtractionEmbeddingUsage == nil ||
		result.Summary.ExtractionEmbeddingUsage.Calls != 3 {
		t.Fatalf("embedding summary = %+v", result.Summary.EmbeddingUsage)
	}
}

func TestBuildEvaluationResultOmitsMemoryQAPromptVersion(t *testing.T) {
	result := buildEvaluationResult(
		scenarios.Config{Scenario: scenarios.ScenarioLongContext},
		"",
		time.Now(),
		nil,
		metrics.NewCategoryAggregator(),
		0,
		scenarios.TokenUsage{},
	)
	if got := result.Metadata.QAPromptVersion; got != "" {
		t.Fatalf("long-context prompt version = %q, want empty", got)
	}
	if got := result.Metadata.QASearchStrategy; got != "" {
		t.Fatalf("long-context search strategy = %q, want empty", got)
	}
	if got := result.Metadata.QARecoveryMaxTokens; got != 0 {
		t.Fatalf("long-context recovery max tokens = %d, want 0", got)
	}
	if got := result.Metadata.VectorTopK; got != 0 {
		t.Fatalf("long-context vector top-k = %d, want 0", got)
	}
}

func TestBuildEvaluationResultAggregatesAutoPhaseUsage(t *testing.T) {
	extractionTokens := scenarios.TokenUsage{
		PromptTokens:        100,
		CompletionTokens:    20,
		TotalTokens:         120,
		CacheCreationTokens: 10,
		CacheReadTokens:     40,
		LLMCalls:            2,
	}
	qaTokens := scenarios.TokenUsage{
		PromptTokens:     50,
		CompletionTokens: 10,
		TotalTokens:      60,
		CachedTokens:     5,
		LLMCalls:         1,
	}
	totalTokens := extractionTokens
	totalTokens.Add(qaTokens)
	extractionEmbeddings := scenarios.EmbeddingUsage{
		PromptTokens: 30,
		TotalTokens:  30,
		Calls:        3,
	}
	qaEmbeddings := scenarios.EmbeddingUsage{
		PromptTokens: 5,
		TotalTokens:  5,
		Calls:        1,
	}
	totalEmbeddings := extractionEmbeddings
	totalEmbeddings.Add(qaEmbeddings)

	result := buildEvaluationResult(
		scenarios.Config{Scenario: scenarios.ScenarioAuto},
		"pgvector",
		time.Now(),
		[]*scenarios.SampleResult{{
			ExtractionTokenUsage:     &extractionTokens,
			QATokenUsage:             &qaTokens,
			EmbeddingUsage:           &totalEmbeddings,
			ExtractionEmbeddingUsage: &extractionEmbeddings,
			QAEmbeddingUsage:         &qaEmbeddings,
		}},
		metrics.NewCategoryAggregator(),
		1,
		totalTokens,
	)

	if result.Metadata.ReplayProtocol != locomoAutoReplayProtocol {
		t.Fatalf("replay protocol = %q", result.Metadata.ReplayProtocol)
	}
	if result.Metadata.ReplayProtocol !=
		"chronological-session-sequential-auto-v4" {
		t.Fatalf("unexpected replay protocol: %q", result.Metadata.ReplayProtocol)
	}
	if result.Metadata.RoleMapping != locomoRoleMapping {
		t.Fatalf("role mapping = %q", result.Metadata.RoleMapping)
	}
	if result.Metadata.VectorTopK != *flagVectorTopK {
		t.Fatalf(
			"vector top-k = %d, want %d",
			result.Metadata.VectorTopK,
			*flagVectorTopK,
		)
	}
	if !strings.Contains(result.Metadata.TokenUsageScope, "judge excluded") {
		t.Fatalf("token usage scope = %q", result.Metadata.TokenUsageScope)
	}
	if !strings.Contains(result.Metadata.EmbeddingUsageScope, "QA-search") {
		t.Fatalf(
			"embedding usage scope = %q",
			result.Metadata.EmbeddingUsageScope,
		)
	}
	if result.Summary.TokenUsage == nil ||
		*result.Summary.TokenUsage != totalTokens {
		t.Fatalf("total token usage = %+v", result.Summary.TokenUsage)
	}
	if result.Summary.ExtractionTokenUsage == nil ||
		*result.Summary.ExtractionTokenUsage != extractionTokens {
		t.Fatalf(
			"extraction token usage = %+v",
			result.Summary.ExtractionTokenUsage,
		)
	}
	if result.Summary.QATokenUsage == nil ||
		*result.Summary.QATokenUsage != qaTokens {
		t.Fatalf("QA token usage = %+v", result.Summary.QATokenUsage)
	}
	if result.Summary.EmbeddingUsage == nil ||
		*result.Summary.EmbeddingUsage != totalEmbeddings {
		t.Fatalf("embedding usage = %+v", result.Summary.EmbeddingUsage)
	}
	if result.Summary.TotalCachedTokens != 40 {
		t.Fatalf(
			"total cached tokens = %d, want 40",
			result.Summary.TotalCachedTokens,
		)
	}
}

func TestBuildEvaluationResultCountsProtocolViolations(t *testing.T) {
	result := buildEvaluationResult(
		scenarios.Config{Scenario: scenarios.ScenarioAuto},
		"pgvector",
		time.Now(),
		[]*scenarios.SampleResult{
			nil,
			{
				QAResults: []*scenarios.QAResult{
					nil,
					{},
					{ProtocolError: "missing search"},
					{AnswerRecovery: &scenarios.AnswerRecoveryTrace{
						Succeeded: true,
						Applied:   true,
					}},
					{AnswerRecovery: &scenarios.AnswerRecoveryTrace{
						InitialAnswerRetained: true,
					}},
					{AnswerRecovery: &scenarios.AnswerRecoveryTrace{
						FallbackApplied: true,
					}},
				},
			},
		},
		metrics.NewCategoryAggregator(),
		2,
		scenarios.TokenUsage{},
	)
	if got := result.Summary.ProtocolViolations; got != 1 {
		t.Fatalf("protocol violations = %d, want 1", got)
	}
	if got := result.Summary.AnswerRecoveryAttempts; got != 3 {
		t.Fatalf("answer recovery attempts = %d, want 3", got)
	}
	if got := result.Summary.AnswerRecoverySuccesses; got != 1 {
		t.Fatalf("answer recovery successes = %d, want 1", got)
	}
	if got := result.Summary.AnswerRecoveryApplied; got != 1 {
		t.Fatalf("answer recovery applied = %d, want 1", got)
	}
	if got := result.Summary.AnswerRecoveryRetained; got != 1 {
		t.Fatalf("answer recovery retained = %d, want 1", got)
	}
	if got := result.Summary.AnswerRecoveryFallbacks; got != 1 {
		t.Fatalf("answer recovery fallbacks = %d, want 1", got)
	}
}
