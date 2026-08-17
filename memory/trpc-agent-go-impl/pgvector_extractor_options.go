//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "trpc.group/trpc-go/trpc-agent-go/memory/extractor"

func pgvectorExtractorOptions(
	config pgvectorExtractionConfig,
) ([]extractor.Option, error) {
	options := []extractor.Option{
		extractor.WithUpdatePolicy(extractor.UpdatePolicy(config.UpdatePolicy)),
	}
	if config.AssistantEpisodeExtraction {
		options = append(options, extractor.WithAssistantEpisodeExtraction())
	}
	return options, nil
}
