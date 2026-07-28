//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package experiment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type frozenTestDocument struct {
	Optimization frozenTestOptimization `json:"optimization"`
}

type frozenTestOptimization struct {
	RandomSeed       int64                `json:"randomSeed"`
	Repeats          int                  `json:"repeats"`
	ValidationScales []string             `json:"validationScales"`
	HoldoutScales    []string             `json:"holdoutScales"`
	SelectedChanged  bool                 `json:"selectedChanged"`
	Search           frozenTestSearch     `json:"search"`
	Comparison       frozenTestComparison `json:"comparison"`
}

type frozenTestSearch struct {
	Spec              map[string]any `json:"spec"`
	PromotionEligible bool           `json:"promotion_eligible"`
	PromotionReason   string         `json:"promotion_reason"`
}

type frozenTestComparison struct {
	Validation []frozenPair `json:"validation"`
	Holdout    []frozenPair `json:"holdout"`
}

func TestAggregateFrozenPassesIndependentCandidateConfirmation(t *testing.T) {
	protocol := DefaultFrozenProtocol()
	var paths []string
	for _, seed := range []int64{501, 502} {
		document := completeFrozenDocument(protocol, seed)
		path := filepath.Join(t.TempDir(), fmt.Sprintf("seed-%d", seed), "results.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		writeFrozenDocument(t, path, document)
		paths = append(paths, path)
	}

	evidence, err := AggregateFrozen(paths, protocol)
	require.NoError(t, err)
	require.True(t, evidence.PromotionEligible)
	require.Len(t, evidence.Sources, 2)
	require.Equal(t, 8, evidence.Holdout.Seed.Pairs)
	require.Equal(t, 4, evidence.Untouched.Seed.Pairs)
	require.Equal(t, 4, evidence.KnownRegression.Seed.Pairs)
	require.InDelta(t, -10, evidence.Holdout.Delta.AgentTokensPC, 0.001)
	require.True(t, gateByName(t, evidence.Gates, "meaningful-benefit").Passed)
}

func TestAggregateFrozenRejectsCandidateChangedBetweenSeeds(t *testing.T) {
	protocol := DefaultFrozenProtocol()
	first := completeFrozenDocument(protocol, 501)
	second := completeFrozenDocument(protocol, 502)
	second.Optimization.Search.Spec["description"] = "different candidate"
	firstPath := filepath.Join(t.TempDir(), "seed-501", "results.json")
	secondPath := filepath.Join(t.TempDir(), "seed-502", "results.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(firstPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(secondPath), 0o755))
	writeFrozenDocument(t, firstPath, first)
	writeFrozenDocument(t, secondPath, second)

	_, err := AggregateFrozen([]string{firstPath, secondPath}, protocol)
	require.ErrorContains(t, err, "multiple frozen candidates")
}

func TestAggregateFrozenV2UsesPrimarySafetyInsteadOfOptimizerScalarGate(t *testing.T) {
	protocol := DefaultFrozenProtocol()
	var paths []string
	for _, seed := range []int64{503, 504} {
		document := completeFrozenDocument(protocol, seed)
		document.Optimization.Search.PromotionEligible = false
		document.Optimization.Search.PromotionReason = "critical case regressed on scalar score"
		path := filepath.Join(t.TempDir(), fmt.Sprintf("seed-%d", seed), "results.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		writeFrozenDocument(t, path, document)
		paths = append(paths, path)
	}

	evidence, err := AggregateFrozen(paths, protocol)
	require.NoError(t, err)
	require.True(t, evidence.PromotionEligible)
	require.False(t, evidence.Sources[0].OptimizerPromotionEligible)
	require.True(t, gateByName(t, evidence.Gates, "validation-pair-primary-safety").Passed)
	require.True(t, gateByName(t, evidence.Gates, "untouched-pair-primary-safety").Passed)
}

func TestAggregateFrozenV2RejectsUntouchedPrimaryLoss(t *testing.T) {
	protocol := DefaultFrozenProtocol()
	var paths []string
	for _, seed := range []int64{505, 506} {
		document := completeFrozenDocument(protocol, seed)
		document.Optimization.Comparison.Holdout[0].Candidate.Objectives["official_quality"] = 0.94
		path := filepath.Join(t.TempDir(), fmt.Sprintf("seed-%d", seed), "results.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		writeFrozenDocument(t, path, document)
		paths = append(paths, path)
	}

	evidence, err := AggregateFrozen(paths, protocol)
	require.NoError(t, err)
	require.False(t, evidence.PromotionEligible)
	require.False(t, gateByName(t, evidence.Gates, "untouched-pair-primary-safety").Passed)
}

func TestAggregateFrozenV1PreservesOptimizerPromotionGate(t *testing.T) {
	protocol := FrozenProtocolV1()
	var paths []string
	for _, seed := range []int64{501, 502} {
		document := completeFrozenDocument(protocol, seed)
		document.Optimization.Search.PromotionEligible = false
		path := filepath.Join(t.TempDir(), fmt.Sprintf("seed-%d", seed), "results.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		writeFrozenDocument(t, path, document)
		paths = append(paths, path)
	}

	evidence, err := AggregateFrozen(paths, protocol)
	require.NoError(t, err)
	require.False(t, evidence.PromotionEligible)
	require.False(t, gateByName(t, evidence.Gates, "per-run-promotion").Passed)
}

func completeFrozenDocument(protocol FrozenProtocol, seed int64) frozenTestDocument {
	document := frozenTestDocument{Optimization: frozenTestOptimization{
		RandomSeed:       seed,
		Repeats:          protocol.Repeats,
		ValidationScales: protocol.ValidationScales,
		HoldoutScales:    protocol.HoldoutScales,
		SelectedChanged:  true,
		Search: frozenTestSearch{
			Spec:              map[string]any{"name": "recipe candidate", "description": "fixed"},
			PromotionEligible: true,
			PromotionReason:   "fixed comparison passed",
		},
	}}
	document.Optimization.Comparison.Validation = frozenTestPairs(
		"validation", "recipe-cookbook-builder", protocol.ValidationScales, protocol.Repeats,
	)
	document.Optimization.Comparison.Holdout = frozenTestPairs(
		"holdout", "recipe-cookbook-builder", protocol.HoldoutScales, protocol.Repeats,
	)
	return document
}

func frozenTestPairs(split, family string, scales []string, repeats int) []frozenPair {
	var pairs []frozenPair
	for _, scale := range scales {
		for repeat := 1; repeat <= repeats; repeat++ {
			pairs = append(pairs, frozenPair{
				CaseID: fmt.Sprintf("%s/%s/%s/r%d", split, family, scale, repeat),
				Seed:   int64(repeat),
				Baseline: frozenEvaluation{Score: 0.9, Objectives: map[string]float64{
					"passed": 1, "official_quality": 0.95, "agent_tokens": 1000,
					"tool_calls": 20, "duration_seconds": 100,
				}},
				Candidate: frozenEvaluation{Score: 0.91, Objectives: map[string]float64{
					"passed": 1, "official_quality": 0.95, "agent_tokens": 900,
					"tool_calls": 18, "duration_seconds": 90,
				}},
			})
		}
	}
	return pairs
}

func writeFrozenDocument(t *testing.T, path string, document frozenTestDocument) {
	t.Helper()
	payload, err := json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, payload, 0o644))
}
