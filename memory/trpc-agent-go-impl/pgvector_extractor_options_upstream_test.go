//go:build lme_upstream

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
)

func TestLongMemEvalExtractorOptionsUpstream(t *testing.T) {
	t.Parallel()
	if options, err := pgvectorExtractorOptions(
		pgvectorExtractionConfig{UpdatePolicy: pgvectorUpdatePolicyReconcile},
	); err != nil || len(options) != 0 {
		t.Fatalf("default upstream options = %v, %v", options, err)
	}

	tests := []struct {
		name   string
		config pgvectorExtractionConfig
		want   string
	}{
		{
			name: "policy",
			config: pgvectorExtractionConfig{
				UpdatePolicy: pgvectorUpdatePolicyAddOnly,
			},
			want: "only supports update policy",
		},
		{
			name: "assistant results",
			config: pgvectorExtractionConfig{
				UpdatePolicy:              pgvectorUpdatePolicyReconcile,
				AssistantResultExtraction: true,
			},
			want: "does not support assistant-result extraction",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := pgvectorExtractorOptions(test.config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}
