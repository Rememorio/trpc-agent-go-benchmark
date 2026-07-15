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

func TestAggregatePassesCompleteImprovingMatrix(t *testing.T) {
	protocol := DefaultProtocol()
	var paths []string
	for run, seed := range []int64{10, 11, 12} {
		input := completeInput(protocol, seed)
		path := filepath.Join(t.TempDir(), fmt.Sprintf("run-%d", run), "results.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		writeInput(t, path, input)
		paths = append(paths, path)
	}

	evidence, err := Aggregate(paths, protocol)
	require.NoError(t, err)
	require.True(t, evidence.PromotionEligible)
	require.Equal(t, 3, evidence.Coverage.Runs)
	require.Equal(t, 30, evidence.Coverage.TasksPerArm)
	require.Equal(t, 90, evidence.OptimizedEvolutionVsEvolution.Pairs.Pairs)
	require.Equal(t, 90, evidence.OptimizedEvolutionVsEvolution.Pairs.QualityWins)
	require.InDelta(t, 1.0, evidence.OptimizedEvolutionVsEvolution.Delta.QualityPP, 0.001)
	require.InDelta(t, -11.11, evidence.OptimizedEvolutionVsEvolution.Delta.EndToEndTokensPC, 0.001)
}

func TestAggregateRejectsFamilyPassRegression(t *testing.T) {
	protocol := DefaultProtocol()
	var paths []string
	for run, seed := range []int64{10, 11, 12} {
		input := completeInput(protocol, seed)
		if run == 0 {
			input.OptimizedEvolution.Cases[0].Evaluation.Passed = false
		}
		path := filepath.Join(t.TempDir(), fmt.Sprintf("run-%d", run), "results.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		writeInput(t, path, input)
		paths = append(paths, path)
	}

	evidence, err := Aggregate(paths, protocol)
	require.NoError(t, err)
	require.False(t, evidence.PromotionEligible)
	require.False(t, gateByName(t, evidence.Gates, "family-pass-non-regression").Passed)
}

func TestAggregateRejectsUnpairedTaskSeed(t *testing.T) {
	protocol := DefaultProtocol()
	input := completeInput(protocol, 10)
	badSeed := int64(999)
	input.OptimizedEvolution.Cases[0].EvaluationSeed = &badSeed
	path := filepath.Join(t.TempDir(), "results.json")
	writeInput(t, path, input)

	_, err := Aggregate([]string{path}, protocol)
	require.ErrorContains(t, err, "not seed-paired across arms")
}

func completeInput(protocol Protocol, rootSeed int64) inputResult {
	input := inputResult{
		EvaluationSeed:     &rootSeed,
		RunOrder:           []string{armBaseline, armEvolution, armOptimized},
		Baseline:           &inputArm{},
		Evolution:          &inputArm{},
		OptimizedEvolution: &inputArm{},
	}
	index := int64(0)
	for _, family := range protocol.ExpectedFamilies {
		for _, scale := range protocol.ExpectedScales {
			index++
			seed := rootSeed*100 + index
			input.Baseline.Cases = append(input.Baseline.Cases,
				testCase(family, scale, seed, 90, 1000, 0))
			input.Evolution.Cases = append(input.Evolution.Cases,
				testCase(family, scale, seed, 95, 800, 100))
			input.OptimizedEvolution.Cases = append(input.OptimizedEvolution.Cases,
				testCase(family, scale, seed, 96, 700, 100))
		}
	}
	return input
}

func testCase(
	family, scale string,
	seed int64,
	quality float64,
	agentTokens, reviewerTokens int,
) inputCase {
	evaluation := &inputEval{Passed: true}
	evaluation.Score.Percent = quality
	return inputCase{
		TaskID:              family + "/" + scale,
		BaseTask:            family,
		Scale:               scale,
		EvaluationSeed:      &seed,
		DurationSeconds:     10,
		TotalTokens:         agentTokens,
		ReviewerTotalTokens: reviewerTokens,
		EndToEndTotalTokens: agentTokens + reviewerTokens,
		ToolCalls:           []string{"one", "two"},
		Evaluation:          evaluation,
	}
}

func writeInput(t *testing.T, path string, input inputResult) {
	t.Helper()
	payload, err := json.Marshal(input)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, payload, 0o644))
}

func gateByName(t *testing.T, gates []GateCheck, name string) GateCheck {
	t.Helper()
	for _, gate := range gates {
		if gate.Name == name {
			return gate
		}
	}
	t.Fatalf("gate %q not found", name)
	return GateCheck{}
}
