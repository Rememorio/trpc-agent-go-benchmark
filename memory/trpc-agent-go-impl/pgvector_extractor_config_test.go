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
		pgvectorUpdatePolicyReconcile, false,
	); got != "" {
		t.Fatalf("disabled policy = %q, want empty", got)
	}
	if got := assistantResultUpdatePolicy(
		pgvectorUpdatePolicyReconcile, true,
	); got != assistantResultPolicyPreserving {
		t.Fatalf("reconcile result policy = %q", got)
	}
	if got := assistantResultUpdatePolicy(
		pgvectorUpdatePolicyAddOnly, true,
	); got != string(pgvectorUpdatePolicyAddOnly) {
		t.Fatalf("add-only result policy = %q", got)
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
		{input: "", want: pgvectorUpdatePolicyReconcile},
		{input: " RECONCILE ", want: pgvectorUpdatePolicyReconcile},
		{input: "HISTORY-PRESERVING", want: pgvectorUpdatePolicyHistoryPreserving},
		{input: "ADD-ONLY", want: pgvectorUpdatePolicyAddOnly},
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

	*flagPGVectorUpdatePolicy = "custom"
	if _, err := currentPGVectorExtractionConfig(); err == nil {
		t.Fatal("expected unsupported update policy error")
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

	*flagPGVectorUpdatePolicy = "reconcile"
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

	*flagPGVectorUpdatePolicy = "reconcile"
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
			pgvectorUpdatePolicyReconcile ||
		!opts.pgvectorExtraction.AssistantResultExtraction {
		t.Fatalf("unexpected pgvector extraction options: %#v", opts)
	}
}
