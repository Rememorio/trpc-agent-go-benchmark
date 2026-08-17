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
	pgvectorUpdatePolicyMergeSimilar    pgvectorUpdatePolicy = "merge_similar"
	pgvectorUpdatePolicyPreserveHistory pgvectorUpdatePolicy = "preserve_history"
	pgvectorUpdatePolicyAppendOnly      pgvectorUpdatePolicy = "append_only"
)

type pgvectorExtractionConfig struct {
	UpdatePolicy               pgvectorUpdatePolicy `json:"update_policy"`
	AssistantEpisodeExtraction bool                 `json:"assistant_episode_extraction"`
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
	case "", string(pgvectorUpdatePolicyMergeSimilar):
		policy = pgvectorUpdatePolicyMergeSimilar
	case string(pgvectorUpdatePolicyPreserveHistory):
		policy = pgvectorUpdatePolicyPreserveHistory
	case string(pgvectorUpdatePolicyAppendOnly):
		policy = pgvectorUpdatePolicyAppendOnly
	default:
		return pgvectorExtractionConfig{}, fmt.Errorf(
			"unsupported pgvector-update-policy %q: expected merge_similar, "+
				"preserve_history, or append_only",
			*flagPGVectorUpdatePolicy,
		)
	}
	return pgvectorExtractionConfig{
		UpdatePolicy:               policy,
		AssistantEpisodeExtraction: *flagPGVectorAssistantEpisodeExtraction,
	}, nil
}
