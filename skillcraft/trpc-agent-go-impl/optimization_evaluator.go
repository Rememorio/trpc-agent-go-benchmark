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
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

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

func newSkillCraftOptimizationEvaluator(
	cfg *benchmarkConfig,
	tasks []*taskDefinition,
	tokenBudget int,
) *skillCraftOptimizationEvaluator {
	tasksByID := make(map[string]*taskDefinition, len(tasks))
	for _, task := range tasks {
		tasksByID[task.ID] = task
	}
	return &skillCraftOptimizationEvaluator{
		cfg:         cfg,
		tasks:       tasksByID,
		outputRoot:  cfg.OutputDir,
		tokenBudget: tokenBudget,
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
