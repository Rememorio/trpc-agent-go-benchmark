//go:build !lme_upstream

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

	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

func TestLongMemEvalExtractorOptionsCandidate(t *testing.T) {
	t.Parallel()
	for _, config := range []pgvectorExtractionConfig{
		{
			UpdatePolicy:              pgvectorUpdatePolicyReconcile,
			AssistantResultExtraction: true,
		},
		{
			UpdatePolicy: pgvectorUpdatePolicyAddOnly,
		},
	} {
		options, err := pgvectorExtractorOptions(config)
		if err != nil {
			t.Fatalf("candidate options for %+v: %v", config, err)
		}
		metadata := extractor.NewExtractor(nil, options...).Metadata()
		if got := metadata["update_policy"]; got != string(config.UpdatePolicy) {
			t.Fatalf("update policy metadata = %v", got)
		}
		if got := metadata["assistant_result_extraction"]; got != config.AssistantResultExtraction {
			t.Fatalf("assistant result metadata = %v", got)
		}
	}
}

func TestLongMemEvalExtractorOptionsCandidateRejectsNormalizedPolicy(
	t *testing.T,
) {
	t.Parallel()

	_, err := pgvectorExtractorOptions(pgvectorExtractionConfig{
		UpdatePolicy: pgvectorUpdatePolicyHistoryPreserving,
	})
	if err == nil || !strings.Contains(err.Error(),
		`does not support update policy "history-preserving"`) {
		t.Fatalf("error = %v", err)
	}
}
