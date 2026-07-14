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
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
)

func TestOptimizationHelpersAndOutputs(t *testing.T) {
	spec := validOptimizationSpec()
	firstID, err := optimizationCandidateID(spec)
	require.NoError(t, err)
	secondID, err := optimizationCandidateID(spec)
	require.NoError(t, err)
	require.Equal(t, firstID, secondID)

	result := &optimizationBenchmarkResult{
		FeedbackScales:   []string{"e1"},
		ValidationScales: []string{"e2"},
		HoldoutScales:    []string{"e3"},
		Repeats:          1,
		Result: &optimization.Result{
			Spec:                spec,
			CandidateCount:      2,
			MetricCalls:         8,
			StopReason:          "max_iterations",
			PromotionEligible:   true,
			PromotionReason:     "holdout requirements satisfied",
			BaselineValidation:  optimization.Summary{Score: 0.8, Cases: 1},
			CandidateValidation: optimization.Summary{Score: 0.9, Cases: 1},
			BaselineHoldout: optimization.Summary{
				Score: 0.7,
				Cases: 1,
				Objectives: map[string]float64{
					objectiveQuality: 0.8,
				},
			},
			CandidateHoldout: optimization.Summary{
				Score:      0.9,
				Cases:      1,
				Objectives: map[string]float64{objectiveQuality: 1},
			},
		},
	}
	benchmark := &benchmarkResult{
		Timestamp:     time.Now().Format(time.RFC3339),
		RequestedMode: modeOptimize,
		Model:         "model",
		ReviewerModel: "reviewer",
		Optimization:  result,
	}
	outputDir := filepath.Join(t.TempDir(), "nested", "output")
	require.NoError(t, writeBenchmarkOutputs(context.Background(), outputDir, benchmark))
	for _, name := range []string{"results.json", "REPORT.md"} {
		_, err := os.Stat(filepath.Join(outputDir, name))
		require.NoError(t, err)
	}
	savedPayload, err := os.ReadFile(filepath.Join(outputDir, "results.json"))
	require.NoError(t, err)
	var saved benchmarkResult
	require.NoError(t, json.Unmarshal(savedPayload, &saved))
	require.Equal(t, result.SeedSpecPath, saved.Optimization.SeedSpecPath)

	report, err := os.ReadFile(filepath.Join(outputDir, "REPORT.md"))
	require.NoError(t, err)
	require.Contains(t, string(report), "| Holdout | 0.7000 | 0.9000 | +0.2000 |")
	require.Contains(t, string(report), "| `official_quality` | higher | 0.8000 | 1.0000 | +0.2000 |")
	require.Contains(t, string(report), "Promotion eligible: `true`")
	require.Contains(t, string(report), "eligible for promotion")
	require.Equal(t, "lower", objectivePreference(objectiveAgentTokens))
	require.Equal(t, "higher", objectivePreference(objectivePassed))
	foundSkill := false
	require.NoError(t, filepath.Walk(filepath.Join(outputDir, "selected_skill"), func(
		path string,
		info os.FileInfo,
		err error,
	) error {
		if err == nil && info != nil && !info.IsDir() && info.Name() == "SKILL.md" {
			foundSkill = true
		}
		return err
	}))
	require.True(t, foundSkill)

	require.Error(t, writeBenchmarkOutputs(context.Background(), outputDir, nil))
	require.Error(t, writeReport(outputDir, nil))
	require.Error(t, writeBenchmarkOutputs(context.Background(), outputDir, &benchmarkResult{
		Optimization: &optimizationBenchmarkResult{},
	}))

	parentFile := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(parentFile, []byte("x"), 0o644))
	require.Error(t, writeBenchmarkOutputs(
		context.Background(), filepath.Join(parentFile, "child"), benchmark,
	))

	jsonFailureDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(jsonFailureDir, "results.json"), 0o755))
	require.Error(t, writeBenchmarkOutputs(context.Background(), jsonFailureDir, benchmark))

	publisherFailureDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(publisherFailureDir, "selected_skill"), []byte("x"), 0o644,
	))
	require.ErrorContains(t,
		writeBenchmarkOutputs(context.Background(), publisherFailureDir, benchmark),
		"write selected skill",
	)

	reportFailureDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(reportFailureDir, "REPORT.md"), 0o755))
	require.ErrorContains(t,
		writeBenchmarkOutputs(context.Background(), reportFailureDir, benchmark),
		"write benchmark report",
	)
}
