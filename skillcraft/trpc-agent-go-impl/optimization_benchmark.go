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
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	objectiveQuality      = "official_quality"
	objectivePassed       = "passed"
	objectiveAgentTokens  = "agent_tokens"
	objectiveToolCalls    = "tool_calls"
	objectiveDuration     = "duration_seconds"
	objectiveSkillLoaded  = "skill_loaded"
	defaultTokenBudget    = 1_000_000
	optimizationDatasetID = "skillcraft-reflective-optimization"
)

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
		defaultTokenBudget,
		"Per-case agent-token ceiling used to normalize the efficiency tie-breaker",
	)
)

type optimizationBenchmarkConfig struct {
	SeedSpecPath     string
	FeedbackScales   []string
	ValidationScales []string
	HoldoutScales    []string
	Repeats          int
	MaxIterations    int
	ReflectionBatch  int
	MaxMetricCalls   int
	RandomSeed       int64
	TimeLimit        time.Duration
	TokenBudget      int
}

type optimizationBenchmarkResult struct {
	StartedAt        time.Time            `json:"startedAt"`
	FinishedAt       time.Time            `json:"finishedAt"`
	SeedSpecPath     string               `json:"seedSpecPath"`
	FeedbackScales   []string             `json:"feedbackScales"`
	ValidationScales []string             `json:"validationScales"`
	HoldoutScales    []string             `json:"holdoutScales"`
	Repeats          int                  `json:"repeats"`
	AgentTokens      int                  `json:"agentTokens"`
	ReflectionUsage  trackedUsage         `json:"reflectionUsage"`
	SelectedChanged  bool                 `json:"selectedChanged"`
	Result           *optimization.Result `json:"result"`
}

type optimizationRunner interface {
	Optimize(context.Context, optimization.Request) (*optimization.Result, error)
}

type optimizationRunnerFactory func(
	model.Model,
	optimization.Evaluator,
	...optimization.Option,
) (optimizationRunner, error)

func buildOptimizationBenchmarkConfig() (optimizationBenchmarkConfig, error) {
	seedPath := strings.TrimSpace(*flagOptimizationSeedSpec)
	if seedPath == "" {
		return optimizationBenchmarkConfig{}, errors.New("optimize mode requires -optimization-seed-spec")
	}
	absSeedPath, err := filepath.Abs(seedPath)
	if err != nil {
		return optimizationBenchmarkConfig{}, fmt.Errorf("resolve optimization seed spec: %w", err)
	}
	info, err := os.Stat(absSeedPath)
	if err != nil || info.IsDir() {
		return optimizationBenchmarkConfig{}, fmt.Errorf("invalid optimization seed spec: %s", absSeedPath)
	}
	cfg := optimizationBenchmarkConfig{
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
	if cfg.Repeats <= 0 {
		return optimizationBenchmarkConfig{}, errors.New("optimization repeats must be positive")
	}
	if cfg.MaxIterations < 0 {
		return optimizationBenchmarkConfig{}, errors.New("optimization max iterations must not be negative")
	}
	if cfg.ReflectionBatch <= 0 {
		return optimizationBenchmarkConfig{}, errors.New("optimization reflection batch size must be positive")
	}
	if cfg.MaxMetricCalls < 0 {
		return optimizationBenchmarkConfig{}, errors.New("optimization max metric calls must not be negative")
	}
	if cfg.TokenBudget <= 0 {
		return optimizationBenchmarkConfig{}, errors.New("optimization token budget must be positive")
	}
	if len(cfg.FeedbackScales) == 0 || len(cfg.ValidationScales) == 0 || len(cfg.HoldoutScales) == 0 {
		return optimizationBenchmarkConfig{}, errors.New("all optimization scale splits must be non-empty")
	}
	if err := validateDisjointScaleSplits(cfg); err != nil {
		return optimizationBenchmarkConfig{}, err
	}
	return cfg, nil
}

func validateDisjointScaleSplits(cfg optimizationBenchmarkConfig) error {
	seen := make(map[string]string)
	for split, scales := range map[string][]string{
		"feedback":   cfg.FeedbackScales,
		"validation": cfg.ValidationScales,
		"holdout":    cfg.HoldoutScales,
	} {
		for _, scale := range scales {
			if previous, ok := seen[scale]; ok {
				return fmt.Errorf("optimization scale %q appears in both %s and %s", scale, previous, split)
			}
			seen[scale] = split
		}
	}
	return nil
}

func runOptimizationBenchmark(
	ctx context.Context,
	cfg *benchmarkConfig,
	tasks []*taskDefinition,
) (*optimizationBenchmarkResult, error) {
	return runOptimizationBenchmarkWithFactory(ctx, cfg, tasks, newOptimizationRunner)
}

func newOptimizationRunner(
	reflectionModel model.Model,
	evaluator optimization.Evaluator,
	opts ...optimization.Option,
) (optimizationRunner, error) {
	return optimization.New(reflectionModel, evaluator, opts...)
}

func runOptimizationBenchmarkWithFactory(
	ctx context.Context,
	cfg *benchmarkConfig,
	tasks []*taskDefinition,
	newRunner optimizationRunnerFactory,
) (*optimizationBenchmarkResult, error) {
	if cfg.Optimization == nil {
		return nil, errors.New("missing optimization configuration")
	}
	optimizationCfg := cfg.Optimization
	seed, err := loadOptimizationSeedSpec(optimizationCfg.SeedSpecPath)
	if err != nil {
		return nil, err
	}
	dataset, err := buildOptimizationDataset(tasks, *optimizationCfg)
	if err != nil {
		return nil, err
	}
	evaluator := newSkillCraftOptimizationEvaluator(
		cfg,
		tasks,
		optimizationCfg.TokenBudget,
	)
	reflectionBase := newOpenAIModel(cfg.ReviewerModelName, cfg.Variant)
	reflectionModel, reflectionTracker := newTrackingModel(reflectionBase)
	opts := []optimization.Option{
		optimization.WithMaxIterations(optimizationCfg.MaxIterations),
		optimization.WithReflectionBatchSize(optimizationCfg.ReflectionBatch),
		optimization.WithMaxMetricCalls(optimizationCfg.MaxMetricCalls),
		optimization.WithRandomSeed(optimizationCfg.RandomSeed),
		optimization.WithStoreDir(filepath.Join(cfg.OutputDir, "optimization_experiments")),
	}
	if optimizationCfg.TimeLimit > 0 {
		opts = append(opts, optimization.WithTimeLimit(optimizationCfg.TimeLimit))
	}
	optimizer, err := newRunner(reflectionModel, evaluator, opts...)
	if err != nil {
		return nil, fmt.Errorf("create optimizer: %w", err)
	}

	startedAt := time.Now().UTC()
	result, err := optimizer.Optimize(ctx, optimization.Request{
		Seed:    seed,
		Dataset: dataset,
	})
	if err != nil {
		return nil, err
	}
	return &optimizationBenchmarkResult{
		StartedAt:        startedAt,
		FinishedAt:       time.Now().UTC(),
		SeedSpecPath:     filepath.Base(optimizationCfg.SeedSpecPath),
		FeedbackScales:   append([]string(nil), optimizationCfg.FeedbackScales...),
		ValidationScales: append([]string(nil), optimizationCfg.ValidationScales...),
		HoldoutScales:    append([]string(nil), optimizationCfg.HoldoutScales...),
		Repeats:          optimizationCfg.Repeats,
		AgentTokens:      evaluator.snapshotAgentTokens(),
		ReflectionUsage:  reflectionTracker.snapshot(),
		SelectedChanged:  !reflect.DeepEqual(seed, result.Spec),
		Result:           result,
	}, nil
}

func loadOptimizationSeedSpec(path string) (*evolution.SkillSpec, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open optimization seed spec: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var spec evolution.SkillSpec
	if err := decoder.Decode(&spec); err != nil {
		return nil, fmt.Errorf("decode optimization seed spec: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("optimization seed spec contains trailing JSON values")
		}
		return nil, fmt.Errorf("decode optimization seed spec trailer: %w", err)
	}
	return &spec, nil
}

func buildOptimizationDataset(
	tasks []*taskDefinition,
	cfg optimizationBenchmarkConfig,
) (optimization.Dataset, error) {
	if len(tasks) == 0 {
		return optimization.Dataset{}, errors.New("optimization requires at least one task")
	}
	family := tasks[0].BaseTask
	tasksByScale := make(map[string]*taskDefinition, len(tasks))
	for _, task := range tasks {
		if task.BaseTask != family {
			return optimization.Dataset{}, fmt.Errorf(
				"optimization task %q belongs to %q, expected one family %q",
				task.ID, task.BaseTask, family,
			)
		}
		if _, duplicate := tasksByScale[task.Scale]; duplicate {
			return optimization.Dataset{}, fmt.Errorf("duplicate optimization scale %q", task.Scale)
		}
		tasksByScale[task.Scale] = task
	}
	makeCases := func(split string, scales []string, critical bool) ([]optimization.Case, error) {
		cases := make([]optimization.Case, 0, len(scales)*cfg.Repeats)
		for _, scale := range scales {
			task := tasksByScale[scale]
			if task == nil {
				return nil, fmt.Errorf("optimization %s scale %q was not selected", split, scale)
			}
			for repeat := 1; repeat <= cfg.Repeats; repeat++ {
				cases = append(cases, optimization.Case{
					ID:       fmt.Sprintf("%s/%s/r%d", split, task.ID, repeat),
					Input:    task.TaskDoc,
					Expected: publicTaskExpectation(task),
					Critical: critical,
					Metadata: map[string]string{
						"task_id": task.ID,
						"repeat":  strconv.Itoa(repeat),
						"split":   split,
					},
				})
			}
		}
		return cases, nil
	}
	feedback, err := makeCases("feedback", cfg.FeedbackScales, false)
	if err != nil {
		return optimization.Dataset{}, err
	}
	validation, err := makeCases("validation", cfg.ValidationScales, false)
	if err != nil {
		return optimization.Dataset{}, err
	}
	holdout, err := makeCases("holdout", cfg.HoldoutScales, true)
	if err != nil {
		return optimization.Dataset{}, err
	}
	return optimization.Dataset{
		ID:         optimizationDatasetID + "/" + family,
		Version:    "skillcraft-scaled-v1",
		Feedback:   feedback,
		Validation: validation,
		Holdout:    holdout,
	}, nil
}

func publicTaskExpectation(task *taskDefinition) string {
	parts := []string{task.Description}
	if output := extractRequiredOutputFile(task.TaskDoc); output != "" {
		parts = append(parts, "produce the required output file "+output)
	}
	if containsString(task.NeededLocalTools, "claim_done") {
		parts = append(parts, "call claim_done after validating the output")
	}
	return strings.Join(parts, "; ")
}
