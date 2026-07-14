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
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
	framework "trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
)

func TestEvaluator(t *testing.T) {
	candidate := testSpec()
	evaluator := NewEvaluator(1000, func(
		ctx context.Context,
		got *evolution.SkillSpec,
		cases []framework.Case,
		seed int64,
	) ([]CaseResult, error) {
		require.NoError(t, ctx.Err())
		require.Same(t, candidate, got)
		require.Equal(t, int64(7), seed)
		require.Len(t, cases, 1)
		return []CaseResult{{
			Agent: &AgentResult{
				TotalTokens:        100,
				ToolCalls:          []string{"weather_get", "weather_get"},
				ToolCallSignatures: []string{"weather_get#same", "weather_get#same"},
				LoadedSkillNames:   []string{"Weather"},
				ClaimDoneCalled:    true,
				SkillToolInvoked:   true,
				FinalResponse:      "done",
			},
			Official: &OfficialResult{Passed: true, Status: "pass", Quality: 0.95},
		}}, nil
	})
	evaluations, err := evaluator.Evaluate(context.Background(), candidate, []framework.Case{{
		ID: "case-1",
	}}, 7)
	require.NoError(t, err)
	require.Len(t, evaluations, 1)
	require.Equal(t, "done", evaluations[0].Output)
	require.Greater(t, evaluations[0].Score, 0.8)
	require.Equal(t, 0.95, evaluations[0].Objectives[objectiveQuality])
	require.Equal(t, 1.0, evaluations[0].Objectives[objectivePassed])
	require.Equal(t, 1.0, evaluations[0].Objectives[objectiveSkillLoaded])
	require.Contains(t, evaluations[0].Feedback, "Repeated tool calls")
	require.Equal(t, 100, evaluator.AgentTokens())
}

func TestEvaluatorFailurePaths(t *testing.T) {
	_, err := NewEvaluator(1000, nil).Evaluate(
		context.Background(), testSpec(), nil, 7,
	)
	require.ErrorContains(t, err, "nil case runner")

	evaluator := NewEvaluator(1000, func(
		context.Context,
		*evolution.SkillSpec,
		[]framework.Case,
		int64,
	) ([]CaseResult, error) {
		return nil, errors.New("runtime failed")
	})
	_, err = evaluator.Evaluate(context.Background(), testSpec(), nil, 7)
	require.ErrorContains(t, err, "runtime failed")
	_, err = evaluator.Evaluate(context.Background(), nil, nil, 7)
	require.ErrorContains(t, err, "nil candidate")

	evaluator = NewEvaluator(1000, func(
		context.Context,
		*evolution.SkillSpec,
		[]framework.Case,
		int64,
	) ([]CaseResult, error) {
		return nil, nil
	})
	_, err = evaluator.Evaluate(context.Background(), testSpec(), []framework.Case{{ID: "one"}}, 7)
	require.ErrorContains(t, err, "returned 0 results for 1 cases")
}

func TestBuildEvaluationTreatsErrorsAsFailure(t *testing.T) {
	evaluation := buildEvaluation(
		framework.Case{ID: "case-1"},
		testSpec(),
		CaseResult{
			Agent:           &AgentResult{},
			AgentError:      errors.New("max tool iterations exceeded"),
			Official:        &OfficialResult{Passed: true, Status: "pass", Quality: 1},
			EvaluationError: errors.New("evaluator failed"),
		},
		1000,
	)
	require.Less(t, evaluation.Score, 0.8)
	require.Zero(t, evaluation.Objectives[objectivePassed])
	require.Contains(t, evaluation.Feedback, "max tool iterations")
	require.Contains(t, evaluation.Feedback, "evaluator failed")
	require.Contains(t, evaluation.Trace, `"agent_error":"max tool iterations exceeded"`)
}

func TestScorePreservesPassBoundary(t *testing.T) {
	require.Less(t, score(1, false, 0, 1000), score(0, true, 1000, 1000))
	require.Greater(t, score(1, true, 100, 1000), score(1, true, 900, 1000))
	require.InDelta(t, 0, score(0, false, 0, 1000), 0.0001)
	require.LessOrEqual(t, score(1, true, 0, 1000), 1.0)
	require.Equal(t, 1.0, officialQuality(&OfficialResult{Quality: 2}))
	require.Zero(t, officialQuality(nil))
}

func TestFeedbackAndTrace(t *testing.T) {
	result := CaseResult{
		Agent: &AgentResult{
			TotalTokens:        5,
			ToolCallSignatures: []string{"a#1", "a#1"},
		},
		AgentError:         errors.New("agent"),
		EvaluationError:    errors.New("evaluator"),
		AdditionalFeedback: "artifact",
		Official: &OfficialResult{
			Status:   "partial",
			Quality:  0.5,
			Findings: "missing fields",
		},
	}
	text := feedback(result)
	require.Contains(t, text, "Agent run error: agent")
	require.Contains(t, text, "Evaluator runtime error: evaluator")
	require.Contains(t, text, "artifact")
	require.Contains(t, text, "missing fields")
	require.Contains(t, trace(result), `"evaluation_error":"evaluator"`)
	require.Equal(t, "No evaluator result was produced.", feedback(CaseResult{}))

	signatures := make([]string, 0, 20)
	for index := 0; index < 10; index++ {
		signature := string(rune('a'+index)) + "#same"
		signatures = append(signatures, signature, signature)
	}
	require.Len(t, strings.Split(repeatedCalls(signatures), ", "), 8)
	require.Equal(t, "weather x2", repeatedCalls([]string{"weather#one", "weather#one"}))
	require.Equal(t,
		"weather: 2 exact argument sets repeated (4 calls)",
		repeatedCalls([]string{"weather#one", "weather#one", "weather#two", "weather#two"}),
	)
}

func testSpec() *evolution.SkillSpec {
	return &evolution.SkillSpec{
		Name:        "Weather",
		Description: "Collect weather data.",
		WhenToUse:   "Use for weather tasks.",
		Steps:       []string{"Collect the data.", "Write the result."},
	}
}
