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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

func pgvectorExtractorOptions(
	config pgvectorExtractionConfig,
) ([]extractor.Option, error) {
	if config.UpdatePolicy != pgvectorUpdatePolicyReconcile {
		return nil, fmt.Errorf(
			"upstream build profile only supports update policy %q",
			pgvectorUpdatePolicyReconcile,
		)
	}
	if config.AssistantResultExtraction {
		return nil, fmt.Errorf(
			"upstream build profile does not support assistant-result extraction",
		)
	}
	return nil, nil
}
