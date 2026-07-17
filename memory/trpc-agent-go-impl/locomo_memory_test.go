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
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/scenarios"
)

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
					{AnswerRecovery: &scenarios.AnswerRecoveryTrace{Succeeded: true}},
					{AnswerRecovery: &scenarios.AnswerRecoveryTrace{}},
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
	if got := result.Summary.AnswerRecoveryAttempts; got != 2 {
		t.Fatalf("answer recovery attempts = %d, want 2", got)
	}
	if got := result.Summary.AnswerRecoverySuccesses; got != 1 {
		t.Fatalf("answer recovery successes = %d, want 1", got)
	}
}
