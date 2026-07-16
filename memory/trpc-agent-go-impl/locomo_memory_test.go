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
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/scenarios"
)

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
