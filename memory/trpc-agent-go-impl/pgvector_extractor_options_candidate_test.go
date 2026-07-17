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
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

func TestLongMemEvalExtractorOptionsCandidate(t *testing.T) {
	t.Parallel()
	options, err := pgvectorExtractorOptions(pgvectorExtractionConfig{
		UpdatePolicy:              pgvectorUpdatePolicyHistoryPreserving,
		AssistantResultExtraction: true,
	})
	if err != nil {
		t.Fatalf("candidate options: %v", err)
	}
	metadataProvider, ok := extractor.NewExtractor(nil, options...).(interface {
		Metadata() map[string]any
	})
	if !ok {
		t.Fatal("candidate extractor does not expose configuration metadata")
	}
	metadata := metadataProvider.Metadata()
	if got := metadata["update_policy"]; got != "history-preserving" {
		t.Fatalf("update policy metadata = %v", got)
	}
	if got := metadata["assistant_result_extraction"]; got != true {
		t.Fatalf("assistant result metadata = %v", got)
	}
}
