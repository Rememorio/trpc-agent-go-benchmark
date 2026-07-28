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

func TestIsLongMemEvalInvocation(t *testing.T) {
	resultFlags := map[string]*string{
		"analyze":           flagLMEAnalyzeResults,
		"audit":             flagLMEAuditResults,
		"hydrate usage":     flagLMEHydrateLogicalUsageResults,
		"reanswer":          flagLMEReanswerResults,
		"refresh retrieval": flagLMERefreshRetrievalResults,
		"refresh snapshots": flagLMERefreshMemorySnapshots,
		"rerank":            flagLMERerankResults,
		"judge":             flagLMEJudgeResults,
		"compare":           flagLMECompareResults,
		"replicates":        flagLMECompareReplicates,
	}
	originalFormat := *flagDatasetFormat
	originalValues := make(map[*string]string, len(resultFlags))
	for _, target := range resultFlags {
		originalValues[target] = *target
		*target = ""
	}
	t.Cleanup(func() {
		*flagDatasetFormat = originalFormat
		for target, value := range originalValues {
			*target = value
		}
	})

	*flagDatasetFormat = datasetFormatLoCoMo
	if isLongMemEvalInvocation() {
		t.Fatal("default LoCoMo invocation routed to LongMemEval")
	}

	for name, target := range resultFlags {
		t.Run(name, func(t *testing.T) {
			*target = "results.json"
			if !isLongMemEvalInvocation() {
				t.Fatalf("%s operation did not route to LongMemEval", name)
			}
			if !isLongMemEvalResultOperation() {
				t.Fatalf("%s operation was not recognized as a result operation", name)
			}
			*target = ""
		})
	}

	*flagDatasetFormat = datasetFormatLongMemEval
	if !isLongMemEvalInvocation() {
		t.Fatal("LongMemEval dataset format did not route to LongMemEval")
	}
}
