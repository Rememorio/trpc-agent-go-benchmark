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
)

func TestPGVectorExtractorOptions(t *testing.T) {
	for _, test := range []struct {
		name              string
		assistantEpisodes bool
		wantOptions       int
	}{
		{name: "ordinary", wantOptions: 1},
		{name: "assistant episodes", assistantEpisodes: true, wantOptions: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, err := pgvectorExtractorOptions(pgvectorExtractionConfig{
				UpdatePolicy:               pgvectorUpdatePolicyPreserveHistory,
				AssistantEpisodeExtraction: test.assistantEpisodes,
			})
			if err != nil {
				t.Fatalf("pgvector extractor options: %v", err)
			}
			if len(options) != test.wantOptions {
				t.Fatalf("option count = %d, want %d", len(options), test.wantOptions)
			}
		})
	}
}
