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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	skilloptimization "trpc.group/trpc-go/trpc-agent-go-benchmark/skillcraft/trpc-agent-go-impl/internal/optimization"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
	framework "trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const defaultOptimizationTokenBudget = 1_000_000

var (
	flagOptimizationSeedSpec = flag.String(
		"optimization-seed-spec",
		"",
		"JSON file containing the seed evolution.SkillSpec for optimize mode",
	)
	flagOptimizationFeedbackScales = flag.String(
		"optimization-feedback-scales",
		"e1,e2",
		"Comma-separated task scales exposed to reflection",
	)
	flagOptimizationValidationScales = flag.String(
		"optimization-validation-scales",
		"e3,m1",
		"Comma-separated task scales used for candidate selection",
	)
	flagOptimizationHoldoutScales = flag.String(
		"optimization-holdout-scales",
		"m2,h1",
		"Comma-separated task scales used only for final paired evaluation",
	)
	flagOptimizationRepeats = flag.Int(
		"optimization-repeats",
		1,
		"Independent repetitions of every task in each optimization split",
	)
	flagOptimizationMaxIterations = flag.Int(
		"optimization-max-iterations",
		4,
		"Maximum reflective mutation attempts",
	)
	flagOptimizationReflectionBatch = flag.Int(
		"optimization-reflection-batch-size",
		2,
		"Number of feedback cases included in each reflection minibatch",
	)
	flagOptimizationMaxMetricCalls = flag.Int(
		"optimization-max-metric-calls",
		200,
		"Maximum evaluated cases across search and holdout; 0 disables the cap",
	)
	flagOptimizationRandomSeed = flag.Int64(
		"optimization-random-seed",
		7,
		"Seed for deterministic case sampling and paired evaluator calls",
	)
	flagOptimizationTimeLimitSeconds = flag.Int(
		"optimization-time-limit-seconds",
		0,
		"Whole-experiment timeout; 0 relies on per-task timeouts",
	)
	flagOptimizationTokenBudget = flag.Int(
		"optimization-token-budget",
		defaultOptimizationTokenBudget,
		"Per-case agent-token ceiling used to normalize the efficiency tie-breaker",
	)
)

func buildOptimizeConfig() (*skilloptimization.Config, error) {
	seedPath := strings.TrimSpace(*flagOptimizationSeedSpec)
	if seedPath == "" {
		return nil, errors.New("optimize mode requires -optimization-seed-spec")
	}
	absSeedPath, err := filepath.Abs(seedPath)
	if err != nil {
		return nil, fmt.Errorf("resolve optimization seed spec: %w", err)
	}
	info, err := os.Stat(absSeedPath)
	if err != nil || info.IsDir() {
		return nil, fmt.Errorf("invalid optimization seed spec: %s", absSeedPath)
	}
	cfg := &skilloptimization.Config{
		SeedSpecPath:     absSeedPath,
		FeedbackScales:   parseCSV(*flagOptimizationFeedbackScales),
		ValidationScales: parseCSV(*flagOptimizationValidationScales),
		HoldoutScales:    parseCSV(*flagOptimizationHoldoutScales),
		Repeats:          *flagOptimizationRepeats,
		MaxIterations:    *flagOptimizationMaxIterations,
		ReflectionBatch:  *flagOptimizationReflectionBatch,
		MaxMetricCalls:   *flagOptimizationMaxMetricCalls,
		RandomSeed:       *flagOptimizationRandomSeed,
		TokenBudget:      *flagOptimizationTokenBudget,
	}
	if *flagOptimizationTimeLimitSeconds > 0 {
		cfg.TimeLimit = time.Duration(*flagOptimizationTimeLimitSeconds) * time.Second
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid optimization configuration: %w", err)
	}
	return cfg, nil
}

func runOptimize(
	ctx context.Context,
	cfg *benchmarkConfig,
	tasks []*taskDefinition,
) (*skilloptimization.Result, error) {
	if cfg.Optimization == nil {
		return nil, errors.New("missing optimization configuration")
	}
	seed, err := skilloptimization.LoadSeed(cfg.Optimization.SeedSpecPath)
	if err != nil {
		return nil, err
	}
	dataset, err := skilloptimization.BuildDataset(
		searchTasks(tasks),
		*cfg.Optimization,
	)
	if err != nil {
		return nil, err
	}
	runtime := newSkillCraftBatchRunner(cfg, tasks)
	evaluator := skilloptimization.NewEvaluator(
		cfg.Optimization.TokenBudget,
		runtime.Run,
	)
	reflectionBase := newOpenAIModel(cfg.ReviewerModelName, cfg.Variant)
	reflectionModel, reflectionTracker := newTrackingModel(reflectionBase)

	startedAt := time.Now().UTC()
	search, err := skilloptimization.Run(
		ctx,
		skilloptimization.Request{
			ReflectionModel: reflectionModel,
			Evaluator:       evaluator,
			Config:          *cfg.Optimization,
			Seed:            seed,
			Dataset:         dataset,
			StoreDir:        filepath.Join(cfg.OutputDir, "optimization_experiments"),
		},
	)
	if err != nil {
		return nil, err
	}
	usage := reflectionTracker.snapshot()
	return &skilloptimization.Result{
		StartedAt:        startedAt,
		FinishedAt:       time.Now().UTC(),
		SeedSpec:         filepath.Base(cfg.Optimization.SeedSpecPath),
		FeedbackScales:   append([]string(nil), cfg.Optimization.FeedbackScales...),
		ValidationScales: append([]string(nil), cfg.Optimization.ValidationScales...),
		HoldoutScales:    append([]string(nil), cfg.Optimization.HoldoutScales...),
		Repeats:          cfg.Optimization.Repeats,
		AgentTokens:      evaluator.AgentTokens(),
		ReflectionUsage: skilloptimization.Usage{
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
		},
		SelectedChanged: !reflect.DeepEqual(seed, search.Spec),
		Search:          search,
	}, nil
}

func searchTasks(tasks []*taskDefinition) []skilloptimization.Task {
	result := make([]skilloptimization.Task, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, skilloptimization.Task{
			ID:          task.ID,
			Family:      task.BaseTask,
			Scale:       task.Scale,
			Input:       task.TaskDoc,
			Expectation: taskExpectation(task),
		})
	}
	return result
}

func taskExpectation(task *taskDefinition) string {
	parts := []string{task.Description}
	if output := extractRequiredOutputFile(task.TaskDoc); output != "" {
		parts = append(parts, "produce the required output file "+output)
	}
	if containsString(task.NeededLocalTools, "claim_done") {
		parts = append(parts, "call claim_done after validating the output")
	}
	return strings.Join(parts, "; ")
}

type skillCraftBatchRunner struct {
	cfg        *benchmarkConfig
	tasks      map[string]*taskDefinition
	outputRoot string
	execute    taskExecutor
	evaluate   taskScorer

	mu      sync.Mutex
	nextRun int
}

type taskExecutor func(
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
) (*runStats, error)

type taskScorer func(
	context.Context,
	*benchmarkConfig,
	*taskDefinition,
	string,
) (*officialEval, error)

func newSkillCraftBatchRunner(
	cfg *benchmarkConfig,
	tasks []*taskDefinition,
) *skillCraftBatchRunner {
	tasksByID := make(map[string]*taskDefinition, len(tasks))
	for _, task := range tasks {
		tasksByID[task.ID] = task
	}
	return &skillCraftBatchRunner{
		cfg:        cfg,
		tasks:      tasksByID,
		outputRoot: cfg.OutputDir,
		execute:    executeTaskWithContext,
		evaluate:   evaluateTaskWithContext,
	}
}

func (r *skillCraftBatchRunner) Run(
	ctx context.Context,
	candidate *evolution.SkillSpec,
	cases []framework.Case,
	seed int64,
) ([]skilloptimization.CaseResult, error) {
	candidateID, err := candidateID(candidate)
	if err != nil {
		return nil, err
	}
	candidateRoot := filepath.Join(r.outputRoot, "optimization_candidates", candidateID)
	if err := evolution.NewFilePublisher(candidateRoot).UpsertSkill(ctx, candidate); err != nil {
		return nil, fmt.Errorf("write optimization candidate: %w", err)
	}
	repo, err := skill.NewFSRepository(candidateRoot)
	if err != nil {
		return nil, fmt.Errorf("open optimization candidate repository: %w", err)
	}

	results := make([]skilloptimization.CaseResult, 0, len(cases))
	for _, item := range cases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		task := r.tasks[item.Metadata["task_id"]]
		if task == nil {
			return nil, fmt.Errorf("unknown optimization task %q", item.Metadata["task_id"])
		}
		runID := r.allocateRunID()
		workspace := filepath.Join(
			r.outputRoot,
			"workspaces",
			"optimization",
			candidateID,
			sanitizeName(item.ID),
			fmt.Sprintf("run-%04d-seed-%d", runID, seed),
		)
		if err := prepareWorkspace(task, workspace); err != nil {
			return nil, fmt.Errorf("prepare optimization workspace: %w", err)
		}
		startedAt := time.Now()
		sessionService := sessioninmemory.NewSessionService()
		sessionID := fmt.Sprintf("optimization-%s-%d-%d", sanitizeName(item.ID), seed, runID)
		stats, runErr := r.execute(
			ctx,
			r.cfg,
			task,
			modeOptimize,
			workspace,
			repo,
			sessionService,
			"skillcraft-optimization",
			"skillcraft-optimization-user",
			sessionID,
		)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		official, evalErr := r.evaluate(ctx, r.cfg, task, workspace)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		results = append(results, skilloptimization.CaseResult{
			Agent:              agentResult(stats),
			AgentError:         runErr,
			Official:           officialResult(official),
			EvaluationError:    evalErr,
			AdditionalFeedback: artifactFeedback(task, workspace, official),
			Duration:           time.Since(startedAt),
		})
	}
	return results, nil
}

func (r *skillCraftBatchRunner) allocateRunID() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextRun++
	return r.nextRun
}

func candidateID(spec *evolution.SkillSpec) (string, error) {
	payload, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal optimization candidate: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:8]), nil
}

func agentResult(stats *runStats) *skilloptimization.AgentResult {
	if stats == nil {
		return nil
	}
	return &skilloptimization.AgentResult{
		TotalTokens:        stats.TotalTokens,
		ToolCalls:          append([]string(nil), stats.ToolCalls...),
		ToolCallSignatures: append([]string(nil), stats.ToolCallSignatures...),
		LoadedSkillNames:   append([]string(nil), stats.LoadedSkillNames...),
		ClaimDoneCalled:    stats.ClaimDoneCalled,
		SkillToolInvoked:   stats.SkillToolInvoked,
		FinalResponse:      stats.FinalResponse,
	}
}

func officialResult(result *officialEval) *skilloptimization.OfficialResult {
	if result == nil {
		return nil
	}
	return &skilloptimization.OfficialResult{
		Passed:   result.Passed,
		Status:   result.Status,
		Quality:  max(0, min(1, result.Score.Percent/100)),
		Findings: strings.TrimSpace(joinScoreNotes(result)),
	}
}

func artifactFeedback(
	task *taskDefinition,
	workspace string,
	official *officialEval,
) string {
	if task == nil || task.BaseTask != "recipe-cookbook-builder" ||
		!hasPartialScore(official, "completeness") {
		return ""
	}
	required := recipeDerivedFields(task.ToolsUsed)
	outputName := extractRequiredOutputFile(task.TaskDoc)
	if len(required) == 0 || outputName == "" {
		return ""
	}
	artifactPath := filepath.Join(workspace, filepath.Base(outputName))
	info, err := os.Lstat(artifactPath)
	const maxArtifactFeedbackBytes = 2 << 20
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxArtifactFeedbackBytes {
		return ""
	}
	payload, err := os.ReadFile(artifactPath)
	if err != nil {
		return ""
	}
	var artifact struct {
		Recipes []map[string]any `json:"recipes"`
	}
	if err := json.Unmarshal(payload, &artifact); err != nil || len(artifact.Recipes) == 0 {
		return ""
	}
	missing := make([]string, 0, len(required))
	for _, field := range required {
		count := 0
		for _, recipe := range artifact.Recipes {
			if value, ok := recipe[field]; !ok || value == nil {
				count++
			}
		}
		if count > 0 {
			missing = append(missing, fmt.Sprintf(
				"%s is missing from %d/%d recipes", field, count, len(artifact.Recipes),
			))
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "Artifact completeness: " + strings.Join(missing, "; ") +
		". Populate these recipe fields from the corresponding category or area tool results."
}

func hasPartialScore(official *officialEval, name string) bool {
	if official == nil {
		return false
	}
	for _, item := range official.Items {
		if item.Name == name && item.Score < item.MaxScore {
			return true
		}
	}
	return false
}

func recipeDerivedFields(toolsUsed []string) []string {
	var fields []string
	if containsString(toolsUsed, "meal_by_category") {
		fields = append(fields, "category_dishes")
	}
	if containsString(toolsUsed, "meal_by_area") {
		fields = append(fields, "cuisine_dishes")
	}
	if containsString(toolsUsed, "meal_by_ingredient") {
		fields = append(fields, "ingredient_dishes")
	}
	return fields
}

func toolCallSignature(name string, arguments []byte) string {
	payload := arguments
	var compact bytes.Buffer
	if err := json.Compact(&compact, arguments); err == nil {
		payload = compact.Bytes()
	}
	digest := sha256.Sum256(payload)
	return normalizeToolCallName(name) + "#" + hex.EncodeToString(digest[:8])
}
