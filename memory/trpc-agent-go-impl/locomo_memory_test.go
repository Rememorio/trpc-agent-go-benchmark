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
}
