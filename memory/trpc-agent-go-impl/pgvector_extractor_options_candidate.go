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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

func pgvectorExtractorOptions(
	config pgvectorExtractionConfig,
) ([]extractor.Option, error) {
	options := []extractor.Option{
		extractor.WithUpdatePolicy(extractor.UpdatePolicy(config.UpdatePolicy)),
		extractor.WithAssistantResultExtraction(
			config.AssistantResultExtraction,
		),
	}
	metadata := extractor.NewExtractor(nil, options...).Metadata()
	if got := metadata["update_policy"]; got != string(config.UpdatePolicy) {
		return nil, fmt.Errorf(
			"candidate build profile does not support update policy %q "+
				"(resolved to %q)",
			config.UpdatePolicy, got,
		)
	}
	if got := metadata["assistant_result_extraction"]; got != config.AssistantResultExtraction {
		return nil, fmt.Errorf(
			"candidate build profile does not support "+
				"assistant-result extraction=%t (resolved to %v)",
			config.AssistantResultExtraction, got,
		)
	}
	return options, nil
}
