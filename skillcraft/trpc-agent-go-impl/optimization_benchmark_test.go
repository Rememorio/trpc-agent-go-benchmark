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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
	"trpc.group/trpc-go/trpc-agent-go/model"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
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
		Optimization: optimizationBenchmarkConfig{
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
	require.NoError(t, runOptimizationBenchmarkWithFactory(
		context.Background(), cfg, tasks, factory,
	))
	require.NotNil(t, runner.request)
	require.Equal(t, "Weather", runner.request.Seed.Name)
	require.Len(t, runner.request.Dataset.Feedback, 1)
	require.FileExists(t, filepath.Join(outputDir, "optimization_result.json"))
	savedPayload, err := os.ReadFile(filepath.Join(outputDir, "optimization_result.json"))
	require.NoError(t, err)
	var saved optimizationBenchmarkResult
	require.NoError(t, json.Unmarshal(savedPayload, &saved))
	require.Equal(t, filepath.Base(seedPath), saved.SeedSpecPath)
	require.True(t, saved.SelectedChanged)

	factoryErr := errors.New("factory failed")
	err = runOptimizationBenchmarkWithFactory(
		context.Background(), cfg, tasks,
		func(model.Model, optimization.Evaluator, ...optimization.Option) (optimizationRunner, error) {
			return nil, factoryErr
		},
	)
	require.ErrorIs(t, err, factoryErr)

	runner.err = errors.New("optimize failed")
	err = runOptimizationBenchmarkWithFactory(context.Background(), cfg, tasks, factory)
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

func TestOptimizationScorePreservesPassBoundary(t *testing.T) {
	require.Less(t,
		optimizationScore(1, false, 0, 1000),
		optimizationScore(0, true, 1000, 1000),
	)
	require.Greater(t,
		optimizationScore(1, true, 100, 1000),
		optimizationScore(1, true, 900, 1000),
	)
	require.InDelta(t, 0, optimizationScore(0, false, 0, 1000), 0.0001)
	require.LessOrEqual(t, optimizationScore(1, true, 0, 1000), 1.0)
}

func TestBuildOptimizationEvaluationIncludesActionableSignals(t *testing.T) {
	evaluation := buildOptimizationEvaluation(
		optimizationCase("case-1"),
		&evolution.SkillSpec{Name: "Weather"},
		&runStats{
			TotalTokens:        100,
			ToolCalls:          []string{"weather_get", "weather_get"},
			ToolCallSignatures: []string{"weather_get#same", "weather_get#same"},
			LoadedSkillNames:   []string{"Weather"},
			ClaimDoneCalled:    true,
			SkillToolInvoked:   true,
		},
		nil,
		&officialEval{Passed: true, Status: "pass", Score: scorePayload{Percent: 95}},
		nil,
		"",
		0,
		1000,
	)
	require.Equal(t, "case-1", evaluation.CaseID)
	require.Greater(t, evaluation.Score, 0.8)
	require.Equal(t, 0.95, evaluation.Objectives[objectiveQuality])
	require.Equal(t, 1.0, evaluation.Objectives[objectivePassed])
	require.Equal(t, 1.0, evaluation.Objectives[objectiveSkillLoaded])
	require.Contains(t, evaluation.Feedback, "Repeated tool calls")
}

func TestRepeatedToolCallSummaryUsesArgumentIdentity(t *testing.T) {
	first := toolCallSignature("weather_get", []byte(`{"city":"Tokyo"}`))
	second := toolCallSignature("weather_get", []byte(`{"city":"London"}`))
	require.Empty(t, repeatedToolCallSummary([]string{first, second}))
	require.Equal(t, "weather_get x2", repeatedToolCallSummary([]string{first, first}))
	require.Equal(t,
		"weather_get: 2 exact argument sets repeated (4 calls)",
		repeatedToolCallSummary([]string{first, first, second, second}),
	)
}

func TestBuildOptimizationEvaluationTreatsAgentErrorAsFailure(t *testing.T) {
	evaluation := buildOptimizationEvaluation(
		optimizationCase("case-1"),
		&evolution.SkillSpec{Name: "Weather"},
		&runStats{},
		errors.New("max tool iterations exceeded"),
		&officialEval{Passed: true, Status: "pass", Score: scorePayload{Percent: 100}},
		nil,
		"",
		0,
		1000,
	)
	require.Less(t, evaluation.Score, 0.8)
	require.Equal(t, 0.0, evaluation.Objectives[objectivePassed])
	require.Contains(t, evaluation.Feedback, "max tool iterations")
}

func TestOutcomeFromEvalNormalizesOfficialPercent(t *testing.T) {
	outcome := outcomeFromEval(nil, &officialEval{
		Passed: true,
		Status: "pass",
		Score:  scorePayload{Percent: 95},
	}, nil)
	require.NotNil(t, outcome.Score)
	require.InDelta(t, 0.95, *outcome.Score, 0.0001)
}

func TestOptimizationModeKeepsConfiguredCompletionBudget(t *testing.T) {
	task := &taskDefinition{MaxTurns: 100}
	require.Equal(t, 8192, completionTokenBudget(task, modeOptimize, 8192))
	require.Equal(t, 2048, completionTokenBudget(task, modeEvolution, 8192))
}

func TestOptimizationInstructionExercisesNonConflictingCandidateDetails(t *testing.T) {
	instruction := buildInstructionForMode(
		&taskDefinition{},
		"workspace",
		[]string{"Recipe Cookbook"},
		modeOptimize,
	)
	require.Contains(t, instruction, "evaluates the loaded managed skill")
	require.Contains(t, instruction, "minimal example schema does not cancel")
	require.NotContains(t,
		buildInstruction(&taskDefinition{}, "workspace", []string{"Recipe Cookbook"}),
		"evaluates the loaded managed skill",
	)
}

func TestEvaluateTaskWithContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := evaluateTaskWithContext(
		ctx,
		&benchmarkConfig{SkillCraftRoot: t.TempDir()},
		&taskDefinition{EvaluationScript: "evaluate.py", Dir: t.TempDir()},
		t.TempDir(),
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestLoadOptimizationSeedSpec(t *testing.T) {
	dir := t.TempDir()
	validPath := filepath.Join(dir, "valid.json")
	spec := `{"name":"Weather","description":"desc","when_to_use":"when","steps":["step"]}`
	require.NoError(t, os.WriteFile(validPath, []byte(spec), 0o644))
	loaded, err := loadOptimizationSeedSpec(validPath)
	require.NoError(t, err)
	require.Equal(t, "Weather", loaded.Name)

	trailingPath := filepath.Join(dir, "trailing.json")
	require.NoError(t, os.WriteFile(trailingPath, []byte(spec+` {}`), 0o644))
	_, err = loadOptimizationSeedSpec(trailingPath)
	require.ErrorContains(t, err, "trailing JSON")

	unknownPath := filepath.Join(dir, "unknown.json")
	require.NoError(t, os.WriteFile(unknownPath, []byte(`{"unknown":true}`), 0o644))
	_, err = loadOptimizationSeedSpec(unknownPath)
	require.ErrorContains(t, err, "unknown field")

	_, err = loadOptimizationSeedSpec(filepath.Join(dir, "missing.json"))
	require.ErrorContains(t, err, "open optimization seed spec")
}

func TestSkillCraftOptimizationEvaluator(t *testing.T) {
	task := optimizationTask("weather/e1", "weather", "e1")
	cfg := &benchmarkConfig{
		OutputDir: t.TempDir(),
		Optimization: optimizationBenchmarkConfig{
			TokenBudget: 1000,
		},
	}
	evaluator := newSkillCraftOptimizationEvaluator(cfg, []*taskDefinition{task})
	evaluator.execute = func(
		ctx context.Context,
		_ *benchmarkConfig,
		gotTask *taskDefinition,
		mode runMode,
		_ string,
		_ *skill.FSRepository,
		_ *sessioninmemory.SessionService,
		_, _, _ string,
	) (*runStats, error) {
		require.NoError(t, ctx.Err())
		require.Same(t, task, gotTask)
		require.Equal(t, modeOptimize, mode)
		return &runStats{
			TotalTokens:      100,
			ToolCalls:        []string{"weather_get"},
			LoadedSkillNames: []string{"Weather"},
			FinalResponse:    "done",
		}, nil
	}
	evaluator.evaluate = func(
		context.Context,
		*benchmarkConfig,
		*taskDefinition,
		string,
	) (*officialEval, error) {
		return &officialEval{Passed: true, Status: "pass", Score: scorePayload{Percent: 100}}, nil
	}
	candidate := validOptimizationSpec()
	results, err := evaluator.Evaluate(context.Background(), candidate, []optimization.Case{{
		ID:       "feedback/weather/e1/r1",
		Metadata: map[string]string{"task_id": task.ID},
	}}, 7)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "done", results[0].Output)
	require.Equal(t, 100, evaluator.snapshotAgentTokens())
	require.Equal(t, 2, evaluator.allocateRunID())
	evaluator.addAgentTokens(nil)

	_, err = evaluator.Evaluate(context.Background(), nil, nil, 7)
	require.ErrorContains(t, err, "nil optimization candidate")
	_, err = evaluator.Evaluate(context.Background(), candidate, []optimization.Case{{
		ID:       "missing",
		Metadata: map[string]string{"task_id": "missing"},
	}}, 7)
	require.ErrorContains(t, err, "unknown optimization task")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = evaluator.Evaluate(cancelled, candidate, []optimization.Case{{
		ID:       "cancelled",
		Metadata: map[string]string{"task_id": task.ID},
	}}, 7)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSkillCraftOptimizationEvaluatorFailurePaths(t *testing.T) {
	task := optimizationTask("weather/e1", "weather", "e1")
	caseInput := []optimization.Case{{
		ID:       "feedback/weather/e1/r1",
		Metadata: map[string]string{"task_id": task.ID},
	}}
	candidate := validOptimizationSpec()

	outputFile := filepath.Join(t.TempDir(), "output-file")
	require.NoError(t, os.WriteFile(outputFile, []byte("x"), 0o644))
	evaluator := newSkillCraftOptimizationEvaluator(&benchmarkConfig{
		OutputDir:    outputFile,
		Optimization: optimizationBenchmarkConfig{TokenBudget: 1000},
	}, []*taskDefinition{task})
	_, err := evaluator.Evaluate(context.Background(), candidate, caseInput, 7)
	require.ErrorContains(t, err, "write optimization candidate")

	task.InitialWorkspace = filepath.Join(t.TempDir(), "missing")
	evaluator = newSkillCraftOptimizationEvaluator(&benchmarkConfig{
		OutputDir:    t.TempDir(),
		Optimization: optimizationBenchmarkConfig{TokenBudget: 1000},
	}, []*taskDefinition{task})
	_, err = evaluator.Evaluate(context.Background(), candidate, caseInput, 7)
	require.ErrorContains(t, err, "prepare optimization workspace")

	task.InitialWorkspace = ""
	evaluator = newSkillCraftOptimizationEvaluator(&benchmarkConfig{
		OutputDir:    t.TempDir(),
		Optimization: optimizationBenchmarkConfig{TokenBudget: 1000},
	}, []*taskDefinition{task})
	evaluator.execute = func(
		context.Context,
		*benchmarkConfig,
		*taskDefinition,
		runMode,
		string,
		*skill.FSRepository,
		*sessioninmemory.SessionService,
		string,
		string,
		string,
	) (*runStats, error) {
		return &runStats{TotalTokens: 10}, errors.New("agent failed")
	}
	evaluator.evaluate = func(
		context.Context,
		*benchmarkConfig,
		*taskDefinition,
		string,
	) (*officialEval, error) {
		return nil, errors.New("evaluator failed")
	}
	results, err := evaluator.Evaluate(context.Background(), candidate, caseInput, 7)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Zero(t, results[0].Objectives[objectivePassed])
	require.Contains(t, results[0].Feedback, "agent failed")
	require.Contains(t, results[0].Feedback, "evaluator failed")
}

func TestOptimizationHelpersAndOutputs(t *testing.T) {
	spec := validOptimizationSpec()
	firstID, err := optimizationCandidateID(spec)
	require.NoError(t, err)
	secondID, err := optimizationCandidateID(spec)
	require.NoError(t, err)
	require.Equal(t, firstID, secondID)

	result := &optimizationBenchmarkResult{
		Model:            "model",
		ReflectionModel:  "reviewer",
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
	outputDir := filepath.Join(t.TempDir(), "nested", "output")
	require.NoError(t, writeOptimizationOutputs(context.Background(), outputDir, result))
	for _, name := range []string{"optimization_result.json", "OPTIMIZATION_REPORT.md"} {
		_, err := os.Stat(filepath.Join(outputDir, name))
		require.NoError(t, err)
	}
	report, err := os.ReadFile(filepath.Join(outputDir, "OPTIMIZATION_REPORT.md"))
	require.NoError(t, err)
	require.Contains(t, string(report), "| Holdout | 0.7000 | 0.9000 | +0.2000 |")
	require.Contains(t, string(report), "| `official_quality` | higher | 0.8000 | 1.0000 | +0.2000 |")
	require.Contains(t, string(report), "Promotion eligible: `true`")
	require.Equal(t, "lower", objectivePreference(objectiveAgentTokens))
	require.Equal(t, "higher", objectivePreference(objectivePassed))
	foundSkill := false
	require.NoError(t, filepath.Walk(filepath.Join(outputDir, "optimized_skill"), func(
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

	require.Error(t, writeOptimizationOutputs(context.Background(), outputDir, nil))
	require.Error(t, writeOptimizationReport(filepath.Join(outputDir, "bad.md"), nil))
	require.Error(t, writeOptimizationJSON(outputDir, result))

	parentFile := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(parentFile, []byte("x"), 0o644))
	require.Error(t, writeOptimizationOutputs(
		context.Background(), filepath.Join(parentFile, "child"), result,
	))

	jsonFailureDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(jsonFailureDir, "optimization_result.json"), 0o755))
	require.Error(t, writeOptimizationOutputs(context.Background(), jsonFailureDir, result))

	publisherFailureDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(publisherFailureDir, "optimized_skill"), []byte("x"), 0o644,
	))
	require.ErrorContains(t,
		writeOptimizationOutputs(context.Background(), publisherFailureDir, result),
		"write optimized skill",
	)

	reportFailureDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(reportFailureDir, "OPTIMIZATION_REPORT.md"), 0o755))
	require.ErrorContains(t,
		writeOptimizationOutputs(context.Background(), reportFailureDir, result),
		"write optimization report",
	)
}

func TestOptimizationFeedbackAndTraceErrorPaths(t *testing.T) {
	feedback := optimizationFeedback(
		nil,
		&runStats{TotalTokens: 5, EventErrors: []string{"event"}},
		errors.New("agent"),
		errors.New("evaluator"),
		"artifact",
	)
	require.Contains(t, feedback, "Agent run error: agent")
	require.Contains(t, feedback, "Evaluator runtime error: evaluator")
	require.Contains(t, feedback, "artifact")
	require.Equal(t, 0.0, officialQuality(nil))
	require.Equal(t, "No evaluator result was produced.", optimizationFeedback(nil, nil, nil, nil, ""))
	trace := optimizationTrace(nil, errors.New("agent"), errors.New("evaluator"))
	require.Contains(t, trace, `"agent_error":"agent"`)
	require.Contains(t, trace, `"evaluation_error":"evaluator"`)

	signatures := make([]string, 0, 10)
	for index := 0; index < 10; index++ {
		signature := toolCallSignature("tool"+string(rune('a'+index)), []byte("not-json"))
		signatures = append(signatures, signature, signature)
	}
	require.Len(t, strings.Split(repeatedToolCallSummary(signatures), ", "), 8)
}

func TestOptimizationArtifactFeedbackNamesMissingRecipeFields(t *testing.T) {
	workspace := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, "recipe_cookbook.json"),
		[]byte(`{
			"recipes": [
				{"name":"one","category_dishes":[]},
				{"name":"two"}
			]
		}`),
		0o644,
	))
	task := &taskDefinition{
		BaseTask:  "recipe-cookbook-builder",
		TaskDoc:   "Save results to `recipe_cookbook.json`:",
		ToolsUsed: []string{"meal_by_category", "meal_by_area", "meal_by_ingredient"},
	}
	official := &officialEval{Items: []scoreItem{{
		Name: "completeness", Score: 15, MaxScore: 20, Status: "partial",
	}}}
	feedback := optimizationArtifactFeedback(task, workspace, official)
	require.Contains(t, feedback, "category_dishes is missing from 1/2 recipes")
	require.Contains(t, feedback, "cuisine_dishes is missing from 2/2 recipes")
	require.Contains(t, feedback, "ingredient_dishes is missing from 2/2 recipes")
	require.True(t, hasPartialScoreItem(official, "completeness"))
	require.False(t, hasPartialScoreItem(nil, "completeness"))
	require.Equal(t,
		[]string{"category_dishes", "cuisine_dishes", "ingredient_dishes"},
		recipeDerivedFields(task.ToolsUsed),
	)

	t.Run("ignores unsafe or unreadable artifacts", func(t *testing.T) {
		for name, prepare := range map[string]func(string){
			"symlink": func(path string) {
				target := filepath.Join(t.TempDir(), "target.json")
				require.NoError(t, os.WriteFile(target, []byte(`{"recipes":[{}]}`), 0o644))
				require.NoError(t, os.Symlink(target, path))
			},
			"oversized": func(path string) {
				require.NoError(t, os.WriteFile(path, make([]byte, (2<<20)+1), 0o644))
			},
			"invalid JSON": func(path string) {
				require.NoError(t, os.WriteFile(path, []byte(`{`), 0o644))
			},
		} {
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				prepare(filepath.Join(dir, "recipe_cookbook.json"))
				require.Empty(t, optimizationArtifactFeedback(task, dir, official))
			})
		}
	})

	official.Items[0].Score = official.Items[0].MaxScore
	require.Empty(t, optimizationArtifactFeedback(task, workspace, official))
	require.Empty(t, optimizationArtifactFeedback(nil, workspace, official))
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
