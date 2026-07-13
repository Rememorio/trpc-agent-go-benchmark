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
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
	"trpc.group/trpc-go/trpc-agent-go/model"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
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
	StartedAt         time.Time            `json:"startedAt"`
	FinishedAt        time.Time            `json:"finishedAt"`
	Model             string               `json:"model"`
	ReflectionModel   string               `json:"reflectionModel"`
	SeedSpecPath      string               `json:"seedSpecPath"`
	FeedbackScales    []string             `json:"feedbackScales"`
	ValidationScales  []string             `json:"validationScales"`
	HoldoutScales     []string             `json:"holdoutScales"`
	Repeats           int                  `json:"repeats"`
	SearchAgentTokens int                  `json:"searchAgentTokens"`
	ReflectionUsage   trackedUsage         `json:"reflectionUsage"`
	SelectedChanged   bool                 `json:"selectedChanged"`
	Result            *optimization.Result `json:"result"`
}

type skillCraftOptimizationEvaluator struct {
	cfg         *benchmarkConfig
	tasks       map[string]*taskDefinition
	outputRoot  string
	tokenBudget int
	execute     optimizationTaskExecutor
	evaluate    optimizationTaskScorer

	mu                sync.Mutex
	nextRun           int
	searchAgentTokens int
}

type optimizationTaskExecutor func(
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

type optimizationTaskScorer func(
	context.Context,
	*benchmarkConfig,
	*taskDefinition,
	string,
) (*officialEval, error)

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
) error {
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
) error {
	seed, err := loadOptimizationSeedSpec(cfg.Optimization.SeedSpecPath)
	if err != nil {
		return err
	}
	dataset, err := buildOptimizationDataset(tasks, cfg.Optimization)
	if err != nil {
		return err
	}
	evaluator := newSkillCraftOptimizationEvaluator(cfg, tasks)
	reflectionBase := newOpenAIModel(cfg.ReviewerModelName, cfg.Variant)
	reflectionModel, reflectionTracker := newTrackingModel(reflectionBase)
	opts := []optimization.Option{
		optimization.WithMaxIterations(cfg.Optimization.MaxIterations),
		optimization.WithReflectionBatchSize(cfg.Optimization.ReflectionBatch),
		optimization.WithMaxMetricCalls(cfg.Optimization.MaxMetricCalls),
		optimization.WithRandomSeed(cfg.Optimization.RandomSeed),
		optimization.WithStoreDir(filepath.Join(cfg.OutputDir, "optimization_experiments")),
	}
	if cfg.Optimization.TimeLimit > 0 {
		opts = append(opts, optimization.WithTimeLimit(cfg.Optimization.TimeLimit))
	}
	optimizer, err := newRunner(reflectionModel, evaluator, opts...)
	if err != nil {
		return fmt.Errorf("create optimizer: %w", err)
	}

	startedAt := time.Now().UTC()
	result, err := optimizer.Optimize(ctx, optimization.Request{
		Seed:    seed,
		Dataset: dataset,
	})
	if err != nil {
		return err
	}
	envelope := &optimizationBenchmarkResult{
		StartedAt:         startedAt,
		FinishedAt:        time.Now().UTC(),
		Model:             cfg.ModelName,
		ReflectionModel:   cfg.ReviewerModelName,
		SeedSpecPath:      filepath.Base(cfg.Optimization.SeedSpecPath),
		FeedbackScales:    append([]string(nil), cfg.Optimization.FeedbackScales...),
		ValidationScales:  append([]string(nil), cfg.Optimization.ValidationScales...),
		HoldoutScales:     append([]string(nil), cfg.Optimization.HoldoutScales...),
		Repeats:           cfg.Optimization.Repeats,
		SearchAgentTokens: evaluator.snapshotAgentTokens(),
		ReflectionUsage:   reflectionTracker.snapshot(),
		SelectedChanged:   !reflect.DeepEqual(seed, result.Spec),
		Result:            result,
	}
	return writeOptimizationOutputs(ctx, cfg.OutputDir, envelope)
}

func writeOptimizationOutputs(
	ctx context.Context,
	outputDir string,
	envelope *optimizationBenchmarkResult,
) error {
	if envelope == nil || envelope.Result == nil || envelope.Result.Spec == nil {
		return errors.New("nil optimization result")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create optimization output directory: %w", err)
	}
	if err := writeOptimizationJSON(filepath.Join(outputDir, "optimization_result.json"), envelope); err != nil {
		return err
	}
	optimizedDir := filepath.Join(outputDir, "optimized_skill")
	if err := evolution.NewFilePublisher(optimizedDir).UpsertSkill(ctx, envelope.Result.Spec); err != nil {
		return fmt.Errorf("write optimized skill: %w", err)
	}
	if err := writeOptimizationReport(filepath.Join(outputDir, "OPTIMIZATION_REPORT.md"), envelope); err != nil {
		return err
	}
	return nil
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

func newSkillCraftOptimizationEvaluator(
	cfg *benchmarkConfig,
	tasks []*taskDefinition,
) *skillCraftOptimizationEvaluator {
	tasksByID := make(map[string]*taskDefinition, len(tasks))
	for _, task := range tasks {
		tasksByID[task.ID] = task
	}
	return &skillCraftOptimizationEvaluator{
		cfg:         cfg,
		tasks:       tasksByID,
		outputRoot:  cfg.OutputDir,
		tokenBudget: cfg.Optimization.TokenBudget,
		execute:     executeTaskWithContext,
		evaluate:    evaluateTaskWithContext,
	}
}

func (e *skillCraftOptimizationEvaluator) Evaluate(
	ctx context.Context,
	candidate *evolution.SkillSpec,
	cases []optimization.Case,
	seed int64,
) ([]optimization.Evaluation, error) {
	if candidate == nil {
		return nil, errors.New("nil optimization candidate")
	}
	candidateID, err := optimizationCandidateID(candidate)
	if err != nil {
		return nil, err
	}
	candidateRoot := filepath.Join(e.outputRoot, "optimization_candidates", candidateID)
	if err := evolution.NewFilePublisher(candidateRoot).UpsertSkill(ctx, candidate); err != nil {
		return nil, fmt.Errorf("write optimization candidate: %w", err)
	}
	repo, err := skill.NewFSRepository(candidateRoot)
	if err != nil {
		return nil, fmt.Errorf("open optimization candidate repository: %w", err)
	}

	results := make([]optimization.Evaluation, 0, len(cases))
	for _, item := range cases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		taskID := item.Metadata["task_id"]
		task := e.tasks[taskID]
		if task == nil {
			return nil, fmt.Errorf("unknown optimization task %q", taskID)
		}
		runID := e.allocateRunID()
		workspace := filepath.Join(
			e.outputRoot,
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
		stats, runErr := e.execute(
			ctx,
			e.cfg,
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
		official, evalErr := e.evaluate(ctx, e.cfg, task, workspace)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		e.addAgentTokens(stats)
		artifactFeedback := optimizationArtifactFeedback(task, workspace, official)
		results = append(results, buildOptimizationEvaluation(
			item,
			candidate,
			stats,
			runErr,
			official,
			evalErr,
			artifactFeedback,
			time.Since(startedAt),
			e.tokenBudget,
		))
	}
	return results, nil
}

func (e *skillCraftOptimizationEvaluator) allocateRunID() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextRun++
	return e.nextRun
}

func (e *skillCraftOptimizationEvaluator) addAgentTokens(stats *runStats) {
	if stats == nil {
		return
	}
	e.mu.Lock()
	e.searchAgentTokens += stats.TotalTokens
	e.mu.Unlock()
}

func (e *skillCraftOptimizationEvaluator) snapshotAgentTokens() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.searchAgentTokens
}

func optimizationCandidateID(spec *evolution.SkillSpec) (string, error) {
	payload, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal optimization candidate: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:8]), nil
}

func buildOptimizationEvaluation(
	item optimization.Case,
	candidate *evolution.SkillSpec,
	stats *runStats,
	runErr error,
	official *officialEval,
	evalErr error,
	artifactFeedback string,
	duration time.Duration,
	tokenBudget int,
) optimization.Evaluation {
	quality := officialQuality(official)
	passed := runErr == nil && evalErr == nil && official != nil && official.Passed
	tokens := 0
	toolCalls := 0
	loaded := false
	output := ""
	if stats != nil {
		tokens = stats.TotalTokens
		toolCalls = len(stats.ToolCalls)
		loaded = containsString(stats.LoadedSkillNames, candidate.Name)
		output = stats.FinalResponse
	}
	return optimization.Evaluation{
		CaseID:   item.ID,
		Score:    optimizationScore(quality, passed, tokens, tokenBudget),
		Output:   output,
		Feedback: optimizationFeedback(official, stats, runErr, evalErr, artifactFeedback),
		Trace:    optimizationTrace(stats, runErr, evalErr),
		Objectives: map[string]float64{
			objectiveQuality:     quality,
			objectivePassed:      boolFloat(passed),
			objectiveAgentTokens: float64(tokens),
			objectiveToolCalls:   float64(toolCalls),
			objectiveDuration:    duration.Seconds(),
			objectiveSkillLoaded: boolFloat(loaded),
		},
	}
}

func officialQuality(eval *officialEval) float64 {
	if eval == nil {
		return 0
	}
	return math.Max(0, math.Min(1, eval.Score.Percent/100))
}

func optimizationArtifactFeedback(
	task *taskDefinition,
	workspace string,
	official *officialEval,
) string {
	if task == nil || task.BaseTask != "recipe-cookbook-builder" ||
		!hasPartialScoreItem(official, "completeness") {
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

func hasPartialScoreItem(official *officialEval, name string) bool {
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

// optimizationScore preserves a hard pass/fail boundary. Official quality
// dominates successful candidates; token efficiency is only a tie-breaker.
func optimizationScore(quality float64, passed bool, tokens, tokenBudget int) float64 {
	quality = math.Max(0, math.Min(1, quality))
	if !passed {
		return 0.79 * quality
	}
	efficiency := 0.0
	if tokenBudget > 0 {
		efficiency = 1 - math.Min(1, float64(max(tokens, 0))/float64(tokenBudget))
	}
	return math.Min(1, 0.80+0.19*quality+0.01*efficiency)
}

func optimizationFeedback(
	official *officialEval,
	stats *runStats,
	runErr error,
	evalErr error,
	artifactFeedback string,
) string {
	parts := make([]string, 0, 6)
	if official != nil {
		parts = append(parts, fmt.Sprintf(
			"Official evaluator: status=%s passed=%t quality=%.3f.",
			official.Status,
			official.Passed,
			officialQuality(official),
		))
		if notes := strings.TrimSpace(joinScoreNotes(official)); notes != "" {
			parts = append(parts, "Evaluator findings: "+notes)
		}
	}
	if runErr != nil {
		parts = append(parts, "Agent run error: "+runErr.Error())
	}
	if evalErr != nil {
		parts = append(parts, "Evaluator runtime error: "+evalErr.Error())
	}
	if artifactFeedback = strings.TrimSpace(artifactFeedback); artifactFeedback != "" {
		parts = append(parts, artifactFeedback)
	}
	if stats != nil {
		parts = append(parts, fmt.Sprintf(
			"Efficiency: %d agent tokens, %d tool calls, claim_done=%t, skill_load=%t.",
			stats.TotalTokens,
			len(stats.ToolCalls),
			stats.ClaimDoneCalled,
			stats.SkillToolInvoked,
		))
		if repeated := repeatedToolCallSummary(stats.ToolCallSignatures); repeated != "" {
			parts = append(parts, "Repeated tool calls: "+repeated+". Reduce avoidable repetition without omitting required calls.")
		}
	}
	if len(parts) == 0 {
		return "No evaluator result was produced."
	}
	return strings.Join(parts, " ")
}

func optimizationTrace(stats *runStats, runErr, evalErr error) string {
	type traceRecord struct {
		ToolCalls       []string `json:"tool_calls,omitempty"`
		LoadedSkills    []string `json:"loaded_skills,omitempty"`
		ClaimDone       bool     `json:"claim_done"`
		AgentTokens     int      `json:"agent_tokens"`
		AgentError      string   `json:"agent_error,omitempty"`
		EvaluationError string   `json:"evaluation_error,omitempty"`
	}
	record := traceRecord{}
	if stats != nil {
		record.ToolCalls = append([]string(nil), stats.ToolCalls...)
		if len(record.ToolCalls) > 80 {
			record.ToolCalls = record.ToolCalls[:80]
		}
		record.LoadedSkills = append([]string(nil), stats.LoadedSkillNames...)
		record.ClaimDone = stats.ClaimDoneCalled
		record.AgentTokens = stats.TotalTokens
	}
	if runErr != nil {
		record.AgentError = runErr.Error()
	}
	if evalErr != nil {
		record.EvaluationError = evalErr.Error()
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return ""
	}
	return string(payload)
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

func repeatedToolCallSummary(signatures []string) string {
	counts := make(map[string]int)
	for _, signature := range signatures {
		counts[signature]++
	}
	type pair struct {
		name          string
		argumentSets  int
		maxRepetition int
		totalCalls    int
	}
	byName := make(map[string]*pair)
	for signature, count := range counts {
		if count > 1 {
			name, _, _ := strings.Cut(signature, "#")
			item := byName[name]
			if item == nil {
				item = &pair{name: name}
				byName[name] = item
			}
			item.argumentSets++
			item.maxRepetition = max(item.maxRepetition, count)
			item.totalCalls += count
		}
	}
	repeated := make([]pair, 0, len(byName))
	for _, item := range byName {
		repeated = append(repeated, *item)
	}
	sort.Slice(repeated, func(i, j int) bool {
		if repeated[i].totalCalls == repeated[j].totalCalls {
			return repeated[i].name < repeated[j].name
		}
		return repeated[i].totalCalls > repeated[j].totalCalls
	})
	if len(repeated) > 8 {
		repeated = repeated[:8]
	}
	parts := make([]string, 0, len(repeated))
	for _, item := range repeated {
		if item.argumentSets == 1 {
			parts = append(parts, fmt.Sprintf("%s x%d", item.name, item.maxRepetition))
			continue
		}
		parts = append(parts, fmt.Sprintf(
			"%s: %d exact argument sets repeated (%d calls)",
			item.name,
			item.argumentSets,
			item.totalCalls,
		))
	}
	return strings.Join(parts, ", ")
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func writeOptimizationJSON(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal optimization result: %w", err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("write optimization result: %w", err)
	}
	return nil
}

func writeOptimizationReport(path string, result *optimizationBenchmarkResult) error {
	if result == nil || result.Result == nil {
		return errors.New("nil optimization benchmark result")
	}
	var b strings.Builder
	b.WriteString("# SkillCraft Reflective Optimization Report\n\n")
	fmt.Fprintf(&b, "- Model: `%s`\n", result.Model)
	fmt.Fprintf(&b, "- Reflection model: `%s`\n", result.ReflectionModel)
	fmt.Fprintf(&b, "- Feedback scales: `%s`\n", strings.Join(result.FeedbackScales, ","))
	fmt.Fprintf(&b, "- Validation scales: `%s`\n", strings.Join(result.ValidationScales, ","))
	fmt.Fprintf(&b, "- Holdout scales: `%s`\n", strings.Join(result.HoldoutScales, ","))
	fmt.Fprintf(&b, "- Repeats per task: `%d`\n", result.Repeats)
	fmt.Fprintf(&b, "- Accepted candidates including seed: `%d`\n", result.Result.CandidateCount)
	fmt.Fprintf(&b, "- Evaluated cases: `%d`\n", result.Result.MetricCalls)
	fmt.Fprintf(&b, "- Search agent tokens: `%d`\n", result.SearchAgentTokens)
	fmt.Fprintf(&b, "- Reflection tokens: `%d`\n", result.ReflectionUsage.TotalTokens)
	fmt.Fprintf(&b, "- Selected skill differs from seed: `%t`\n", result.SelectedChanged)
	fmt.Fprintf(&b, "- Stop reason: `%s`\n", result.Result.StopReason)
	fmt.Fprintf(&b, "- Promotion eligible: `%t`\n", result.Result.PromotionEligible)
	fmt.Fprintf(&b, "- Promotion reason: `%s`\n", result.Result.PromotionReason)
	b.WriteString("\n## Paired Scores\n\n")
	b.WriteString("| Split | Seed | Optimized | Delta |\n")
	b.WriteString("|---|---:|---:|---:|\n")
	appendOptimizationScoreRow(&b, "Validation", result.Result.BaselineValidation, result.Result.CandidateValidation)
	appendOptimizationScoreRow(&b, "Holdout", result.Result.BaselineHoldout, result.Result.CandidateHoldout)
	b.WriteString("\n## Holdout Objectives\n\n")
	b.WriteString("| Objective | Preferred | Seed | Optimized | Delta |\n")
	b.WriteString("|---|---|---:|---:|---:|\n")
	keys := make([]string, 0, len(result.Result.BaselineHoldout.Objectives))
	for key := range result.Result.BaselineHoldout.Objectives {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		baseline := result.Result.BaselineHoldout.Objectives[key]
		candidate := result.Result.CandidateHoldout.Objectives[key]
		fmt.Fprintf(
			&b,
			"| `%s` | %s | %.4f | %.4f | %+.4f |\n",
			key,
			objectivePreference(key),
			baseline,
			candidate,
			candidate-baseline,
		)
	}
	b.WriteString("\nThe optimized skill is written under `optimized_skill/`. Full candidate lineage, paired seeds, evaluator feedback, and traces are stored under `optimization_experiments/`.\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write optimization report: %w", err)
	}
	return nil
}

func objectivePreference(name string) string {
	switch name {
	case objectiveAgentTokens, objectiveToolCalls, objectiveDuration:
		return "lower"
	default:
		return "higher"
	}
}

func appendOptimizationScoreRow(
	b *strings.Builder,
	name string,
	baseline optimization.Summary,
	candidate optimization.Summary,
) {
	fmt.Fprintf(
		b,
		"| %s | %.4f | %.4f | %+.4f |\n",
		name,
		baseline.Score,
		candidate.Score,
		candidate.Score-baseline.Score,
	)
}
