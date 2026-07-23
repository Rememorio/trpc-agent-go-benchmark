//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	framework "trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
)

const (
	objectiveQuality     = "official_quality"
	objectivePassed      = "passed"
	objectiveAgentTokens = "agent_tokens"
	objectiveToolCalls   = "tool_calls"
	objectiveDuration    = "duration_seconds"
	objectiveSkillLoaded = "skill_loaded"
)

// AgentResult is the bounded runtime evidence needed for search feedback.
type AgentResult struct {
	TotalTokens        int
	ToolCalls          []string
	ToolCallSignatures []string
	LoadedSkillNames   []string
	ClaimDoneCalled    bool
	SkillToolInvoked   bool
	FinalResponse      string
}

// OfficialResult is the normalized public result returned by the benchmark
// evaluator. Quality must be in [0,1].
type OfficialResult struct {
	Passed   bool
	Status   string
	Quality  float64
	Findings string
}

// CaseResult combines one agent run with its official evaluation. Agent and
// evaluation errors are scored as case failures rather than aborting a batch.
type CaseResult struct {
	Agent              *AgentResult
	AgentError         error
	Official           *OfficialResult
	EvaluationError    error
	AdditionalFeedback string
	Duration           time.Duration
}

// Evaluator adapts batch execution to the framework optimization contract.
type Evaluator struct {
	tokenBudget int
	run         func(
		context.Context,
		*evolution.SkillSpec,
		[]framework.Case,
		int64,
	) ([]CaseResult, error)

	mu          sync.Mutex
	agentTokens int
}

// NewEvaluator creates an evaluator around the benchmark runtime callback.
func NewEvaluator(
	tokenBudget int,
	run func(
		context.Context,
		*evolution.SkillSpec,
		[]framework.Case,
		int64,
	) ([]CaseResult, error),
) *Evaluator {
	return &Evaluator{tokenBudget: tokenBudget, run: run}
}

// Evaluate implements the framework evaluator contract.
func (e *Evaluator) Evaluate(
	ctx context.Context,
	candidate *evolution.SkillSpec,
	cases []framework.Case,
	seed int64,
) ([]framework.Evaluation, error) {
	if candidate == nil {
		return nil, errors.New("nil candidate")
	}
	if e.run == nil {
		return nil, errors.New("nil case runner")
	}
	results, err := e.run(ctx, candidate, cases, seed)
	if err != nil {
		return nil, err
	}
	if len(results) != len(cases) {
		return nil, fmt.Errorf("case runner returned %d results for %d cases", len(results), len(cases))
	}
	evaluations := make([]framework.Evaluation, 0, len(cases))
	for i, item := range cases {
		result := results[i]
		e.addAgentTokens(result.Agent)
		evaluations = append(evaluations, buildEvaluation(
			item,
			candidate,
			result,
			e.tokenBudget,
		))
	}
	return evaluations, nil
}

// AgentTokens returns agent tokens observed across all evaluated cases.
func (e *Evaluator) AgentTokens() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.agentTokens
}

func (e *Evaluator) addAgentTokens(result *AgentResult) {
	if result == nil {
		return
	}
	e.mu.Lock()
	e.agentTokens += result.TotalTokens
	e.mu.Unlock()
}

func buildEvaluation(
	item framework.Case,
	candidate *evolution.SkillSpec,
	result CaseResult,
	tokenBudget int,
) framework.Evaluation {
	quality := officialQuality(result.Official)
	passed := result.AgentError == nil && result.EvaluationError == nil &&
		result.Official != nil && result.Official.Passed
	tokens := 0
	toolCalls := 0
	loaded := false
	output := ""
	if result.Agent != nil {
		tokens = result.Agent.TotalTokens
		toolCalls = len(result.Agent.ToolCalls)
		loaded = contains(result.Agent.LoadedSkillNames, candidate.Name)
		output = result.Agent.FinalResponse
	}
	return framework.Evaluation{
		CaseID:   item.ID,
		Score:    score(quality, passed, tokens, tokenBudget),
		Output:   output,
		Feedback: feedback(result),
		Trace:    trace(result),
		Objectives: map[string]float64{
			objectiveQuality:     quality,
			objectivePassed:      boolFloat(passed),
			objectiveAgentTokens: float64(tokens),
			objectiveToolCalls:   float64(toolCalls),
			objectiveDuration:    result.Duration.Seconds(),
			objectiveSkillLoaded: boolFloat(loaded),
		},
	}
}

func officialQuality(result *OfficialResult) float64 {
	if result == nil {
		return 0
	}
	return math.Max(0, math.Min(1, result.Quality))
}

// score preserves a hard pass/fail boundary. Official quality dominates
// successful candidates; token efficiency is only a tie-breaker.
func score(quality float64, passed bool, tokens, tokenBudget int) float64 {
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

func feedback(result CaseResult) string {
	parts := make([]string, 0, 6)
	if result.Official != nil {
		parts = append(parts, fmt.Sprintf(
			"Official evaluator: status=%s passed=%t quality=%.3f.",
			result.Official.Status,
			result.Official.Passed,
			officialQuality(result.Official),
		))
		if findings := strings.TrimSpace(result.Official.Findings); findings != "" {
			parts = append(parts, "Evaluator findings: "+findings)
		}
	}
	if result.AgentError != nil {
		parts = append(parts, "Agent run error: "+result.AgentError.Error())
	}
	if result.EvaluationError != nil {
		parts = append(parts, "Evaluator runtime error: "+result.EvaluationError.Error())
	}
	if additional := strings.TrimSpace(result.AdditionalFeedback); additional != "" {
		parts = append(parts, additional)
	}
	if result.Agent != nil {
		parts = append(parts, fmt.Sprintf(
			"Efficiency: %d agent tokens, %d tool calls, claim_done=%t, skill_load=%t.",
			result.Agent.TotalTokens,
			len(result.Agent.ToolCalls),
			result.Agent.ClaimDoneCalled,
			result.Agent.SkillToolInvoked,
		))
		if repeated := repeatedCalls(result.Agent.ToolCallSignatures); repeated != "" {
			parts = append(parts, "Repeated tool calls: "+repeated+". Reduce avoidable repetition without omitting required calls.")
		}
	}
	if len(parts) == 0 {
		return "No evaluator result was produced."
	}
	return strings.Join(parts, " ")
}

func trace(result CaseResult) string {
	type record struct {
		ToolCalls       []string `json:"tool_calls,omitempty"`
		LoadedSkills    []string `json:"loaded_skills,omitempty"`
		ClaimDone       bool     `json:"claim_done"`
		AgentTokens     int      `json:"agent_tokens"`
		AgentError      string   `json:"agent_error,omitempty"`
		EvaluationError string   `json:"evaluation_error,omitempty"`
	}
	item := record{}
	if result.Agent != nil {
		item.ToolCalls = append([]string(nil), result.Agent.ToolCalls...)
		if len(item.ToolCalls) > 80 {
			item.ToolCalls = item.ToolCalls[:80]
		}
		item.LoadedSkills = append([]string(nil), result.Agent.LoadedSkillNames...)
		item.ClaimDone = result.Agent.ClaimDoneCalled
		item.AgentTokens = result.Agent.TotalTokens
	}
	if result.AgentError != nil {
		item.AgentError = result.AgentError.Error()
	}
	if result.EvaluationError != nil {
		item.EvaluationError = result.EvaluationError.Error()
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return ""
	}
	return string(payload)
}

func repeatedCalls(signatures []string) string {
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
		if count <= 1 {
			continue
		}
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

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
