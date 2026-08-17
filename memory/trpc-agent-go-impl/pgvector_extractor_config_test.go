//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "testing"

func TestAssistantResultUpdatePolicy(t *testing.T) {
	t.Parallel()

	if got := assistantResultUpdatePolicy(
		pgvectorUpdatePolicyMergeSimilar, false,
	); got != "" {
		t.Fatalf("disabled policy = %q, want empty", got)
	}
	if got := assistantResultUpdatePolicy(
		pgvectorUpdatePolicyMergeSimilar, true,
	); got != assistantResultPolicyPreserving {
		t.Fatalf("merge-similar result policy = %q", got)
	}
	if got := assistantResultUpdatePolicy(
		pgvectorUpdatePolicyPreserveHistory, true,
	); got != assistantResultPolicyPreserving {
		t.Fatalf("preserve-history result policy = %q", got)
	}
	if got := assistantResultUpdatePolicy(
		pgvectorUpdatePolicyAppendOnly, true,
	); got != string(pgvectorUpdatePolicyAppendOnly) {
		t.Fatalf("append-only result policy = %q", got)
	}
}

func TestCurrentPGVectorExtractionConfig(t *testing.T) {
	oldPolicy := *flagPGVectorUpdatePolicy
	oldAssistantResults := *flagPGVectorAssistantResultExtraction
	defer func() {
		*flagPGVectorUpdatePolicy = oldPolicy
		*flagPGVectorAssistantResultExtraction = oldAssistantResults
	}()

	for _, test := range []struct {
		input string
		want  pgvectorUpdatePolicy
	}{
		{input: "", want: pgvectorUpdatePolicyMergeSimilar},
		{input: " MERGE_SIMILAR ", want: pgvectorUpdatePolicyMergeSimilar},
		{input: " PRESERVE_HISTORY ", want: pgvectorUpdatePolicyPreserveHistory},
		{input: "APPEND_ONLY", want: pgvectorUpdatePolicyAppendOnly},
	} {
		*flagPGVectorUpdatePolicy = test.input
		*flagPGVectorAssistantResultExtraction = true
		got, err := currentPGVectorExtractionConfig()
		if err != nil {
			t.Fatalf("policy %q returned error: %v", test.input, err)
		}
		if got.UpdatePolicy != test.want {
			t.Fatalf("policy %q = %q, want %q",
				test.input, got.UpdatePolicy, test.want)
		}
		if !got.AssistantResultExtraction {
			t.Fatalf("policy %q lost assistant-result setting", test.input)
		}
	}

	for _, invalid := range []string{
		"custom",
		"conservative",
		"reconcile",
		"history-preserving",
		"add-only",
		"preserve-history",
	} {
		*flagPGVectorUpdatePolicy = invalid
		if _, err := currentPGVectorExtractionConfig(); err == nil {
			t.Fatalf("expected policy %q to be rejected", invalid)
		}
	}
}

func TestValidatePGVectorExtractionFlags(t *testing.T) {
	oldPolicy := *flagPGVectorUpdatePolicy
	oldAssistantResults := *flagPGVectorAssistantResultExtraction
	defer func() {
		*flagPGVectorUpdatePolicy = oldPolicy
		*flagPGVectorAssistantResultExtraction = oldAssistantResults
	}()

	*flagPGVectorUpdatePolicy = "custom"
	*flagPGVectorAssistantResultExtraction = false
	if err := validatePGVectorExtractionFlags([]string{"mem0"}); err != nil {
		t.Fatalf("irrelevant pgvector flags returned error: %v", err)
	}
	if err := validatePGVectorExtractionFlags([]string{"pgvector"}); err == nil {
		t.Fatal("unsupported pgvector policy passed early validation")
	}

	*flagPGVectorUpdatePolicy = "merge_similar"
	if err := validatePGVectorExtractionFlags(
		[]string{"pgvector", "mem0"},
	); err != nil {
		t.Fatalf("supported pgvector configuration returned error: %v", err)
	}
}

func TestBuildMemoryServiceOptionsAppliesPGVectorExtractionConfig(t *testing.T) {
	oldPolicy := *flagPGVectorUpdatePolicy
	oldAssistantResults := *flagPGVectorAssistantResultExtraction
	defer func() {
		*flagPGVectorUpdatePolicy = oldPolicy
		*flagPGVectorAssistantResultExtraction = oldAssistantResults
	}()

	*flagPGVectorUpdatePolicy = "merge_similar"
	*flagPGVectorAssistantResultExtraction = true
	opts, err := buildMemoryServiceOptions(memoryConfig{
		backend: "pgvector",
		mode:    memoryModeAuto,
	}, nil)
	if err != nil {
		t.Fatalf("build memory service options: %v", err)
	}
	if !opts.enableExtractor ||
		opts.pgvectorExtraction.UpdatePolicy !=
			pgvectorUpdatePolicyMergeSimilar ||
		!opts.pgvectorExtraction.AssistantResultExtraction {
		t.Fatalf("unexpected pgvector extraction options: %#v", opts)
	}
}
