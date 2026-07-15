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
	if options, err := longMemEvalExtractorOptions(
		lmePGVectorExtractionConfig{UpdatePolicy: lmeUpdatePolicyReconcile},
	); err != nil || len(options) != 0 {
		t.Fatalf("default upstream options = %v, %v", options, err)
	}

	tests := []struct {
		name   string
		config lmePGVectorExtractionConfig
		want   string
	}{
		{
			name: "policy",
			config: lmePGVectorExtractionConfig{
				UpdatePolicy: lmeUpdatePolicyHistoryPreserving,
			},
			want: "only supports update policy",
		},
		{
			name: "assistant results",
			config: lmePGVectorExtractionConfig{
				UpdatePolicy:              lmeUpdatePolicyReconcile,
				AssistantResultExtraction: true,
			},
			want: "does not support assistant-result extraction",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := longMemEvalExtractorOptions(test.config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}
