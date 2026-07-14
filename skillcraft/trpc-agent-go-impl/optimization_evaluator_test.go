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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

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
	}
	evaluator := newSkillCraftOptimizationEvaluator(cfg, []*taskDefinition{task}, 1000)
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
		OutputDir: outputFile,
	}, []*taskDefinition{task}, 1000)
	_, err := evaluator.Evaluate(context.Background(), candidate, caseInput, 7)
	require.ErrorContains(t, err, "write optimization candidate")

	task.InitialWorkspace = filepath.Join(t.TempDir(), "missing")
	evaluator = newSkillCraftOptimizationEvaluator(&benchmarkConfig{
		OutputDir: t.TempDir(),
	}, []*taskDefinition{task}, 1000)
	_, err = evaluator.Evaluate(context.Background(), candidate, caseInput, 7)
	require.ErrorContains(t, err, "prepare optimization workspace")

	task.InitialWorkspace = ""
	evaluator = newSkillCraftOptimizationEvaluator(&benchmarkConfig{
		OutputDir: t.TempDir(),
	}, []*taskDefinition{task}, 1000)
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

func TestOptimizationConclusion(t *testing.T) {
	run := &optimizationBenchmarkResult{
		SelectedChanged: true,
		Result: &optimization.Result{
			PromotionReason: "holdout score regressed",
		},
	}
	require.Contains(t, optimizationConclusion(run), "not eligible for promotion")
	require.Contains(t, optimizationConclusion(run), "holdout score regressed")

	run.SelectedChanged = false
	run.Result.PromotionReason = ""
	require.Contains(t, optimizationConclusion(run), "retained the seed skill")
	require.Contains(t, optimizationConclusion(run), "holdout gate did not approve")
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
