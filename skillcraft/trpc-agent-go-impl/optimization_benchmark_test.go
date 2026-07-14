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
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type fakeOptimizationRunner struct {
	result  *optimization.Result
	err     error
	request *optimization.Request
}

func (r *fakeOptimizationRunner) Optimize(
	_ context.Context,
	req optimization.Request,
) (*optimization.Result, error) {
	r.request = &req
	return r.result, r.err
}

func TestBuildOptimizationBenchmarkConfig(t *testing.T) {
	seedPath := filepath.Join(t.TempDir(), "seed.json")
	require.NoError(t, os.WriteFile(seedPath, []byte(`{}`), 0o644))
	setOptimizationFlagsForTest(t)
	*flagOptimizationSeedSpec = seedPath
	*flagOptimizationFeedbackScales = " e1, e2 "
	*flagOptimizationValidationScales = "e3,m1"
	*flagOptimizationHoldoutScales = "m2,h1"
	*flagOptimizationRepeats = 2
	*flagOptimizationMaxIterations = 3
	*flagOptimizationReflectionBatch = 2
	*flagOptimizationMaxMetricCalls = 40
	*flagOptimizationRandomSeed = 17
	*flagOptimizationTimeLimitSeconds = 9
	*flagOptimizationTokenBudget = 2000

	cfg, err := buildOptimizationBenchmarkConfig()
	require.NoError(t, err)
	require.Equal(t, []string{"e1", "e2"}, cfg.FeedbackScales)
	require.Equal(t, 2, cfg.Repeats)
	require.Equal(t, int64(17), cfg.RandomSeed)
	require.Equal(t, 9*time.Second, cfg.TimeLimit)
	require.Equal(t, 2000, cfg.TokenBudget)

	*flagOptimizationHoldoutScales = "e2"
	_, err = buildOptimizationBenchmarkConfig()
	require.ErrorContains(t, err, "appears in both")
}

func TestBuildOptimizationBenchmarkConfigRejectsInvalidValues(t *testing.T) {
	setOptimizationFlagsForTest(t)
	*flagOptimizationSeedSpec = ""
	_, err := buildOptimizationBenchmarkConfig()
	require.ErrorContains(t, err, "requires -optimization-seed-spec")

	*flagOptimizationSeedSpec = t.TempDir()
	_, err = buildOptimizationBenchmarkConfig()
	require.ErrorContains(t, err, "invalid optimization seed spec")

	seedPath := filepath.Join(t.TempDir(), "seed.json")
	require.NoError(t, os.WriteFile(seedPath, []byte(`{}`), 0o644))
	*flagOptimizationSeedSpec = seedPath
	*flagOptimizationFeedbackScales = "e1"
	*flagOptimizationValidationScales = "e2"
	*flagOptimizationHoldoutScales = "e3"
	tests := []struct {
		name    string
		prepare func()
		want    string
	}{
		{"repeats", func() { *flagOptimizationRepeats = 0 }, "repeats must be positive"},
		{"iterations", func() { *flagOptimizationMaxIterations = -1 }, "iterations must not be negative"},
		{"batch", func() { *flagOptimizationReflectionBatch = 0 }, "batch size must be positive"},
		{"metric calls", func() { *flagOptimizationMaxMetricCalls = -1 }, "metric calls must not be negative"},
		{"token budget", func() { *flagOptimizationTokenBudget = 0 }, "token budget must be positive"},
		{"empty split", func() { *flagOptimizationHoldoutScales = "" }, "splits must be non-empty"},
	}
	for _, test := range tests {
		*flagOptimizationRepeats = 1
		*flagOptimizationMaxIterations = 1
		*flagOptimizationReflectionBatch = 1
		*flagOptimizationMaxMetricCalls = 10
		*flagOptimizationTokenBudget = 1000
		*flagOptimizationHoldoutScales = "e3"
		test.prepare()
		_, err = buildOptimizationBenchmarkConfig()
		require.ErrorContains(t, err, test.want, test.name)
	}
}

func TestRunOptimizationBenchmarkWithFactory(t *testing.T) {
	seedPath := filepath.Join(t.TempDir(), "seed.json")
	payload := `{"name":"Weather","description":"desc","when_to_use":"when","steps":["step"]}`
	require.NoError(t, os.WriteFile(seedPath, []byte(payload), 0o644))
	tasks := []*taskDefinition{
		optimizationTask("weather/e1", "weather", "e1"),
		optimizationTask("weather/e2", "weather", "e2"),
		optimizationTask("weather/e3", "weather", "e3"),
	}
	outputDir := t.TempDir()
	cfg := &benchmarkConfig{
		ModelName:         "agent-model",
		ReviewerModelName: "reflection-model",
		OutputDir:         outputDir,
		Optimization: &optimizationBenchmarkConfig{
			SeedSpecPath:     seedPath,
			FeedbackScales:   []string{"e1"},
			ValidationScales: []string{"e2"},
			HoldoutScales:    []string{"e3"},
			Repeats:          1,
			MaxIterations:    1,
			ReflectionBatch:  1,
			MaxMetricCalls:   10,
			RandomSeed:       7,
			TimeLimit:        time.Second,
			TokenBudget:      1000,
		},
	}
	runner := &fakeOptimizationRunner{result: &optimization.Result{
		Spec:                validOptimizationSpec(),
		BaselineValidation:  optimization.Summary{Score: 0.8, Cases: 1},
		CandidateValidation: optimization.Summary{Score: 0.9, Cases: 1},
		BaselineHoldout:     optimization.Summary{Score: 0.8, Cases: 1},
		CandidateHoldout:    optimization.Summary{Score: 0.9, Cases: 1},
		CandidateCount:      2,
		MetricCalls:         8,
		StopReason:          "max_iterations",
	}}
	factory := func(
		reflectionModel model.Model,
		evaluator optimization.Evaluator,
		opts ...optimization.Option,
	) (optimizationRunner, error) {
		require.NotNil(t, reflectionModel)
		require.NotNil(t, evaluator)
		require.NotEmpty(t, opts)
		return runner, nil
	}
	saved, err := runOptimizationBenchmarkWithFactory(
		context.Background(), cfg, tasks, factory,
	)
	require.NoError(t, err)
	require.NotNil(t, runner.request)
	require.Equal(t, "Weather", runner.request.Seed.Name)
	require.Len(t, runner.request.Dataset.Feedback, 1)
	require.Equal(t, filepath.Base(seedPath), saved.SeedSpecPath)
	require.True(t, saved.SelectedChanged)

	factoryErr := errors.New("factory failed")
	_, err = runOptimizationBenchmarkWithFactory(
		context.Background(), cfg, tasks,
		func(model.Model, optimization.Evaluator, ...optimization.Option) (optimizationRunner, error) {
			return nil, factoryErr
		},
	)
	require.ErrorIs(t, err, factoryErr)

	runner.err = errors.New("optimize failed")
	_, err = runOptimizationBenchmarkWithFactory(context.Background(), cfg, tasks, factory)
	require.ErrorIs(t, err, runner.err)
}

func setOptimizationFlagsForTest(t *testing.T) {
	t.Helper()
	seedSpec := *flagOptimizationSeedSpec
	feedback := *flagOptimizationFeedbackScales
	validation := *flagOptimizationValidationScales
	holdout := *flagOptimizationHoldoutScales
	repeats := *flagOptimizationRepeats
	iterations := *flagOptimizationMaxIterations
	batch := *flagOptimizationReflectionBatch
	metricCalls := *flagOptimizationMaxMetricCalls
	randomSeed := *flagOptimizationRandomSeed
	timeLimit := *flagOptimizationTimeLimitSeconds
	tokenBudget := *flagOptimizationTokenBudget
	t.Cleanup(func() {
		*flagOptimizationSeedSpec = seedSpec
		*flagOptimizationFeedbackScales = feedback
		*flagOptimizationValidationScales = validation
		*flagOptimizationHoldoutScales = holdout
		*flagOptimizationRepeats = repeats
		*flagOptimizationMaxIterations = iterations
		*flagOptimizationReflectionBatch = batch
		*flagOptimizationMaxMetricCalls = metricCalls
		*flagOptimizationRandomSeed = randomSeed
		*flagOptimizationTimeLimitSeconds = timeLimit
		*flagOptimizationTokenBudget = tokenBudget
	})
}

func TestBuildOptimizationDatasetUsesDisjointScaleSplits(t *testing.T) {
	tasks := []*taskDefinition{
		optimizationTask("weather/e1", "weather", "e1"),
		optimizationTask("weather/e2", "weather", "e2"),
		optimizationTask("weather/e3", "weather", "e3"),
	}
	dataset, err := buildOptimizationDataset(tasks, optimizationBenchmarkConfig{
		FeedbackScales:   []string{"e1"},
		ValidationScales: []string{"e2"},
		HoldoutScales:    []string{"e3"},
		Repeats:          2,
	})
	require.NoError(t, err)
	require.Len(t, dataset.Feedback, 2)
	require.Len(t, dataset.Validation, 2)
	require.Len(t, dataset.Holdout, 2)
	require.Equal(t, "feedback/weather/e1/r1", dataset.Feedback[0].ID)
	require.Equal(t, "weather/e1", dataset.Feedback[0].Metadata["task_id"])
	require.True(t, dataset.Holdout[0].Critical)
}

func TestBuildOptimizationDatasetRejectsMixedFamilies(t *testing.T) {
	_, err := buildOptimizationDataset([]*taskDefinition{
		optimizationTask("weather/e1", "weather", "e1"),
		optimizationTask("recipe/e2", "recipe", "e2"),
	}, optimizationBenchmarkConfig{
		FeedbackScales:   []string{"e1"},
		ValidationScales: []string{"e2"},
		HoldoutScales:    []string{"e3"},
		Repeats:          1,
	})
	require.ErrorContains(t, err, "expected one family")
}

func TestBuildOptimizationDatasetRejectsInvalidSelections(t *testing.T) {
	_, err := buildOptimizationDataset(nil, optimizationBenchmarkConfig{})
	require.ErrorContains(t, err, "at least one task")

	tasks := []*taskDefinition{
		optimizationTask("weather/e1", "weather", "e1"),
		optimizationTask("weather/e1-copy", "weather", "e1"),
	}
	_, err = buildOptimizationDataset(tasks, optimizationBenchmarkConfig{
		FeedbackScales:   []string{"e1"},
		ValidationScales: []string{"e2"},
		HoldoutScales:    []string{"e3"},
		Repeats:          1,
	})
	require.ErrorContains(t, err, "duplicate optimization scale")

	_, err = buildOptimizationDataset(tasks[:1], optimizationBenchmarkConfig{
		FeedbackScales:   []string{"missing"},
		ValidationScales: []string{"e1"},
		HoldoutScales:    []string{"e3"},
		Repeats:          1,
	})
	require.ErrorContains(t, err, "was not selected")
}

func TestValidateDisjointScaleSplits(t *testing.T) {
	err := validateDisjointScaleSplits(optimizationBenchmarkConfig{
		FeedbackScales:   []string{"e1", "e2"},
		ValidationScales: []string{"e2", "m1"},
		HoldoutScales:    []string{"m2"},
	})
	require.ErrorContains(t, err, "appears in both")
}

func validOptimizationSpec() *evolution.SkillSpec {
	return &evolution.SkillSpec{
		Name:        "Weather",
		Description: "Collect weather data.",
		WhenToUse:   "Use for weather tasks.",
		Steps:       []string{"Collect the data.", "Write the result."},
		Pitfalls:    []string{"Validate the output."},
	}
}

func optimizationTask(id, family, scale string) *taskDefinition {
	return &taskDefinition{
		ID:          id,
		BaseTask:    family,
		Scale:       scale,
		Description: id,
		TaskDoc:     "Save results to `result.json`:",
	}
}

func optimizationCase(id string) optimization.Case {
	return optimization.Case{ID: id}
}
