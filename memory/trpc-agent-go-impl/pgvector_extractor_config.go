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
	"strings"
)

type pgvectorUpdatePolicy string

const (
	pgvectorUpdatePolicyReconcile pgvectorUpdatePolicy = "reconcile"
	pgvectorUpdatePolicyAddOnly   pgvectorUpdatePolicy = "add-only"

	assistantResultPolicyPreserving = "assistant-result-preserving"
)

type pgvectorExtractionConfig struct {
	UpdatePolicy                pgvectorUpdatePolicy `json:"update_policy"`
	AssistantResultExtraction   bool                 `json:"assistant_result_extraction"`
	AssistantResultUpdatePolicy string               `json:"assistant_result_update_policy,omitempty"`
}

func validatePGVectorExtractionFlags(backends []string) error {
	usesPGVector := false
	for _, backend := range backends {
		if backend == "pgvector" {
			usesPGVector = true
			break
		}
	}
	if !usesPGVector {
		return nil
	}
	config, err := currentPGVectorExtractionConfig()
	if err != nil {
		return err
	}
	_, err = pgvectorExtractorOptions(config)
	return err
}

func currentPGVectorExtractionConfig() (
	pgvectorExtractionConfig,
	error,
) {
	var policy pgvectorUpdatePolicy
	switch strings.ToLower(strings.TrimSpace(*flagPGVectorUpdatePolicy)) {
	case "", string(pgvectorUpdatePolicyReconcile):
		policy = pgvectorUpdatePolicyReconcile
	case string(pgvectorUpdatePolicyAddOnly):
		policy = pgvectorUpdatePolicyAddOnly
	default:
		return pgvectorExtractionConfig{}, fmt.Errorf(
			"unsupported pgvector-update-policy %q: expected reconcile or add-only",
			*flagPGVectorUpdatePolicy,
		)
	}
	return pgvectorExtractionConfig{
		UpdatePolicy:              policy,
		AssistantResultExtraction: *flagPGVectorAssistantResultExtraction,
		AssistantResultUpdatePolicy: assistantResultUpdatePolicy(
			policy, *flagPGVectorAssistantResultExtraction,
		),
	}, nil
}

func assistantResultUpdatePolicy(
	policy pgvectorUpdatePolicy,
	enabled bool,
) string {
	if !enabled {
		return ""
	}
	if policy == pgvectorUpdatePolicyAddOnly {
		return string(pgvectorUpdatePolicyAddOnly)
	}
	return assistantResultPolicyPreserving
}
