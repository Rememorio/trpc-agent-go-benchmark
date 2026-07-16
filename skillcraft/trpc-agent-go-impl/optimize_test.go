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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	skilloptimization "trpc.group/trpc-go/trpc-agent-go-benchmark/skillcraft/trpc-agent-go-impl/internal/optimization"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
	framework "trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestBuildOptimizeConfig(t *testing.T) {
	seedPath := filepath.Join(t.TempDir(), "seed.json")
	require.NoError(t, os.WriteFile(seedPath, []byte(`{}`), 0o644))
	setOptimizationFlagsForTest(t)
	*flagOptimizationSeedSpec = seedPath
	candidatePath := filepath.Join(t.TempDir(), "candidate.json")
	require.NoError(t, os.WriteFile(candidatePath, []byte(`{}`), 0o644))
	*flagOptimizationCandidateSpec = candidatePath
	*flagOptimizationFeedbackScales = " e1, e2 "
	*flagOptimizationValidationScales = "e3,m1"
	*flagOptimizationHoldoutScales = "m2,h1"
	*flagOptimizationCriticalScales = "h1"
	*flagOptimizationRepeats = 2
	*flagOptimizationMaxIterations = 3
	*flagOptimizationReflectionBatch = 2
	*flagOptimizationMaxMetricCalls = 40
	*flagOptimizationRandomSeed = 17
	*flagOptimizationEvaluationTemperature = 0.25
	*flagOptimizationTimeLimitSeconds = 9
	*flagOptimizationTokenBudget = 2000

	cfg, err := buildOptimizeConfig()
	require.NoError(t, err)
	require.Equal(t, []string{"e1", "e2"}, cfg.FeedbackScales)
	require.Equal(t, []string{"h1"}, cfg.CriticalScales)
	require.Equal(t, 2, cfg.Repeats)
	require.Equal(t, int64(17), cfg.RandomSeed)
	require.Equal(t, 0.25, cfg.EvaluationTemperature)
	require.Equal(t, 9*time.Second, cfg.TimeLimit)
	require.Equal(t, 2000, cfg.TokenBudget)
	require.Equal(t, candidatePath, cfg.CandidateSpecPath)

	*flagOptimizationHoldoutScales = "e2"
	_, err = buildOptimizeConfig()
	require.ErrorContains(t, err, "appears in both")

	*flagOptimizationHoldoutScales = "m2,h1"
	*flagOptimizationCriticalScales = "e3"
	_, err = buildOptimizeConfig()
	require.ErrorContains(t, err, "not in the holdout")
	*flagOptimizationCriticalScales = ""

	*flagOptimizationHoldoutScales = ""
	_, err = buildOptimizeConfig()
	require.ErrorContains(t, err, "requires holdout scales")
	*flagOptimizationCandidateSpec = ""
	cfg, err = buildOptimizeConfig()
	require.NoError(t, err)
	require.Empty(t, cfg.HoldoutScales)
}

func TestBuildOptimizeConfigRejectsInvalidSeed(t *testing.T) {
	setOptimizationFlagsForTest(t)
	*flagOptimizationSeedSpec = ""
	_, err := buildOptimizeConfig()
	require.ErrorContains(t, err, "requires -optimization-seed-spec")

	*flagOptimizationSeedSpec = t.TempDir()
	_, err = buildOptimizeConfig()
	require.ErrorContains(t, err, "invalid optimization seed spec")

	seedPath := filepath.Join(t.TempDir(), "seed.json")
	require.NoError(t, os.WriteFile(seedPath, []byte(`{}`), 0o644))
	*flagOptimizationSeedSpec = seedPath
	*flagOptimizationCandidateSpec = t.TempDir()
	_, err = buildOptimizeConfig()
	require.ErrorContains(t, err, "invalid optimization candidate spec")
}

func setOptimizationFlagsForTest(t *testing.T) {
	t.Helper()
	seedSpec := *flagOptimizationSeedSpec
	candidateSpec := *flagOptimizationCandidateSpec
	feedback := *flagOptimizationFeedbackScales
	validation := *flagOptimizationValidationScales
	holdout := *flagOptimizationHoldoutScales
	critical := *flagOptimizationCriticalScales
	repeats := *flagOptimizationRepeats
	iterations := *flagOptimizationMaxIterations
	batch := *flagOptimizationReflectionBatch
	metricCalls := *flagOptimizationMaxMetricCalls
	randomSeed := *flagOptimizationRandomSeed
	evaluationTemperature := *flagOptimizationEvaluationTemperature
	timeLimit := *flagOptimizationTimeLimitSeconds
	tokenBudget := *flagOptimizationTokenBudget
	t.Cleanup(func() {
		*flagOptimizationSeedSpec = seedSpec
		*flagOptimizationCandidateSpec = candidateSpec
		*flagOptimizationFeedbackScales = feedback
		*flagOptimizationValidationScales = validation
		*flagOptimizationHoldoutScales = holdout
		*flagOptimizationCriticalScales = critical
		*flagOptimizationRepeats = repeats
		*flagOptimizationMaxIterations = iterations
		*flagOptimizationReflectionBatch = batch
		*flagOptimizationMaxMetricCalls = metricCalls
		*flagOptimizationRandomSeed = randomSeed
		*flagOptimizationEvaluationTemperature = evaluationTemperature
		*flagOptimizationTimeLimitSeconds = timeLimit
		*flagOptimizationTokenBudget = tokenBudget
	})
}

func TestSkillCraftBatchRunner(t *testing.T) {
	task := optimizationTask("weather/e1", "weather", "e1")
	runner := newSkillCraftBatchRunner(&benchmarkConfig{OutputDir: t.TempDir()}, []*taskDefinition{task})
	runner.execute = func(
		ctx context.Context,
		cfg *benchmarkConfig,
		gotTask *taskDefinition,
		mode runMode,
		_ string,
		_ *skill.FSRepository,
		_ *sessioninmemory.SessionService,
		_, _, _ string,
	) (*runStats, error) {
		require.NoError(t, ctx.Err())
		require.NotNil(t, cfg.EvaluationSeed)
		require.Equal(
			t,
			deriveEvaluationSeed(7, "feedback/weather/e1/r1"),
			*cfg.EvaluationSeed,
		)
		require.NotNil(t, cfg.EvaluationTemperature)
		require.Zero(t, *cfg.EvaluationTemperature)
		require.Same(t, task, gotTask)
		require.Equal(t, modeOptimize, mode)
		return &runStats{
			TotalTokens:      100,
			ToolCalls:        []string{"weather_get"},
			LoadedSkillNames: []string{"Weather"},
			FinalResponse:    "done",
		}, nil
	}
	runner.evaluate = func(
		context.Context,
		*benchmarkConfig,
		*taskDefinition,
		string,
	) (*officialEval, error) {
		return &officialEval{Passed: true, Status: "pass", Score: scorePayload{Percent: 100}}, nil
	}
	results, err := runner.Run(context.Background(), validOptimizationSpec(), []framework.Case{{
		ID:       "feedback/weather/e1/r1",
		Metadata: map[string]string{"task_id": task.ID},
	}}, 7)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "done", results[0].Agent.FinalResponse)
	require.Equal(t, 1.0, results[0].Official.Quality)
	require.Equal(t, 2, runner.allocateRunID())

	_, err = runner.Run(context.Background(), validOptimizationSpec(), []framework.Case{{
		ID:       "missing",
		Metadata: map[string]string{"task_id": "missing"},
	}}, 7)
	require.ErrorContains(t, err, "unknown optimization task")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runner.Run(cancelled, validOptimizationSpec(), []framework.Case{{
		ID:       "cancelled",
		Metadata: map[string]string{"task_id": task.ID},
	}}, 7)
	require.ErrorIs(t, err, context.Canceled)
}

func TestDeriveEvaluationSeedIsStableAndCaseSpecific(t *testing.T) {
	first := deriveEvaluationSeed(7, "feedback/weather/e1/r1")
	require.Equal(t, first, deriveEvaluationSeed(7, "feedback/weather/e1/r1"))
	require.NotEqual(t, first, deriveEvaluationSeed(7, "feedback/weather/e1/r2"))
	require.NotEqual(t, first, deriveEvaluationSeed(8, "feedback/weather/e1/r1"))
}

func TestSkillCraftBatchRunnerFailurePaths(t *testing.T) {
	task := optimizationTask("weather/e1", "weather", "e1")
	cases := []framework.Case{{
		ID:       "feedback/weather/e1/r1",
		Metadata: map[string]string{"task_id": task.ID},
	}}

	outputFile := filepath.Join(t.TempDir(), "output-file")
	require.NoError(t, os.WriteFile(outputFile, []byte("x"), 0o644))
	runner := newSkillCraftBatchRunner(
		&benchmarkConfig{OutputDir: outputFile}, []*taskDefinition{task},
	)
	_, err := runner.Run(context.Background(), validOptimizationSpec(), cases, 7)
	require.ErrorContains(t, err, "write optimization candidate")

	task.InitialWorkspace = filepath.Join(t.TempDir(), "missing")
	runner = newSkillCraftBatchRunner(
		&benchmarkConfig{OutputDir: t.TempDir()}, []*taskDefinition{task},
	)
	_, err = runner.Run(context.Background(), validOptimizationSpec(), cases, 7)
	require.ErrorContains(t, err, "prepare optimization workspace")

	task.InitialWorkspace = ""
	runner = newSkillCraftBatchRunner(
		&benchmarkConfig{OutputDir: t.TempDir()}, []*taskDefinition{task},
	)
	runner.execute = func(
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
	runner.evaluate = func(
		context.Context,
		*benchmarkConfig,
		*taskDefinition,
		string,
	) (*officialEval, error) {
		return nil, errors.New("evaluator failed")
	}
	results, err := runner.Run(context.Background(), validOptimizationSpec(), cases, 7)
	require.NoError(t, err)
	require.ErrorContains(t, results[0].AgentError, "agent failed")
	require.ErrorContains(t, results[0].EvaluationError, "evaluator failed")
}

func TestOptimizationAdapterHelpers(t *testing.T) {
	spec := validOptimizationSpec()
	firstID, err := candidateID(spec)
	require.NoError(t, err)
	secondID, err := candidateID(spec)
	require.NoError(t, err)
	require.Equal(t, firstID, secondID)

	tasks := searchTasks([]*taskDefinition{{
		ID:               "weather/e1",
		BaseTask:         "weather",
		Scale:            "e1",
		Description:      "weather task",
		TaskDoc:          "Save results to `result.json`:",
		NeededLocalTools: []string{"claim_done"},
	}})
	require.Equal(t, "weather", tasks[0].Family)
	require.Contains(t, tasks[0].Expectation, "result.json")
	require.Contains(t, tasks[0].Expectation, "claim_done")

	stats := &runStats{TotalTokens: 10, ToolCalls: []string{"tool"}}
	require.Equal(t, 10, agentResult(stats).TotalTokens)
	require.Nil(t, agentResult(nil))
	require.Equal(t, 0.95, officialResult(&officialEval{
		Score: scorePayload{Percent: 95},
	}).Quality)
	require.Nil(t, officialResult(nil))

	first := toolCallSignature("weather_get", []byte(`{"city":"Tokyo"}`))
	second := toolCallSignature("weather_get", []byte(`{"city":"London"}`))
	require.NotEqual(t, first, second)
}

func TestArtifactFeedbackNamesMissingRecipeFields(t *testing.T) {
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
	feedback := artifactFeedback(task, workspace, official)
	require.Contains(t, feedback, "category_dishes is missing from 1/2 recipes")
	require.Contains(t, feedback, "cuisine_dishes is missing from 2/2 recipes")
	require.Contains(t, feedback, "ingredient_dishes is missing from 2/2 recipes")
	require.Contains(t, feedback, "exact keys on each recipe object")
	require.Contains(t, feedback, "nested or renamed equivalents")
	require.True(t, hasPartialScore(official, "completeness"))
	require.False(t, hasPartialScore(nil, "completeness"))
	require.Equal(t,
		[]string{"category_dishes", "cuisine_dishes", "ingredient_dishes"},
		recipeDerivedFields(task.ToolsUsed),
	)

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
			require.Empty(t, artifactFeedback(task, dir, official))
		})
	}

	official.Items[0].Score = official.Items[0].MaxScore
	require.Empty(t, artifactFeedback(task, workspace, official))
	require.Empty(t, artifactFeedback(nil, workspace, official))
}

func TestToolContractFeedback(t *testing.T) {
	task := &taskDefinition{ToolsUsed: []string{
		"meal_details", "meal_by_area", "meal_by_ingredient", "claim_done",
	}}
	stats := &runStats{ToolCalls: []string{
		"skill_load",
		"mcp_local-meal_search",
		"mcp_local-meal_search",
		"mcp_local-meal_details",
		"mcp_local-meal_by_ingredient",
		"mcp_local-write_final_json",
		"mcp_local-claim_done",
	}}
	feedback := toolContractFeedback(task, stats)
	require.Contains(t, feedback, "missing required domain tools: meal_by_area")
	require.Contains(t, feedback, "task did not require: meal_search (2 calls)")
	require.Contains(t, feedback, "specific to the current case")
	require.Contains(t, feedback, "instead of banning endpoint names")

	stats.ToolCalls = []string{
		"mcp_local-meal_details",
		"mcp_local-meal_by_area",
		"mcp_local-meal_by_ingredient",
		"mcp_local-write_final_json",
	}
	require.Empty(t, toolContractFeedback(task, stats))
	require.Empty(t, toolContractFeedback(nil, stats))
	require.Empty(t, toolContractFeedback(task, nil))
}

func TestAdditionalFeedbackCombinesPublicSignals(t *testing.T) {
	workspace := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, "recipe_cookbook.json"),
		[]byte(`{"recipes":[{"name":"one"}]}`),
		0o644,
	))
	task := &taskDefinition{
		BaseTask:  "recipe-cookbook-builder",
		TaskDoc:   "Save results to `recipe_cookbook.json`:",
		ToolsUsed: []string{"meal_details", "meal_by_area"},
	}
	official := &officialEval{Items: []scoreItem{{
		Name: "completeness", Score: 15, MaxScore: 20, Status: "partial",
	}}}
	feedback := additionalFeedback(
		task,
		&runStats{ToolCalls: []string{"mcp_local-meal_search"}},
		workspace,
		official,
	)
	require.Contains(t, feedback, "Task tool contract")
	require.Contains(t, feedback, "Artifact completeness")
}

func TestOptimizationOutputs(t *testing.T) {
	spec := validOptimizationSpec()
	result := &skilloptimization.Result{
		SeedSpec:         "seed.json",
		FeedbackScales:   []string{"e1"},
		ValidationScales: []string{"e2"},
		HoldoutScales:    []string{"e3"},
		Repeats:          1,
		RandomSeed:       17,
		Search: &framework.Result{
			Spec:                spec,
			CandidateCount:      2,
			MetricCalls:         8,
			StopReason:          "max_iterations",
			PromotionEligible:   true,
			PromotionReason:     "holdout requirements satisfied",
			BaselineValidation:  framework.Summary{Score: 0.8, Cases: 1},
			CandidateValidation: framework.Summary{Score: 0.9, Cases: 1},
			BaselineHoldout: framework.Summary{
				Score:      0.7,
				Cases:      1,
				Objectives: map[string]float64{"official_quality": 0.8},
			},
			CandidateHoldout: framework.Summary{
				Score:      0.9,
				Cases:      1,
				Objectives: map[string]float64{"official_quality": 1},
			},
		},
	}
	benchmark := &benchmarkResult{
		Timestamp:     time.Now().Format(time.RFC3339),
		RequestedMode: modeOptimize,
		Model:         "model",
		ReviewerModel: "reviewer",
		MaxTokens:     8192,
		Optimization:  result,
	}
	outputDir := filepath.Join(t.TempDir(), "nested", "output")
	require.NoError(t, writeBenchmarkOutputs(context.Background(), outputDir, benchmark))
	require.FileExists(t, filepath.Join(outputDir, "results.json"))
	require.FileExists(t, filepath.Join(outputDir, "REPORT.md"))

	payload, err := os.ReadFile(filepath.Join(outputDir, "results.json"))
	require.NoError(t, err)
	var saved benchmarkResult
	require.NoError(t, json.Unmarshal(payload, &saved))
	require.Equal(t, "seed.json", saved.Optimization.SeedSpec)
	require.Equal(t, 8192, saved.MaxTokens)
	report, err := os.ReadFile(filepath.Join(outputDir, "REPORT.md"))
	require.NoError(t, err)
	require.Contains(t, string(report), "Promotion eligible: `true`")
	require.Contains(t, string(report), "| Holdout | 0.7000 | 0.9000 | +0.2000 |")
	require.DirExists(t, filepath.Join(outputDir, "selected_skill"))

	require.Error(t, writeBenchmarkOutputs(context.Background(), outputDir, nil))
	require.Error(t, writeBenchmarkOutputs(context.Background(), outputDir, &benchmarkResult{
		Optimization: &skilloptimization.Result{},
	}))

	publisherFailureDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(publisherFailureDir, "selected_skill"), []byte("x"), 0o644,
	))
	require.ErrorContains(t,
		writeBenchmarkOutputs(context.Background(), publisherFailureDir, benchmark),
		"write selected skill",
	)
}

func TestOptimizationRuntimeBehavior(t *testing.T) {
	instruction := buildInstructionForMode(
		&taskDefinition{}, "workspace", []string{"Recipe Cookbook"}, modeOptimize,
	)
	require.Contains(t, instruction, "evaluates the loaded managed skill")
	require.NotContains(t,
		buildInstruction(&taskDefinition{}, "workspace", []string{"Recipe Cookbook"}),
		"evaluates the loaded managed skill",
	)

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
